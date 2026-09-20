// Package provider describes the built-in upstream providers.
package provider

import "yzapi/internal/model"

type AccountType struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url,omitempty"`
	// Protocols narrows the provider's protocols for this endpoint: many Chinese
	// providers expose an OpenAI-compatible base and a separate Anthropic-compatible
	// base (for Claude Code), which must not be mixed under one URL.
	Protocols []string `json:"protocols,omitempty"`
}

type Provider struct {
	Key          string        `json:"key"`
	Name         string        `json:"name"`
	BaseURL      string        `json:"base_url"`
	Types        []string      `json:"types"`     // text/image/embedding supported
	Protocols    []string      `json:"protocols"` // wire protocols natively supported
	AccountTypes []AccountType `json:"account_types,omitempty"`
	AuthHeader   string        `json:"auth_header"` // bearer | x-api-key
	Discover     bool          `json:"discover"`    // supports GET /models
	Custom       bool          `json:"custom"`
	Icon         string        `json:"icon"`
	// Endpoints pre-fills the endpoint paths of a custom (non-chat JSON) account.
	Endpoints []string `json:"endpoints,omitempty"`
	// DefaultMappings pre-fills the model mappings of a new account for providers with
	// no model-listing API (the admin form and the create API use them when the
	// request carries no mappings and passthrough is off).
	DefaultMappings []DefaultMapping `json:"default_mappings,omitempty"`
}

// DefaultMapping is one preset request-model -> upstream-model pair.
type DefaultMapping struct {
	RequestModel  string `json:"request_model"`
	UpstreamModel string `json:"upstream_model"`
}

var registry = []Provider{
	{Key: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1",
		Types:      []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIResponses, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages},
		AuthHeader: "bearer", Discover: true, Icon: "openai"},
	{Key: "anthropic", Name: "Anthropic", BaseURL: "https://api.anthropic.com/v1",
		Types:      []string{model.TypeText},
		Protocols:  []string{model.ProtoAnthropicMessages},
		AuthHeader: "x-api-key", Discover: true, Icon: "anthropic"},
	{Key: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1",
		Types:     []string{model.TypeText},
		Protocols: []string{model.ProtoOpenAIChat, model.ProtoAnthropicMessages},
		AccountTypes: []AccountType{
			{Key: "openai", Name: "OpenAI 兼容", BaseURL: "https://api.deepseek.com/v1", Protocols: []string{model.ProtoOpenAIChat}},
			{Key: "anthropic", Name: "Anthropic 兼容（Claude Code）", BaseURL: "https://api.deepseek.com/anthropic", Protocols: []string{model.ProtoAnthropicMessages}},
		},
		AuthHeader: "bearer", Discover: true, Icon: "deepseek"},
	{Key: "aliyun", Name: "阿里云百炼", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Types:     []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols: []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages, model.ProtoAnthropicMessages},
		AccountTypes: []AccountType{
			{Key: "payg", Name: "按量付费", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", Protocols: []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages}},
			{Key: "coding", Name: "Coding Plan", BaseURL: "https://coding.dashscope.aliyuncs.com/v1", Protocols: []string{model.ProtoOpenAIChat}},
			{Key: "intl", Name: "国际站", BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", Protocols: []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages}},
			{Key: "anthropic", Name: "Anthropic 兼容（Claude Code）", BaseURL: "https://dashscope.aliyuncs.com/apps/anthropic", Protocols: []string{model.ProtoAnthropicMessages}},
			{Key: "coding-anthropic", Name: "Coding Plan · Anthropic 兼容", BaseURL: "https://coding.dashscope.aliyuncs.com/apps/anthropic", Protocols: []string{model.ProtoAnthropicMessages}},
		},
		AuthHeader: "bearer", Discover: true, Icon: "aliyun"},
	{Key: "tencent", Name: "腾讯云 TokenHub", BaseURL: "https://api.hunyuan.cloud.tencent.com/v1",
		Types:     []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols: []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages},
		AccountTypes: []AccountType{
			{Key: "cn-payg", Name: "中国区按量付费", BaseURL: "https://api.hunyuan.cloud.tencent.com/v1"},
			{Key: "cn-plan", Name: "中国区 Token Plan", BaseURL: "https://api.hunyuan.cloud.tencent.com/v1"},
		},
		AuthHeader: "bearer", Discover: true, Icon: "tencent"},
	{Key: "volcengine", Name: "火山方舟", BaseURL: "https://ark.cn-beijing.volces.com/api/v3",
		Types:      []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages, model.ProtoOpenAIResponses},
		AuthHeader: "bearer", Discover: true, Icon: "volcengine"},
	{Key: "zhipu", Name: "智谱 AI", BaseURL: "https://open.bigmodel.cn/api/paas/v4",
		Types:     []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols: []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages, model.ProtoAnthropicMessages},
		AccountTypes: []AccountType{
			{Key: "payg", Name: "按量付费", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Protocols: []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages}},
			{Key: "coding", Name: "Coding Plan", BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", Protocols: []string{model.ProtoOpenAIChat}},
			{Key: "anthropic", Name: "Anthropic 兼容（Claude Code）", BaseURL: "https://open.bigmodel.cn/api/anthropic", Protocols: []string{model.ProtoAnthropicMessages}},
		},
		AuthHeader: "bearer", Discover: true, Icon: "zhipu"},
	{Key: "moonshot", Name: "Moonshot (Kimi)", BaseURL: "https://api.moonshot.cn/v1",
		Types:     []string{model.TypeText},
		Protocols: []string{model.ProtoOpenAIChat, model.ProtoAnthropicMessages},
		AccountTypes: []AccountType{
			{Key: "openai", Name: "OpenAI 兼容", BaseURL: "https://api.moonshot.cn/v1", Protocols: []string{model.ProtoOpenAIChat}},
			{Key: "anthropic", Name: "Anthropic 兼容（Claude Code）", BaseURL: "https://api.moonshot.cn/anthropic", Protocols: []string{model.ProtoAnthropicMessages}},
		},
		AuthHeader: "bearer", Discover: true, Icon: "moonshot"},
	{Key: "minimax", Name: "MiniMax", BaseURL: "https://api.minimaxi.com/v1",
		Types:     []string{model.TypeText},
		Protocols: []string{model.ProtoOpenAIChat, model.ProtoAnthropicMessages},
		AccountTypes: []AccountType{
			{Key: "openai", Name: "OpenAI 兼容", BaseURL: "https://api.minimaxi.com/v1", Protocols: []string{model.ProtoOpenAIChat}},
			{Key: "anthropic", Name: "Anthropic 兼容（Claude Code）", BaseURL: "https://api.minimaxi.com/anthropic", Protocols: []string{model.ProtoAnthropicMessages}},
		},
		AuthHeader: "bearer", Discover: false, Icon: "minimax"},
	{Key: "stepfun", Name: "阶跃星辰", BaseURL: "https://api.stepfun.com/v1",
		Types:      []string{model.TypeText, model.TypeImage},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIImages},
		AuthHeader: "bearer", Discover: true, Icon: "stepfun"},
	{Key: "qianfan", Name: "百度千帆", BaseURL: "https://qianfan.baidubce.com/v2",
		Types:      []string{model.TypeText, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings},
		AuthHeader: "bearer", Discover: true, Icon: "qianfan"},
	{Key: "siliconflow", Name: "硅基流动", BaseURL: "https://api.siliconflow.cn/v1",
		Types:      []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages},
		AuthHeader: "bearer", Discover: true, Icon: "siliconflow"},
	{Key: "gemini", Name: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta",
		Types:     []string{model.TypeText, model.TypeEmbedding},
		Protocols: []string{model.ProtoGemini, model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings},
		AccountTypes: []AccountType{
			{Key: "native", Name: "原生 Gemini API（Gemini CLI）", BaseURL: "https://generativelanguage.googleapis.com/v1beta", Protocols: []string{model.ProtoGemini}},
			{Key: "openai", Name: "OpenAI 兼容", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Protocols: []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings}},
		},
		AuthHeader: "bearer", Discover: true, Icon: "gemini"},
	{Key: "xai", Name: "xAI", BaseURL: "https://api.x.ai/v1",
		Types:      []string{model.TypeText, model.TypeImage},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIResponses, model.ProtoOpenAIImages},
		AuthHeader: "bearer", Discover: true, Icon: "xai"},
	{Key: "groq", Name: "Groq", BaseURL: "https://api.groq.com/openai/v1",
		Types:      []string{model.TypeText},
		Protocols:  []string{model.ProtoOpenAIChat},
		AuthHeader: "bearer", Discover: true, Icon: "groq"},
	{Key: "mistral", Name: "Mistral", BaseURL: "https://api.mistral.ai/v1",
		Types:      []string{model.TypeText, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings},
		AuthHeader: "bearer", Discover: true, Icon: "mistral"},
	{Key: "together", Name: "Together AI", BaseURL: "https://api.together.xyz/v1",
		Types:      []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIImages, model.ProtoOpenAIEmbeddings},
		AuthHeader: "bearer", Discover: true, Icon: "together"},
	{Key: "fireworks", Name: "Fireworks AI", BaseURL: "https://api.fireworks.ai/inference/v1",
		Types:      []string{model.TypeText, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings},
		AuthHeader: "bearer", Discover: true, Icon: "fireworks"},
	{Key: "cerebras", Name: "Cerebras", BaseURL: "https://api.cerebras.ai/v1",
		Types:      []string{model.TypeText},
		Protocols:  []string{model.ProtoOpenAIChat},
		AuthHeader: "bearer", Discover: true, Icon: "cerebras"},
	{Key: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1",
		Types:      []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings},
		AuthHeader: "bearer", Discover: true, Icon: "openrouter"},
	{Key: "vllm", Name: "vLLM", BaseURL: "http://127.0.0.1:8000/v1",
		Types:      []string{model.TypeText, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIResponses},
		AuthHeader: "bearer", Discover: true, Custom: true, Icon: "vllm"},
	{Key: "lmstudio", Name: "LM Studio", BaseURL: "http://127.0.0.1:1234/v1",
		Types:      []string{model.TypeText, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings},
		AuthHeader: "bearer", Discover: true, Custom: true, Icon: "lmstudio"},
	{Key: "ollama", Name: "Ollama", BaseURL: "http://127.0.0.1:11434/v1",
		Types:      []string{model.TypeText, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings},
		AuthHeader: "bearer", Discover: true, Custom: true, Icon: "ollama"},
	{Key: "newapi", Name: "New API / One API", BaseURL: "http://127.0.0.1:3000/v1",
		Types:      []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIResponses, model.ProtoAnthropicMessages, model.ProtoGemini, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages},
		AuthHeader: "bearer", Discover: true, Custom: true, Icon: "newapi"},
	{Key: "custom", Name: "自定义 (OpenAI 兼容)", BaseURL: "",
		Types:      []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIResponses, model.ProtoAnthropicMessages, model.ProtoGemini, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages},
		AuthHeader: "bearer", Discover: true, Custom: true, Icon: "custom"},
	{Key: "custom-anthropic", Name: "自定义 (Anthropic 兼容)", BaseURL: "",
		Types:      []string{model.TypeText},
		Protocols:  []string{model.ProtoAnthropicMessages},
		AuthHeader: "x-api-key", Discover: false, Custom: true, Icon: "custom"},
	// Non-chat JSON APIs: the account declares the paths it serves; requests to
	// POST /v1/<path> are forwarded to <base_url>/<path> with the key injected.
	{Key: "typesafe", Name: "TypeSafe (Jev)", BaseURL: "https://api.typesafe.ai/v1",
		Types:      []string{model.TypeCustom},
		Protocols:  []string{model.ProtoCustomJSON},
		AuthHeader: "bearer", Discover: false, Icon: "custom", Endpoints: []string{"/systemone"},
		// TypeSafe has no model-listing API; the documented ids are jev-latest (the SDK
		// default), jev-preview and the pinned jev-1.13.0.
		DefaultMappings: []DefaultMapping{{RequestModel: "jev", UpstreamModel: "jev-latest"}}},
	{Key: "custom-json", Name: "自定义 (JSON 接口)", BaseURL: "",
		Types:      []string{model.TypeCustom},
		Protocols:  []string{model.ProtoCustomJSON},
		AuthHeader: "bearer", Discover: false, Custom: true, Icon: "custom"},
}

// AccountTypeOf returns the provider's account type by key, if any.
func (p Provider) AccountTypeOf(key string) (AccountType, bool) {
	for _, at := range p.AccountTypes {
		if at.Key == key {
			return at, true
		}
	}
	return AccountType{}, false
}

func List() []Provider { return registry }

func Get(key string) (Provider, bool) {
	for _, p := range registry {
		if p.Key == key {
			return p, true
		}
	}
	return Provider{}, false
}

// ProtocolsForType filters wire protocols applicable to a protocol type.
func ProtocolsForType(t string) []string {
	switch t {
	case model.TypeImage:
		return []string{model.ProtoOpenAIImages}
	case model.TypeEmbedding:
		return []string{model.ProtoOpenAIEmbeddings}
	case model.TypeCustom:
		return []string{model.ProtoCustomJSON}
	default:
		return []string{model.ProtoOpenAIChat, model.ProtoOpenAIResponses, model.ProtoAnthropicMessages, model.ProtoGemini}
	}
}

// ProtocolType maps a wire protocol to its protocol type.
func ProtocolType(proto string) string {
	switch proto {
	case model.ProtoOpenAIImages:
		return model.TypeImage
	case model.ProtoOpenAIEmbeddings:
		return model.TypeEmbedding
	case model.ProtoCustomJSON:
		return model.TypeCustom
	default:
		return model.TypeText
	}
}
