package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	a.webSearch = func(ctx context.Context, w web, query string) ([]websearch.Result, error) {
		if w.service != tinyFish || w.key != "sk-tinyfish-test" {
			return nil, fmt.Errorf("key %q", w.key)
		}
		queries = append(queries, query)
		return goResults[query], nil
	}
	return a, reqs, &queries
}

func tool(t *testing.T, req llm.Request, name string, args map[string]string) (string, error) {
	t.Helper()
	if len(req.Tools) != 2 || req.Tools[0].Name != "web_search" || req.Tools[1].Name != "read_page" {
		t.Fatalf("tools %+v", req.Tools)
	}
	input, _ := json.Marshal(args)
	for _, tool := range req.Tools {
		if tool.Name == name {
			return tool.Run(context.Background(), input)
		}
	}
	t.Fatalf("no tool %q", name)
	return "", nil
}

func run(t *testing.T, req llm.Request, query string) (string, error) {
	t.Helper()
	return tool(t, req, "web_search", map[string]string{"query": query})
}

func read(t *testing.T, req llm.Request, link string) (string, error) {
	t.Helper()
	return tool(t, req, "read_page", map[string]string{"url": link})
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

	a.settings.TinyFishKey = "sk-tinyfish-test"
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
	if !tt.HasText("已搜索「Go 1.27」· 2 个网页") || !tt.HasText("已搜索「Go release」· 2 个网页") || tt.HasText("Release History") {
		t.Fatalf("texts %q", tt.Texts())
	}
	click(t, tt, "已搜索「Go 1.27」· 2 个网页")
	click(t, tt, "已搜索「Go release」· 2 个网页")
	for _, s := range []string{"[2]", "Go 1.27 Release Notes", "[3]", "Release History", "go.dev · 2026-08-12", "go.dev · 2026-09-03", "go.dev"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	time.Sleep(400 * time.Millisecond)
	tt.Frame()
	snapshot(t, tt, "search-sources")

	b := newApp(a.store)
	if !b.settings.WebSearch || b.settings.TinyFishKey != "sk-tinyfish-test" {
		t.Errorf("loaded settings %+v", b.settings)
	}
	if m := b.chats[0].Messages[1]; len(m.Searches) != 2 || len(m.Sources) != 3 || m.Sources[0].Published != "2026-08-12T00:00:00Z" || fmt.Sprint(m.Searches[1].Sources) != "[1 3]" || len(m.Steps) != 4 {
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
	if a.settings.WebSearch || !a.settingsOpen || a.settingsTab != 1 || !tt.HasText("先在设置里填写 TinyFish API Key，才能联网搜索") {
		t.Fatalf("search on %v, settings open %v on tab %d, texts %q", a.settings.WebSearch, a.settingsOpen, a.settingsTab, tt.Texts())
	}
	a.settings.WebSearch = true
	if a.searchOn() {
		t.Error("search on without a key")
	}
}

func TestSearching(t *testing.T) {
	a, _, _ := searchingApp(t)
	a.settings.TinyFishKey, a.settings.WebSearch = "sk-tinyfish-test", true
	tt := ui.NewTester(a.view, 1080, 720)
	shown := false
	a.webSearch = func(ctx context.Context, w web, query string) ([]websearch.Result, error) {
		tt.Frame()
		shown = tt.HasText("正在搜索：Go 1.27") && !tt.HasText("正在等待回复…")
		snapshot(t, tt, "searching")
		return nil, errors.New("TinyFish API Key 无效，请在设置里检查")
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
	if fmt.Sprint(told) != `[Search failed: TinyFish API Key 无效，请在设置里检查 Give what to search for, as {"query": "…"}]` {
		t.Errorf("told %q", told)
	}
	reply := a.current.Messages[1]
	if len(reply.Searches) != 1 || reply.Searches[0].Error == "" || len(reply.Sources) != 0 || reply.Error != "" {
		t.Fatalf("reply %+v", reply)
	}
	click(t, tt, "搜索「Go 1.27」失败")
	if !tt.HasText("TinyFish API Key 无效，请在设置里检查") || tt.HasText("没有结果") {
		t.Errorf("texts %q", tt.Texts())
	}
	time.Sleep(400 * time.Millisecond)
	tt.Frame()
	snapshot(t, tt, "search-failed")
}

func TestSearchSteps(t *testing.T) {
	a, _, _ := searchingApp(t)
	a.settings.TinyFishKey, a.settings.WebSearch = "sk-tinyfish-test", true
	tt := ui.NewTester(a.view, 1080, 720)
	below := func(texts ...string) {
		t.Helper()
		tt.Frame()
		y := float32(-1)
		for _, s := range texts {
			r, ok := tt.Find(s)
			if !ok {
				t.Errorf("no %q in %q", s, tt.Texts())
				return
			}
			if r.Y <= y {
				t.Errorf("%q at y %.1f, not below the one before at y %.1f", s, r.Y, y)
			}
			y = r.Y
		}
	}
	thinking := func() int {
		n := 0
		for _, s := range tt.Texts() {
			if strings.HasPrefix(s, "思考中…") {
				n++
			}
		}
		return n
	}
	a.webSearch = func(ctx context.Context, w web, query string) ([]websearch.Result, error) {
		if query == "Go 1.27" {
			below("我先查一下。", "正在搜索：Go 1.27")
			if tt.HasText("正在等待回复…") || thinking() != 0 {
				t.Errorf("thinking or waiting while searching: %q", tt.Texts())
			}
			snapshot(t, tt, "steps-searching")
		}
		return goResults[query], nil
	}
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		emit(llm.Event{Reasoning: "要查一下。"})
		below("要查一下。")
		if thinking() != 1 {
			t.Errorf("no live thinking label in %q", tt.Texts())
		}
		emit(llm.Event{Text: "我先查一下。"})
		for _, q := range []string{"Go 1.27", "Go release"} {
			if _, err := run(t, req, q); err != nil {
				return err
			}
		}
		below("我先查一下。", "已搜索「Go 1.27」· 2 个网页", "已搜索「Go release」· 2 个网页", "正在等待回复…")
		snapshot(t, tt, "steps-waiting")
		emit(llm.Event{Reasoning: "\n\n结果说是 1.27。"})
		below("已搜索「Go release」· 2 个网页", "结果说是 1.27。")
		if thinking() != 1 || tt.HasText("正在等待回复…") {
			t.Errorf("thinking again: %q", tt.Texts())
		}
		snapshot(t, tt, "steps-thinking")
		emit(llm.Event{Text: "\n\nGo 1.27 [1]，最新是 1.27.1 [3]。"})
		return nil
	}
	a.send("Go 最新版本？")
	tt.Frame()
	time.Sleep(400 * time.Millisecond)
	below("我先查一下。", "已搜索「Go 1.27」· 2 个网页", "已搜索「Go release」· 2 个网页", "Go 1.27 [1]，最新是 1.27.1 [3]。")
	reply := a.current.Messages[1]
	var kinds []stepKind
	var ms int64
	for _, s := range reply.Steps {
		kinds = append(kinds, s.Kind)
		ms += s.ThinkMS
	}
	if fmt.Sprint(kinds) != "[think text search search think text]" || reply.Steps[0].ThinkMS == 0 || reply.Steps[4].ThinkMS == 0 || ms != reply.ThinkMS {
		t.Errorf("steps %+v, think %d ms", reply.Steps, reply.ThinkMS)
	}
	if reply.writing || !reply.thinkStart.IsZero() {
		t.Errorf("writing %v, thinking since %v", reply.writing, reply.thinkStart)
	}
	labels := 0
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "已思考") {
			labels++
		}
	}
	if labels != 2 || thinking() != 0 || tt.HasText("要查一下。") || tt.HasText("结果说是 1.27。") || tt.HasText("已搜索 2 次 · 3 个网页") {
		t.Errorf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "steps-done")

	f := a.current
	a.fork(f, 1)
	if m := a.current.Messages[1]; len(m.Steps) != 6 || m.Steps[0] == reply.Steps[0] || *m.Steps[5] != *reply.Steps[5] {
		t.Errorf("forked steps %+v", m.Steps)
	}
}

func TestSearchesWithoutSteps(t *testing.T) {
	a, _ := testApp(t)
	a.current = &Chat{ID: newID(), Title: "Go", Messages: []*Message{
		{Role: "user", Content: "Go 最新版本？", key: a.newKey()},
		{Role: "assistant", Content: "Go 1.27 [1]。", Reasoning: "要查一下。\n\n结果说是 1.27。", ThinkMS: 3000, key: a.newKey(),
			Searches: []*Search{{Query: "Go 1.27"}}, Sources: []*Source{{Title: "Go 1.27 is released", URL: "https://go.dev/blog/go1.27"}}},
	}}
	a.chats = []*Chat{a.current}
	tt := ui.NewTester(a.view, 1080, 720)
	for _, s := range []string{"已思考 3 秒", "已搜索 1 次 · 1 个网页", "Go 1.27 [1]。"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
}

func TestSearchLimit(t *testing.T) {
	a, _, queries := searchingApp(t)
	a.settings.TinyFishKey, a.settings.WebSearch = "sk-tinyfish-test", true
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
	click(t, tt, "已搜索「q4」· 0 个网页")
	if !tt.HasText("没有结果") || !tt.HasText("已搜索「q0」· 0 个网页") {
		t.Errorf("texts %q", tt.Texts())
	}

	a.settings.TinyFishKey = " "
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
	a.settings.TinyFishKey, a.settings.WebSearch = "sk-tinyfish-test", true
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
	a.settings.TinyFishKey, a.settings.WebSearch = "sk-tinyfish-test", true
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
	a.webSearch = func(ctx context.Context, w web, query string) ([]websearch.Result, error) {
		return nil, errors.New("今天的 TinyFish 免费次数已用完，每天 UTC 0 点恢复")
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
	a.settings.TinyFishKey = "sk-tinyfish-test"
	tt.Frame()
	click(t, tt, "测试搜索")
	if fmt.Sprint(*queries) != "[Murmur]" || !tt.HasText("可以搜索，找到 0 个网页") {
		t.Errorf("queries %q, texts %q", *queries, tt.Texts())
	}
	snapshot(t, tt, "search-settings")
	a.settings.TinyFishKey = "sk-tinyfish-other"
	tt.Frame()
	if tt.HasText("可以搜索，找到 0 个网页") {
		t.Error("the result of another key shows")
	}
	a.settings.TinyFishKey = "sk-tinyfish-bad"
	tt.Frame()
	click(t, tt, "测试搜索")
	if !tt.HasText(`搜索失败：key "sk-tinyfish-bad"`) {
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
		a.settings.TinyFishKey = "sk-tinyfish-test"
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

var goPages = map[string]*websearch.Page{
	"https://go.dev/blog/go1.27": {URL: "https://go.dev/blog/go1.27", Title: "Go 1.27 is released - The Go Programming Language", Text: "# Go 1.27 is released\n\nToday the Go team…", Published: "2026-08-12T00:00:00+08:00"},
	"https://example.com/notes":  {URL: "https://example.com/notes", Text: "Notes on Go 1.27"},
}

func TestReadPage(t *testing.T) {
	a, reqs, _ := searchingApp(t)
	a.settings.TinyFishKey, a.settings.WebSearch = "sk-tinyfish-test", true
	tt := ui.NewTester(a.view, 1080, 720)
	var links []string
	a.readPage = func(ctx context.Context, w web, link string) (*websearch.Page, error) {
		if w.service != tinyFish || w.key != "sk-tinyfish-test" {
			return nil, fmt.Errorf("web %+v", w)
		}
		links = append(links, link)
		if link == "https://go.dev/blog/go1.27" {
			tt.Frame()
			if !tt.HasText("正在读取：go.dev/blog/go1.27") || tt.HasText("正在等待回复…") {
				t.Errorf("texts while reading %q", tt.Texts())
			}
			snapshot(t, tt, "reading")
		}
		if p, ok := goPages[link]; ok {
			return p, nil
		}
		return nil, errors.New("网页不存在")
	}
	var pages []string
	var told error
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		*reqs = append(*reqs, req)
		if _, err := run(t, req, "Go 1.27"); err != nil {
			return err
		}
		for _, link := range []string{"https://go.dev/blog/go1.27", " https://example.com/notes "} {
			out, err := read(t, req, link)
			if err != nil {
				return err
			}
			pages = append(pages, out)
		}
		_, told = read(t, req, "https://example.com/gone")
		emit(llm.Event{Text: "Go 1.27 已发布 [1][3]。"})
		return nil
	}
	a.send("Go 1.27 有什么新东西？我的笔记在 example.com/notes，还有 https://example.com/gone")
	tt.Frame()
	if system := (*reqs)[0].System; !strings.Contains(system, "read_page tool") || !strings.Contains(system, "up to 5 for one answer") {
		t.Errorf("system %q", system)
	}
	if fmt.Sprint(links) != "[https://go.dev/blog/go1.27 https://example.com/notes https://example.com/gone]" {
		t.Errorf("read %q", links)
	}
	want := []string{
		"[1] Go 1.27 is released - The Go Programming Language\nURL: https://go.dev/blog/go1.27\nPublished: 2026-08-12T00:00:00+08:00\n\n# Go 1.27 is released\n\nToday the Go team…",
		"[3] example.com/notes\nURL: https://example.com/notes\n\nNotes on Go 1.27",
	}
	if fmt.Sprint(pages) != fmt.Sprint(want) {
		t.Errorf("pages %q\nwant %q", pages, want)
	}
	if told == nil || told.Error() != "Reading the page failed: 网页不存在" {
		t.Errorf("told %v", told)
	}

	reply := a.current.Messages[1]
	var kinds []stepKind
	for _, s := range reply.Steps {
		kinds = append(kinds, s.Kind)
	}
	if fmt.Sprint(kinds) != "[search read read read text]" {
		t.Errorf("steps %v", kinds)
	}
	if len(reply.Reads) != 3 || reply.Reads[0].Source != 1 || reply.Reads[1].Source != 3 || reply.Reads[2].Source != 0 || reply.Reads[2].Error != "网页不存在" {
		t.Errorf("reads %+v", reply.Reads)
	}
	if len(reply.Sources) != 3 || reply.Sources[0].Title != "Go 1.27 is released" || reply.Sources[2].Title != "example.com/notes" || reply.reading != "" {
		t.Errorf("sources %+v, reading %q", reply.Sources, reply.reading)
	}
	for _, s := range []string{"已搜索「Go 1.27」· 2 个网页", "已读取「Go 1.27 is released」", "已读取「example.com/notes」", "读取「example.com/gone」失败"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	click(t, tt, "读取「example.com/gone」失败")
	click(t, tt, "已读取「Go 1.27 is released」")
	for _, s := range []string{"网页不存在", "https://example.com/gone", "[1]", "go.dev · 2026-08-12"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	time.Sleep(400 * time.Millisecond)
	tt.Frame()
	snapshot(t, tt, "read-pages")

	b := newApp(a.store)
	if m := b.chats[0].Messages[1]; len(m.Reads) != 3 || m.Reads[1].Source != 3 || m.Reads[2].Error == "" || len(m.Sources) != 3 {
		t.Errorf("loaded reply %+v", m)
	}
	a.fork(a.current, 1)
	if m := a.current.Messages[1]; len(m.Reads) != 3 || m.Reads[0] == reply.Reads[0] || m.Reads[2].open || m.Reads[2].Error != reply.Reads[2].Error || m.Reads[1].Source != 3 {
		t.Errorf("forked reads %+v", m.Reads)
	}
}

func TestReadLimit(t *testing.T) {
	a, _, _ := searchingApp(t)
	a.settings.TinyFishKey, a.settings.WebSearch = "sk-tinyfish-test", true
	n := 0
	a.readPage = func(ctx context.Context, w web, link string) (*websearch.Page, error) {
		n++
		return &websearch.Page{URL: link, Title: "Page", Text: "text"}, nil
	}
	var told []string
	var last error
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		for _, link := range []string{"go.dev/blog", "ftp://go.dev/x", "  ", "https://"} {
			_, err := read(t, req, link)
			told = append(told, err.Error())
		}
		_, err := tool(t, req, "read_page", map[string]string{"query": "https://go.dev/"})
		told = append(told, err.Error())
		for i := 0; i <= maxReads; i++ {
			_, last = read(t, req, fmt.Sprintf("https://example.com/%d", i))
		}
		emit(llm.Event{Text: "ok"})
		return nil
	}
	a.send("https://example.com/0 https://example.com/1 https://example.com/2 https://example.com/3 https://example.com/4 https://example.com/5")
	for _, s := range told {
		if s != `Give the full URL of the page, as {"url": "https://…"}` {
			t.Errorf("told %q", s)
		}
	}
	if n != maxReads || last == nil || !strings.Contains(last.Error(), "No more pages") {
		t.Errorf("%d pages read, last error %v", n, last)
	}
	if m := a.current.Messages[1]; len(m.Reads) != maxReads || len(m.Steps) != maxReads+1 || len(m.Sources) != maxReads {
		t.Errorf("reads %d, steps %d, sources %d", len(m.Reads), len(m.Steps), len(m.Sources))
	}
}

func TestReadStopped(t *testing.T) {
	a, _, _ := searchingApp(t)
	a.settings.TinyFishKey, a.settings.WebSearch = "sk-tinyfish-test", true
	a.readPage = func(ctx context.Context, w web, link string) (*websearch.Page, error) {
		a.stop(a.current)
		return nil, ctx.Err()
	}
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		emit(llm.Event{Text: "我看看。"})
		_, err := req.Tools[1].Run(ctx, json.RawMessage(`{"url":"https://go.dev/blog/go1.27"}`))
		return err
	}
	a.send("https://go.dev/blog/go1.27 讲了什么？")
	reply := a.current.Messages[1]
	if len(reply.Steps) != 1 || reply.Steps[0].Kind != stepText || len(reply.Reads) != 0 || reply.reading != "" || reply.Error != "" {
		t.Errorf("reply %+v, steps %+v", reply, reply.Steps)
	}
}

func TestFormatPage(t *testing.T) {
	long := strings.Repeat("字", maxPageRunes+5)
	got := formatPage(2, &websearch.Page{URL: "https://www.example.com/a%20b/", Text: long})
	want := "[2] example.com/a b\nURL: https://www.example.com/a%20b/\n\n" + strings.Repeat("字", maxPageRunes) + "\n\n[The page goes on: only its first 10000 characters are here.]"
	if got != want {
		t.Errorf("page %q", got[:80])
	}
	if got := shortURL("https://example.com/" + strings.Repeat("a", 80) + "?q=1"); got != "example.com/"+strings.Repeat("a", 47)+"…" {
		t.Errorf("short URL %q", got)
	}
}

func TestSearchServices(t *testing.T) {
	a, _ := testApp(t)
	if a.settings.SearchService != tinyFish {
		t.Errorf("new settings search with %q", a.settings.SearchService)
	}
	if err := os.WriteFile(filepath.Join(a.store.dir, "settings.json"), []byte(`{"web_search":true,"tavily_api_key":"tvly-old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.settings = a.store.loadSettings()
	if a.settings.SearchService != tavily || !a.searchOn() {
		t.Fatalf("old settings search with %q, on %v", a.settings.SearchService, a.searchOn())
	}
	a.addModel(presets[2])
	var used []web
	a.webSearch = func(ctx context.Context, w web, query string) ([]websearch.Result, error) {
		used = append(used, w)
		return nil, nil
	}
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		if _, err := run(t, req, "q"); err != nil {
			return err
		}
		emit(llm.Event{Text: "ok"})
		return nil
	}
	a.send("hi")

	tt := ui.NewTester(a.view, 1080, 720)
	a.openSettings()
	a.settingsTab = 1
	tt.Frame()
	if !tt.HasText(locales.TavilyHint()) {
		t.Errorf("no Tavily hint in %q", tt.Texts())
	}
	click(t, tt, "测试搜索")
	if !tt.HasText("可以搜索，找到 0 个网页") {
		t.Errorf("texts %q", tt.Texts())
	}
	click(t, tt, "TinyFish（免费）")
	if a.settings.SearchService != tinyFish || !tt.HasText(locales.TinyFishHint()) || tt.HasText("可以搜索，找到 0 个网页") || a.searchOn() {
		t.Errorf("service %q, on %v, texts %q", a.settings.SearchService, a.searchOn(), tt.Texts())
	}
	a.settings.TinyFishKey = " sk-tinyfish-new "
	tt.Frame()
	click(t, tt, "测试搜索")
	snapshot(t, tt, "search-services")
	a.settingsOpen = false
	a.send("again")
	want := []web{{tavily, "tvly-old", ""}, {tavily, "tvly-old", ""}, {tinyFish, "sk-tinyfish-new", ""}, {tinyFish, "sk-tinyfish-new", ""}}
	if fmt.Sprint(used) != fmt.Sprint(want) {
		t.Errorf("searched with %+v\nwant %+v", used, want)
	}
	a.saveSettings()
	if st := a.store.loadSettings(); st.SearchService != tinyFish || st.TavilyKey != "tvly-old" {
		t.Errorf("saved settings %+v", st)
	}

	a.settings.SearchService, a.settings.TavilyKey, a.settings.WebSearch = tavily, "", false
	a.setSearch(true)
	if a.settings.WebSearch || a.toast != "先在设置里填写 Tavily API Key，才能联网搜索" {
		t.Errorf("search on %v, toast %q", a.settings.WebSearch, a.toast)
	}
}

func TestReadOnlyKnownPages(t *testing.T) {
	a, _, _ := searchingApp(t)
	a.settings.TinyFishKey, a.settings.WebSearch = "sk-tinyfish-test", true
	var links []string
	a.readPage = func(ctx context.Context, w web, link string) (*websearch.Page, error) {
		links = append(links, link)
		return &websearch.Page{URL: link, Title: "Page", Text: "text"}, nil
	}
	got := map[string]bool{}
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		if _, err := run(t, req, "Go 1.27"); err != nil {
			return err
		}
		for _, link := range []string{
			"https://go.dev/doc/go1.27",
			"https://go.dev/doc/go1.27?secret=1",
			"https://evil.example/?q=secret",
			"https://zh.wikipedia.org/wiki/围棋",
			"https://notes.example/a",
			"https://www.go.dev/doc/devel/release",
			"https://go.dev/doc/go1.27#SECRET",
			"https://SECRET@go.dev/doc/go1.27",
			"https://hub.com/Qsnh/Murmur",
			"https://github.co",
			"https://github.com/Qsnh/Murmur/issues",
			"https://github.com/Qsnh",
			"https://blog.example/post?utm_source=x",
			"https://go.dev/x",
		} {
			_, err := read(t, req, link)
			got[link] = err == nil || !strings.HasPrefix(err.Error(), "Only the pages")
		}
		emit(llm.Event{Text: "ok"})
		return nil
	}
	a.current = &Chat{ID: newID(), Title: "Go", Messages: []*Message{
		{Role: "user", Content: "Go?", key: a.newKey()},
		{Role: "assistant", Content: "Go [1]", key: a.newKey(), Sources: []*Source{{Title: "Release History", URL: "https://go.dev/doc/devel/release/"}}},
	}}
	a.chats = []*Chat{a.current}
	a.send("涨了 50% 吗？看看 https://zh.wikipedia.org/wiki/%E5%9B%B4%E6%A3%8B、NOTES.example/a/，github.com/Qsnh/Murmur 和 https://blog.example/post/?utm_source=x。还有看看https://go.dev/x讲了什么")
	want := map[string]bool{
		"https://go.dev/doc/go1.27":              true,
		"https://go.dev/doc/go1.27?secret=1":     false,
		"https://evil.example/?q=secret":         false,
		"https://zh.wikipedia.org/wiki/围棋":       true,
		"https://notes.example/a":                true,
		"https://www.go.dev/doc/devel/release":   true,
		"https://go.dev/doc/go1.27#SECRET":       true,
		"https://SECRET@go.dev/doc/go1.27":       false,
		"https://hub.com/Qsnh/Murmur":            false,
		"https://github.co":                      false,
		"https://github.com/Qsnh/Murmur/issues":  false,
		"https://github.com/Qsnh":                true,
		"https://blog.example/post?utm_source=x": true,
		"https://go.dev/x":                       true,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("read %v\nwant %v", got, want)
	}
	reply := a.current.Messages[3]
	if len(links) != maxReads || links[4] != "https://go.dev/doc/go1.27" || len(reply.Reads) != maxReads || reply.Reads[0].Source != 2 || reply.Reads[4].Source != 2 || len(reply.Sources) != 5 {
		t.Errorf("read %q, reads %+v, %d sources", links, reply.Reads, len(reply.Sources))
	}
}
