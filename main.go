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
			log.Printf("AnyPic: 截图快捷键注册失败: %v", err)
		}

		if _, err := mygo.NewTray(mygo.TrayOptions{
			ID:             "anypic",
			Icon:           trayIcon,
			IconIsTemplate: true,
			ToolTip:        "AnyPic · 图片压缩",
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{Label: "打开 AnyPic", Click: func(*mygo.MenuItem, *mygo.Window) { showWindow() }},
				{Label: "截取屏幕区域", Click: command("screenshot")},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			}),
		}); err != nil {
			log.Printf("AnyPic: 无法创建托盘图标: %v", err)
		} else {
			trayReady = true
		}

		app.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Label: "文件", Submenu: []*mygo.MenuItem{
				{Label: "打开图片…", Accelerator: "CmdOrCtrl+O", Click: command("open")},
				{Label: "截取屏幕区域…", Click: command("screenshot")},
				{Label: "开始压缩", Accelerator: "CmdOrCtrl+Return", Click: command("compress")},
				{Label: "导出全部…", Accelerator: "CmdOrCtrl+E", Click: command("export")},
				mygo.Separator(),
				{Label: "清空列表", Accelerator: "CmdOrCtrl+Backspace", Click: command("clear")},
				mygo.Separator(),
				{Role: mygo.RoleClose},
			}},
			{Role: mygo.RoleEditMenu},
			{Label: "视图", Submenu: []*mygo.MenuItem{
				{Label: "切换主题", Accelerator: "CmdOrCtrl+Shift+L", Click: command("theme")},
				mygo.Separator(),
				{Role: mygo.RoleToggleFullScreen},
			}},
			{Role: mygo.RoleWindowMenu},
			{Role: mygo.RoleHelp},
		}))
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func command(name string) func(*mygo.MenuItem, *mygo.Window) {
	return func(*mygo.MenuItem, *mygo.Window) {
		_ = CommandRequested.Broadcast(name)
	}
}
