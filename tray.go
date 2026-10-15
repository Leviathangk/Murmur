package main

import (
	_ "embed"
	"log"
	"runtime"

	"github.com/egoist/mygo"
)

// Murmur keeps running when its window is closed, until it is quit: closing
// the window hides it, and an icon in the menu bar (macOS) or the
// notification area (Windows) opens it again or quits.

// trayMacOS is the logo's bars, black on transparent, for macOS to tint like
// the rest of the menu bar.
//
//go:embed resources/tray-macos.png
var trayMacOS []byte

// trayWindows is the app's icon, which shows on light and dark taskbars.
//
//go:embed resources/tray-windows.png
var trayWindows []byte

// trayMenu is the menu of the tray icon: open the window, check for
// updates, or quit.
func trayMenu(open, check func()) []*mygo.MenuItem {
	return []*mygo.MenuItem{
		{Label: "打开 Murmur", Click: func(*mygo.MenuItem, *mygo.Window) { open() }},
		checkMenuItem(check),
		mygo.Separator(),
		{Label: "退出 Murmur", Role: mygo.RoleQuit},
	}
}

// stayInBackground puts the app's icon in the tray and keeps the app
// running when the window is closed. Without a tray, closing the window
// quits as before, as nothing could open it again.
func (a *app) stayInBackground() {
	menu := mygo.NewMenu(trayMenu(a.showWindow, a.updates.Check))
	opts := mygo.TrayOptions{Icon: trayWindows, ToolTip: "Murmur", Menu: menu}
	if runtime.GOOS == "darwin" {
		opts.Icon, opts.IconIsTemplate = trayMacOS, true
	}
	// On Windows, as icons there do, a click opens the window and a right
	// click shows the menu; elsewhere a click shows the menu.
	windows := runtime.GOOS == "windows"
	if windows {
		opts.Menu = nil
	}
	tray, err := mygo.NewTray(opts)
	if err != nil {
		log.Printf("no tray icon: %v", err)
		return
	}
	if windows {
		tray.OnClick(a.showWindow)
		tray.OnRightClick(func() {
			tray.SetMenu(menu)
			tray.PopUpMenu()
			tray.SetMenu(nil)
		})
	}

	quitting := false
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { quitting = true })
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if quitting {
			return // quitting closes the window for good
		}
		e.PreventDefault()
		a.hideWindow()
	})
	// A click on the Dock icon (macOS) opens the window again, and so does
	// starting the app again.
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			a.showWindow()
		}
	})
	mygo.App.OnSecondInstance(func([]string, string) { a.showWindow() })
}

// hideWindow hides the window, leaving the app running.
func (a *app) hideWindow() {
	a.stopHiding()
	if runtime.GOOS == "darwin" && a.win.IsFullScreen() {
		// Hidden in full screen, the window would leave its Space behind,
		// empty: it leaves full screen first.
		a.hiding = a.win.OnLeaveFullScreen(func() {
			a.stopHiding()
			a.win.Hide()
		})
		a.win.SetFullScreen(false)
		return
	}
	a.win.Hide()
}

// stopHiding keeps the window that is leaving full screen from hiding then.
func (a *app) stopHiding() {
	if a.hiding != nil {
		a.hiding()
		a.hiding = nil
	}
}

// showWindow brings the window back to the front, as it was.
func (a *app) showWindow() {
	a.stopHiding()
	if a.win.IsMinimized() {
		a.win.Restore()
	}
	a.win.Show()
	mygo.App.Focus()
}
