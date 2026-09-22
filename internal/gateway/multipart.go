package gateway

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
)

// multipartBody is a parsed multipart/form-data request (image edits / variations):
// every part is kept byte-for-byte so it can be re-encoded toward the upstream with
// only the "model" field replaced.
type multipartBody struct {
	parts          []formPart
	outContentType string // media type of the last encode() result (new boundary)
}

type formPart struct {
	name     string
	filename string
	header   textproto.MIMEHeader // original part headers (Content-Type etc.)
	data     []byte
}

func isMultipart(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && mt == "multipart/form-data"
}

// parseMultipart reads the whole form. The body is already bounded by the request
// body limit, so parts are held in memory.
func parseMultipart(body []byte, contentType string) (*multipartBody, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, errors.New("missing boundary")
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	f := &multipartBody{}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(p)
		if err != nil {
			return nil, err
		}
		f.parts = append(f.parts, formPart{name: p.FormName(), filename: p.FileName(), header: p.Header, data: data})
	}
	if len(f.parts) == 0 {
		return nil, errors.New("empty form")
	}
	return f, nil
}

// field returns the first non-file part with the given name, trimmed.
func (f *multipartBody) field(name string) string {
	for _, p := range f.parts {
		if p.name == name && p.filename == "" {
			return strings.TrimSpace(string(p.data))
		}
	}
	return ""
}

// encode re-serialises the form with a fresh boundary. The "model" field is written
// with the upstream name (or appended when the client omitted it); every other part
// keeps its name, filename, headers and bytes.
func (f *multipartBody) encode(upstreamModel string) ([]byte, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	wroteModel := false
	for _, p := range f.parts {
		if p.name == "model" && p.filename == "" {
			if wroteModel {
				continue // duplicate model fields collapse into the rewritten one
			}
			if err := w.WriteField("model", upstreamModel); err != nil {
				return nil, err
			}
			wroteModel = true
			continue
		}
		h := textproto.MIMEHeader{}
		for k, v := range p.header {
			h[k] = append([]string(nil), v...)
		}
		if h.Get("Content-Disposition") == "" {
			cd := `form-data; name="` + escapeQuotes(p.name) + `"`
			if p.filename != "" {
				cd += `; filename="` + escapeQuotes(p.filename) + `"`
			}
			h.Set("Content-Disposition", cd)
		}
		pw, err := w.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if _, err := pw.Write(p.data); err != nil {
			return nil, err
		}
	}
	if !wroteModel {
		if err := w.WriteField("model", upstreamModel); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	f.outContentType = w.FormDataContentType()
	return buf.Bytes(), nil
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string { return quoteEscaper.Replace(s) }
