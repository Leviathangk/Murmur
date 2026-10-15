package main

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"

	"github.com/Qsnh/Murmur/llm"
)

// Murmur updates itself from its releases on GitHub, as the updates of
// mygo.json say: the release workflow signs the app of each platform, and
// the app installs only what the public key there verifies. MyGo's update
// window, in native UI, checks once a day; when a new version is out it
// tells what is new, from CHANGELOG.md, then downloads and installs it, and
// the app restarts into it, or the new version runs the next time Murmur
// starts. The settings turn the checks off, or check at once, as the
// tray's menu and the app menu of macOS do.

// updates checks for updates, and keeps the user's choices about them:
// MyGo's update window in the app; tests replace it.
type updates interface {
	// Enabled reports whether the app can update itself: it is a release
	// build, installed where it can write.
	Enabled() bool
	// Check checks at once, in the update window, which tells the user
	// when the app is up to date.
	Check()
	AutomaticChecks() bool
	SetAutomaticChecks(on bool)
	// AutomaticDownloads reports whether the updates found are installed
	// without asking, to run the next time Murmur starts.
	AutomaticDownloads() bool
	SetAutomaticDownloads(on bool)
	// LastCheck is when the app last checked successfully, zero if never.
	LastCheck() time.Time
}

// useUpdates sets up the update window before the app runs, once, as
// mygo.Use takes a plugin once, and returns it: it checks in the
// background once the app is ready, in Chinese as the app speaks, through
// proxy, the proxy of the settings.
func useUpdates(proxy string) updates {
	setUpUpdates.Do(func() {
		mygo.Use(native.New(updater.Options{Language: "zh-Hans", Strings: updateStrings}))
		go updatesEnabled() // off the main thread, which the settings ask on
	})
	proxyUpdates()
	setUpdateProxy(proxy)
	return updateWindow{}
}

var setUpUpdates sync.Once

// updateStrings are the texts of the update window that Murmur says its
// own way: an app that cannot write where it is was opened from the disk
// image or the Downloads folder, or by a user who is not an administrator,
// rather than installed by a package manager.
var updateStrings = map[string]updater.Strings{
	"zh-Hans": {UnavailableMessage: "%[1]s 没有权限写入它所在的文件夹，无法自动更新。在 macOS 上，请把 %[1]s 拖进「应用程序」文件夹，再从那里打开；已经在里面的话，可能是当前账户不是管理员，请到 GitHub 下载新版本手动安装。"},
}

// updateWindow is the update window of useUpdates.
type updateWindow struct{}

// updatesEnabled is mygo.Updater.Enabled, which tries to write next to the
// app: it is asked once, not at every frame of the settings.
var updatesEnabled = sync.OnceValue(mygo.Updater.Enabled)

func (updateWindow) Enabled() bool                 { return updatesEnabled() }
func (updateWindow) Check()                        { updater.CheckForUpdates() }
func (updateWindow) AutomaticChecks() bool         { return updater.AutomaticChecks() }
func (updateWindow) SetAutomaticChecks(on bool)    { updater.SetAutomaticChecks(on) }
func (updateWindow) AutomaticDownloads() bool      { return updater.AutomaticDownloads() }
func (updateWindow) SetAutomaticDownloads(on bool) { updater.SetAutomaticDownloads(on) }
func (updateWindow) LastCheck() time.Time          { return updater.LastCheck() }

// updateProxy is the proxy of the settings, which updates are checked and
// downloaded through, as the models are asked.
var updateProxy atomic.Pointer[string]

// setUpdateProxy sends the updates through proxy from now on.
func setUpdateProxy(proxy string) { updateProxy.Store(&proxy) }

// proxyUpdates sends the requests of mygo.Updater, which go through
// http.DefaultClient, to updateProxy.
func proxyUpdates() {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = proxyFor
	http.DefaultClient.Transport = tr
}

// proxyFor returns the proxy of the settings, or without one the
// environment's.
func proxyFor(r *http.Request) (*url.URL, error) {
	if p := updateProxy.Load(); p != nil && strings.TrimSpace(*p) != "" {
		return llm.ProxyURL(*p)
	}
	return http.ProxyFromEnvironment(r)
}

// checkedLabel says when updates were last checked for, "" if never.
func checkedLabel(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.In(now.Location())
	day := func(t time.Time) time.Time {
		y, m, d := t.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	}
	switch today := day(now); {
	case day(t).Equal(today):
		return "上次检查：今天 " + t.Format("15:04")
	case day(t).Equal(today.AddDate(0, 0, -1)):
		return "上次检查：昨天 " + t.Format("15:04")
	case t.Year() == now.Year():
		return "上次检查：" + t.Format("1 月 2 日 15:04")
	default:
		return "上次检查：" + t.Format("2006 年 1 月 2 日")
	}
}

// checkMenuItem is the menu item that checks for updates at once.
func checkMenuItem(check func()) *mygo.MenuItem {
	return &mygo.MenuItem{Label: "检查更新…", Click: func(*mygo.MenuItem, *mygo.Window) { check() }}
}

// appMenu is the menu bar of macOS: MyGo's default one, with an item that
// checks for updates after About, where Mac apps have it.
func appMenu(check func()) []*mygo.MenuItem {
	return []*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			checkMenuItem(check),
			mygo.Separator(),
			{Role: mygo.RoleServices},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Role: mygo.RoleFileMenu},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleViewMenu},
		{Role: mygo.RoleWindowMenu},
	}
}
