package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/extract"
)

const (
	// maxDocumentFile is the largest file read as a document.
	maxDocumentFile = 64 << 20
	// maxDocumentChars is how much of a document's text is sent: models
	// read 128K tokens or so, which a hundred thousand characters of
	// Chinese about fill.
	maxDocumentChars = 100_000
	// maxDocuments is how many documents a message takes.
	maxDocuments = 10
	// maxPreviewChars is how much of a document's text its preview shows.
	maxPreviewChars = 20_000
	// documentWidth is the width of a document's card.
	documentWidth = 232
)

// Document is a file of a message, as PDF, Word, Excel or code, sent as
// its text, which is kept in a file of its own in the documents directory,
// as images are.
type Document struct {
	Name  string `json:"name"`           // the name of the file attached
	File  string `json:"file,omitempty"` // that of its text in the documents directory
	Chars int    `json:"chars"`          // how long its text is, in characters
	Cut   bool   `json:"cut,omitempty"`  // the file was longer than its text

	text    string // its text, until it is in File
	missing bool   // its text's file is gone
	key     int    // identifies it among those attached
}

// documentSource is where a document's text is, to read it off the main
// thread.
type documentSource struct {
	name, text, path string
	cut              bool
}

func (s documentSource) read() (string, error) {
	if s.text != "" {
		return s.text, nil
	}
	if s.path == "" {
		return "", os.ErrNotExist
	}
	b, err := os.ReadFile(s.path)
	return string(b), err
}

// documentSource returns where the text of doc is.
func (s store) documentSource(doc *Document) documentSource {
	src := documentSource{name: doc.Name, text: doc.text, cut: doc.Cut}
	if doc.File != "" && s.dir != "" {
		src.path = s.documentPath(doc.File)
	}
	return src
}

func (s store) documentPath(name string) string { return filepath.Join(s.dir, "documents", name) }

// writeDocument puts the text of doc in a file of its own, readable by the
// user alone, and lets go of it: it is read from the file to send.
func (s store) writeDocument(doc *Document) error {
	if s.dir == "" || doc.File != "" {
		return nil
	}
	name := newID() + ".txt"
	if err := os.MkdirAll(filepath.Join(s.dir, "documents"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(s.documentPath(name), []byte(doc.text), 0o600); err != nil {
		return err
	}
	doc.File, doc.text = name, ""
	return nil
}

// checkDocuments marks the documents of chats whose text is gone.
func (s store) checkDocuments(chats []*Chat) {
	if s.dir == "" {
		return
	}
	for _, c := range chats {
		for _, m := range c.Messages {
			for _, doc := range m.Documents {
				if doc.File == "" {
					doc.missing = true
				} else if _, err := os.Stat(s.documentPath(doc.File)); err != nil {
					doc.missing = true
				}
			}
		}
	}
}

// readDocument reads the text of the document at path: its first
// maxDocumentChars characters.
func readDocument(path string) (*Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("无法打开")
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.IsDir() {
		return nil, errors.New("不是文件")
	} else if st.Size() > maxDocumentFile {
		return nil, errors.New("文件太大")
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, errors.New("无法读取")
	}
	name := filepath.Base(path)
	text, cut, err := extract.Read(name, data, maxDocumentChars)
	if err != nil {
		return nil, err
	}
	return &Document{Name: name, Chars: utf8.RuneCountInString(text), Cut: cut, text: text}, nil
}

// isDocumentFile reports whether the file at path is named as a document
// is.
func isDocumentFile(path string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	return slices.Contains(extract.Extensions, ext)
}

// detachDocument removes document i from the message being written.
func (a *app) detachDocument(i int) {
	if i >= 0 && i < len(a.pendingDocs) {
		a.pendingDocs = slices.Delete(a.pendingDocs, i, i+1)
	}
}

// sentText returns the text of a document to send: that of src, saying
// so when the file was longer.
func sentText(src documentSource, text string) string {
	if src.cut {
		text += fmt.Sprintf("\n\n（文件过长，只发送了开头的 %d 字）", maxDocumentChars)
	}
	return text
}

// documentKind returns the kind of the document named name, as its card
// says, and its color.
func documentKind(name string, t *ui.Theme) (string, ui.Color) {
	switch extract.KindOf(name) {
	case extract.PDF:
		return "PDF", t.Danger
	case extract.Word:
		return "Word", t.Accent
	case extract.Excel:
		return "Excel", t.Success
	case extract.PowerPoint:
		return "PPT", t.Warning
	}
	if ext := strings.TrimPrefix(filepath.Ext(name), "."); ext != "" && len(ext) <= 8 {
		return strings.ToUpper(ext), t.TextMuted
	}
	return "文本", t.TextMuted
}

// documentMeta says what doc is and how long, as its card does.
func documentMeta(doc *Document, t *ui.Theme) string {
	if doc.missing {
		return "文件已丢失"
	}
	kind, _ := documentKind(doc.Name, t)
	if doc.Cut {
		return kind + " · 只发送前 " + characters(maxDocumentChars)
	}
	return kind + " · " + characters(doc.Chars)
}

// characters says how many characters n is: 1,234 字, or 1.2 万字.
func characters(n int) string {
	if n < 10000 {
		return thousands(n) + " 字"
	}
	s := fmt.Sprintf("%.1f", float64(n)/10000)
	return strings.TrimSuffix(s, ".0") + " 万字"
}

// documentCard shows doc: its kind, name and length; a click shows its
// text, as the model reads it.
func (a *app) documentCard(c *ui.Context, doc *Document) ui.Element {
	t := c.Theme()
	_, color := documentKind(doc.Name, t)
	icon := fileIcon
	if doc.missing {
		color, icon = t.TextMuted, noFileIcon
	}
	b := ui.ButtonBase(c).Width(documentWidth).Height(56).Padding(8, 12, 8, 8).Gap(10).Radius(12).AlignItems(ui.Center).
		Background(t.Surface).Label("查看文件：" + doc.Name)
	if b.Hovered() && !doc.missing {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		ui.Box(c).Size(40, 40).Radius(10).Center().Shrink(0).Background(color.Alpha(0.14)).TextColor(color).Children(func() {
			ui.Icon(c, icon).Size(20, 20)
		})
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, doc.Name).FontSize(13).SingleLine()
			ui.Text(c, documentMeta(doc, t)).FontSize(12).TextColor(t.TextMuted).SingleLine()
		})
	})
	if doc.missing {
		b.Label("文件已丢失：" + doc.Name).Tooltip("文件已不存在")
		return b
	}
	b.OnClick(func() { a.showDocument(doc) })
	return b
}

// showDocument shows the text of doc, read off the main thread.
func (a *app) showDocument(doc *Document) {
	a.reading, a.readOpen, a.readText, a.readLoaded = doc, true, "", false
	src := a.store.documentSource(doc)
	a.spawn(func() {
		text, err := src.read()
		a.update(func() {
			if a.reading != doc {
				return
			}
			if err != nil {
				a.readOpen, doc.missing = false, true
				a.toast = "文件已丢失"
				return
			}
			if r := []rune(text); len(r) > maxPreviewChars {
				text = string(r[:maxPreviewChars]) + "\n……"
			}
			a.readText, a.readLoaded = text, true
		})
	})
}

// documentViewer shows the text of the document being read, which a click
// outside or Escape closes.
func (a *app) documentViewer(c *ui.Context) {
	t := c.Theme()
	ui.DialogBase(c.Key("document"), &a.readOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		panel.Width(680).MaxWidthPercent(94).MaxHeightPercent(86).Padding(18, 6, 6, 20).Gap(12).Radius(14).
			Background(t.Background).Border(1, t.Border).Shadow(0, 24, 64, 0, ui.RGBA(0, 0, 0, 0.28))
		doc := a.reading
		if doc == nil {
			return
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(0, 12, 0, 0).Children(func() {
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, doc.Name).FontSize(15).Bold().SingleLine()
				meta := documentMeta(doc, t) + "，以下是发送给模型的文字"
				if doc.Chars > maxPreviewChars {
					meta += "，预览只显示前 " + characters(maxPreviewChars)
				}
				ui.Text(c, meta).FontSize(12).TextColor(t.TextMuted)
			})
			iconButton(c.Key("close"), closeIcon, "关闭").Shrink(0).OnClick(func() { a.readOpen = false })
		})
		if !a.readLoaded {
			ui.Row(c).Height(120).Center().Children(func() { ui.Spinner(c).Size(20, 20) })
			return
		}
		ui.Scroll(c.Key("text")).Grow(1).Shrink(1).MinHeight(0).Padding(0, 14, 12, 0).Children(func() {
			ui.Text(c, a.readText).FontSize(13).LineHeight(1.6).Selectable()
		})
	})
}
