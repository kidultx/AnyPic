package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"testing"
)

// TestScreenshotPipelineCopiesRawCaptureAndStaysInBackground: with the
// pipeline on, the raw shot reaches the clipboard before compression and the
// app does not steal focus — the user is about to paste, not to open the
// queue. Result.Copied still follows the explicit "copy after capture"
// setting, because the pipeline's copy is only an intermediate state.
func TestScreenshotPipelineCopiesRawCaptureAndStaysInBackground(t *testing.T) {
	issue17Seams(t)
	a, _ := issue17App(t)
	a.settings.ScreenshotPipeline = "on"
	a.settings.ScreenshotClipboard = "on"
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) { return true, nil }
	screenCaptureRequestAccess = func() {}
	png := issue17PNG(t)
	screenshotPlanFor = issue17Plan
	runCaptureCommand = func(cmd *exec.Cmd) error { return issue17Runner(png)(cmd) }
	focused := 0
	focusScreenshotWindow = func(context.Context) { focused++ }
	var writes [][]byte
	clipboardReadState = func() clipboardState { return clipboardState{} }
	clipboardWriteImage = func(data []byte) (uint64, error) {
		writes = append(writes, append([]byte(nil), data...))
		return 7, nil
	}

	result, err := a.CaptureScreenshot(context.Background())
	if err != nil {
		t.Fatalf("CaptureScreenshot: %v", err)
	}
	if result.Added != 1 {
		t.Fatalf("added = %d, want 1", result.Added)
	}
	if len(writes) != 1 || !bytes.Equal(writes[0], png) {
		t.Fatalf("clipboard writes = %d, want the raw capture exactly once", len(writes))
	}
	if !result.Copied {
		t.Fatal("Copied should report the explicit copy setting, which is on")
	}
	if focused != 0 {
		t.Fatalf("window focused %d times; the pipeline should leave the app in the background", focused)
	}
	if len(a.items) != 1 {
		t.Fatalf("queue has %d items, want 1", len(a.items))
	}
	if got := a.items[0].clipboardLease; got != 7 {
		t.Fatalf("capture clipboard lease = %d, want 7", got)
	}
	if !a.items[0].clipboardHasPixels {
		t.Fatal("the capture's decoded pixels were not recorded for the paste fallback")
	}
}

// TestScreenshotPipelineNeedsClipboardPermission: a stored pipeline "on"
// must not write an image when the user disabled capture copying. The
// pipeline exists to replace the raw copy, so without it there is nothing to
// replace.
func TestScreenshotPipelineNeedsClipboardPermission(t *testing.T) {
	issue17Seams(t)
	a, _ := issue17App(t)
	a.settings.ScreenshotPipeline = "on"
	a.settings.ScreenshotClipboard = "off"
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) { return true, nil }
	screenCaptureRequestAccess = func() {}
	png := issue17PNG(t)
	screenshotPlanFor = issue17Plan
	runCaptureCommand = func(cmd *exec.Cmd) error { return issue17Runner(png)(cmd) }
	focusScreenshotWindow = func(context.Context) {}
	writes := 0
	clipboardWriteImage = func([]byte) (uint64, error) {
		writes++
		return 1, nil
	}

	if _, err := a.CaptureScreenshot(context.Background()); err != nil {
		t.Fatalf("CaptureScreenshot: %v", err)
	}
	if writes != 0 {
		t.Fatalf("clipboard writes = %d, want 0 with capture copying off", writes)
	}
	if a.items[0].clipboardLease != 0 {
		t.Fatal("a clipboard lease was recorded although nothing was written")
	}
}

// TestScreenshotClipboardWriteFailureFallsBackFromPipeline: the pipeline
// replaces the raw capture on the clipboard, so a failed raw write must
// switch it off. The app comes forward like a plain capture instead of
// staying in the background, and Copied=false keeps the page from starting a
// replacement pass — and from later claiming the user copied something else
// during compression.
func TestScreenshotClipboardWriteFailureFallsBackFromPipeline(t *testing.T) {
	issue17Seams(t)
	a, _ := issue17App(t)
	a.settings.ScreenshotPipeline = "on"
	a.settings.ScreenshotClipboard = "on"
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) { return true, nil }
	screenCaptureRequestAccess = func() {}
	png := issue17PNG(t)
	screenshotPlanFor = issue17Plan
	runCaptureCommand = issue17Runner(png)
	focused := 0
	focusScreenshotWindow = func(context.Context) { focused++ }
	stub := stubClipboard(t)
	stub.writeErr = errors.New("clipboard unavailable")

	result, err := a.CaptureScreenshot(context.Background())
	if err != nil {
		t.Fatalf("CaptureScreenshot: %v", err)
	}
	if result.Copied {
		t.Fatal("Copied is true after the raw write failed")
	}
	if focused != 1 {
		t.Fatalf("window focus calls = %d after a failed raw write, want 1: the pipeline has nothing to replace and must fall back", focused)
	}
}

// TestScreenshotClipboardWriteWithoutLeaseIsNotACopy: a writer that reports
// success but hands back no lease token proves nothing about ownership. The
// capture must not count as copied, or the pipeline would arm on a clipboard
// it can never verify and later blame the user for copying something else.
func TestScreenshotClipboardWriteWithoutLeaseIsNotACopy(t *testing.T) {
	issue17Seams(t)
	a, _ := issue17App(t)
	a.settings.ScreenshotPipeline = "on"
	a.settings.ScreenshotClipboard = "on"
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) { return true, nil }
	screenCaptureRequestAccess = func() {}
	png := issue17PNG(t)
	screenshotPlanFor = issue17Plan
	runCaptureCommand = issue17Runner(png)
	focused := 0
	focusScreenshotWindow = func(context.Context) { focused++ }
	clipboardWriteImage = func([]byte) (uint64, error) { return 0, nil }

	result, err := a.CaptureScreenshot(context.Background())
	if err != nil {
		t.Fatalf("CaptureScreenshot: %v", err)
	}
	if result.Copied {
		t.Fatal("Copied is true although the write returned no lease, so ownership is unverifiable")
	}
	if focused != 1 {
		t.Fatalf("window focus calls = %d without a lease, want 1: the pipeline must fall back", focused)
	}
	if a.items[0].clipboardLease != 0 {
		t.Fatalf("clipboard lease = %d, want 0 without a token", a.items[0].clipboardLease)
	}
}

// TestScreenshotClipboardWriteFailureRecordsNoOwnership: if the raw write
// fails, the compressed result must never think the clipboard is ours.
func TestScreenshotClipboardWriteFailureRecordsNoOwnership(t *testing.T) {
	issue17Seams(t)
	a, _ := issue17App(t)
	a.settings.ScreenshotPipeline = "on"
	a.settings.ScreenshotClipboard = "on"
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) { return true, nil }
	screenCaptureRequestAccess = func() {}
	png := issue17PNG(t)
	screenshotPlanFor = issue17Plan
	runCaptureCommand = func(cmd *exec.Cmd) error { return issue17Runner(png)(cmd) }
	focusScreenshotWindow = func(context.Context) {}
	stub := stubClipboard(t)
	stub.writeErr = errors.New("clipboard unavailable")

	result, err := a.CaptureScreenshot(context.Background())
	if err != nil {
		t.Fatalf("CaptureScreenshot: %v", err)
	}
	if result.Copied {
		t.Fatal("Copied is true after the raw write failed")
	}
	if a.items[0].clipboardLease != 0 || a.items[0].clipboardHasPixels {
		t.Fatal("ownership was recorded although the raw clipboard write failed")
	}
	// Even if the clipboard later holds an image that looks like the capture,
	// the failed write left no ownership; the pipeline must skip instead of
	// overwriting it.
	stub.read = png
	a.tempDir = t.TempDir()
	a.compressItem(context.Background(), a.items[0].ID, a.settings)
	outcome, err := a.CopyCaptureResult(a.items[0].ID)
	if err != nil {
		t.Fatalf("CopyCaptureResult: %v", err)
	}
	if !outcome.Skipped || outcome.Copied {
		t.Fatalf("outcome = %+v, want skipped after the failed raw write", outcome)
	}
}

// TestScreenshotWithoutPipelineLeavesTheClipboardAlone: both clipboard
// switches off means the capture is neither copied nor used as a pipeline
// intermediate, and the app comes forward so the queue confirms the shot.
func TestScreenshotWithoutPipelineLeavesTheClipboardAlone(t *testing.T) {
	issue17Seams(t)
	a, _ := issue17App(t)
	a.settings.ScreenshotClipboard = "off"
	a.settings.ScreenshotPipeline = "off"
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) { return true, nil }
	screenCaptureRequestAccess = func() {}
	png := issue17PNG(t)
	screenshotPlanFor = issue17Plan
	runCaptureCommand = func(cmd *exec.Cmd) error { return issue17Runner(png)(cmd) }
	focused := 0
	focusScreenshotWindow = func(context.Context) { focused++ }
	writes := 0
	clipboardWriteImage = func([]byte) (uint64, error) {
		writes++
		return 1, nil
	}

	if _, err := a.CaptureScreenshot(context.Background()); err != nil {
		t.Fatalf("CaptureScreenshot: %v", err)
	}
	if writes != 0 {
		t.Fatalf("clipboard writes = %d, want 0", writes)
	}
	if focused != 1 {
		t.Fatalf("window focused %d times, want 1", focused)
	}
}
