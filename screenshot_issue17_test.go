package main

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
)

// issue17Seams restores every injected screenshot seam after the test.
func issue17Seams(t *testing.T) {
	t.Helper()
	goos := screenshotGOOS
	preflight := screenCapturePreflight
	request := screenCaptureRequestAccess
	plan := screenshotPlanFor
	run := runCaptureCommand
	start := startCaptureCommand
	focus := focusScreenshotWindow
	t.Cleanup(func() {
		screenshotGOOS = goos
		screenCapturePreflight = preflight
		screenCaptureRequestAccess = request
		screenshotPlanFor = plan
		runCaptureCommand = run
		startCaptureCommand = start
		focusScreenshotWindow = focus
	})
}

// issue17App returns an app whose screenshot paths are inside t.TempDir(), so
// a test never touches the user's Pictures/AnyPic or settings.json.
func issue17App(t *testing.T) (*AnyPic, string) {
	t.Helper()
	pictures := t.TempDir()
	mygo.App.SetPath(mygo.PathPictures, pictures)
	t.Cleanup(func() { mygo.App.SetPath(mygo.PathPictures, "") })
	a := &AnyPic{
		byID:     map[int64]*Item{},
		settings: defaultSettings(),
		thumbSem: make(chan struct{}, 1),
	}
	// No clipboard writes and no zero-click flow: the success path is then
	// deterministic and never reads or writes the real clipboard.
	a.settings.AutoProcess = "off"
	a.settings.ScreenshotClipboard = "off"
	return a, pictures
}

func issue17PNG(t *testing.T) []byte {
	t.Helper()
	img := solidImage(20, 16, color.RGBA{R: 0x20, G: 0x40, B: 0xe0, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// issue17Plan is the macOS file-based plan. The fake runner writes the image
// straight to the path captureToFile appends, so no real screenshot tool runs.
func issue17Plan() (screenshotCommand, error) {
	return screenshotCommand{name: "/usr/sbin/screencapture", args: []string{"-i", "-x"}, blocking: true}, nil
}

func issue17Runner(data []byte) func(*exec.Cmd) error {
	return func(cmd *exec.Cmd) error {
		return os.WriteFile(cmd.Args[len(cmd.Args)-1], data, 0o644)
	}
}

// issue17ExitError stands in for a failed screenshot command without
// starting a process: the fake runner returns it, and captureRegion turns it
// into errCaptureCanceled.
type issue17ExitError struct{}

func (issue17ExitError) Error() string { return "screenshot command failed" }

func issue17Files(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

// TestIssue17NoPermissionAsksAndDoesNotCapture is the core of #17: a missing
// macOS screen recording permission must ask the system once, skip the
// capture command entirely, write nothing, and surface a distinct error.
func TestIssue17NoPermissionAsksAndDoesNotCapture(t *testing.T) {
	issue17Seams(t)
	a, pictures := issue17App(t)
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) { return false, nil }
	requested := 0
	screenCaptureRequestAccess = func() { requested++ }
	ran := 0
	screenshotPlanFor = issue17Plan
	runCaptureCommand = func(*exec.Cmd) error {
		ran++
		return nil
	}
	focused := 0
	focusScreenshotWindow = func(context.Context) { focused++ }

	_, err := a.CaptureScreenshot(context.Background())
	if !errors.Is(err, errScreenCapturePermission) {
		t.Fatalf("CaptureScreenshot error = %v, want errScreenCapturePermission", err)
	}
	if errors.Is(err, errCaptureCanceled) {
		t.Fatal("permission failure is reported as a cancel, so the page would stay silent")
	}
	if requested != 1 {
		t.Errorf("CGRequestScreenCaptureAccess calls = %d, want 1", requested)
	}
	if ran != 0 {
		t.Errorf("capture command ran %d times without permission, want 0", ran)
	}
	if files := issue17Files(t, pictures); len(files) != 0 {
		t.Errorf("permission failure wrote %v, want no screenshot files", files)
	}
	if focused != 1 {
		t.Errorf("window focus calls = %d, want 1 so the error is visible", focused)
	}
}

// TestIssue17PermissionGrantedCaptures: with access granted the capture runs
// and lands in the queue as before.
func TestIssue17PermissionGrantedCaptures(t *testing.T) {
	issue17Seams(t)
	a, pictures := issue17App(t)
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) { return true, nil }
	requested := 0
	screenCaptureRequestAccess = func() { requested++ }
	ran := 0
	png := issue17PNG(t)
	screenshotPlanFor = issue17Plan
	runCaptureCommand = func(cmd *exec.Cmd) error {
		ran++
		return issue17Runner(png)(cmd)
	}
	focused := 0
	focusScreenshotWindow = func(context.Context) { focused++ }

	result, err := a.CaptureScreenshot(context.Background())
	if err != nil {
		t.Fatalf("CaptureScreenshot: %v", err)
	}
	if result.Added != 1 {
		t.Errorf("added = %d, want 1", result.Added)
	}
	if ran != 1 {
		t.Errorf("capture command runs = %d, want 1", ran)
	}
	if requested != 0 {
		t.Errorf("asked for permission %d times although granted, want 0", requested)
	}
	if focused != 1 {
		t.Errorf("window focus calls = %d, want 1 after a successful capture", focused)
	}
	if files := issue17Files(t, pictures); len(files) != 1 {
		t.Errorf("screenshot files = %v, want exactly one under Pictures/AnyPic", files)
	}
}

// TestIssue17CancelStaysQuiet: a failed capture command (the user pressing
// Esc, for example) stays errCaptureCanceled and does not steal focus.
func TestIssue17CancelStaysQuiet(t *testing.T) {
	issue17Seams(t)
	a, pictures := issue17App(t)
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) { return true, nil }
	screenCaptureRequestAccess = func() {}
	screenshotPlanFor = issue17Plan
	runCaptureCommand = func(*exec.Cmd) error { return issue17ExitError{} }
	focused := 0
	focusScreenshotWindow = func(context.Context) { focused++ }

	_, err := a.CaptureScreenshot(context.Background())
	if !errors.Is(err, errCaptureCanceled) {
		t.Fatalf("CaptureScreenshot error = %v, want errCaptureCanceled", err)
	}
	if focused != 0 {
		t.Errorf("window focus calls = %d after a cancel, want 0", focused)
	}
	if files := issue17Files(t, pictures); len(files) != 0 {
		t.Errorf("cancel wrote %v, want no screenshot files", files)
	}
}

// TestIssue17SaveFailureBringsWindowForward covers errors after a successful
// capture: the page must be visible to show the save failure.
func TestIssue17SaveFailureBringsWindowForward(t *testing.T) {
	issue17Seams(t)
	a, _ := issue17App(t)
	notADir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	mygo.App.SetPath(mygo.PathPictures, notADir)
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) { return true, nil }
	screenCaptureRequestAccess = func() {}
	screenshotPlanFor = issue17Plan
	runCaptureCommand = issue17Runner(issue17PNG(t))
	focused := 0
	focusScreenshotWindow = func(context.Context) { focused++ }

	_, err := a.CaptureScreenshot(context.Background())
	if err == nil {
		t.Fatal("CaptureScreenshot succeeded although the screenshot could not be saved")
	}
	if focused != 1 {
		t.Errorf("window focus calls = %d, want 1 after a save failure", focused)
	}
}

// TestIssue17NonDarwinSkipsPermissionCheck: only macOS has the TCC gate.
func TestIssue17NonDarwinSkipsPermissionCheck(t *testing.T) {
	issue17Seams(t)
	a, _ := issue17App(t)
	screenshotGOOS = "linux"
	preflight := 0
	screenCapturePreflight = func() (bool, error) {
		preflight++
		return false, nil
	}
	requested := 0
	screenCaptureRequestAccess = func() { requested++ }
	screenshotPlanFor = issue17Plan
	runCaptureCommand = issue17Runner(issue17PNG(t))
	focusScreenshotWindow = func(context.Context) {}

	result, err := a.CaptureScreenshot(context.Background())
	if err != nil {
		t.Fatalf("CaptureScreenshot on linux: %v", err)
	}
	if result.Added != 1 {
		t.Errorf("added = %d, want 1", result.Added)
	}
	if preflight != 0 {
		t.Errorf("preflight ran %d times off macOS, want 0", preflight)
	}
	if requested != 0 {
		t.Errorf("permission was requested %d times off macOS, want 0", requested)
	}
}

// TestIssue17SymbolFailureFallsBackToCapture: if purego cannot resolve the
// CoreGraphics symbols, the old behavior (try the capture) is kept.
func TestIssue17SymbolFailureFallsBackToCapture(t *testing.T) {
	issue17Seams(t)
	a, _ := issue17App(t)
	screenshotGOOS = "darwin"
	screenCapturePreflight = func() (bool, error) {
		return false, errors.New("CGPreflightScreenCaptureAccess not found")
	}
	requested := 0
	screenCaptureRequestAccess = func() { requested++ }
	screenshotPlanFor = issue17Plan
	ran := 0
	runCaptureCommand = func(cmd *exec.Cmd) error {
		ran++
		return issue17Runner(issue17PNG(t))(cmd)
	}
	focusScreenshotWindow = func(context.Context) {}

	result, err := a.CaptureScreenshot(context.Background())
	if err != nil {
		t.Fatalf("CaptureScreenshot after a symbol failure: %v", err)
	}
	if result.Added != 1 || ran != 1 {
		t.Errorf("added = %d, command runs = %d; want the old capture path", result.Added, ran)
	}
	if requested != 0 {
		t.Errorf("permission was requested %d times after a symbol failure, want 0", requested)
	}
}

// TestIssue17BusyStaysQuiet: a second press while a capture is open is not an
// error the user needs to see, so it keeps the window where it is.
func TestIssue17BusyStaysQuiet(t *testing.T) {
	issue17Seams(t)
	a, _ := issue17App(t)
	captureBusy.Store(true)
	t.Cleanup(func() { captureBusy.Store(false) })
	focused := 0
	focusScreenshotWindow = func(context.Context) { focused++ }

	_, err := a.CaptureScreenshot(context.Background())
	if !errors.Is(err, errCaptureBusy) {
		t.Fatalf("CaptureScreenshot error = %v, want errCaptureBusy", err)
	}
	if focused != 0 {
		t.Errorf("window focus calls = %d for a busy press, want 0", focused)
	}
}
