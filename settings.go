package main

import (
	"context"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/Qsnh/Murmur/llm"
	"github.com/Qsnh/Murmur/locales"
)

// settingsView is the settings dialog: the models, what every chat sends
// them, and the preferences of Murmur.
func (a *app) settingsView(c *ui.Context) {
	t := c.Theme()
	ui.DialogBase(c.Key("settings"), &a.settingsOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		panel.Width(760).Height(560).MaxWidthPercent(94).MaxHeightPercent(92).Radius(14).Clip().
			Background(t.Background).Border(1, t.Border).Shadow(0, 24, 64, 0, ui.RGBA(0, 0, 0, 0.28))
		ui.Row(c).Gap(12).Padding(12, 12, 12, 20).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, locales.Settings()).FontSize(16).Bold()
			ui.Segmented(c.Key("tab"), &a.settingsTab, locales.SettingsModels(), locales.SettingsChat(), locales.SettingsGeneral()).Label(locales.SettingsSections())
			ui.Spacer(c)
			iconButton(c.Key("close"), closeIcon, locales.Close()).OnClick(func() { a.settingsOpen = false })
		})
		ui.Divider(c)
		switch a.settingsTab {
		case 0:
			a.modelSettings(c)
		case 1:
			a.chatSettings(c)
		default:
			a.generalSettings(c)
		}
	})
}

// modelSettings lists the models beside the one edited.
func (a *app) modelSettings(c *ui.Context) {
	t := c.Theme()
	if a.settings.model(a.editing) == nil && len(a.settings.Models) > 0 {
		a.editing = a.settings.Models[0].ID
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(210).Shrink(0).Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Padding(8).Gap(2).Children(func() {
					if len(a.settings.Models) == 0 {
						ui.Text(c, locales.ModelsEmpty()).FontSize(13).TextColor(t.TextMuted).Padding(6, 10)
					}
					for _, m := range a.settings.Models {
						row := ui.ButtonBase(c.Key(m.ID)).Gap(6).Padding(7, 10).Radius(7).AlignItems(ui.Center).Label(m.label())
						switch {
						case m.ID == a.editing:
							row.Background(t.Accent.Alpha(0.14))
						case row.Hovered():
							row.Background(t.SurfaceHover)
						}
						row.Children(func() {
							ui.Text(c, m.label()).FontSize(13).SingleLine().Grow(1).Shrink(1)
							if m.ID == a.settings.Current {
								ui.Text(c, locales.ModelDefault()).FontSize(11).TextColor(t.Accent).Padding(1, 6).Radius(999).Border(1, t.Accent.Alpha(0.4))
							}
						})
						row.OnClick(func() { a.editing, a.testResult, a.deleting = m.ID, "", "" })
					}
				})
			})
			ui.Box(c).Padding(8).Shrink(0).Children(func() {
				add := ui.Button(c.Key("add"), "").Gap(6).Children(func() {
					ui.Icon(c, plusIcon).Size(14, 14)
					ui.Text(c, locales.AddModel()).SingleLine()
				})
				add.Menu(func(menu *ui.Menu) {
					for _, p := range presets {
						if menu.Item(p.name()).Chosen() {
							a.addModel(p)
						}
					}
				})
			})
		})
		ui.Scroll(c).Grow(1).MinWidth(0).Children(func() {
			if m := a.settings.model(a.editing); m != nil {
				a.modelEditor(c, m)
			} else {
				ui.Column(c).Padding(40, 24).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, locales.ModelsHint()).TextColor(t.TextMuted)
				})
			}
		})
	})
}

// addModel adds a model made from p, and edits it.
func (a *app) addModel(p preset) {
	m := p.newModel()
	a.settings.Models = append(a.settings.Models, m)
	a.editing, a.testResult = m.ID, ""
	if a.settings.model(a.settings.Current) == nil {
		a.settings.Current = m.ID
	}
}

// deleteModel removes the model of the given ID.
func (a *app) deleteModel(id string) {
	for i, m := range a.settings.Models {
		if m.ID == id {
			a.settings.Models = append(a.settings.Models[:i], a.settings.Models[i+1:]...)
			break
		}
	}
	a.editing, a.testResult, a.deleting = "", "", ""
	if a.settings.model(a.settings.Current) == nil {
		a.settings.Current = ""
		if len(a.settings.Models) > 0 {
			a.settings.Current = a.settings.Models[0].ID
		}
	}
}

// modelEditor edits m.
func (a *app) modelEditor(c *ui.Context, m *Model) {
	t := c.Theme()
	k := func(field string) *ui.Context { return c.Key(m.ID + "/" + field) }
	ui.Column(c).Padding(20, 24, 24, 24).Gap(16).Children(func() {
		ui.Form(c, func() {
			ui.Field(c, locales.ModelName(), func() {
				ui.TextInput(k("name"), &m.Name).Placeholder(m.Model)
			})
			ui.Field(c, locales.ModelProtocol(), func() {
				choice(k("protocol"), &m.Protocol, protocols(), locales.ModelProtocol())
			}).Description(locales.ModelProtocolHint())
			base := "https://api.openai.com/v1"
			if m.Protocol == llm.Anthropic {
				base = "https://api.anthropic.com"
			}
			ui.Field(c, locales.ModelBaseURL(), func() {
				ui.TextInput(k("base"), &m.BaseURL).Placeholder(base)
			})
			ui.Field(c, "API Key", func() {
				ui.TextInput(k("key"), &m.APIKey).Password().Placeholder(locales.ModelAPIKeyPlaceholder())
			})
			ui.Field(c, locales.ModelID(), func() {
				ui.TextInput(k("model"), &m.Model).Placeholder(locales.ModelIDPlaceholder())
			})
			if m.Protocol != llm.Anthropic {
				desc := locales.ModelThinkStyleHint()
				if m.ThinkStyle == llm.ThinkAuto || m.ThinkStyle == "" {
					desc = locales.ModelThinkStyleDetected(styleLabel(llm.ResolveThinkStyle(llm.ThinkAuto, m.BaseURL)))
				}
				ui.Field(c, locales.ModelThinkStyle(), func() {
					choice(k("style"), &m.ThinkStyle, thinkStyles(), locales.ModelThinkStyle())
				}).Description(desc)
			} else {
				ui.Field(c, locales.ModelThinking(), func() {
					ui.Text(c, locales.ModelThinkingAnthropic()).FontSize(13).TextColor(t.TextMuted)
				})
			}
			ui.Field(c, locales.ModelMaxTokens(), func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					n := float64(m.MaxTokens)
					if ui.NumberInput(k("max"), &n, 0, 1_000_000, 1024).Width(160).Changed() {
						m.MaxTokens = int(n)
					}
					ui.Text(c, locales.ModelMaxTokensHint()).FontSize(13).TextColor(t.TextMuted)
				})
			})
		})

		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			testing := a.testing == m.ID
			test := ui.Button(k("test"), locales.ModelTest()).Disabled(testing || a.testing != "")
			test.OnClick(func() { a.test(m) })
			if m.ID != a.settings.Current {
				ui.Button(k("default"), locales.ModelSetDefault()).OnClick(func() { a.settings.Current = m.ID })
			}
			ui.Spacer(c)
			id := m.ID
			if a.deleting == id {
				ui.Button(k("cancel"), locales.Cancel()).OnClick(func() { a.deleting = "" })
				ui.Button(k("confirm"), locales.ModelConfirmDelete()).Background(t.Danger).TextColor(ui.RGB(255, 255, 255)).
					OnClick(func() { a.deleteModel(id) })
			} else {
				ui.Button(k("delete"), locales.Delete()).TextColor(t.Danger).OnClick(func() { a.deleting = id })
			}
		})
		switch {
		case a.testing == m.ID:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(14, 14)
				ui.Text(c, locales.ModelTesting()).FontSize(13).TextColor(t.TextMuted)
			})
		case a.testResult != "":
			color := t.Danger
			if a.testOK {
				color = t.Success
			}
			ui.Text(c, a.testResult).FontSize(13).TextColor(color).Selectable()
		}
	})
}

// test sends m a short message, and tells whether it answers.
func (a *app) test(m *Model) {
	a.testing, a.testResult = m.ID, ""
	id := m.ID
	cfg := m.config(a.settings.Proxy)
	ctx, cancel := context.WithCancel(context.Background())
	a.spawn(func() {
		answered := false
		err := a.stream(ctx, cfg, llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}, func(llm.Event) {
			answered = true
			cancel() // one token tells enough
		})
		cancel()
		a.update(func() {
			a.testing = ""
			if a.editing != id {
				return
			}
			switch {
			case answered || err == nil:
				a.testOK, a.testResult = true, locales.ModelTestSucceeded()
			default:
				a.testOK, a.testResult = false, locales.ModelTestFailed(err.Error())
			}
		})
	})
}

// themes are the appearances to choose from, in the order the settings
// show them.
var themes = []mygo.ThemeSource{mygo.ThemeSystem, mygo.ThemeLight, mygo.ThemeDark}

var languages = []string{"zh-Hans", "zh-Hant", "en"}

func languageOptions() []option[string] {
	options := []option[string]{{"", locales.LanguageSystem()}}
	for _, tag := range languages {
		options = append(options, option[string]{tag, i18n.Autonym(tag)})
	}
	return options
}

func (a *app) chatSettings(c *ui.Context) {
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(20, 24, 24, 24).Children(func() {
			ui.Form(c, func() {
				ui.Field(c, locales.ChatSystemPrompt(), func() {
					ui.TextArea(c.Key("system"), &a.settings.SystemPrompt).Lines(4, 12).Placeholder(locales.SystemPromptPlaceholder())
				})
				ui.Field(c, locales.ChatWebSearch(), func() { a.searchSettings(c) }).Description(locales.WebSearchHint())
			})
		})
	})
}

// generalSettings are the preferences of Murmur.
func (a *app) generalSettings(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(20, 24, 24, 24).Gap(16).Children(func() {
			ui.Form(c, func() {
				ui.Field(c, locales.GeneralAppearance(), func() {
					i := max(slices.Index(themes, a.settings.Theme), 0) // empty follows the system
					ui.Segmented(c.Key("theme"), &i, locales.ThemeSystem(), locales.ThemeLight(), locales.ThemeDark()).Label(locales.GeneralAppearance()).AlignSelf(ui.Start).
						OnChange(func() { a.setTheme(themes[i]) })
				})
				ui.Field(c, locales.GeneralLanguage(), func() {
					lang := a.settings.Language
					choice(c.Key("language"), &lang, languageOptions(), locales.GeneralLanguage()).
						OnChange(func() { a.setLanguage(lang) })
				})
				ui.Field(c, locales.GeneralNotifications(), func() {
					ui.Checkbox(c.Key("notify"), &a.settings.Notify, locales.NotifyOnReply())
				}).Description(locales.NotifyHint())
				ui.Field(c, locales.GeneralUpdates(), func() { a.updateSettings(c) }).Description(a.updatesDescription())
				ui.Field(c, locales.GeneralProxy(), func() {
					ui.TextInput(c.Key("proxy"), &a.settings.Proxy).Placeholder(locales.ProxyPlaceholder())
				}).Description(locales.ProxyHint())
			})
			where := a.store.dir
			if where == "" {
				where = locales.DataNotSaved()
			}
			ui.Column(c).Gap(4).Margin(8, 0, 0, 0).Children(func() {
				ui.Text(c, "Murmur "+version).FontSize(13).Bold()
				ui.Text(c, locales.DataLocation(where)).FontSize(12).TextColor(t.TextMuted).Selectable()
			})
		})
	})
}

// updateSettings turn the checks for updates and installing them without
// asking on and off, and check at once.
func (a *app) updateSettings(c *ui.Context) {
	t := c.Theme()
	u := a.updates
	enabled := u.Enabled()
	ui.Column(c).Gap(10).Children(func() {
		auto := u.AutomaticChecks()
		ui.Checkbox(c.Key("auto-check"), &auto, locales.UpdatesAutoCheck()).Disabled(!enabled).
			OnChange(func() { u.SetAutomaticChecks(auto) })
		install := u.AutomaticDownloads()
		ui.Checkbox(c.Key("auto-install"), &install, locales.UpdatesAutoInstall()).Disabled(!enabled || !auto).
			OnChange(func() { u.SetAutomaticDownloads(install) })
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Button(c.Key("check"), locales.UpdatesCheck()).OnClick(u.Check)
			if s := checkedLabel(u.LastCheck(), time.Now()); s != "" && enabled {
				ui.Text(c, s).FontSize(12).TextColor(t.TextMuted)
			}
		})
	})
}

// updatesDescription says how Murmur updates, or why it cannot.
func (a *app) updatesDescription() string {
	switch {
	case a.updates.Enabled():
		return locales.UpdatesHint()
	case mygo.IsDev():
		return locales.UpdatesDevBuild()
	}
	return locales.UpdatesUnwritable("Murmur")
}

// option is a choice of a drop-down.
type option[T comparable] struct {
	value T
	label string
}

// choice is a drop-down choosing one of options into v.
func choice[T comparable](c *ui.Context, v *T, options []option[T], label string) ui.Element {
	t := c.Theme()
	sel := ui.SelectBase(c, v)
	current := ""
	for _, o := range options {
		if o.value == *v {
			current = o.label
		}
	}
	if current == "" && len(options) > 0 {
		current = options[0].label
	}
	sel.Trigger.Gap(6).Padding(6, 10).Radius(t.Radius).Border(1, t.Border).Background(t.Background).AlignItems(ui.Center).Label(label)
	if sel.Trigger.Hovered() {
		sel.Trigger.Background(t.SurfaceHover)
	}
	sel.Trigger.Children(func() {
		ui.Text(c, current).FontSize(13).SingleLine().Grow(1).Shrink(1)
		ui.Icon(c, downIcon).Size(14, 14).TextColor(t.TextMuted)
	})
	sel.Popup(func(panel ui.Element) {
		menuPanel(c, panel)
		for _, o := range options {
			item := sel.Item(o.value).Padding(6, 10).Radius(6)
			if item.Highlighted() {
				item.Background(t.Accent).TextColor(t.AccentText)
			}
			item.Children(func() { ui.Text(c, o.label).FontSize(13) })
		}
	})
	return sel.Trigger
}

// styleLabel names a thinking style.
func styleLabel(s llm.ThinkStyle) string {
	for _, o := range thinkStyles() {
		if o.value == s {
			return o.label
		}
	}
	return string(s)
}
