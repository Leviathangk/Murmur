package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type scripted struct {
	bodies []map[string]any
	paths  []string
}

func scriptServer(t *testing.T, replies ...func(w http.ResponseWriter, n int)) (*httptest.Server, *scripted) {
	t.Helper()
	s := &scripted{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		s.bodies = append(s.bodies, body)
		s.paths = append(s.paths, r.URL.Path)
		n := len(s.bodies)
		w.Header().Set("Content-Type", "text/event-stream")
		replies[min(n, len(replies))-1](w, n)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

func sse(chunks ...string) func(w http.ResponseWriter, n int) {
	return func(w http.ResponseWriter, n int) {
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
	}
}

type ran struct {
	inputs []string
}

func searchTool(r *ran, reply func(input string) (string, error)) Tool {
	return Tool{
		Name:        "web_search",
		Description: "Search the web.",
		Properties:  map[string]any{"query": map[string]any{"type": "string", "description": "What to search for"}},
		Required:    []string{"query"},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			r.inputs = append(r.inputs, string(input))
			return reply(string(input))
		},
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestOpenAITools(t *testing.T) {
	srv, s := scriptServer(t,
		sse(
			`{"choices":[{"delta":{"role":"assistant","reasoning_content":"R1"}}]}`,
			`{"choices":[{"delta":{"content":"Let me search."}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"web_search","arguments":""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"go 1.27\"}"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"b\"}"},"extra_content":{"google":{"thought_signature":"sig"}}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
			`[DONE]`,
		),
		sse(
			`{"choices":[{"delta":{"reasoning_content":"R2"}}]}`,
			`{"choices":[{"delta":{"content":"Go 1.27 is out [1]."}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":7}}`,
			`[DONE]`,
		),
	)
	var r ran
	tool := searchTool(&r, func(input string) (string, error) { return "result " + input, nil })
	req := Request{
		System: "sys",
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello", Reasoning: "old thought"},
			{Role: "user", Content: "what's new in Go?"},
		},
		Thinking: true,
		Tools:    []Tool{tool},
	}
	reasoning, text, usage, err := collect(t, Config{BaseURL: srv.URL, Model: "m", ThinkStyle: ThinkType}, req)
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "R1\n\nR2" || text != "Let me search.\n\nGo 1.27 is out [1]." {
		t.Errorf("reasoning %q, text %q", reasoning, text)
	}
	if usage == nil || *usage != (Usage{Input: 30, Output: 12}) {
		t.Errorf("usage %+v", usage)
	}
	if fmt.Sprint(r.inputs) != `[{"query":"go 1.27"} {"query":"b"}]` {
		t.Errorf("tool inputs %q", r.inputs)
	}
	if len(s.bodies) != 2 {
		t.Fatalf("%d requests", len(s.bodies))
	}
	want := `[{"function":{"description":"Search the web.","name":"web_search","parameters":{"properties":{"query":{"description":"What to search for","type":"string"}},"required":["query"],"type":"object"}},"type":"function"}]`
	if got := jsonOf(s.bodies[0]["tools"]); got != want {
		t.Errorf("tools %s\nwant %s", got, want)
	}
	if got := jsonOf(s.bodies[0]["messages"]); got != `[{"content":"sys","role":"system"},{"content":"hi","role":"user"},{"content":"hello","role":"assistant"},{"content":"what's new in Go?","role":"user"}]` {
		t.Errorf("first messages %s", got)
	}
	msgs := s.bodies[1]["messages"].([]any)
	if len(msgs) != 7 {
		t.Fatalf("second messages %s", jsonOf(msgs))
	}
	want = `[{"content":"Let me search.","role":"assistant","tool_calls":[` +
		`{"function":{"arguments":"{\"query\":\"go 1.27\"}","name":"web_search"},"id":"call_a","type":"function"},` +
		`{"extra_content":{"google":{"thought_signature":"sig"}},"function":{"arguments":"{\"query\":\"b\"}","name":"web_search"},"id":"call_b","type":"function"}]},` +
		`{"content":"result {\"query\":\"go 1.27\"}","role":"tool","tool_call_id":"call_a"},` +
		`{"content":"result {\"query\":\"b\"}","role":"tool","tool_call_id":"call_b"}]`
	if got := jsonOf(msgs[4:]); got != want {
		t.Errorf("second messages %s\nwant %s", got, want)
	}
	if jsonOf(s.bodies[1]["tools"]) != jsonOf(s.bodies[0]["tools"]) || jsonOf(s.bodies[1]["thinking"]) != `{"type":"enabled"}` {
		t.Errorf("second request tools %s, thinking %s", jsonOf(s.bodies[1]["tools"]), jsonOf(s.bodies[1]["thinking"]))
	}

	s.bodies = nil
	req.Tools = nil
	if _, _, _, err := collect(t, Config{BaseURL: srv.URL, Model: "m"}, req); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.bodies[0]["tools"]; ok {
		t.Errorf("without tools: %s", jsonOf(s.bodies[0]))
	}
}

func TestKeepsReasoning(t *testing.T) {
	for base, want := range map[string]bool{
		"https://api.deepseek.com":                                true,
		"https://api.deepseek.com/v1":                             true,
		"https://api.moonshot.cn/v1":                              true,
		"https://api.moonshot.ai/v1":                              true,
		"https://open.bigmodel.cn/api/paas/v4":                    true,
		"https://api.z.ai/api/paas/v4":                            true,
		"https://api.openai.com/v1":                               false,
		"https://generativelanguage.googleapis.com/v1beta/openai": false,
		"https://dashscope.aliyuncs.com/compatible-mode/v1":       false,
		"https://notdeepseek.com/v1":                              false,
		"http://localhost:11434/v1":                               false,
	} {
		if got := keepsReasoning(base); got != want {
			t.Errorf("%s: %v, want %v", base, got, want)
		}
	}
}

func TestReasoningAskedBack(t *testing.T) {
	srv, s := scriptServer(t,
		func(w http.ResponseWriter, n int) {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"The reasoning_content in the thinking mode must be passed back to the API.","type":"invalid_request_error"}}`)
		},
		sse(
			`{"choices":[{"delta":{"reasoning_content":"R1"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"x\"}"}}]}}]}`,
			`[DONE]`,
		),
		sse(`{"choices":[{"delta":{"content":"done"}}]}`, `[DONE]`),
	)
	var r ran
	tool := searchTool(&r, func(string) (string, error) { return "ok", nil })
	req := Request{Messages: []Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b", Reasoning: "old"}, {Role: "user", Content: "c"}, {Role: "assistant", Content: "d"}, {Role: "user", Content: "e"}}, Thinking: true, Tools: []Tool{tool}}
	_, text, _, err := collect(t, Config{BaseURL: srv.URL, Model: "m", ThinkStyle: ThinkType}, req)
	if err != nil || text != "done" || len(s.bodies) != 3 {
		t.Fatalf("text %q, %d requests, error %v", text, len(s.bodies), err)
	}
	if got := jsonOf(s.bodies[0]["messages"]); strings.Contains(got, "reasoning_content") {
		t.Errorf("first messages %s", got)
	}
	if got := jsonOf(s.bodies[1]["messages"]); !strings.Contains(got, `{"content":"b","reasoning_content":"old","role":"assistant"}`) || !strings.Contains(got, `{"content":"d","reasoning_content":"","role":"assistant"}`) || strings.Count(got, "reasoning_content") != 2 {
		t.Errorf("messages asked again %s", got)
	}
	msgs := s.bodies[2]["messages"].([]any)
	if got := jsonOf(msgs[5]); got != `{"content":null,"reasoning_content":"R1","role":"assistant","tool_calls":[{"function":{"arguments":"{\"query\":\"x\"}","name":"web_search"},"id":"c","type":"function"}]}` {
		t.Errorf("assistant message %s", got)
	}

	srv, s = scriptServer(t, func(w http.ResponseWriter, n int) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"reasoning_content is required"}}`)
	})
	_, _, _, err = collect(t, Config{BaseURL: srv.URL, Model: "m"}, req)
	if err == nil || len(s.bodies) != 2 {
		t.Errorf("%d requests, error %v", len(s.bodies), err)
	}
}

func TestOpenAIToolCallShapes(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		inputs string
		calls  string
	}{
		{
			"no index, the id once",
			[]string{
				`{"choices":[{"delta":{"tool_calls":[{"id":"c1","type":"function","function":{"name":"web_search","arguments":"{\"qu"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"ery\":\"a\"}"}}]}}]}`,
			},
			`[{"query":"a"}]`,
			`[{"function":{"arguments":"{\"query\":\"a\"}","name":"web_search"},"id":"c1","type":"function"}]`,
		},
		{
			"one index, two ids",
			[]string{
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"a\"}"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c2","type":"function","function":{"name":"web_search","arguments":"{\"query\":"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"b\"}"}}]}}]}`,
			},
			`[{"query":"a"} {"query":"b"}]`,
			`[{"function":{"arguments":"{\"query\":\"a\"}","name":"web_search"},"id":"c1","type":"function"},{"function":{"arguments":"{\"query\":\"b\"}","name":"web_search"},"id":"c2","type":"function"}]`,
		},
		{
			"arguments cut off",
			[]string{
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"a"}}]}}]}`,
			},
			`[{"query":"a]`,
			`[{"function":{"arguments":"{}","name":"web_search"},"id":"c1","type":"function"}]`,
		},
		{
			"no id, no arguments, whole in one chunk",
			[]string{
				`{"choices":[{"delta":{"tool_calls":[{"index":3,"type":"function","function":{"name":"web_search"}}]}}]}`,
			},
			`[{}]`,
			`[{"function":{"arguments":"{}","name":"web_search"},"id":"call_0_0","type":"function"}]`,
		},
	}
	for _, c := range cases {
		chunks := append(c.chunks, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`, `[DONE]`)
		srv, s := scriptServer(t, sse(chunks...), sse(`{"choices":[{"delta":{"content":"done"}}]}`, `[DONE]`))
		var r ran
		tool := searchTool(&r, func(string) (string, error) { return "ok", nil })
		_, text, _, err := collect(t, Config{BaseURL: srv.URL, Model: "m"}, Request{Messages: []Message{{Role: "user", Content: "q"}}, Tools: []Tool{tool}})
		if err != nil || text != "done" {
			t.Errorf("%s: text %q, error %v", c.name, text, err)
			continue
		}
		if fmt.Sprint(r.inputs) != c.inputs {
			t.Errorf("%s: inputs %q, want %s", c.name, r.inputs, c.inputs)
		}
		msgs := s.bodies[1]["messages"].([]any)
		assistant := msgs[1].(map[string]any)
		if got := jsonOf(assistant["tool_calls"]); got != c.calls {
			t.Errorf("%s: calls %s\nwant %s", c.name, got, c.calls)
		}
		if assistant["content"] != nil {
			t.Errorf("%s: content %v without text", c.name, assistant["content"])
		}
		ids := assistant["tool_calls"].([]any)
		for i, m := range msgs[2:] {
			if m.(map[string]any)["tool_call_id"] != ids[i].(map[string]any)["id"] {
				t.Errorf("%s: tool message %v", c.name, m)
			}
		}
	}
}

func TestToolFailures(t *testing.T) {
	call := func(name, args string) string {
		return `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"` + name + `","arguments":"` + args + `"}}]}}]}`
	}
	srv, s := scriptServer(t,
		sse(call("web_search", `{\"query\":\"a\"}`), `[DONE]`),
		sse(call("fetch_page", `{}`), `[DONE]`),
		sse(`{"choices":[{"delta":{"content":"sorry"}}]}`, `[DONE]`),
	)
	var r ran
	tool := searchTool(&r, func(string) (string, error) { return "", errors.New("搜索失败：额度用完") })
	_, text, _, err := collect(t, Config{BaseURL: srv.URL, Model: "m"}, Request{Messages: []Message{{Role: "user", Content: "q"}}, Tools: []Tool{tool}})
	if err != nil || text != "sorry" {
		t.Fatalf("text %q, error %v", text, err)
	}
	msgs := s.bodies[2]["messages"].([]any)
	if got := msgs[2].(map[string]any)["content"]; got != "搜索失败：额度用完" {
		t.Errorf("failed search told %q", got)
	}
	if got := msgs[4].(map[string]any)["content"]; got != `Unknown tool "fetch_page"` {
		t.Errorf("unknown tool told %q", got)
	}

	srv, s = scriptServer(t, sse(call("web_search", `{\"query\":\"again\"}`), `[DONE]`))
	r = ran{}
	tool = searchTool(&r, func(string) (string, error) { return "ok", nil })
	_, _, _, err = collect(t, Config{BaseURL: srv.URL, Model: "m"}, Request{Messages: []Message{{Role: "user", Content: "q"}}, Tools: []Tool{tool}})
	if err != nil || len(s.bodies) != maxToolRounds || len(r.inputs) != maxToolRounds-1 {
		t.Errorf("%d requests, %d searches, error %v", len(s.bodies), len(r.inputs), err)
	}
	for i, b := range s.bodies {
		if got, want := b["tool_choice"], map[bool]any{true: "none", false: nil}[i == maxToolRounds-1]; got != want {
			t.Errorf("request %d: tool_choice %v", i, got)
		}
	}
}

func TestAnthropicToolRounds(t *testing.T) {
	srv, s := scriptServer(t, func(w http.ResponseWriter, n int) {
		anthropicEvents(w,
			`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t`+fmt.Sprint(n)+`","name":"web_search","input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"x\"}"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":2}}`,
			`{"type":"message_stop"}`,
		)
	})
	var r ran
	tool := searchTool(&r, func(string) (string, error) { return "ok", nil })
	_, _, _, err := collect(t, Config{Protocol: Anthropic, BaseURL: srv.URL, APIKey: "k", Model: "claude-opus-5"}, Request{Messages: []Message{{Role: "user", Content: "q"}}, Tools: []Tool{tool}})
	if err != nil || len(s.bodies) != maxToolRounds || len(r.inputs) != maxToolRounds-1 {
		t.Fatalf("%d requests, %d searches, error %v", len(s.bodies), len(r.inputs), err)
	}
	if got := jsonOf(s.bodies[maxToolRounds-1]["tool_choice"]); got != `{"type":"none"}` {
		t.Errorf("last tool_choice %s", got)
	}
	if _, ok := s.bodies[0]["tool_choice"]; ok {
		t.Errorf("first tool_choice %v", s.bodies[0]["tool_choice"])
	}
}

func TestToolsRejected(t *testing.T) {
	cases := []struct {
		round, status int
		msg           string
		want          bool
	}{
		{0, 404, "No endpoints found that support tool use. To learn more about provider routing, visit: https://openrouter.ai/docs/provider-routing", true},
		{0, 404, "model not found", false},
		{0, 400, "registry.ollama.ai/library/gemma2:latest does not support tools", true},
		{0, 422, `[{"type":"extra_forbidden","loc":["body","tools"],"msg":"Extra inputs are not permitted"}]`, true},
		{0, 400, "This model's maximum context length is 128000 tokens. However, your messages resulted in 130000 tokens (including 120 in the functions).", false},
		{0, 400, "Invalid value for 'function.name'", false},
		{0, 401, "tools not supported", false},
		{1, 400, "messages[2].tool_calls[0].extra_content: unknown field", true},
		{1, 400, "Thought signature is not valid", true},
		{1, 400, "The reasoning_content in the thinking mode must be passed back to the API.", false},
		{1, 400, "Input is too long for requested model", false},
		{1, 429, "rate limited", false},
		{1, 404, "not found", false},
	}
	for _, c := range cases {
		if got := toolsRejected(c.round, c.status, c.msg); got != c.want {
			t.Errorf("round %d, %d %q: %v", c.round, c.status, c.msg, got)
		}
	}
}

func TestToolLoopRejected(t *testing.T) {
	srv, _ := scriptServer(t,
		sse(`{"choices":[{"delta":{"content":"Let me look."}}]}`, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"web_search","arguments":"{}"}}]}}]}`, `[DONE]`),
		func(w http.ResponseWriter, n int) {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"Function call is missing a thought_signature in functionCall parts."}}`)
		},
	)
	tool := searchTool(&ran{}, func(string) (string, error) { return "ok", nil })
	_, text, _, err := collect(t, Config{BaseURL: srv.URL, Model: "m"}, Request{Messages: []Message{{Role: "user", Content: "q"}}, Tools: []Tool{tool}})
	if !errors.Is(err, ErrToolsUnsupported) || text != "Let me look." {
		t.Errorf("text %q, error %v", text, err)
	}
}

func TestToolsCanceled(t *testing.T) {
	srv, s := scriptServer(t, sse(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"web_search","arguments":"{}"}}]}}]}`, `[DONE]`))
	ctx, cancel := context.WithCancel(context.Background())
	tool := Tool{Name: "web_search", Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
		cancel()
		return "", ctx.Err()
	}}
	err := Stream(ctx, Config{BaseURL: srv.URL, Model: "m"}, Request{Messages: []Message{{Role: "user", Content: "q"}}, Tools: []Tool{tool}}, func(Event) {})
	if err != context.Canceled || len(s.bodies) != 1 {
		t.Errorf("error %v after %d requests", err, len(s.bodies))
	}
}

func TestToolsUnsupported(t *testing.T) {
	reject := func(code int, body string) func(w http.ResponseWriter, n int) {
		return func(w http.ResponseWriter, n int) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			io.WriteString(w, body)
		}
	}
	tool := Tool{Name: "web_search", Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }}
	req := Request{Messages: []Message{{Role: "user", Content: "q"}}, Tools: []Tool{tool}}
	cases := []struct {
		protocol Protocol
		code     int
		body     string
		want     bool
	}{
		{OpenAI, 400, `{"error":{"message":"registry.ollama.ai/library/gemma2:latest does not support tools"}}`, true},
		{OpenAI, 400, `{"error":{"message":"deepseek-reasoner does not support Function Calling"}}`, true},
		{OpenAI, 400, `{"object":"error","message":"\"auto\" tool choice requires --enable-auto-tool-choice and --tool-call-parser to be set"}`, true},
		{OpenAI, 401, `{"error":{"message":"Invalid tool access key"}}`, false},
		{OpenAI, 400, `{"error":{"message":"context length exceeded"}}`, false},
		{OpenAI, 500, `{"error":{"message":"tool server down"}}`, false},
		{Anthropic, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"tools: this model does not support tool use"}}`, true},
		{Anthropic, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long"}}`, false},
	}
	for _, c := range cases {
		srv, _ := scriptServer(t, reject(c.code, c.body))
		_, _, _, err := collect(t, Config{Protocol: c.protocol, BaseURL: srv.URL, APIKey: "k", Model: "m"}, req)
		if err == nil || errors.Is(err, ErrToolsUnsupported) != c.want || !strings.Contains(err.Error(), fmt.Sprint(c.code)) {
			t.Errorf("%s %d %s: error %v", c.protocol, c.code, c.body, err)
		}
	}
	srv, _ := scriptServer(t, reject(400, `{"error":{"message":"does not support tools"}}`))
	if _, _, _, err := collect(t, Config{BaseURL: srv.URL, Model: "m"}, Request{Messages: req.Messages}); errors.Is(err, ErrToolsUnsupported) {
		t.Errorf("tools unsupported without tools: %v", err)
	}
}

func anthropicEvents(w http.ResponseWriter, events ...string) {
	for _, e := range events {
		var v struct {
			Type string `json:"type"`
		}
		json.Unmarshal([]byte(e), &v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", v.Type, e)
	}
}

func TestAnthropicTools(t *testing.T) {
	srv, s := scriptServer(t,
		func(w http.ResponseWriter, n int) {
			anthropicEvents(w,
				`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Hmm"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig1"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Searching."}}`,
				`{"type":"content_block_stop","index":1}`,
				`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_stop","index":2}`,
				`{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_1","name":"web_search","input":{}}}`,
				`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"query\":"}}`,
				`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"\"x\"}"}}`,
				`{"type":"content_block_stop","index":3}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":20}}`,
				`{"type":"message_stop"}`,
			)
		},
		func(w http.ResponseWriter, n int) {
			anthropicEvents(w,
				`{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"usage":{"input_tokens":50,"cache_read_input_tokens":5,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Answer [1]."}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":8}}`,
				`{"type":"message_stop"}`,
			)
		},
	)
	var r ran
	tool := searchTool(&r, func(string) (string, error) { return "[1] X", nil })
	req := Request{Messages: []Message{{Role: "user", Content: "q"}, {Role: "assistant", Content: "a", Reasoning: "kept here"}, {Role: "user", Content: "x?"}}, Thinking: true, Tools: []Tool{tool}}
	reasoning, text, usage, err := collect(t, Config{Protocol: Anthropic, BaseURL: srv.URL, APIKey: "k", Model: "claude-opus-5"}, req)
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "Hmm" || text != "Searching.\n\nAnswer [1]." {
		t.Errorf("reasoning %q, text %q", reasoning, text)
	}
	if usage == nil || *usage != (Usage{Input: 65, Output: 28}) {
		t.Errorf("usage %+v", usage)
	}
	if fmt.Sprint(r.inputs) != `[{"query":"x"}]` {
		t.Errorf("inputs %q", r.inputs)
	}
	want := `[{"description":"Search the web.","input_schema":{"properties":{"query":{"description":"What to search for","type":"string"}},"required":["query"],"type":"object"},"name":"web_search"}]`
	if got := jsonOf(s.bodies[0]["tools"]); got != want {
		t.Errorf("tools %s\nwant %s", got, want)
	}
	if strings.Contains(jsonOf(s.bodies[0]["messages"]), "kept here") {
		t.Errorf("reasoning sent to Anthropic: %s", jsonOf(s.bodies[0]["messages"]))
	}
	msgs := s.bodies[1]["messages"].([]any)
	want = `[{"content":[{"signature":"sig1","thinking":"Hmm","type":"thinking"},{"text":"Searching.","type":"text"},{"id":"toolu_1","input":{"query":"x"},"name":"web_search","type":"tool_use"}],"role":"assistant"},` +
		`{"content":[{"content":[{"text":"[1] X","type":"text"}],"is_error":false,"tool_use_id":"toolu_1","type":"tool_result"}],"role":"user"}]`
	if got := jsonOf(msgs[3:]); got != want {
		t.Errorf("second messages %s\nwant %s", got, want)
	}
	if jsonOf(s.bodies[1]["thinking"]) != jsonOf(s.bodies[0]["thinking"]) || jsonOf(s.bodies[1]["tools"]) != jsonOf(s.bodies[0]["tools"]) {
		t.Errorf("second request thinking %s tools %s", jsonOf(s.bodies[1]["thinking"]), jsonOf(s.bodies[1]["tools"]))
	}
}
