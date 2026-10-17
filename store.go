package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/llm"
	"github.com/Qsnh/Murmur/locales"
)

// Model is a model the user added: where it is, and how to call it.
type Model struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Protocol   llm.Protocol   `json:"protocol"`
	BaseURL    string         `json:"base_url"`
	APIKey     string         `json:"api_key"`
	Model      string         `json:"model"`
	ThinkStyle llm.ThinkStyle `json:"think_style,omitempty"`
	MaxTokens  int            `json:"max_tokens,omitempty"`
}

// label is the name the model shows under.
func (m *Model) label() string {
	if s := strings.TrimSpace(m.Name); s != "" {
		return s
	}
	if s := strings.TrimSpace(m.Model); s != "" {
		return s
	}
	return locales.ModelUntitled()
}

// config returns how to call the model, through proxy.
func (m *Model) config(proxy string) llm.Config {
	return llm.Config{
		Protocol:   m.Protocol,
		BaseURL:    m.BaseURL,
		APIKey:     m.APIKey,
		Model:      m.Model,
		ThinkStyle: m.ThinkStyle,
		MaxTokens:  m.MaxTokens,
		Proxy:      proxy,
	}
}

// Settings are the user's models and preferences.
type Settings struct {
	Models       []*Model   `json:"models"`
	Current      string     `json:"current"` // the ID of the model chats use
	Thinking     bool       `json:"thinking"`
	Effort       llm.Effort `json:"thinking_effort,omitempty"`
	SystemPrompt string     `json:"system_prompt"`
	Proxy        string     `json:"proxy"`
	// Theme is the appearance chosen; empty follows the system.
	Theme    mygo.ThemeSource `json:"theme,omitempty"`
	Language string           `json:"language,omitempty"`
	// Notify tells of the replies that come while the window is not in
	// front, in system notifications.
	Notify bool `json:"notify"`

	WebSearch     bool   `json:"web_search,omitempty"`
	SearchService string `json:"search_service,omitempty"`
	TavilyKey     string `json:"tavily_api_key,omitempty"`
	TinyFishKey   string `json:"tinyfish_api_key,omitempty"`
	DeepSeekKey   string `json:"deepseek_api_key,omitempty"`
}

// model returns the model of the given ID, or nil.
func (s *Settings) model(id string) *Model {
	for _, m := range s.Models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// Message is a turn of a chat.
type Message struct {
	Role      string      `json:"role"` // "user" or "assistant"
	Content   string      `json:"content"`
	Reasoning string      `json:"reasoning,omitempty"`
	ThinkMS   int64       `json:"think_ms,omitempty"` // how long the model thought
	Model     string      `json:"model,omitempty"`    // the name of the model that replied
	Error     string      `json:"error,omitempty"`
	Images    []*Image    `json:"images,omitempty"`    // the user's pictures
	Documents []*Document `json:"documents,omitempty"` // and files
	Time      time.Time   `json:"time"`
	// The tokens a reply took, as the provider told them: those it read,
	// and those it wrote.
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`

	Searches []*Search `json:"searches,omitempty"`
	Reads    []*Read   `json:"reads,omitempty"`
	Sources  []*Source `json:"sources,omitempty"`
	Steps    []*Step   `json:"steps,omitempty"`

	key        int       // identifies the message in the list
	streaming  bool      // the reply is still coming
	thinkOpen  bool      // the reasoning shows
	thinkStart time.Time // when the reasoning began to come
	page       *page     // the page the reply wrote out of code blocks, if any
	pageSought bool      // page was looked for

	searching   string
	reading     string
	sourcesOpen bool
	writing     bool
}

type stepKind string

const (
	stepThink  stepKind = "think"
	stepText   stepKind = "text"
	stepSearch stepKind = "search"
	stepRead   stepKind = "read"
)

type Step struct {
	Kind    stepKind `json:"kind"`
	Size    int      `json:"size,omitempty"`
	ThinkMS int64    `json:"think_ms,omitempty"`

	open bool
}

func (m *Message) step(kind stepKind) *Step {
	if n := len(m.Steps); n > 0 {
		last := m.Steps[n-1]
		if last.Kind == kind && (kind == stepThink || kind == stepText) {
			return last
		}
		if last.Kind == stepThink {
			last.open = false
		}
	}
	s := &Step{Kind: kind, open: kind == stepThink}
	m.Steps = append(m.Steps, s)
	return s
}

func (m *Message) dropStep(kind stepKind) {
	if n := len(m.Steps); n > 0 && m.Steps[n-1].Kind == kind {
		m.Steps = m.Steps[:n-1]
	}
}

func (m *Message) think(now time.Time, s string) {
	if m.thinkStart.IsZero() {
		m.thinkStart = now
	}
	m.Reasoning += s
	m.step(stepThink).Size += len(s)
	m.writing = false
}

func (m *Message) write(now time.Time, s string) {
	m.pause(now)
	if m.Content == "" {
		m.thinkOpen = false
	}
	m.Content += s
	m.step(stepText).Size += len(s)
	m.writing = true
}

func (m *Message) pause(now time.Time) {
	if !m.thinkStart.IsZero() {
		ms := max(now.Sub(m.thinkStart).Milliseconds(), 1)
		m.ThinkMS += ms
		if n := len(m.Steps); n > 0 && m.Steps[n-1].Kind == stepThink {
			m.Steps[n-1].ThinkMS += ms
		}
		m.thinkStart = time.Time{}
	}
	m.writing = false
}

// Chat is a conversation.
type Chat struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Updated  time.Time  `json:"updated"`
	Messages []*Message `json:"messages"`

	list   ui.ListState
	end    ui.Handle          // the last message, to tell whether the messages fill the list
	cancel context.CancelFunc // stops the reply coming, if any
}

// busy reports whether a reply is coming.
func (c *Chat) busy() bool { return c.cancel != nil }

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// store keeps the settings and chats in files of a directory.
type store struct{ dir string }

func (s store) path(name string) string { return filepath.Join(s.dir, name) }

func (s store) read(name string, v any) bool {
	if s.dir == "" {
		return false
	}
	b, err := os.ReadFile(s.path(name))
	return err == nil && json.Unmarshal(b, v) == nil
}

// write replaces the file atomically, readable by the user alone, as it
// holds API keys.
func (s store) write(name string, v any) error {
	if s.dir == "" {
		return nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp := s.path(name + ".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(name))
}

func (s store) loadSettings() *Settings {
	st := &Settings{Thinking: true, Notify: true}
	s.read("settings.json", st)
	if !slices.Contains(searchServices, st.SearchService) {
		st.SearchService = tinyFish
		if strings.TrimSpace(st.TavilyKey) != "" {
			st.SearchService = tavily
		}
	}
	return st
}

func (s store) loadChats() []*Chat {
	var chats []*Chat
	s.read("chats.json", &chats)
	sort.SliceStable(chats, func(i, j int) bool { return chats[i].Updated.After(chats[j].Updated) })
	return chats
}
