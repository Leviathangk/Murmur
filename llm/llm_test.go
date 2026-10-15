package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// collect runs Stream and returns the reasoning and the text it emitted.
func collect(t *testing.T, cfg Config, req Request) (reasoning, text string, err error) {
	t.Helper()
	err = Stream(context.Background(), cfg, req, func(e Event) {
		reasoning += e.Reasoning
		text += e.Text
	})
	return
}

func TestThinkSplitter(t *testing.T) {
	cases := []struct {
		chunks          []string
		reasoning, text string
	}{
		{[]string{"Hello", " world"}, "", "Hello world"},
		{[]string{"<think>", "a", "b</think>", "\n\nanswer"}, "ab", "answer"},
		{[]string{"<th", "ink>plan", "ning</th", "ink>done"}, "planning", "done"},
		{[]string{"\n<think>x</think>y"}, "x", "y"},
		{[]string{"<t", "able>"}, "", "<table>"},
		{[]string{"<think>never closed"}, "never closed", ""},
		{[]string{"a <think>b</think>"}, "", "a <think>b</think>"},
	}
	for _, c := range cases {
		var s thinkSplitter
		var r, x string
		emit := func(e Event) { r += e.Reasoning; x += e.Text }
		for _, ch := range c.chunks {
			s.feed(ch, emit)
		}
		s.flush(emit)
		if r != c.reasoning || x != c.text {
			t.Errorf("%q: reasoning %q text %q, want %q %q", c.chunks, r, x, c.reasoning, c.text)
		}
	}
}

func TestResolveThinkStyle(t *testing.T) {
	cases := map[string]ThinkStyle{
		"https://api.openai.com/v1":                         ThinkEffort,
		"https://api.deepseek.com":                          ThinkType,
		"https://dashscope.aliyuncs.com/compatible-mode/v1": ThinkEnable,
		"https://open.bigmodel.cn/api/paas/v4":              ThinkType,
		"https://openrouter.ai/api/v1":                      ThinkOpenRouter,
		"http://localhost:11434/v1":                         ThinkEffort,
		"https://example.com/v1":                            ThinkNone,
	}
	for base, want := range cases {
		if got := ResolveThinkStyle(ThinkAuto, base); got != want {
			t.Errorf("%s: %s, want %s", base, got, want)
		}
	}
	if got := ResolveThinkStyle(ThinkEnable, "https://api.openai.com/v1"); got != ThinkEnable {
		t.Errorf("an explicit style is kept, got %s", got)
	}
}

func TestClaudeThinkingOf(t *testing.T) {
	cases := map[string]claudeThinking{
		"claude-opus-5":              thinkAdaptive,
		"claude-sonnet-5":            thinkAdaptive,
		"claude-opus-4-8":            thinkAdaptive,
		"claude-sonnet-4-6":          thinkAdaptive,
		"claude-opus-5-5":            thinkAlways,
		"claude-fable-5-1":           thinkAlways,
		"claude-haiku-4-5":           thinkBudget,
		"claude-sonnet-4-5-20250929": thinkBudget,
		"claude-opus-4-20250514":     thinkBudget,
		"anthropic.claude-opus-5":    thinkAdaptive,
		"deepseek-chat":              thinkBudget,
	}
	for model, want := range cases {
		if got := claudeThinkingOf(model); got != want {
			t.Errorf("%s: %d, want %d", model, got, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	got := normalize([]Message{
		{"assistant", "hi"},
		{"user", "a"},
		{"assistant", ""},
		{"user", "b"},
		{"assistant", "c"},
	})
	want := []Message{{"user", "a\n\nb"}, {"assistant", "c"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOpenAIStream(t *testing.T) {
	var body map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		auth = r.Header.Get("Authorization")
		body = nil
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, d := range []string{
			`{"choices":[{"delta":{"role":"assistant","reasoning_content":"Let me "}}]}`,
			`{"choices":[{"delta":{"reasoning_content":"think."}}]}`,
			`{"choices":[{"delta":{"content":"Hi"}}]}`,
			`{"choices":[{"delta":{"content":" there"}}]}`,
			`[DONE]`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", d)
		}
	}))
	defer srv.Close()

	cfg := Config{Protocol: OpenAI, BaseURL: srv.URL + "/v1/", APIKey: "sk-test", Model: "m", ThinkStyle: ThinkType}
	r, x, err := collect(t, cfg, Request{System: "be brief", Messages: []Message{{"user", "hello"}}, Thinking: true})
	if err != nil {
		t.Fatal(err)
	}
	if r != "Let me think." || x != "Hi there" {
		t.Errorf("reasoning %q, text %q", r, x)
	}
	if auth != "Bearer sk-test" {
		t.Errorf("authorization %q", auth)
	}
	msgs, _ := json.Marshal(body["messages"])
	if string(msgs) != `[{"content":"be brief","role":"system"},{"content":"hello","role":"user"}]` {
		t.Errorf("messages %s", msgs)
	}
	if th, _ := json.Marshal(body["thinking"]); string(th) != `{"type":"enabled"}` {
		t.Errorf("thinking %s", th)
	}

	cfg.ThinkStyle = ThinkEnable
	if _, _, err := collect(t, cfg, Request{Messages: []Message{{"user", "hello"}}}); err != nil {
		t.Fatal(err)
	}
	if body["enable_thinking"] != false {
		t.Errorf("enable_thinking %v with thinking off", body["enable_thinking"])
	}
}

func TestOpenAIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()
	_, _, err := collect(t, Config{BaseURL: srv.URL, Model: "m"}, Request{Messages: []Message{{"user", "hello"}}})
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Incorrect API key") {
		t.Errorf("error %v", err)
	}
}

func TestAnthropicStream(t *testing.T) {
	var body map[string]any
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		key = r.Header.Get("X-Api-Key")
		body = nil
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		events := []struct{ name, data string }{
			{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Pondering."}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello"}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"!"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":1}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":9}}`},
			{"message_stop", `{"type":"message_stop"}`},
		}
		for _, e := range events {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, e.data)
		}
	}))
	defer srv.Close()

	cfg := Config{Protocol: Anthropic, BaseURL: srv.URL + "/v1", APIKey: "ak", Model: "claude-opus-5"}
	r, x, err := collect(t, cfg, Request{System: "sys", Messages: []Message{{"user", "hi"}}, Thinking: true})
	if err != nil {
		t.Fatal(err)
	}
	if r != "Pondering." || x != "Hello!" {
		t.Errorf("reasoning %q, text %q", r, x)
	}
	if key != "ak" {
		t.Errorf("api key %q", key)
	}
	if th, _ := json.Marshal(body["thinking"]); string(th) != `{"display":"summarized","type":"adaptive"}` {
		t.Errorf("thinking %s", th)
	}

	// Off: disabled on a model that thinks adaptively.
	collect(t, cfg, Request{Messages: []Message{{"user", "hi"}}})
	if th, _ := json.Marshal(body["thinking"]); string(th) != `{"type":"disabled"}` {
		t.Errorf("thinking off %s", th)
	}

	// Models that cannot stop thinking think less instead.
	cfg.Model = "claude-opus-5-5"
	collect(t, cfg, Request{Messages: []Message{{"user", "hi"}}})
	if _, ok := body["thinking"]; ok {
		t.Errorf("thinking sent to a model that always thinks: %v", body["thinking"])
	}
	if oc, _ := json.Marshal(body["output_config"]); string(oc) != `{"effort":"low"}` {
		t.Errorf("output_config %s", oc)
	}

	// Other providers' models take a budget.
	cfg.Model = "deepseek-chat"
	collect(t, cfg, Request{Messages: []Message{{"user", "hi"}}, Thinking: true})
	if th, _ := json.Marshal(body["thinking"]); string(th) != `{"budget_tokens":4096,"type":"enabled"}` {
		t.Errorf("budget thinking %s", th)
	}
}
