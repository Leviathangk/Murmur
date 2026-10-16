package main

import (
	"github.com/Qsnh/Murmur/llm"
	"github.com/Qsnh/Murmur/locales"
)

// preset fills a new model with a provider's address and a model of it.
type preset struct {
	name     func() string
	protocol llm.Protocol
	baseURL  string
	model    string
	style    llm.ThinkStyle
}

var presets = []preset{
	{brand("OpenAI"), llm.OpenAI, "https://api.openai.com/v1", "gpt-5", llm.ThinkAuto},
	{brand("Claude"), llm.Anthropic, "https://api.anthropic.com", "claude-opus-5", ""},
	{brand("DeepSeek"), llm.OpenAI, "https://api.deepseek.com", "deepseek-chat", llm.ThinkAuto},
	{locales.ProviderQwen, llm.OpenAI, "https://dashscope.aliyuncs.com/compatible-mode/v1", "qwen-plus", llm.ThinkAuto},
	{locales.ProviderZhipu, llm.OpenAI, "https://open.bigmodel.cn/api/paas/v4", "glm-4.6", llm.ThinkAuto},
	{brand("Kimi"), llm.OpenAI, "https://api.moonshot.cn/v1", "kimi-k2-thinking", llm.ThinkNone},
	{locales.ProviderDoubao, llm.OpenAI, "https://ark.cn-beijing.volces.com/api/v3", "doubao-seed-1-6-250615", llm.ThinkAuto},
	{locales.ProviderSiliconFlow, llm.OpenAI, "https://api.siliconflow.cn/v1", "Qwen/Qwen3-8B", llm.ThinkAuto},
	{brand("Gemini"), llm.OpenAI, "https://generativelanguage.googleapis.com/v1beta/openai", "gemini-2.5-flash", llm.ThinkAuto},
	{brand("OpenRouter"), llm.OpenAI, "https://openrouter.ai/api/v1", "openai/gpt-5", llm.ThinkAuto},
	{locales.ProviderOllama, llm.OpenAI, "http://localhost:11434/v1", "qwen3", llm.ThinkAuto},
	{locales.ProviderCustom, llm.OpenAI, "", "", llm.ThinkAuto},
}

func brand(name string) func() string {
	return func() string { return name }
}

// newModel returns a model made from p, named after its model ID until
// the user names it.
func (p preset) newModel() *Model {
	return &Model{
		ID:         newID(),
		Protocol:   p.protocol,
		BaseURL:    p.baseURL,
		Model:      p.model,
		ThinkStyle: p.style,
	}
}

// protocols are the protocols a model can speak, as the settings name them.
func protocols() []option[llm.Protocol] {
	return []option[llm.Protocol]{
		{llm.OpenAI, locales.ProtocolOpenAI()},
		{llm.Anthropic, "Anthropic"},
	}
}

// thinkStyles are the ways an OpenAI-compatible request asks for thinking.
func thinkStyles() []option[llm.ThinkStyle] {
	return []option[llm.ThinkStyle]{
		{llm.ThinkAuto, locales.ThinkStyleAuto()},
		{llm.ThinkEffort, locales.ThinkStyleEffort()},
		{llm.ThinkEnable, locales.ThinkStyleEnable()},
		{llm.ThinkType, locales.ThinkStyleType()},
		{llm.ThinkOpenRouter, locales.ThinkStyleOpenRouter()},
		{llm.ThinkNone, locales.ThinkStyleNone()},
	}
}
