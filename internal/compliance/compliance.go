// Package compliance implements the content-compliance engine: exact keyword
// matching (Aho-Corasick) plus semantic matching against vectorized audit
// samples, aggregated into a single Verdict per request.
//
// The package deliberately does not import internal/gateway. Verdict has the
// exact field names and types of gateway.ComplianceVerdict so main can adapt
// it with a trivial wrapper.
package compliance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/settings"
	"yzapi/internal/vector"
)

// EmbedFunc produces one unit-normalized vector per input, in order.
type EmbedFunc func(ctx context.Context, inputs []string) ([][]float32, error)

// Policy actions, risk levels and detection methods.
const (
	ActionBlock = "block"
	ActionAudit = "audit"

	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"

	MethodKeyword  = "keyword"
	MethodSemantic = "semantic"
)

// Tunables.
var (
	// DefaultSemanticThreshold is used when settings carry no positive threshold.
	DefaultSemanticThreshold = 0.85
	// BuildBatchSize is the number of samples embedded per request.
	BuildBatchSize = 16
	// EvidenceRunes caps the sample text echoed as semantic evidence.
	EvidenceRunes = 200
)

// Verdict mirrors gateway.ComplianceVerdict field-for-field.
type Verdict struct {
	Hit          bool
	Block        bool
	Action       string
	RiskLevel    string
	DetectMethod string
	PolicyGroup  string
	PolicyID     uint
	Evidence     string
	Confidence   float64
	Hits         model.JSON // []HitEntry
	// Degraded is set when the semantic pass could not run (vector service failure);
	// keyword results are still valid in that case.
	Degraded       bool
	DegradedReason string
}

// HitEntry is one element of Verdict.Hits.
type HitEntry struct {
	Method        string  `json:"method"`
	PolicyGroup   string  `json:"policy_group"`
	PolicyGroupID uint    `json:"policy_group_id"`
	Evidence      string  `json:"evidence"`
	Score         float64 `json:"score"`
	Action        string  `json:"action"`
	RiskLevel     string  `json:"risk_level"`
}

type policy struct {
	ID        uint
	Name      string
	Action    string
	RiskLevel string
}

type wordEntry struct {
	ID     uint
	Word   string
	Policy policy
}

type sampleEntry struct {
	ID     uint
	Text   string
	Vec    []float32
	Policy policy
}

type index struct {
	ac      *matcher
	words   []wordEntry
	samples []sampleEntry
	stale   int // enabled samples skipped because their vectors were built with another model
}

// Engine is the content-compliance engine.
type Engine struct {
	db       *gorm.DB
	st       *settings.Store
	embed    EmbedFunc
	idx      atomic.Pointer[index]
	identity func() string
}

// New creates an engine and loads its index. New never fails so the gateway
// can start; call Reload to surface load errors.
func New(db *gorm.DB, st *settings.Store, embed EmbedFunc) *Engine {
	e := &Engine{db: db, st: st, embed: embed}
	e.idx.Store(&index{})
	_ = e.Reload()
	return e
}

// Reload loads enabled policy groups, their enabled sensitive words and
// enabled vectorized audit samples, rebuilds the matcher and swaps atomically.
func (e *Engine) Reload() error {
	var groups []model.PolicyGroup
	if err := e.db.Where("enabled = ?", true).Find(&groups).Error; err != nil {
		return err
	}
	pol := make(map[uint]policy, len(groups))
	ids := make([]uint, 0, len(groups))
	for _, g := range groups {
		pol[g.ID] = policy{ID: g.ID, Name: g.Name, Action: g.Action, RiskLevel: g.RiskLevel}
		ids = append(ids, g.ID)
	}
	idx := &index{}
	if len(ids) == 0 {
		e.idx.Store(idx)
		return nil
	}

	var words []model.SensitiveWord
	if err := e.db.Where("enabled = ? AND policy_group_id IN ?", true, ids).Order("id").Find(&words).Error; err != nil {
		return err
	}
	patterns := make([]string, 0, len(words))
	for _, w := range words {
		word := strings.ToLower(strings.TrimSpace(w.Word))
		if word == "" {
			continue
		}
		idx.words = append(idx.words, wordEntry{ID: w.ID, Word: w.Word, Policy: pol[w.PolicyGroupID]})
		patterns = append(patterns, word)
	}
	idx.ac = newMatcher(patterns)

	var samples []model.AuditSample
	if err := e.db.Where("enabled = ? AND policy_group_id IN ? AND vector IS NOT NULL AND length(vector) > 0", true, ids).
		Order("id").Find(&samples).Error; err != nil {
		return err
	}
	cur := e.vectorID()
	skipped := 0
	for _, s := range samples {
		if len(s.Vector) == 0 {
			continue
		}
		if s.VectorModel != "" && cur != "" && s.VectorModel != cur {
			skipped++
			continue
		}
		idx.samples = append(idx.samples, sampleEntry{ID: s.ID, Text: s.Text, Vec: vector.Decode(s.Vector), Policy: pol[s.PolicyGroupID]})
	}
	idx.stale = skipped
	if skipped > 0 {
		slog.Warn("audit samples built with a different embedding model were skipped; rebuild vectors", "skipped", skipped)
	}
	e.idx.Store(idx)
	return nil
}

// Check inspects text. It returns a zero Verdict when compliance is disabled.
func (e *Engine) Check(ctx context.Context, text string) Verdict {
	c := e.st.Get().Compliance
	if !c.Enabled {
		return Verdict{}
	}
	return e.check(ctx, text, c.SemanticThreshold)
}

// Test is Check ignoring the enabled flag (admin "test" button).
func (e *Engine) Test(ctx context.Context, text string) Verdict {
	return e.check(ctx, text, e.st.Get().Compliance.SemanticThreshold)
}

func (e *Engine) check(ctx context.Context, text string, threshold float64) Verdict {
	idx := e.idx.Load()
	if idx == nil || strings.TrimSpace(text) == "" {
		return Verdict{}
	}
	var hits []HitEntry

	// Keyword pass.
	if idx.ac != nil && len(idx.words) > 0 {
		for _, pi := range idx.ac.Match(strings.ToLower(text)) {
			w := idx.words[pi]
			hits = append(hits, HitEntry{
				Method: MethodKeyword, PolicyGroup: w.Policy.Name, PolicyGroupID: w.Policy.ID,
				Evidence: w.Word, Score: 1, Action: w.Policy.Action, RiskLevel: w.Policy.RiskLevel,
			})
		}
	}

	// Semantic pass.
	if len(idx.samples) == 0 && idx.stale > 0 {
		// Samples are configured but none is usable with the current embedding model:
		// the semantic check cannot run, which is a degradation, not a clean pass.
		v := aggregate(hits)
		v.Degraded, v.DegradedReason = true, fmt.Sprintf("%d audit samples were built with a different embedding model; rebuild vectors", idx.stale)
		return v
	}
	if len(idx.samples) > 0 && e.embed != nil {
		if threshold <= 0 {
			threshold = DefaultSemanticThreshold
		}
		vecs, err := e.embed(ctx, []string{text})
		switch {
		case err != nil:
			v := aggregate(hits)
			v.Degraded, v.DegradedReason = true, err.Error()
			return v
		case len(vecs) != 1 || len(vecs[0]) == 0:
			v := aggregate(hits)
			v.Degraded, v.DegradedReason = true, "embedding returned no vector"
			return v
		}
		q := vecs[0]
		compatible := 0
		for _, s := range idx.samples {
			if len(s.Vec) != len(q) {
				continue // built with a different model; skipped until rebuilt
			}
			compatible++
			score := vector.Dot(q, s.Vec)
			if score >= threshold {
				hits = append(hits, HitEntry{
					Method: MethodSemantic, PolicyGroup: s.Policy.Name, PolicyGroupID: s.Policy.ID,
					Evidence: truncateRunes(s.Text, EvidenceRunes), Score: round4(score),
					Action: s.Policy.Action, RiskLevel: s.Policy.RiskLevel,
				})
			}
		}
		if compatible == 0 {
			// Samples exist but none can be compared: the semantic check did not happen.
			v := aggregate(hits)
			v.Degraded, v.DegradedReason = true, fmt.Sprintf("no audit sample matches the query vector dimension (%d); rebuild vectors", len(q))
			return v
		}
	}
	return aggregate(hits)
}

// SetVectorIdentity overrides how the embedding identity is computed (the API layer
// resolves account, base URL and mapped upstream model).
func (e *Engine) SetVectorIdentity(fn func() string) {
	e.identity = fn
	_ = e.Reload()
}

func (e *Engine) vectorID() string {
	if e.identity != nil {
		if id := e.identity(); id != "" {
			return id
		}
	}
	return VectorModelID(e.st)
}

// VectorModelID identifies the configured embedding model; vectors built with another
// identity are ignored so results from incompatible vector spaces never mix.
func VectorModelID(st *settings.Store) string {
	v := st.Get().Vector
	if v.AccountID == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%s", v.AccountID, v.Model)
}

// aggregate folds hits into a Verdict. The primary hit is the blocking hit
// with the highest risk then score; failing that, the highest-risk audit hit.
func aggregate(hits []HitEntry) Verdict {
	if len(hits) == 0 {
		return Verdict{}
	}
	sorted := make([]HitEntry, len(hits))
	copy(sorted, hits)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if (a.Action == ActionBlock) != (b.Action == ActionBlock) {
			return a.Action == ActionBlock
		}
		if ra, rb := riskRank(a.RiskLevel), riskRank(b.RiskLevel); ra != rb {
			return ra > rb
		}
		return a.Score > b.Score
	})
	p := sorted[0]
	v := Verdict{
		Hit:          true,
		Block:        p.Action == ActionBlock,
		Action:       p.Action,
		RiskLevel:    p.RiskLevel,
		DetectMethod: p.Method,
		PolicyGroup:  p.PolicyGroup,
		PolicyID:     p.PolicyGroupID,
		Evidence:     p.Evidence,
		Confidence:   p.Score,
	}
	if b, err := json.Marshal(hits); err == nil {
		v.Hits = model.JSON(b)
	}
	return v
}

func riskRank(level string) int {
	switch strings.ToLower(level) {
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	}
	return 0
}

// BuildVectors embeds the given audit samples (nil = all), stores their
// vectors and reloads the index. On an embedding error it stops and returns
// the counts so far together with the error.
func (e *Engine) BuildVectors(ctx context.Context, ids []uint) (built int, failed int, err error) {
	if e.embed == nil {
		return 0, 0, errors.New("vector service is not configured")
	}
	q := e.db.Model(&model.AuditSample{}).Select("id", "text")
	if ids != nil {
		q = q.Where("id IN ?", ids)
	}
	var rows []model.AuditSample
	if err = q.Order("id").Find(&rows).Error; err != nil {
		return 0, 0, err
	}
	defer func() { _ = e.Reload() }()
	for i := 0; i < len(rows); i += BuildBatchSize {
		end := min(i+BuildBatchSize, len(rows))
		batch := rows[i:end]
		inputs := make([]string, len(batch))
		for j, r := range batch {
			inputs[j] = strings.TrimSpace(r.Text)
			if inputs[j] == "" {
				inputs[j] = " "
			}
		}
		vecs, eerr := e.embed(ctx, inputs)
		if eerr != nil {
			return built, failed + len(batch), eerr
		}
		if len(vecs) != len(batch) {
			return built, failed + len(batch), errors.New("embedding returned wrong number of vectors")
		}
		for j, r := range batch {
			v := vecs[j]
			if len(v) == 0 {
				failed++
				continue
			}
			uerr := e.db.Model(&model.AuditSample{}).Where("id = ?", r.ID).
				Updates(map[string]any{"vector": vector.Encode(v), "vector_dim": len(v), "vector_model": e.vectorID(), "updated_at": time.Now()}).Error
			if uerr != nil {
				failed++
				continue
			}
			built++
		}
	}
	return built, failed, nil
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func round4(f float64) float64 { return float64(int64(f*10000+0.5)) / 10000 }
