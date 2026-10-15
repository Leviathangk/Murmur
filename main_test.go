package main

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/llm"
)

// testApp returns an app keeping its files in a temporary directory, whose
// model replies with canned reasoning and text.
func testApp(t *testing.T) (*app, *[]llm.Request) {
	t.Helper()
	a := newApp(store{dir: t.TempDir()})
	a.applyTheme = func(mygo.ThemeSource) {} // no app runs to take it
	var reqs []llm.Request
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		reqs = append(reqs, req)
		if cfg.Model == "broken" {
			return errors.New("HTTP 401：Incorrect API key provided")
		}
		if req.Thinking {
			emit(llm.Event{Reasoning: "用户在打招呼，"})
			emit(llm.Event{Reasoning: "我应该友好地回应。"})
		}
		emit(llm.Event{Text: "**你好**！"})
		emit(llm.Event{Text: "有什么可以帮你？"})
		emit(llm.Event{Usage: &llm.Usage{Input: 1234, Output: 56}})
		return nil
	}
	return a, &reqs
}

// snapshot writes what tt shows to testdata/name.png when MURMUR_SNAPSHOTS
// is set, to look at the interface without running the app.
func snapshot(t *testing.T, tt *ui.Tester, name string) {
	if os.Getenv("MURMUR_SNAPSHOTS") == "" {
		return
	}
	os.MkdirAll("testdata", 0o755)
	f, err := os.Create(filepath.Join("testdata", name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestWelcomeWithoutModels(t *testing.T) {
	a, _ := testApp(t)
	tt := ui.NewTester(a.view, 1080, 720)
	if !tt.HasText("有什么可以帮你？") {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "welcome")
	if err := tt.Click("添加模型"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settingsOpen || !tt.HasText("还没有模型") {
		t.Fatalf("settings not open: texts %q", tt.Texts())
	}
}

func TestChat(t *testing.T) {
	a, reqs := testApp(t)
	a.addModel(presets[2]) // DeepSeek
	a.settings.Models[0].APIKey = "sk-test"
	tt := ui.NewTester(a.view, 1080, 720)

	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("你好")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if len(a.chats) != 1 || a.current == nil {
		t.Fatalf("%d chats after sending", len(a.chats))
	}
	msgs := a.current.Messages
	if len(msgs) != 2 || msgs[0].Content != "你好" || msgs[1].Content != "**你好**！有什么可以帮你？" {
		t.Fatalf("messages %+v", msgs)
	}
	if msgs[1].Reasoning != "用户在打招呼，我应该友好地回应。" || msgs[1].ThinkMS == 0 {
		t.Errorf("reasoning %q, %d ms", msgs[1].Reasoning, msgs[1].ThinkMS)
	}
	if a.draft != "" {
		t.Errorf("draft %q after sending", a.draft)
	}
	if !tt.HasText("有什么可以帮你？") || !tt.HasText("你好") {
		t.Errorf("texts %q", tt.Texts())
	}
	found := false
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "已思考") {
			found = true
		}
	}
	if !found {
		t.Errorf("no thinking label in %q", tt.Texts())
	}
	if r := (*reqs)[0]; !r.Thinking || len(r.Messages) != 1 || r.Messages[0].Content != "你好" {
		t.Errorf("request %+v", r)
	}

	// Thinking off: the next request asks for none.
	if err := tt.Click("深度思考"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.Thinking {
		t.Fatal("thinking still on after a click")
	}
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("再说一次")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if r := (*reqs)[1]; r.Thinking || len(r.Messages) != 3 {
		t.Errorf("second request %+v", r)
	}
	snapshot(t, tt, "chat")

	tt.SetDark(true)
	tt.Frame()
	snapshot(t, tt, "chat-dark")

	// The chat and the settings are saved, and load again.
	b := newApp(a.store)
	if len(b.chats) != 1 || len(b.chats[0].Messages) != 4 || b.chats[0].Title != "你好" {
		t.Errorf("loaded chats %+v", b.chats)
	}
	if len(b.settings.Models) != 1 || b.settings.Models[0].APIKey != "sk-test" || b.settings.Thinking {
		t.Errorf("loaded settings %+v", b.settings)
	}
}

func TestUsage(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[2]) // DeepSeek
	tt := ui.NewTester(a.view, 1080, 720)
	a.send("你好")
	tt.Frame()
	if m := a.current.Messages[1]; m.InputTokens != 1234 || m.OutputTokens != 56 {
		t.Fatalf("usage %d, %d", m.InputTokens, m.OutputTokens)
	}
	const label = "输入 1,234 · 输出 56 tokens"
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("no usage in %q", tt.Texts())
	}
	// What it counts shows when the pointer rests on it.
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	time.Sleep(700 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("输入包括系统提示词和之前的对话，输出包括思考过程") {
		t.Errorf("no tooltip in %q", tt.Texts())
	}
	if m := newApp(a.store).chats[0].Messages[1]; m.InputTokens != 1234 || m.OutputTokens != 56 {
		t.Errorf("saved usage %d, %d", m.InputTokens, m.OutputTokens)
	}

	// Nothing shows for a reply whose provider does not tell it.
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		emit(llm.Event{Text: "好的"})
		return nil
	}
	a.regenerate(a.current)
	tt.Frame()
	if m := a.current.Messages[1]; m.Content != "好的" || m.InputTokens != 0 || m.OutputTokens != 0 {
		t.Fatalf("message %+v", m)
	}
	for _, s := range tt.Texts() {
		if strings.HasSuffix(s, " tokens") {
			t.Errorf("usage %q shown", s)
		}
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 12345: "12,345", 1234567: "1,234,567"} {
		if got := thousands(n); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
}

func TestErrorAndRetry(t *testing.T) {
	a, reqs := testApp(t)
	a.addModel(presets[0])
	a.settings.Models[0].Model = "broken"
	tt := ui.NewTester(a.view, 1080, 720)
	a.send("hello")
	tt.Frame()
	if !tt.HasText("HTTP 401：Incorrect API key provided") {
		t.Fatalf("no error in %q", tt.Texts())
	}
	snapshot(t, tt, "error")

	a.settings.Models[0].Model = "gpt-5"
	if err := tt.Click("重新生成"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	msgs := a.current.Messages
	if len(msgs) != 2 || msgs[1].Error != "" || msgs[1].Content == "" {
		t.Errorf("messages after retrying %+v", msgs)
	}
	if len(*reqs) != 2 || len((*reqs)[1].Messages) != 1 {
		t.Errorf("requests %+v", *reqs)
	}
}

func TestSettings(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[1]) // Claude
	a.addModel(presets[3]) // Qwen
	tt := ui.NewTester(a.view, 1080, 720)
	a.openSettings()
	tt.Frame()
	if !tt.HasText("claude-opus-5") || !tt.HasText("qwen-plus") {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "settings")
	if a.editing != a.settings.Models[0].ID {
		t.Fatalf("editing %q", a.editing)
	}
	if err := tt.Click("qwen-plus"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.editing != a.settings.Models[1].ID || !tt.HasText("按接口地址识别为：enable_thinking（通义千问 / 硅基流动）") {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "settings-qwen")
	if err := tt.Click("设为默认"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.Current != a.settings.Models[1].ID {
		t.Error("the default did not change")
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.settings.Models) != 2 {
		t.Fatal("deleted without confirmation")
	}
	if err := tt.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.settings.Models) != 1 || a.settings.Current != a.settings.Models[0].ID {
		t.Errorf("models after deleting %+v, current %q", a.settings.Models, a.settings.Current)
	}
	if b := newApp(a.store); len(b.settings.Models) != 1 {
		t.Errorf("saved %d models", len(b.settings.Models))
	}
}

func TestEditModel(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[len(presets)-1]) // custom
	m := a.settings.Models[0]
	tt := ui.NewTester(a.view, 1080, 720)
	a.openSettings()
	tt.Frame()

	if err := tt.Click("模型 ID"); err != nil {
		t.Fatal(err)
	}
	tt.Type("my-model")
	if err := tt.Click("接口协议"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Anthropic"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("最大输出"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("4096")
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if m.Model != "my-model" || m.Protocol != llm.Anthropic || m.MaxTokens != 4096 {
		t.Errorf("model %+v", m)
	}
	snapshot(t, tt, "settings-edited")
	tt.Frame()
	if b := newApp(a.store); b.settings.Models[0].Model != "my-model" || b.settings.Models[0].MaxTokens != 4096 {
		t.Errorf("saved %+v", b.settings.Models[0])
	}
}

func TestTheme(t *testing.T) {
	a, _ := testApp(t)
	var applied []mygo.ThemeSource
	a.applyTheme = func(s mygo.ThemeSource) { applied = append(applied, s) }
	tt := ui.NewTester(a.view, 1080, 720)
	a.openSettings()
	tt.Frame()
	if err := tt.Click("通用"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("深色"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.Theme != mygo.ThemeDark || len(applied) != 1 || applied[0] != mygo.ThemeDark {
		t.Fatalf("theme %q, applied %q", a.settings.Theme, applied)
	}
	tt.SetDark(true) // as the window follows the theme applied
	snapshot(t, tt, "settings-dark")
	if b := newApp(a.store); b.settings.Theme != mygo.ThemeDark {
		t.Errorf("saved theme %q", b.settings.Theme)
	}

	if err := tt.Click("跟随系统"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.Theme != mygo.ThemeSystem || len(applied) != 2 || applied[1] != mygo.ThemeSystem {
		t.Errorf("theme %q, applied %q", a.settings.Theme, applied)
	}
}

func TestSendButton(t *testing.T) {
	a, reqs := testApp(t)
	a.addModel(presets[0])
	tt := ui.NewTester(a.view, 1080, 720)
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("第一行")
	tt.Key(ui.Shift, ui.KeyEnter) // a new line, not sent
	tt.Type("第二行")
	tt.Frame()
	if len(*reqs) != 0 || a.draft != "第一行\n第二行" {
		t.Fatalf("draft %q, %d requests", a.draft, len(*reqs))
	}
	if err := tt.Click("发送"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if len(*reqs) != 1 || (*reqs)[0].Messages[0].Content != "第一行\n第二行" {
		t.Fatalf("requests %+v", *reqs)
	}
	if a.draft != "" {
		t.Errorf("draft %q after sending", a.draft)
	}
}

func TestCopyMessage(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[0])
	tt := ui.NewTester(a.view, 1080, 720)
	const text = "第一行\n第二行"
	a.send(text)
	tt.Frame()

	// The button is beside the bubble, before the reply's, and shows only
	// while the pointer is over the message.
	bubble, ok := tt.Find(text)
	if !ok {
		t.Fatalf("no message in %q", tt.Texts())
	}
	r, ok := tt.Find("复制")
	if !ok || r.X+r.W > bubble.X || r.Y > bubble.Y+bubble.H {
		t.Fatalf("copy button at %+v, the message at %+v", r, bubble)
	}
	if !blank(tt.Image(), r) {
		t.Error("copy button shown before the pointer is over the message")
	}
	tt.Move(bubble.X+bubble.W/2, bubble.Y+bubble.H/2)
	tt.Frame()
	time.Sleep(150 * time.Millisecond) // it fades in
	tt.Frame()
	if blank(tt.Image(), r) {
		t.Error("copy button hidden with the pointer over the message")
	}
	snapshot(t, tt, "copy-message")
	if err := tt.Click("复制"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != text {
		t.Errorf("copied %q", got)
	}
	if !tt.HasText("已复制") {
		t.Errorf("no toast in %q", tt.Texts())
	}

	// A message of only images has nothing to copy.
	pasteImage(t, tt, pngOf(t, picture(320, 200, 255)))
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if n := len(a.current.Messages); n != 4 || a.current.Messages[2].Content != "" {
		t.Fatalf("%d messages", n)
	}
	n := 0
	for _, s := range tt.Texts() {
		if s == "复制" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d copy buttons for 2 replies and a message with text", n)
	}
}

// blank reports whether the pixels of img in r, in DIPs at a scale of 1,
// are all of one color.
func blank(img *image.RGBA, r ui.Rect) bool {
	first := img.RGBAAt(int(r.X), int(r.Y))
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			if img.RGBAAt(x, y) != first {
				return false
			}
		}
	}
	return true
}

func TestGroupChats(t *testing.T) {
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, time.Local)
	chats := []*Chat{
		{ID: "a", Updated: now.Add(-time.Hour)},
		{ID: "b", Updated: now.Add(-20 * time.Hour)},
		{ID: "c", Updated: now.AddDate(0, 0, -3)},
		{ID: "d", Updated: now.AddDate(0, -2, 0)},
	}
	var got []string
	for _, g := range groupChats(chats, now) {
		s := g.title + ":"
		for _, c := range g.chats {
			s += c.ID
		}
		got = append(got, s)
	}
	if strings.Join(got, " ") != "今天:a 昨天:b 7 天内:c 更早:d" {
		t.Errorf("groups %q", got)
	}
}

// The version shown is mygo.json's, which the release workflow sets from
// the tag, as the packages are named after it.
func TestVersionFromConfig(t *testing.T) {
	b, err := os.ReadFile("mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var c struct{ Version string }
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.Version == "" || version != c.Version {
		t.Errorf("version %q, mygo.json says %q", version, c.Version)
	}
}

func TestRename(t *testing.T) {
	a, _ := testApp(t)
	a.chats = testChats()
	updated := a.chats[1].Updated
	tt := ui.NewTester(a.view, 1080, 720)

	// From the menu of a chat in the sidebar.
	if err := tt.RightClick("周末去杭州"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := strings.Join(tt.Menu(), ","); got != "重命名…,导出为 Markdown…,-,删除对话" {
		t.Fatalf("menu %q", got)
	}
	if err := tt.ChooseMenuItem("重命名…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.renameOpen || !tt.Focused("对话名称") {
		t.Fatalf("rename open %v, texts %q", a.renameOpen, tt.Texts())
	}
	snapshot(t, tt, "rename")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("  杭州两日游 ")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.renameOpen || a.chats[1].Title != "杭州两日游" || !tt.HasText("杭州两日游") {
		t.Fatalf("open %v, title %q, texts %q", a.renameOpen, a.chats[1].Title, tt.Texts())
	}
	// It keeps its place, as it was not written in.
	if a.chats[1].Updated != updated || a.chats[0].ID != "go" {
		t.Errorf("chats %+v", a.chats)
	}
	if b := newApp(a.store); b.chat("trip") == nil || b.chat("trip").Title != "杭州两日游" {
		t.Errorf("saved chats %+v", b.chats)
	}

	// From the title bar of the chat shown: a blank title is not taken,
	// and Escape cancels.
	a.open("go")
	tt.Frame()
	if err := tt.Click("更多"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("重命名…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.renameOpen || a.chats[0].Title != "Go 并发" {
		t.Errorf("open %v, title %q", a.renameOpen, a.chats[0].Title)
	}
	a.startRename("go")
	tt.Frame()
	tt.Type("改了又不要")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.renameOpen || a.chats[0].Title != "Go 并发" {
		t.Errorf("open %v, title %q after Escape", a.renameOpen, a.chats[0].Title)
	}
}
