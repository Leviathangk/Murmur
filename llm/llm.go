// Package llm streams chat replies from language models over two
// protocols: OpenAI's Chat Completions, which most providers speak, and
// Anthropic's Messages API. Replies come as events of reasoning (the
// model's thinking) and text.
package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Protocol is the API a model is reached with.
type Protocol string

const (
	OpenAI    Protocol = "openai"
	Anthropic Protocol = "anthropic"
)

// ThinkStyle is how an OpenAI-compatible request turns thinking on and
// off: each provider took a field of its own.
type ThinkStyle string

const (
	ThinkAuto       ThinkStyle = "auto"             // chosen from the base URL
	ThinkEffort     ThinkStyle = "reasoning_effort" // OpenAI, Ollama, vLLM, Gemini
	ThinkEnable     ThinkStyle = "enable_thinking"  // Qwen (DashScope), SiliconFlow
	ThinkType       ThinkStyle = "thinking"         // GLM, DeepSeek, Doubao
	ThinkOpenRouter ThinkStyle = "openrouter"       // OpenRouter's reasoning object
	ThinkNone       ThinkStyle = "none"             // nothing sent: the model decides
)

// Config is where a model is and how to call it.
type Config struct {
	Protocol   Protocol
	BaseURL    string
	APIKey     string
	Model      string
	ThinkStyle ThinkStyle
	// MaxTokens caps the reply; 0 leaves it to the provider (OpenAI) or
	// to a default that suits the model (Anthropic, which requires one).
	MaxTokens int
	// Proxy is an http, https or socks5 URL; empty uses the environment's.
	Proxy string
}

// Message is a turn of the conversation: Role is "user" or "assistant".
type Message struct {
	Role      string
	Content   string
	Images    []Image    // the pictures of a user's turn
	Documents []Document // the files of a user's turn
}

// Image is a picture the model looks at: PNG, JPEG, GIF or WebP data, of
// its MIME type.
type Image struct {
	MIME string
	Data []byte
}

// Document is a file the model reads as its text, as a PDF or a sheet is
// read before it is sent.
type Document struct {
	Name string
	Text string
}

// text returns the content of m after the text of its documents, each in
// tags that name it: any model reads them so, whatever files it takes.
func (m Message) text() string {
	if len(m.Documents) == 0 {
		return m.Content
	}
	var b strings.Builder
	for _, d := range m.Documents {
		fmt.Fprintf(&b, "<file name=%q>\n%s\n</file>\n\n", d.Name, d.Text)
	}
	b.WriteString(m.Content)
	return strings.TrimRight(b.String(), "\n")
}

// Request is what to send: the system prompt, the conversation, and
// whether the model should think before it answers.
type Request struct {
	System   string
	Messages []Message
	Thinking bool
}

// Event is a piece of the reply.
type Event struct {
	Reasoning string
	Text      string
	// Usage comes once the reply has ended, when the provider tells it.
	Usage *Usage
}

// Usage is how many tokens a reply took: Input those of the request the
// model read, the system prompt and earlier turns included, and Output
// those it wrote, its reasoning included.
type Usage struct {
	Input, Output int
}

// Stream sends req to the model and calls emit with each piece of the
// reply as it comes, on the calling goroutine, until the reply ends, ctx
// is canceled or the request fails. When the reply ends, a last event
// tells its usage, if the provider does.
func Stream(ctx context.Context, cfg Config, req Request, emit func(Event)) error {
	if strings.TrimSpace(cfg.Model) == "" {
		return fmt.Errorf("没有填写模型 ID")
	}
	client, err := httpClient(cfg.Proxy)
	if err != nil {
		return err
	}
	req.Messages = normalize(req.Messages)
	if len(req.Messages) == 0 {
		return fmt.Errorf("没有可发送的消息")
	}
	switch cfg.Protocol {
	case Anthropic:
		return streamAnthropic(ctx, client, cfg, req, emit)
	default:
		return streamOpenAI(ctx, client, cfg, req, emit)
	}
}

// normalize drops empty turns and joins turns of the same role, so that
// users and the assistant alternate, as Anthropic requires, even after a
// reply that failed.
func normalize(msgs []Message) []Message {
	var out []Message
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" && len(m.Images) == 0 && len(m.Documents) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			prev := &out[n-1]
			switch {
			case strings.TrimSpace(prev.Content) == "":
				prev.Content = m.Content
			case strings.TrimSpace(m.Content) != "":
				prev.Content += "\n\n" + m.Content
			}
			prev.Images = slices.Concat(prev.Images, m.Images)
			prev.Documents = slices.Concat(prev.Documents, m.Documents)
			continue
		}
		out = append(out, m)
	}
	// A conversation starts with the user.
	for len(out) > 0 && out[0].Role != "user" {
		out = out[1:]
	}
	return out
}

// httpClient returns a client that goes through proxy, if any. It has no
// overall timeout, as replies stream for minutes, but gives up on a
// server that sends no response headers.
func httpClient(proxy string) (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 3 * time.Minute
	if strings.TrimSpace(proxy) != "" {
		u, err := ProxyURL(proxy)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: tr}, nil
}

// ProxyURL returns the URL of proxy, an http, https or socks5 URL, or a
// host and port of an http proxy.
func ProxyURL(proxy string) (*url.URL, error) {
	proxy = strings.TrimSpace(proxy)
	if !strings.Contains(proxy, "://") {
		proxy = "http://" + proxy
	}
	u, err := url.Parse(proxy)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("代理地址无效：%s", proxy)
	}
	return u, nil
}
