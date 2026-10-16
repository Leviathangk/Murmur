package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/llm"
	"github.com/Qsnh/Murmur/locales"
	"github.com/Qsnh/Murmur/websearch"
)

var goResults = map[string][]websearch.Result{
	"Go 1.27": {
		{Title: "Go 1.27 is released", URL: "https://go.dev/blog/go1.27", Content: "Go 1.27 brings…", Published: "2026-08-12T00:00:00Z"},
		{Title: "Go 1.27 Release Notes", URL: "https://go.dev/doc/go1.27", Content: "The latest Go release…"},
	},
	"Go release": {
		{Title: "Go 1.27 is released", URL: "https://go.dev/blog/go1.27", Content: "Go 1.27 brings…", Published: "2026-08-12T00:00:00Z"},
		{Title: "Release History", URL: "https://www.go.dev/doc/devel/release", Content: "go1.27.1 (released 2026-09-03)", Published: "Thu, 03 Sep 2026 10:00:00 GMT"},
	},
}

func searchingApp(t *testing.T) (*app, *[]llm.Request, *[]string) {
	t.Helper()
	a, reqs := testApp(t)
	a.addModel(presets[2])
	var queries []string
	a.webSearch = func(ctx context.Context, key, proxy, query string) ([]websearch.Result, error) {
		if key != "tvly-test" {
			return nil, fmt.Errorf("key %q", key)
		}
		queries = append(queries, query)
		return goResults[query], nil
	}
	return a, reqs, &queries
}

func run(t *testing.T, req llm.Request, query string) (string, error) {
	t.Helper()
	if len(req.Tools) != 1 || req.Tools[0].Name != "web_search" {
		t.Fatalf("tools %+v", req.Tools)
	}
	input, _ := json.Marshal(map[string]string{"query": query})
	return req.Tools[0].Run(context.Background(), input)
}

func TestWebSearch(t *testing.T) {
	a, reqs, queries := searchingApp(t)
	a.settings.SystemPrompt = "回答要简短"
	var results []string
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		*reqs = append(*reqs, req)
		if len(req.Tools) == 0 {
			emit(llm.Event{Text: "好的"})
			return nil
		}
		emit(llm.Event{Reasoning: "要查一下。"})
		for _, q := range []string{"Go 1.27", "  Go\n release "} {
			out, err := run(t, req, q)
			if err != nil {
				return err
			}
			results = append(results, out)
		}
		emit(llm.Event{Text: "Go 1.27 已发布 [1]，最新是 1.27.1 [3]。"})
		emit(llm.Event{Usage: &llm.Usage{Input: 3000, Output: 80}})
		return nil
	}
	tt := ui.NewTester(a.view, 1080, 720)
	if !tt.HasText("联网搜索") || a.searchOn() {
		t.Fatalf("search on %v, texts %q", a.searchOn(), tt.Texts())
	}

	a.settings.TavilyKey = "tvly-test"
	tt.Frame()
	click(t, tt, "联网搜索")
	if !a.searchOn() {
		t.Fatal("search still off")
	}
	snapshot(t, tt, "search-on")

	a.send("Go 最新版本是多少？")
	tt.Frame()
	req := (*reqs)[0]
	if !strings.HasPrefix(req.System, "回答要简短\n\nToday is ") || !strings.Contains(req.System, "web_search tool") || !strings.Contains(req.System, "Cite those you use as [1]") {
		t.Errorf("system %q", req.System)
	}
	if fmt.Sprint(*queries) != "[Go 1.27 Go release]" {
		t.Errorf("queries %q", *queries)
	}
	want := "Results for \"Go 1.27\":\n\n[1] Go 1.27 is released\nURL: https://go.dev/blog/go1.27\nPublished: 2026-08-12T00:00:00Z\nGo 1.27 brings…\n\n[2] Go 1.27 Release Notes\nURL: https://go.dev/doc/go1.27\nThe latest Go release…"
	if results[0] != want {
		t.Errorf("first results %q\nwant %q", results[0], want)
	}
	if !strings.Contains(results[1], "\n[1] Go 1.27 is released\n") || !strings.Contains(results[1], "\n[3] Release History\n") {
		t.Errorf("second results %q", results[1])
	}

	reply := a.current.Messages[1]
	if len(reply.Searches) != 2 || reply.Searches[1].Query != "Go release" || len(reply.Sources) != 3 || reply.Sources[2].Title != "Release History" {
		t.Fatalf("searches %+v, sources %+v", reply.Searches, reply.Sources)
	}
	if reply.Content != "Go 1.27 已发布 [1]，最新是 1.27.1 [3]。" || reply.searching != "" {
		t.Errorf("content %q, searching %q", reply.Content, reply.searching)
	}
	if !tt.HasText("已搜索 2 次 · 3 个网页") || tt.HasText("Release History") {
		t.Fatalf("texts %q", tt.Texts())
	}
	click(t, tt, "已搜索 2 次 · 3 个网页")
	for _, s := range []string{"搜索「Go 1.27」", "搜索「Go release」", "[3]", "Release History", "go.dev · 2026-08-12", "go.dev · 2026-09-03", "go.dev"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	time.Sleep(400 * time.Millisecond)
	tt.Frame()
	snapshot(t, tt, "search-sources")

	b := newApp(a.store)
	if !b.settings.WebSearch || b.settings.TavilyKey != "tvly-test" {
		t.Errorf("loaded settings %+v", b.settings)
	}
	if m := b.chats[0].Messages[1]; len(m.Searches) != 2 || len(m.Sources) != 3 || m.Sources[0].Published != "2026-08-12T00:00:00Z" {
		t.Errorf("loaded reply %+v", m)
	}

	a.send("谢谢")
	if r := (*reqs)[1]; len(r.Messages) != 3 || r.Messages[1].Reasoning != "要查一下。" {
		t.Errorf("second request %+v", r.Messages)
	}

	a.setSearch(false)
	a.send("再见")
	if r := (*reqs)[2]; len(r.Tools) != 0 || r.System != "回答要简短" {
		t.Errorf("request with search off: tools %d, system %q", len(r.Tools), r.System)
	}
}

func TestSearchNeedsKey(t *testing.T) {
	a, _, _ := searchingApp(t)
	tt := ui.NewTester(a.view, 1080, 720)
	click(t, tt, "联网搜索")
	if a.settings.WebSearch || !a.settingsOpen || a.settingsTab != 1 || !tt.HasText("先在设置里填写 Tavily API Key，才能联网搜索") {
		t.Fatalf("search on %v, settings open %v on tab %d, texts %q", a.settings.WebSearch, a.settingsOpen, a.settingsTab, tt.Texts())
	}
	a.settings.WebSearch = true
	if a.searchOn() {
		t.Error("search on without a key")
	}
}

func TestSearching(t *testing.T) {
	a, _, _ := searchingApp(t)
	a.settings.TavilyKey, a.settings.WebSearch = "tvly-test", true
	tt := ui.NewTester(a.view, 1080, 720)
	shown := false
	a.webSearch = func(ctx context.Context, key, proxy, query string) ([]websearch.Result, error) {
		tt.Frame()
		shown = tt.HasText("正在搜索：Go 1.27") && !tt.HasText("正在等待回复…")
		snapshot(t, tt, "searching")
		return nil, errors.New("Tavily API Key 无效，请在设置里检查")
	}
	var told []string
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		_, err := run(t, req, "Go 1.27")
		told = append(told, err.Error())
		_, err = run(t, req, " ")
		told = append(told, err.Error())
		emit(llm.Event{Text: "搜索失败了，根据已有知识回答。"})
		return nil
	}
	a.send("Go 最新版本？")
	tt.Frame()
	if !shown {
		t.Error("the search did not show while it ran")
	}
	if fmt.Sprint(told) != `[Search failed: Tavily API Key 无效，请在设置里检查 Give what to search for, as {"query": "…"}]` {
		t.Errorf("told %q", told)
	}
	reply := a.current.Messages[1]
	if len(reply.Searches) != 1 || reply.Searches[0].Error == "" || len(reply.Sources) != 0 || reply.Error != "" {
		t.Fatalf("reply %+v", reply)
	}
	click(t, tt, "联网搜索失败")
	if !tt.HasText("搜索失败：Tavily API Key 无效，请在设置里检查") || tt.HasText("没有结果") {
		t.Errorf("texts %q", tt.Texts())
	}
	time.Sleep(400 * time.Millisecond)
	tt.Frame()
	snapshot(t, tt, "search-failed")
}

func TestSearchLimit(t *testing.T) {
	a, _, queries := searchingApp(t)
	a.settings.TavilyKey, a.settings.WebSearch = "tvly-test", true
	var last error
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		for i := 0; i <= maxSearches; i++ {
			_, last = run(t, req, fmt.Sprintf("q%d", i))
		}
		emit(llm.Event{Text: "ok"})
		return nil
	}
	a.send("hi")
	if len(*queries) != maxSearches || last == nil || !strings.Contains(last.Error(), "No more searches") {
		t.Errorf("%d searches, last error %v", len(*queries), last)
	}
	if n := len(a.current.Messages[1].Searches); n != maxSearches {
		t.Errorf("%d searches recorded", n)
	}
	tt := ui.NewTester(a.view, 1080, 720)
	click(t, tt, "已搜索 5 次 · 0 个网页")
	if !tt.HasText("没有结果") || !tt.HasText("搜索「q4」") {
		t.Errorf("texts %q", tt.Texts())
	}

	a.settings.TavilyKey = " "
	var tools int
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		tools = len(req.Tools)
		emit(llm.Event{Text: "ok"})
		return nil
	}
	a.send("hi")
	if tools != 0 || a.searchOn() {
		t.Errorf("searching without a key: %d tools", tools)
	}
}

func TestSearchFirst(t *testing.T) {
	a, reqs, queries := searchingApp(t)
	a.settings.TavilyKey, a.settings.WebSearch = "tvly-test", true
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		*reqs = append(*reqs, req)
		if len(req.Tools) > 0 {
			return fmt.Errorf("HTTP 400：gemma3 does not support tools: %w", llm.ErrToolsUnsupported)
		}
		emit(llm.Event{Text: "Go 1.27 [1]"})
		return nil
	}
	a.send("Go 1.27")
	if len(*reqs) != 2 || fmt.Sprint(*queries) != "[Go 1.27]" {
		t.Fatalf("%d requests, queries %q", len(*reqs), *queries)
	}
	req := (*reqs)[1]
	last := req.Messages[len(req.Messages)-1]
	if !strings.HasPrefix(last.Content, "<search_results>\nResults for \"Go 1.27\":\n\n[1] Go 1.27 is released\n") || !strings.HasSuffix(last.Content, "</search_results>\n\nGo 1.27") {
		t.Errorf("message %q", last.Content)
	}
	if strings.Contains(req.System, "web_search tool") || !strings.Contains(req.System, "comes with results of a web search") {
		t.Errorf("system %q", req.System)
	}
	if m := a.current.Messages[0]; m.Content != "Go 1.27" {
		t.Errorf("the user's message changed: %q", m.Content)
	}
	reply := a.current.Messages[1]
	if reply.Error != "" || reply.Content != "Go 1.27 [1]" || len(reply.Searches) != 1 || len(reply.Sources) != 2 {
		t.Errorf("reply %+v", reply)
	}

	*reqs = nil
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		*reqs = append(*reqs, req)
		return fmt.Errorf("HTTP 400：bad request")
	}
	a.send("again")
	if len(*reqs) != 1 || a.current.Messages[3].Error != "HTTP 400：bad request" {
		t.Errorf("%d requests, error %q", len(*reqs), a.current.Messages[3].Error)
	}
}

func TestSearchFirstAfterTools(t *testing.T) {
	a, reqs, queries := searchingApp(t)
	a.settings.TavilyKey, a.settings.WebSearch = "tvly-test", true
	a.settings.SystemPrompt = "be brief"
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		*reqs = append(*reqs, req)
		if len(req.Tools) > 0 {
			emit(llm.Event{Text: "我先搜一下。"})
			if _, err := run(t, req, "Go 1.27"); err != nil {
				return err
			}
			return fmt.Errorf("HTTP 400：Thought signature is not valid: %w", llm.ErrToolsUnsupported)
		}
		emit(llm.Event{Text: "Go 1.27 [1]"})
		return nil
	}
	a.send("Go release")
	reply := a.current.Messages[1]
	if reply.Content != "我先搜一下。\n\nGo 1.27 [1]" || reply.Error != "" {
		t.Errorf("content %q, error %q", reply.Content, reply.Error)
	}
	if fmt.Sprint(*queries) != "[Go 1.27 Go release]" || len(reply.Searches) != 2 || len(reply.Sources) != 3 {
		t.Errorf("queries %q, searches %+v, sources %d", *queries, reply.Searches, len(reply.Sources))
	}
	last := (*reqs)[1].Messages[0].Content
	if !strings.Contains(last, "\n[1] Go 1.27 is released\n") || !strings.Contains(last, "\n[3] Release History\n") {
		t.Errorf("message %q", last)
	}

	*reqs = nil
	a.webSearch = func(ctx context.Context, key, proxy, query string) ([]websearch.Result, error) {
		return nil, errors.New("Tavily 的搜索额度已用完")
	}
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		*reqs = append(*reqs, req)
		if len(req.Tools) > 0 {
			return fmt.Errorf("HTTP 400：does not support tools: %w", llm.ErrToolsUnsupported)
		}
		emit(llm.Event{Text: "ok"})
		return nil
	}
	a.send("again")
	req := (*reqs)[1]
	if req.System != "be brief" || req.Messages[len(req.Messages)-1].Content != "again" {
		t.Errorf("system %q, message %q", req.System, req.Messages[len(req.Messages)-1].Content)
	}
	if m := a.current.Messages[3]; m.Content != "ok" || len(m.Searches) != 1 || m.Searches[0].Error == "" {
		t.Errorf("reply %+v", m)
	}
}

func TestSearchSettings(t *testing.T) {
	a, _, queries := searchingApp(t)
	tt := ui.NewTester(a.view, 1080, 720)
	a.openSettings()
	a.settingsTab = 1
	tt.Frame()
	if !tt.HasText("联网搜索") {
		t.Fatalf("texts %q", tt.Texts())
	}
	a.settings.TavilyKey = "tvly-test"
	tt.Frame()
	click(t, tt, "测试搜索")
	if fmt.Sprint(*queries) != "[Murmur]" || !tt.HasText("可以搜索，找到 0 个网页") {
		t.Errorf("queries %q, texts %q", *queries, tt.Texts())
	}
	snapshot(t, tt, "search-settings")
	a.settings.TavilyKey = "tvly-other"
	tt.Frame()
	if tt.HasText("可以搜索，找到 0 个网页") {
		t.Error("the result of another key shows")
	}
	a.settings.TavilyKey = "tvly-bad"
	tt.Frame()
	click(t, tt, "测试搜索")
	if !tt.HasText(`搜索失败：key "tvly-bad"`) {
		t.Errorf("texts %q", tt.Texts())
	}
}

func TestSearchPrompt(t *testing.T) {
	now := time.Date(2026, 10, 15, 9, 30, 0, 0, time.FixedZone("PDT", -7*3600))
	got := searchPrompt(" be brief ", now, true)
	if !strings.HasPrefix(got, "be brief\n\nToday is Thursday, 15 October 2026 (UTC-07:00).\n\nYou can search the web") {
		t.Errorf("prompt %q", got)
	}
	got = searchPrompt("", time.Date(2026, 1, 2, 0, 0, 0, 0, time.FixedZone("", 5*3600+1800)), false)
	if !strings.HasPrefix(got, "Today is Friday, 2 January 2026 (UTC+05:30).\n\nThe user's last message comes with results") {
		t.Errorf("prompt %q", got)
	}
}

func TestExportSources(t *testing.T) {
	tr := transcript{messages: []transcriptMessage{
		{role: "user", content: "Go?"},
		{role: "assistant", model: "DeepSeek", content: "Go 1.27 [1]", sources: []Source{
			{Title: "Go [1.27] *released*", URL: "https://go.dev/blog/go1.27?x=1#top"},
			{Title: "Notes", URL: "https://example.com/a b"},
		}},
	}}
	got := tr.markdown(make([][]string, 2), make([][]string, 2))
	want := "# 对话\n\n## 你\n\nGo?\n\n## DeepSeek\n\nGo 1.27 [1]\n\n**参考来源**\n\n1. [Go \\[1.27\\] \\*released\\*](<https://go.dev/blog/go1.27?x=1#top>)\n2. [Notes](<https://example.com/a%20b>)\n"
	if got != want {
		t.Errorf("markdown %q\nwant %q", got, want)
	}
}

func TestShortDate(t *testing.T) {
	for in, want := range map[string]string{
		"2026-08-12T00:00:00Z":          "2026-08-12",
		"2026-08-12T08:00:00":           "2026-08-12",
		"2026-08-12":                    "2026-08-12",
		"Thu, 03 Sep 2026 10:00:00 GMT": "2026-09-03",
		"last week":                     "",
		"":                              "",
	} {
		if got := shortDate(in); got != want {
			t.Errorf("shortDate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNarrowComposer(t *testing.T) {
	for _, tag := range []string{"zh-Hans", "en"} {
		t.Setenv("MYGO_LOCALE", tag)
		a, _, _ := searchingApp(t)
		a.settings.TavilyKey = "tvly-test"
		tt := ui.NewTester(a.view, 640, 560)
		tt.SetLocale(tag)
		tt.Frame()
		for _, label := range []string{locales.ComposerSend(), locales.ComposerSearch(), locales.ComposerThinking()} {
			r, ok := tt.Find(label)
			if !ok || r.X < 248 || r.X+r.W > 640 {
				t.Errorf("%s: %q at %+v, found %v", tag, label, r, ok)
			}
		}
		click(t, tt, locales.ComposerSearch())
		if !a.settings.WebSearch {
			t.Errorf("%s: search off after clicking it", tag)
		}
	}
}
