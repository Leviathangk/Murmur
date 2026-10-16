package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Qsnh/Murmur/locales"
)

// ResolveThinkStyle returns the style a request uses: style itself, or for
// ThinkAuto the one of the provider at baseURL, ThinkNone for those it
// does not know.
func ResolveThinkStyle(style ThinkStyle, baseURL string) ThinkStyle {
	if style != "" && style != ThinkAuto {
		return style
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ThinkNone
	}
	host := strings.ToLower(u.Host)
	has := func(names ...string) bool {
		for _, n := range names {
			if strings.Contains(host, n) {
				return true
			}
		}
		return false
	}
	switch {
	case has("openrouter.ai"):
		return ThinkOpenRouter
	case has("dashscope", "siliconflow", "modelscope"):
		return ThinkEnable
	case has("bigmodel.cn", "z.ai", "deepseek.com", "volces.com"):
		return ThinkType
	case has("api.openai.com", "generativelanguage.googleapis.com", ":11434", "ollama"):
		return ThinkEffort
	}
	return ThinkNone
}

// applyThinking sets the fields of body that turn thinking on or off in
// the given style.
func applyThinking(body map[string]any, style ThinkStyle, on bool) {
	switch style {
	case ThinkEffort:
		if on {
			body["reasoning_effort"] = "medium"
		}
	case ThinkEnable:
		body["enable_thinking"] = on
	case ThinkType:
		if on {
			body["thinking"] = map[string]any{"type": "enabled"}
		} else {
			body["thinking"] = map[string]any{"type": "disabled"}
		}
	case ThinkOpenRouter:
		if on {
			body["reasoning"] = map[string]any{"effort": "medium"}
		}
	}
}

// chatURL returns the Chat Completions endpoint under base, which may be
// the endpoint already.
func chatURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}

type openAIChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
		} `json:"delta"`
		Usage *openAIUsage `json:"usage"` // where Kimi tells it
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
	Error *apiError    `json:"error"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

func streamOpenAI(ctx context.Context, client *http.Client, cfg Config, req Request, emit func(Event)) error {
	var msgs []map[string]any
	if s := strings.TrimSpace(req.System); s != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": s})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]any{"role": m.Role, "content": openAIContent(m)})
	}
	body := map[string]any{
		"model":          strings.TrimSpace(cfg.Model),
		"messages":       msgs,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if cfg.MaxTokens > 0 {
		// OpenAI's reasoning models take only the newer name.
		if strings.Contains(chatURL(cfg.BaseURL), "api.openai.com") {
			body["max_completion_tokens"] = cfg.MaxTokens
		} else {
			body["max_tokens"] = cfg.MaxTokens
		}
	}
	applyThinking(body, ResolveThinkStyle(cfg.ThinkStyle, cfg.BaseURL), req.Thinking)
	var resp *http.Response
	for {
		var err error
		if resp, err = postChat(ctx, client, cfg, body); err != nil {
			return err
		}
		if resp.StatusCode/100 == 2 {
			break
		}
		err = httpError(resp)
		resp.Body.Close()
		// Not every provider takes stream_options: ask again without it,
		// for the reply without its usage.
		if _, ok := body["stream_options"]; !ok || resp.StatusCode/100 != 4 || !strings.Contains(err.Error(), "stream_options") {
			return err
		}
		delete(body, "stream_options")
	}
	defer resp.Body.Close()

	var think thinkSplitter
	var usage *Usage
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk openAIChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return fmt.Errorf("%s", chunk.Error.Message)
		}
		// The usage comes in the last chunk, or so far in each.
		if u := chunk.Usage; u != nil {
			usage = &Usage{Input: u.PromptTokens, Output: u.CompletionTokens}
		}
		for _, ch := range chunk.Choices {
			d := ch.Delta
			if r := d.ReasoningContent + d.Reasoning; r != "" {
				emit(Event{Reasoning: r})
			}
			if d.Content != "" {
				think.feed(d.Content, emit)
			}
			if u := ch.Usage; u != nil {
				usage = &Usage{Input: u.PromptTokens, Output: u.CompletionTokens}
			}
		}
	}
	think.flush(emit)
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if usage != nil && (usage.Input > 0 || usage.Output > 0) {
		emit(Event{Usage: usage})
	}
	return nil
}

// postChat sends body to the Chat Completions endpoint of cfg.
func postChat(ctx context.Context, client *http.Client, cfg Config, body map[string]any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, chatURL(cfg.BaseURL), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	if k := strings.TrimSpace(cfg.APIKey); k != "" {
		hreq.Header.Set("Authorization", "Bearer "+k)
	}
	return client.Do(hreq)
}

// openAIContent returns the content of m: its text, that of its documents
// before it, or with images, a part of that text and one of each image, as
// a data URL. A turn of text alone stays a string, which every provider
// takes.
func openAIContent(m Message) any {
	text := m.text()
	if len(m.Images) == 0 {
		return text
	}
	var parts []map[string]any
	if strings.TrimSpace(text) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": text})
	}
	for _, img := range m.Images {
		url := "data:" + img.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
	}
	return parts
}

// httpError reads the error a provider answered with.
func httpError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var e struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	msg := ""
	if json.Unmarshal(b, &e) == nil {
		var inner apiError
		var s string
		switch {
		case json.Unmarshal(e.Error, &inner) == nil && inner.Message != "":
			msg = inner.Message
		case json.Unmarshal(e.Error, &s) == nil && s != "":
			msg = s
		case e.Message != "":
			msg = e.Message
		}
	}
	if msg == "" {
		msg = strings.TrimSpace(string(b))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return errors.New(locales.LlmHTTPError(strconv.Itoa(resp.StatusCode), msg))
}

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// thinkSplitter moves the reasoning that some models write in the text,
// between <think> and </think> at its start, to reasoning events.
type thinkSplitter struct {
	// state is 0 before it is known whether the text starts with <think>,
	// 1 in the reasoning, 2 in the answer and 3 between the two, where
	// blank lines are dropped.
	state int
	buf   string
}

func (s *thinkSplitter) feed(text string, emit func(Event)) {
	switch s.state {
	case 2:
		emit(Event{Text: text})
		return
	case 3:
		if text = strings.TrimLeft(text, " \t\r\n"); text != "" {
			s.state = 2
			emit(Event{Text: text})
		}
		return
	case 0:
		s.buf += text
		head := strings.TrimLeft(s.buf, " \t\r\n")
		switch {
		case strings.HasPrefix(head, thinkOpen):
			s.state, s.buf = 1, ""
			s.feed(head[len(thinkOpen):], emit)
		case len(head) < len(thinkOpen) && strings.HasPrefix(thinkOpen, head):
			// Could still become <think>: wait for more.
		default:
			s.state = 2
			out := s.buf
			s.buf = ""
			emit(Event{Text: out})
		}
		return
	}
	s.buf += text
	if i := strings.Index(s.buf, thinkClose); i >= 0 {
		if i > 0 {
			emit(Event{Reasoning: s.buf[:i]})
		}
		rest := s.buf[i+len(thinkClose):]
		s.state, s.buf = 3, ""
		s.feed(rest, emit)
		return
	}
	// Keep what could be the start of </think>.
	keep := 0
	for n := min(len(thinkClose)-1, len(s.buf)); n > 0; n-- {
		if strings.HasSuffix(s.buf, thinkClose[:n]) {
			keep = n
			break
		}
	}
	if out := s.buf[:len(s.buf)-keep]; out != "" {
		emit(Event{Reasoning: out})
	}
	s.buf = s.buf[len(s.buf)-keep:]
}

// flush emits what is left at the end of the reply.
func (s *thinkSplitter) flush(emit func(Event)) {
	if s.buf == "" {
		return
	}
	if s.state == 1 {
		emit(Event{Reasoning: s.buf})
	} else {
		emit(Event{Text: s.buf})
	}
	s.buf = ""
}
