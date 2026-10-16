package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// A chat goes on from a reply in a new chat, of the messages up to it:
// the chat it came from stays as it was.
func TestFork(t *testing.T) {
	a, reqs := testApp(t)
	a.addModel(presets[0])
	tt := ui.NewTester(a.view, 1080, 720)
	a.send("第一个问题")
	a.send("第二个问题")
	tt.Frame()
	from := a.current
	if len(from.Messages) != 4 {
		t.Fatalf("%d messages", len(from.Messages))
	}

	// Each reply has the button, the first reply's first.
	n := 0
	for _, s := range tt.Texts() {
		if s == "从这里开新对话" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d fork buttons for 2 replies", n)
	}
	// What it does shows when the pointer rests on it.
	r, _ := tt.Find("从这里开新对话")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	time.Sleep(700 * time.Millisecond)
	tt.Frame()
	n = 0
	for _, s := range tt.Texts() {
		if s == "从这里开新对话" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("no tooltip in %q", tt.Texts())
	}
	snapshot(t, tt, "fork-button")
	if err := tt.Click("从这里开新对话"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	f := a.current
	if f == from || len(a.chats) != 2 || a.chats[0] != f || a.selected != f.ID {
		t.Fatalf("chats %+v, current %+v", a.chats, f)
	}
	if f.Title != "分支 · 第一个问题" || !tt.HasText("分支 · 第一个问题") {
		t.Errorf("title %q, texts %q", f.Title, tt.Texts())
	}
	if len(f.Messages) != 2 || f.Messages[0].Content != "第一个问题" || f.Messages[1].Content != from.Messages[1].Content ||
		f.Messages[1].Reasoning != from.Messages[1].Reasoning || f.Messages[1].Model != from.Messages[1].Model {
		t.Fatalf("messages %+v", f.Messages)
	}
	for i, m := range f.Messages {
		if m == from.Messages[i] || m.key == from.Messages[i].key || !m.Time.Equal(from.Messages[i].Time) {
			t.Errorf("message %d %+v, from %+v", i, m, from.Messages[i])
		}
	}
	if len(from.Messages) != 4 || tt.HasText("第二个问题") {
		t.Errorf("%d messages in the chat forked from, texts %q", len(from.Messages), tt.Texts())
	}
	if !tt.Focused("消息") {
		t.Error("the composer has not the focus")
	}
	snapshot(t, tt, "fork")

	// What is said next goes on from the reply.
	tt.Type("换个问题")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	req := (*reqs)[len(*reqs)-1]
	var sent []string
	for _, m := range req.Messages {
		sent = append(sent, m.Content)
	}
	if strings.Join(sent, "|") != "第一个问题|"+from.Messages[1].Content+"|换个问题" {
		t.Errorf("sent %q", sent)
	}
	if len(from.Messages) != 4 || len(f.Messages) != 4 || a.chats[0] != f {
		t.Errorf("%d and %d messages", len(from.Messages), len(f.Messages))
	}

	// A chat forked from a fork is not named a branch twice.
	a.fork(f, 3)
	if a.current.Title != "分支 · 第一个问题" || len(a.current.Messages) != 4 {
		t.Errorf("title %q, %d messages", a.current.Title, len(a.current.Messages))
	}

	b := newApp(a.store)
	if len(b.chats) != 3 || b.chat(f.ID) == nil || len(b.chat(f.ID).Messages) != 4 || len(b.chat(from.ID).Messages) != 4 {
		t.Errorf("loaded chats %+v", b.chats)
	}
}

// Replies that failed, or still come, have no button, nor have the user's
// messages.
func TestForkable(t *testing.T) {
	for _, tc := range []struct {
		m    Message
		want bool
	}{
		{Message{Role: "assistant", Content: "好的"}, true},
		{Message{Role: "user", Content: "你好"}, false},
		{Message{Role: "assistant", Content: "好", streaming: true}, false},
		{Message{Role: "assistant", Content: "好", Error: "HTTP 500"}, false},
		{Message{Role: "assistant", Reasoning: "想了想"}, false},
	} {
		if got := forkable(&tc.m); got != tc.want {
			t.Errorf("%+v: %v", tc.m, got)
		}
	}
	a, _ := testApp(t)
	a.chats = testChats()
	a.fork(a.chats[0], 0)
	a.fork(a.chats[0], 5)
	if len(a.chats) != 2 {
		t.Errorf("%d chats", len(a.chats))
	}
}

// The forks share the files of the images and documents: those of a chat
// deleted stay while another has them.
func TestForkFiles(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[0])
	_, notes, _, _ := documents(t, t.TempDir())
	a.attachData(pngOf(t, picture(64, 64, 255)))
	a.attachFiles([]string{notes})
	a.send("看看")
	from := a.current
	img, doc := from.Messages[0].Images[0], from.Messages[0].Documents[0]
	a.fork(from, 1)
	f := a.current
	exists := func() (bool, bool) {
		_, err1 := os.Stat(a.store.imagePath(img.File))
		_, err2 := os.Stat(a.store.documentPath(doc.File))
		return err1 == nil, err2 == nil
	}

	a.deleteChat(from.ID)
	if i, d := exists(); !i || !d {
		t.Fatalf("image %v, document %v after deleting the chat forked from", i, d)
	}
	if m := f.Messages[0]; len(m.Images) != 1 || m.Images[0].File != img.File || len(m.Documents) != 1 || m.Documents[0].File != doc.File {
		t.Fatalf("message %+v", m)
	}
	a.deleteChat(f.ID)
	if i, d := exists(); i || d {
		t.Errorf("image %v, document %v after deleting both", i, d)
	}
}
