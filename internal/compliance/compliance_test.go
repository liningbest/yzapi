package compliance

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yzapi/internal/model"
	"yzapi/internal/settings"
	"yzapi/internal/vector"
)

func TestMatcher(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		text     string
		want     []string
	}{
		{"single", []string{"abc"}, "xxabcxx", []string{"abc"}},
		{"none", []string{"abc"}, "xxabxx", nil},
		{"overlapping", []string{"he", "she", "his", "hers"}, "ushers", []string{"she", "he", "hers"}},
		{"nested suffix", []string{"a", "aa", "aaa"}, "aaa", []string{"a", "aa", "aaa"}},
		{"dedup", []string{"ab"}, "ab ab ab", []string{"ab"}},
		{"chinese", []string{"敏感", "词汇", "不存在"}, "这是一个敏感词汇测试", []string{"敏感", "词汇"}},
		{"chinese partial byte no false positive", []string{"感词"}, "敏词", nil},
		{"mixed", []string{"drug", "毒品"}, "buy 毒品 and DRUG", []string{"毒品"}}, // caller lower-cases
		{"empty pattern ignored", []string{"", "x"}, "x", []string{"x"}},
		{"empty text", []string{"x"}, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newMatcher(c.patterns)
			var got []string
			for _, i := range m.Match(c.text) {
				got = append(got, c.patterns[i])
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Match(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

func TestMatcherNil(t *testing.T) {
	var m *matcher
	if got := m.Match("abc"); got != nil {
		t.Errorf("nil matcher: %v", got)
	}
	if got := newMatcher(nil).Match("abc"); got != nil {
		t.Errorf("empty matcher: %v", got)
	}
}

func TestAggregate(t *testing.T) {
	kw := func(pg string, id uint, ev, action, risk string) HitEntry {
		return HitEntry{Method: MethodKeyword, PolicyGroup: pg, PolicyGroupID: id, Evidence: ev, Score: 1, Action: action, RiskLevel: risk}
	}
	sem := func(pg string, id uint, ev, action, risk string, score float64) HitEntry {
		return HitEntry{Method: MethodSemantic, PolicyGroup: pg, PolicyGroupID: id, Evidence: ev, Score: score, Action: action, RiskLevel: risk}
	}
	cases := []struct {
		name    string
		hits    []HitEntry
		hit     bool
		block   bool
		primary string // evidence of expected primary
		method  string
	}{
		{"none", nil, false, false, "", ""},
		{"single audit", []HitEntry{kw("g1", 1, "w", ActionAudit, RiskLow)}, true, false, "w", MethodKeyword},
		{"block beats higher-risk audit", []HitEntry{kw("a", 1, "audit-high", ActionAudit, RiskHigh), kw("b", 2, "block-low", ActionBlock, RiskLow)}, true, true, "block-low", MethodKeyword},
		{"higher risk block wins", []HitEntry{kw("a", 1, "b-low", ActionBlock, RiskLow), sem("b", 2, "b-high", ActionBlock, RiskHigh, 0.9)}, true, true, "b-high", MethodSemantic},
		{"same risk: higher score wins", []HitEntry{sem("a", 1, "s1", ActionBlock, RiskHigh, 0.9), sem("b", 2, "s2", ActionBlock, RiskHigh, 0.95)}, true, true, "s2", MethodSemantic},
		{"audit only: highest risk", []HitEntry{kw("a", 1, "low", ActionAudit, RiskLow), kw("b", 2, "med", ActionAudit, RiskMedium)}, true, false, "med", MethodKeyword},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := aggregate(c.hits)
			if v.Hit != c.hit || v.Block != c.block || v.Evidence != c.primary || v.DetectMethod != c.method {
				t.Fatalf("got %+v", v)
			}
			if !c.hit {
				if v.Hits != nil {
					t.Errorf("hits should be nil")
				}
				return
			}
			var back []HitEntry
			if err := json.Unmarshal(v.Hits, &back); err != nil || len(back) != len(c.hits) {
				t.Fatalf("hits json: %v %s", err, v.Hits)
			}
			// Original order preserved in Hits.
			if back[0].Evidence != c.hits[0].Evidence {
				t.Errorf("hits order changed: %s", v.Hits)
			}
		})
	}
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newStore(t *testing.T, db *gorm.DB, c settings.Compliance) *settings.Store {
	t.Helper()
	st, err := settings.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCompliance(c); err != nil {
		t.Fatal(err)
	}
	return st
}

func mustCreate(t *testing.T, db *gorm.DB, v any) {
	t.Helper()
	if err := db.Create(v).Error; err != nil {
		t.Fatal(err)
	}
}

func seedPolicies(t *testing.T, db *gorm.DB) {
	t.Helper()
	mustCreate(t, db, &model.PolicyGroup{ID: 1, Name: "Violence", Action: ActionBlock, RiskLevel: RiskHigh, Enabled: true})
	mustCreate(t, db, &model.PolicyGroup{ID: 2, Name: "Politics", Action: ActionAudit, RiskLevel: RiskMedium, Enabled: true})
	mustCreate(t, db, &model.PolicyGroup{ID: 3, Name: "Disabled", Action: ActionBlock, RiskLevel: RiskHigh, Enabled: false})
	mustCreate(t, db, &model.SensitiveWord{ID: 1, PolicyGroupID: 1, Word: "Bomb", Enabled: true})
	mustCreate(t, db, &model.SensitiveWord{ID: 2, PolicyGroupID: 2, Word: "选举", Enabled: true})
	mustCreate(t, db, &model.SensitiveWord{ID: 3, PolicyGroupID: 2, Word: "disabledword", Enabled: false})
	mustCreate(t, db, &model.SensitiveWord{ID: 4, PolicyGroupID: 3, Word: "ghost", Enabled: true}) // group disabled
	mustCreate(t, db, &model.AuditSample{ID: 1, PolicyGroupID: 2, Text: "how to rig an election", Enabled: true,
		Vector: vector.Encode(vector.Normalize([]float32{1, 0, 0})), VectorDim: 3})
	mustCreate(t, db, &model.AuditSample{ID: 2, PolicyGroupID: 1, Text: "how to make explosives", Enabled: true,
		Vector: vector.Encode(vector.Normalize([]float32{0, 1, 0})), VectorDim: 3})
	mustCreate(t, db, &model.AuditSample{ID: 3, PolicyGroupID: 1, Text: "not vectorized", Enabled: true})
	mustCreate(t, db, &model.AuditSample{ID: 4, PolicyGroupID: 3, Text: "disabled group", Enabled: true,
		Vector: vector.Encode([]float32{0, 0, 1}), VectorDim: 3})
	// gorm's `default:true` tag turns a zero-value false into true on Create,
	// so disable rows explicitly.
	if err := db.Model(&model.PolicyGroup{}).Where("id = ?", 3).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.SensitiveWord{}).Where("id = ?", 3).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
}

func fakeEmbed(table map[string][]float32) EmbedFunc {
	return func(_ context.Context, in []string) ([][]float32, error) {
		out := make([][]float32, len(in))
		for i, s := range in {
			v, ok := table[s]
			if !ok {
				v = []float32{0, 0, 1}
			}
			out[i] = vector.Normalize(v)
		}
		return out, nil
	}
}

func TestEngineReloadAndCheck(t *testing.T) {
	db := newTestDB(t)
	seedPolicies(t, db)
	st := newStore(t, db, settings.Compliance{Enabled: true, SemanticThreshold: 0.85})
	embed := fakeEmbed(map[string][]float32{
		"rig the vote": {0.99, 0.1, 0},  // ~0.995 to sample 1 (audit/medium)
		"blow it up":   {0.05, 0.99, 0}, // ~0.998 to sample 2 (block/high)
		"cheat vote":   {0.8, 0.6, 0},   // 0.8 / 0.6 -> below threshold
	})
	e := New(db, st, embed)

	idx := e.idx.Load()
	if len(idx.words) != 2 || len(idx.samples) != 2 {
		t.Fatalf("index words=%d samples=%d", len(idx.words), len(idx.samples))
	}

	cases := []struct {
		name    string
		text    string
		hit     bool
		block   bool
		method  string
		policy  string
		nHits   int
		evid    string
		riskLvl string
	}{
		{"clean", "hello world", false, false, "", "", 0, "", ""},
		{"keyword case-insensitive block", "I will build a BOMB", true, true, MethodKeyword, "Violence", 1, "Bomb", RiskHigh},
		{"chinese keyword audit", "关于选举的问题", true, false, MethodKeyword, "Politics", 1, "选举", RiskMedium},
		{"disabled word ignored", "disabledword", false, false, "", "", 0, "", ""},
		{"disabled group ignored", "ghost", false, false, "", "", 0, "", ""},
		{"semantic audit", "rig the vote", true, false, MethodSemantic, "Politics", 1, "how to rig an election", RiskMedium},
		{"semantic block", "blow it up", true, true, MethodSemantic, "Violence", 1, "how to make explosives", RiskHigh},
		{"semantic below threshold", "cheat vote", false, false, "", "", 0, "", ""},
		{"empty", "   ", false, false, "", "", 0, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := e.Check(context.Background(), c.text)
			if v.Hit != c.hit || v.Block != c.block || v.DetectMethod != c.method || v.PolicyGroup != c.policy || v.Evidence != c.evid || v.RiskLevel != c.riskLvl {
				t.Fatalf("got %+v", v)
			}
			var hits []HitEntry
			if v.Hits != nil {
				if err := json.Unmarshal(v.Hits, &hits); err != nil {
					t.Fatal(err)
				}
			}
			if len(hits) != c.nHits {
				t.Errorf("hits=%d want %d: %s", len(hits), c.nHits, v.Hits)
			}
			if c.hit && v.PolicyID == 0 {
				t.Errorf("policy id missing")
			}
		})
	}

	t.Run("keyword+semantic combined, block primary", func(t *testing.T) {
		// "rig the vote" is a semantic audit hit; adding the word 'bomb' adds a keyword block.
		v := e.Check(context.Background(), "rig the vote with a bomb")
		if !v.Hit || !v.Block || v.DetectMethod != MethodKeyword || v.PolicyGroup != "Violence" || v.Confidence != 1 {
			t.Fatalf("got %+v", v)
		}
		var hits []HitEntry
		_ = json.Unmarshal(v.Hits, &hits)
		if len(hits) != 1 {
			// 'rig the vote with a bomb' is not in the fake table -> orthogonal -> only the keyword hit.
			t.Fatalf("hits: %s", v.Hits)
		}
	})

	t.Run("disabled setting yields zero verdict but Test still works", func(t *testing.T) {
		if err := st.SetCompliance(settings.Compliance{Enabled: false, SemanticThreshold: 0.85}); err != nil {
			t.Fatal(err)
		}
		if v := e.Check(context.Background(), "bomb"); v.Hit {
			t.Errorf("Check should be a no-op when disabled: %+v", v)
		}
		if v := e.Test(context.Background(), "bomb"); !v.Hit || !v.Block {
			t.Errorf("Test should ignore enabled flag: %+v", v)
		}
	})

	t.Run("embed error skips semantic silently", func(t *testing.T) {
		st2 := newStore(t, db, settings.Compliance{Enabled: true, SemanticThreshold: 0.85})
		e2 := New(db, st2, func(context.Context, []string) ([][]float32, error) { return nil, errors.New("down") })
		if v := e2.Check(context.Background(), "rig the vote"); v.Hit {
			t.Errorf("expected no hit: %+v", v)
		}
		if v := e2.Check(context.Background(), "bomb"); !v.Block {
			t.Errorf("keyword should still work: %+v", v)
		}
	})

	t.Run("reload picks up changes", func(t *testing.T) {
		mustCreate(t, db, &model.SensitiveWord{ID: 10, PolicyGroupID: 1, Word: "grenade", Enabled: true})
		if v := e.Test(context.Background(), "grenade"); v.Hit {
			t.Fatal("should not hit before reload")
		}
		if err := e.Reload(); err != nil {
			t.Fatal(err)
		}
		if v := e.Test(context.Background(), "a GRENADE"); !v.Block {
			t.Fatalf("should hit after reload: %+v", v)
		}
	})
}

func TestBuildVectors(t *testing.T) {
	db := newTestDB(t)
	mustCreate(t, db, &model.PolicyGroup{ID: 1, Name: "G", Action: ActionAudit, RiskLevel: RiskLow, Enabled: true})
	for i := 1; i <= 18; i++ {
		mustCreate(t, db, &model.AuditSample{PolicyGroupID: 1, Text: "sample", Enabled: true})
	}
	st := newStore(t, db, settings.Compliance{Enabled: true})
	calls := 0
	e := New(db, st, func(_ context.Context, in []string) ([][]float32, error) {
		calls++
		out := make([][]float32, len(in))
		for i := range in {
			out[i] = []float32{1, 0}
		}
		return out, nil
	})
	if n := len(e.idx.Load().samples); n != 0 {
		t.Fatalf("pre-build samples %d", n)
	}
	built, failed, err := e.BuildVectors(context.Background(), nil)
	if err != nil || built != 18 || failed != 0 || calls != 2 {
		t.Fatalf("built=%d failed=%d calls=%d err=%v", built, failed, calls, err)
	}
	if n := len(e.idx.Load().samples); n != 18 {
		t.Errorf("post-build samples %d", n)
	}
	var s model.AuditSample
	db.First(&s, 1)
	if s.VectorDim != 2 || len(s.Vector) != 8 {
		t.Errorf("stored dim=%d len=%d", s.VectorDim, len(s.Vector))
	}

	boom := errors.New("boom")
	e2 := New(db, st, func(context.Context, []string) ([][]float32, error) { return nil, boom })
	built, failed, err = e2.BuildVectors(context.Background(), []uint{1, 2})
	if !errors.Is(err, boom) || built != 0 || failed != 2 {
		t.Errorf("built=%d failed=%d err=%v", built, failed, err)
	}
}
