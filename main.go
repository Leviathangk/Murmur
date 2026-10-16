// Murmur is a minimal AI chat for macOS and Windows, in native UI drawn by
// MyGo: models of any provider that speaks OpenAI's or Anthropic's API,
// replies streamed as Markdown, and the model's thinking shown as it
// thinks.
package main

import (
	_ "embed"
	"encoding/json"
	"log"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"
)

// mygoJSON is the app's configuration, whose version is the app's: the
// release workflow sets it from the tag, as the packages are named after it.
//
//go:embed mygo.json
var mygoJSON []byte

var version = func() string {
	var c struct{ Version string }
	json.Unmarshal(mygoJSON, &c)
	return c.Version
}()

// onReady, when set, runs once the window shows, as the demo build drives
// the app (demo.go).
var onReady func(a *app)

func main() {
	mygo.App.SetLanguages("en", "zh-Hans", "zh-Hant")
	// Murmur runs once: started again, as it stays running in the
	// background, the running one shows its window (tray.go).
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Printf("no place for the data: %v", err)
	}
	a := newApp(store{dir: dir})
	// The app starts in the appearance chosen last.
	mygo.Theme.SetSource(a.settings.Theme)
	if a.settings.Language != "" {
		mygo.App.SetLocale(a.settings.Language)
	}
	// A click on a notification shows its chat, also the click that
	// launched the app (macOS), which comes before the window (notify.go).
	mygo.App.OnNotificationClick(a.openNotified)
	// Murmur checks for new versions and installs them (update.go); the
	// app menu of macOS checks at once too.
	a.updates = useUpdates(a.settings.Proxy)
	if runtime.GOOS == "darwin" {
		setAppMenu := func() { mygo.App.SetMenu(mygo.NewMenu(appMenu(a.updates.Check))) }
		setAppMenu()
		mygo.App.OnLocaleChanged(func(string) { setAppMenu() })
	}
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "Murmur",
			Width:     1080,
			Height:    720,
			MinWidth:  640,
			MinHeight: 480,
			StateKey:  "main",
			// The view draws the title bar, under the window controls:
			// the traffic lights inset over the sidebar on macOS, the
			// buttons of Windows 11 at the top right.
			TitleBarStyle:  mygo.TitleBarHiddenInset,
			TitleBarHeight: titleBarHeight,
			Vibrancy:       mygo.VibrancySidebar,
			Content:        ui.View(a.view),
		})
		a.stayInBackground()
		// The settings show the choices made in the update window.
		updater.OnChange(a.win.Invalidate)
		// The window brought to the front shows what the notifications
		// told: they do not wait in Notification Center.
		a.win.OnFocus(mygo.ClearNotifications)
		if onReady != nil {
			onReady(a)
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
