// Package model defines the persistent entities of the gateway.
package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// StringList is a JSON-encoded []string column.
type StringList []string

func (s StringList) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal(s)
	return string(b), err
}

func (s *StringList) Scan(v any) error {
	switch t := v.(type) {
	case nil:
		*s = StringList{}
		return nil
	case []byte:
		return json.Unmarshal(t, s)
	case string:
		return json.Unmarshal([]byte(t), s)
	}
	return errors.New("StringList: unsupported scan type")
}

// JSON is an opaque JSON column.
type JSON json.RawMessage

func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return "null", nil
	}
	return string(j), nil
}

func (j *JSON) Scan(v any) error {
	switch t := v.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = append((*j)[:0], t...)
	case string:
		*j = JSON(t)
	default:
		return errors.New("JSON: unsupported scan type")
	}
	return nil
}

func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}

func (j *JSON) UnmarshalJSON(b []byte) error {
	*j = append((*j)[:0], b...)
	return nil
}

// Protocol types (what a model does).
const (
	TypeText      = "text"
	TypeImage     = "image"
	TypeEmbedding = "embedding"
)

// Wire protocols (how a request is shaped).
const (
	ProtoOpenAIChat        = "openai-completions"
	ProtoOpenAIResponses   = "openai-responses"
	ProtoAnthropicMessages = "anthropic-messages"
	ProtoOpenAIEmbeddings  = "openai-embeddings"
	ProtoOpenAIImages      = "openai-images"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

const (
	HealthAvailable   = "available"
	HealthCooling     = "cooling"
	HealthUnavailable = "unavailable"
)

type User struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	Username           string     `gorm:"size:64;uniqueIndex" json:"username"`
	PasswordHash       string     `gorm:"size:255" json:"-"`
	Role               string     `gorm:"size:16;index" json:"role"`
	GroupID            uint       `gorm:"index" json:"group_id"`
	Group              *UserGroup `json:"group,omitempty"`
	Enabled            bool       `gorm:"default:true" json:"enabled"`
	Locked             bool       `json:"locked"`
	FailedLogins       int        `json:"-"`
	MustChangePassword bool       `json:"must_change_password"`
	SessionVersion     int        `json:"-"`
	Note               string     `gorm:"size:255" json:"note"`
	LastLoginAt        *time.Time `json:"last_login_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type UserGroup struct {
	ID                uint         `gorm:"primaryKey" json:"id"`
	Name              string       `gorm:"size:64;uniqueIndex" json:"name"`
	MaxConcurrency    int          `json:"max_concurrency"`
	KeyMaxConcurrency int          `json:"key_max_concurrency"`
	TokenQuota        int64        `json:"token_quota"`
	IsDefault         bool         `gorm:"index" json:"is_default"`
	Enabled           bool         `gorm:"default:true" json:"enabled"`
	Note              string       `gorm:"size:255" json:"note"`
	ModelGroups       []ModelGroup `gorm:"many2many:user_group_model_groups" json:"model_groups,omitempty"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

type ModelGroup struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Name      string     `gorm:"size:64;uniqueIndex" json:"name"`
	Type      string     `gorm:"size:16;index" json:"type"`
	Models    StringList `gorm:"type:text" json:"models"`
	Note      string     `gorm:"size:255" json:"note"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Account is an upstream provider account (账号池).
type Account struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Name        string         `gorm:"size:64" json:"name"`
	Provider    string         `gorm:"size:32;index" json:"provider"`
	AccountType string         `gorm:"size:32" json:"account_type"`
	Type        string         `gorm:"size:16;index" json:"type"`
	BaseURL     string         `gorm:"size:512" json:"base_url"`
	APIKeyEnc   string         `gorm:"size:1024" json:"-"`
	Protocols   StringList     `gorm:"type:text" json:"protocols"`
	Mappings    []ModelMapping `gorm:"constraint:OnDelete:CASCADE" json:"mappings"`
	TestModel   string         `gorm:"size:128" json:"test_model"`
	Priority    int            `gorm:"index" json:"priority"`
	// Weight orders accounts that share a priority: they are tried in weighted-random
	// order, so a weight of 5 next to a weight of 95 is a 5% canary. Default 1.
	Weight         int `gorm:"not null;default:1" json:"weight"`
	MaxConcurrency int `json:"max_concurrency"`
	// PassthroughModels: any model name without an explicit mapping is forwarded to this
	// account unchanged (typed by the account). Lets unknown coding clients use their
	// default model names without a mapping per name.
	PassthroughModels bool       `gorm:"not null;default:false" json:"passthrough_models"`
	Enabled           bool       `gorm:"default:true;index" json:"enabled"`
	Health            string     `gorm:"size:16;default:available" json:"health"`
	CooldownUntil     *time.Time `json:"cooldown_until"`
	LastError         string     `gorm:"size:512" json:"last_error"`
	Note              string     `gorm:"size:255" json:"note"`
	Extra             JSON       `gorm:"type:text" json:"extra"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type ModelMapping struct {
	ID            uint   `gorm:"primaryKey" json:"id"`
	AccountID     uint   `gorm:"index:idx_mapping_account_req,unique" json:"account_id"`
	RequestModel  string `gorm:"size:128;index:idx_mapping_account_req,unique;index" json:"request_model"`
	UpstreamModel string `gorm:"size:128" json:"upstream_model"`
}

type APIKey struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	UserID     uint       `gorm:"index" json:"user_id"`
	User       *User      `json:"user,omitempty"`
	Name       string     `gorm:"size:64" json:"name"`
	KeyHash    string     `gorm:"size:64;uniqueIndex" json:"-"`
	Prefix     string     `gorm:"size:8" json:"prefix"`
	Suffix     string     `gorm:"size:8" json:"suffix"`
	Enabled    bool       `gorm:"default:true" json:"enabled"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Usage status of a call log: how trustworthy the token counts are.
const (
	UsageConfirmed = "confirmed" // upstream reported complete usage
	UsagePartial   = "partial"   // stream interrupted; some usage seen (e.g. prompt only)
	UsageUnknown   = "unknown"   // upstream consumed the request but reported no usage
	UsageNone      = "none"      // request never reached / was never processed by an upstream
)

// CallLog records one data-plane request. RequestID is unique so journal replays are idempotent.
type CallLog struct {
	ID               uint   `gorm:"primaryKey" json:"id"`
	RequestID        string `gorm:"size:40;uniqueIndex:uq_call_logs_request_id" json:"request_id"`
	UserID           uint   `gorm:"index" json:"user_id"`
	Username         string `gorm:"size:64" json:"username"`
	GroupID          uint   `gorm:"index" json:"group_id"`
	GroupName        string `gorm:"size:64" json:"group_name"`
	APIKeyID         uint   `gorm:"index" json:"api_key_id"`
	APIKeyName       string `gorm:"size:64" json:"api_key_name"`
	AccountID        uint   `gorm:"index" json:"account_id"`
	AccountName      string `gorm:"size:64" json:"account_name"`
	Provider         string `gorm:"size:32;index" json:"provider"`
	RequestModel     string `gorm:"size:128;index" json:"request_model"`
	UpstreamModel    string `gorm:"size:128" json:"upstream_model"`
	ModelGroup       string `gorm:"size:64" json:"model_group"`
	APIType          string `gorm:"size:16;index" json:"api_type"`
	ClientProtocol   string `gorm:"size:32" json:"client_protocol"`
	UpstreamProtocol string `gorm:"size:32" json:"upstream_protocol"`
	Stream           bool   `json:"stream"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	TokensKnown      bool   `json:"tokens_known"`
	UsageStatus      string `gorm:"size:12;index" json:"usage_status"` // confirmed | partial | unknown | none
	EstPromptTokens  int64  `json:"est_prompt_tokens"`                 // rough estimate (request bytes / 4) when usage is not confirmed
	// UsageCorrected marks a request whose request-level usage was re-derived from its
	// attempt records during an upgrade (older gateways only kept the final attempt).
	UsageCorrected bool   `gorm:"not null;default:false" json:"usage_corrected"`
	Result         string `gorm:"size:16;index" json:"result"` // success | client_error | upstream_error | blocked
	StatusCode     int    `gorm:"index" json:"status_code"`
	LatencyMs      int64  `json:"latency_ms"`
	// Timing semantics (all from request start unless noted):
	//   LatencyMs         whole request until the response to the client is complete
	//   FirstByteMs       upstream response headers received (upstream time to first byte)
	//   UpstreamLatencyMs from sending to the upstream until the relay finished; for
	//                     streams this interleaves upstream reads and client writes
	//   ClientWriteMs     time spent blocked in Write/Flush towards the client (streams);
	//                     UpstreamLatencyMs - ClientWriteMs approximates pure upstream wait
	UpstreamLatencyMs int64     `json:"upstream_latency_ms"`
	FirstByteMs       int64     `json:"first_byte_ms"`
	ClientWriteMs     int64     `json:"client_write_ms"`
	Error             string    `gorm:"size:1024" json:"error"`
	Attempts          JSON      `gorm:"type:text" json:"attempts"`
	RouteLabel        string    `gorm:"size:16" json:"route_label"`
	ClientIP          string    `gorm:"size:64" json:"client_ip"`
	CreatedAt         time.Time `gorm:"index" json:"created_at"`
}

// UsageHourly is a pre-aggregated rollup used by dashboards.
type UsageHourly struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Hour         time.Time `gorm:"index:idx_usage_dims,unique;index" json:"hour"`
	UserID       uint      `gorm:"index:idx_usage_dims,unique" json:"user_id"`
	GroupID      uint      `gorm:"index:idx_usage_dims,unique" json:"group_id"`
	APIKeyID     uint      `gorm:"index:idx_usage_dims,unique" json:"api_key_id"`
	AccountID    uint      `gorm:"index:idx_usage_dims,unique" json:"account_id"`
	Provider     string    `gorm:"size:32;index:idx_usage_dims,unique" json:"provider"`
	RequestModel string    `gorm:"size:128;index:idx_usage_dims,unique" json:"request_model"`
	ModelGroup   string    `gorm:"size:64;index:idx_usage_dims,unique" json:"model_group"`
	APIType      string    `gorm:"size:16;index:idx_usage_dims,unique" json:"api_type"`
	// Requests / Success / Failed / LatencyMs / UnknownUsage count requests and are booked
	// on the account that produced the final response. Tokens and Attempts are booked on
	// the account of the attempt that consumed them, so a retried request can spread its
	// tokens over several rows while still counting as one request.
	Requests         int64 `json:"requests"`
	Attempts         int64 `gorm:"not null;default:0" json:"attempts"`
	Success          int64 `json:"success"`
	Failed           int64 `json:"failed"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	CachedTokens     int64 `json:"cached_tokens"`
	UnknownUsage     int64 `json:"unknown_usage"` // requests whose usage is partial or unknown
	LatencyMs        int64 `json:"latency_ms"`
}

// RouteSample is a labelled example used by smart routing.
type RouteSample struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Label       string    `gorm:"size:16;index" json:"label"` // simple | complex
	Text        string    `gorm:"type:text" json:"text"`
	Threshold   float64   `json:"threshold"` // 0 = use global
	Note        string    `gorm:"size:255" json:"note"`
	Vector      []byte    `gorm:"type:blob" json:"-"`
	VectorDim   int       `json:"vector_dim"`
	VectorModel string    `gorm:"size:160" json:"vector_model"` // "<account id>:<model>" the vector was built with
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type RouteDecision struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	RequestID      string    `gorm:"size:40;index" json:"request_id"`
	Label          string    `gorm:"size:16;index" json:"label"`
	Source         string    `gorm:"size:16;index" json:"source"` // rule | context | vector | fallback | preview
	Confidence     float64   `json:"confidence"`
	SelectedModel  string    `gorm:"size:128;index" json:"selected_model"`
	ModelGroup     string    `gorm:"size:64" json:"model_group"`
	NormalizedText string    `gorm:"type:text" json:"normalized_text"`
	TopK           JSON      `gorm:"type:text" json:"top_k"`
	RequestType    string    `gorm:"size:16" json:"request_type"`
	TotalTokens    int64     `json:"total_tokens"`
	LatencyMs      int64     `json:"latency_ms"`
	Failed         bool      `json:"failed"`
	CreatedAt      time.Time `gorm:"index" json:"created_at"`
}

type PolicyGroup struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:64;uniqueIndex" json:"name"`
	Action      string    `gorm:"size:16" json:"action"`     // block | audit
	RiskLevel   string    `gorm:"size:16" json:"risk_level"` // low | medium | high
	Enabled     bool      `gorm:"default:true" json:"enabled"`
	Description string    `gorm:"size:255" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SensitiveWord struct {
	ID            uint         `gorm:"primaryKey" json:"id"`
	PolicyGroupID uint         `gorm:"index" json:"policy_group_id"`
	PolicyGroup   *PolicyGroup `json:"policy_group,omitempty"`
	Word          string       `gorm:"size:255;index" json:"word"`
	Note          string       `gorm:"size:255" json:"note"`
	Enabled       bool         `gorm:"default:true" json:"enabled"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

type AuditSample struct {
	ID            uint         `gorm:"primaryKey" json:"id"`
	PolicyGroupID uint         `gorm:"index" json:"policy_group_id"`
	PolicyGroup   *PolicyGroup `json:"policy_group,omitempty"`
	Text          string       `gorm:"type:text" json:"text"`
	Note          string       `gorm:"size:255" json:"note"`
	Enabled       bool         `gorm:"default:true" json:"enabled"`
	Vector        []byte       `gorm:"type:blob" json:"-"`
	VectorDim     int          `json:"vector_dim"`
	VectorModel   string       `gorm:"size:160" json:"vector_model"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

type AuditLog struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	RequestID     string    `gorm:"size:40;index" json:"request_id"`
	UserID        uint      `gorm:"index" json:"user_id"`
	Username      string    `gorm:"size:64" json:"username"`
	RequestModel  string    `gorm:"size:128" json:"request_model"`
	Protocol      string    `gorm:"size:32" json:"protocol"`
	Action        string    `gorm:"size:16;index" json:"action"`
	RiskLevel     string    `gorm:"size:16;index" json:"risk_level"`
	DetectMethod  string    `gorm:"size:16;index" json:"detect_method"` // keyword | semantic
	PolicyGroupID uint      `gorm:"index" json:"policy_group_id"`
	PolicyGroup   string    `gorm:"size:64" json:"policy_group"`
	Evidence      string    `gorm:"size:512" json:"evidence"`
	Confidence    float64   `json:"confidence"`
	StatusCode    int       `json:"status_code"`
	Hits          JSON      `gorm:"type:text" json:"hits"`
	Snippet       string    `gorm:"size:512" json:"snippet"`
	CreatedAt     time.Time `gorm:"index" json:"created_at"`
}

type Setting struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

func All() []any {
	return []any{
		&User{}, &UserGroup{}, &ModelGroup{}, &Account{}, &ModelMapping{}, &APIKey{},
		&CallLog{}, &UsageHourly{}, &RouteSample{}, &RouteDecision{},
		&PolicyGroup{}, &SensitiveWord{}, &AuditSample{}, &AuditLog{}, &Setting{},
	}
}
