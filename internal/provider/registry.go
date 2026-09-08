// Package provider describes the built-in upstream providers.
package provider

import "yzapi/internal/model"

type AccountType struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url,omitempty"`
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
		Types:      []string{model.TypeText},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoAnthropicMessages},
		AuthHeader: "bearer", Discover: true, Icon: "deepseek"},
	{Key: "aliyun", Name: "阿里云百炼", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Types:     []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols: []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages, model.ProtoAnthropicMessages},
		AccountTypes: []AccountType{
			{Key: "payg", Name: "按量付费", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"},
			{Key: "coding", Name: "Coding Plan", BaseURL: "https://coding.dashscope.aliyuncs.com/v1"},
			{Key: "intl", Name: "国际站", BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"},
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
		Protocols: []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages},
		AccountTypes: []AccountType{
			{Key: "payg", Name: "按量付费", BaseURL: "https://open.bigmodel.cn/api/paas/v4"},
			{Key: "coding", Name: "Coding Plan", BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4"},
		},
		AuthHeader: "bearer", Discover: true, Icon: "zhipu"},
	{Key: "moonshot", Name: "Moonshot (Kimi)", BaseURL: "https://api.moonshot.cn/v1",
		Types:      []string{model.TypeText},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoAnthropicMessages},
		AuthHeader: "bearer", Discover: true, Icon: "moonshot"},
	{Key: "minimax", Name: "MiniMax", BaseURL: "https://api.minimaxi.com/v1",
		Types:      []string{model.TypeText},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoAnthropicMessages},
		AuthHeader: "bearer", Discover: false, Icon: "minimax"},
	{Key: "siliconflow", Name: "硅基流动", BaseURL: "https://api.siliconflow.cn/v1",
		Types:      []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages},
		AuthHeader: "bearer", Discover: true, Icon: "siliconflow"},
	{Key: "gemini", Name: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
		Types:      []string{model.TypeText, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings},
		AuthHeader: "bearer", Discover: true, Icon: "gemini"},
	{Key: "xai", Name: "xAI", BaseURL: "https://api.x.ai/v1",
		Types:      []string{model.TypeText, model.TypeImage},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIResponses, model.ProtoOpenAIImages},
		AuthHeader: "bearer", Discover: true, Icon: "xai"},
	{Key: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1",
		Types:      []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings},
		AuthHeader: "bearer", Discover: true, Icon: "openrouter"},
	{Key: "vllm", Name: "vLLM", BaseURL: "http://127.0.0.1:8000/v1",
		Types:      []string{model.TypeText, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIResponses},
		AuthHeader: "bearer", Discover: true, Custom: true, Icon: "vllm"},
	{Key: "ollama", Name: "Ollama", BaseURL: "http://127.0.0.1:11434/v1",
		Types:      []string{model.TypeText, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIEmbeddings},
		AuthHeader: "bearer", Discover: true, Custom: true, Icon: "ollama"},
	{Key: "newapi", Name: "New API / One API", BaseURL: "http://127.0.0.1:3000/v1",
		Types:      []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIResponses, model.ProtoAnthropicMessages, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages},
		AuthHeader: "bearer", Discover: true, Custom: true, Icon: "newapi"},
	{Key: "custom", Name: "自定义 (OpenAI 兼容)", BaseURL: "",
		Types:      []string{model.TypeText, model.TypeImage, model.TypeEmbedding},
		Protocols:  []string{model.ProtoOpenAIChat, model.ProtoOpenAIResponses, model.ProtoAnthropicMessages, model.ProtoOpenAIEmbeddings, model.ProtoOpenAIImages},
		AuthHeader: "bearer", Discover: true, Custom: true, Icon: "custom"},
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
	default:
		return []string{model.ProtoOpenAIChat, model.ProtoOpenAIResponses, model.ProtoAnthropicMessages}
	}
}

// ProtocolType maps a wire protocol to its protocol type.
func ProtocolType(proto string) string {
	switch proto {
	case model.ProtoOpenAIImages:
		return model.TypeImage
	case model.ProtoOpenAIEmbeddings:
		return model.TypeEmbedding
	default:
		return model.TypeText
	}
}
