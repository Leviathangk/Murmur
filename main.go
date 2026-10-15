// Murmur is a minimal AI chat for macOS and Windows, in native UI drawn by
// MyGo: models of any provider that speaks OpenAI's or Anthropic's API,
// replies streamed as Markdown, and the model's thinking shown as it
// thinks.
package main

import (
	_ "embed"
	"encoding/json"
	"log"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/locales/zh-Hans"
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
	// The interface is in Chinese: MyGo's own controls and menus say
	// theirs in Chinese too.
	mygo.App.SetLanguages("zh-Hans")
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
		if onReady != nil {
			onReady(a)
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
