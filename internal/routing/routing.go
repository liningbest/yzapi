// Package routing implements the smart-routing engine: it classifies a request
// as "simple" or "complex" using (in order) a context heuristic, vector
// similarity against labelled samples, and local keyword rules.
//
// The package deliberately does not import internal/gateway. Result has the
// exact field names and types of gateway.RouteResult so that main can adapt it
// with a trivial wrapper.
package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/settings"
	"yzapi/internal/vector"
)

// EmbedFunc produces one unit-normalized vector per input, in order.
type EmbedFunc func(ctx context.Context, inputs []string) ([][]float32, error)

// Labels and decision sources.
const (
	LabelSimple  = "simple"
	LabelComplex = "complex"

	SourceRule     = "rule"
	SourceContext  = "context"
	SourceVector   = "vector"
	SourceFallback = "fallback"
)

// Tunables. They are variables so tests (or main) can adjust them.
var (
	// MaxNormalizedRunes caps the normalized text length.
	MaxNormalizedRunes = 2000
	// ContextMsgThreshold: conversations with more messages than this are complex.
	ContextMsgThreshold = 12
	// ContextRuneThreshold: normalized texts longer than this are complex.
	ContextRuneThreshold = 1500
	// SimpleRuleMaxRunes: texts shorter than this may match the simple rule.
	SimpleRuleMaxRunes = 20
	// DefaultThreshold is used when settings carry no positive threshold.
	DefaultThreshold = 0.72
	// DefaultTopK is used when settings carry no positive TopK.
	DefaultTopK = 5
	// RuleConfidence is the confidence reported for rule-based decisions.
	RuleConfidence = 0.9
	// BuildBatchSize is the number of samples embedded per request.
	BuildBatchSize = 16
	// TopKTextRunes caps the sample text echoed in TopK entries.
	TopKTextRunes = 200

	complexTriggers = []string{
		"证明", "推导", "分析", "设计一个", "架构",
		"step by step", "prove", "derive", "compare", "refactor", "optimize", "debug",
	}
	simpleTriggers = []string{
		"你好", "hi", "hello", "翻译", "是什么", "what is", "define",
	}
	codeLineRe = regexp.MustCompile(`^\s*(func |def |class |import |#include|package |return |const |let |var |if\s*\(|for\s*\(|while\s*\(|public |private |static |select |from |\}|.*[;{}]\s*$|.*=>\s*\{?\s*$|.*\)\s*\{\s*$|.*(==|!=|&&|\|\|).*)`)
	spaceRe    = regexp.MustCompile(`\s+`)
)

// Result mirrors gateway.RouteResult field-for-field.
type Result struct {
	Label      string     // simple | complex
	Source     string     // rule | context | vector | fallback
	Confidence float64    // 0..1
	GroupID    uint       // model group chosen for Label
	TopK       model.JSON // [{id,label,text,score}]
	Normalized string
	LatencyMs  int64
}

// TopKEntry is one element of Result.TopK.
type TopKEntry struct {
	ID    uint    `json:"id"`
	Label string  `json:"label"`
	Text  string  `json:"text"`
	Score float64 `json:"score"`
}

// Sample is a vectorized routing sample held in the in-memory index.
type Sample struct {
	ID        uint
	Label     string
	Text      string
	Threshold float64
	Vec       []float32
}

// Engine is the smart-routing engine.
type Engine struct {
	db    *gorm.DB
	st    *settings.Store
	embed EmbedFunc
	index atomic.Pointer[[]Sample]
}

// New creates an engine and loads the sample index. A load error is logged by
// the caller if desired via Reload; New never fails so the gateway can start.
func New(db *gorm.DB, st *settings.Store, embed EmbedFunc) *Engine {
	e := &Engine{db: db, st: st, embed: embed}
	empty := []Sample{}
	e.index.Store(&empty)
	_ = e.Reload()
	return e
}

// Reload loads all vectorized samples from the database and atomically swaps
// the in-memory index.
func (e *Engine) Reload() error {
	var rows []model.RouteSample
	if err := e.db.Where("vector IS NOT NULL AND length(vector) > 0").Find(&rows).Error; err != nil {
		return err
	}
	idx := make([]Sample, 0, len(rows))
	cur := VectorModelID(e.st)
	skipped := 0
	for _, r := range rows {
		if len(r.Vector) == 0 {
			continue
		}
		if r.VectorModel != "" && cur != "" && r.VectorModel != cur {
			skipped++
			continue
		}
		idx = append(idx, Sample{ID: r.ID, Label: r.Label, Text: r.Text, Threshold: r.Threshold, Vec: vector.Decode(r.Vector)})
	}
	if skipped > 0 {
		slog.Warn("route samples built with a different embedding model were skipped; rebuild vectors", "skipped", skipped)
	}
	e.index.Store(&idx)
	return nil
}

// VectorModelID identifies the configured embedding model ("<account>:<model>").
func VectorModelID(st *settings.Store) string {
	v := st.Get().Vector
	if v.AccountID == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%s", v.AccountID, v.Model)
}

// Samples returns the current in-memory index (read-only snapshot).
func (e *Engine) Samples() []Sample { return *e.index.Load() }

// Decide classifies text. It never returns an error: embedding failures fall
// through to rules/fallback.
func (e *Engine) Decide(ctx context.Context, requestID, text string, msgCount int) Result {
	res, _ := e.decide(ctx, text, msgCount)
	return res
}

// Preview is Decide for the admin UI: it also returns the embedding error, if
// any, so the operator can see why the vector step was skipped.
func (e *Engine) Preview(ctx context.Context, text string, msgCount int) (Result, error) {
	return e.decide(ctx, text, msgCount)
}

func (e *Engine) decide(ctx context.Context, text string, msgCount int) (res Result, err error) {
	start := time.Now()
	sr := e.st.Get().SmartRoute
	res = Result{Label: LabelSimple, Source: SourceFallback}
	norm := Normalize(text)
	res.Normalized = norm
	var embedErr error

	// Named result so the deferred fill-in applies to every return path.
	defer func() {
		res.GroupID = groupFor(res.Label, sr)
		res.LatencyMs = time.Since(start).Milliseconds()
	}()

	if norm == "" {
		return res, nil
	}
	runes := utf8.RuneCountInString(norm)

	// 2. Context rule.
	msgLimit := ContextMsgThreshold
	if sr.ContextComplex > 0 {
		msgLimit = sr.ContextComplex
	}
	if msgCount > msgLimit || runes > ContextRuneThreshold {
		res.Label, res.Source, res.Confidence = LabelComplex, SourceContext, 1.0
		return res, nil
	}

	// 4. Vector similarity.
	if idx := e.Samples(); len(idx) > 0 && e.embed != nil {
		vecs, err := e.embed(ctx, []string{norm})
		switch {
		case err != nil:
			embedErr = err
		case len(vecs) != 1 || len(vecs[0]) == 0:
			embedErr = errors.New("embedding returned no vector")
		default:
			r, ok := classifyByVector(vecs[0], idx, sr)
			res.TopK = r.TopK // keep for diagnostics even when below threshold
			if ok {
				res.Label, res.Source, res.Confidence = r.Label, r.Source, r.Confidence
				return res, nil
			}
		}
	}

	// 3. Local rules.
	if label, ok := classifyByRule(norm, runes, sr); ok {
		res.Label, res.Source, res.Confidence = label, SourceRule, RuleConfidence
		return res, embedErr
	}

	// Fallback.
	return res, embedErr
}

// classifyByVector scores the query against every sample. ok is false when the
// best score is below the applicable threshold; the returned Result still
// carries the TopK list for diagnostics.
func classifyByVector(q []float32, idx []Sample, sr settings.SmartRoute) (Result, bool) {
	k := sr.TopK
	if k <= 0 {
		k = DefaultTopK
	}
	type scored struct {
		s     *Sample
		score float64
	}
	// Partial selection: keep the k best in a min-heap (O(N log K)) instead of sorting
	// everything. Ties are broken by lower sample ID so results are deterministic.
	less := func(a, b scored) bool { // a ranks better than b
		if a.score != b.score {
			return a.score > b.score
		}
		return a.s.ID < b.s.ID
	}
	h := make([]scored, 0, k+1)
	worse := func(i, j int) bool { return less(h[j], h[i]) } // heap root = worst kept
	up := func(i int) {
		for i > 0 {
			p := (i - 1) / 2
			if !worse(i, p) {
				break
			}
			h[i], h[p] = h[p], h[i]
			i = p
		}
	}
	down := func(i int) {
		for {
			l, r, m := 2*i+1, 2*i+2, i
			if l < len(h) && worse(l, m) {
				m = l
			}
			if r < len(h) && worse(r, m) {
				m = r
			}
			if m == i {
				return
			}
			h[i], h[m] = h[m], h[i]
			i = m
		}
	}
	for i := range idx {
		if len(idx[i].Vec) != len(q) {
			continue // incompatible vector; skipped until rebuilt
		}
		c := scored{&idx[i], vector.Dot(q, idx[i].Vec)}
		if len(h) < k {
			h = append(h, c)
			up(len(h) - 1)
		} else if less(c, h[0]) {
			h[0] = c
			down(0)
		}
	}
	all := h
	sort.SliceStable(all, func(i, j int) bool { return less(all[i], all[j]) })
	entries := make([]TopKEntry, len(all))
	for i, s := range all {
		entries[i] = TopKEntry{ID: s.s.ID, Label: s.s.Label, Text: truncateRunes(s.s.Text, TopKTextRunes), Score: round4(s.score)}
	}
	res := Result{Source: SourceVector}
	if b, err := json.Marshal(entries); err == nil {
		res.TopK = model.JSON(b)
	}
	if len(all) == 0 {
		return res, false
	}
	best := all[0]
	threshold := sr.Threshold
	if best.s.Threshold > 0 {
		threshold = best.s.Threshold
	}
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	if best.score < threshold {
		return res, false
	}
	// Per-label max score among top K.
	labelMax := map[string]float64{}
	for _, s := range all {
		if v, ok := labelMax[s.s.Label]; !ok || s.score > v {
			labelMax[s.s.Label] = s.score
		}
	}
	bestLabel := best.s.Label
	second := -1.0
	for l, v := range labelMax {
		if l != bestLabel && v > second {
			second = v
		}
	}
	res.Label = bestLabel
	res.Confidence = best.score
	if second >= 0 && best.score-second < sr.ConfidenceGap {
		res.Label = LabelSimple
	}
	return res, true
}

// classifyByRule applies cheap keyword heuristics.
func classifyByRule(norm string, runes int, sr settings.SmartRoute) (string, bool) {
	lower := strings.ToLower(norm)
	if strings.Contains(norm, "```") || looksLikeCode(norm) {
		return LabelComplex, true
	}
	for _, w := range complexTriggers {
		if strings.Contains(lower, w) {
			return LabelComplex, true
		}
	}
	if runes < SimpleRuleMaxRunes {
		for _, w := range simpleTriggers {
			if strings.Contains(lower, w) {
				return LabelSimple, true
			}
		}
	}
	if sr.RuleMaxChars > 0 && runes < sr.RuleMaxChars {
		return LabelSimple, true
	}
	return "", false
}

// looksLikeCode reports whether at least three lines resemble source code.
func looksLikeCode(s string) bool {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if codeLineRe.MatchString(line) {
			n++
			if n >= 3 {
				return true
			}
		}
	}
	return false
}

func groupFor(label string, sr settings.SmartRoute) uint {
	if label == LabelComplex {
		return sr.ComplexGroupID
	}
	return sr.SimpleGroupID
}

// Normalize trims, collapses whitespace runs (keeping newlines as newlines so
// code detection still works) and truncates to MaxNormalizedRunes.
func Normalize(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	// Collapse runs of horizontal whitespace; collapse blank-line runs to one newline.
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimRightFunc(spaceRe.ReplaceAllString(strings.TrimLeftFunc(l, func(r rune) bool { return r == '\t' || r == '\r' }), " "), unicode.IsSpace)
		if l == "" {
			if len(out) > 0 && out[len(out)-1] == "" {
				continue
			}
			out = append(out, "")
			continue
		}
		out = append(out, l)
	}
	text = strings.TrimSpace(strings.Join(out, "\n"))
	return truncateRunes(text, MaxNormalizedRunes)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func round4(f float64) float64 { return float64(int64(f*10000+0.5)) / 10000 }

// BuildVectors embeds the given samples (nil = all), stores their vectors and
// reloads the index. On an embedding error it stops and returns the counts so
// far together with the error.
func (e *Engine) BuildVectors(ctx context.Context, ids []uint) (built int, failed int, err error) {
	if e.embed == nil {
		return 0, 0, errors.New("vector service is not configured")
	}
	q := e.db.Model(&model.RouteSample{}).Select("id", "text")
	if ids != nil {
		q = q.Where("id IN ?", ids)
	}
	var rows []model.RouteSample
	if err = q.Order("id").Find(&rows).Error; err != nil {
		return 0, 0, err
	}
	defer func() { _ = e.Reload() }()
	for i := 0; i < len(rows); i += BuildBatchSize {
		end := min(i+BuildBatchSize, len(rows))
		batch := rows[i:end]
		inputs := make([]string, len(batch))
		for j, r := range batch {
			inputs[j] = Normalize(r.Text)
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
			uerr := e.db.Model(&model.RouteSample{}).Where("id = ?", r.ID).
				Updates(map[string]any{"vector": vector.Encode(v), "vector_dim": len(v), "vector_model": VectorModelID(e.st), "updated_at": time.Now()}).Error
			if uerr != nil {
				failed++
				continue
			}
			built++
		}
	}
	return built, failed, nil
}
