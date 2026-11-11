// AnyPic is a cross-platform image compressor with a MyGo web frontend and
// a pure-Go compression pipeline.
package main

import (
	_ "embed"
	"log"
	"runtime"

	"github.com/egoist/mygo"
)

//go:embed resources/tray.png
var trayIcon []byte

func main() {
	app := mygo.App
	app.SetName("AnyPic")

	// A second launch — an image opened from the file manager while AnyPic
	// sits in the tray — hands its paths to the running instance instead of
	// starting a second app.
	if !app.RequestSingleInstanceLock() {
		return
	}

	// The languages the app is written in (#32); MyGo picks the one to show
	// among them, and the page, the menus MyGo labels itself and the Go side
	// all read that one answer (i18n.go).
	app.SetLanguages(appLanguages...)

	service := NewAnyPic()
	mygo.Bind(service)

	// Set by a quit started from the tray, Cmd+Q or the app menu, so the
	// window's close handler lets that close through instead of hiding.
	var quitting bool
	app.OnBeforeQuit(func(*mygo.QuitEvent) {
		quitting = true
		service.Cleanup()
	})

	// Registered before Run so launch-time opens (double-clicked files, Dock
	// drops, command lines) reach the app; showWindow is nil until the
	// window exists, and a cold start opens it anyway.
	var showWindow func()
	app.OnOpenFile(func(path string) {
		service.OpenFile(path)
		if showWindow != nil {
			showWindow()
		}
	})

	// A second launch that carries no image of its own — the user started
	// AnyPic again from the shortcut while it sat in the tray — reaches the
	// app here and nowhere else (#14): bring the running window back instead
	// of doing nothing.
	app.OnSecondInstance(secondLaunchHandler(&showWindow))

	app.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:                "AnyPic",
			Width:                1220,
			Height:               820,
			MinWidth:             940,
			MinHeight:            640,
			BackgroundColor:      "light-dark(#f6f6f7, #111113)",
			TitleBarStyle:        mygo.TitleBarHidden,
			TitleBarHeight:       48,
			TrafficLightPosition: &mygo.Point{X: 18, Y: 17},
			StateKey:             "main",
			URL:                  "/",
		})
		_ = win

		showWindow = func() {
			if win.IsMinimized() {
				win.Restore()
			}
			// The Dock icon is removed while the app sits in the tray; bring
			// it back before the window so the app is a regular app again.
			if runtime.GOOS == "darwin" {
				app.Dock.Show()
			}
			win.Show()
			win.Focus()
		}

		// Closing the window keeps AnyPic in the tray, so the screenshot
		// shortcut keeps working; quitting goes through the tray or Cmd+Q.
		// Without a tray there would be no way back to a hidden window, so
		// closing then quits as usual.
		var trayReady bool
		win.OnClose(func(e *mygo.CloseEvent) {
			if quitting || !trayReady {
				return
			}
			e.PreventDefault()
			win.Hide()
			// With the window in the tray, the app can drop out of the Dock
			// and app switcher entirely; the tray icon remains the way back.
			if runtime.GOOS == "darwin" && service.HideDockWhenTray() {
				app.Dock.Hide()
			}
		})
		if runtime.GOOS == "darwin" {
			// A Dock click reopens the window; on other platforms activation
			// can come from the tray itself, which should not pop it open.
			app.OnActivate(func(hasVisibleWindows bool) {
				if !hasVisibleWindows {
					showWindow()
				}
			})
		}

		if err := service.RegisterScreenshotShortcut(); err != nil {
			log.Printf("AnyPic: could not register the screenshot shortcut: %v", err)
		}

		// The menus say their labels in the language the app shows (#32);
		// resolveLanguageNow reads the desktop's languages now that the app is
		// ready, and OnLocaleChanged re-reads them when they change while the
		// app follows the system. A change from the preferences rebuilds both
		// menus through the same listener.
		app.OnLocaleChanged(func(string) { service.followDesktopLanguage() })
		service.resolveLanguageNow()
		t := tr()

		tray, trayErr := mygo.NewTray(mygo.TrayOptions{
			ID:             "anypic",
			Icon:           trayIcon,
			IconIsTemplate: true,
			ToolTip:        t.GoTrayTooltip(),
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{Label: t.GoMenuOpenApp(), Click: func(*mygo.MenuItem, *mygo.Window) { showWindow() }},
				{Label: t.GoMenuCapture(), Click: command("screenshot")},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			}),
		})
		if trayErr != nil {
			log.Printf("AnyPic: could not create the tray icon: %v", trayErr)
		} else {
			trayReady = true
		}

		buildMenus := func() {
			t := tr()
			app.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
				{Role: mygo.RoleAppMenu},
				{Label: t.GoMenuFile(), Submenu: []*mygo.MenuItem{
					{Label: t.GoMenuOpenImages(), Accelerator: "CmdOrCtrl+O", Click: command("open")},
					{Label: t.GoMenuCaptureEllipsis(), Click: command("screenshot")},
					{Label: t.GoMenuStartCompression(), Accelerator: "CmdOrCtrl+Return", Click: command("compress")},
					{Label: t.GoMenuExportAll(), Accelerator: "CmdOrCtrl+E", Click: command("export")},
					mygo.Separator(),
					{Label: t.GoMenuClearList(), Accelerator: "CmdOrCtrl+Backspace", Click: command("clear")},
					mygo.Separator(),
					{Role: mygo.RoleClose},
				}},
				{Role: mygo.RoleEditMenu},
				{Label: t.GoMenuView(), Submenu: []*mygo.MenuItem{
					{Label: t.GoMenuPreferences(), Accelerator: "CmdOrCtrl+,", Click: command("prefs")},
					mygo.Separator(),
					{Label: t.GoMenuToggleTheme(), Accelerator: "CmdOrCtrl+Shift+L", Click: command("theme")},
					mygo.Separator(),
					{Role: mygo.RoleToggleFullScreen},
				}},
				{Role: mygo.RoleWindowMenu},
				{Role: mygo.RoleHelp},
			}))
			if tray != nil {
				tray.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
					{Label: t.GoMenuOpenApp(), Click: func(*mygo.MenuItem, *mygo.Window) { showWindow() }},
					{Label: t.GoMenuCapture(), Click: command("screenshot")},
					mygo.Separator(),
					{Role: mygo.RoleQuit},
				}))
				tray.SetToolTip(t.GoTrayTooltip())
			}
		}
		buildMenus()
		service.setLanguageListener(buildMenus)
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// secondLaunchHandler is what the running AnyPic does when it is started
// again (#14): bring its window back. The second process gives up at the
// single-instance lock and exits; on Windows and Linux this callback is the
// only thing the user's launch reaches unless it carries files, so without
// it starting AnyPic again while it sits in the tray did nothing at all.
// show points at main's showWindow, which is nil until the window exists.
func secondLaunchHandler(show *func()) func(args []string, workingDir string) {
	return func([]string, string) {
		if *show != nil {
			(*show)()
		}
	}
}

func command(name string) func(*mygo.MenuItem, *mygo.Window) {
	return func(*mygo.MenuItem, *mygo.Window) {
		_ = CommandRequested.Broadcast(name)
	}
}
