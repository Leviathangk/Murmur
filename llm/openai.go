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
// the given style, and say how hard to think.
func applyThinking(body map[string]any, style ThinkStyle, on bool, effort Effort) {
	switch style {
	case ThinkEffort:
		if on {
			body["reasoning_effort"] = string(effort)
		}
	case ThinkEnable:
		body["enable_thinking"] = on
		if on {
			body["thinking_budget"] = thinkingBudgets[effort]
		}
	case ThinkType:
		if !on {
			body["thinking"] = map[string]any{"type": "disabled"}
			break
		}
		body["thinking"] = map[string]any{"type": "enabled"}
		switch effort {
		case EffortLow:
			body["reasoning_effort"] = "low"
		case EffortHigh:
			body["reasoning_effort"] = "max"
		}
	case ThinkOpenRouter:
		if on {
			body["reasoning"] = map[string]any{"effort": string(effort)}
		}
	}
}

var thinkingBudgets = map[Effort]int{EffortLow: 2048, EffortMedium: 16384, EffortHigh: 32768}

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
			Content          string            `json:"content"`
			ReasoningContent string            `json:"reasoning_content"`
			Reasoning        string            `json:"reasoning"`
			ToolCalls        []json.RawMessage `json:"tool_calls"`
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

type statusError struct {
	status int
	msg    string
	text   string
}

func (e *statusError) Error() string { return e.text }

func streamOpenAI(ctx context.Context, client *http.Client, cfg Config, req Request, emit func(Event)) error {
	tools := len(req.Tools) > 0
	withReasoning := tools && keepsReasoning(cfg.BaseURL)
	var steps []openAIStep
	messages := func() []any {
		var msgs []any
		if s := strings.TrimSpace(req.System); s != "" {
			msgs = append(msgs, map[string]any{"role": "system", "content": s})
		}
		for _, m := range req.Messages {
			msg := map[string]any{"role": m.Role, "content": openAIContent(m)}
			if withReasoning && m.Role == "assistant" {
				msg["reasoning_content"] = m.Reasoning
			}
			msgs = append(msgs, msg)
		}
		for round, st := range steps {
			msgs = append(msgs, st.turn.message(round, withReasoning))
			for i, c := range st.turn.calls {
				msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": c.callID(round, i), "content": st.results[i]})
			}
		}
		return msgs
	}
	body := map[string]any{
		"model":          strings.TrimSpace(cfg.Model),
		"messages":       messages(),
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if tools {
		body["tools"] = openAITools(req.Tools)
	}
	if cfg.MaxTokens > 0 {
		// OpenAI's reasoning models take only the newer name.
		if strings.Contains(chatURL(cfg.BaseURL), "api.openai.com") {
			body["max_completion_tokens"] = cfg.MaxTokens
		} else {
			body["max_tokens"] = cfg.MaxTokens
		}
	}
	applyThinking(body, ResolveThinkStyle(cfg.ThinkStyle, cfg.BaseURL), req.Thinking, req.Effort)
	out := &rounds{emit: emit}
	for round := 0; round < maxToolRounds; round++ {
		if tools && round == maxToolRounds-1 {
			body["tool_choice"] = "none"
		}
		turn, err := openAIRound(ctx, client, cfg, body, out.send)
		if err != nil {
			var se *statusError
			switch {
			case !errors.As(err, &se) || !tools:
			case !withReasoning && se.status/100 == 4 && strings.Contains(se.msg, "reasoning_content"):
				withReasoning = true
				body["messages"] = messages()
				round--
				continue
			case toolsRejected(round, se.status, se.msg):
				return toolsError{err}
			}
			return err
		}
		out.add(turn.usage)
		if len(turn.calls) == 0 || round == maxToolRounds-1 {
			break
		}
		st := openAIStep{turn: turn}
		for _, c := range turn.calls {
			result, _ := runTool(ctx, req.Tools, c.name, json.RawMessage(c.args.String()))
			if ctx.Err() != nil {
				return ctx.Err()
			}
			st.results = append(st.results, result)
		}
		steps = append(steps, st)
		body["messages"] = messages()
		out.next()
	}
	out.done()
	return nil
}

func keepsReasoning(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range []string{"deepseek.com", "moonshot.cn", "moonshot.ai", "kimi.com", "bigmodel.cn", "z.ai"} {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

type openAIStep struct {
	turn    *openAITurn
	results []string
}

func openAITools(tools []Tool) []any {
	var out []any
	for _, t := range tools {
		params := map[string]any{"type": "object", "properties": t.Properties}
		if len(t.Required) > 0 {
			params["required"] = t.Required
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  params,
			},
		})
	}
	return out
}

type openAITurn struct {
	text, reasoning string
	calls           []*openAICall
	byIndex         map[int]*openAICall
	usage           *Usage
}

type openAICall struct {
	id, name string
	args     strings.Builder
	extra    map[string]json.RawMessage
}

func (c *openAICall) callID(round, i int) string {
	if c.id == "" {
		c.id = fmt.Sprintf("call_%d_%d", round, i)
	}
	return c.id
}

func (t *openAITurn) message(round int, withReasoning bool) map[string]any {
	msg := map[string]any{"role": "assistant", "content": nil}
	if t.text != "" {
		msg["content"] = t.text
	}
	if withReasoning {
		msg["reasoning_content"] = t.reasoning
	}
	var calls []any
	for i, c := range t.calls {
		args := c.args.String()
		if !json.Valid([]byte(args)) {
			args = "{}"
		}
		call := map[string]any{}
		for k, v := range c.extra {
			call[k] = v
		}
		call["id"] = c.callID(round, i)
		call["type"] = "function"
		call["function"] = map[string]any{"name": c.name, "arguments": args}
		calls = append(calls, call)
	}
	msg["tool_calls"] = calls
	return msg
}

func (t *openAITurn) addCall(raw json.RawMessage) {
	var d struct {
		Index    *int   `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return
	}
	var c *openAICall
	switch {
	case d.Index != nil:
		if x := t.byIndex[*d.Index]; x != nil && (d.ID == "" || x.id == "" || x.id == d.ID) {
			c = x
		}
	case d.ID != "":
		for _, x := range t.calls {
			if x.id == d.ID {
				c = x
			}
		}
	case len(t.calls) > 0:
		c = t.calls[len(t.calls)-1]
	}
	if c == nil {
		c = &openAICall{}
		t.calls = append(t.calls, c)
	}
	if d.Index != nil {
		if t.byIndex == nil {
			t.byIndex = map[int]*openAICall{}
		}
		t.byIndex[*d.Index] = c
	}
	if c.id == "" {
		c.id = d.ID
	}
	if c.name == "" {
		c.name = d.Function.Name
	}
	c.args.WriteString(d.Function.Arguments)
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) == nil {
		for k, v := range fields {
			switch k {
			case "index", "id", "type", "function":
			default:
				if string(v) != "null" {
					if c.extra == nil {
						c.extra = map[string]json.RawMessage{}
					}
					c.extra[k] = v
				}
			}
		}
	}
}

func openAIRound(ctx context.Context, client *http.Client, cfg Config, body map[string]any, emit func(Event)) (*openAITurn, error) {
	var resp *http.Response
	for {
		var err error
		if resp, err = postChat(ctx, client, cfg, body); err != nil {
			return nil, err
		}
		if resp.StatusCode/100 == 2 {
			break
		}
		err = httpError(resp)
		resp.Body.Close()
		// Not every provider takes stream_options: ask again without it,
		// for the reply without its usage.
		if _, ok := body["stream_options"]; !ok || resp.StatusCode/100 != 4 || !strings.Contains(err.Error(), "stream_options") {
			return nil, err
		}
		delete(body, "stream_options")
	}
	defer resp.Body.Close()

	turn := &openAITurn{}
	var text, reasoning strings.Builder
	keep := func(e Event) {
		text.WriteString(e.Text)
		reasoning.WriteString(e.Reasoning)
		emit(e)
	}
	var think thinkSplitter
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
			return nil, fmt.Errorf("%s", chunk.Error.Message)
		}
		// The usage comes in the last chunk, or so far in each.
		if u := chunk.Usage; u != nil {
			turn.usage = &Usage{Input: u.PromptTokens, Output: u.CompletionTokens}
		}
		for _, ch := range chunk.Choices {
			d := ch.Delta
			if r := d.ReasoningContent + d.Reasoning; r != "" {
				keep(Event{Reasoning: r})
			}
			if d.Content != "" {
				think.feed(d.Content, keep)
			}
			for _, tc := range d.ToolCalls {
				turn.addCall(tc)
			}
			if u := ch.Usage; u != nil {
				turn.usage = &Usage{Input: u.PromptTokens, Output: u.CompletionTokens}
			}
		}
	}
	think.flush(keep)
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	turn.text, turn.reasoning = text.String(), reasoning.String()
	return turn, nil
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
	return &statusError{status: resp.StatusCode, msg: msg, text: locales.LlmHTTPError(strconv.Itoa(resp.StatusCode), msg)}
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
