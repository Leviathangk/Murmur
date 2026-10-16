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

// collect runs Stream and returns the reasoning, the text and the usage it
// emitted.
func collect(t *testing.T, cfg Config, req Request) (reasoning, text string, usage *Usage, err error) {
	t.Helper()
	err = Stream(context.Background(), cfg, req, func(e Event) {
		reasoning += e.Reasoning
		text += e.Text
		if e.Usage != nil {
			usage = e.Usage
		}
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
		"claude-sonnet-5-5":          thinkBetweenTools,
		"claude-sonnet-6":            thinkBetweenTools,
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
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: ""},
		{Role: "user", Content: "b"},
		{Role: "assistant", Content: "c"},
	})
	want := []Message{{Role: "user", Content: "a\n\nb"}, {Role: "assistant", Content: "c"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// A turn of images alone is kept, and joins the next one's text and
	// images without leaving the input changed.
	a, b := Image{MIME: "image/png", Data: []byte("a")}, Image{MIME: "image/jpeg", Data: []byte("b")}
	in := []Message{
		{Role: "user", Images: []Image{a}},
		{Role: "user", Content: "what", Images: []Image{b}},
	}
	got = normalize(in)
	if len(got) != 1 || got[0].Content != "what" || len(got[0].Images) != 2 || got[0].Images[1].MIME != "image/jpeg" {
		t.Errorf("images joined into %+v", got)
	}
	got[0].Images[0] = Image{MIME: "image/gif"}
	if len(in[0].Images) != 1 || in[0].Images[0].MIME != "image/png" || len(in[1].Images) != 1 {
		t.Errorf("the input changed: %+v", in)
	}

	// So is a turn of files alone.
	d, e := Document{Name: "a.pdf", Text: "A"}, Document{Name: "b.csv", Text: "B"}
	got = normalize([]Message{
		{Role: "user", Documents: []Document{d}},
		{Role: "assistant", Content: "?"},
		{Role: "user", Documents: []Document{e}},
		{Role: "user", Content: "compare"},
	})
	if len(got) != 3 || len(got[0].Documents) != 1 || got[2].Content != "compare" || len(got[2].Documents) != 1 || got[2].Documents[0] != e {
		t.Errorf("documents joined into %+v", got)
	}
}

// documentRequest is a turn of two files and a question, and one of a file
// alone.
var documentRequest = Request{Messages: []Message{
	{Role: "user", Content: "总结一下", Documents: []Document{{Name: "周报.pdf", Text: "本周完成了发送文档"}, {Name: `a "b".csv`, Text: "x,y"}}},
	{Role: "assistant", Content: "好的"},
	{Role: "user", Documents: []Document{{Name: "c.txt", Text: "C"}}, Images: []Image{{MIME: "image/png", Data: []byte("png")}}},
}}

// The files go as text before the question, which every model reads.
func TestDocuments(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "/messages") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	first := "<file name=\"周报.pdf\">\n本周完成了发送文档\n</file>\n\n<file name=\"a \\\"b\\\".csv\">\nx,y\n</file>\n\n总结一下"
	last := "<file name=\"c.txt\">\nC\n</file>"

	if _, _, _, err := collect(t, Config{BaseURL: srv.URL, Model: "m"}, documentRequest); err != nil {
		t.Fatal(err)
	}
	msgs, _ := json.Marshal(body["messages"])
	want, _ := json.Marshal([]any{
		map[string]any{"role": "user", "content": first},
		map[string]any{"role": "assistant", "content": "好的"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": last},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,cG5n"}},
		}},
	})
	if string(msgs) != string(want) {
		t.Errorf("OpenAI messages %s\nwant %s", msgs, want)
	}

	collect(t, Config{Protocol: Anthropic, BaseURL: srv.URL, APIKey: "ak", Model: "claude-opus-5"}, documentRequest)
	msgs, _ = json.Marshal(body["messages"])
	want, _ = json.Marshal([]any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": first}}},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "好的"}}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "cG5n"}},
			map[string]any{"type": "text", "text": last},
		}},
	})
	if string(msgs) != string(want) {
		t.Errorf("Anthropic messages %s\nwant %s", msgs, want)
	}
}

// imageRequest is a turn of a picture and a question, and one of a
// picture alone.
var imageRequest = Request{Messages: []Message{
	{Role: "user", Content: "what is it?", Images: []Image{{MIME: "image/png", Data: []byte("png")}}},
	{Role: "assistant", Content: "a cat"},
	{Role: "user", Images: []Image{{MIME: "image/jpeg", Data: []byte("jpg")}}},
}}

func TestOpenAIImages(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	if _, _, _, err := collect(t, Config{BaseURL: srv.URL, Model: "m"}, imageRequest); err != nil {
		t.Fatal(err)
	}
	msgs, _ := json.Marshal(body["messages"])
	want := `[{"content":[{"text":"what is it?","type":"text"},{"image_url":{"url":"data:image/png;base64,cG5n"},"type":"image_url"}],"role":"user"},` +
		`{"content":"a cat","role":"assistant"},` +
		`{"content":[{"image_url":{"url":"data:image/jpeg;base64,anBn"},"type":"image_url"}],"role":"user"}]`
	if string(msgs) != want {
		t.Errorf("messages %s\nwant %s", msgs, want)
	}
}

func TestAnthropicImages(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()
	collect(t, Config{Protocol: Anthropic, BaseURL: srv.URL, APIKey: "ak", Model: "claude-opus-5"}, imageRequest)
	msgs, _ := json.Marshal(body["messages"])
	// The pictures go before the question, as Anthropic advises.
	want := `[{"content":[{"source":{"data":"cG5n","media_type":"image/png","type":"base64"},"type":"image"},{"text":"what is it?","type":"text"}],"role":"user"},` +
		`{"content":[{"text":"a cat","type":"text"}],"role":"assistant"},` +
		`{"content":[{"source":{"data":"anBn","media_type":"image/jpeg","type":"base64"},"type":"image"}],"role":"user"}]`
	if string(msgs) != want {
		t.Errorf("messages %s\nwant %s", msgs, want)
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
			`{"choices":[{"delta":{"content":"Hi"}}],"usage":null}`,
			`{"choices":[{"delta":{"content":" there"}}],"usage":null}`,
			`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}`,
			`[DONE]`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", d)
		}
	}))
	defer srv.Close()

	cfg := Config{Protocol: OpenAI, BaseURL: srv.URL + "/v1/", APIKey: "sk-test", Model: "m", ThinkStyle: ThinkType}
	r, x, u, err := collect(t, cfg, Request{System: "be brief", Messages: []Message{{Role: "user", Content: "hello"}}, Thinking: true})
	if err != nil {
		t.Fatal(err)
	}
	if r != "Let me think." || x != "Hi there" {
		t.Errorf("reasoning %q, text %q", r, x)
	}
	if u == nil || *u != (Usage{Input: 12, Output: 34}) {
		t.Errorf("usage %+v", u)
	}
	if so, _ := json.Marshal(body["stream_options"]); string(so) != `{"include_usage":true}` {
		t.Errorf("stream_options %s", so)
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
	if _, _, _, err := collect(t, cfg, Request{Messages: []Message{{Role: "user", Content: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	if body["enable_thinking"] != false {
		t.Errorf("enable_thinking %v with thinking off", body["enable_thinking"])
	}
}

func TestOpenAIUsage(t *testing.T) {
	var bodies []map[string]any
	var reply []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if _, ok := body["stream_options"]; ok && r.URL.Path == "/strict/chat/completions" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, `{"object":"error","message":{"detail":[{"type":"extra_forbidden","loc":["body","stream_options"],"msg":"Extra inputs are not permitted"}]}}`)
			return
		}
		for _, d := range reply {
			fmt.Fprintf(w, "data: %s\n\n", d)
		}
	}))
	defer srv.Close()
	hello := Request{Messages: []Message{{Role: "user", Content: "hello"}}}

	// Kimi tells it in the last choice.
	reply = []string{
		`{"choices":[{"delta":{"content":"Hi"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop","usage":{"prompt_tokens":8,"completion_tokens":2}}]}`,
		`[DONE]`,
	}
	if _, _, u, err := collect(t, Config{BaseURL: srv.URL, Model: "m"}, hello); err != nil || u == nil || *u != (Usage{Input: 8, Output: 2}) {
		t.Errorf("usage %+v, error %v", u, err)
	}

	// Some tell it so far in each chunk: the last counts.
	reply = []string{
		`{"choices":[{"delta":{"content":"Hi"}}],"usage":{"prompt_tokens":8,"completion_tokens":1}}`,
		`{"choices":[{"delta":{"content":"!"}}],"usage":{"prompt_tokens":8,"completion_tokens":2}}`,
	}
	if _, _, u, err := collect(t, Config{BaseURL: srv.URL, Model: "m"}, hello); err != nil || u == nil || *u != (Usage{Input: 8, Output: 2}) {
		t.Errorf("usage %+v, error %v", u, err)
	}

	// A provider that does not take stream_options is asked again without
	// it, and replies without the usage.
	reply = []string{`{"choices":[{"delta":{"content":"Hi"}}]}`, `[DONE]`}
	bodies = nil
	_, x, u, err := collect(t, Config{BaseURL: srv.URL + "/strict", Model: "m"}, hello)
	if err != nil || x != "Hi" || u != nil {
		t.Errorf("text %q, usage %+v, error %v", x, u, err)
	}
	if len(bodies) != 2 || bodies[1]["stream_options"] != nil || bodies[1]["model"] != "m" {
		t.Errorf("requests %v", bodies)
	}
}

func TestOpenAIError(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()
	_, _, _, err := collect(t, Config{BaseURL: srv.URL, Model: "m"}, Request{Messages: []Message{{Role: "user", Content: "hello"}}})
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Incorrect API key") {
		t.Errorf("error %v", err)
	}
	if n != 1 {
		t.Errorf("asked %d times", n)
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
			{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"usage":{"input_tokens":5,"cache_read_input_tokens":100,"output_tokens":1}}}`},
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
	r, x, u, err := collect(t, cfg, Request{System: "sys", Messages: []Message{{Role: "user", Content: "hi"}}, Thinking: true})
	if err != nil {
		t.Fatal(err)
	}
	if r != "Pondering." || x != "Hello!" {
		t.Errorf("reasoning %q, text %q", r, x)
	}
	// What was read from the cache counts as read.
	if u == nil || *u != (Usage{Input: 105, Output: 9}) {
		t.Errorf("usage %+v", u)
	}
	if key != "ak" {
		t.Errorf("api key %q", key)
	}
	if th, _ := json.Marshal(body["thinking"]); string(th) != `{"display":"summarized","type":"adaptive"}` {
		t.Errorf("thinking %s", th)
	}
	if oc, _ := json.Marshal(body["output_config"]); string(oc) != `{"effort":"medium"}` {
		t.Errorf("output_config %s", oc)
	}
	collect(t, cfg, Request{Messages: []Message{{Role: "user", Content: "hi"}}, Thinking: true, Effort: EffortHigh})
	if oc, _ := json.Marshal(body["output_config"]); string(oc) != `{"effort":"high"}` {
		t.Errorf("output_config at high effort %s", oc)
	}

	// Off: disabled on a model that thinks adaptively.
	collect(t, cfg, Request{Messages: []Message{{Role: "user", Content: "hi"}}, Effort: EffortHigh})
	if th, _ := json.Marshal(body["thinking"]); string(th) != `{"type":"disabled"}` {
		t.Errorf("thinking off %s", th)
	}
	if oc, ok := body["output_config"]; ok {
		t.Errorf("output_config %v with thinking off", oc)
	}

	cfg.Model = "claude-sonnet-5-5"
	collect(t, cfg, Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if th, _ := json.Marshal(body["thinking"]); string(th) != `{"type":"between_tools"}` {
		t.Errorf("thinking off on Sonnet 5.5 %s", th)
	}
	collect(t, cfg, Request{Messages: []Message{{Role: "user", Content: "hi"}}, Thinking: true})
	if th, _ := json.Marshal(body["thinking"]); string(th) != `{"display":"summarized","type":"adaptive"}` {
		t.Errorf("thinking on Sonnet 5.5 %s", th)
	}

	// Models that cannot stop thinking think less instead.
	cfg.Model = "claude-opus-5-5"
	collect(t, cfg, Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if _, ok := body["thinking"]; ok {
		t.Errorf("thinking sent to a model that always thinks: %v", body["thinking"])
	}
	if oc, _ := json.Marshal(body["output_config"]); string(oc) != `{"effort":"low"}` {
		t.Errorf("output_config %s", oc)
	}

	// Other providers' models take a budget.
	cfg.Model = "deepseek-chat"
	collect(t, cfg, Request{Messages: []Message{{Role: "user", Content: "hi"}}, Thinking: true})
	if th, _ := json.Marshal(body["thinking"]); string(th) != `{"budget_tokens":4096,"type":"enabled"}` {
		t.Errorf("budget thinking %s", th)
	}
	if oc, ok := body["output_config"]; ok {
		t.Errorf("output_config %v sent with a budget", oc)
	}
	for effort, want := range map[Effort]string{EffortLow: "2048", EffortHigh: "6144"} {
		collect(t, cfg, Request{Messages: []Message{{Role: "user", Content: "hi"}}, Thinking: true, Effort: effort})
		if th, _ := json.Marshal(body["thinking"]); string(th) != `{"budget_tokens":`+want+`,"type":"enabled"}` {
			t.Errorf("budget thinking at %s effort %s", effort, th)
		}
	}
	cfg.Model, cfg.MaxTokens = "claude-sonnet-4-5", 64000
	collect(t, cfg, Request{Messages: []Message{{Role: "user", Content: "hi"}}, Thinking: true, Effort: EffortHigh})
	if th, _ := json.Marshal(body["thinking"]); string(th) != `{"budget_tokens":32000,"type":"enabled"}` {
		t.Errorf("budget thinking at high effort %s", th)
	}
}

func TestOpenAIEffort(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = nil
		json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	off := Effort("off")
	cases := []struct {
		style  ThinkStyle
		effort Effort
		want   string
	}{
		{ThinkEffort, off, `{}`},
		{ThinkEffort, EffortLow, `{"reasoning_effort":"low"}`},
		{ThinkEffort, "", `{"reasoning_effort":"medium"}`},
		{ThinkEffort, EffortHigh, `{"reasoning_effort":"high"}`},
		{ThinkEnable, off, `{"enable_thinking":false}`},
		{ThinkEnable, EffortLow, `{"enable_thinking":true,"thinking_budget":2048}`},
		{ThinkEnable, EffortMedium, `{"enable_thinking":true,"thinking_budget":16384}`},
		{ThinkEnable, EffortHigh, `{"enable_thinking":true,"thinking_budget":32768}`},
		{ThinkType, off, `{"thinking":{"type":"disabled"}}`},
		{ThinkType, EffortLow, `{"reasoning_effort":"low","thinking":{"type":"enabled"}}`},
		{ThinkType, EffortMedium, `{"thinking":{"type":"enabled"}}`},
		{ThinkType, EffortHigh, `{"reasoning_effort":"max","thinking":{"type":"enabled"}}`},
		{ThinkOpenRouter, off, `{}`},
		{ThinkOpenRouter, EffortLow, `{"reasoning":{"effort":"low"}}`},
		{ThinkOpenRouter, EffortHigh, `{"reasoning":{"effort":"high"}}`},
		{ThinkNone, EffortHigh, `{}`},
	}
	for _, c := range cases {
		req := Request{Messages: []Message{{Role: "user", Content: "hi"}}, Thinking: c.effort != off, Effort: c.effort}
		if _, _, _, err := collect(t, Config{BaseURL: srv.URL, Model: "m", ThinkStyle: c.style}, req); err != nil {
			t.Fatal(err)
		}
		got := map[string]any{}
		for _, k := range []string{"reasoning_effort", "enable_thinking", "thinking_budget", "thinking", "reasoning"} {
			if v, ok := body[k]; ok {
				got[k] = v
			}
		}
		if b, _ := json.Marshal(got); string(b) != c.want {
			t.Errorf("%s at %q: %s, want %s", c.style, c.effort, b, c.want)
		}
	}
}
