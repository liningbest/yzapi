package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yzapi/internal/model"
)

func imageSpec(url string) acctSpec {
	return acctSpec{URL: url, Provider: "openai", Type: model.TypeImage, Protocols: []string{model.ProtoOpenAIImages},
		Mappings: map[string]string{"img": "gpt-image-2.5"}}
}

// formUpstream records the raw multipart form an upstream received.
type formUpstream struct {
	srv    *httptest.Server
	path   string
	ctype  string
	model  string
	fields map[string]string
	files  map[string][]formFile
	raw    []byte
}

type formFile struct {
	name, ctype string
	data        []byte
}

func newFormUpstream(reply string) *formUpstream {
	u := &formUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.path, u.ctype = r.URL.Path, r.Header.Get("Content-Type")
		u.raw, _ = io.ReadAll(r.Body)
		u.fields, u.files = map[string]string{}, map[string][]formFile{}
		if mt, params, _ := mime.ParseMediaType(u.ctype); mt == "multipart/form-data" {
			mr := multipart.NewReader(bytes.NewReader(u.raw), params["boundary"])
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				data, _ := io.ReadAll(p)
				if p.FileName() == "" {
					u.fields[p.FormName()] = string(data)
				} else {
					u.files[p.FormName()] = append(u.files[p.FormName()], formFile{p.FileName(), p.Header.Get("Content-Type"), data})
				}
			}
			u.model = u.fields["model"]
		} else {
			var m map[string]any
			_ = json.Unmarshal(u.raw, &m)
			u.model, _ = m["model"].(string)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
	return u
}

func buildForm(t *testing.T, fields map[string]string, files map[string][]formFile) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	for name, fs := range files {
		for _, f := range fs {
			h := make(map[string][]string)
			h["Content-Disposition"] = []string{`form-data; name="` + name + `"; filename="` + f.name + `"`}
			h["Content-Type"] = []string{f.ctype}
			pw, _ := w.CreatePart(h)
			_, _ = pw.Write(f.data)
		}
	}
	_ = w.Close()
	return &buf, w.FormDataContentType()
}

const imageOK = `{"created":1,"data":[{"b64_json":"AAAA"}],"usage":{"input_tokens":20,"output_tokens":5,"input_tokens_details":{"text_tokens":8,"image_tokens":12}}}`

// One image account and one mapping serve generations, edits and variations. A
// multipart edit reaches the upstream at /images/edits with every field and file
// intact (names, filenames, content types, bytes) and only "model" rewritten.
func TestImageEditsMultipartForwardedVerbatim(t *testing.T) {
	up := newFormUpstream(imageOK)
	defer up.srv.Close()
	e := newE2EAccounts(t, imageSpec(up.srv.URL))
	png := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3}
	files := map[string][]formFile{
		"image[]": {{"a.png", "image/png", png}, {"b.png", "image/png", append(png, 9)}},
		"mask":    {{"mask.png", "image/png", []byte("mask-bytes")}},
	}
	body, ct := buildForm(t, map[string]string{"model": "img", "prompt": "make it night", "n": "2", "size": "1024x1024", "quality": "high"}, files)
	r := httptest.NewRequest(http.MethodPost, "/v1/images/edits", body)
	r.Header.Set("Authorization", "Bearer "+e.key)
	r.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	e.g.HandleImageEdits(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"b64_json"`) {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if up.path != "/images/edits" || !strings.HasPrefix(up.ctype, "multipart/form-data; boundary=") {
		t.Fatalf("upstream path=%q ctype=%q", up.path, up.ctype)
	}
	if up.model != "gpt-image-2.5" {
		t.Fatalf("model %q", up.model)
	}
	for k, v := range map[string]string{"prompt": "make it night", "n": "2", "size": "1024x1024", "quality": "high"} {
		if up.fields[k] != v {
			t.Fatalf("field %s=%q want %q", k, up.fields[k], v)
		}
	}
	if len(up.files["image[]"]) != 2 || up.files["image[]"][0].name != "a.png" || up.files["image[]"][1].name != "b.png" ||
		up.files["image[]"][0].ctype != "image/png" || !bytes.Equal(up.files["image[]"][0].data, png) || !bytes.Equal(up.files["image[]"][1].data, append(png, 9)) {
		t.Fatalf("image parts %+v", up.files["image[]"])
	}
	if len(up.files["mask"]) != 1 || string(up.files["mask"][0].data) != "mask-bytes" {
		t.Fatalf("mask parts %+v", up.files["mask"])
	}
	log, atts := e.callLog(t)
	if log.APIType != model.TypeImage || log.ClientProtocol != model.ProtoOpenAIImages || log.RequestModel != "img" || log.UpstreamModel != "gpt-image-2.5" ||
		log.PromptTokens != 20 || log.CompletionTokens != 5 || log.UsageStatus != model.UsageConfirmed || log.Result != "success" {
		t.Fatalf("log %+v", log)
	}
	if len(atts) != 1 || atts[0].Model != "gpt-image-2.5" {
		t.Fatalf("attempts %+v", atts)
	}
}

// Variations, generations and a JSON edit all ride the same account: each reaches
// its own upstream path; JSON bodies keep the JSON content type; generations usage is metered.
func TestImageOperationsShareOneAccount(t *testing.T) {
	up := newFormUpstream(imageOK)
	defer up.srv.Close()
	e := newE2EAccounts(t, imageSpec(up.srv.URL))
	body, ct := buildForm(t, map[string]string{"model": "img", "n": "1"}, map[string][]formFile{"image": {{"a.png", "image/png", []byte("x")}}})
	r := httptest.NewRequest(http.MethodPost, "/v1/images/variations", body)
	r.Header.Set("Authorization", "Bearer "+e.key)
	r.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	e.g.HandleImageVariations(w, r)
	if w.Code != 200 || up.path != "/images/variations" || up.model != "gpt-image-2.5" || len(up.files["image"]) != 1 {
		t.Fatalf("variations: %d path=%q model=%q files=%v", w.Code, up.path, up.model, up.files)
	}
	r = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(`{"model":"img","prompt":"p","image":"https://example.com/a.png","x_future":1}`))
	r.Header.Set("Authorization", "Bearer "+e.key)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	e.g.HandleImageEdits(w, r)
	if w.Code != 200 || up.path != "/images/edits" || up.ctype != "application/json" || up.model != "gpt-image-2.5" || !strings.Contains(string(up.raw), `"x_future":1`) {
		t.Fatalf("json edit: %d path=%q ctype=%q model=%q raw=%s", w.Code, up.path, up.ctype, up.model, up.raw)
	}
	r = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"img","prompt":"p"}`))
	r.Header.Set("Authorization", "Bearer "+e.key)
	w = httptest.NewRecorder()
	e.g.HandleImages(w, r)
	if w.Code != 200 || up.path != "/images/generations" {
		t.Fatalf("generations: %d path=%q", w.Code, up.path)
	}
	log, _ := e.lastLog(t, 3)
	if log.PromptTokens != 20 || log.CompletionTokens != 5 || log.UsageStatus != model.UsageConfirmed {
		t.Fatalf("generations usage %+v", log)
	}
}

// Guards: a form without "model" is 400 missing_model, a broken multipart body is 400
// invalid_body, a text model on an image path is 400 model_type_mismatch, an image reply
// without usage is metered unknown, and a body over the limit is 413.
func TestImageEditsGuards(t *testing.T) {
	up := newFormUpstream(`{"created":1,"data":[{"url":"https://example.com/x.png"}]}`)
	defer up.srv.Close()
	e := newE2EAccounts(t, imageSpec(up.srv.URL), chatSpec(up.srv.URL))
	post := func(path, ct string, body io.Reader) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, body)
		r.Header.Set("Authorization", "Bearer "+e.key)
		r.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		e.g.HandleImageEdits(w, r)
		return w
	}
	body, ct := buildForm(t, map[string]string{"prompt": "p"}, map[string][]formFile{"image": {{"a.png", "image/png", []byte("x")}}})
	if w := post("/v1/images/edits", ct, body); w.Code != 400 || !strings.Contains(w.Body.String(), "missing_model") {
		t.Fatalf("no model: %d %s", w.Code, w.Body.String())
	}
	if w := post("/v1/images/edits", "multipart/form-data; boundary=zzz", strings.NewReader("--zzz\r\nnot a valid part")); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_body") {
		t.Fatalf("broken form: %d %s", w.Code, w.Body.String())
	}
	body, ct = buildForm(t, map[string]string{"model": "m"}, map[string][]formFile{"image": {{"a.png", "image/png", []byte("x")}}})
	if w := post("/v1/images/edits", ct, body); w.Code != 400 || !strings.Contains(w.Body.String(), "model_type_mismatch") {
		t.Fatalf("text model: %d %s", w.Code, w.Body.String())
	}
	body, ct = buildForm(t, map[string]string{"model": "img"}, map[string][]formFile{"image": {{"a.png", "image/png", []byte("x")}}})
	if w := post("/v1/images/edits", ct, body); w.Code != 200 {
		t.Fatalf("ok edit: %d %s", w.Code, w.Body.String())
	}
	log, _ := e.lastLog(t, 4) // the three refused requests above are logged too
	if log.Result != "success" || log.UsageStatus != model.UsageUnknown {
		t.Fatalf("no-usage reply: %+v", log)
	}
	if n := len(up.files); n == 0 {
		t.Fatal("upstream saw no files")
	}
	// Over the configured body limit.
	perf := e.g.settings.Get().Performance
	perf.MaxBodyKB = 1
	if err := e.g.settings.SetPerformance(perf); err != nil {
		t.Fatal(err)
	}
	big, ct := buildForm(t, map[string]string{"model": "img"}, map[string][]formFile{"image": {{"a.png", "image/png", bytes.Repeat([]byte("x"), 4096)}}})
	if w := post("/v1/images/edits", ct, big); w.Code != 413 {
		t.Fatalf("oversize: %d %s", w.Code, w.Body.String())
	}
}
