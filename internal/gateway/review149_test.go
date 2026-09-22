package gateway

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"yzapi/internal/model"
	"yzapi/internal/pricing"
)

// R149-01: image replies keep their cached-input detail under both vocabularies, so
// cached input is priced at the cached rate. Test prices are arbitrary (per million).
func TestR149ImageCachedTokensPriced(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage string
	}{
		{"openai names", `{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25,"prompt_tokens_details":{"cached_tokens":12}}`},
		{"gpt-image names", `{"input_tokens":20,"output_tokens":5,"total_tokens":25,"input_tokens_details":{"cached_tokens":12,"text_tokens":8}}`},
		{"both names: openai pair and its details win", `{"prompt_tokens":20,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":12},"input_tokens":99,"output_tokens":99,"input_tokens_details":{"cached_tokens":99}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := newFormUpstream(`{"data":[{"b64_json":"AAAA"}],"usage":` + tc.usage + `}`)
			defer up.srv.Close()
			e := newE2EAccounts(t, imageSpec(up.srv.URL))
			price := model.ModelPrice{Pattern: "gpt-image-2.5", Provider: "openai", Currency: "USD", InputPerM: 10, OutputPerM: 20, CachedInputPerM: 1, Enabled: true}
			if err := e.db.Create(&price).Error; err != nil {
				t.Fatal(err)
			}
			svc, err := pricing.New(e.db, e.g.settings)
			if err != nil {
				t.Fatal(err)
			}
			e.g.SetPricer(svc)
			r := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"img","prompt":"p"}`))
			r.Header.Set("Authorization", "Bearer "+e.key)
			w := httptest.NewRecorder()
			e.g.HandleImages(w, r)
			if w.Code != 200 {
				t.Fatalf("status %d %s", w.Code, w.Body.String())
			}
			log, atts := e.callLog(t)
			// (20-12)*10 + 12*1 + 5*20 = 192 micro-USD
			if log.PromptTokens != 20 || log.CompletionTokens != 5 || log.CachedTokens != 12 || log.CostMicros != 192 || !log.CostKnown {
				t.Fatalf("log prompt=%d completion=%d cached=%d cost=%d known=%v", log.PromptTokens, log.CompletionTokens, log.CachedTokens, log.CostMicros, log.CostKnown)
			}
			if len(atts) != 1 || atts[0].CachedTokens != 12 || atts[0].CostMicros != 192 {
				t.Fatalf("attempts %+v", atts)
			}
		})
	}
}

// R149-02: parts are forwarded as raw wire bytes. A quoted-printable part keeps its
// encoded body and its Content-Transfer-Encoding header; duplicate non-model fields keep
// order, whitespace and content; extra headers survive; duplicate model fields collapse.
func TestR149MultipartRawPartsPreserved(t *testing.T) {
	for _, encoding := range []string{"binary", "quoted-printable"} {
		t.Run(encoding, func(t *testing.T) {
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			_ = mw.WriteField("model", "img")
			_ = mw.WriteField("model", "ignored")
			_ = mw.WriteField("prompt", " first \n")
			_ = mw.WriteField("prompt", "第二段")
			h := textproto.MIMEHeader{"Content-Disposition": {`form-data; name="image[]"; filename="a.png"`}, "Content-Type": {"image/png"}, "Content-Transfer-Encoding": {encoding}, "X-Custom": {"one", "two"}}
			pw, err := mw.CreatePart(h)
			if err != nil {
				t.Fatal(err)
			}
			data := []byte("abc=3Ddef\r\n")
			_, _ = pw.Write(data)
			_ = mw.Close()

			type rawPart struct {
				header textproto.MIMEHeader
				data   []byte
			}
			var got []rawPart
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				mr := multipart.NewReader(r.Body, params["boundary"])
				for {
					p, err := mr.NextRawPart()
					if err != nil {
						break
					}
					b, _ := io.ReadAll(p)
					got = append(got, rawPart{p.Header, b})
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"url":"u"}]}`)
			}))
			defer up.Close()
			e := newE2EAccounts(t, imageSpec(up.URL))
			r := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &buf)
			r.Header.Set("Authorization", "Bearer "+e.key)
			r.Header.Set("Content-Type", mw.FormDataContentType())
			w := httptest.NewRecorder()
			e.g.HandleImageEdits(w, r)
			if w.Code != 200 {
				t.Fatalf("status %d %s", w.Code, w.Body.String())
			}
			// model (rewritten, once), prompt, prompt, image[]
			if len(got) != 4 {
				t.Fatalf("got %d parts", len(got))
			}
			if string(got[0].data) != "gpt-image-2.5" || !strings.Contains(got[0].header.Get("Content-Disposition"), `name="model"`) {
				t.Fatalf("model part %q %v", got[0].data, got[0].header)
			}
			if string(got[1].data) != " first \n" || string(got[2].data) != "第二段" {
				t.Fatalf("prompt parts %q %q", got[1].data, got[2].data)
			}
			img := got[3]
			if !bytes.Equal(img.data, data) {
				t.Fatalf("%s: image bytes %q want %q", encoding, img.data, data)
			}
			if img.header.Get("Content-Transfer-Encoding") != encoding || img.header.Get("Content-Type") != "image/png" ||
				!strings.Contains(img.header.Get("Content-Disposition"), `filename="a.png"`) || len(img.header["X-Custom"]) != 2 {
				t.Fatalf("%s: image headers %v", encoding, img.header)
			}
		})
	}
}
