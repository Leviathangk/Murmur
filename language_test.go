package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/llm"
	"github.com/Qsnh/Murmur/locales"
)

func click(t *testing.T, tt *ui.Tester, s string) {
	t.Helper()
	if err := tt.Click(s); err != nil {
		t.Fatalf("%v: texts %q", err, tt.Texts())
	}
	tt.Frame()
}

func TestLanguage(t *testing.T) {
	a, _ := testApp(t)
	var applied []string
	a.applyLanguage = func(tag string) {
		applied = append(applied, tag)
		t.Setenv("MYGO_LOCALE", tag)
	}
	tt := ui.NewTester(a.view, 1080, 720)
	a.openSettings()
	tt.Frame()
	click(t, tt, "通用")
	click(t, tt, "语言")
	for _, name := range []string{"跟随系统", "简体中文", "繁體中文", "English"} {
		if !tt.HasText(name) {
			t.Fatalf("no %q among %q", name, tt.Texts())
		}
	}
	click(t, tt, "English")
	if a.settings.Language != "en" || !slices.Equal(applied, []string{"en"}) {
		t.Fatalf("language %q, applied %q", a.settings.Language, applied)
	}
	tt.Frame()
	if !tt.HasText("Appearance") || !tt.HasText("Check for Updates") || tt.HasText("外观") {
		t.Errorf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "settings-english")
	if b := newApp(a.store); b.settings.Language != "en" {
		t.Errorf("saved language %q", b.settings.Language)
	}
	a.settingsOpen = false
	tt.Frame()
	if !tt.HasText("How can I help you?") || !tt.HasText("New Chat") {
		t.Errorf("texts %q", tt.Texts())
	}

	a.openSettings()
	tt.Frame()
	click(t, tt, "Language")
	click(t, tt, "繁體中文")
	tt.Frame()
	if a.settings.Language != "zh-Hant" || !tt.HasText("外觀") || !tt.HasText("檢查更新") {
		t.Errorf("language %q, texts %q", a.settings.Language, tt.Texts())
	}
	click(t, tt, "語言")
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.settings.Language != "" || !slices.Equal(applied, []string{"en", "zh-Hant", ""}) || !tt.HasText("外观") {
		t.Errorf("language %q, applied %q, texts %q", a.settings.Language, applied, tt.Texts())
	}
}

func TestCatalogs(t *testing.T) {
	keys := func(tag string) []string {
		b, err := os.ReadFile(filepath.Join("locales", tag+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		var out []string
		for k := range m {
			if !strings.HasPrefix(k, "@") {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	source := keys("zh-Hans")
	for _, tag := range languages {
		if got := keys(tag); !slices.Equal(got, source) {
			t.Errorf("%s has %d messages, zh-Hans %d: every language says every message", tag, len(got), len(source))
		}
	}
	shipped := locales.Catalog.Languages()
	slices.Sort(shipped)
	want := slices.Clone(languages)
	slices.Sort(want)
	if !slices.Equal(shipped, want) {
		t.Errorf("the settings offer %q, the catalogs are %q", languages, shipped)
	}
	for preferred, want := range map[string]string{"zh-CN": "zh-Hans", "zh-SG": "zh-Hans", "zh-TW": "zh-Hant", "zh-HK": "zh-Hant", "en-GB": "en"} {
		if got := i18n.Match([]string{preferred}, languages); got != want {
			t.Errorf("a reader of %s gets %q, want %q", preferred, got, want)
		}
	}
}

func TestInterfaceTranslated(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("locales", "zh-Hans.json"))
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]any
	if err := json.Unmarshal(b, &source); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"en", "zh-Hant"} {
		t.Run(tag, func(t *testing.T) {
			t.Setenv("MYGO_LOCALE", tag)
			var simplified []string
			for k, v := range source {
				if _, hasArgs := source["@"+k]; strings.HasPrefix(k, "@") || hasArgs {
					continue
				}
				if s := strings.TrimSpace(v.(string)); s != locales.Catalog.Format(tag, k, nil) {
					simplified = append(simplified, s)
				}
			}
			check := func(screen string, texts []string) {
				t.Helper()
				for _, s := range texts {
					if tag == "en" && strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
						t.Errorf("%s: %q is not in English", screen, s)
					}
					for _, z := range simplified {
						if s == z {
							t.Errorf("%s: %q is in Simplified Chinese", screen, s)
						}
					}
				}
			}

			a, _ := testApp(t)
			a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
				emit(llm.Event{Reasoning: "The user says hello."})
				emit(llm.Event{Text: "Hello!\n\n```html\n<h1>Hi</h1>\n```"})
				emit(llm.Event{Usage: &llm.Usage{Input: 1234, Output: 56}})
				return nil
			}
			tt := ui.NewTester(a.view, 1080, 720)
			tt.SetLocale(tag)
			check("welcome", tt.Texts())

			a.addModel(presets[3])
			a.addModel(presets[len(presets)-1])
			a.openSettings()
			tt.Frame()
			check("models", tt.Texts())
			var options []string
			for _, o := range protocols() {
				options = append(options, o.label)
			}
			for _, o := range thinkStyles() {
				options = append(options, o.label)
			}
			for _, p := range presets {
				options = append(options, p.name())
			}
			check("options", options)
			click(t, tt, locales.SettingsGeneral())
			check("general", tt.Texts())
			a.settingsOpen = false
			tt.Frame()

			click(t, tt, locales.ComposerLabel())
			tt.Type("Hello")
			tt.Key(0, ui.KeyEnter)
			tt.Frame()
			check("chat", tt.Texts())
			if !tt.HasText(locales.Preview()) || !tt.HasText(locales.Download()) {
				t.Errorf("no buttons of the code block in %q", tt.Texts())
			}
			if want := locales.UsageTokens(locales.UsageInput(1234) + " · " + locales.UsageOutput(56)); !tt.HasText(want) {
				t.Errorf("no %q in %q", want, tt.Texts())
			}
			click(t, tt, locales.More())
			check("chat menu", tt.Menu())
			tt.CloseMenu()

			a.chats[0].Updated = time.Now().AddDate(0, 0, -30)
			a.newChat()
			tt.Frame()
			check("sidebar", tt.Texts())
		})
	}
}

func TestForkTitle(t *testing.T) {
	for title, want := range map[string]string{
		"周末去杭州":         "分支 · 周末去杭州",
		"分支 · 周末去杭州":    "分支 · 周末去杭州",
		"Branch · Trip": "Branch · Trip",
	} {
		if got := forkTitle(title); got != want {
			t.Errorf("forkTitle(%q) = %q, want %q", title, got, want)
		}
	}
	t.Setenv("MYGO_LOCALE", "en")
	if got := forkTitle("Trip"); got != "Branch · Trip" {
		t.Errorf("forkTitle in English %q", got)
	}
}

func TestExportLanguage(t *testing.T) {
	t.Setenv("MYGO_LOCALE", "en")
	tr := transcript{messages: []transcriptMessage{
		{role: "user", content: "Hi"},
		{role: "assistant", reasoning: "Hmm", thinkMS: 3000, content: "Hello", err: "HTTP 500"},
	}}
	got := tr.markdown([][]string{{"Hi.assets/1.png"}, nil}, make([][]string, 2))
	want := "# Chat\n\n## You\n\n![Image](<Hi.assets/1.png>)\n\nHi\n\n## Model\n\n> **Thought for 3s**\n>\n> Hmm\n\nHello\n\n> **Error**: HTTP 500\n"
	if got != want {
		t.Errorf("markdown %q, want %q", got, want)
	}
}
