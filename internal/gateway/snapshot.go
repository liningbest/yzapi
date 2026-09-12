package gateway

import (
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"yzapi/internal/crypto"
	"yzapi/internal/model"
	"yzapi/internal/provider"
	"yzapi/internal/settings"
)

// Upstream is a decrypted, ready-to-use view of an account.
type Upstream struct {
	ID             uint
	Name           string
	Provider       string
	Type           string
	BaseURL        string
	APIKey         string
	AuthHeader     string
	Protocols      map[string]bool
	Mappings       map[string]string // request model -> upstream model
	Priority       int
	Weight         int
	MaxConcurrency int
	Passthrough    bool // accepts unmapped model names as-is
	Extra          map[string]any
}

func (u *Upstream) HasProtocol(p string) bool { return u.Protocols[p] }

// ModelInfo is a client-visible model.
type ModelInfo struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Kind     string   `json:"kind"` // model | virtual | group
	Virtual  bool     `json:"virtual"`
	Provider string   `json:"provider,omitempty"`
	Accounts int      `json:"accounts"`
	Models   []string `json:"models,omitempty"` // for groups
}

type GroupView struct {
	ID                uint
	Name              string
	Enabled           bool
	MaxConcurrency    int
	KeyMaxConcurrency int
	TokenQuota        int64
	TokensPerMinute   int64
	RequestsPerMinute int
	Allowed           map[string]bool // nil = all models allowed
	ModelGroupNames   map[string]bool
}

type ModelGroupView struct {
	ID     uint
	Name   string
	Type   string
	Models []string
}

// Snapshot is an immutable view of routing configuration.
type Snapshot struct {
	Accounts     map[uint]*Upstream
	byModel      map[string][]*Upstream // request model -> upstreams sorted by priority
	Models       []ModelInfo
	modelType    map[string]string
	lowerName    map[string]string      // lower-cased request model -> canonical name
	lowerDup     map[string]bool        // lower-cased names configured with more than one spelling
	passthrough  map[string][]*Upstream // model type -> accounts accepting unmapped names
	Groups       map[uint]*GroupView
	DefaultGroup *GroupView
	ModelGroups  map[uint]*ModelGroupView
	groupByName  map[string]*ModelGroupView
	VirtualModel string
	// SmartRoute is the complete smart-route configuration this generation was built
	// from. The data plane reads group ids, thresholds and rules from here only, so a
	// generation is either wholly old or wholly new; a failed rebuild never mixes a
	// previous snapshot with newer settings.
	SmartRoute settings.SmartRoute
	BuiltAt    time.Time
}

func (s *Snapshot) UpstreamsFor(reqModel string) []*Upstream { return s.byModel[reqModel] }

// PassthroughFor lists accounts of the given type that accept unmapped model names.
func (s *Snapshot) PassthroughFor(typ string) []*Upstream { return s.passthrough[typ] }

// vendorPrefixes are stripped when a client sends OpenRouter / AI-SDK style ids
// ("anthropic/claude-sonnet-4-5", "models/gemini-2.5-pro").
var vendorPrefixes = []string{"anthropic/", "openai/", "deepseek/", "google/", "models/", "x-ai/", "xai/", "moonshotai/", "moonshot/", "zhipu/", "z-ai/", "minimax/", "qwen/", "alibaba/", "meta-llama/", "mistralai/"}

// versionLike reports whether a name suffix is a date / version tag rather than a
// different model variant.
func versionLike(rest string) bool {
	if rest == "" {
		return false
	}
	if rest == "latest" {
		return true
	}
	for _, r := range rest {
		if (r < '0' || r > '9') && r != '-' && r != '.' {
			return false
		}
	}
	return rest[0] >= '0' && rest[0] <= '9'
}

// Resolution outcomes.
const (
	ResolveFound     = iota
	ResolveUnknown   // nothing configured matches and no pass-through account applies
	ResolveAmbiguous // several configured names match equally well; refused, never forwarded
)

// Resolve maps whatever model name a client sent to a configured request model.
// Order: exact -> case-insensitive -> vendor prefix stripped -> "-latest" stripped ->
// unique version-suffix match (client "claude-sonnet-4-5-20250929" vs configured
// "claude-sonnet-4-5", or the reverse) -> pass-through account of the wanted type
// (any type when wantType is empty, e.g. metadata lookups). Ambiguity is a distinct
// outcome so it is never papered over by pass-through.
func (s *Snapshot) Resolve(name, wantType string) (canonical string, typ string, ok bool) {
	c, t, st := s.ResolveDetail(name, wantType)
	return c, t, st == ResolveFound
}

func (s *Snapshot) ResolveDetail(name, wantType string) (canonical string, typ string, status int) {
	ambiguous := false
	try := func(n string) (string, string, bool) {
		if t, ok := s.modelType[n]; ok {
			return n, t, true
		}
		l := strings.ToLower(n)
		if s.lowerDup[l] {
			ambiguous = true // "GPT-5" and "gpt-5" both configured: refuse rather than guess
			return "", "", false
		}
		if c, ok := s.lowerName[l]; ok {
			return c, s.modelType[c], true
		}
		return "", "", false
	}
	if c, t, ok := try(name); ok {
		return c, t, ResolveFound
	}
	n := name
	lower := strings.ToLower(n)
	for _, p := range vendorPrefixes {
		if strings.HasPrefix(lower, p) {
			n = n[len(p):]
			break
		}
	}
	if i := strings.LastIndex(n, "/"); i >= 0 && i < len(n)-1 {
		n = n[i+1:]
	}
	if c, t, ok := try(n); ok {
		return c, t, ResolveFound
	}
	if strings.HasSuffix(strings.ToLower(n), "-latest") {
		if c, t, ok := try(n[:len(n)-len("-latest")]); ok {
			return c, t, ResolveFound
		}
	}
	// Version-suffix tolerance: among configured names that differ from the request only
	// by a dash-separated version / date segment, the longest wins. A tie, or a longest
	// candidate that stands for several case variants, is ambiguous and refused.
	ln := strings.ToLower(n)
	type cand struct {
		name string
		dup  bool
	}
	var matches []cand
	bestLen := -1
	for l, c := range s.lowerName {
		if wantType != "" && s.modelType[c] != wantType {
			continue
		}
		// The extra segment must look like a version or date ("20250929", "2025-08-07",
		// "latest"); "claude" must not match "claude-sonnet-4-5", nor "gpt-5-codex" "gpt-5",
		// and "demo-coder" is a different model, not a version of "demo".
		var rest string
		switch {
		case strings.HasPrefix(ln, l+"-"):
			rest = ln[len(l)+1:]
		case strings.HasPrefix(l, ln+"-"):
			rest = l[len(ln)+1:]
		default:
			continue
		}
		if !versionLike(rest) {
			continue
		}
		switch {
		case len(l) > bestLen:
			matches, bestLen = []cand{{c, s.lowerDup[l]}}, len(l)
		case len(l) == bestLen:
			matches = append(matches, cand{c, s.lowerDup[l]})
		}
	}
	if len(matches) == 1 && !matches[0].dup && !ambiguous {
		return matches[0].name, s.modelType[matches[0].name], ResolveFound
	}
	if len(matches) > 0 || ambiguous {
		return "", "", ResolveAmbiguous
	}
	types := []string{wantType}
	if wantType == "" {
		types = []string{model.TypeText, model.TypeEmbedding, model.TypeImage}
	}
	for _, t := range types {
		if len(s.passthrough[t]) > 0 {
			return name, t, ResolveFound
		}
	}
	return "", "", ResolveUnknown
}
func (s *Snapshot) GroupByName(name string) *ModelGroupView { return s.groupByName[name] }
func (s *Snapshot) ModelType(m string) (string, bool) {
	t, ok := s.modelType[m]
	return t, ok
}

// ModelGroupFor returns the model group containing the model that the group is allowed to use.
func (s *Snapshot) ModelGroupNameFor(g *GroupView, m string) string {
	for _, mg := range s.ModelGroups {
		if g != nil && g.ModelGroupNames != nil && !g.ModelGroupNames[mg.Name] {
			continue
		}
		for _, x := range mg.Models {
			if x == m {
				return mg.Name
			}
		}
	}
	return ""
}

type snapshotHolder struct {
	db     *gorm.DB
	cipher *crypto.Cipher
	cur    atomic.Pointer[Snapshot]
}

func (h *snapshotHolder) get() *Snapshot { return h.cur.Load() }

func (h *snapshotHolder) rebuild(sr settings.SmartRoute) error {
	virtualModel, smartEnabled := sr.VirtualModel, sr.Enabled
	snap := &Snapshot{
		SmartRoute:  sr,
		Accounts:    map[uint]*Upstream{},
		byModel:     map[string][]*Upstream{},
		modelType:   map[string]string{},
		lowerName:   map[string]string{},
		lowerDup:    map[string]bool{},
		passthrough: map[string][]*Upstream{},
		Groups:      map[uint]*GroupView{},
		ModelGroups: map[uint]*ModelGroupView{},
		groupByName: map[string]*ModelGroupView{},
		BuiltAt:     time.Now(),
	}

	var accounts []model.Account
	if err := h.db.Preload("Mappings").Where("enabled = ?", true).Find(&accounts).Error; err != nil {
		return err
	}
	modelProviders := map[string]map[string]bool{}
	for i := range accounts {
		a := &accounts[i]
		key, err := h.cipher.Decrypt(a.APIKeyEnc)
		if err != nil {
			key = ""
		}
		p, _ := provider.Get(a.Provider)
		auth := p.AuthHeader
		if auth == "" {
			auth = "bearer"
		}
		u := &Upstream{
			ID: a.ID, Name: a.Name, Provider: a.Provider, Type: a.Type,
			BaseURL: strings.TrimRight(a.BaseURL, "/"), APIKey: key, AuthHeader: auth,
			Protocols: map[string]bool{}, Mappings: map[string]string{},
			Priority: a.Priority, Weight: max(a.Weight, 1), MaxConcurrency: a.MaxConcurrency, Passthrough: a.PassthroughModels,
		}
		if a.PassthroughModels {
			snap.passthrough[a.Type] = append(snap.passthrough[a.Type], u)
		}
		for _, pr := range a.Protocols {
			u.Protocols[pr] = true
		}
		for _, m := range a.Mappings {
			u.Mappings[m.RequestModel] = m.UpstreamModel
			snap.byModel[m.RequestModel] = append(snap.byModel[m.RequestModel], u)
			snap.modelType[m.RequestModel] = a.Type
			if modelProviders[m.RequestModel] == nil {
				modelProviders[m.RequestModel] = map[string]bool{}
			}
			modelProviders[m.RequestModel][a.Provider] = true
		}
		snap.Accounts[a.ID] = u
	}
	for m, list := range snap.byModel {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Priority < list[j].Priority })
		snap.byModel[m] = list
		if prev, ok := snap.lowerName[strings.ToLower(m)]; ok && prev != m {
			snap.lowerDup[strings.ToLower(m)] = true
		}
		snap.lowerName[strings.ToLower(m)] = m
	}
	for t, list := range snap.passthrough {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Priority < list[j].Priority })
		snap.passthrough[t] = list
	}
	names := make([]string, 0, len(snap.byModel))
	for m := range snap.byModel {
		names = append(names, m)
	}
	sort.Strings(names)
	for _, m := range names {
		prov := ""
		if ps := modelProviders[m]; len(ps) == 1 {
			for k := range ps {
				prov = k
			}
		}
		snap.Models = append(snap.Models, ModelInfo{Name: m, Type: snap.modelType[m], Kind: "model", Provider: prov, Accounts: len(snap.byModel[m])})
	}
	if smartEnabled && virtualModel != "" {
		snap.Models = append([]ModelInfo{{Name: virtualModel, Type: model.TypeText, Kind: "virtual", Virtual: true}}, snap.Models...)
		snap.modelType[virtualModel] = model.TypeText
		snap.VirtualModel = virtualModel
	}

	var mgs []model.ModelGroup
	if err := h.db.Find(&mgs).Error; err != nil {
		return err
	}
	for _, mg := range mgs {
		mgv := &ModelGroupView{ID: mg.ID, Name: mg.Name, Type: mg.Type, Models: []string(mg.Models)}
		snap.ModelGroups[mg.ID] = mgv
		// A model group can be addressed directly by name (ordered failover),
		// unless the name collides with a concrete model.
		if _, exists := snap.modelType[mg.Name]; !exists {
			snap.groupByName[mg.Name] = mgv
			snap.modelType[mg.Name] = mg.Type
			snap.Models = append(snap.Models, ModelInfo{Name: mg.Name, Type: mg.Type, Kind: "group", Models: mgv.Models})
		}
	}

	var groups []model.UserGroup
	if err := h.db.Preload("ModelGroups").Find(&groups).Error; err != nil {
		return err
	}
	for _, g := range groups {
		gv := &GroupView{ID: g.ID, Name: g.Name, Enabled: g.Enabled, MaxConcurrency: g.MaxConcurrency,
			KeyMaxConcurrency: g.KeyMaxConcurrency, TokenQuota: g.TokenQuota, TokensPerMinute: g.TokensPerMinute, RequestsPerMinute: g.RequestsPerMinute}
		if len(g.ModelGroups) > 0 {
			gv.Allowed = map[string]bool{}
			gv.ModelGroupNames = map[string]bool{}
			for _, mg := range g.ModelGroups {
				gv.ModelGroupNames[mg.Name] = true
				for _, m := range mg.Models {
					gv.Allowed[m] = true
				}
			}
		}
		snap.Groups[g.ID] = gv
		if g.IsDefault {
			snap.DefaultGroup = gv
		}
	}
	h.cur.Store(snap)
	return nil
}
