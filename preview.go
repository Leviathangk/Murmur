package main

import (
	"crypto/sha256"
	"encoding/hex"
	"html"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/Qsnh/Murmur/locales"
)

// previewBase is the address of the pages previewed: one of no server,
// whose pages call none of the app's Go, as those of about:blank could.
const previewBase = "https://preview.invalid/"

// page is HTML a reply wrote, to preview.
type page struct {
	title string
	html  string
	svg   bool
}

// codePage returns the page a code block in lang shows, and whether it
// shows one: HTML as it is, SVG in a page of its own.
func codePage(lang, code string) (page, bool) {
	switch strings.ToLower(lang) {
	case "html", "htm", "xhtml":
		return page{title: pageTitle(code, locales.PreviewHTML()), html: code}, true
	case "svg":
		return page{title: locales.PreviewSVG(), html: svgPage + code, svg: true}, true
	}
	return page{}, false
}

// svgPage starts the page of an SVG image, which it centers, as large as
// it is and no larger than the page.
const svgPage = `<!DOCTYPE html><meta charset="utf-8"><style>html,body{height:100%;margin:0}body{display:flex;align-items:center;justify-content:center}svg{max-width:100%;max-height:100%}</style>`

var titleTag = regexp.MustCompile(`(?is)<title(?:\s[^>]*)?>(.*?)</title\s*>`)

// pageTitle returns the title of the page src, or untitled without one.
func pageTitle(src, untitled string) string {
	if m := titleTag.FindStringSubmatch(src); m != nil {
		if t := strings.Join(strings.Fields(html.UnescapeString(m[1])), " "); t != "" {
			return t
		}
	}
	return untitled
}

var (
	pageStart = regexp.MustCompile(`(?i)^(<!doctype\s+html|<html[\s>])`)
	pageEnd   = regexp.MustCompile(`(?i)</html\s*>`)
)

// replyPage returns the page a reply wrote out of code blocks, and
// whether it wrote one: from its <!DOCTYPE html> or <html> to its
// </html>, or to its end when it was cut short.
func replyPage(text string) (page, bool) {
	d := parser.New(parser.Default)
	d.Set(text)
	for i := range d.Children(0) {
		b := d.Block(i)
		if b.Kind != parser.HTMLBlock || !pageStart.MatchString(strings.TrimSpace(text[b.Start:b.End])) {
			continue
		}
		src := text[b.Start:]
		if end := pageEnd.FindStringIndex(src); end != nil {
			src = src[:end[1]]
		}
		return page{title: pageTitle(src, locales.PreviewWebPage()), html: src}, true
	}
	return page{}, false
}

// pageOf returns the page reply m wrote out of code blocks, looked for
// once the reply came whole, or nil.
func pageOf(m *Message) *page {
	if m.Role != "assistant" || m.streaming {
		return nil
	}
	if !m.pageSought {
		m.pageSought = true
		if p, ok := replyPage(m.Content); ok {
			m.page = &p
		}
	}
	return m.page
}

// codeBlock builds the code blocks of replies: one of HTML or SVG has a
// button previewing it beside the copy button, and one of HTML a button
// downloading it, which wait, as that one does, for the block to be
// written.
func (a *app) codeBlock(c *ui.Context, b *markdown.CodeBlockView) {
	kind, ok := codePage(b.Language, "")
	if !ok {
		markdown.DefaultCodeBlock(c, b)
		return
	}
	// As markdown.DefaultCodeBlock builds it.
	th := markdown.DefaultTheme(c.Theme())
	em := func(v float32) float32 { return v * th.FontSize }
	box := ui.Column(c).Direction(ui.LeftToRight).Background(th.CodeBlockBackground).Border(1, th.CodeBlockBorder).
		Radius(th.CodeBlockRadius).AlignSelf(ui.Stretch).Clip().Role(ui.RoleGroup).Label("Code, " + b.Language)
	box.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Padding(em(0.35), em(0.4), 0, em(th.CodeBlockPadding)).Children(func() {
			ui.Text(c, b.Language).FontSize(em(0.8)).TextColor(th.Muted).Unselectable()
			ui.Spacer(c)
			button := func(key string, svg *ui.SVG, label string) bool {
				btn := ui.ButtonBase(c.Key(key)).Padding(em(0.2), em(0.45)).Radius(5).Gap(em(0.3)).Label(label).Disabled(b.Open)
				if btn.Hovered() && !b.Open {
					btn.Background(th.Text.Alpha(0.08))
				}
				btn.Children(func() {
					ui.Icon(c, svg).Size(em(0.95), em(0.95)).TextColor(th.Muted)
					ui.Text(c, label).FontSize(em(0.8)).TextColor(th.Muted)
				})
				return btn.Clicked()
			}
			if button("preview", previewIcon, locales.Preview()) {
				p, _ := codePage(b.Language, b.Code())
				a.preview(p)
			}
			if !kind.svg && button("download", saveIcon, locales.Download()) {
				p, _ := codePage(b.Language, b.Code())
				a.download(p)
			}
			b.CopyButton(c)
		})
		b.Body(c)
	})
}

// preview shows p over the window, or in the browser where the system has
// no web view to show it in.
func (a *app) preview(p page) {
	v := a.previewView()
	if v == nil && a.win != nil {
		a.openInBrowser(p)
		return
	}
	a.previewing, a.previewOpen = p, true
	if v != nil {
		v.Page().LoadHTML(p.html, previewBase)
	}
}

// previewView returns the web view previews show in, made the first time:
// nil without a window, as in tests, or where the system has no web view,
// as Windows without the WebView2 Runtime.
func (a *app) previewView() *mygo.WebView {
	if a.web != nil || a.win == nil || !mygo.WebViewAvailable() {
		return a.web
	}
	v, err := a.win.NewWebView(mygo.WebViewOptions{})
	if err != nil {
		log.Printf("no web view to preview in: %v", err)
		return nil
	}
	// The page stays: a link clicked opens in the browser, and the page
	// goes nowhere else by itself.
	v.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
		if strings.HasPrefix(e.URL, previewBase) {
			return
		}
		e.PreventDefault()
		if e.UserInitiated && outsideLink(e.URL) {
			mygo.Shell.OpenExternal(e.URL)
		}
	})
	a.web = v
	return v
}

// outsideLink reports whether a page previewed links to rawURL out of
// itself, on the web or to mail.
func outsideLink(rawURL string) bool {
	if strings.HasPrefix(rawURL, previewBase) {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return true
	}
	return false
}

// openInBrowser opens p in the browser, from a file of the temporary
// directory named after it.
func (a *app) openInBrowser(p page) {
	sum := sha256.Sum256([]byte(p.html))
	dir := filepath.Join(os.TempDir(), "Murmur")
	path := filepath.Join(dir, "preview-"+hex.EncodeToString(sum[:6])+".html")
	err := os.MkdirAll(dir, 0o700)
	if err == nil {
		// The byte order mark tells a page without <meta charset> to
		// read as UTF-8, rather than the system's encoding.
		err = os.WriteFile(path, append([]byte("\uFEFF"), p.html...), 0o600)
	}
	if err == nil {
		err = a.openPath(path)
	}
	if err != nil {
		a.toast = locales.PreviewBrowserFailed(err.Error())
	}
}

func (a *app) download(p page) {
	name := fileName(pageTitle(p.html, locales.DownloadUntitled())) + ".html"
	dir := a.exportDir
	a.spawn(func() {
		path, err := a.saveAs(dir, name)
		if err == nil && path == "" {
			return
		}
		if err == nil {
			err = os.WriteFile(path, htmlFile(p.html), 0o644)
		}
		a.update(func() {
			if err != nil {
				a.toast = locales.DownloadFailed(err.Error())
				return
			}
			a.exportDir, a.savedPage = filepath.Dir(path), path
		})
	})
}

var charsetMeta = regexp.MustCompile(`(?i)<meta\s[^>]*charset`)

func htmlFile(src string) []byte {
	if charsetMeta.MatchString(src) {
		return []byte(src)
	}
	return append([]byte("\uFEFF"), src...)
}

// previewer shows the page previewed over the window, under its title.
// A click outside, Escape or the close button closes it, and the page
// with it, which would go on running otherwise, sounds and all; the
// composer has the keyboard then, which the page took when clicked.
func (a *app) previewer(c *ui.Context) {
	t := c.Theme()
	ui.DialogBase(c.Key("preview"), &a.previewOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		ww, wh := c.Size()
		panel.Size(ww-64, wh-64).Radius(14).Clip().
			Background(t.Background).Border(1, t.Border).Shadow(0, 24, 64, 0, ui.RGBA(0, 0, 0, 0.28))
		p := a.previewing
		ui.Row(c).Height(48).Shrink(0).Gap(2).AlignItems(ui.Center).Padding(0, 10, 0, 18).
			BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Text(c, p.title).FontSize(14).Bold().SingleLine().Grow(1).Shrink(1).MinWidth(0).Margin(0, 8, 0, 0)
			iconButton(c.Key("reload"), reloadIcon, locales.PreviewReload()).Shrink(0).OnClick(func() {
				if a.web != nil {
					a.web.Page().LoadHTML(p.html, previewBase)
				}
			})
			if !p.svg {
				iconButton(c.Key("download"), saveIcon, locales.DownloadHTML()).Shrink(0).OnClick(func() { a.download(p) })
			}
			iconButton(c.Key("browser"), browserIcon, locales.PreviewOpenInBrowser()).Shrink(0).OnClick(func() { a.openInBrowser(p) })
			iconButton(c.Key("close"), closeIcon, locales.Close()).Shrink(0).OnClick(func() { a.previewOpen = false })
		})
		if a.web != nil {
			ui.WebView(c, a.web).Grow(1)
		}
	})
	if !a.previewOpen && a.previewing.html != "" {
		a.previewing = page{}
		if a.web != nil {
			a.web.Page().LoadHTML("", previewBase)
		}
		a.draftInput.Focus(c)
	}
}
