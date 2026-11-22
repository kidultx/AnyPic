package main

// Tests for the Windows selector's platform-independent pieces: the
// DIP-to-physical conversion, the black-frame check, and every cancel path.
// The Windows window and GDI code is build-tagged and cannot run here, which
// is exactly why these decisions live outside it.

import (
	"context"
	"errors"
	"image"
	"image/color"
	"os/exec"
	"testing"
	"time"
)

func overlayTestDisplays() []overlayDisplayInfo {
	return []overlayDisplayInfo{
		{rect: image.Rect(0, 0, 2560, 1080), scale: 1.5, primary: true},
		{rect: image.Rect(-1920, 0, 0, 1080), scale: 1.25},
	}
}

func fillRGBA(img *image.RGBA, c color.RGBA) {
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func TestOverlayDIPToPhysical(t *testing.T) {
	cases := []struct {
		v     float64
		scale float64
		want  int
	}{
		{0, 1, 0},
		{100, 1.5, 150},
		{1, 1.25, 1},  // 1.25 rounds down
		{2, 1.25, 3},  // 2.5 rounds away from zero
		{3, 1.5, 5},   // 4.5 rounds away from zero
		{-1, 1.5, -2}, // negative rounds away from zero too
		{100, 0, 100}, // a bad scale falls back to 1, never to zero pixels
	}
	for _, c := range cases {
		if got := dipToPhysical(c.v, c.scale); got != c.want {
			t.Errorf("dipToPhysical(%v, %v) = %d, want %d", c.v, c.scale, got, c.want)
		}
	}
}

// TestOverlayDIPSizeIsPixelExact guards the one-pixel seam the evaluation
// calls out: at 150 % a 2560-pixel monitor is 1706.67 DIP, and the window's
// truncated 1706 DIP would leave a black line. The fractional size must map
// back to exactly 2560 physical pixels.
func TestOverlayDIPSizeIsPixelExact(t *testing.T) {
	d := overlayDisplayInfo{rect: image.Rect(0, 0, 2560, 1080), scale: 1.5}
	w, h := overlayDIPSize(d)
	if got := dipToPhysical(w, d.scale); got != 2560 {
		t.Errorf("DIP width %v maps back to %d physical pixels, want 2560", w, got)
	}
	if got := dipToPhysical(h, d.scale); got != 1080 {
		t.Errorf("DIP height %v maps back to %d physical pixels, want 1080", h, got)
	}
}

func TestOverlayPhysicalPoint(t *testing.T) {
	displays := overlayTestDisplays()
	if p := overlayPhysicalPoint(displays[0], 100, 100); p != image.Pt(150, 150) {
		t.Errorf("primary DIP (100,100) → %v, want (150,150)", p)
	}
	// The display left of the primary has a negative origin; the DIP offset
	// is still relative to its own top-left.
	if p := overlayPhysicalPoint(displays[1], 100, 100); p != image.Pt(-1795, 125) {
		t.Errorf("left display DIP (100,100) → %v, want (-1795,125)", p)
	}
}

// TestOverlayCropUsesVirtualCoordinates guards the multi-monitor offset bug:
// the frozen image's rectangle is the virtual screen's, so a display left of
// the primary has a negative origin and yet crops to its own pixels. An image
// allocated at (0,0) — the obvious mistake — would return an empty or shifted
// crop for every such display and for selections on it.
func TestOverlayCropUsesVirtualCoordinates(t *testing.T) {
	bounds := image.Rect(-1920, 0, 2560, 1080)
	img := image.NewRGBA(bounds)
	fillRGBA(img, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	img.SetRGBA(-1900, 10, color.RGBA{R: 200, G: 100, B: 50, A: 255})

	crop, ok := overlayCrop(img, image.Rect(-1920, 0, 0, 1080))
	if !ok {
		t.Fatal("the left display crop is empty on a negative-origin screen")
	}
	if got := crop.Bounds(); got != image.Rect(-1920, 0, 0, 1080) {
		t.Errorf("crop bounds = %v, want the display rect", got)
	}
	if c := crop.RGBAAt(-1900, 10); c != (color.RGBA{R: 200, G: 100, B: 50, A: 255}) {
		t.Errorf("left display pixel = %v, want the marked pixel", c)
	}
	// The final selection crop goes through the same space.
	sel, ok := overlayCrop(img, image.Rect(-1910, 5, -1890, 15))
	if !ok {
		t.Fatal("the selection crop is empty on a negative-origin screen")
	}
	if c := sel.RGBAAt(-1900, 10); c.R != 200 {
		t.Errorf("selection pixel = %v, want the marked pixel", c)
	}
	if _, ok := overlayCrop(img, image.Rect(3000, 0, 3100, 50)); ok {
		t.Error("a crop outside the virtual screen reported success")
	}
	if _, ok := overlayCrop(nil, image.Rect(0, 0, 10, 10)); ok {
		t.Error("a nil image reported success")
	}
}

func TestOverlayLocalRect(t *testing.T) {
	displays := overlayTestDisplays()
	local, ok := overlayLocalRect(image.Rect(100, 100, 300, 200), displays[0])
	if !ok || local != image.Rect(100, 100, 300, 200) {
		t.Errorf("primary local rect = %v, %v; want (100,100)-(300,200)", local, ok)
	}
	local, ok = overlayLocalRect(image.Rect(-1900, 10, -100, 50), displays[1])
	if !ok || local != image.Rect(20, 10, 1820, 50) {
		t.Errorf("left display local rect = %v, %v; want (20,10)-(1820,50)", local, ok)
	}
	if _, ok := overlayLocalRect(image.Rect(3000, 0, 3100, 50), displays[0]); ok {
		t.Error("a selection outside the display reported a local rect")
	}
}

func TestOverlayClampSelection(t *testing.T) {
	displays := overlayTestDisplays()
	got := clampOverlaySelection(image.Rect(-4000, -100, 3000, 2000), displays)
	want := image.Rect(-1920, 0, 2560, 1080)
	if got != want {
		t.Errorf("clamped selection = %v, want %v", got, want)
	}
}

func TestOverlaySessionDrag(t *testing.T) {
	s := newOverlaySession(overlayTestDisplays(), 0)
	if !s.beginDrag(0, 100, 100) {
		t.Fatal("beginDrag on the primary was refused")
	}
	if !s.updateDrag(200, 50) {
		t.Fatal("updateDrag reported no change")
	}
	snap := s.state()
	if !snap.dragging || !snap.hasSelection {
		t.Fatalf("state after drag = %+v, want dragging with a selection", snap)
	}
	if want := image.Rect(150, 75, 300, 150); snap.selection != want {
		t.Errorf("selection = %v, want %v (DIP × 1.5)", snap.selection, want)
	}
	sel, ok := s.finishDrag()
	if !ok || sel != snap.selection {
		t.Errorf("finishDrag = %v, %v; want the selection and true", sel, ok)
	}
	if !s.isDone() {
		t.Error("the session did not finish after a drag")
	}
	if res := s.wait(context.Background()); res.canceled || res.selection != sel {
		t.Errorf("result = %+v, want the finished selection", res)
	}
}

func TestOverlaySessionDragOnLeftDisplay(t *testing.T) {
	s := newOverlaySession(overlayTestDisplays(), 0)
	if !s.beginDrag(1, 10, 10) {
		t.Fatal("beginDrag on the left display was refused")
	}
	s.updateDrag(100, 50)
	sel, ok := s.finishDrag()
	if !ok {
		t.Fatal("finishDrag reported a cancel")
	}
	if want := image.Rect(-1907, 13, -1795, 63); sel != want {
		t.Errorf("selection = %v, want %v (negative origin, DIP × 1.25)", sel, want)
	}
}

func TestOverlaySessionClickCancels(t *testing.T) {
	s := newOverlaySession(overlayTestDisplays(), 0)
	s.beginDrag(0, 100, 100)
	if _, ok := s.finishDrag(); ok {
		t.Fatal("a press without movement finished as a selection")
	}
	res := s.wait(context.Background())
	if !res.canceled || res.reason != overlayCancelClick {
		t.Errorf("result = %+v, want a click cancel", res)
	}
}

func TestOverlaySessionCancelPaths(t *testing.T) {
	reasons := []overlayCancelReason{
		overlayCancelEscape,
		overlayCancelRightClick,
		overlayCancelFocusLost,
		overlayCancelTimeout,
		overlayCancelContext,
		overlayCancelWindowClosed,
	}
	for _, reason := range reasons {
		s := newOverlaySession(overlayTestDisplays(), 0)
		s.beginDrag(0, 10, 10)
		fired := 0
		s.setOnFinish(func() { fired++ })
		s.cancel(reason)
		res := s.wait(context.Background())
		if !res.canceled || res.reason != reason {
			t.Errorf("reason %d: result = %+v, want canceled with that reason", reason, res)
		}
		if fired != 1 {
			t.Errorf("reason %d: onFinish ran %d times, want 1", reason, fired)
		}
	}
}

// TestRestoreForegroundAfter: every way out gives the foreground back to
// the window the user was in, except when the user themselves moved it (a
// focus loss) or the app is going away (a context cancel).
func TestRestoreForegroundAfter(t *testing.T) {
	cases := map[overlayCancelReason]bool{
		overlayCancelNone:         true, // a selection
		overlayCancelEscape:       true,
		overlayCancelRightClick:   true,
		overlayCancelTimeout:      true,
		overlayCancelClick:        true,
		overlayCancelWindowClosed: true,
		overlayCancelFocusLost:    false,
		overlayCancelContext:      false,
	}
	for reason, want := range cases {
		if got := restoreForegroundAfter(reason); got != want {
			t.Errorf("restoreForegroundAfter(%d) = %v, want %v", reason, got, want)
		}
	}
}

func TestOverlaySessionTimeout(t *testing.T) {
	s := newOverlaySession(overlayTestDisplays(), 20*time.Millisecond)
	select {
	case <-s.finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the selector did not time out")
	}
	res := s.wait(context.Background())
	if !res.canceled || res.reason != overlayCancelTimeout {
		t.Errorf("timeout result = %+v, want a timeout cancel", res)
	}
}

func TestOverlaySessionContextCancel(t *testing.T) {
	s := newOverlaySession(overlayTestDisplays(), 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := s.wait(ctx)
	if !res.canceled || res.reason != overlayCancelContext {
		t.Errorf("context result = %+v, want a context cancel", res)
	}
}

func TestLooksBlack(t *testing.T) {
	if !looksBlack(nil) {
		t.Error("a nil capture did not count as black")
	}
	if !looksBlack(image.NewRGBA(image.Rect(0, 0, 0, 0))) {
		t.Error("an empty capture did not count as black")
	}

	black := image.NewRGBA(image.Rect(0, 0, 64, 64))
	fillRGBA(black, color.RGBA{A: 255})
	if !looksBlack(black) {
		t.Error("a solid black capture did not count as black")
	}

	// One bright sample must defeat the 99.5 % threshold. The sampler starts
	// at pixel 0, so a white pixel there is always seen.
	black.SetRGBA(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	if looksBlack(black) {
		t.Error("a capture with a bright pixel counted as black")
	}

	gray := image.NewRGBA(image.Rect(0, 0, 64, 64))
	fillRGBA(gray, color.RGBA{R: 20, G: 20, B: 20, A: 255})
	if looksBlack(gray) {
		t.Error("a dark gray capture counted as black")
	}

	// Alpha is ignored: GDI leaves it undefined and this code forces it. A
	// black frame with alpha 0 is still black.
	transparentBlack := image.NewRGBA(image.Rect(0, 0, 64, 64))
	if !looksBlack(transparentBlack) {
		t.Error("a transparent black capture did not count as black")
	}
}

// TestOverlayFallbackNotices covers the branch the page keys its message on:
// a black capture and an outright failure fall back to the system tool with
// distinct notices, while a user cancel never reaches the system tool.
func TestOverlayFallbackNotices(t *testing.T) {
	seam := func(t *testing.T, overlayErr error) (*captureOutcome, error) {
		t.Helper()
		issue17Seams(t)
		oldSupported, oldCapture := overlaySupported, runOverlayCapture
		t.Cleanup(func() { overlaySupported, runOverlayCapture = oldSupported, oldCapture })
		overlaySupported = func() bool { return true }
		runOverlayCapture = func(context.Context) ([]byte, error) { return nil, overlayErr }
		screenshotPlanFor = issue17Plan
		runCaptureCommand = issue17Runner(issue17PNG(t))
		outcome, err := captureRegion(context.Background(), true)
		return &outcome, err
	}

	outcome, err := seam(t, errOverlayBlack)
	if err != nil {
		t.Fatalf("black fallback: %v", err)
	}
	if outcome.fallback != noticeOverlayBlack {
		t.Errorf("black fallback notice = %q, want %q", outcome.fallback, noticeOverlayBlack)
	}
	if len(outcome.data) == 0 {
		t.Error("black fallback returned no system-tool image")
	}

	outcome, err = seam(t, errors.New("window creation exploded"))
	if err != nil {
		t.Fatalf("failure fallback: %v", err)
	}
	if outcome.fallback != noticeOverlayFailed {
		t.Errorf("failure fallback notice = %q, want %q", outcome.fallback, noticeOverlayFailed)
	}

	issue17Seams(t)
	oldSupported, oldCapture := overlaySupported, runOverlayCapture
	t.Cleanup(func() { overlaySupported, runOverlayCapture = oldSupported, oldCapture })
	overlaySupported = func() bool { return true }
	runOverlayCapture = func(context.Context) ([]byte, error) { return nil, errCaptureCanceled }
	ran := 0
	screenshotPlanFor = func() (screenshotCommand, error) {
		ran++
		return screenshotCommand{name: "/usr/sbin/screencapture", args: []string{"-i", "-x"}, blocking: true}, nil
	}
	runCaptureCommand = func(*exec.Cmd) error { return nil }
	if _, err := captureRegion(context.Background(), true); !errors.Is(err, errCaptureCanceled) {
		t.Errorf("cancel result error = %v, want errCaptureCanceled", err)
	}
	if ran != 0 {
		t.Errorf("the system capture plan ran %d times after a user cancel, want 0", ran)
	}
}

// TestCaptureScreenshotReportsOverlayFallback is the end-to-end notice: a
// black selector falls back, still adds the capture, and reports the code
// the page turns into a message.
func TestCaptureScreenshotReportsOverlayFallback(t *testing.T) {
	issue17Seams(t)
	a, _ := issue17App(t)
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) { return true, nil }
	screenCaptureRequestAccess = func() {}
	focusScreenshotWindow = func(context.Context) {}
	screenshotPlanFor = issue17Plan
	runCaptureCommand = issue17Runner(issue17PNG(t))
	oldSupported, oldCapture := overlaySupported, runOverlayCapture
	t.Cleanup(func() { overlaySupported, runOverlayCapture = oldSupported, oldCapture })
	overlaySupported = func() bool { return true }
	runOverlayCapture = func(context.Context) ([]byte, error) { return nil, errOverlayBlack }

	result, err := a.CaptureScreenshot(context.Background())
	if err != nil {
		t.Fatalf("CaptureScreenshot: %v", err)
	}
	if result.Added != 1 {
		t.Errorf("added = %d, want 1 from the system fallback", result.Added)
	}
	if result.Notice != noticeOverlayBlack {
		t.Errorf("notice = %q, want %q", result.Notice, noticeOverlayBlack)
	}
}

// TestNormalizeSettingsScreenshotTool guards the closed value set: an old
// settings file (zero value) and a typo both mean the built-in selector, and
// only an explicit "system" opts out.
func TestNormalizeSettingsScreenshotTool(t *testing.T) {
	if got := normalizeSettings(Settings{}).ScreenshotTool; got != "anypic" {
		t.Errorf("zero value normalized to %q, want anypic", got)
	}
	if got := normalizeSettings(Settings{ScreenshotTool: "system"}).ScreenshotTool; got != "system" {
		t.Errorf("system normalized to %q, want system", got)
	}
	if got := normalizeSettings(Settings{ScreenshotTool: "snipping"}).ScreenshotTool; got != "anypic" {
		t.Errorf("unknown value normalized to %q, want anypic", got)
	}
	if got := defaultSettings().ScreenshotTool; got != "anypic" {
		t.Errorf("default ScreenshotTool = %q, want anypic", got)
	}
}
