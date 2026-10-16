package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// documents writes files to attach to dir: a PDF, notes in Markdown, and
// two the app does not take.
func documents(t *testing.T, dir string) (pdf, notes, bin, doc string) {
	t.Helper()
	data, err := os.ReadFile("extract/testdata/macos.pdf")
	if err != nil {
		t.Fatal(err)
	}
	pdf, notes, bin, doc = filepath.Join(dir, "季度报告.pdf"), filepath.Join(dir, "notes.md"), filepath.Join(dir, "data.bin"), filepath.Join(dir, "旧.doc")
	os.WriteFile(pdf, data, 0o644)
	os.WriteFile(notes, []byte("# 待办\n\n- 发送文档\n"), 0o644)
	os.WriteFile(bin, []byte{0, 1, 2, 3, 0, 0}, 0o644)
	os.WriteFile(doc, []byte("\xd0\xcf\x11\xe0"), 0o644)
	return
}

func TestAttachDocuments(t *testing.T) {
	a, reqs := testApp(t)
	a.addModel(presets[2])
	pdf, notes, bin, doc := documents(t, t.TempDir())
	a.openFiles = func() ([]string, error) { return []string{pdf, notes, bin, doc}, nil }

	tt := ui.NewTester(a.view, 1080, 720)
	if err := tt.Click("添加图片或文件"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.pendingDocs) != 2 || a.pendingDocs[0].Name != "季度报告.pdf" || a.pendingDocs[1].Name != "notes.md" {
		t.Fatalf("attached %+v", a.pendingDocs)
	}
	if d := a.pendingDocs[0]; !strings.Contains(d.text, "本季度营业收入为 1,234 万元") || d.Chars == 0 || d.Cut {
		t.Errorf("PDF read as %+v", d)
	}
	if !tt.HasText("没有添加：data.bin：不是支持的文件（PDF、Word、Excel、PPT 或文本文件）；旧.doc：旧版 Word 文档请另存为 .docx 后再添加") {
		t.Errorf("no toast in %q", tt.Texts())
	}
	if !tt.HasText("PDF · 182 字") || !tt.HasText("MD · 12 字") {
		t.Errorf("cards: %q", tt.Texts())
	}
	snapshot(t, tt, "documents-attached")

	// Removed, it is not sent.
	if err := tt.Click("移除文件"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.pendingDocs) != 1 || a.pendingDocs[0].Name != "notes.md" {
		t.Fatalf("attached after removing %+v", a.pendingDocs)
	}

	// A message may be documents alone, the first of which names the chat.
	if err := tt.Click("发送"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(*reqs) != 1 {
		t.Fatalf("%d requests", len(*reqs))
	}
	msg := (*reqs)[0].Messages[0]
	if msg.Content != "" || len(msg.Documents) != 1 || msg.Documents[0].Name != "notes.md" || msg.Documents[0].Text != "# 待办\n\n- 发送文档" {
		t.Fatalf("sent %+v", msg)
	}
	if a.current.Title != "notes.md" || len(a.pendingDocs) != 0 {
		t.Errorf("title %q, %d documents still attached", a.current.Title, len(a.pendingDocs))
	}

	// The text is kept in a file of its own, and loads again.
	sent := a.current.Messages[0].Documents[0]
	if b, err := os.ReadFile(a.store.documentPath(sent.File)); err != nil || string(b) != "# 待办\n\n- 发送文档" || sent.text != "" {
		t.Fatalf("document file %q: %q, %v", sent.File, b, err)
	}
	b := newApp(a.store)
	b.addModel(presets[2])
	b.stream = a.stream
	loaded := b.chats[0].Messages[0].Documents
	if len(loaded) != 1 || loaded[0].File != sent.File || loaded[0].Name != "notes.md" || loaded[0].Chars != 12 || loaded[0].missing {
		t.Fatalf("loaded documents %+v", loaded)
	}
	tb := ui.NewTester(b.view, 1080, 720)
	b.open(b.chats[0].ID)
	tb.Frame()
	// And is sent again with the next message.
	b.send("再看看")
	if r := (*reqs)[1]; len(r.Messages) != 3 || len(r.Messages[0].Documents) != 1 || r.Messages[0].Documents[0].Text != "# 待办\n\n- 发送文档" {
		t.Errorf("second request %+v", r.Messages)
	}
	tb.Frame()
	snapshot(t, tb, "documents")

	// Clicked, it shows its text, until closed.
	if err := tb.Click("查看文件：notes.md"); err != nil {
		t.Fatal(err)
	}
	tb.Frame()
	if !b.readOpen || b.reading != loaded[0] || !tb.HasText("# 待办\n\n- 发送文档") {
		t.Fatalf("viewer open %v, texts %q", b.readOpen, tb.Texts())
	}
	snapshot(t, tb, "document-viewer")
	if err := tb.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	tb.Frame()
	if b.readOpen {
		t.Error("the viewer stays open")
	}

	// Deleting the chat deletes its documents.
	b.deleteChat(b.current.ID)
	if _, err := os.Stat(b.store.documentPath(sent.File)); !os.IsNotExist(err) {
		t.Errorf("%s left: %v", sent.File, err)
	}
}

// Documents of more than maxDocumentChars characters are cut, and say so;
// a message takes no more than maxDocuments.
func TestDocumentLimits(t *testing.T) {
	a, reqs := testApp(t)
	a.addModel(presets[2])
	dir := t.TempDir()
	long := filepath.Join(dir, "long.txt")
	os.WriteFile(long, []byte(strings.Repeat("长", maxDocumentChars+1)), 0o644)
	tt := ui.NewTester(a.view, 1080, 720)
	a.attachFiles([]string{long})
	tt.Frame()
	if len(a.pendingDocs) != 1 || !a.pendingDocs[0].Cut || a.pendingDocs[0].Chars != maxDocumentChars {
		t.Fatalf("attached %+v", a.pendingDocs)
	}
	if !tt.HasText("「long.txt」太长，只发送开头的 10 万字") || !tt.HasText("TXT · 只发送前 10 万字") {
		t.Errorf("texts %q", tt.Texts())
	}
	a.send("总结")
	text := (*reqs)[0].Messages[0].Documents[0].Text
	if !strings.HasSuffix(text, "长\n\n（文件过长，只发送了开头的 100000 字）") || strings.Count(text, "长") != maxDocumentChars+1 {
		t.Errorf("sent %d characters ending %q", len([]rune(text)), text[len(text)-80:])
	}

	notes := filepath.Join(dir, "notes.txt")
	os.WriteFile(notes, []byte("hi"), 0o644)
	paths := make([]string, maxDocuments+2)
	for i := range paths {
		paths[i] = notes
	}
	a.attachFiles(paths)
	tt.Frame()
	if len(a.pendingDocs) != maxDocuments || !tt.HasText("没有添加：一条消息最多 10 个文件") {
		t.Errorf("%d documents attached, texts %q", len(a.pendingDocs), tt.Texts())
	}
}

// A document whose text is gone shows as lost, and is not sent.
func TestMissingDocument(t *testing.T) {
	a, reqs := testApp(t)
	a.addModel(presets[0])
	_, notes, _, _ := documents(t, t.TempDir())
	a.attachFiles([]string{notes})
	a.send("看看")
	os.Remove(a.store.documentPath(a.current.Messages[0].Documents[0].File))

	b := newApp(a.store)
	b.addModel(presets[0])
	b.stream = a.stream
	tb := ui.NewTester(b.view, 1080, 720)
	b.open(b.chats[0].ID)
	tb.Frame()
	if !b.chats[0].Messages[0].Documents[0].missing || !tb.HasText("文件已丢失") {
		t.Fatalf("not missing: texts %q", tb.Texts())
	}
	if err := tb.Click("文件已丢失：notes.md"); err != nil {
		t.Fatal(err)
	}
	tb.Frame()
	if b.readOpen {
		t.Error("a lost document shows")
	}
	b.regenerate(b.current)
	if r := (*reqs)[1].Messages[0]; r.Content != "看看" || len(r.Documents) != 0 {
		t.Errorf("sent %+v", r)
	}
}

// Documents copied in Finder or Explorer are attached as they paste, with
// images.
func TestPasteDocumentFiles(t *testing.T) {
	a, _ := testApp(t)
	a.addModel(presets[0])
	dir := t.TempDir()
	pdf, notes, _, _ := documents(t, dir)
	photo := filepath.Join(dir, "a.png")
	os.WriteFile(photo, pngOf(t, picture(50, 50, 255)), 0o644)
	tt := ui.NewTester(a.view, 1080, 720)
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	data, err := transfer.FileData(pdf, notes, photo)
	if err != nil {
		t.Fatal(err)
	}
	tt.SetClipboardData(data)
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if len(a.pendingDocs) != 2 || len(a.pending) != 1 || a.draft != "" {
		t.Errorf("%d documents and %d images attached, draft %q", len(a.pendingDocs), len(a.pending), a.draft)
	}
}

func TestCharacters(t *testing.T) {
	for n, want := range map[int]string{0: "0 字", 9999: "9,999 字", 10000: "1 万字", 12345: "1.2 万字", 100000: "10 万字"} {
		if got := characters(n); got != want {
			t.Errorf("characters(%d) = %q, want %q", n, got, want)
		}
	}
	t.Setenv("MYGO_LOCALE", "zh-Hant")
	if got := characters(12345); got != "1.2 萬字" {
		t.Errorf("characters(12345) in zh-Hant = %q", got)
	}
	t.Setenv("MYGO_LOCALE", "en")
	for n, want := range map[int]string{1: "1 character", 12345: "12,345 characters", 100000: "100,000 characters"} {
		if got := characters(n); got != want {
			t.Errorf("characters(%d) = %q, want %q", n, got, want)
		}
	}
}
