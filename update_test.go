package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// fakeUpdates keeps the choices of updates as the update window does, and
// counts the checks asked for.
type fakeUpdates struct {
	enabled, automaticChecks, automaticDownloads bool
	lastCheck                                    time.Time
	checks                                       int
}

func (f *fakeUpdates) Enabled() bool                 { return f.enabled }
func (f *fakeUpdates) Check()                        { f.checks++ }
func (f *fakeUpdates) AutomaticChecks() bool         { return f.automaticChecks }
func (f *fakeUpdates) SetAutomaticChecks(on bool)    { f.automaticChecks = on }
func (f *fakeUpdates) AutomaticDownloads() bool      { return f.automaticDownloads }
func (f *fakeUpdates) SetAutomaticDownloads(on bool) { f.automaticDownloads = on }
func (f *fakeUpdates) LastCheck() time.Time          { return f.lastCheck }

// generalSettings opens the general settings of a.
func generalSettings(t *testing.T, a *app) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(a.view, 1080, 720)
	a.openSettings()
	tt.Frame()
	if err := tt.Click("通用"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	return tt
}

func TestUpdateSettings(t *testing.T) {
	a, _ := testApp(t)
	u := &fakeUpdates{enabled: true, automaticChecks: true, lastCheck: time.Now().Add(-time.Minute)}
	a.updates = u
	tt := generalSettings(t, a)
	if !tt.HasText("自动检查更新") || !tt.HasText(checkedLabel(u.lastCheck, time.Now())) ||
		!tt.HasText("每天检查一次，有新版本时告诉你更新了什么。自动安装的新版本在 Murmur 重新启动后生效") {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "settings-updates")

	if err := tt.Click("检查更新"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if u.checks != 1 {
		t.Errorf("%d checks after a click", u.checks)
	}

	if err := tt.Click("自动下载并安装新版本"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !u.automaticDownloads {
		t.Fatal("automatic downloads still off after a click")
	}

	// Without checks, nothing is found to install.
	if err := tt.Click("自动检查更新"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if u.automaticChecks {
		t.Fatal("automatic checks still on after a click")
	}
	if err := tt.Click("自动下载并安装新版本"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !u.automaticDownloads {
		t.Error("automatic downloads turned off while checks are off")
	}

	// What the update window changes shows.
	u.automaticChecks = true
	tt.Frame()
	if err := tt.Click("自动下载并安装新版本"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if u.automaticDownloads {
		t.Error("automatic downloads still on after a click")
	}
}

func TestUpdatesUnavailable(t *testing.T) {
	a, _ := testApp(t)
	u := &fakeUpdates{automaticChecks: true, lastCheck: time.Now()}
	a.updates = u
	tt := generalSettings(t, a)
	// Tests run as development builds do.
	if !tt.HasText("开发版本不会自动更新") || tt.HasText(checkedLabel(u.lastCheck, time.Now())) {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "settings-updates-unavailable")
	if err := tt.Click("自动检查更新"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !u.automaticChecks {
		t.Error("automatic checks turned off in a build that cannot update")
	}
	// The update window tells why, when asked.
	if err := tt.Click("检查更新"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if u.checks != 1 {
		t.Errorf("%d checks after a click", u.checks)
	}
}

func TestCheckedLabel(t *testing.T) {
	now := time.Date(2026, 10, 15, 9, 30, 0, 0, time.Local)
	for _, tc := range []struct {
		t    time.Time
		want string
	}{
		{time.Time{}, ""},
		{now.Add(-time.Hour), "上次检查：今天 08:30"},
		{time.Date(2026, 10, 14, 23, 5, 0, 0, time.Local), "上次检查：昨天 23:05"},
		{time.Date(2026, 3, 2, 14, 0, 0, 0, time.Local), "上次检查：3月2日 14:00"},
		{time.Date(2025, 12, 31, 14, 0, 0, 0, time.Local), "上次检查：2025年12月31日"},
	} {
		if got := checkedLabel(tc.t, now); got != tc.want {
			t.Errorf("checkedLabel(%v) = %q, want %q", tc.t, got, tc.want)
		}
	}
	t.Setenv("MYGO_LOCALE", "en")
	for _, tc := range []struct {
		t    time.Time
		want string
	}{
		{now.Add(-time.Hour), "Last checked today at 8:30 AM"},
		{time.Date(2026, 3, 2, 14, 0, 0, 0, time.Local), "Last checked Mar 2 at 2:00 PM"},
		{time.Date(2025, 12, 31, 14, 0, 0, 0, time.Local), "Last checked Dec 31, 2025"},
	} {
		if got := checkedLabel(tc.t, now); got != tc.want {
			t.Errorf("checkedLabel(%v) = %q, want %q", tc.t, got, tc.want)
		}
	}
}

func TestUpdateProxy(t *testing.T) {
	defer updateProxy.Store(nil)
	a, _ := testApp(t)
	a.settings.Proxy = "127.0.0.1:7890"
	a.autosave()
	req, _ := http.NewRequest("GET", "https://github.com/Qsnh/Murmur/releases/latest/download/update-darwin-universal.json", nil)
	if u, err := proxyFor(req); err != nil || u == nil || u.String() != "http://127.0.0.1:7890" {
		t.Fatalf("proxy %v, %v", u, err)
	}
	a.settings.Proxy = "socks5://127.0.0.1:1080"
	a.autosave()
	if u, err := proxyFor(req); err != nil || u == nil || u.String() != "socks5://127.0.0.1:1080" {
		t.Errorf("proxy %v, %v", u, err)
	}
	a.settings.Proxy = "http://"
	a.autosave()
	if _, err := proxyFor(req); err == nil {
		t.Error("no error for a proxy without a host")
	}
	// Without one, the environment's.
	a.settings.Proxy = ""
	a.autosave()
	want, _ := http.ProxyFromEnvironment(req)
	if u, err := proxyFor(req); err != nil || fmt.Sprint(u) != fmt.Sprint(want) {
		t.Errorf("proxy %v, %v, want the environment's %v", u, err, want)
	}
}

func TestAppMenu(t *testing.T) {
	checked := false
	menu := appMenu(func() { checked = true })
	app := menu[0]
	if app.Role != mygo.RoleAppMenu || len(app.Submenu) < 2 || app.Submenu[0].Role != mygo.RoleAbout {
		t.Fatalf("app menu %+v", app)
	}
	item := app.Submenu[1]
	if item.Label != "检查更新…" {
		t.Fatalf("after About %+v", item)
	}
	item.Click(item, nil)
	if !checked {
		t.Error("no check")
	}
	if last := app.Submenu[len(app.Submenu)-1]; last.Role != mygo.RoleQuit {
		t.Errorf("last item %+v, want quit", last)
	}
}

// The releases on GitHub hold the updates, which the key of mygo.json
// verifies.
func TestUpdateConfig(t *testing.T) {
	b, err := os.ReadFile("mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Updates struct{ PublicKey, GitHub string }
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if key, err := base64.StdEncoding.DecodeString(c.Updates.PublicKey); err != nil || len(key) != 32 {
		t.Errorf("public key %q: %d bytes, %v", c.Updates.PublicKey, len(key), err)
	}
	if c.Updates.GitHub != "Qsnh/Murmur" {
		t.Errorf("updates from %q", c.Updates.GitHub)
	}
}

// The update window tells what is new in a version from its section of
// CHANGELOG.md, which mygo build requires of the versions it signs: the
// release workflow, which sets the version from the tag, fails here
// before it builds.
func TestChangelog(t *testing.T) {
	if strings.Contains(version, "-") {
		t.Skip("prereleases are not offered as updates")
	}
	b, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	in := false
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if rest, ok := strings.CutPrefix(line, "## "); ok {
			if in {
				break
			}
			// "## 0.7.0", or "## [0.7.0] - 2026-10-15" as mygo build takes it.
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				continue
			}
			v := fields[0]
			if inner, ok := strings.CutPrefix(v, "["); ok {
				v, _, _ = strings.Cut(inner, "]")
			}
			in = strings.TrimPrefix(v, "v") == version
			continue
		}
		if in && strings.TrimSpace(line) != "" {
			notes = append(notes, line)
		}
	}
	if len(notes) == 0 {
		t.Errorf("CHANGELOG.md tells nothing of %s: add a \"## %s\" section", version, version)
	}
}

// The update window is set up as the app sets it up, which panics on texts
// that are not formats of theirs, and the updates go through the proxy of
// the settings.
func TestUseUpdates(t *testing.T) {
	transport := http.DefaultClient.Transport
	t.Cleanup(func() {
		http.DefaultClient.Transport = transport
		updateProxy.Store(nil)
		mygo.App.SetPath(mygo.PathUserData, "")
	})
	// The update window keeps the user's choices with the settings.
	mygo.App.SetPath(mygo.PathUserData, t.TempDir())
	u := useUpdates("127.0.0.1:7890")
	tr, ok := http.DefaultClient.Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		t.Fatalf("transport %#v", http.DefaultClient.Transport)
	}
	req, _ := http.NewRequest("GET", "https://github.com/", nil)
	if p, err := tr.Proxy(req); err != nil || p.String() != "http://127.0.0.1:7890" {
		t.Errorf("proxy %v, %v", p, err)
	}
	if u.Enabled() {
		t.Error("a test can update itself")
	}
	if !u.AutomaticChecks() {
		t.Error("no automatic checks at first")
	}
}
