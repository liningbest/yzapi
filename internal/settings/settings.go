// Package settings holds runtime-tunable configuration persisted in the settings table
// and cached in memory. Reads are lock-free via atomic pointer swap.
package settings

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"yzapi/internal/model"
)

type Basic struct {
	BaseURL            string `json:"base_url"`
	LogRetentionDays   int    `json:"log_retention_days"`
	ProtocolConversion bool   `json:"protocol_conversion"`
	SiteName           string `json:"site_name"`
}

type Performance struct {
	MaxConcurrency       int `json:"max_concurrency"`
	QueueSize            int `json:"queue_size"`
	QueueTimeoutSec      int `json:"queue_timeout_sec"`
	RequestTimeoutSec    int `json:"request_timeout_sec"`
	StreamIdleTimeout    int `json:"stream_idle_timeout_sec"`
	MaxBodyKB            int `json:"max_body_kb"`
	CooldownSec          int `json:"cooldown_sec"`
	MaxRetries           int `json:"max_retries"`
	UpstreamConnTimeout  int `json:"upstream_connect_timeout_sec"`
	MaxBodyMemoryMB      int `json:"max_body_memory_mb"`     // total bytes of request bodies held in memory
	VectorMaxConcurrency int `json:"vector_max_concurrency"` // concurrent embedding calls
	VectorTimeoutSec     int `json:"vector_timeout_sec"`     // per embedding call
}

type Vector struct {
	AccountID uint   `json:"account_id"`
	Model     string `json:"model"`
}

type SmartRoute struct {
	Enabled        bool    `json:"enabled"`
	VirtualModel   string  `json:"virtual_model"`
	SimpleGroupID  uint    `json:"simple_group_id"`
	ComplexGroupID uint    `json:"complex_group_id"`
	Threshold      float64 `json:"threshold"`
	ConfidenceGap  float64 `json:"confidence_gap"`
	TopK           int     `json:"top_k"`
	RuleMaxChars   int     `json:"rule_max_chars"`  // texts shorter than this are "simple" by rule when no sample matches
	ContextComplex int     `json:"context_complex"` // messages count above which requests are "complex" by context
}

type Compliance struct {
	Enabled           bool    `json:"enabled"`
	SemanticThreshold float64 `json:"semantic_threshold"`
	CheckSystemPrompt bool    `json:"check_system_prompt"`
	// OnFailure decides what happens when the semantic check cannot run (vector service
	// down / timeout): "allow" forwards with keyword-only screening and records a degraded
	// audit entry; "block" rejects the request with 503.
	OnFailure string `json:"on_failure"`
}

type Elasticsearch struct {
	Enabled       bool   `json:"enabled"`
	URL           string `json:"url"`
	AuthType      string `json:"auth_type"` // apikey | basic
	APIKey        string `json:"api_key"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	IndexPrefix   string `json:"index_prefix"`
	RequestKB     int    `json:"request_kb"`
	ResponseKB    int    `json:"response_kb"`
	RetentionDays int    `json:"retention_days"`
}

type All struct {
	Basic         Basic         `json:"basic"`
	Performance   Performance   `json:"performance"`
	Vector        Vector        `json:"vector"`
	SmartRoute    SmartRoute    `json:"smart_route"`
	Compliance    Compliance    `json:"compliance"`
	Elasticsearch Elasticsearch `json:"elasticsearch"`
}

func Defaults() All {
	return All{
		Basic: Basic{
			BaseURL:            "http://127.0.0.1:8080/v1",
			LogRetentionDays:   30,
			ProtocolConversion: true,
			SiteName:           "YZ AI Gateway",
		},
		Performance: Performance{
			MaxConcurrency:       512,
			QueueSize:            1024,
			QueueTimeoutSec:      30,
			RequestTimeoutSec:    300,
			StreamIdleTimeout:    120,
			MaxBodyKB:            20480,
			CooldownSec:          60,
			MaxRetries:           3,
			UpstreamConnTimeout:  10,
			MaxBodyMemoryMB:      512,
			VectorMaxConcurrency: 16,
			VectorTimeoutSec:     10,
		},
		SmartRoute: SmartRoute{
			VirtualModel:   "yz-auto",
			Threshold:      0.72,
			ConfidenceGap:  0.0,
			TopK:           5,
			RuleMaxChars:   0,
			ContextComplex: 0,
		},
		Compliance: Compliance{
			Enabled:           false,
			SemanticThreshold: 0.85,
			OnFailure:         "allow",
		},
		Elasticsearch: Elasticsearch{
			AuthType:      "apikey",
			IndexPrefix:   "yzapi",
			RequestKB:     64,
			ResponseKB:    64,
			RetentionDays: 30,
		},
	}
}

type Store struct {
	db      *gorm.DB
	cur     atomic.Pointer[All]
	onApply []func(All)
	saveMu  sync.Mutex // one save -> reload -> apply sequence at a time
}

func New(db *gorm.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// OnApply registers a callback invoked after settings change.
func (s *Store) OnApply(fn func(All)) { s.onApply = append(s.onApply, fn) }

func (s *Store) Get() All { return *s.cur.Load() }

func (s *Store) Reload() error {
	all := Defaults()
	var rows []model.Setting
	if err := s.db.Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		switch r.Key {
		case "basic":
			_ = json.Unmarshal([]byte(r.Value), &all.Basic)
		case "performance":
			_ = json.Unmarshal([]byte(r.Value), &all.Performance)
		case "vector":
			_ = json.Unmarshal([]byte(r.Value), &all.Vector)
		case "smart_route":
			_ = json.Unmarshal([]byte(r.Value), &all.SmartRoute)
		case "compliance":
			_ = json.Unmarshal([]byte(r.Value), &all.Compliance)
		case "elasticsearch":
			_ = json.Unmarshal([]byte(r.Value), &all.Elasticsearch)
		}
	}
	s.cur.Store(&all)
	return nil
}

func (s *Store) save(key string, v any) error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	row := model.Setting{Key: key, Value: string(b), UpdatedAt: time.Now()}
	if err := s.db.Save(&row).Error; err != nil {
		return err
	}
	if err := s.Reload(); err != nil {
		return err
	}
	cur := s.Get()
	for _, fn := range s.onApply {
		fn(cur)
	}
	return nil
}

func (s *Store) SetBasic(v Basic) error                 { return s.save("basic", v) }
func (s *Store) SetPerformance(v Performance) error     { return s.save("performance", v) }
func (s *Store) SetVector(v Vector) error               { return s.save("vector", v) }
func (s *Store) SetSmartRoute(v SmartRoute) error       { return s.save("smart_route", v) }
func (s *Store) SetCompliance(v Compliance) error       { return s.save("compliance", v) }
func (s *Store) SetElasticsearch(v Elasticsearch) error { return s.save("elasticsearch", v) }
