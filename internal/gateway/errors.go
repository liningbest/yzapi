package gateway

import (
	"encoding/json"
	"net/http"
)

// GatewayError is returned to clients in OpenAI or Anthropic error format.
type GatewayError struct {
	Status  int    `json:"-"`
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *GatewayError) Error() string { return e.Message }

func newErr(status int, code, msg string) *GatewayError {
	t := "invalid_request_error"
	switch {
	case status == 401:
		t = "authentication_error"
	case status == 403:
		t = "permission_error"
	case status == 404:
		t = "not_found_error"
	case status == 429:
		t = "rate_limit_error"
	case status >= 500:
		t = "api_error"
	}
	return &GatewayError{Status: status, Type: t, Code: code, Message: msg}
}

var (
	ErrUnauthorized     = newErr(401, "invalid_api_key", "Invalid or missing API key")
	ErrKeyDisabled      = newErr(401, "api_key_disabled", "API key is disabled")
	ErrUserDisabled     = newErr(403, "user_disabled", "User account is disabled")
	ErrGroupDisabled    = newErr(403, "group_disabled", "User group is disabled")
	ErrModelNotFound    = newErr(404, "model_not_found", "The requested model does not exist")
	ErrModelNotAllowed  = newErr(403, "model_not_allowed", "Your group is not allowed to use this model")
	ErrQuotaExceeded    = newErr(429, "quota_exceeded", "Token quota for your group has been exhausted")
	ErrGatewayBusy      = newErr(429, "gateway_busy", "Gateway concurrency limit reached, request queue is full")
	ErrQueueTimeout     = newErr(429, "queue_timeout", "Request timed out while waiting in the gateway queue")
	ErrGroupBusy        = newErr(429, "group_rate_limited", "User group concurrency limit reached")
	ErrKeyBusy          = newErr(429, "key_rate_limited", "API key concurrency limit reached")
	ErrNoUpstream       = newErr(503, "no_available_upstream", "No healthy upstream account can serve this model")
	ErrBodyTooLarge     = newErr(413, "request_too_large", "Request body exceeds the configured limit")
	ErrBadJSON          = newErr(400, "invalid_json", "Request body is not valid JSON")
	ErrContentBlocked   = newErr(400, "content_blocked", "Request blocked by content compliance policy")
	ErrConversionFailed = newErr(400, "protocol_conversion_failed", "Request could not be converted to a protocol supported by the upstream")
	ErrMemoryBudget     = newErr(503, "gateway_overloaded", "Gateway request-body memory budget exhausted, try again later")
	ErrComplianceDown   = newErr(503, "compliance_unavailable", "Content compliance service is unavailable and the policy requires blocking")
)

func writeError(w http.ResponseWriter, anthropic bool, e *GatewayError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	var body any
	if anthropic {
		body = map[string]any{
			"type":  "error",
			"error": map[string]any{"type": e.Type, "message": e.Message},
		}
	} else {
		body = map[string]any{
			"error": map[string]any{"message": e.Message, "type": e.Type, "code": e.Code, "param": nil},
		}
	}
	_ = json.NewEncoder(w).Encode(body)
}
