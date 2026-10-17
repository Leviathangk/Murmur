package main

import (
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/locales"
)

const (
	sourcesWidth = 320
	minPaneWidth = 420
)

var siteColors = []ui.Color{
	ui.Hex("#2563eb"), ui.Hex("#7c3aed"), ui.Hex("#db2777"), ui.Hex("#dc2626"), ui.Hex("#ea580c"),
	ui.Hex("#ca8a04"), ui.Hex("#16a34a"), ui.Hex("#0d9488"), ui.Hex("#0891b2"), ui.Hex("#4f46e5"),
}

func (a *app) toggleSources(m *Message) {
	if a.sourcesOpen && a.sourcesFor == m {
		a.sourcesOpen = false
		return
	}
	a.sourcesFor, a.sourcesOpen = m, true
}

func (a *app) closeStaleSources() {
	if a.sourcesOpen && (a.current == nil || !slices.Contains(a.current.Messages, a.sourcesFor)) {
		a.sourcesOpen = false
	}
}

func (a *app) sourcesDocked(c *ui.Context) bool {
	w, _ := c.Size()
	if !a.collapsed {
		w -= sidebarWidth
	}
	return w-sourcesWidth >= minPaneWidth
}

func (a *app) sourcesChip(c *ui.Context, m *Message) {
	t := c.Theme()
	label := locales.SourcesCount(len(m.Sources))
	b := ui.ButtonBase(c.Key("sources-chip")).Gap(6).Padding(4, 10, 4, 5).Radius(999).AlignItems(ui.Center).AlignSelf(ui.Start).
		TextColor(t.TextMuted).Border(1, t.Border).Label(label)
	bg := t.Background
	switch {
	case a.sourcesOpen && a.sourcesFor == m:
		bg = t.Accent.Alpha(0.12).Over(t.Background)
		b.TextColor(t.Accent).Border(1, t.Accent.Alpha(0.35))
	case b.Hovered():
		bg = t.SurfaceHover
		b.TextColor(t.Text)
	}
	b.Background(bg)
	b.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			for i, site := range sitesOf(m.Sources, 4) {
				ring := ui.Box(c.Key(i)).Size(20, 20).Radius(10).Center().Shrink(0).Background(bg)
				if i > 0 {
					ring.Margin(0, 0, 0, -5)
				}
				ring.Children(func() { siteIcon(c, site, 16) })
			}
		})
		ui.Text(c, label).FontSize(13).SingleLine()
	})
	b.OnClick(func() { a.toggleSources(m) })
}

func sitesOf(sources []*Source, n int) []string {
	var sites []string
	for _, src := range sources {
		if site := siteOf(src.URL); !slices.Contains(sites, site) {
			sites = append(sites, site)
			if len(sites) == n {
				break
			}
		}
	}
	return sites
}

func siteIcon(c *ui.Context, site string, size float32) ui.Element {
	h := fnv.New32a()
	h.Write([]byte(site))
	letter := "#"
	for _, r := range site {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letter = strings.ToUpper(string(r))
			break
		}
	}
	e := ui.Box(c).Size(size, size).Radius(size / 2).Center().Shrink(0).
		Background(siteColors[h.Sum32()%uint32(len(siteColors))]).TextColor(ui.RGB(255, 255, 255))
	e.Children(func() {
		ui.Text(c, letter).FontSize(size * 0.5).FontWeight(600).SingleLine()
	})
	return e
}

func (a *app) sourcesPanel(c *ui.Context, right float32) {
	m := a.sourcesFor
	if m == nil {
		return
	}
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Gap(8).Padding(0, right+12, 0, 20).DragWindow().Children(func() {
		ui.Text(c, locales.SourcesTitle()).FontSize(14).Bold().SingleLine().Shrink(1)
		ui.Spacer(c)
		iconButton(c.Key("close"), closeIcon, locales.Close()).Shrink(0).OnClick(func() { a.sourcesOpen = false })
	})
	ui.Scroll(c.Key(m.key)).Grow(1).MinHeight(0).Padding(0, 10, 16, 10).Gap(2).Children(func() {
		for i, src := range m.Sources {
			sourceCard(c.Key(i), i+1, src)
		}
	})
}

func sourceCard(c *ui.Context, n int, src *Source) {
	t := c.Theme()
	svc := c.Services()
	site := siteOf(src.URL)
	b := ui.ButtonBase(c).Column().AlignItems(ui.Stretch).Gap(4).Padding(10).Radius(10).Label(src.title()).Tooltip(src.URL)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			siteIcon(c, site, 16)
			ui.Text(c, site).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
			if d := shortDate(src.Published); d != "" {
				ui.Box(c).Size(1, 10).Shrink(0).Background(t.Border)
				ui.Text(c, d).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(0)
			}
			ui.Spacer(c)
			ui.Text(c, strconv.Itoa(n)).FontSize(10).TextColor(t.TextMuted).Padding(1, 5).Radius(4).Background(t.Text.Alpha(0.06)).Shrink(0)
		})
		ui.Text(c, src.title()).FontSize(14).FontWeight(500).LineHeight(1.4).MaxLines(2)
		if src.Snippet != "" {
			ui.Text(c, src.Snippet).FontSize(12).LineHeight(1.5).TextColor(t.TextMuted).MaxLines(3)
		}
	})
	b.OnClick(func() { svc.OpenURL(src.URL) })
}
