package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/llm"
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
	return "未命名模型"
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
	Models       []*Model `json:"models"`
	Current      string   `json:"current"` // the ID of the model chats use
	Thinking     bool     `json:"thinking"`
	SystemPrompt string   `json:"system_prompt"`
	Proxy        string   `json:"proxy"`
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
	Role      string    `json:"role"` // "user" or "assistant"
	Content   string    `json:"content"`
	Reasoning string    `json:"reasoning,omitempty"`
	ThinkMS   int64     `json:"think_ms,omitempty"` // how long the model thought
	Model     string    `json:"model,omitempty"`    // the name of the model that replied
	Error     string    `json:"error,omitempty"`
	Time      time.Time `json:"time"`

	key        int       // identifies the message in the list
	streaming  bool      // the reply is still coming
	thinkOpen  bool      // the reasoning shows
	thinkStart time.Time // when the reasoning began to come
}

// Chat is a conversation.
type Chat struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Updated  time.Time  `json:"updated"`
	Messages []*Message `json:"messages"`

	list   ui.ListState
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
	st := &Settings{Thinking: true}
	s.read("settings.json", st)
	return st
}

func (s store) loadChats() []*Chat {
	var chats []*Chat
	s.read("chats.json", &chats)
	sort.SliceStable(chats, func(i, j int) bool { return chats[i].Updated.After(chats[j].Updated) })
	return chats
}
