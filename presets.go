package main

import "github.com/Qsnh/Murmur/llm"

// preset fills a new model with a provider's address and a model of it.
type preset struct {
	name     string
	protocol llm.Protocol
	baseURL  string
	model    string
	style    llm.ThinkStyle
}

var presets = []preset{
	{"OpenAI", llm.OpenAI, "https://api.openai.com/v1", "gpt-5", llm.ThinkAuto},
	{"Claude", llm.Anthropic, "https://api.anthropic.com", "claude-opus-5", ""},
	{"DeepSeek", llm.OpenAI, "https://api.deepseek.com", "deepseek-chat", llm.ThinkAuto},
	{"通义千问", llm.OpenAI, "https://dashscope.aliyuncs.com/compatible-mode/v1", "qwen-plus", llm.ThinkAuto},
	{"智谱 GLM", llm.OpenAI, "https://open.bigmodel.cn/api/paas/v4", "glm-4.6", llm.ThinkAuto},
	{"Kimi", llm.OpenAI, "https://api.moonshot.cn/v1", "kimi-k2-thinking", llm.ThinkNone},
	{"豆包", llm.OpenAI, "https://ark.cn-beijing.volces.com/api/v3", "doubao-seed-1-6-250615", llm.ThinkAuto},
	{"硅基流动", llm.OpenAI, "https://api.siliconflow.cn/v1", "Qwen/Qwen3-8B", llm.ThinkAuto},
	{"Gemini", llm.OpenAI, "https://generativelanguage.googleapis.com/v1beta/openai", "gemini-2.5-flash", llm.ThinkAuto},
	{"OpenRouter", llm.OpenAI, "https://openrouter.ai/api/v1", "openai/gpt-5", llm.ThinkAuto},
	{"Ollama（本地）", llm.OpenAI, "http://localhost:11434/v1", "qwen3", llm.ThinkAuto},
	{"自定义", llm.OpenAI, "", "", llm.ThinkAuto},
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
var protocols = []option[llm.Protocol]{
	{llm.OpenAI, "OpenAI 兼容"},
	{llm.Anthropic, "Anthropic"},
}

// thinkStyles are the ways an OpenAI-compatible request asks for thinking.
var thinkStyles = []option[llm.ThinkStyle]{
	{llm.ThinkAuto, "自动识别"},
	{llm.ThinkEffort, "reasoning_effort（OpenAI / Ollama / Gemini）"},
	{llm.ThinkEnable, "enable_thinking（通义千问 / 硅基流动）"},
	{llm.ThinkType, "thinking.type（智谱 / DeepSeek / 豆包）"},
	{llm.ThinkOpenRouter, "reasoning（OpenRouter）"},
	{llm.ThinkNone, "不发送（由模型决定）"},
}
