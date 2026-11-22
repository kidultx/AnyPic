package main

// The Windows region selector's platform-independent core: the selection
// state machine, the DIP-to-physical conversion, and the black-frame check.
// The window and GDI code lives in overlay_windows.go; this file compiles
// everywhere so the arithmetic and the cancel paths are testable on macOS
// and Linux.

import (
	"context"
	"errors"
	"image"
	"log"
	"math"
	"sync"
	"time"
)

var (
	// errOverlayBlack says the frozen desktop came back (almost) entirely
	// black. RDP sessions that stopped rendering, DRM-protected content and
	// exclusive full-screen games hit this; the caller falls back to the
	// system screenshot tool instead of showing a black selector.
	errOverlayBlack = errors.New("overlay capture is black")
	// errOverlayFailed wraps any other reason the overlay could not be used.
	errOverlayFailed = errors.New("overlay capture failed")
	// errOverlayUnsupported is what non-Windows builds return if an overlay
	// capture is attempted anyway.
	errOverlayUnsupported = errors.New("overlay capture is not supported on this platform")
)

// overlayTimeout ends a selector the user left alone. The window loss paths
// (activation, right-click, Esc) cover the usual ways out; this is the
// backstop for the case where the overlay never got the keyboard at all.
const overlayTimeout = 60 * time.Second

// overlayDisplayInfo is one monitor: its rectangle in virtual-screen
// physical pixels, and the effective DPI scale (1.5 at 150 %). The capture
// bitmap is physical pixels, while PointerPosition is DIPs in the window, so
// every input coordinate has to cross this scale exactly once.
type overlayDisplayInfo struct {
	rect    image.Rectangle
	scale   float64
	primary bool
}

// overlayCancelReason records why a selector ended; used by tests and logs,
// never shown to the user.
type overlayCancelReason int

const (
	overlayCancelNone overlayCancelReason = iota
	overlayCancelEscape
	overlayCancelRightClick
	overlayCancelFocusLost
	overlayCancelTimeout
	overlayCancelContext
	overlayCancelClick
	overlayCancelWindowClosed
)

// overlayResult is what the selector's wait returns.
type overlayResult struct {
	selection image.Rectangle // global physical pixels; empty when canceled
	canceled  bool
	reason    overlayCancelReason
}

// overlaySnapshot is the drawing and input state one frame sees.
type overlaySnapshot struct {
	selection    image.Rectangle
	hasSelection bool
	dragging     bool
	owner        int
	version      uint64
	done         bool
}

// overlaySession owns the selection while the per-monitor windows exist. Its
// methods are safe from any goroutine: views run on the main thread, the
// timeout goroutine ends it, and captureOverlayRegion waits on another.
type overlaySession struct {
	mu       sync.Mutex
	displays []overlayDisplayInfo

	dragging  bool
	owner     int
	anchor    image.Point
	selection image.Rectangle
	has       bool
	version   uint64

	done     bool
	result   overlayResult
	finished chan struct{}
	timer    *time.Timer
	onFinish func()
}

func newOverlaySession(displays []overlayDisplayInfo, timeout time.Duration) *overlaySession {
	s := &overlaySession{
		displays: displays,
		owner:    -1,
		finished: make(chan struct{}),
	}
	if timeout > 0 {
		// Assign under the lock: a short timeout can fire before the
		// constructor returns, and finishLocked reads s.timer.
		s.mu.Lock()
		s.timer = time.AfterFunc(timeout, func() { s.cancel(overlayCancelTimeout) })
		s.mu.Unlock()
	}
	return s
}

// setOnFinish installs the teardown hook. If the session already ended, the
// hook runs at once on the caller's goroutine.
func (s *overlaySession) setOnFinish(fn func()) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		fn()
		return
	}
	s.onFinish = fn
	s.mu.Unlock()
}

// startTimeout arms the backstop after the overlay is visible; a capture
// that never shows a window should not burn the user's minute.
func (s *overlaySession) startTimeout(d time.Duration) {
	if d <= 0 {
		return
	}
	s.mu.Lock()
	if s.done || s.timer != nil {
		s.mu.Unlock()
		return
	}
	s.timer = time.AfterFunc(d, func() { s.cancel(overlayCancelTimeout) })
	s.mu.Unlock()
}

// wait blocks until the selection finishes, the user cancels, or ctx ends.
func (s *overlaySession) wait(ctx context.Context) overlayResult {
	select {
	case <-s.finished:
	case <-ctx.Done():
		s.cancel(overlayCancelContext)
		<-s.finished
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

func (s *overlaySession) state() overlaySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return overlaySnapshot{
		selection:    s.selection,
		hasSelection: s.has,
		dragging:     s.dragging,
		owner:        s.owner,
		version:      s.version,
		done:         s.done,
	}
}

func (s *overlaySession) isDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// beginDrag starts a selection in window displayIndex at the pointer's DIP
// position in that window.
func (s *overlaySession) beginDrag(displayIndex int, dipX, dipY float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done || s.dragging || displayIndex < 0 || displayIndex >= len(s.displays) {
		return false
	}
	d := s.displays[displayIndex]
	s.dragging = true
	s.owner = displayIndex
	s.anchor = overlayPhysicalPoint(d, dipX, dipY)
	s.selection = image.Rectangle{}
	s.has = false
	s.version++
	return true
}

// updateDrag moves the free corner. A move that changes nothing reports
// false, so callers can skip extra work.
func (s *overlaySession) updateDrag(dipX, dipY float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done || !s.dragging {
		return false
	}
	d := s.displays[s.owner]
	p := overlayPhysicalPoint(d, dipX, dipY)
	sel := image.Rect(s.anchor.X, s.anchor.Y, p.X, p.Y)
	sel = clampOverlaySelection(sel, s.displays)
	if sel == s.selection {
		return false
	}
	s.selection = sel
	s.has = sel.Dx() > 0 && sel.Dy() > 0
	s.version++
	return true
}

// finishDrag ends the drag on button release. A press without a drag is a
// click, and a click cancels: it gives the user a second way out that does
// not need the keyboard.
func (s *overlaySession) finishDrag() (image.Rectangle, bool) {
	s.mu.Lock()
	if s.done || !s.dragging {
		s.mu.Unlock()
		return image.Rectangle{}, false
	}
	s.dragging = false
	sel := s.selection
	var hook func()
	var ok bool
	if sel.Dx() < 1 || sel.Dy() < 1 {
		hook = s.finishLocked(overlayResult{canceled: true, reason: overlayCancelClick})
	} else {
		hook = s.finishLocked(overlayResult{selection: sel})
		ok = true
	}
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return sel, ok
}

// cancel ends the selector without an image.
func (s *overlaySession) cancel(reason overlayCancelReason) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.dragging = false
	hook := s.finishLocked(overlayResult{canceled: true, reason: reason})
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
}

// finishedResult returns the result recorded when the session ended, or the
// zero result while it runs.
func (s *overlaySession) finishedResult() overlayResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

// restoreForegroundAfter reports whether the window that had the foreground
// before the selector should get it back when the selector ends for reason.
// A focus loss means the user already chose another window, so taking the
// foreground back would fight them; a context cancel means the app is going
// away. Every other way out — a selection, Esc, right-click, a click, the
// timeout, a close — restores it, because Windows activates the next window
// in z-order after a destroyed foreground window, not necessarily the one
// the user was in.
func restoreForegroundAfter(reason overlayCancelReason) bool {
	switch reason {
	case overlayCancelFocusLost, overlayCancelContext:
		return false
	}
	return true
}

// finishLocked records the result once and returns the teardown hook to run
// after the lock is released.
func (s *overlaySession) finishLocked(res overlayResult) func() {
	if s.done {
		return nil
	}
	s.done = true
	s.result = res
	if s.timer != nil {
		s.timer.Stop()
	}
	close(s.finished)
	return s.onFinish
}

// dipToPhysical converts a window DIP coordinate to physical pixels on a
// display with the given scale. Rounding is to nearest, as Windows' own
// MulDiv-based DIP conversion does.
func dipToPhysical(v, scale float64) int {
	if scale <= 0 {
		scale = 1
	}
	return int(math.Round(v * scale))
}

// overlayPhysicalPoint maps a DIP point inside a window covering d to global
// virtual-screen physical pixels. The window's top-left is d.rect.Min: the
// overlay is placed exactly on its monitor, so no window-frame offset
// belongs in this conversion.
func overlayPhysicalPoint(d overlayDisplayInfo, dipX, dipY float64) image.Point {
	return image.Pt(
		d.rect.Min.X+dipToPhysical(dipX, d.scale),
		d.rect.Min.Y+dipToPhysical(dipY, d.scale),
	)
}

// overlayDIPSize is the display's size in DIPs. It is not c.Size(): the
// window's DIP size is truncated to whole DIPs, so at 150 % a 2560-pixel
// monitor would come back as 1706 DIP (2559 physical pixels) and leave a
// black line. The bitmap and the dim layer use this fractional size, and
// the painter's device-pixel snapping lands on exactly 2560.
func overlayDIPSize(d overlayDisplayInfo) (float64, float64) {
	scale := d.scale
	if scale <= 0 {
		scale = 1
	}
	return float64(d.rect.Dx()) / scale, float64(d.rect.Dy()) / scale
}

// overlayCrop returns the part of the frozen desktop inside r. The frozen
// image's rectangle is the virtual screen's, so r — a display rect or a
// selection — is already in the image's own coordinate space, negative
// origins included. This is the only crop the overlay performs: a capture
// allocated at (0,0) instead would shift every display left or above the
// primary by the virtual-screen origin.
func overlayCrop(img *image.RGBA, r image.Rectangle) (*image.RGBA, bool) {
	if img == nil {
		return nil, false
	}
	r = r.Intersect(img.Rect)
	if r.Empty() {
		return nil, false
	}
	return img.SubImage(r).(*image.RGBA), true
}

// overlayLocalRect returns the part of a global physical rectangle on d, in
// physical pixels relative to the display's top-left.
func overlayLocalRect(r image.Rectangle, d overlayDisplayInfo) (image.Rectangle, bool) {
	local := r.Intersect(d.rect)
	if local.Empty() {
		return image.Rectangle{}, false
	}
	return local.Sub(d.rect.Min), true
}

// clampOverlaySelection keeps a selection inside the bounding box of the
// displays. A non-rectangular virtual desktop (screens of different heights
// or offset vertically) still has black gaps inside that box; the rectangle
// cannot represent their union, so this only guarantees the selection never
// reaches the empty area around all displays.
func clampOverlaySelection(sel image.Rectangle, displays []overlayDisplayInfo) image.Rectangle {
	if len(displays) == 0 {
		return image.Rectangle{}
	}
	bounds := displays[0].rect
	for _, d := range displays[1:] {
		bounds = bounds.Union(d.rect)
	}
	return sel.Intersect(bounds)
}

// looksBlack reports whether a capture is almost entirely black. A nearly
// black frame usually means GDI could not see the desktop (an RDP session
// that stopped rendering, protected content), not that the desktop is black.
// The eval calls for this check instead of refusing RDP sessions up front.
func looksBlack(img *image.RGBA) bool {
	if img == nil || img.Rect.Empty() {
		return true
	}
	const stride = 4093 // prime, spreads samples across rows
	dark, n := 0, 0
	for i := 0; i+3 < len(img.Pix); i += 4 * stride {
		n++
		if img.Pix[i] < 8 && img.Pix[i+1] < 8 && img.Pix[i+2] < 8 {
			dark++
		}
	}
	return n > 0 && dark*1000 >= n*995
}

// logOverlayFailure records an unexpected overlay failure. A user cancel and
// a black capture are expected outcomes with their own handling; anything
// else is worth a line now that the user only sees a fallback notice.
func logOverlayFailure(err error) {
	if err != nil && !errors.Is(err, errCaptureCanceled) && !errors.Is(err, errOverlayBlack) {
		log.Printf("AnyPic: overlay capture failed: %v", err)
	}
}

// Seams for tests: the platform check and the overlay entry point. On
// non-Windows hosts the platform check is false, so the existing capture
// plans run unchanged.
var (
	overlaySupported  = overlayPlatformSupported
	runOverlayCapture = captureOverlayRegion
)
