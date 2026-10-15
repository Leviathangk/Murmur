package main

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/Qsnh/Murmur/llm"
)

// app is the state the window shows.
type app struct {
	win      *mygo.Window
	store    store
	settings *Settings
	chats    []*Chat // the newest first
	current  *Chat   // the chat shown; nil for a new one, not yet sent
	selected string  // the ID of the chat chosen in the sidebar
	draft    string  // what is typed in the composer
	nextKey  int

	collapsed bool // the sidebar is hidden
	md        markdown.Options

	settingsOpen bool
	settingsTab  int
	editing      string // the ID of the model edited in the settings
	testing      string // the ID of the model being tested
	testResult   string // what the last test found, for the model edited
	testOK       bool
	deleting     string // the ID of the model whose deletion awaits confirmation
	toast        string // a message to show in the next frame

	savedSettings string // the settings last written, as JSON

	// stream calls the model; tests replace it.
	stream func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error
}

func newApp(s store) *app {
	a := &app{store: s, stream: llm.Stream}
	a.settings = s.loadSettings()
	a.chats = s.loadChats()
	for _, c := range a.chats {
		for _, m := range c.Messages {
			m.key = a.newKey()
		}
	}
	return a
}

func (a *app) newKey() int {
	a.nextKey++
	return a.nextKey
}

// model returns the model chats use, or nil when there is none.
func (a *app) model() *Model {
	if m := a.settings.model(a.settings.Current); m != nil {
		return m
	}
	if len(a.settings.Models) > 0 {
		a.settings.Current = a.settings.Models[0].ID
		return a.settings.Models[0]
	}
	return nil
}

// update runs fn on the main thread, as the window's state may only change
// there; without a window, as in tests, it runs it at once.
func (a *app) update(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	a.win.Update(fn)
}

// spawn runs fn on a goroutine of its own, or at once without a window.
func (a *app) spawn(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	go fn()
}

// send sends text in the current chat, starting one if needed.
func (a *app) send(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	m := a.model()
	if m == nil {
		a.toast = "请先添加一个模型"
		a.openSettings()
		return
	}
	c := a.current
	if c != nil && c.busy() {
		return
	}
	if c == nil {
		c = &Chat{ID: newID(), Title: titleOf(text)}
		a.chats = append([]*Chat{c}, a.chats...)
		a.current, a.selected = c, c.ID
	}
	c.Messages = append(c.Messages, &Message{Role: "user", Content: text, Time: time.Now(), key: a.newKey()})
	c.Updated = time.Now()
	a.saveChats() // what the user wrote outlives a reply cut short by quitting
	a.draft = ""
	a.reply(c, m)
}

// titleOf returns the title of a chat that starts with text.
func titleOf(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) > 28 {
		r := []rune(text)
		text = string(r[:28]) + "…"
	}
	return text
}

// reply asks m for the next message of c, and streams it in.
func (a *app) reply(c *Chat, m *Model) {
	var history []llm.Message
	for _, msg := range c.Messages {
		if msg.Error != "" {
			continue
		}
		history = append(history, llm.Message{Role: msg.Role, Content: msg.Content})
	}
	reply := &Message{Role: "assistant", Model: m.label(), Time: time.Now(), key: a.newKey(), streaming: true, thinkOpen: true}
	c.Messages = append(c.Messages, reply)
	c.Updated = time.Now()
	a.moveToTop(c)
	c.list.ScrollToEnd()

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	cfg := m.config(a.settings.Proxy)
	req := llm.Request{System: a.settings.SystemPrompt, Messages: history, Thinking: a.settings.Thinking}
	a.spawn(func() {
		err := a.stream(ctx, cfg, req, func(e llm.Event) {
			a.update(func() {
				now := time.Now()
				if e.Reasoning != "" {
					if reply.thinkStart.IsZero() {
						reply.thinkStart = now
					}
					reply.Reasoning += e.Reasoning
				}
				if e.Text != "" {
					if reply.Reasoning != "" && reply.ThinkMS == 0 {
						reply.ThinkMS = max(now.Sub(reply.thinkStart).Milliseconds(), 1)
						reply.thinkOpen = false
					}
					reply.Content += e.Text
				}
			})
		})
		a.update(func() {
			stopped := ctx.Err() != nil
			cancel()
			c.cancel = nil
			reply.streaming = false
			if reply.Reasoning != "" && reply.ThinkMS == 0 {
				reply.ThinkMS = max(time.Since(reply.thinkStart).Milliseconds(), 1)
			}
			switch {
			case stopped:
			case err != nil:
				reply.Error = err.Error()
			case reply.Content == "" && reply.Reasoning == "":
				reply.Error = "模型没有返回任何内容"
			}
			c.Updated = time.Now()
			a.saveChats()
		})
	})
}

// stop stops the reply coming in c, keeping what came.
func (a *app) stop(c *Chat) {
	if c != nil && c.cancel != nil {
		c.cancel()
	}
}

// regenerate asks again for the last reply of c.
func (a *app) regenerate(c *Chat) {
	m := a.model()
	if c == nil || c.busy() || m == nil {
		return
	}
	for n := len(c.Messages); n > 0 && c.Messages[n-1].Role == "assistant"; n-- {
		c.Messages = c.Messages[:n-1]
	}
	if len(c.Messages) == 0 {
		return
	}
	a.reply(c, m)
}

// moveToTop puts c first in the sidebar.
func (a *app) moveToTop(c *Chat) {
	for i, x := range a.chats {
		if x == c {
			copy(a.chats[1:i+1], a.chats[:i])
			a.chats[0] = c
			return
		}
	}
}

// newChat shows a new, empty chat.
func (a *app) newChat() {
	a.current, a.selected = nil, ""
}

// open shows the chat of the given ID.
func (a *app) open(id string) {
	for _, c := range a.chats {
		if c.ID == id {
			a.current, a.selected = c, id
			return
		}
	}
	a.newChat()
}

// deleteChat removes the chat of the given ID.
func (a *app) deleteChat(id string) {
	for i, c := range a.chats {
		if c.ID != id {
			continue
		}
		a.stop(c)
		a.chats = append(a.chats[:i], a.chats[i+1:]...)
		if a.current == c {
			a.newChat()
		}
		a.saveChats()
		return
	}
}

func (a *app) saveChats() {
	if err := a.store.write("chats.json", a.chats); err != nil {
		a.toast = "保存对话失败：" + err.Error()
	}
}

func (a *app) saveSettings() {
	if err := a.store.write("settings.json", a.settings); err != nil {
		a.toast = "保存设置失败：" + err.Error()
	}
}

// openSettings shows the settings, on the model chats use.
func (a *app) openSettings() {
	a.settingsOpen = true
	a.testResult = ""
	if m := a.model(); m != nil {
		a.editing = m.ID
	}
}
