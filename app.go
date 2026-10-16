package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/Qsnh/Murmur/llm"
	"github.com/Qsnh/Murmur/locales"
	"github.com/Qsnh/Murmur/websearch"
)

// app is the state the window shows.
type app struct {
	win        *mygo.Window
	hiding     func() // stops the window leaving full screen from hiding then
	store      store
	settings   *Settings
	chats      []*Chat   // the newest first
	current    *Chat     // the chat shown; nil for a new one, not yet sent
	selected   string    // the ID of the chat chosen in the sidebar
	draft      string    // what is typed in the composer
	draftInput ui.Handle // the field it is typed in
	nextKey    int

	pending     []*Image    // the images attached to the message being written
	pendingDocs []*Document // and the documents
	attaching   int         // how many attachments are being read
	viewing     *Image      // the image shown large, if any
	viewOpen    bool        // the image viewer shows
	viewFull    *ui.Bitmap
	reading     *Document // the document whose text shows, if any
	readOpen    bool      // its text shows
	readText    string    // what of it shows, once read
	readLoaded  bool
	previewing  page          // the page previewed, if any
	previewOpen bool          // it shows
	web         *mygo.WebView // where pages are previewed, made the first time

	collapsed bool // the sidebar is hidden
	md        markdown.Options

	search      string    // what is searched for in the sidebar
	searchInput ui.Handle // the field it is typed in
	searched    string    // the search of found
	found       []found   // the chats matching it
	searchStale bool      // the chats changed since found was searched

	renaming   string // the ID of the chat being renamed
	renameOpen bool
	newTitle   string // the title typed for it

	exportDir string // the folder exported to last
	exported  string // the file just exported, to tell in a toast
	savedPage string

	settingsOpen bool
	settingsTab  int
	editing      string // the ID of the model edited in the settings
	testing      string // the ID of the model being tested
	testResult   string // what the last test found, for the model edited
	testOK       bool
	deleting     string // the ID of the model whose deletion awaits confirmation
	toast        string // a message to show in the next frame

	searchTesting    bool
	searchTestResult string
	searchTestOK     bool
	searchTestKey    string

	savedSettings string // the settings last written, as JSON

	// stream calls the model; tests replace it.
	stream    func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error
	webSearch func(ctx context.Context, key, proxy, query string) ([]websearch.Result, error)
	// applyTheme shows the app in an appearance; tests replace it.
	applyTheme    func(mygo.ThemeSource)
	applyLanguage func(string)
	// openFiles asks for images and documents to attach, blocking until
	// the user chooses; tests replace it.
	openFiles func() ([]string, error)
	// saveAs asks where to save a file, named name at first in dir,
	// blocking until the user chooses, "" when canceled; tests replace it.
	saveAs func(dir, name string) (string, error)
	// openPath opens a file with its app, as a page previewed in the
	// browser; tests replace it.
	openPath func(path string) error
	// notify tells of a reply in a system notification, unless the window
	// is in front; tests replace it.
	notify func(mygo.NotificationOptions)
	// updates checks for new versions of Murmur and installs them, once
	// main sets it up; tests replace it.
	updates updates
}

func newApp(s store) *app {
	a := &app{store: s, stream: llm.Stream, webSearch: tavilySearch, applyTheme: mygo.Theme.SetSource, applyLanguage: mygo.App.SetLocale}
	a.openFiles, a.saveAs, a.notify = a.openFilesDialog, a.saveAsDialog, a.notifyDesktop
	a.openPath = mygo.Shell.OpenPath
	a.md.CodeBlock = a.codeBlock
	a.settings = s.loadSettings()
	a.chats = s.loadChats()
	s.checkDocuments(a.chats)
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

// canSend reports whether text and the images and documents attached make
// a message to send: those still being read wait for the message.
func (a *app) canSend(text string) bool {
	return (strings.TrimSpace(text) != "" || len(a.pending) > 0 || len(a.pendingDocs) > 0) && a.attaching == 0
}

// send sends text and the images and documents attached in the current
// chat, starting one if needed.
func (a *app) send(text string) {
	text = strings.TrimSpace(text)
	if !a.canSend(text) {
		return
	}
	m := a.model()
	if m == nil {
		a.toast = locales.AddModelFirst()
		a.openSettings()
		return
	}
	c := a.current
	if c != nil && c.busy() {
		return
	}
	if c == nil {
		title := titleOf(text)
		switch {
		case title != "":
		case len(a.pendingDocs) > 0:
			title = titleOf(a.pendingDocs[0].Name)
		default:
			title = locales.ChatImages()
		}
		c = &Chat{ID: newID(), Title: title}
		a.chats = append([]*Chat{c}, a.chats...)
		a.current, a.selected = c, c.ID
	}
	for _, img := range a.pending {
		if err := a.store.writeImage(img); err != nil {
			a.toast = locales.ImageSaveFailed(err.Error())
		}
	}
	for _, doc := range a.pendingDocs {
		if err := a.store.writeDocument(doc); err != nil {
			a.toast = locales.DocumentSaveFailed(err.Error())
		}
	}
	c.Messages = append(c.Messages, &Message{Role: "user", Content: text, Images: a.pending, Documents: a.pendingDocs, Time: time.Now(), key: a.newKey()})
	c.Updated = time.Now()
	a.saveChats() // what the user wrote outlives a reply cut short by quitting
	a.draft, a.pending, a.pendingDocs = "", nil, nil
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

// turn is a message to send, whose images and documents are read off the
// main thread.
type turn struct {
	role, content string
	reasoning     string
	images        []imageSource
	docs          []documentSource
}

// reply asks m for the next message of c, and streams it in.
func (a *app) reply(c *Chat, m *Model) {
	var turns []turn
	for _, msg := range c.Messages {
		if msg.Error != "" {
			continue
		}
		t := turn{role: msg.Role, content: msg.Content, reasoning: msg.Reasoning}
		for _, img := range msg.Images {
			t.images = append(t.images, a.store.source(img))
		}
		for _, doc := range msg.Documents {
			t.docs = append(t.docs, a.store.documentSource(doc))
		}
		turns = append(turns, t)
	}
	reply := &Message{Role: "assistant", Model: m.label(), Time: time.Now(), key: a.newKey(), streaming: true, thinkOpen: true}
	c.Messages = append(c.Messages, reply)
	c.Updated = time.Now()
	a.moveToTop(c)
	c.list.ScrollToEnd()

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	cfg := m.config(a.settings.Proxy)
	req := llm.Request{System: a.settings.SystemPrompt, Thinking: a.settings.Thinking, Effort: a.settings.Effort}
	var s *searcher
	fallbackSystem := ""
	if a.searchOn() {
		s = &searcher{a: a, reply: reply, key: strings.TrimSpace(a.settings.TavilyKey), proxy: a.settings.Proxy}
		now := time.Now()
		req.System = searchPrompt(a.settings.SystemPrompt, now, true)
		req.Tools = []llm.Tool{s.tool()}
		fallbackSystem = searchPrompt(a.settings.SystemPrompt, now, false)
	}
	system := a.settings.SystemPrompt
	a.spawn(func() {
		req.Messages = history(turns)
		wrote := false
		emit := func(e llm.Event) {
			wrote = wrote || e.Text != ""
			a.update(func() {
				now := time.Now()
				if e.Reasoning != "" {
					reply.think(now, e.Reasoning)
				}
				if e.Text != "" {
					reply.write(now, e.Text)
				}
				if u := e.Usage; u != nil {
					reply.InputTokens, reply.OutputTokens = u.Input, u.Output
				}
			})
		}
		err := a.stream(ctx, cfg, req, emit)
		if s != nil && errors.Is(err, llm.ErrToolsUnsupported) && ctx.Err() == nil {
			gap := wrote
			err = a.searchFirst(ctx, cfg, req, s, system, fallbackSystem, func(e llm.Event) {
				if gap && e.Text != "" {
					e.Text, gap = "\n\n"+e.Text, false
				}
				emit(e)
			})
		}
		a.update(func() {
			stopped := ctx.Err() != nil
			cancel()
			c.cancel = nil
			reply.streaming = false
			reply.pause(time.Now())
			switch {
			case stopped:
			case err != nil:
				reply.Error = err.Error()
			case reply.Content == "" && reply.Reasoning == "":
				reply.Error = locales.ReplyEmpty()
			}
			c.Updated = time.Now()
			a.saveChats()
			// A reply stopped was stopped by the user, who is there.
			if !stopped && a.settings.Notify {
				a.notify(replyNotification(c, reply))
			}
		})
	})
}

// history returns the messages of turns to send, with the bytes of their
// images and the text of their documents; those whose files are gone are
// left out.
func history(turns []turn) []llm.Message {
	msgs := make([]llm.Message, 0, len(turns))
	for _, t := range turns {
		msg := llm.Message{Role: t.role, Content: t.content, Reasoning: t.reasoning}
		for _, src := range t.images {
			if data, err := src.read(); err == nil {
				msg.Images = append(msg.Images, llm.Image{MIME: src.mime, Data: data})
			}
		}
		for _, src := range t.docs {
			if text, err := src.read(); err == nil {
				msg.Documents = append(msg.Documents, llm.Document{Name: src.name, Text: sentText(src, text)})
			}
		}
		msgs = append(msgs, msg)
	}
	return msgs
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

// forkable reports whether a chat can go on from m in a new chat: m is a
// reply that came whole.
func forkable(m *Message) bool {
	return m.Role == "assistant" && !m.streaming && m.Error == "" && m.Content != ""
}

func forkTitle(title string) string {
	for _, tag := range languages {
		if strings.HasPrefix(title, locales.In(tag).ForkPrefix()) {
			return title
		}
	}
	return locales.ForkPrefix() + title
}

// fork shows a new chat of the messages of c up to message i, a reply, to
// go on from there; c stays as it is. The two chats share the files of the
// images and documents.
func (a *app) fork(c *Chat, i int) {
	if c == nil || i < 0 || i >= len(c.Messages) || !forkable(c.Messages[i]) {
		return
	}
	f := &Chat{ID: newID(), Title: forkTitle(c.Title), Updated: time.Now()}
	for _, m := range c.Messages[:i+1] {
		m := *m
		m.Images, m.Documents = slices.Clone(m.Images), slices.Clone(m.Documents)
		m.key, m.thinkOpen = a.newKey(), false
		f.Messages = append(f.Messages, &m)
	}
	a.chats = append([]*Chat{f}, a.chats...)
	a.current, a.selected = f, f.ID
	f.list.ScrollToEnd()
	a.saveChats()
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

// chat returns the chat of the given ID, or nil.
func (a *app) chat(id string) *Chat {
	for _, c := range a.chats {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// open shows the chat of the given ID.
func (a *app) open(id string) {
	if c := a.chat(id); c != nil {
		a.current, a.selected = c, id
		return
	}
	a.newChat()
}

// startRename asks for a new title for the chat of the given ID.
func (a *app) startRename(id string) {
	if c := a.chat(id); c != nil {
		a.renaming, a.renameOpen, a.newTitle = id, true, c.Title
	}
}

// rename gives the chat of the given ID title, unless it is blank. The
// chat keeps its place: it was not written in.
func (a *app) rename(id, title string) {
	title = strings.TrimSpace(title)
	c := a.chat(id)
	if c == nil || title == "" || title == c.Title {
		return
	}
	c.Title = title
	a.saveChats()
}

// deleteChat removes the chat of the given ID.
func (a *app) deleteChat(id string) {
	for i, c := range a.chats {
		if c.ID != id {
			continue
		}
		a.stop(c)
		a.chats = append(a.chats[:i], a.chats[i+1:]...)
		a.store.removeFiles(c, a.chats)
		if a.current == c {
			a.newChat()
		}
		a.saveChats()
		return
	}
}

// saveChats writes the chats, as they changed: what is searched for is
// searched again.
func (a *app) saveChats() {
	a.searchStale = true
	if err := a.store.write("chats.json", a.chats); err != nil {
		a.toast = locales.SaveChatsFailed(err.Error())
	}
}

func (a *app) saveSettings() {
	if err := a.store.write("settings.json", a.settings); err != nil {
		a.toast = locales.SaveSettingsFailed(err.Error())
	}
}

// setTheme shows the app in s from now on, as the next start will.
func (a *app) setTheme(s mygo.ThemeSource) {
	a.settings.Theme = s
	a.applyTheme(s)
}

func (a *app) setLanguage(tag string) {
	a.settings.Language = tag
	a.applyLanguage(tag)
}

// openSettings shows the settings, on the model chats use.
func (a *app) openSettings() {
	a.settingsOpen = true
	a.testResult = ""
	if m := a.model(); m != nil {
		a.editing = m.ID
	}
}
