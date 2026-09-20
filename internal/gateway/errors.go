package gateway

import (
	"encoding/json"
	"net/http"

	"yzapi/internal/model"
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
	ErrKeyExpired       = newErr(401, "api_key_expired", "API key has expired")
	ErrKeyModelDenied   = newErr(403, "key_model_not_allowed", "This API key is not allowed to use this model")
	ErrRateLimited      = newErr(429, "rate_limited", "Requests per minute limit reached")
	ErrTokenRateLimited = newErr(429, "token_rate_limited", "Tokens per minute limit reached")
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
	ErrEndpointNotFound = newErr(404, "not_found", "not found")
	ErrBodyTooLarge     = newErr(413, "request_too_large", "Request body exceeds the configured limit")
	ErrBadJSON          = newErr(400, "invalid_json", "Request body is not valid JSON")
	ErrContentBlocked   = newErr(400, "content_blocked", "Request blocked by content compliance policy")
	ErrConversionFailed = newErr(400, "protocol_conversion_failed", "Request could not be converted to a protocol supported by the upstream")
	ErrMemoryBudget     = newErr(503, "gateway_overloaded", "Gateway request-body memory budget exhausted, try again later")
	ErrComplianceDown   = newErr(503, "compliance_unavailable", "Content compliance service is unavailable and the policy requires blocking")
)

func writeError(w http.ResponseWriter, anthropic bool, e *GatewayError) {
	if anthropic {
		writeErrorProto(w, model.ProtoAnthropicMessages, e)
	} else {
		writeErrorProto(w, model.ProtoOpenAIChat, e)
	}
}

// geminiStatus maps an HTTP status to the google.rpc.Code name Gemini clients expect.
func geminiStatus(status int) string {
	switch status {
	case 400, 413:
		return "INVALID_ARGUMENT"
	case 401:
		return "UNAUTHENTICATED"
	case 403:
		return "PERMISSION_DENIED"
	case 404:
		return "NOT_FOUND"
	case 429:
		return "RESOURCE_EXHAUSTED"
	case 503:
		return "UNAVAILABLE"
	}
	return "INTERNAL"
}

// writeErrorProto writes e in the error shape of the client's wire protocol.
func writeErrorProto(w http.ResponseWriter, proto string, e *GatewayError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	var body any
	switch proto {
	case model.ProtoGemini:
		body = map[string]any{
			"error": map[string]any{"code": e.Status, "message": e.Message, "status": geminiStatus(e.Status)},
		}
	case model.ProtoAnthropicMessages:
		body = map[string]any{
			"type":  "error",
			"error": map[string]any{"type": e.Type, "message": e.Message},
		}
	default:
		body = map[string]any{
			"error": map[string]any{"message": e.Message, "type": e.Type, "code": e.Code, "param": nil},
		}
	}
	_ = json.NewEncoder(w).Encode(body)
}
