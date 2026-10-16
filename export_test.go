package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestExport(t *testing.T) {
	a, _ := testApp(t)
	png1 := pngOf(t, picture(40, 30, 255))
	jpg2 := jpegOf(t, picture(40, 30, 255), 0)
	img1, _ := newImage(png1)
	img2, _ := newImage(jpg2)
	for _, img := range []*Image{img1, img2} {
		if err := a.store.writeImage(img); err != nil {
			t.Fatal(err)
		}
	}
	gone := &Image{File: "gone.png", MIME: "image/png"}
	docs := []*Document{{Name: "周报_v[2].pdf", text: "第一页"}, {Name: "notes.md", text: "# 待办"}, {Name: "notes.md", text: "另一份"}}
	for _, doc := range docs {
		if err := a.store.writeDocument(doc); err != nil {
			t.Fatal(err)
		}
	}
	docs = append(docs, &Document{Name: "gone.xlsx", File: "gone.txt"})
	start := time.Date(2026, 10, 14, 9, 30, 0, 0, time.Local)
	a.chats = []*Chat{{ID: "x", Title: "关于 a/b: 测试?", Updated: start, Messages: []*Message{
		{Role: "user", Content: "看看这几张图", Images: []*Image{img1, img2, gone}, Documents: docs, Time: start},
		{Role: "assistant", Content: "第一张是红蓝两色。\n", Reasoning: "先看第一张\n\n再看第二张", ThinkMS: 3200, Model: "GPT-5", Time: start},
		{Role: "user", Content: "再说一次", Time: start},
		{Role: "assistant", Error: "HTTP 401：Incorrect API key provided", Model: "GPT-5", Time: start},
		{Role: "user", Content: "第一行\n第二行\n\n```\nfmt.Println()\nos.Exit(1)\n```", Time: start},
		{Role: "assistant", Content: "代码如下：\n\n```go\nfunc main() {", Model: "GPT-5", Time: start}, // stopped in a code block
		{Role: "user", Content: "停", Time: start},
		{Role: "assistant", Model: "GPT-5", Time: start}, // stopped before it came
	}}}
	dir := t.TempDir()
	var asked [][2]string
	a.saveAs = func(d, name string) (string, error) {
		asked = append(asked, [2]string{d, name})
		return filepath.Join(dir, "C# 导出 1.md"), nil
	}
	tt := ui.NewTester(a.view, 1080, 720)

	if err := tt.RightClick("关于 a/b: 测试?"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("导出为 Markdown…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(asked) != 1 || asked[0] != [2]string{"", "关于 a b 测试.md"} {
		t.Fatalf("asked %q", asked)
	}
	b, err := os.ReadFile(filepath.Join(dir, "C# 导出 1.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := `# 关于 a/b: 测试?

*2026-10-14 09:30 · Murmur*

## 你

附件：[周报\_v\[2\].pdf](<C%23 导出 1.assets/周报_v[2].pdf.txt>)

附件：[notes.md](<C%23 导出 1.assets/notes.md>)

附件：[notes.md](<C%23 导出 1.assets/notes (2).md>)

*（文件已丢失：gone.xlsx）*

![图片](<C%23 导出 1.assets/1.png>)

![图片](<C%23 导出 1.assets/2.jpg>)

*（图片已丢失）*

看看这几张图

## GPT-5

> **已思考 3 秒**
>
> 先看第一张
>
> 再看第二张

第一张是红蓝两色。

## 你

再说一次

## GPT-5

> **出错了**：HTTP 401：Incorrect API key provided

## 你

第一行  
第二行

` + "```\nfmt.Println()\nos.Exit(1)\n```" + `

## GPT-5

代码如下：

` + "```go\nfunc main() {\n```" + `

## 你

停
`
	if string(b) != want {
		t.Errorf("exported\n%s\nwant\n%s", b, want)
	}
	for name, data := range map[string][]byte{"1.png": png1, "2.jpg": jpg2, "周报_v[2].pdf.txt": []byte("第一页"), "notes.md": []byte("# 待办"), "notes (2).md": []byte("另一份")} {
		if got, err := os.ReadFile(filepath.Join(dir, "C# 导出 1.assets", name)); err != nil || string(got) != string(data) {
			t.Errorf("image %s: %d bytes, %v", name, len(got), err)
		}
	}
	if !tt.HasText("已导出「C# 导出 1.md」") {
		t.Errorf("texts %q", tt.Texts())
	}

	// The next export starts in the folder of the last, from the title bar;
	// a chat without images makes no folder for them.
	a.chats = append(a.chats, &Chat{ID: "y", Title: "纯文字", Messages: []*Message{{Role: "user", Content: "你好"}}})
	a.open("y")
	a.saveAs = func(d, name string) (string, error) {
		asked = append(asked, [2]string{d, name})
		return filepath.Join(dir, "纯文字.md"), nil
	}
	tt.Frame()
	if err := tt.Click("更多"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("导出为 Markdown…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(asked) != 2 || asked[1] != [2]string{dir, "纯文字.md"} {
		t.Fatalf("asked %q", asked)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "纯文字.md")); err != nil || string(b) != "# 纯文字\n\n## 你\n\n你好\n" {
		t.Errorf("exported %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "纯文字.assets")); !os.IsNotExist(err) {
		t.Errorf("a folder for no images: %v", err)
	}

	// Canceled, nothing is written; failing, it says why.
	a.saveAs = func(string, string) (string, error) { return "", nil }
	a.exportChat("y")
	if a.exported != "" || a.toast != "" {
		t.Errorf("canceled: exported %q, toast %q", a.exported, a.toast)
	}
	a.saveAs = func(string, string) (string, error) { return filepath.Join(dir, "没有这个文件夹", "a.md"), nil }
	a.exportChat("y")
	if !strings.HasPrefix(a.toast, "导出失败：") {
		t.Errorf("toast %q", a.toast)
	}
	a.saveAs = func(string, string) (string, error) { return "", errors.New("no dialog") }
	a.exportChat("y")
	if a.toast != "导出失败：no dialog" {
		t.Errorf("toast %q", a.toast)
	}
}

func TestFileName(t *testing.T) {
	for title, want := range map[string]string{
		"Go 并发":                 "Go 并发",
		`a/b\c:d*e?f"g<h>i|j`:   "a b c d e f g h i j",
		"  .hidden.  ":          "hidden",
		"tab\there\nline":       "tab here line",
		"…":                     "…",
		"":                      "对话",
		"/?:":                   "对话",
		strings.Repeat("长", 70): strings.Repeat("长", 60),
		"con":                   "_con",
		"Com1":                  "_Com1",
		"COM0":                  "COM0",
		"console":               "console",
	} {
		if got := fileName(title); got != want {
			t.Errorf("fileName(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestWithExtension(t *testing.T) {
	for _, tc := range []struct{ path, ext, want string }{
		{"a.md", ".md", "a.md"},
		{"a.MD", ".md", "a.MD"},
		{"a.markdown", ".md", "a.markdown"},
		{"a", ".md", "a.md"},
		{"Go 1.22 笔记", ".md", "Go 1.22 笔记.md"},
		{"a.txt", ".md", "a.txt.md"},
		{"a.html", ".html", "a.html"},
		{"a.HTM", ".html", "a.HTM"},
		{"index", ".html", "index.html"},
		{"a.md", ".html", "a.md.html"},
	} {
		if got := withExtension(tc.path, saveKinds[tc.ext].exts); got != tc.want {
			t.Errorf("withExtension(%q, %s) = %q, want %q", tc.path, tc.ext, got, tc.want)
		}
	}
}

func TestAsMarkdown(t *testing.T) {
	for _, tc := range []struct {
		s      string
		breaks bool
		want   string
	}{
		// The user's lines break where they do, but in code blocks.
		{"a\nb\n\nc", true, "a  \nb\n\nc"},
		{"a\n~~~\nx\ny\n~~~\nb", true, "a  \n~~~\nx\ny\n~~~\nb"},
		{"a\nb", false, "a\nb"},
		// A code block left open is closed with its fence.
		{"x\n````\ncode\n```", false, "x\n````\ncode\n```\n````"},
		{"x\n```js\na\n```\n~~~", false, "x\n```js\na\n```\n~~~\n~~~"},
		// Indented by four spaces, or with backticks after, no fence.
		{"    ```\na", false, "    ```\na"},
		{"``` a ` b", false, "``` a ` b"},
	} {
		if got := asMarkdown(tc.s, tc.breaks); got != tc.want {
			t.Errorf("asMarkdown(%q, %v) = %q, want %q", tc.s, tc.breaks, got, tc.want)
		}
	}
}
