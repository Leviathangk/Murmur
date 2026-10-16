// Package llm streams chat replies from language models over two
// protocols: OpenAI's Chat Completions, which most providers speak, and
// Anthropic's Messages API. Replies come as events of reasoning (the
// model's thinking) and text.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Qsnh/Murmur/locales"
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

type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
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
	Reasoning string
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
// whether the model should think before it answers, and how hard.
type Request struct {
	System   string
	Messages []Message
	Thinking bool
	Effort   Effort
	Tools    []Tool
}

type Tool struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
	Run         func(ctx context.Context, input json.RawMessage) (string, error)
}

const maxToolRounds = 10

var ErrToolsUnsupported = errors.New("tools unsupported")

type toolsError struct{ err error }

func (e toolsError) Error() string        { return e.err.Error() }
func (e toolsError) Unwrap() error        { return e.err }
func (e toolsError) Is(target error) bool { return target == ErrToolsUnsupported }

func toolsRejected(round, status int, msg string) bool {
	msg = strings.ToLower(msg)
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity && status != http.StatusNotFound {
		return false
	}
	for _, s := range []string{"context", "too long", "too many tokens", "reasoning_content"} {
		if strings.Contains(msg, s) {
			return false
		}
	}
	if round > 0 {
		return status != http.StatusNotFound
	}
	if !strings.Contains(msg, "tool") && !strings.Contains(msg, "function") {
		return false
	}
	for _, s := range []string{"support", "not allowed", "not enabled", "tool choice", "tool_choice", "tool-call-parser", "extra inputs", "extra_forbidden", "unrecognized", "unknown"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func runTool(ctx context.Context, tools []Tool, name string, input json.RawMessage) (string, bool) {
	if len(strings.TrimSpace(string(input))) == 0 {
		input = json.RawMessage("{}")
	}
	for _, t := range tools {
		if t.Name == name {
			out, err := t.Run(ctx, input)
			if err != nil {
				return err.Error(), true
			}
			return out, false
		}
	}
	return fmt.Sprintf("Unknown tool %q", name), true
}

type rounds struct {
	emit                  func(Event)
	wroteText, wroteThink bool
	sepText, sepThink     bool
	usage                 *Usage
}

func (r *rounds) send(e Event) {
	if e.Text != "" {
		if r.sepText {
			e.Text = "\n\n" + e.Text
			r.sepText = false
		}
		r.wroteText = true
	}
	if e.Reasoning != "" {
		if r.sepThink {
			e.Reasoning = "\n\n" + e.Reasoning
			r.sepThink = false
		}
		r.wroteThink = true
	}
	r.emit(e)
}

func (r *rounds) next() {
	r.sepText, r.sepThink = r.wroteText, r.wroteThink
}

func (r *rounds) add(u *Usage) {
	if u == nil {
		return
	}
	if r.usage == nil {
		r.usage = &Usage{}
	}
	r.usage.Input += u.Input
	r.usage.Output += u.Output
}

func (r *rounds) done() {
	if u := r.usage; u != nil && (u.Input > 0 || u.Output > 0) {
		r.emit(Event{Usage: u})
	}
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
		return errors.New(locales.LlmNoModel())
	}
	client, err := HTTPClient(cfg.Proxy)
	if err != nil {
		return err
	}
	req.Messages = normalize(req.Messages)
	if len(req.Messages) == 0 {
		return errors.New(locales.LlmNoMessages())
	}
	if req.Effort != EffortLow && req.Effort != EffortHigh {
		req.Effort = EffortMedium
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
			switch {
			case prev.Reasoning == "":
				prev.Reasoning = m.Reasoning
			case m.Reasoning != "":
				prev.Reasoning += "\n\n" + m.Reasoning
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

// HTTPClient returns a client that goes through proxy, if any. It has no
// overall timeout, as replies stream for minutes, but gives up on a
// server that sends no response headers.
func HTTPClient(proxy string) (*http.Client, error) {
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
		return nil, errors.New(locales.LlmInvalidProxy(proxy))
	}
	return u, nil
}
