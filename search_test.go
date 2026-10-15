package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// testChats returns chats about Go and about a trip, the newest first.
func testChats() []*Chat {
	now := time.Now()
	chats := []*Chat{
		{ID: "go", Title: "Go 并发", Updated: now, Messages: []*Message{
			{Role: "user", Content: "怎样等所有 goroutine 结束？"},
			{Role: "assistant", Content: "可以用 **sync.WaitGroup**\n\n每启动一个就 Add(1)。", Reasoning: "也可以提一下 errgroup"},
		}},
		{ID: "trip", Title: "周末去杭州", Updated: now.Add(-time.Hour), Messages: []*Message{
			{Role: "user", Content: "İstanbul 之后去杭州，推荐几个西湖边的景点"},
			{Role: "assistant", Content: "灵隐寺、断桥残雪、苏堤春晓。"},
		}},
	}
	for _, c := range chats {
		for i, m := range c.Messages {
			m.key = i + 1
		}
	}
	return chats
}

func TestSearchChats(t *testing.T) {
	chats := testChats()
	for _, tc := range []struct {
		query   string
		want    string // the IDs found
		snippet string // that of the first found
	}{
		{"杭州", "trip", ""}, // in the title
		{"WAITGROUP", "go", "…sync.WaitGroup 每启动一个就 Add(1)。"}, // in a reply, whatever the case
		{"go", "go", ""},
		{"西湖 断桥", "trip", "…去杭州，推荐几个西湖边的景点"}, // every word, in any message
		{"西湖 waitgroup", "", ""}, // not all in one chat
		{"errgroup", "", ""},     // the thinking is not searched
		{"  ", "", ""},
	} {
		got := searchChats(chats, tc.query)
		var ids []string
		for _, f := range got {
			ids = append(ids, f.chat.ID)
		}
		if strings.Join(ids, " ") != tc.want {
			t.Errorf("%q found %q, want %q", tc.query, ids, tc.want)
			continue
		}
		if len(got) > 0 && got[0].snippet != tc.snippet {
			t.Errorf("%q snippet %q, want %q", tc.query, got[0].snippet, tc.snippet)
		}
	}
	// The match is found in the text as it is, though a letter before it
	// is longer in lower case.
	if f := searchChats(chats, "推荐"); len(f) != 1 || f[0].message != 0 || f[0].snippet != "…l 之后去杭州，推荐几个西湖边的景点" {
		t.Errorf("found %+v", f)
	}
}

func TestSearchSidebar(t *testing.T) {
	a, _ := testApp(t)
	a.chats = testChats()
	for i := range 30 { // a long chat, to scroll to the match
		a.chats[0].Messages = append(a.chats[0].Messages, &Message{Role: "user", Content: strings.Repeat("填充 ", i+1)})
	}
	a.chats[0].Messages = append(a.chats[0].Messages, &Message{Role: "assistant", Content: "最后一条"})
	for i, m := range a.chats[0].Messages {
		m.key = i + 1
	}
	tt := ui.NewTester(a.view, 1080, 720)

	// Cmd+F reaches the search.
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	if !tt.Focused("搜索对话") {
		t.Fatal("the search has not the focus")
	}
	tt.Type("waitgroup")
	tt.Frame()
	if !tt.HasText("找到 1 个对话") || !tt.HasText("Go 并发") || tt.HasText("周末去杭州") {
		t.Fatalf("texts %q", tt.Texts())
	}

	// Opening a chat found shows where it matched.
	if err := tt.Click("Go 并发"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if a.current != a.chats[0] {
		t.Fatalf("current %+v", a.current)
	}
	if first, last := a.chats[0].list.Visible(); first > 1 || last < 1 {
		t.Errorf("rows %d to %d show, not the reply matched", first, last)
	}
	snapshot(t, tt, "search")

	// A search finding nothing says so, and Escape clears it.
	if err := tt.Click("搜索对话"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("没有这个词")
	tt.Frame()
	if !tt.HasText("没有找到相关对话") {
		t.Fatalf("texts %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.search != "" || !tt.HasText("Go 并发") || !tt.HasText("周末去杭州") {
		t.Errorf("search %q, texts %q", a.search, tt.Texts())
	}

	// The results follow the chats as they change.
	tt.Type("杭州")
	tt.Frame()
	if !tt.HasText("周末去杭州") {
		t.Fatalf("texts %q", tt.Texts())
	}
	a.deleteChat("trip")
	tt.Frame()
	if !tt.HasText("没有找到相关对话") {
		t.Errorf("a chat deleted is still found: texts %q", tt.Texts())
	}

	// Cmd+F shows the sidebar hidden.
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	a.collapsed = true
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	if a.collapsed || !tt.Focused("搜索对话") {
		t.Errorf("collapsed %v, search focused %v", a.collapsed, tt.Focused("搜索对话"))
	}
}
