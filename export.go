package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/extract"
)

// transcript is a chat to export, copied on the main thread to be written
// on another.
type transcript struct {
	title    string
	start    time.Time // when the chat began
	messages []transcriptMessage
}

type transcriptMessage struct {
	role, content, reasoning, model, err string
	thinkMS                              int64
	images                               []imageSource
	docs                                 []documentSource
}

// transcriptOf copies what of c an export writes.
func (a *app) transcriptOf(c *Chat) transcript {
	t := transcript{title: c.Title}
	if len(c.Messages) > 0 {
		t.start = c.Messages[0].Time
	}
	for _, m := range c.Messages {
		tm := transcriptMessage{role: m.Role, content: m.Content, reasoning: m.Reasoning, model: m.Model, err: m.Error, thinkMS: m.ThinkMS}
		for _, img := range m.Images {
			tm.images = append(tm.images, a.store.source(img))
		}
		for _, doc := range m.Documents {
			tm.docs = append(tm.docs, a.store.documentSource(doc))
		}
		t.messages = append(t.messages, tm)
	}
	return t
}

// write writes t as Markdown to the file at path, and its images and the
// text of its documents, those whose files are still there, into a folder
// beside it named after it, as Typora keeps them: notes.md links
// notes.assets/1.png and notes.assets/report.pdf.txt.
func (t transcript) write(path string) error {
	folder := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) + ".assets"
	links := make([][]string, len(t.messages))    // "" for an image gone
	docLinks := make([][]string, len(t.messages)) // and a document
	used := map[string]bool{}                     // the names in the folder, in lower case
	put := func(name string, data []byte) (string, error) {
		if len(used) == 0 {
			if err := os.MkdirAll(filepath.Join(filepath.Dir(path), folder), 0o755); err != nil {
				return "", err
			}
		}
		used[strings.ToLower(name)] = true
		return folder + "/" + name, os.WriteFile(filepath.Join(filepath.Dir(path), folder, name), data, 0o644)
	}
	n := 0
	for i, m := range t.messages {
		for _, src := range m.images {
			data, err := src.read()
			if err != nil {
				links[i] = append(links[i], "")
				continue
			}
			n++
			link, err := put(fmt.Sprintf("%d.%s", n, extension(src.mime)), data)
			if err != nil {
				return err
			}
			links[i] = append(links[i], link)
		}
	}
	for i, m := range t.messages {
		for _, src := range m.docs {
			text, err := src.read()
			if err != nil {
				docLinks[i] = append(docLinks[i], "")
				continue
			}
			link, err := put(documentAsset(src.name, used), []byte(text))
			if err != nil {
				return err
			}
			docLinks[i] = append(docLinks[i], link)
		}
	}
	return os.WriteFile(path, []byte(t.markdown(links, docLinks)), 0o644)
}

// documentAsset returns the name of the file of the text of the document
// named name, not one of used: its own for text, with .txt after it for
// others, as report.pdf.txt.
func documentAsset(name string, used map[string]bool) string {
	base := fileName(name)
	if extract.KindOf(name) != extract.Text || filepath.Ext(base) == "" {
		base += ".txt"
	}
	out := base
	for n := 2; used[strings.ToLower(out)]; n++ {
		ext := filepath.Ext(base)
		out = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(base, ext), n, ext)
	}
	return out
}

// markdown returns t as Markdown: the title, then each message under who
// wrote it, the thinking and errors quoted, the images and documents of
// message i linked as links[i] and docLinks[i] say.
func (t transcript) markdown(links, docLinks [][]string) string {
	var b strings.Builder
	title := strings.Join(strings.Fields(t.title), " ")
	if title == "" {
		title = "对话"
	}
	b.WriteString("# " + title + "\n\n")
	if !t.start.IsZero() {
		b.WriteString("*" + t.start.Local().Format("2006-01-02 15:04") + " · Murmur*\n\n")
	}
	for i, m := range t.messages {
		if m.content == "" && m.reasoning == "" && m.err == "" && len(m.images) == 0 && len(m.docs) == 0 {
			continue // a reply stopped before it came
		}
		who := "你"
		if m.role == "assistant" {
			who = m.model
			if who == "" {
				who = "模型"
			}
		}
		b.WriteString("## " + who + "\n\n")
		if r := strings.TrimSpace(m.reasoning); r != "" {
			label := "思考过程"
			if m.thinkMS > 0 {
				label = "已思考 " + seconds(m.thinkMS)
			}
			quote(&b, "**"+label+"**\n\n"+r)
		}
		for j, l := range docLinks[i] {
			name := markdownEscaper.Replace(m.docs[j].name)
			if l == "" {
				b.WriteString("*（文件已丢失：" + name + "）*\n\n")
				continue
			}
			b.WriteString("附件：[" + name + "](<" + linkPath(l) + ">)\n\n")
		}
		for _, l := range links[i] {
			if l == "" {
				b.WriteString("*（图片已丢失）*\n\n")
				continue
			}
			b.WriteString("![图片](<" + linkPath(l) + ">)\n\n")
		}
		if s := strings.TrimSpace(m.content); s != "" {
			// The app shows the user's text as it is, and a reply's as
			// Markdown.
			b.WriteString(asMarkdown(s, m.role == "user") + "\n\n")
		}
		if m.err != "" {
			quote(&b, "**出错了**："+m.err)
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// markdownEscaper escapes the characters Markdown takes for its marks, in
// the text of a link.
var markdownEscaper = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`, "*", `\*`, "_", `\_`, "`", "\\`")

// linkPath returns path as the destination of a link in angle brackets,
// where it may hold spaces: the characters a URL takes for others, as # for
// a fragment, are escaped.
func linkPath(path string) string {
	return strings.NewReplacer("%", "%25", "#", "%23", "?", "%3F", "<", "%3C", ">", "%3E", `\`, "%5C").Replace(path)
}

// asMarkdown returns s with a code block left open closed, as a reply
// stopped in one leaves it, which would hold the rest of the file; with
// breaks, the lines of s break where they do, but in code blocks, as the
// app shows the user's text.
func asMarkdown(s string, breaks bool) string {
	lines := strings.Split(s, "\n")
	open := "" // the fence of the code block the line is in
	for i, l := range lines {
		if open != "" {
			if closes(l, open) {
				open = ""
			}
			continue
		}
		if open = fence(l); open != "" {
			continue
		}
		if breaks && strings.TrimSpace(l) != "" && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
			lines[i] = l + "  " // a hard line break
		}
	}
	s = strings.Join(lines, "\n")
	if open != "" {
		s += "\n" + open
	}
	return s
}

// fence returns the fence opening a code block on line l, three backticks
// or tildes or more, indented by less than four spaces, or "" when it opens
// none.
func fence(l string) string {
	t := strings.TrimLeft(l, " ")
	if len(l)-len(t) > 3 || len(t) < 3 || (t[0] != '`' && t[0] != '~') {
		return ""
	}
	n := len(t) - len(strings.TrimLeft(t, t[:1]))
	if n < 3 || (t[0] == '`' && strings.Contains(t[n:], "`")) {
		return ""
	}
	return t[:n]
}

// closes reports whether line l closes the code block that f opened: as
// many of its characters or more, and nothing else.
func closes(l, f string) bool {
	t := strings.TrimSpace(l)
	return strings.HasPrefix(t, f) && strings.Trim(t, f[:1]) == "" && len(l)-len(strings.TrimLeft(l, " ")) <= 3
}

// quote writes s to b as a block quote.
func quote(b *strings.Builder, s string) {
	for line := range strings.SplitSeq(s, "\n") {
		if line == "" {
			b.WriteString(">\n")
		} else {
			b.WriteString("> " + line + "\n")
		}
	}
	b.WriteString("\n")
}

// fileName returns title as the name of a file, without the characters
// that macOS or Windows do not take in one, nor dots at its ends, which
// hide the file or which Windows drops.
func fileName(title string) string {
	s := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|`, r) {
			return ' '
		}
		return r
	}, title)
	s = strings.Trim(strings.Join(strings.Fields(s), " "), ". ")
	if r := []rune(s); len(r) > 60 {
		s = strings.TrimRight(string(r[:60]), ". ")
	}
	if s == "" {
		s = "对话"
	}
	if reservedName(s) {
		s = "_" + s
	}
	return s
}

// reservedName reports whether Windows keeps s for a device, whatever the
// extension after it.
func reservedName(s string) bool {
	switch s = strings.ToUpper(s); s {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(s) == 4 && (strings.HasPrefix(s, "COM") || strings.HasPrefix(s, "LPT")) && s[3] >= '1' && s[3] <= '9'
}

// markdownPath returns path ending in .md, unless it ends in .md or
// .markdown already, as Windows' dialog adds no extension to a name typed
// without it.
func markdownPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return path
	}
	return path + ".md"
}

// exportChat asks where to save the chat of the given ID as Markdown, and
// writes it there with its images, off the main thread.
func (a *app) exportChat(id string) {
	c := a.chat(id)
	if c == nil {
		return
	}
	t := a.transcriptOf(c)
	dir := a.exportDir
	a.spawn(func() {
		path, err := a.saveAs(dir, fileName(t.title)+".md")
		if err == nil && path == "" {
			return // canceled
		}
		if err == nil {
			err = t.write(path)
		}
		a.update(func() {
			if err != nil {
				a.toast = "导出失败：" + err.Error()
				return
			}
			a.exportDir, a.exported = filepath.Dir(path), path
		})
	})
}

// saveAsDialog shows the system's dialog to choose where to save a
// Markdown file, named name at first, in dir, or Downloads without one; it
// blocks until the dialog closes, and returns "" when canceled.
func (a *app) saveAsDialog(dir, name string) (string, error) {
	if dir == "" {
		dir, _ = mygo.App.Path(mygo.PathDownloads)
	}
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:            a.win,
		Title:             "导出对话",
		DefaultPath:       filepath.Join(dir, name),
		ButtonLabel:       "导出",
		Filters:           []mygo.FileFilter{{Name: "Markdown", Extensions: []string{"md"}}},
		CreateDirectories: true,
	})
	if err != nil || path == "" {
		return "", err
	}
	md := markdownPath(path)
	if md == path {
		return path, nil
	}
	// The dialog asked about replacing the file as named, not this one.
	if _, err := os.Stat(md); err == nil {
		r, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:        a.win,
			Type:          mygo.MessageWarning,
			Message:       "「" + filepath.Base(md) + "」已存在，要替换它吗？",
			Buttons:       []string{"替换", "取消"},
			DefaultButton: 1,
			CancelButton:  1,
		})
		if err != nil || r.Button != 0 {
			return "", err
		}
	}
	return md, nil
}

// exportToast tells that a chat was exported, with a button showing the
// file in the file manager.
func (a *app) exportToast(c *ui.Context) {
	if a.exported == "" {
		return
	}
	path := a.exported
	a.exported = ""
	reveal := "在访达中显示"
	if runtime.GOOS == "windows" {
		reveal = "在资源管理器中显示"
	}
	c.ToastAction("已导出「"+filepath.Base(path)+"」", reveal, func() { mygo.Shell.ShowItemInFolder(path) })
}
