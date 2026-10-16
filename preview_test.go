package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/llm"
)

const counterPage = `<!DOCTYPE html>
<html lang="zh">
<head>
  <title>计数器 &amp; 按钮</title>
  <style>body { font-family: sans-serif; }</style>
</head>
<body>
  <button onclick="n.textContent = +n.textContent + 1">加一</button>
  <span id="n">0</span>
</body>
</html>
`

// replyWith makes the model of a reply with text.
func replyWith(a *app, text string) {
	a.stream = func(ctx context.Context, cfg llm.Config, req llm.Request, emit func(llm.Event)) error {
		emit(llm.Event{Text: text})
		return nil
	}
}

// A code block of HTML has a button previewing it, over the window, under
// its title; the page opens in the browser from there too.
func TestPreviewCodeBlock(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[2])
	var opened []string
	a.openPath = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	replyWith(a, "这是一个计数器：\n\n```html\n"+counterPage+"```\n\n还有一段 Go：\n\n```go\nfmt.Println(1)\n```\n")
	tt := ui.NewTester(a.view, 1080, 720)
	a.send("写一个计数器网页")
	tt.Frame()

	// The block of Go has none: the button's label and its text show.
	n := 0
	for _, s := range tt.Texts() {
		if s == "预览" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d preview buttons for a block of HTML: texts %q", n, tt.Texts())
	}
	snapshot(t, tt, "preview-button")
	if err := tt.Click("预览"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.previewOpen || a.previewing.html != counterPage {
		t.Fatalf("previewing %+v, open %v", a.previewing, a.previewOpen)
	}
	if !tt.HasText("计数器 & 按钮") {
		t.Errorf("no title in %q", tt.Texts())
	}
	snapshot(t, tt, "preview")

	if err := tt.Click("在浏览器中打开"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(opened) != 1 || filepath.Ext(opened[0]) != ".html" {
		t.Fatalf("opened %q", opened)
	}
	b, err := os.ReadFile(opened[0])
	if err != nil || string(b) != "\uFEFF"+counterPage {
		t.Errorf("file %q, %v", b, err)
	}

	// Closed, it shows no page, and what is typed goes to the composer.
	if err := tt.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.previewOpen || a.previewing.html != "" || tt.HasText("计数器 & 按钮") {
		t.Errorf("previewing %+v, open %v after closing", a.previewing, a.previewOpen)
	}
	if !tt.Focused("消息") {
		t.Error("the composer has not the focus")
	}

	// Escape closes it too.
	tt.Click("预览")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.previewOpen || a.previewing.html != "" {
		t.Errorf("previewing %+v, open %v after Escape", a.previewing, a.previewOpen)
	}
}

func TestDownloadPage(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[2])
	dir := t.TempDir()
	var asked [][2]string
	saveTo := func(name string) {
		a.saveAs = func(d, n string) (string, error) {
			asked = append(asked, [2]string{d, n})
			return filepath.Join(dir, name), nil
		}
	}
	saveTo("计数器.html")
	replyWith(a, "```html\n"+counterPage+"```\n\n```svg\n<svg viewBox=\"0 0 10 10\"><circle r=\"5\"/></svg>\n```\n")
	tt := ui.NewTester(a.view, 1080, 720)
	a.send("写一个网页和一个图标")
	tt.Frame()

	n := 0
	for _, s := range tt.Texts() {
		if s == "下载" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d download buttons for a block of HTML: texts %q", n, tt.Texts())
	}
	snapshot(t, tt, "download-button")
	if err := tt.Click("下载"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(asked) != 1 || asked[0] != [2]string{"", "计数器 & 按钮.html"} {
		t.Fatalf("asked %q", asked)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "计数器.html")); err != nil || string(b) != "\uFEFF"+counterPage {
		t.Errorf("file %q, %v", b, err)
	}
	if !tt.HasText("已下载「计数器.html」") {
		t.Errorf("texts %q", tt.Texts())
	}

	saveTo("index.html")
	tt.Click("预览")
	tt.Frame()
	if err := tt.Click("下载 HTML 文件"); err != nil {
		t.Fatalf("%v: texts %q", err, tt.Texts())
	}
	tt.Frame()
	if len(asked) != 2 || asked[1] != [2]string{dir, "计数器 & 按钮.html"} {
		t.Fatalf("asked %q", asked)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Error(err)
	}
	snapshot(t, tt, "download-preview")

	a.previewOpen = false
	tt.Frame()
	p, _ := codePage("svg", `<svg viewBox="0 0 10 10"><circle r="5"/></svg>`)
	a.preview(p)
	tt.Frame()
	if !tt.HasText("SVG 预览") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if _, ok := tt.Find("下载 HTML 文件"); ok {
		t.Error("a download button for SVG")
	}

	charset := strings.Replace(counterPage, "<head>", "<head>\n  <meta charset=\"utf-8\">", 1)
	saveTo("charset.html")
	a.download(page{html: charset})
	if b, err := os.ReadFile(filepath.Join(dir, "charset.html")); err != nil || string(b) != charset {
		t.Errorf("file %q, %v", b, err)
	}

	a.savedPage, a.toast = "", ""
	a.saveAs = func(string, string) (string, error) { return "", nil }
	a.download(page{html: counterPage})
	if a.savedPage != "" || a.toast != "" {
		t.Errorf("canceled: downloaded %q, toast %q", a.savedPage, a.toast)
	}
	a.saveAs = func(string, string) (string, error) {
		return filepath.Join(dir, "没有这个文件夹", "a.html"), nil
	}
	a.download(page{html: counterPage})
	if !strings.HasPrefix(a.toast, "下载失败：") {
		t.Errorf("toast %q", a.toast)
	}
}

// A block still being written waits for its end.
func TestPreviewWaitsForTheCode(t *testing.T) {
	a, _ := testApp(t)
	a.current = &Chat{ID: newID(), Title: "网页", Messages: []*Message{
		{Role: "user", Content: "写一个网页", key: a.newKey()},
		{Role: "assistant", Content: "```html\n<!DOCTYPE html>\n<html><body>", streaming: true, key: a.newKey()},
	}}
	a.chats = []*Chat{a.current}
	tt := ui.NewTester(a.view, 1080, 720)
	if _, ok := tt.Find("预览"); !ok {
		t.Fatalf("no preview button in %q", tt.Texts())
	}
	tt.Click("预览")
	tt.Frame()
	if a.previewOpen {
		t.Error("previewing a block being written")
	}
	a.saveAs = func(string, string) (string, error) {
		t.Error("downloading a block being written")
		return "", nil
	}
	tt.Click("下载")
	tt.Frame()
}

// A reply that is a page itself, out of code blocks, has a button
// previewing it under it.
func TestPreviewReply(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[2])
	replyWith(a, counterPage)
	tt := ui.NewTester(a.view, 1080, 720)
	a.send("只输出 HTML")
	tt.Frame()
	if err := tt.Click("预览网页"); err != nil {
		t.Fatalf("%v: texts %q", err, tt.Texts())
	}
	tt.Frame()
	if !a.previewOpen || a.previewing.html != strings.TrimSuffix(counterPage, "\n") || !tt.HasText("计数器 & 按钮") {
		t.Fatalf("previewing %+v, open %v", a.previewing, a.previewOpen)
	}

	// A reply of a page in a code block has no such button: the block has.
	a.previewOpen = false
	a.newChat()
	replyWith(a, "```html\n"+counterPage+"```")
	a.send("再写一个")
	tt.Frame()
	if _, ok := tt.Find("预览网页"); ok {
		t.Error("a button under a reply of a code block")
	}
}

func TestDownloadReply(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[2])
	dir := t.TempDir()
	var asked []string
	a.saveAs = func(d, name string) (string, error) {
		asked = append(asked, name)
		return filepath.Join(dir, name), nil
	}
	replyWith(a, "<!DOCTYPE html>\n<html><body><p>没有标题</p></body></html>")
	tt := ui.NewTester(a.view, 1080, 720)
	a.send("只输出 HTML")
	tt.Frame()
	tt.Click("预览网页")
	tt.Frame()
	if err := tt.Click("下载 HTML 文件"); err != nil {
		t.Fatalf("%v: texts %q", err, tt.Texts())
	}
	tt.Frame()
	if len(asked) != 1 || asked[0] != "网页.html" {
		t.Fatalf("asked %q", asked)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "网页.html")); err != nil || string(b) != "\uFEFF<!DOCTYPE html>\n<html><body><p>没有标题</p></body></html>" {
		t.Errorf("file %q, %v", b, err)
	}
}

func TestReplyPage(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"page", counterPage, strings.TrimSuffix(counterPage, "\n")},
		{"words around", "好的，页面如下：\n\n" + counterPage + "\n保存为 index.html 打开即可。", strings.TrimSuffix(counterPage, "\n")},
		{"no doctype", "<HTML>\n<body><p>你好</p></body>\n</HTML>", "<HTML>\n<body><p>你好</p></body>\n</HTML>"},
		{"cut short", "<!doctype html>\n<html>\n<body>\n<p>写到一半", "<!doctype html>\n<html>\n<body>\n<p>写到一半"},
		{"in a code block", "```html\n" + counterPage + "```", ""},
		{"indented code", "    <!DOCTYPE html>\n    <html></html>", ""},
		{"named in words", "网页以 <html> 开头。", ""},
		{"a fragment", "<div>\n<p>只是一段</p>\n</div>", ""},
		{"markdown", "# 标题\n\n正文", ""},
	} {
		got := ""
		if p, ok := replyPage(tc.text); ok {
			got = p.html
		}
		if got != tc.want {
			t.Errorf("%s: page %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCodePage(t *testing.T) {
	if p, ok := codePage("HTML", "<b>粗</b>"); !ok || p.html != "<b>粗</b>" || p.title != "HTML 预览" {
		t.Errorf("HTML: %+v, %v", p, ok)
	}
	if p, ok := codePage("svg", `<svg viewBox="0 0 10 10"><title>圆</title><circle r="5"/></svg>`); !ok ||
		!strings.Contains(p.html, `<circle r="5"/></svg>`) || !strings.HasPrefix(p.html, "<!DOCTYPE html>") || p.title != "SVG 预览" {
		t.Errorf("SVG: %+v, %v", p, ok)
	}
	for _, lang := range []string{"go", "xml", "", "markdown"} {
		if _, ok := codePage(lang, "x"); ok {
			t.Errorf("%q previewed", lang)
		}
	}
}

func TestPageTitle(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"<title>贪吃蛇</title>", "贪吃蛇"},
		{"<TITLE lang=zh>\n  我的\n  页面 &lt;1&gt;\n</TITLE>", "我的 页面 <1>"},
		{"<title></title>", "HTML 预览"},
		{"<titles>不是</titles>", "HTML 预览"},
		{"<p>没有标题</p>", "HTML 预览"},
	} {
		if got := pageTitle(tc.src, "HTML 预览"); got != tc.want {
			t.Errorf("pageTitle(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestOutsideLink(t *testing.T) {
	for url, want := range map[string]bool{
		"https://example.com/":           true,
		"http://example.com/a?b=1":       true,
		"mailto:a@example.com":           true,
		"HTTPS://EXAMPLE.COM":            true,
		"file:///etc/passwd":             false,
		"javascript:alert(1)":            false,
		"murmur://x":                     false,
		previewBase + "#top":             false,
		"data:text/html,<p>hi</p>":       false,
		"about:blank":                    false,
		"https://preview.invalid.evil/x": true,
	} {
		if got := outsideLink(url); got != want {
			t.Errorf("outsideLink(%q) = %v", url, got)
		}
	}
}
