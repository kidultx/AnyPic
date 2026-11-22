//go:build windows

package main

// AnyPic's own Windows region selector: one borderless overlay window per
// monitor, each showing a frozen GDI capture of that monitor, with a shared
// selection in virtual-screen physical pixels.
//
// The GDI screen capture is adapted from github.com/kbinani/screenshot's
// windows.go (MIT, Copyright (c) 2016 kbinani; see
// LICENSE-kbinani-screenshot.txt). It differs in four ways:
//
//   - BitBlt uses SRCCOPY|CAPTUREBLT, so layered windows, tooltips and
//     other third-party overlays are not left out of the frozen image
//     (kbinani's windows.go uses SRCCOPY alone).
//   - The capture is the whole virtual desktop, not one monitor, so a
//     selection can start on any display without a second capture.
//   - Every alpha byte is forced to 255. GetDIBits leaves 32bpp alpha
//     undefined (usually 0), which would make the image transparent in
//     MyGo's premultiplied pipeline.
//   - CreateDIBSection hands back the pixel pointer directly instead of
//     copying through GetDIBits into a GlobalAlloc block.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var (
	procUser32 = syscall.NewLazyDLL("user32.dll")
	procGDI32  = syscall.NewLazyDLL("gdi32.dll")
	procShcore = syscall.NewLazyDLL("shcore.dll")
	procDwmapi = syscall.NewLazyDLL("dwmapi.dll")

	procGetDC                 = procUser32.NewProc("GetDC")
	procReleaseDC             = procUser32.NewProc("ReleaseDC")
	procGetSystemMetrics      = procUser32.NewProc("GetSystemMetrics")
	procEnumDisplayMonitors   = procUser32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW       = procUser32.NewProc("GetMonitorInfoW")
	procGetCursorPos          = procUser32.NewProc("GetCursorPos")
	procGetForegroundWindow   = procUser32.NewProc("GetForegroundWindow")
	procSetForegroundWindow   = procUser32.NewProc("SetForegroundWindow")
	procIsWindow              = procUser32.NewProc("IsWindow")
	procGetWindowLongW        = procUser32.NewProc("GetWindowLongW")
	procSetWindowLongW        = procUser32.NewProc("SetWindowLongW")
	procOverlaySetWindowPos   = procUser32.NewProc("SetWindowPos")
	procGetDpiForMonitor      = procShcore.NewProc("GetDpiForMonitor")
	procCreateCompatibleDC    = procGDI32.NewProc("CreateCompatibleDC")
	procCreateDIBSection      = procGDI32.NewProc("CreateDIBSection")
	procSelectObject          = procGDI32.NewProc("SelectObject")
	procDeleteObject          = procGDI32.NewProc("DeleteObject")
	procDeleteDC              = procGDI32.NewProc("DeleteDC")
	procBitBlt                = procGDI32.NewProc("BitBlt")
	procDwmSetWindowAttribute = procDwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	srcCopy    = 0x00CC0020
	captureBLT = 0x40000000

	dibRGBColors = 0
	biRGB        = 0

	gwlStyle       = ^uintptr(15) // -16
	gwlExStyle     = ^uintptr(19) // -20
	wsPopup        = 0x80000000
	wsCaption      = 0x00C00000
	wsThickFrame   = 0x00040000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsMaximizeBox  = 0x00010000
	wsClipChildren = 0x02000000
	wsExToolWindow = 0x00000080

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020

	hwndTopmost = ^uintptr(0) // -1

	monitorInfoPrimary = 0x00000001

	dwmwaTransitionsForcedisabled = 3
	dwmwaWindowCornerPreference   = 33
	dwmwaBorderColor              = 34
	dwmcpDoNotRound               = 1
	dwmwaColorNone                = 0xFFFFFFFE
)

type winRect struct {
	Left, Top, Right, Bottom int32
}

type winPoint struct {
	X, Y int32
}

// monitorInfoEx matches MONITORINFOEXW (104 bytes); Size must be set before
// GetMonitorInfoW.
type monitorInfoEx struct {
	Size    uint32
	Monitor winRect
	Work    winRect
	Flags   uint32
	Device  [32]uint16
}

type winBitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

func overlayPlatformSupported() bool { return true }

// captureOverlayRegion freezes the virtual desktop, shows one overlay per
// monitor and returns the selected region as a PNG. A cancel returns
// errCaptureCanceled; a black capture returns errOverlayBlack so the caller
// can fall back to the system screenshot tool.
func captureOverlayRegion(ctx context.Context) ([]byte, error) {
	img, bounds, err := captureVirtualDesktop()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errOverlayFailed, err)
	}
	if looksBlack(img) {
		return nil, errOverlayBlack
	}
	displays, err := physicalDisplays()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errOverlayFailed, err)
	}
	// A monitor can only show what the capture contains. In practice the
	// virtual screen is their bounding box, so this keeps them all.
	kept := displays[:0]
	for _, d := range displays {
		if d.rect.In(bounds) {
			kept = append(kept, d)
		}
	}
	displays = kept
	if len(displays) == 0 {
		return nil, fmt.Errorf("%w: no display lies inside the capture", errOverlayFailed)
	}

	// Remember who had the foreground before the selector appears: Windows
	// hands the foreground to the next window in z-order once ours is
	// destroyed, which is not necessarily the window the user was in.
	restore, _, _ := procGetForegroundWindow.Call()

	session := newOverlaySession(displays, 0)
	overlay, err := newOverlayWindows(session, img, displays, displayIndexAtCursor(displays), restore)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errOverlayFailed, err)
	}
	session.setOnFinish(overlay.close)
	if err := overlay.create(); err != nil {
		session.cancel(overlayCancelWindowClosed)
		return nil, fmt.Errorf("%w: %v", errOverlayFailed, err)
	}
	session.startTimeout(overlayTimeout)

	result := session.wait(ctx)
	if result.canceled || result.selection.Empty() {
		return nil, errCaptureCanceled
	}
	// The selection lives in virtual-screen coordinates, and the capture's
	// image.Rectangle uses the same origin, so the crop is the exact pixels
	// with no offset arithmetic left to get wrong.
	sel, ok := overlayCrop(img, result.selection)
	if !ok {
		return nil, errCaptureCanceled
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, sel); err != nil {
		return nil, fmt.Errorf("%w: %v", errOverlayFailed, err)
	}
	return buf.Bytes(), nil
}

// captureVirtualDesktop BitBlts the whole virtual screen into a top-down
// 32bpp DIB. The returned rectangle is the virtual screen's origin and size
// in physical pixels; the image's own rectangle is the same.
func captureVirtualDesktop() (*image.RGBA, image.Rectangle, error) {
	x := int(getSystemMetric(smXVirtualScreen))
	y := int(getSystemMetric(smYVirtualScreen))
	w := int(getSystemMetric(smCXVirtualScreen))
	h := int(getSystemMetric(smCYVirtualScreen))
	if w <= 0 || h <= 0 {
		return nil, image.Rectangle{}, fmt.Errorf("virtual screen is %dx%d", w, h)
	}

	screen, _, errno := procGetDC.Call(0)
	if screen == 0 {
		return nil, image.Rectangle{}, fmt.Errorf("GetDC: %v", errno)
	}
	defer procReleaseDC.Call(0, screen)

	memDC, _, errno := procCreateCompatibleDC.Call(screen)
	if memDC == 0 {
		return nil, image.Rectangle{}, fmt.Errorf("CreateCompatibleDC: %v", errno)
	}
	defer procDeleteDC.Call(memDC)

	// Top-down (negative height) 32bpp DIB: its first row is the top of the
	// screen, matching image.RGBA's layout.
	bmi := winBitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(winBitmapInfoHeader{})),
		Width:       int32(w),
		Height:      -int32(h),
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}
	var bits unsafe.Pointer
	bitmap, _, errno := procCreateDIBSection.Call(
		screen,
		uintptr(unsafe.Pointer(&bmi)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if bitmap == 0 || bits == nil {
		return nil, image.Rectangle{}, fmt.Errorf("CreateDIBSection: %v", errno)
	}
	defer procDeleteObject.Call(bitmap)

	old, _, _ := procSelectObject.Call(memDC, bitmap)
	if old == 0 {
		return nil, image.Rectangle{}, fmt.Errorf("SelectObject failed")
	}
	defer procSelectObject.Call(memDC, old)

	// CAPTUREBLT includes layered windows; without it menus, tooltips and
	// third-party overlays are missing from the frozen image.
	ok, _, errno := procBitBlt.Call(
		memDC, 0, 0,
		uintptr(uint32(w)), uintptr(uint32(h)),
		screen,
		uintptr(uint32(int32(x))), uintptr(uint32(int32(y))),
		uintptr(srcCopy|captureBLT),
	)
	if ok == 0 {
		return nil, image.Rectangle{}, fmt.Errorf("BitBlt: %v", errno)
	}

	// The image's own rectangle is the virtual screen's, not (0,0): every
	// display rect and selection is expressed in that space, negative origin
	// included, and overlayCrop relies on the two matching. NewRGBA lays Pix
	// out from Rect.Min, so bgraToRGBA's linear fill still starts at the
	// first row.
	bounds := image.Rect(x, y, x+w, y+h)
	img := image.NewRGBA(bounds)
	bgraToRGBA(img, bits, w, h)
	return img, bounds, nil
}

func getSystemMetric(i int) int32 {
	v, _, _ := procGetSystemMetrics.Call(uintptr(uint32(i)))
	return int32(v)
}

// bgraToRGBA converts GDI's BGRA rows to image.RGBA in parallel, one stripe
// of rows per goroutine. Alpha is forced to 255: GDI leaves it undefined.
func bgraToRGBA(dst *image.RGBA, bits unsafe.Pointer, w, h int) {
	src := unsafe.Slice((*byte)(bits), w*h*4)
	workers := runtime.GOMAXPROCS(0)
	if workers > h {
		workers = h
	}
	if workers < 1 {
		workers = 1
	}
	rows := (h + workers - 1) / workers
	var wg sync.WaitGroup
	for y0 := 0; y0 < h; y0 += rows {
		y1 := min(y0+rows, h)
		wg.Add(1)
		go func(y0, y1 int) {
			defer wg.Done()
			off := y0 * w * 4
			for y := y0; y < y1; y++ {
				row := dst.Pix[off : off+w*4]
				for x := 0; x < w; x++ {
					i := x * 4
					row[i] = src[off+i+2]
					row[i+1] = src[off+i+1]
					row[i+2] = src[off+i]
					row[i+3] = 255
				}
				off += w * 4
			}
		}(y0, y1)
	}
	wg.Wait()
}

// queryMonitor reads one HMONITOR's physical rectangle and effective DPI.
func queryMonitor(mon uintptr) (overlayDisplayInfo, bool) {
	if mon == 0 {
		return overlayDisplayInfo{}, false
	}
	var mi monitorInfoEx
	mi.Size = uint32(unsafe.Sizeof(mi))
	ok, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	if ok == 0 {
		return overlayDisplayInfo{}, false
	}
	r := image.Rect(
		int(mi.Monitor.Left), int(mi.Monitor.Top),
		int(mi.Monitor.Right), int(mi.Monitor.Bottom),
	)
	if r.Empty() {
		return overlayDisplayInfo{}, false
	}
	dpi := 96
	if procGetDpiForMonitor.Find() == nil {
		var dx, dy uint32
		if hr, _, _ := procGetDpiForMonitor.Call(mon, 0,
			uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); hr == 0 && dx > 0 {
			dpi = int(dx)
		}
	}
	return overlayDisplayInfo{
		rect:    r,
		scale:   float64(dpi) / 96,
		primary: mi.Flags&monitorInfoPrimary != 0,
	}, true
}

var (
	monitorListMu sync.Mutex
	monitorList   []overlayDisplayInfo
	// One callback for the life of the process: syscall.NewCallback slots are
	// finite, and a selector must not leak one per capture.
	monitorEnumCallback = syscall.NewCallback(func(mon, _, _, _ uintptr) uintptr {
		if d, ok := queryMonitor(mon); ok {
			monitorListMu.Lock()
			monitorList = append(monitorList, d)
			monitorListMu.Unlock()
		}
		return 1
	})
)

// physicalDisplays enumerates every monitor in virtual-screen physical
// pixels. MyGo sets PerMonitorAwareV2 at startup, so neither the rectangles
// nor GetSystemMetrics are DPI-virtualized.
func physicalDisplays() ([]overlayDisplayInfo, error) {
	monitorListMu.Lock()
	monitorList = nil
	monitorListMu.Unlock()
	ok, _, errno := procEnumDisplayMonitors.Call(0, 0, monitorEnumCallback, 0)
	if ok == 0 {
		return nil, fmt.Errorf("EnumDisplayMonitors: %v", errno)
	}
	monitorListMu.Lock()
	out := append([]overlayDisplayInfo(nil), monitorList...)
	monitorListMu.Unlock()
	if len(out) == 0 {
		return nil, fmt.Errorf("no monitors")
	}
	return out, nil
}

// displayIndexAtCursor picks the display the pointer is on, which becomes
// the one window that takes the keyboard. Windows' SetForegroundWindow may
// still refuse; the click, right-click and timeout paths cover that case.
func displayIndexAtCursor(displays []overlayDisplayInfo) int {
	var p winPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	pt := image.Pt(int(p.X), int(p.Y))
	for i, d := range displays {
		if pt.In(d.rect) {
			return i
		}
	}
	for i, d := range displays {
		if d.primary {
			return i
		}
	}
	return 0
}

// applyOverlayStyle removes the frame MyGo's Frameless leaves behind on
// Windows: v0.6.4 only drops WS_THICKFRAME and WS_MAXIMIZEBOX, so Windows
// 11 still draws a border, rounded corners and a show transition.
func applyOverlayStyle(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	raw, _, _ := procGetWindowLongW.Call(hwnd, gwlStyle)
	style := uint32(raw)
	style &^= wsCaption | wsThickFrame | wsSysMenu | wsMinimizeBox | wsMaximizeBox
	style |= wsPopup | wsClipChildren
	procSetWindowLongW.Call(hwnd, gwlStyle, uintptr(style))

	ex, _, _ := procGetWindowLongW.Call(hwnd, gwlExStyle)
	procSetWindowLongW.Call(hwnd, gwlExStyle, ex|wsExToolWindow)

	procOverlaySetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpFrameChanged)

	// No show animation: the first frame should appear where the frozen
	// desktop already was, not fade or scale in.
	noTransitions := int32(1)
	procDwmSetWindowAttribute.Call(hwnd, uintptr(uint32(dwmwaTransitionsForcedisabled)),
		uintptr(unsafe.Pointer(&noTransitions)), unsafe.Sizeof(noTransitions))
	corner := int32(dwmcpDoNotRound)
	procDwmSetWindowAttribute.Call(hwnd, uintptr(uint32(dwmwaWindowCornerPreference)),
		uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))
	border := uint32(dwmwaColorNone)
	procDwmSetWindowAttribute.Call(hwnd, uintptr(uint32(dwmwaBorderColor)),
		uintptr(unsafe.Pointer(&border)), unsafe.Sizeof(border))
}

// placeOverlayWindow puts a window exactly on its monitor in physical
// pixels. HWND_TOPMOST is passed without SWP_NOZORDER: staying above
// everything is the point.
func placeOverlayWindow(hwnd uintptr, r image.Rectangle) {
	if hwnd == 0 {
		return
	}
	procOverlaySetWindowPos.Call(hwnd, hwndTopmost,
		uintptr(uint32(int32(r.Min.X))), uintptr(uint32(int32(r.Min.Y))),
		uintptr(uint32(r.Dx())), uintptr(uint32(r.Dy())),
		swpNoActivate|swpFrameChanged)
}

// overlayWindows is the window side of a session: one window and one bitmap
// per display, sharing the session's selection.
type overlayWindows struct {
	session  *overlaySession
	displays []overlayDisplayInfo
	img      *image.RGBA
	hint     int
	// restore is the HWND that had the foreground before the selector.
	restore uintptr

	mu       sync.Mutex
	bitmaps  []*ui.Bitmap
	windows  []*mygo.Window
	contexts []*ui.Context
	hwnds    map[uintptr]struct{}
	closed   sync.Once
}

func newOverlayWindows(session *overlaySession, img *image.RGBA, displays []overlayDisplayInfo, hint int, restore uintptr) (*overlayWindows, error) {
	if session == nil || img == nil || len(displays) == 0 {
		return nil, fmt.Errorf("no displays to overlay")
	}
	if hint < 0 || hint >= len(displays) {
		hint = 0
	}
	return &overlayWindows{
		session:  session,
		displays: displays,
		img:      img,
		hint:     hint,
		restore:  restore,
		hwnds:    map[uintptr]struct{}{},
	}, nil
}

// create builds the windows on the main thread, places each on its monitor
// and focuses the one under the cursor. A window on another monitor never
// crosses a DPI boundary, so WM_DPICHANGED does not re-place it; the second
// placement covers a monitor change between creation and showing.
func (o *overlayWindows) create() error {
	var createErr error
	mygo.RunOnMain(func() {
		o.mu.Lock()
		for i, d := range o.displays {
			crop, ok := overlayCrop(o.img, d.rect)
			if !ok {
				createErr = fmt.Errorf("display %d crop is empty", i)
				break
			}
			o.bitmaps = append(o.bitmaps, ui.NewBitmap(crop))
		}
		o.mu.Unlock()
		if createErr != nil {
			return
		}

		for i := range o.displays {
			index := i
			win := mygo.NewWindow(mygo.WindowOptions{
				Title:     "AnyPic",
				Width:     120,
				Height:    80,
				Hidden:    true,
				Frameless: true,
				// DisableMove must stay false: MyGo turns it into SWP_NOMOVE
				// on every WM_WINDOWPOSCHANGING, including our own placement.
				AlwaysOnTop:       true,
				SkipTaskbar:       true,
				DisableResize:     true,
				DisableMinimize:   true,
				DisableMaximize:   true,
				DisableFullScreen: true,
				DisableShadow:     true,
				BackgroundColor:   "#000000",
				Content:           ui.View(func(c *ui.Context) { o.view(c, index) }),
			})
			if win == nil || win.IsDestroyed() {
				createErr = fmt.Errorf("could not create the overlay for display %d", index)
				break
			}
			hwnd := win.NativeHandle()
			o.mu.Lock()
			o.hwnds[hwnd] = struct{}{}
			o.windows = append(o.windows, win)
			o.mu.Unlock()
			applyOverlayStyle(hwnd)
			placeOverlayWindow(hwnd, o.displays[index].rect)
		}
		if createErr != nil {
			return
		}

		// Register before showing: once the selector is visible, losing the
		// keyboard to another app must end it instead of leaving the user
		// typing into whatever is underneath.
		for _, win := range o.windows {
			win.OnBlur(func() { o.foregroundChanged() })
			win.OnClosed(func() { o.session.cancel(overlayCancelWindowClosed) })
		}
		for _, win := range o.windows {
			win.ShowInactive()
		}
		for i, win := range o.windows {
			placeOverlayWindow(win.NativeHandle(), o.displays[i].rect)
		}
		target := o.windows[o.hint]
		target.Show()
		placeOverlayWindow(target.NativeHandle(), o.displays[o.hint].rect)
		target.Focus()
	})
	return createErr
}

// foregroundChanged is WM_ACTIVATE(WA_INACTIVE): the selector may have had
// the keyboard and lost it. Moving focus between AnyPic's own overlays is
// fine; anything else cancels at once.
func (o *overlayWindows) foregroundChanged() {
	fg, _, _ := procGetForegroundWindow.Call()
	o.mu.Lock()
	_, ours := o.hwnds[fg]
	o.mu.Unlock()
	if ours {
		return
	}
	o.session.cancel(overlayCancelFocusLost)
}

// close hides and destroys the windows off the current call stack, so a
// view that ended the session is not painting into a destroyed window.
func (o *overlayWindows) close() {
	o.closed.Do(func() {
		go func() {
			mygo.RunOnMain(func() {
				// Give the foreground back before destroying ours: while our
				// window is the foreground one, this process may set it, and
				// destroying a window that is no longer foreground leaves the
				// restored window alone. A focus loss skips it: the user
				// already chose where to go.
				if o.restore != 0 && restoreForegroundAfter(o.session.finishedResult().reason) {
					if ok, _, _ := procIsWindow.Call(o.restore); ok != 0 {
						procSetForegroundWindow.Call(o.restore)
					}
				}
				o.mu.Lock()
				windows := append([]*mygo.Window(nil), o.windows...)
				o.mu.Unlock()
				for _, win := range windows {
					if win != nil && !win.IsDestroyed() {
						win.Close()
					}
				}
			})
		}()
	})
}

// view builds one overlay frame and handles the input that lands in it.
func (o *overlayWindows) view(c *ui.Context, index int) {
	o.mu.Lock()
	if index < len(o.contexts) {
		o.contexts[index] = c
	} else {
		for len(o.contexts) < index {
			o.contexts = append(o.contexts, nil)
		}
		o.contexts = append(o.contexts, c)
	}
	o.mu.Unlock()

	// The root box holds the keyboard focus: Shortcut delivers Escape only
	// through the focus chain, and there is nothing else here to focus.
	// Without it the Esc cancel would never fire, leaving the click,
	// right-click and timeout paths to cover a window that did get the
	// foreground.
	box := ui.Box(c).Fill().Cursor(ui.CursorCrosshair).Focus()
	box = box.Draw(func(p *ui.Painter, _ ui.Rect) { o.draw(p, index) })
	if o.session.isDone() {
		// The session ended this frame (or just before the queued close ran):
		// keep painting the frozen desktop instead of a black window, but
		// take no more input.
		return
	}
	o.input(c, box, index)
}

func (o *overlayWindows) input(c *ui.Context, box ui.Element, index int) {
	if c.Shortcut(0, ui.KeyEscape) {
		o.session.cancel(overlayCancelEscape)
		return
	}
	if box.RightClicked() {
		o.session.cancel(overlayCancelRightClick)
		return
	}
	snap := o.session.state()
	if snap.done {
		return
	}
	x, y, _ := box.PointerPosition()
	if box.Pressed() {
		if !snap.dragging && o.session.beginDrag(index, float64(x), float64(y)) {
			// The other windows are idle; wake them so they start following
			// the shared selection.
			o.invalidateAll()
		}
		if o.session.updateDrag(float64(x), float64(y)) {
			// The selection is shared, but only this window's view runs the
			// drag. A full-size rebuild of every overlay costs milliseconds
			// (measured at 8 ms/frame for a 4K bitmap) and is what keeps the
			// un-dimmed region and border in step across monitors.
			o.invalidateAll()
		}
	} else if snap.dragging && snap.owner == index {
		o.session.finishDrag()
	}
}

func (o *overlayWindows) invalidateAll() {
	o.mu.Lock()
	contexts := append([]*ui.Context(nil), o.contexts...)
	o.mu.Unlock()
	for _, ctx := range contexts {
		if ctx != nil {
			ctx.Invalidate()
		}
	}
}

func (o *overlayWindows) draw(p *ui.Painter, index int) {
	o.mu.Lock()
	var bm *ui.Bitmap
	if index < len(o.bitmaps) {
		bm = o.bitmaps[index]
	}
	o.mu.Unlock()

	d := o.displays[index]
	dipW, dipH := overlayDIPSize(d)
	full := ui.Rect{W: float32(dipW), H: float32(dipH)}
	if bm == nil {
		p.Fill(full, ui.RGB(0, 0, 0), 0)
		return
	}
	p.Image(bm, full, ui.FillBox)
	p.Fill(full, ui.RGBA(0, 0, 0, 0.45), 0)

	snap := o.session.state()
	if snap.hasSelection {
		if local, ok := overlayLocalRect(snap.selection, d); ok {
			src := ui.Rect{
				X: float32(local.Min.X),
				Y: float32(local.Min.Y),
				W: float32(local.Dx()),
				H: float32(local.Dy()),
			}
			dst := ui.Rect{
				X: src.X / float32(d.scale),
				Y: src.Y / float32(d.scale),
				W: src.W / float32(d.scale),
				H: src.H / float32(d.scale),
			}
			p.ImageRegion(bm, src, dst, ui.ImageOptions{})
		}
		// The full selection rectangle, clipped by this window, so a
		// selection crossing monitors gets its outer border only.
		border := ui.Rect{
			X: float32(snap.selection.Min.X-d.rect.Min.X) / float32(d.scale),
			Y: float32(snap.selection.Min.Y-d.rect.Min.Y) / float32(d.scale),
			W: float32(snap.selection.Dx()) / float32(d.scale),
			H: float32(snap.selection.Dy()) / float32(d.scale),
		}
		p.Stroke(border, ui.RGB(79, 70, 229), 0, 2)
	}
	if index == o.hint {
		o.drawHint(p, snap)
	}
}

func (o *overlayWindows) drawHint(p *ui.Painter, snap overlaySnapshot) {
	lines := []string{tr().GoOverlayHint()}
	if snap.hasSelection {
		lines = append(lines, fmt.Sprintf("%d × %d px", snap.selection.Dx(), snap.selection.Dy()))
	}
	const pad, lineH, fontSize = float32(14), float32(20), float32(13.5)
	panel := ui.Rect{
		X: 14,
		Y: 14,
		W: 420,
		H: pad*2 + lineH*float32(len(lines)),
	}
	p.Fill(panel, ui.RGBA(10, 12, 18, 0.84), 10)
	for i, s := range lines {
		p.Text(panel.X+pad, panel.Y+pad+float32(i)*lineH, s, fontSize, ui.RGB(238, 240, 245))
	}
}
