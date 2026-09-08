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
	MaxConcurrency int
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
	Groups       map[uint]*GroupView
	DefaultGroup *GroupView
	ModelGroups  map[uint]*ModelGroupView
	groupByName  map[string]*ModelGroupView
	VirtualModel string
	BuiltAt      time.Time
}

func (s *Snapshot) UpstreamsFor(reqModel string) []*Upstream { return s.byModel[reqModel] }
func (s *Snapshot) GroupByName(name string) *ModelGroupView  { return s.groupByName[name] }
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

func (h *snapshotHolder) rebuild(virtualModel string, smartEnabled bool) error {
	snap := &Snapshot{
		Accounts:    map[uint]*Upstream{},
		byModel:     map[string][]*Upstream{},
		modelType:   map[string]string{},
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
			Priority: a.Priority, MaxConcurrency: a.MaxConcurrency,
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
			KeyMaxConcurrency: g.KeyMaxConcurrency, TokenQuota: g.TokenQuota}
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
