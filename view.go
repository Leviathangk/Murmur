package main

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/Qsnh/Murmur/llm"
	"github.com/Qsnh/Murmur/locales"
)

const (
	titleBarHeight = 52
	sidebarWidth   = 248
	contentWidth   = 760 // the widest the messages and the composer go
)

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	if a.toast != "" {
		c.Toast(a.toast)
		a.toast = ""
	}
	if c.Shortcut(ui.Cmd, ui.KeyN) {
		a.newChat()
	}
	if c.Shortcut(ui.Cmd, ui.KeyComma) {
		a.openSettings()
	}
	if c.Shortcut(ui.Cmd, ui.KeyF) {
		a.collapsed = false
		a.searchInput.Focus(c)
	}
	a.exportToast(c)

	vibrant := c.Vibrancy()
	if vibrant {
		c.Root().Background(ui.Transparent)
	}
	bar := c.TitleBar()
	row := ui.Row(c).Fill().AlignItems(ui.Stretch)
	shown := row.Animate("sidebar", b2f(!a.collapsed), 220*time.Millisecond)
	row.Children(func() {
		// The sidebar slides out to the left as it hides.
		w := sidebarWidth * shown
		ui.Row(c).Width(w).Shrink(0).AlignItems(ui.Stretch).Clip().Children(func() {
			side := ui.Column(c).Width(sidebarWidth).Shrink(0).Margin(0, 0, 0, w-sidebarWidth)
			if !vibrant {
				side.Background(t.Surface)
			}
			side.Children(func() { a.sidebar(c, vibrant) })
		})
		if w > 0.5 {
			ui.Box(c).Width(1).Background(t.Border)
		}
		// With the sidebar hidden, the title makes room for the buttons
		// beside the window controls.
		a.pane(c, (1-shown)*(bar.Left+76))
	})

	// Beside the window controls, whether the sidebar shows or not.
	ui.Row(c.Key("tools")).Absolute().Left(bar.Left + 8).Top((titleBarHeight - 30) / 2).Gap(2).Children(func() {
		label := locales.SidebarHide()
		if a.collapsed {
			label = locales.SidebarShow()
		}
		iconButton(c.Key("sidebar"), sidebarIcon, label).OnClick(func() { a.collapsed = !a.collapsed })
		if a.collapsed {
			iconButton(c.Key("new"), newChatIcon, locales.NewChat()).OnClick(a.newChat)
		}
	})

	a.settingsView(c)
	a.renameDialog(c)
	a.imageViewer(c)
	a.documentViewer(c)
	a.previewer(c)
	a.autosave()
}

// autosave writes the settings when they changed, wherever they were
// changed: the composer's model and thinking, or the settings dialog. The
// updates go through the proxy of the settings from then on.
func (a *app) autosave() {
	b, err := json.Marshal(a.settings)
	if err != nil || string(b) == a.savedSettings {
		return
	}
	if a.savedSettings != "" {
		a.saveSettings()
	}
	a.savedSettings = string(b)
	setUpdateProxy(a.settings.Proxy)
}

// iconButton returns a borderless button showing an icon, named by label.
func iconButton(c *ui.Context, svg *ui.SVG, label string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Size(30, 30).Radius(8).Center().TextColor(t.TextMuted).Label(label).Tooltip(label)
	if b.Hovered() {
		b.Background(t.SurfaceHover).TextColor(t.Text)
	}
	b.Children(func() { ui.Icon(c, svg).Size(16, 16) })
	return b
}

// sidebar is the chats, under the room the window controls take, or
// those matching what is searched for, each with the words around the
// match.
func (a *app) sidebar(c *ui.Context, vibrant bool) {
	t := c.Theme()
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, 10, 0, 0).DragWindow().Children(func() {
		ui.Spacer(c)
		iconButton(c.Key("new"), newChatIcon, locales.NewChat()).OnClick(a.newChat)
	})
	a.searchField(c)
	searching := strings.TrimSpace(a.search) != ""
	results := a.results()
	switch {
	case len(a.chats) == 0:
		ui.Text(c, locales.SidebarEmpty()).FontSize(13).TextColor(t.TextMuted).Padding(8, 18)
		ui.Spacer(c)
	case searching && len(results) == 0:
		ui.Text(c, locales.SidebarNotFound()).FontSize(13).TextColor(t.TextMuted).Padding(8, 18)
		ui.Spacer(c)
	default:
		deleted := ""
		item := func(ch *Chat) ui.Element {
			title := ch.Title
			if title == "" {
				title = locales.NewChat()
			}
			id := ch.ID
			return ui.SidebarItem(c.Key(id), id, nil, title).ContextMenu(func(m *ui.Menu) { a.chatMenu(m, id, &deleted) })
		}
		list := ui.Sidebar(c, &a.selected, func() {
			if searching {
				ui.SidebarSection(c.Key("found"), locales.SidebarFound(len(results)), nil, func() {
					for _, f := range results {
						it := item(f.chat)
						if f.snippet != "" {
							// The words matched show on a line of their own,
							// under the title.
							it.Wrap().Children(func() {
								ui.Text(c, f.snippet).FontSize(12).Opacity(0.6).SingleLine().FillWidth()
							})
						}
					}
				})
				return
			}
			for _, g := range groupChats(a.chats, time.Now()) {
				ui.SidebarSection(c.Key(g.title), g.title, nil, func() {
					for _, ch := range g.chats {
						item(ch)
					}
				})
			}
		}).Grow(1).Label(locales.SidebarChats())
		if vibrant {
			list.Background(ui.Transparent)
		}
		if list.Changed() {
			a.open(a.selected)
			if searching {
				a.showMatch(results)
			}
		}
		if deleted != "" {
			a.deleteChat(deleted)
		}
	}
	ui.Box(c).Padding(8, 10, 10, 10).Shrink(0).Children(func() {
		b := ui.ButtonBase(c.Key("settings")).Gap(8).Padding(7, 10).Radius(8).AlignItems(ui.Center).TextColor(t.TextMuted).Label(locales.Settings())
		if b.Hovered() {
			b.Background(t.SurfaceHover).TextColor(t.Text)
		}
		b.Children(func() {
			ui.Icon(c, gearIcon).Size(16, 16)
			ui.Text(c, locales.Settings()).FontSize(13)
		})
		b.OnClick(a.openSettings)
	})
}

// searchField is where chats are searched for, as they are typed: Cmd+F
// reaches it, and Escape clears it.
func (a *app) searchField(c *ui.Context) {
	t := c.Theme()
	ui.Box(c).Padding(0, 10, 6, 10).Shrink(0).Children(func() {
		box := ui.Row(c.Key("search")).Gap(6).Padding(0, 8).Radius(8).AlignItems(ui.Center).
			Background(t.Text.Alpha(0.05)).Border(1, ui.Transparent)
		if a.searchInput.Focused(c) {
			box.Background(t.Background).Border(1, t.Accent)
		}
		box.Children(func() {
			ui.Icon(c, searchIcon).Size(14, 14).Shrink(0).TextColor(t.TextMuted)
			ui.TextInputBase(c.Key("query"), &a.search).Bind(&a.searchInput).Grow(1).MinWidth(0).Padding(6, 0).FontSize(13).
				Placeholder(locales.SearchChats()).Label(locales.SearchChats()).
				OnShortcut(0, ui.KeyEscape, func() { a.search = "" })
			if a.search != "" {
				b := ui.ButtonBase(c.Key("clear")).Size(18, 18).Radius(9).Center().Shrink(0).TextColor(t.TextMuted).Label(locales.SearchClear())
				if b.Hovered() {
					b.Background(t.SurfaceHover).TextColor(t.Text)
				}
				b.Children(func() { ui.Icon(c, closeIcon).Size(12, 12) })
				b.OnClick(func() {
					a.search = ""
					a.searchInput.Focus(c)
				})
			}
		})
	})
}

// showMatch scrolls the chat shown to where the search found it.
func (a *app) showMatch(results []found) {
	for _, f := range results {
		if f.chat == a.current && f.message >= 0 {
			f.chat.list.ScrollTo(f.message, ui.Center)
		}
	}
}

// chatMenu is the menu of the chat of the given ID: deleting it is left
// to the caller, through deleted, as it takes the chat out of a list that
// may be being built.
func (a *app) chatMenu(m *ui.Menu, id string, deleted *string) {
	if m.Item(locales.ChatRename()).Chosen() {
		a.startRename(id)
	}
	if m.Item(locales.ChatExport()).Chosen() {
		a.exportChat(id)
	}
	m.Separator()
	if m.Item(locales.ChatDelete()).Chosen() {
		*deleted = id
	}
}

// renameDialog asks for a new title for the chat being renamed.
func (a *app) renameDialog(c *ui.Context) {
	t := c.Theme()
	ui.DialogBase(c.Key("rename"), &a.renameOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		panel.Width(380).MaxWidthPercent(94).Padding(20).Gap(14).Radius(14).
			Background(t.Background).Border(1, t.Border).Shadow(0, 24, 64, 0, ui.RGBA(0, 0, 0, 0.28))
		id := a.renaming
		done := func() {
			a.rename(id, a.newTitle)
			a.renameOpen = false
		}
		ui.Text(c, locales.RenameTitle()).FontSize(16).Bold()
		ui.TextInput(c.Key("title"), &a.newTitle).Label(locales.RenameField()).Placeholder(locales.RenameField()).AutoFocus().OnSubmit(done)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c.Key("cancel"), locales.Cancel()).OnClick(func() { a.renameOpen = false })
			ui.PrimaryButton(c.Key("save"), locales.Save()).Disabled(strings.TrimSpace(a.newTitle) == "").OnClick(done)
		})
	})
}

// chatGroup is the chats of a period, as the sidebar shows them.
type chatGroup struct {
	title string
	chats []*Chat
}

// groupChats sorts chats, the newest first, into today, yesterday, the
// week and earlier.
func groupChats(chats []*Chat, now time.Time) []chatGroup {
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	bounds := []struct {
		title string
		since time.Time
	}{
		{locales.SidebarToday(), today},
		{locales.SidebarYesterday(), today.AddDate(0, 0, -1)},
		{locales.SidebarWeek(), today.AddDate(0, 0, -7)},
		{locales.SidebarEarlier(), time.Time{}},
	}
	var groups []chatGroup
	for _, ch := range chats {
		for _, b := range bounds {
			if !ch.Updated.Before(b.since) || b.since.IsZero() {
				if n := len(groups); n == 0 || groups[n-1].title != b.title {
					groups = append(groups, chatGroup{title: b.title})
				}
				groups[len(groups)-1].chats = append(groups[len(groups)-1].chats, ch)
				break
			}
		}
	}
	return groups
}

// pane is the chat shown, under a title bar whose title starts titleLeft
// further right. Images and documents dropped on it are attached to the
// message being written.
func (a *app) pane(c *ui.Context, titleLeft float32) {
	t := c.Theme()
	bar := c.TitleBar()
	col := ui.Column(c).Grow(1).MinWidth(0).Background(t.Background)
	if files := col.DroppedFiles(); files != nil {
		a.attachFiles(files)
	}
	dragOver := col.FileDragOver()
	col.Children(func() {
		deleted := ""
		ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Gap(8).Padding(0, bar.Right+12, 0, 20+titleLeft).DragWindow().Children(func() {
			title := locales.NewChat()
			if a.current != nil && a.current.Title != "" {
				title = a.current.Title
			}
			ui.Text(c, title).FontSize(14).Bold().SingleLine().Shrink(1)
			if ch := a.current; ch != nil {
				ui.Spacer(c)
				id := ch.ID
				iconButton(c.Key("more"), moreIcon, locales.More()).Shrink(0).Menu(func(m *ui.Menu) { a.chatMenu(m, id, &deleted) })
			}
		})
		if deleted != "" {
			a.deleteChat(deleted)
		}
		if ch := a.current; ch != nil && len(ch.Messages) > 0 {
			a.messages(c, ch)
		} else {
			a.welcome(c)
		}
		a.composer(c, dragOver)
	})
}

// welcome is what a new chat shows.
func (a *app) welcome(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(14).Padding(24).Children(func() {
		ui.Icon(c, logoIcon).Size(40, 40).TextColor(t.Accent)
		ui.Text(c, locales.WelcomeTitle()).FontSize(24).Bold()
		if a.model() == nil {
			ui.Text(c, locales.WelcomeAddModel()).
				TextColor(t.TextMuted).TextAlign(ui.Center).MaxWidth(420)
			ui.PrimaryButton(c, locales.AddModel()).OnClick(a.openSettings)
		}
	})
}

// messagesBottom is the room below the last message in the list.
const messagesBottom = 28

// messages is the list of the messages of ch, which follows its end as a
// reply comes in.
func (a *app) messages(c *ui.Context, ch *Chat) {
	st := &ch.list
	st.Key = func(i int) any { return ch.Messages[i].key }
	st.FollowEnd = true
	// The end glides into view only once the messages reach the bottom of
	// the list. MyGo glides the rows by as much as the end moved down even
	// while they fit and the list does not scroll, so that they moved down
	// and back up with each line of a reply (#1).
	st.FollowAnimation = nil
	if list, end := st.Bounds(c), ch.end.Bounds(c); end.H > 0 && end.Y+end.H >= list.Y+list.H-messagesBottom-0.5 {
		st.FollowAnimation = &ui.Smooth
	}
	ui.List(c.Key(ch.ID), st, len(ch.Messages), func(i int) {
		a.message(c, ch, i)
	}).Grow(1).Padding(8, 24, messagesBottom, 24).Gap(28)
}

// message builds message i of ch: the user's in a bubble on the right,
// under its documents and images, the model's as Markdown under its
// thinking.
func (a *app) message(c *ui.Context, ch *Chat, i int) {
	t := c.Theme()
	m := ch.Messages[i]
	col := ui.Column(c).FillWidth().MaxWidth(contentWidth).Margin(0, ui.Auto).Gap(8)
	if i == len(ch.Messages)-1 {
		col.Bind(&ch.end)
	}
	col.Children(func() {
		if m.Role == "user" {
			if len(m.Documents) > 0 {
				ui.Row(c.Key("documents")).Wrap().Justify(ui.End).Gap(8).Children(func() {
					for j, doc := range m.Documents {
						a.documentCard(c.Key(j), doc)
					}
				})
			}
			if len(m.Images) > 0 {
				ui.Row(c.Key("images")).Wrap().Justify(ui.End).Gap(8).Children(func() {
					for j, img := range m.Images {
						w, h := shownSize(img, len(m.Images))
						a.thumbnail(c.Key(j), img, w, h)
					}
				})
			}
			if m.Content != "" {
				// The copy button beside the bubble shows while the pointer
				// is over the message, or the button has the focus; it keeps
				// its room, so that nothing moves as it shows.
				shown := col.Animate("copy", b2f(col.Hovered() || col.FocusWithin()), 120*time.Millisecond)
				ui.Row(c).Gap(6).AlignItems(ui.End).Children(func() {
					ui.Spacer(c)
					a.copyButton(c, m.Content).Shrink(0).Margin(0, 0, 6, 0).Opacity(shown)
					ui.Text(c, m.Content).Selectable().LineHeight(1.5).Padding(10, 16).Radius(18).Background(t.Surface).MaxWidthPercent(80)
				})
			}
			return
		}
		ui.Column(c).Gap(10).Children(func() {
			if m.Reasoning != "" {
				a.thinking(c, m)
			}
			if len(m.Searches) > 0 || m.searching != "" {
				a.searches(c, m)
			}
			switch {
			case m.Content != "" && m.streaming:
				markdown.Stream(c, m.Content, &a.md)
			case m.Content != "":
				markdown.View(c, m.Content, &a.md)
			case m.streaming && m.Reasoning == "" && m.searching == "":
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, locales.ReplyWaiting()).FontSize(13).TextColor(t.TextMuted)
				})
			}
			if m.Error != "" {
				ui.Row(c).Gap(8).Padding(10, 12).Radius(10).Background(t.Danger.Alpha(0.1)).TextColor(t.Danger).Children(func() {
					ui.Icon(c, alertIcon).Size(16, 16).Shrink(0)
					ui.Text(c, m.Error).FontSize(13).Selectable().Grow(1).Shrink(1)
				})
			}
			if !m.streaming {
				a.actions(c, ch, i)
			}
		})
	})
}

// thinking shows the model's reasoning, which hides once it answers.
func (a *app) thinking(c *ui.Context, m *Message) {
	t := c.Theme()
	label := locales.ThinkingTitle()
	switch {
	case m.streaming && m.Content == "":
		label = locales.ThinkingNow(seconds(time.Since(m.thinkStart).Milliseconds()))
		c.After(time.Second)
	case m.ThinkMS > 0:
		label = locales.ThinkingDone(seconds(m.ThinkMS))
	}
	p := ui.CollapsibleBase(c.Key("think"), &m.thinkOpen)
	p.Trigger.Gap(6).Padding(5, 10).Margin(0, 0, 0, -10).Radius(8).AlignItems(ui.Center).AlignSelf(ui.Start).TextColor(t.TextMuted)
	if p.Trigger.Hovered() {
		p.Trigger.Background(t.SurfaceHover).TextColor(t.Text)
	}
	p.Trigger.Children(func() {
		ui.Icon(c, bulbIcon).Size(14, 14)
		ui.Text(c, label).FontSize(13)
		ui.Icon(c, chevronIcon).Size(14, 14).Rotate(90 * p.Progress())
	})
	p.Panel(func() {
		ui.Column(c).Margin(6, 0, 2, 4).Padding(2, 0, 2, 14).BorderWidth(0, 0, 0, 2).BorderColor(t.Border).Children(func() {
			ui.Text(c, strings.TrimSpace(m.Reasoning)).FontSize(13).LineHeight(1.6).TextColor(t.TextMuted).Selectable()
		})
	})
}

// seconds says how long ms is, as the thinking label does.
func seconds(ms int64) string {
	s := (ms + 500) / 1000
	if s < 60 {
		return locales.DurationSeconds(int(max(s, 1)))
	}
	return locales.DurationMinutes(int(s/60), int(s%60))
}

// actions are the buttons under a reply.
func (a *app) actions(c *ui.Context, ch *Chat, i int) {
	t := c.Theme()
	m := ch.Messages[i]
	last := i == len(ch.Messages)-1
	ui.Row(c).Gap(2).AlignItems(ui.Center).Margin(-4, 0, 0, -6).Children(func() {
		if m.Content != "" {
			a.copyButton(c, m.Content)
		}
		if p := pageOf(m); p != nil {
			iconButton(c.Key("preview"), previewIcon, locales.ReplyPreviewPage()).OnClick(func() { a.preview(*p) })
		}
		if forkable(m) {
			iconButton(c.Key("fork"), forkIcon, locales.ReplyFork()).OnClick(func() {
				a.fork(ch, i)
				a.draftInput.Focus(c)
			})
		}
		if last {
			iconButton(c.Key("retry"), retryIcon, locales.ReplyRegenerate()).OnClick(func() { a.regenerate(ch) })
		}
		if m.Model != "" {
			ui.Text(c, m.Model).FontSize(12).TextColor(t.TextMuted).Margin(0, 0, 0, 6)
		}
		if s := usageLabel(m); s != "" {
			left := float32(6)
			if m.Model != "" {
				ui.Text(c, "·").FontSize(12).TextColor(t.TextMuted).Margin(0, 0, 0, 2)
				left = 2
			}
			ui.Text(c, s).FontSize(12).TextColor(t.TextMuted).Margin(0, 0, 0, left).
				Tooltip(locales.UsageTooltip())
		}
	})
}

// copyButton puts text on the clipboard, as a message's is.
func (a *app) copyButton(c *ui.Context, text string) ui.Element {
	svc := c.Services()
	return iconButton(c.Key("copy"), copyIcon, locales.Copy()).OnClick(func() {
		svc.WriteClipboard(text)
		a.toast = locales.Copied()
	})
}

// usageLabel says how many tokens reply m took, or "" when it is not
// known: the provider did not tell, or the reply stopped or failed.
func usageLabel(m *Message) string {
	var parts []string
	if m.InputTokens > 0 {
		parts = append(parts, locales.UsageInput(m.InputTokens))
	}
	if m.OutputTokens > 0 {
		parts = append(parts, locales.UsageOutput(m.OutputTokens))
	}
	if len(parts) == 0 {
		return ""
	}
	return locales.UsageTokens(strings.Join(parts, " · "))
}

// composer is where the user writes, with the documents and images
// attached above the text and the model and thinking chosen below it. It
// shows that it takes files dragged over the chat.
func (a *app) composer(c *ui.Context, dragOver bool) {
	t := c.Theme()
	ch := a.current
	busy := ch != nil && ch.busy()
	svc := c.Services()
	ui.Column(c).FillWidth().MaxWidth(contentWidth+48).Margin(0, ui.Auto).Padding(0, 24, 18, 24).Shrink(0).Children(func() {
		box := ui.Column(c).Gap(6).Padding(12, 12, 8, 16).Radius(20).Background(t.Background).Border(1, t.Border)
		if !t.Dark {
			box.Shadow(0, 2, 12, 0, ui.RGBA(0, 0, 0, 0.05))
		}
		if dragOver {
			box.Background(t.Accent.Alpha(0.06)).Border(1, t.Accent)
		}
		box.Children(func() {
			if len(a.pending) > 0 || len(a.pendingDocs) > 0 || a.attaching > 0 {
				a.attachments(c)
			}
			ui.TextAreaBase(c.Key("draft"), &a.draft).Bind(&a.draftInput).Lines(1, 10).Padding(4, 4, 4, 0).
				Placeholder(locales.ComposerPlaceholder()).Label(locales.ComposerLabel()).AutoFocus().
				OnPaste(func(text string) bool { return a.paste(svc, text) }).
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key != ui.KeyEnter || k.Mods != 0 {
						return false // Shift+Enter breaks the line
					}
					if cur := a.current; cur != nil && cur.busy() {
						return true
					}
					text := k.Text()
					if !a.canSend(text) {
						return true
					}
					if a.model() == nil {
						a.toast = locales.AddModelFirst()
						a.openSettings()
						return true
					}
					k.SetText("")
					a.send(text)
					return true
				})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				iconButton(c.Key("attach"), clipIcon, locales.ComposerAttach()).OnClick(a.pickFiles)
				a.modelPicker(c)
				w, _ := c.Size()
				if !a.collapsed {
					w -= sidebarWidth
				}
				a.thinkPicker(c, w < 640)
				a.searchToggle(c, w < 640)
				ui.Spacer(c)
				b := ui.ButtonBase(c.Key("send")).Size(32, 32).Radius(16).Center()
				if busy {
					b.Background(t.Text).TextColor(t.Background).Label(locales.ComposerStop()).Tooltip(locales.ComposerStopTooltip())
					b.Children(func() { ui.Icon(c, stopIcon).Size(16, 16) })
					b.OnClick(func() { a.stop(ch) })
					return
				}
				empty := !a.canSend(a.draft)
				if empty {
					b.Background(t.SurfaceHover).TextColor(t.TextMuted)
				} else {
					b.Background(t.Text).TextColor(t.Background)
				}
				b.Disabled(empty).Label(locales.ComposerSend()).Tooltip(locales.ComposerSend())
				b.Children(func() { ui.Icon(c, arrowUpIcon).Size(16, 16) })
				b.OnClick(func() { a.send(a.draft) })
			})
		})
	})
}

// attachments are the documents and images attached to the message being
// written, each with a button that removes it, and those still being read.
func (a *app) attachments(c *ui.Context) {
	t := c.Theme()
	ui.Row(c.Key("attachments")).Wrap().Gap(8).Padding(0, 0, 4, 0).AlignItems(ui.Center).Children(func() {
		for i, doc := range a.pendingDocs {
			ui.Box(c.Key(doc.key)).Children(func() {
				a.documentCard(c.Key("document"), doc).Padding(8, 34, 8, 8)
				b := ui.ButtonBase(c.Key("remove")).Absolute().Top(18).Right(8).Size(20, 20).Radius(10).Center().
					TextColor(t.TextMuted).Label(locales.AttachmentRemoveFile()).Tooltip(locales.Remove())
				if b.Hovered() {
					b.Background(t.Border).TextColor(t.Text)
				}
				b.Children(func() { ui.Icon(c, closeIcon).Size(12, 12) })
				b.OnClick(func() { a.detachDocument(i) })
			})
		}
		for i, img := range a.pending {
			ui.Box(c.Key(img)).Size(64, 64).Children(func() {
				a.thumbnail(c.Key("image"), img, 64, 64)
				b := ui.ButtonBase(c.Key("remove")).Absolute().Top(4).Right(4).Size(20, 20).Radius(10).Center().
					Background(ui.RGBA(0, 0, 0, 0.6)).TextColor(ui.RGB(255, 255, 255)).Label(locales.AttachmentRemoveImage()).Tooltip(locales.Remove())
				if b.Hovered() {
					b.Background(ui.RGBA(0, 0, 0, 0.8))
				}
				b.Children(func() { ui.Icon(c, closeIcon).Size(12, 12) })
				b.OnClick(func() { a.detach(i) })
			})
		}
		if a.attaching > 0 {
			ui.Box(c.Key("attaching")).Size(64, 64).Radius(10).Background(t.Surface).Center().Children(func() {
				ui.Spinner(c).Size(16, 16)
			})
		}
	})
}

// shownSize returns the size img shows at in a message of n images: one
// alone keeps its shape, several show as squares.
func shownSize(img *Image, n int) (float32, float32) {
	const side = 280
	if n > 1 {
		return 120, 120
	}
	if img.W <= 0 || img.H <= 0 {
		return 200, 200
	}
	scale := min(1, side/float32(max(img.W, img.H)))
	return max(float32(img.W)*scale, 48), max(float32(img.H)*scale, 48)
}

// thumbnail shows img at w×h, cropped to fill it, and large when clicked.
func (a *app) thumbnail(c *ui.Context, img *Image, w, h float32) {
	t := c.Theme()
	b := ui.ButtonBase(c).Size(w, h).Radius(10).Background(t.Surface).Center().Label(locales.ImageView())
	b.Children(func() {
		switch {
		case img.thumb != nil:
			ui.Image(c, img.thumb).Size(w, h).Fit(ui.Cover).Radius(10)
		case img.missing:
			ui.Icon(c, noImageIcon).Size(20, 20).TextColor(t.TextMuted)
		default:
			if !img.loading {
				a.loadThumb(img)
			}
			ui.Spinner(c).Size(16, 16)
		}
	})
	if img.missing {
		b.Label(locales.ImageMissing()).Tooltip(locales.ImageGone())
		return
	}
	b.OnClick(func() { a.showImage(img) })
}

// showImage shows img large, over the window, read in full off the main
// thread: its thumbnail shows until then.
func (a *app) showImage(img *Image) {
	a.viewing, a.viewOpen, a.viewFull = img, true, nil
	src := a.store.source(img)
	a.spawn(func() {
		var full *ui.Bitmap
		data, err := src.read()
		if err == nil {
			full, _ = ui.DecodeBitmap(data)
		}
		a.update(func() {
			if a.viewing == img && full != nil {
				a.viewFull = full
			}
		})
	})
}

// imageViewer is the image shown large, which a click or Escape closes.
func (a *app) imageViewer(c *ui.Context) {
	ui.DialogBase(c.Key("viewer"), &a.viewOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.8))
		img := a.viewing
		if img == nil {
			return
		}
		bm := a.viewFull
		if bm == nil {
			bm = img.thumb
		}
		if bm == nil {
			ui.Spinner(c).Size(20, 20)
			return
		}
		w, h := float32(img.W), float32(img.H)
		if w <= 0 || h <= 0 {
			pw, ph := bm.Size()
			w, h = float32(pw), float32(ph)
		}
		ww, wh := c.Size()
		scale := min(1, (ww-64)/w, (wh-64)/h)
		b := ui.ButtonBase(c.Key("image")).Label(locales.ImageClose())
		b.Children(func() { ui.Image(c, bm).Size(w*scale, h*scale).Radius(6) })
		b.OnClick(func() { a.viewOpen = false })
	})
}

// paste attaches the images and documents a paste holds: a picture
// copied, as a screenshot, or files copied in Finder or Explorer. It
// reports whether it took the paste, which text otherwise is.
func (a *app) paste(svc ui.Services, text string) bool {
	formats := svc.ClipboardFormats()
	if slices.Contains(formats, transfer.FileList) {
		files, _ := svc.ReadClipboardData(transfer.FileList).Files()
		var attached []string
		for _, f := range files {
			if isImageFile(f) || isDocumentFile(f) {
				attached = append(attached, f)
			}
		}
		if len(attached) > 0 {
			a.attachFiles(attached)
			return true
		}
	}
	// A text copied with a picture of it, as cells of a spreadsheet, is
	// pasted as text.
	if text == "" && slices.Contains(formats, transfer.PNG) {
		if data, err := svc.ReadClipboardData(transfer.PNG).Read(transfer.PNG); err == nil && len(data) > 0 {
			a.attachData(data)
			return true
		}
	}
	return false
}

// modelPicker chooses the model chats use.
func (a *app) modelPicker(c *ui.Context) {
	t := c.Theme()
	m := a.model()
	if m == nil {
		b := ui.ButtonBase(c.Key("add-model")).Gap(4).Padding(5, 8).Radius(8).AlignItems(ui.Center).TextColor(t.Accent).Label(locales.AddModel())
		if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
		b.Children(func() {
			ui.Icon(c, plusIcon).Size(14, 14)
			ui.Text(c, locales.AddModel()).FontSize(13)
		})
		b.OnClick(a.openSettings)
		return
	}
	sel := ui.SelectBase(c.Key("model"), &a.settings.Current)
	sel.Trigger.Gap(4).Padding(5, 8).Radius(8).AlignItems(ui.Center).Shrink(1).MinWidth(56).TextColor(t.TextMuted).Label(locales.ComposerModel()).Tooltip(locales.ComposerModelTooltip())
	if sel.Trigger.Hovered() || sel.Open() {
		sel.Trigger.Background(t.SurfaceHover).TextColor(t.Text)
	}
	label := m.label()
	if cur := a.settings.model(a.settings.Current); cur != nil {
		label = cur.label()
	}
	sel.Trigger.Children(func() {
		ui.Text(c, label).FontSize(13).SingleLine().Shrink(1).MinWidth(0)
		ui.Icon(c, downIcon).Size(14, 14).Shrink(0)
	})
	sel.Popup(func(panel ui.Element) {
		menuPanel(c, panel).MinWidth(220)
		for _, mm := range a.settings.Models {
			item := sel.Item(mm.ID).Padding(6, 10).Radius(6).Gap(1)
			sub := t.TextMuted
			if item.Highlighted() {
				item.Background(t.Accent).TextColor(t.AccentText)
				sub = t.AccentText.Alpha(0.75)
			}
			item.Children(func() {
				ui.Text(c, mm.label()).FontSize(13).SingleLine()
				if mm.Model != "" && mm.Model != mm.label() {
					ui.Text(c, mm.Model).FontSize(11).TextColor(sub).SingleLine()
				}
			})
		}
	})
}

// menuPanel styles the panel of a drop-down.
func menuPanel(c *ui.Context, panel ui.Element) ui.Element {
	t := c.Theme()
	return panel.Margin(4, 0).Padding(4).Gap(1).Radius(10).Background(t.Background).Border(1, t.Border).
		Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.14))
}

type thinkLevel struct {
	effort     llm.Effort
	name, hint string
}

func thinkLevels() []thinkLevel {
	return []thinkLevel{
		{"", locales.ThinkOff(), locales.ThinkOffHint()},
		{llm.EffortLow, locales.ThinkLow(), locales.ThinkLowHint()},
		{llm.EffortMedium, locales.ThinkMedium(), locales.ThinkMediumHint()},
		{llm.EffortHigh, locales.ThinkHigh(), locales.ThinkHighHint()},
	}
}

func (a *app) thinkEffort() llm.Effort {
	switch e := a.settings.Effort; {
	case !a.settings.Thinking:
		return ""
	case e == llm.EffortLow || e == llm.EffortHigh:
		return e
	}
	return llm.EffortMedium
}

func (a *app) setThinkEffort(e llm.Effort) {
	a.settings.Thinking = e != ""
	if e != "" {
		a.settings.Effort = e
	}
}

func (a *app) thinkPicker(c *ui.Context, compact bool) {
	t := c.Theme()
	effort := a.thinkEffort()
	sel := ui.SelectBase(c.Key("thinking"), &effort)
	sel.Trigger.Gap(5).Padding(4, 8, 4, 10).Radius(999).AlignItems(ui.Center).
		Label(locales.ComposerThinking()).Tooltip(locales.ComposerThinkingTooltip())
	if effort != "" {
		sel.Trigger.Background(t.Accent.Alpha(0.12)).TextColor(t.Accent).Border(1, t.Accent.Alpha(0.35))
	} else {
		sel.Trigger.TextColor(t.TextMuted).Border(1, t.Border)
		if sel.Trigger.Hovered() || sel.Open() {
			sel.Trigger.Background(t.SurfaceHover)
		}
	}
	levels := thinkLevels()
	sel.Trigger.Shrink(0).Children(func() {
		ui.Icon(c, bulbIcon).Size(14, 14)
		if !compact {
			ui.Text(c, locales.ComposerThinking()).FontSize(13).SingleLine()
		}
		for _, l := range levels {
			if l.effort == effort && effort != "" {
				name := "· " + l.name
				if compact {
					name = l.name
				}
				ui.Text(c, name).FontSize(13).SingleLine()
			}
		}
		ui.Icon(c, downIcon).Size(12, 12)
	})
	sel.Trigger.OnChange(func() { a.setThinkEffort(effort) })
	sel.Popup(func(panel ui.Element) {
		menuPanel(c, panel).MinWidth(200)
		for _, l := range levels {
			item := sel.Item(l.effort).Column().AlignItems(ui.Start).Padding(6, 10).Radius(6).Gap(2)
			sub := t.TextMuted
			if item.Highlighted() {
				item.Background(t.Accent).TextColor(t.AccentText)
				sub = t.AccentText.Alpha(0.75)
			}
			item.Children(func() {
				ui.Text(c, l.name).FontSize(13).SingleLine()
				ui.Text(c, l.hint).FontSize(11).TextColor(sub).SingleLine()
			})
		}
	})
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
