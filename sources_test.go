package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/llm"
)

func TestSourcesPanel(t *testing.T) {
	a, _, _ := searchingApp(t)
	a.settings.TinyFishKey, a.settings.WebSearch = "sk-tinyfish-test", true
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		if len(req.Tools) == 0 {
			emit(llm.Event{Text: "好的"})
			return nil
		}
		for _, q := range []string{"Go 1.27", "Go release"} {
			if _, err := run(t, req, q); err != nil {
				return err
			}
		}
		emit(llm.Event{Text: "Go 1.27 已发布 [1]，最新是 1.27.1 [3]。"})
		return nil
	}
	tt := ui.NewTester(a.view, 1080, 720)
	settle := func() {
		time.Sleep(300 * time.Millisecond)
		tt.Frame()
	}
	a.send("Go 最新版本是多少？")
	tt.Frame()

	reply := a.current.Messages[1]
	if len(reply.Sources) != 3 || reply.Sources[0].Snippet != "Go 1.27 brings…" || reply.Sources[2].Snippet != "go1.27.1 (released 2026-09-03)" {
		t.Fatalf("sources %+v", reply.Sources)
	}
	if !tt.HasText("3 个网页") || tt.HasText("搜索结果") {
		t.Fatalf("texts %q", tt.Texts())
	}

	click(t, tt, "3 个网页")
	settle()
	for _, s := range []string{"搜索结果", "Go 1.27 is released", "Go 1.27 brings…", "Release History", "go1.27.1 (released 2026-09-03)", "2026-08-12", "2026-09-03", "go.dev"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	if r, ok := tt.Find("Release History"); !ok || r.X < 1080-sourcesWidth {
		t.Errorf("Release History at %+v, not in the panel", r)
	}
	if r, ok := tt.Find("Go 1.27 已发布 [1]，最新是 1.27.1 [3]。"); !ok || r.X+r.W > 1080-sourcesWidth {
		t.Errorf("the reply at %+v, under the panel", r)
	}
	snapshot(t, tt, "sources-panel")

	click(t, tt, "Release History")
	if fmt.Sprint(tt.OpenedURLs()) != "[https://www.go.dev/doc/devel/release]" {
		t.Errorf("opened %q", tt.OpenedURLs())
	}

	click(t, tt, "关闭")
	settle()
	if a.sourcesOpen || tt.HasText("搜索结果") {
		t.Fatalf("still open: %q", tt.Texts())
	}

	click(t, tt, "3 个网页")
	settle()
	click(t, tt, "3 个网页")
	settle()
	if a.sourcesOpen {
		t.Error("a second click did not close the panel")
	}

	click(t, tt, "3 个网页")
	settle()
	id := a.current.ID
	a.newChat()
	tt.Frame()
	settle()
	if a.sourcesOpen || tt.HasText("搜索结果") {
		t.Errorf("open in a new chat: %q", tt.Texts())
	}
	a.open(id)
	tt.Frame()
	settle()
	if a.sourcesOpen {
		t.Error("open again on coming back")
	}

	click(t, tt, "3 个网页")
	settle()
	a.regenerate(a.current)
	tt.Frame()
	if a.sourcesOpen || !tt.HasText("3 个网页") {
		t.Errorf("open %v after regenerating: %q", a.sourcesOpen, tt.Texts())
	}
	settle()

	tt.SetSize(640, 600)
	click(t, tt, "3 个网页")
	settle()
	if r, ok := tt.Find("Go 1.27 已发布 [1]，最新是 1.27.1 [3]。"); !ok || r.X+r.W < 640-sourcesWidth {
		t.Errorf("the reply at %+v, squeezed beside the panel", r)
	}
	if r, ok := tt.Find("Release History"); !ok || r.X < 640-sourcesWidth {
		t.Errorf("Release History at %+v, not in the panel", r)
	}
	snapshot(t, tt, "sources-floating")

	b := newApp(a.store)
	if m := b.chats[0].Messages[1]; len(m.Sources) != 3 || m.Sources[2].Snippet != "go1.27.1 (released 2026-09-03)" {
		t.Errorf("loaded sources %+v", m.Sources)
	}
}

func TestExcerpt(t *testing.T) {
	if s := excerpt("  Go 1.27\n\n  is   released "); s != "Go 1.27 is released" {
		t.Errorf("excerpt %q", s)
	}
	s := excerpt(strings.Repeat("字", 500))
	if utf8.RuneCountInString(s) != maxSnippet || !strings.HasSuffix(s, "…") {
		t.Errorf("long excerpt of %d runes: %q", utf8.RuneCountInString(s), s)
	}
}

func TestSiteIcons(t *testing.T) {
	sources := []*Source{
		{URL: "https://go.dev/blog/go1.27"},
		{URL: "https://www.go.dev/doc/devel/release"},
		{URL: "https://pkg.go.dev/golang.org/dl"},
		{URL: "https://mirrors.cas.cn/go"},
		{URL: "https://github.com/golang/go"},
		{URL: "https://zh.wikipedia.org/wiki/Go"},
	}
	if s := fmt.Sprint(sitesOf(sources, 4)); s != "[go.dev pkg.go.dev mirrors.cas.cn github.com]" {
		t.Errorf("sites %s", s)
	}
}
