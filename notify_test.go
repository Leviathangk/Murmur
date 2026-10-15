package main

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/llm"
)

func TestNotify(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[2]) // DeepSeek
	var shown []mygo.NotificationOptions
	a.notify = func(n mygo.NotificationOptions) { shown = append(shown, n) }

	a.send("你好")
	want := mygo.NotificationOptions{ID: a.current.ID, Title: "你好", Subtitle: "deepseek-chat", Body: "你好！有什么可以帮你？"}
	if len(shown) != 1 || shown[0] != want {
		t.Fatalf("notifications %+v, want %+v", shown, want)
	}

	// A reply that fails tells why.
	a.settings.Models[0].Model = "broken"
	a.regenerate(a.current)
	if len(shown) != 2 || shown[1].Body != "回复失败：HTTP 401：Incorrect API key provided" {
		t.Fatalf("notifications %+v", shown)
	}

	// One the user stopped tells nothing: they were there.
	canned := a.stream
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		emit(llm.Event{Text: "好"})
		a.stop(a.current)
		return ctx.Err()
	}
	a.regenerate(a.current)
	if len(shown) != 2 {
		t.Fatalf("notified of a stopped reply: %+v", shown[2:])
	}

	// Nor any, turned off.
	a.settings.Notify = false
	a.settings.Models[0].Model = "deepseek-chat"
	a.stream = canned
	a.regenerate(a.current)
	if len(shown) != 2 {
		t.Errorf("notified while turned off: %+v", shown[2:])
	}
}

func TestOpenNotified(t *testing.T) {
	a, _ := testApp(t)
	a.chats = testChats()
	a.openNotified("trip")
	if a.current == nil || a.current.ID != "trip" || a.selected != "trip" {
		t.Fatalf("current %+v", a.current)
	}
	// A chat deleted since leaves a new one.
	a.openNotified("gone")
	if a.current != nil || a.selected != "" {
		t.Errorf("current %+v", a.current)
	}
}

func TestNotifySetting(t *testing.T) {
	a, _ := testApp(t)
	if !a.settings.Notify {
		t.Fatal("notifications off at first")
	}
	tt := ui.NewTester(a.view, 1080, 720)
	a.openSettings()
	tt.Frame()
	if err := tt.Click("通用"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "settings-general")
	if err := tt.Click("回复完成时发送系统通知"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.Notify {
		t.Fatal("notifications still on after a click")
	}
	if b := newApp(a.store); b.settings.Notify {
		t.Error("saved on")
	}
}

func TestPreview(t *testing.T) {
	long := strings.Repeat("长", 120)
	for _, tc := range []struct{ text, want string }{
		{"**你好**！有什么可以帮你？", "你好！有什么可以帮你？"},
		{"## 标题\n\n> 引用的话\n\n用 `go test` 跑", "标题 引用的话 用 go test 跑"},
		{"  \n", ""},
		{long, strings.Repeat("长", 100) + "…"},
	} {
		if got := preview(tc.text); got != tc.want {
			t.Errorf("preview(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}
