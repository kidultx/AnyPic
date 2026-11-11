package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// DefaultScreenshotShortcut opens the desktop's region capture and adds the
// result to the queue.
const DefaultScreenshotShortcut = "CmdOrCtrl+Alt+S"

var (
	errCaptureCanceled = errors.New("已取消截图")
	errCaptureBusy     = errors.New("正在截图中")
	// errScreenCapturePermission is matched by its text in src/main.ts, which
	// then offers the system settings dialog instead of a generic error. Keep
	// the Go error and the page check in sync (TestIssue17Page... guards it).
	errScreenCapturePermission = errors.New("需要屏幕录制权限")
	captureBusy                atomic.Bool
	shortcutMu                 sync.Mutex
)

// Seams for tests: the platform check, the two TCC calls, the capture plan,
// the command runner and the window hand-off. Production uses the real
// implementations above and below.
var (
	screenshotGOOS             = runtime.GOOS
	screenCapturePreflight     = preflightScreenCaptureAccess
	screenCaptureRequestAccess = requestScreenCaptureAccess
	screenshotPlanFor          = screenshotPlan
	runCaptureCommand          = func(cmd *exec.Cmd) error { return cmd.Run() }
	startCaptureCommand        = func(cmd *exec.Cmd) error { return cmd.Start() }
	focusScreenshotWindow      = focusCaptureWindow
)

// screenshotRequested asks the page to run a capture, so the queue, toasts and
// settings all update through one code path.
func screenshotRequested() {
	_ = CommandRequested.Broadcast("screenshot")
}

// replaceScreenshotShortcut moves the global binding from prev to next. When
// the new accelerator is taken, prev is bound again so the shortcut keeps
// working.
func replaceScreenshotShortcut(prev, next string) error {
	shortcutMu.Lock()
	defer shortcutMu.Unlock()
	if prev != "" {
		mygo.GlobalShortcut.Unregister(prev)
	}
	if err := mygo.GlobalShortcut.Register(next, screenshotRequested); err != nil {
		if prev != "" && prev != next {
			_ = mygo.GlobalShortcut.Register(prev, screenshotRequested)
		}
		return err
	}
	return nil
}

// CaptureScreenshot runs the desktop's region capture, saves the PNG under
// Pictures/AnyPic and adds it to the queue. A canceled capture returns
// errCaptureCanceled so the page can stay quiet; a missing macOS screen
// recording permission returns errScreenCapturePermission so the page can
// offer the system settings.
func (a *AnyPic) CaptureScreenshot(ctx context.Context) (result AddResult, err error) {
	if !captureBusy.CompareAndSwap(false, true) {
		return AddResult{}, errCaptureBusy
	}
	defer captureBusy.Store(false)
	// The global shortcut can fire while AnyPic is behind another app or
	// hidden. Every failure except a cancel (and a second press while a
	// capture is open) brings the window forward, or the page's message is
	// never seen.
	defer func() {
		if err != nil && !errors.Is(err, errCaptureCanceled) {
			focusScreenshotWindow(ctx)
		}
	}()

	if err = requireScreenCaptureAccess(); err != nil {
		return AddResult{}, err
	}

	data, viaClipboard, err := captureRegion(ctx)
	if err != nil {
		return AddResult{}, err
	}

	// The clipboard, auto-process and appearance preferences are read in one
	// locked snapshot: one capture, one consistent set. Beautify is a
	// deliberate pass in the preview workbench, never applied to captures
	// implicitly.
	a.mu.Lock()
	copyToClipboard := a.settings.ScreenshotClipboard != "off"
	autoProcess := a.settings.AutoProcess == "on"
	a.mu.Unlock()

	// Copying is explicit. When the capture tool itself wrote to the
	// clipboard (Windows/Linux) and the preference is off, clear it again.
	// A file-based capture never touches the clipboard in the first place.
	//
	// With auto-process on, the page copies the compressed result once the
	// pass finishes, so the capture is not put on the clipboard twice. The
	// tool-provided copy stays meanwhile as a fallback.
	copied := false
	if copyToClipboard && !autoProcess {
		copied = mygo.Clipboard.WriteImage(data) == nil
	} else if !copyToClipboard && viaClipboard {
		mygo.Clipboard.Clear()
	}

	path, err := writeScreenshot(data)
	if err != nil {
		return AddResult{}, fmt.Errorf("截图已生成，但无法保存：%w", err)
	}
	result, err = a.AddPaths([]string{path})
	if err != nil {
		return AddResult{}, fmt.Errorf("截图已保存到 %s，但无法加入队列：%w", path, err)
	}
	result.Copied = copied

	// The shortcut may have fired while the app was behind another window;
	// bring it forward now that the capture is done. The zero-click flow
	// leaves the app in the background instead: the user is about to paste,
	// not to open the queue.
	if !(autoProcess && copyToClipboard) {
		focusScreenshotWindow(ctx)
	}
	return result, nil
}

// focusCaptureWindow brings the app forward so a capture result or error is
// visible even when the global shortcut fired in the background.
func focusCaptureWindow(ctx context.Context) {
	win := mygo.CallerWindow(ctx)
	if win == nil {
		return
	}
	if win.IsMinimized() {
		win.Restore()
	}
	win.Show()
	win.Focus()
}

// requireScreenCaptureAccess checks macOS's screen recording permission
// before the capture command runs. Without it screencapture can return only
// the desktop wallpaper, and the user has no idea why. A failed symbol
// lookup keeps the old behavior (try the capture).
func requireScreenCaptureAccess() error {
	if screenshotGOOS != "darwin" {
		return nil
	}
	allowed, err := screenCapturePreflight()
	if err != nil || allowed {
		return nil
	}
	// The first call adds AnyPic to the system list and shows the prompt.
	screenCaptureRequestAccess()
	return errScreenCapturePermission
}

// OpenScreenRecordingSettings opens the macOS privacy pane where the user
// grants screen recording access. Capturing other apps' windows without it
// only returns the desktop wallpaper.
func (a *AnyPic) OpenScreenRecordingSettings() error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return mygo.Shell.OpenExternal("x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture")
}

// screenshotCommand is one way to ask the desktop for a region capture.
// blocking reports whether the command returns only after the user finishes
// the selection; clipboard says the result arrives on the clipboard rather
// than in a file.
type screenshotCommand struct {
	name      string
	args      []string
	blocking  bool
	clipboard bool
}

func screenshotPlan() (screenshotCommand, error) {
	switch runtime.GOOS {
	case "darwin":
		// -i picks a region or window, -x silences the shutter sound. Without
		// -c the shot is written to a temp file: reading the clipboard to fetch
		// it would trip macOS's paste permission prompt on every capture.
		return screenshotCommand{"/usr/sbin/screencapture", []string{"-i", "-x"}, true, false}, nil
	case "windows":
		// The Snipping Tool overlay from Win+Shift+S; the snip lands on the
		// clipboard. Supported on Windows 10 1809 and later.
		return screenshotCommand{"cmd", []string{"/c", "start", "", "ms-screenclip:"}, false, true}, nil
	}

	for _, candidate := range []screenshotCommand{
		{"gnome-screenshot", []string{"-a", "-c"}, false, true},
		{"spectacle", []string{"-r", "-b", "-n", "-c"}, false, true},
		{"xfce4-screenshooter", []string{"-r", "-c"}, false, true},
	} {
		if _, err := exec.LookPath(candidate.name); err == nil {
			return candidate, nil
		}
	}
	if hasCommands("grim", "slurp", "wl-copy") {
		return screenshotCommand{"sh", []string{"-c", `grim -g "$(slurp)" - | wl-copy`}, false, true}, nil
	}
	return screenshotCommand{}, errors.New("没有找到可用的截图工具，可安装 gnome-screenshot、spectacle 或 grim + slurp + wl-copy")
}

func hasCommands(names ...string) bool {
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			return false
		}
	}
	return true
}

// captureRegion runs the capture plan. It returns the PNG and whether the
// image was fetched from the clipboard; file-based captures never touch it,
// which avoids macOS's paste permission prompt.
func captureRegion(ctx context.Context) ([]byte, bool, error) {
	plan, err := screenshotPlanFor()
	if err != nil {
		return nil, false, err
	}

	cmd := exec.Command(plan.name, plan.args...)
	if !plan.clipboard {
		// The tool writes the shot to a file we point it at; read it there
		// instead of going through the clipboard, which on macOS would ask for
		// the paste permission every time.
		data, err := captureToFile(cmd, plan)
		if err != nil {
			return nil, false, err
		}
		return data, false, nil
	}

	baseline := clipboardImageDigest()
	if plan.blocking {
		if err := runCaptureCommand(cmd); err != nil {
			return nil, false, errCaptureCanceled
		}
		if data := waitForNewImage(ctx, baseline, 3*time.Second); data != nil {
			return data, true, nil
		}
		return nil, false, errCaptureCanceled
	}

	if err := startCaptureCommand(cmd); err != nil {
		return nil, false, fmt.Errorf("无法启动截图工具：%w", err)
	}
	go func() { _ = cmd.Wait() }()
	// These tools return as soon as their overlay opens, so the wait has to
	// cover a leisurely selection; canceling means waiting it out.
	if data := waitForNewImage(ctx, baseline, 30*time.Second); data != nil {
		return data, true, nil
	}
	return nil, false, errCaptureCanceled
}

// captureToFile runs a file-writing capture command and returns the image it
// wrote. The temporary folder handed to the tool is removed on every path —
// success, cancel and failure alike (#18): it used to survive as an empty
// anypic-shot-* folder in $TMPDIR, one per capture, uncleaned at exit.
func captureToFile(cmd *exec.Cmd, plan screenshotCommand) ([]byte, error) {
	if plan.name != "/usr/sbin/screencapture" {
		// Generic fallback: watch the default screenshot folder for a new file.
		return nil, fmt.Errorf("截图工具 %s 不支持输出到文件", plan.name)
	}
	dir, err := os.MkdirTemp("", "anypic-shot-")
	if err != nil {
		return nil, fmt.Errorf("无法创建临时目录：%w", err)
	}
	defer os.RemoveAll(dir)

	// screencapture writes straight to the path given as an argument.
	path := filepath.Join(dir, "shot.png")
	cmd.Args = append(cmd.Args, path)
	cmd.Path = plan.name
	if err := runCaptureCommand(cmd); err != nil {
		return nil, errCaptureCanceled
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, errCaptureCanceled
	}
	return data, nil
}

// waitForNewImage polls the clipboard until an image that differs from the
// one there before the capture appears.
func waitForNewImage(ctx context.Context, baseline [32]byte, timeout time.Duration) []byte {
	deadline := time.Now().Add(timeout)
	for {
		data := mygo.Clipboard.ReadImage()
		if len(data) > 0 && sha256.Sum256(data) != baseline {
			return data
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func clipboardImageDigest() [32]byte {
	return sha256.Sum256(mygo.Clipboard.ReadImage())
}

// writeScreenshot stores the capture in Pictures/AnyPic, a folder the user
// can find later, and never overwrites an earlier shot.
func writeScreenshot(data []byte) (string, error) {
	base, err := mygo.App.Path(mygo.PathPictures)
	if err != nil || base == "" {
		base, err = mygo.App.Path(mygo.PathUserData)
		if err != nil {
			return "", err
		}
	}
	dir := filepath.Join(base, "AnyPic")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	stamp := time.Now().Format("2006-01-02 15.04.05")
	path := filepath.Join(dir, "截图 "+stamp+".png")
	for n := 2; ; n++ {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("截图 %s (%d).png", stamp, n))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// cleanupStaleShotDirs removes anypic-shot-* folders an earlier launch (or an
// earlier version, which never removed them) left in the temp directory. It
// runs at start, when no capture of ours can be in flight, and touches only
// folders carrying this app's own prefix (#18).
func cleanupStaleShotDirs() {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "anypic-shot-*"))
	if err != nil {
		return
	}
	for _, path := range matches {
		_ = os.RemoveAll(path)
	}
}
