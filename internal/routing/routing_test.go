package routing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yzapi/internal/model"
	"yzapi/internal/settings"
	"yzapi/internal/vector"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// A unique in-memory database per call: the name carries a random suffix so
	// -count=N and parallel runs never share rows, and the pool is closed with the test.
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"-"+model.NewBuildToken()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if raw, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = raw.Close() })
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newStore(t *testing.T, db *gorm.DB, sr settings.SmartRoute) *settings.Store {
	t.Helper()
	st, err := settings.New(db)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if err := st.SetSmartRoute(sr); err != nil {
		t.Fatalf("set smart route: %v", err)
	}
	return st
}

// fakeEmbed maps known strings to fixed unit vectors; unknown strings get a
// vector orthogonal to everything.
func fakeEmbed(table map[string][]float32) EmbedFunc {
	return func(_ context.Context, inputs []string) ([][]float32, error) {
		out := make([][]float32, len(inputs))
		for i, s := range inputs {
			v, ok := table[s]
			if !ok {
				v = []float32{0, 0, 0, 1}
			}
			out[i] = vector.Normalize(v)
		}
		return out, nil
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  hello   world  ", "hello world"},
		{"a\r\n\r\n\r\nb", "a\n\nb"},
		{"\t x \t y \t", "x y"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("字", MaxNormalizedRunes+50)
	if got := Normalize(long); len([]rune(got)) != MaxNormalizedRunes {
		t.Errorf("truncate: got %d runes", len([]rune(got)))
	}
}

func TestDecideRules(t *testing.T) {
	db := newTestDB(t)
	st := newStore(t, db, settings.SmartRoute{Enabled: true, SimpleGroupID: 1, ComplexGroupID: 2, Threshold: 0.72, TopK: 5})
	e := New(db, st, nil) // no index, no embedder -> rules only

	cases := []struct {
		name     string
		text     string
		msgCount int
		label    string
		source   string
		conf     float64
		group    uint
	}{
		{"empty", "   ", 1, LabelSimple, SourceFallback, 0, 1},
		{"context msgs", "ok", 13, LabelComplex, SourceContext, 1, 2},
		{"context long", strings.Repeat("长", ContextRuneThreshold+1), 1, LabelComplex, SourceContext, 1, 2},
		{"code fence", "please fix\n```go\nfmt.Println(1)\n```", 1, LabelComplex, SourceRule, RuleConfidence, 2},
		{"code lines", "int x = 1;\nint y = 2;\nreturn x + y;", 1, LabelComplex, SourceRule, RuleConfidence, 2},
		{"zh reasoning", "请证明勾股定理", 1, LabelComplex, SourceRule, RuleConfidence, 2},
		{"en reasoning", "Let's do this Step By Step", 1, LabelComplex, SourceRule, RuleConfidence, 2},
		{"zh greeting", "你好", 1, LabelSimple, SourceRule, RuleConfidence, 1},
		{"en simple", "What is Go?", 1, LabelSimple, SourceRule, RuleConfidence, 1},
		{"simple trigger but long", "hello " + strings.Repeat("x", 30), 1, LabelSimple, SourceFallback, 0, 1},
		{"complex beats simple", "hello, prove it", 1, LabelComplex, SourceRule, RuleConfidence, 2},
		{"no match", "tell me about the weather tomorrow please", 1, LabelSimple, SourceFallback, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := e.Decide(context.Background(), "req", c.text, c.msgCount)
			if r.Label != c.label || r.Source != c.source || r.Confidence != c.conf || r.GroupID != c.group {
				t.Errorf("got {%s %s %.2f g=%d}, want {%s %s %.2f g=%d}", r.Label, r.Source, r.Confidence, r.GroupID, c.label, c.source, c.conf, c.group)
			}
		})
	}
}

func TestDecideRuleMaxChars(t *testing.T) {
	db := newTestDB(t)
	st := newStore(t, db, settings.SmartRoute{SimpleGroupID: 1, ComplexGroupID: 2, RuleMaxChars: 50, ContextComplex: 3})
	e := New(db, st, nil)
	r := e.Decide(context.Background(), "", "tell me about the weather tomorrow", 1)
	if r.Label != LabelSimple || r.Source != SourceRule {
		t.Errorf("RuleMaxChars: got %s/%s", r.Label, r.Source)
	}
	r = e.Decide(context.Background(), "", "ok", 4)
	if r.Label != LabelComplex || r.Source != SourceContext {
		t.Errorf("ContextComplex override: got %s/%s", r.Label, r.Source)
	}
}

func seedSamples(t *testing.T, db *gorm.DB, samples []model.RouteSample) {
	t.Helper()
	for i := range samples {
		if err := db.Create(&samples[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestDecideVector(t *testing.T) {
	db := newTestDB(t)
	// Sample vectors in a 4-d space.
	sA := []float32{1, 0, 0, 0}     // complex
	sB := []float32{0, 1, 0, 0}     // simple
	sC := []float32{0.9, 0.1, 0, 0} // complex, close to A
	sD := []float32{0, 0, 1, 0}     // simple, with a very strict per-sample threshold
	seedSamples(t, db, []model.RouteSample{
		{ID: 1, Label: LabelComplex, Text: "A", Vector: vector.Encode(vector.Normalize(sA))},
		{ID: 2, Label: LabelSimple, Text: "B", Vector: vector.Encode(vector.Normalize(sB))},
		{ID: 3, Label: LabelComplex, Text: "C", Vector: vector.Encode(vector.Normalize(sC))},
		{ID: 4, Label: LabelSimple, Text: "D", Threshold: 0.999, Vector: vector.Encode(vector.Normalize(sD))},
		{ID: 5, Label: LabelSimple, Text: "unvectorized"},
	})

	// Scores (cosine) for reference:
	//   "near A"  -> A 0.9988, C 0.9982, B 0.0499, D 0
	//   "near B"  -> B 0.9988, C 0.1104, D 0.0499, A 0
	//   "mid A/B" -> C 0.8451, A 0.7809, B 0.6247, D 0   (gap complex-simple = 0.22)
	//   "exact D" -> D 1.0 (>= 0.999)
	//   unknown   -> {0,0,0,1}: orthogonal to everything
	embed := fakeEmbed(map[string][]float32{
		"near A":  {1, 0.05, 0, 0},
		"near B":  {0, 1, 0.05, 0},
		"mid A/B": {1, 0.8, 0, 0},
		"exact D": {0, 0, 1, 0},
	})

	t.Run("index loads only vectorized", func(t *testing.T) {
		st := newStore(t, db, settings.SmartRoute{SimpleGroupID: 1, ComplexGroupID: 2, Threshold: 0.72, TopK: 3})
		e := New(db, st, embed)
		if n := len(e.Samples()); n != 4 {
			t.Fatalf("index size %d, want 4", n)
		}
	})

	cases := []struct {
		name   string
		sr     settings.SmartRoute
		text   string
		label  string
		source string
		topIDs []uint
	}{
		{"complex match", settings.SmartRoute{Threshold: 0.72, TopK: 3}, "near A", LabelComplex, SourceVector, []uint{1, 3, 2}},
		{"simple match", settings.SmartRoute{Threshold: 0.72, TopK: 5}, "near B", LabelSimple, SourceVector, []uint{2, 3, 4, 1}},
		{"gap forces simple", settings.SmartRoute{Threshold: 0.5, ConfidenceGap: 0.5, TopK: 5}, "mid A/B", LabelSimple, SourceVector, nil},
		{"gap satisfied", settings.SmartRoute{Threshold: 0.5, ConfidenceGap: 0.05, TopK: 5}, "mid A/B", LabelComplex, SourceVector, nil},
		{"per-sample threshold met", settings.SmartRoute{Threshold: 0.72, TopK: 5}, "exact D", LabelSimple, SourceVector, nil},
		{"below threshold falls to rule", settings.SmartRoute{Threshold: 0.72, TopK: 5}, "prove it", LabelComplex, SourceRule, nil},
		{"default topK", settings.SmartRoute{Threshold: 0.72}, "near A", LabelComplex, SourceVector, []uint{1, 3, 2, 4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.sr.SimpleGroupID, c.sr.ComplexGroupID = 1, 2
			st := newStore(t, db, c.sr)
			e := New(db, st, embed)
			r := e.Decide(context.Background(), "req", c.text, 1)
			if r.Label != c.label || r.Source != c.source {
				t.Fatalf("got %s/%s (%.3f), want %s/%s; topk=%s", r.Label, r.Source, r.Confidence, c.label, c.source, r.TopK)
			}
			if c.topIDs != nil {
				var entries []TopKEntry
				if err := json.Unmarshal(r.TopK, &entries); err != nil {
					t.Fatalf("topk json: %v", err)
				}
				var ids []uint
				for _, en := range entries {
					ids = append(ids, en.ID)
				}
				if len(ids) != len(c.topIDs) {
					t.Fatalf("topk ids %v, want %v", ids, c.topIDs)
				}
				for i := range ids {
					if ids[i] != c.topIDs[i] {
						t.Fatalf("topk ids %v, want %v", ids, c.topIDs)
					}
				}
			}
			if r.Source == SourceVector && r.Confidence <= 0 {
				t.Errorf("vector decision should carry confidence")
			}
		})
	}

	t.Run("per-sample threshold too high", func(t *testing.T) {
		st := newStore(t, db, settings.SmartRoute{SimpleGroupID: 1, ComplexGroupID: 2, Threshold: 0.72, TopK: 5})
		e := New(db, st, fakeEmbed(map[string][]float32{"x": {0, 0.05, 1, 0}}))
		// D (thr 0.999) is best with score 0.9988 < 0.999 -> no match -> fallback.
		r := e.Decide(context.Background(), "", "x", 1)
		if r.Source != SourceFallback || r.Label != LabelSimple {
			t.Errorf("got %s/%s, want simple/fallback", r.Label, r.Source)
		}
		if len(r.TopK) == 0 {
			t.Errorf("topk should still be attached for diagnostics")
		}
	})
}

func TestPreviewReturnsEmbedError(t *testing.T) {
	db := newTestDB(t)
	seedSamples(t, db, []model.RouteSample{{ID: 1, Label: LabelComplex, Text: "A", Vector: vector.Encode([]float32{1, 0})}})
	st := newStore(t, db, settings.SmartRoute{SimpleGroupID: 1, ComplexGroupID: 2})
	boom := errors.New("boom")
	e := New(db, st, func(context.Context, []string) ([][]float32, error) { return nil, boom })
	r, err := e.Preview(context.Background(), "please prove this", 1)
	if !errors.Is(err, boom) {
		t.Fatalf("want embed error, got %v", err)
	}
	if r.Label != LabelComplex || r.Source != SourceRule {
		t.Errorf("should fall through to rules: %s/%s", r.Label, r.Source)
	}
	// Decide swallows it.
	if r2 := e.Decide(context.Background(), "", "please prove this", 1); r2.Source != SourceRule {
		t.Errorf("Decide: %s", r2.Source)
	}
}

func TestBuildVectors(t *testing.T) {
	db := newTestDB(t)
	var rows []model.RouteSample
	for i := 1; i <= 20; i++ {
		rows = append(rows, model.RouteSample{Label: LabelSimple, Text: "sample " + strings.Repeat("x", i)})
	}
	seedSamples(t, db, rows)
	st := newStore(t, db, settings.SmartRoute{})

	calls := 0
	e := New(db, st, func(_ context.Context, in []string) ([][]float32, error) {
		calls++
		out := make([][]float32, len(in))
		for i := range in {
			out[i] = []float32{1, 0, 0}
		}
		return out, nil
	})
	built, failed, err := e.BuildVectors(context.Background(), nil)
	if err != nil || built != 20 || failed != 0 {
		t.Fatalf("built=%d failed=%d err=%v", built, failed, err)
	}
	if calls != 2 {
		t.Errorf("expected 2 batches, got %d", calls)
	}
	if n := len(e.Samples()); n != 20 {
		t.Errorf("index after build: %d", n)
	}
	var s model.RouteSample
	db.First(&s, 1)
	if s.VectorDim != 3 || len(s.Vector) != 12 {
		t.Errorf("stored vector dim=%d len=%d", s.VectorDim, len(s.Vector))
	}

	// Subset with error mid-way.
	boom := errors.New("boom")
	e2 := New(db, st, func(context.Context, []string) ([][]float32, error) { return nil, boom })
	built, failed, err = e2.BuildVectors(context.Background(), []uint{1, 2, 3})
	if !errors.Is(err, boom) || built != 0 || failed != 3 {
		t.Errorf("built=%d failed=%d err=%v", built, failed, err)
	}
}

func TestStats(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	rows := []model.RouteDecision{
		{Label: "simple", Source: "vector", SelectedModel: "gpt-4o-mini", TotalTokens: 100, LatencyMs: 5, CreatedAt: now},
		{Label: "complex", Source: "vector", SelectedModel: "gpt-4o", TotalTokens: 3000, LatencyMs: 7, Failed: true, CreatedAt: now},
		{Label: "simple", Source: "rule", SelectedModel: "gpt-4o-mini", TotalTokens: 700, LatencyMs: 1, CreatedAt: now},
		{Label: "simple", Source: SourcePreview, SelectedModel: "", TotalTokens: 0, LatencyMs: 2, CreatedAt: now},
		{Label: "complex", Source: "context", SelectedModel: "gpt-4o", TotalTokens: 40000, LatencyMs: 3, CreatedAt: now.Add(-48 * time.Hour)}, // out of range
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	s, err := Stats(db, now.Add(-24*time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.Decisions != 4 || s.Requests != 3 || s.Failed != 1 || s.TotalTokens != 3800 || s.AvgTokens != 1266 || s.LatencyMs != 15 {
		t.Errorf("totals: %+v", s)
	}
	find := func(b []Bucket, k string) Bucket {
		for _, x := range b {
			if x.Key == k {
				return x
			}
		}
		return Bucket{}
	}
	if b := find(s.ByLabel, "simple"); b.Count != 3 || b.Tokens != 800 {
		t.Errorf("by_label simple: %+v", b)
	}
	if b := find(s.BySource, "vector"); b.Count != 2 || b.Tokens != 3100 {
		t.Errorf("by_source vector: %+v", b)
	}
	if b := find(s.ByModel, "gpt-4o"); b.Count != 1 {
		t.Errorf("by_model: %+v", s.ByModel)
	}
	want := map[string]int64{"0-500": 2, "500-2k": 1, "2k-8k": 1, "8k-32k": 0, "32k+": 0}
	if len(s.ByTokenBucket) != 5 {
		t.Fatalf("buckets: %+v", s.ByTokenBucket)
	}
	for _, b := range s.ByTokenBucket {
		if want[b.Key] != b.Count {
			t.Errorf("bucket %s = %d, want %d", b.Key, b.Count, want[b.Key])
		}
	}
	if _, err := json.Marshal(s); err != nil {
		t.Fatal(err)
	}
}

// The vector identity resolver is installed after New() loads the index; installing it
// must re-evaluate sample compatibility, otherwise valid samples stay skipped after restart.
func TestSetVectorIdentityReloads(t *testing.T) {
	db := newTestDB(t)
	st := newStore(t, db, settings.SmartRoute{Enabled: true, VirtualModel: "auto", SimpleGroupID: 1, ComplexGroupID: 2, Threshold: 0.5, TopK: 5})
	if err := st.SetVector(settings.Vector{AccountID: 7, Model: "embed"}); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.RouteSample{Label: LabelSimple, Text: "hi", Vector: vector.Encode([]float32{1, 0, 0}), VectorDim: 3, VectorModel: "7|http://vec|embed-v2"})
	e := New(db, st, nil) // startup: identity falls back to "7:embed", so the sample looks stale
	if n := len(e.Samples()); n != 0 {
		t.Fatalf("before identity is known the new-format sample is treated as stale, got %d", n)
	}
	e.SetVectorIdentity(func() string { return "7|http://vec|embed-v2" })
	if n := len(e.Samples()); n != 1 {
		t.Fatalf("expected the sample to load once the identity matches, got %d", n)
	}
}
