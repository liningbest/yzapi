package gateway

import (
	"encoding/json"
	"net/http"
	"strings"

	"yzapi/internal/model"
)

// HandleGemini serves the Google Gemini REST surface under /v1beta (and /v1):
//
//	GET  /v1beta/models                              list models (Gemini shape)
//	GET  /v1beta/models/{model}                      describe one model
//	POST /v1beta/models/{model}:generateContent      one-shot generation
//	POST /v1beta/models/{model}:streamGenerateContent?alt=sse   streaming generation (SSE)
//	POST /v1beta/models/{model}:countTokens          local estimate
//
// Auth accepts x-goog-api-key, ?key=, Authorization: Bearer and x-api-key.
func (g *Gateway) HandleGemini(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	for _, pfx := range []string{"/v1beta/", "/v1/"} {
		if strings.HasPrefix(path, pfx) {
			path = strings.TrimPrefix(path, pfx)
			break
		}
	}
	path = strings.TrimSuffix(path, "/")
	switch {
	case path == "models":
		if r.Method != http.MethodGet {
			writeErrorProto(w, model.ProtoGemini, newErr(404, "not_found", "Unknown Gemini endpoint"))
			return
		}
		g.handleGeminiModels(w, r)
		return
	case strings.HasPrefix(path, "models/"):
		rest := strings.TrimPrefix(path, "models/")
		name, action, _ := strings.Cut(rest, ":")
		name = strings.TrimPrefix(name, "models/")
		switch {
		case action == "" && r.Method == http.MethodGet:
			g.handleGeminiModel(w, r, name)
		case action == "generateContent" && r.Method == http.MethodPost:
			g.handleGeminiGenerate(w, r, name, false)
		case action == "streamGenerateContent" && r.Method == http.MethodPost:
			g.handleGeminiGenerate(w, r, name, true)
		case action == "countTokens" && r.Method == http.MethodPost:
			g.handleGeminiCountTokens(w, r, name)
		default:
			writeErrorProto(w, model.ProtoGemini, newErr(404, "not_found", "Unknown Gemini endpoint '"+r.URL.Path+"'"))
		}
		return
	}
	writeErrorProto(w, model.ProtoGemini, newErr(404, "not_found", "Unknown Gemini endpoint '"+r.URL.Path+"'"))
}

func (g *Gateway) handleGeminiGenerate(w http.ResponseWriter, r *http.Request, name string, stream bool) {
	if name == "" {
		writeErrorProto(w, model.ProtoGemini, newErr(400, "missing_model", "The model name is required in the URL"))
		return
	}
	g.handleTextWith(w, r, model.ProtoGemini, func(req *request) {
		req.pathModel = name
		req.pathStream = stream
	})
}

func geminiModelEntry(name string, mi ModelInfo) map[string]any {
	methods := []string{"generateContent", "streamGenerateContent", "countTokens"}
	if mi.Type == model.TypeEmbedding {
		methods = []string{"embedContent"}
	}
	return map[string]any{
		"name":                       "models/" + name,
		"displayName":                name,
		"description":                "Served by yzapi",
		"version":                    "001",
		"inputTokenLimit":            1048576,
		"outputTokenLimit":           65536,
		"supportedGenerationMethods": methods,
	}
}

func (g *Gateway) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	p, e := g.authenticate(r)
	if e != nil {
		writeErrorProto(w, model.ProtoGemini, e)
		return
	}
	snap := g.snap.get()
	grp := snap.Groups[p.GroupID]
	if grp == nil || !grp.Enabled {
		grp = snap.DefaultGroup
	}
	out := []map[string]any{}
	for _, mi := range snap.Models {
		if grp != nil && grp.Allowed != nil && !grp.Allowed[mi.Name] {
			if mi.Kind != "group" || !grp.ModelGroupNames[mi.Name] {
				continue
			}
		}
		if mi.Type != model.TypeText && mi.Type != model.TypeEmbedding {
			continue // images and custom JSON endpoints have no Gemini surface
		}
		out = append(out, geminiModelEntry(mi.Name, mi))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"models": out})
}

func (g *Gateway) handleGeminiModel(w http.ResponseWriter, r *http.Request, name string) {
	p, e := g.authenticate(r)
	if e != nil {
		writeErrorProto(w, model.ProtoGemini, e)
		return
	}
	snap := g.snap.get()
	grp := snap.Groups[p.GroupID]
	if grp == nil || !grp.Enabled {
		grp = snap.DefaultGroup
	}
	canonical, typ, st := snap.ResolveDetail(name, "")
	if st == ResolveAmbiguous {
		writeErrorProto(w, model.ProtoGemini, newErr(400, "model_ambiguous", "Model '"+name+"' matches more than one configured model"))
		return
	}
	if st != ResolveFound || (grp != nil && grp.Allowed != nil && !grp.Allowed[canonical] && !grp.ModelGroupNames[canonical]) {
		writeErrorProto(w, model.ProtoGemini, ErrModelNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(geminiModelEntry(canonical, ModelInfo{Name: canonical, Type: typ}))
}

// handleGeminiCountTokens answers countTokens with a local estimate after the same auth,
// model and limit checks as a generation request; the request never reaches an upstream.
func (g *Gateway) handleGeminiCountTokens(w http.ResponseWriter, r *http.Request, name string) {
	req := g.newRequest(w, r, model.ProtoGemini)
	req.pathModel = name
	defer func() {
		if req.reserved > 0 {
			g.bodyBudget.release(req.reserved)
		}
	}()
	if e := g.prepare(req); e != nil {
		writeErrorProto(w, model.ProtoGemini, e)
		return
	}
	n := len(req.raw["contents"]) + len(req.raw["systemInstruction"])
	if gcr, ok := req.raw["generateContentRequest"]; ok {
		n += len(gcr)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"totalTokens": (n + 3) / 4})
}
