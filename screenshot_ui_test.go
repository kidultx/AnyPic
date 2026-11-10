package main

import (
	"strings"
	"testing"
)

// TestIssue17PageHandlesScreenPermission guards the page half of #17: the Go
// side identifies a missing macOS screen recording permission by its error
// text, so the page must look for exactly that text and offer the system
// settings instead of falling into the quiet capture-notice path or a bare
// toast.
func TestIssue17PageHandlesScreenPermission(t *testing.T) {
	main := ruleSources(t)["src/main.ts"]
	permissionText := errScreenCapturePermission.Error()

	notice, _, _, ok := functionBody(main, "isCaptureNotice")
	if !ok {
		t.Fatalf("src/main.ts: no isCaptureNotice function, the quiet-capture rule cannot be checked (#17)")
	}
	if strings.Contains(notice, permissionText) {
		t.Errorf("src/main.ts: isCaptureNotice matches %q, so the permission error would be swallowed (#17)", permissionText)
	}

	check, _, _, ok := functionBody(main, "isScreenCapturePermission")
	if !ok {
		t.Fatalf("src/main.ts: no isScreenCapturePermission function, the permission error text has no page-side match (#17)")
	}
	if !strings.Contains(check, permissionText) {
		t.Errorf("src/main.ts: isScreenCapturePermission does not look for %q, the exact text Go sends (#17)", permissionText)
	}

	help, _, _, ok := functionBody(main, "showScreenCapturePermission")
	if !ok {
		t.Fatalf("src/main.ts: no showScreenCapturePermission function, a permission error has nowhere to explain itself (#17)")
	}
	if !strings.Contains(help, "askConfirm(") {
		t.Errorf("src/main.ts: showScreenCapturePermission does not use askConfirm (#17)")
	}
	if !strings.Contains(help, "openScreenRecordingSettings(") {
		t.Errorf("src/main.ts: showScreenCapturePermission never opens the screen recording settings (#17)")
	}
	for _, want := range []string{"屏幕与系统录音", "屏幕录制", "开关已经打开", "删掉", "退出", "重新打开"} {
		if !strings.Contains(help, want) {
			t.Errorf("src/main.ts: permission help does not mention %q; the #17 copy must cover the settings name, the stale entry and the restart", want)
		}
	}

	capture, _, _, ok := functionBody(main, "captureScreenshot")
	if !ok {
		t.Fatalf("src/main.ts: no captureScreenshot function (#17)")
	}
	checkAt := strings.Index(capture, "isScreenCapturePermission(")
	helpAt := strings.Index(capture, "showScreenCapturePermission(")
	failAt := strings.LastIndex(capture, "showError(")
	if checkAt < 0 || helpAt < 0 {
		t.Fatalf("src/main.ts: captureScreenshot does not route a permission error to showScreenCapturePermission (#17):\n%s", capture)
	}
	if failAt < 0 || helpAt > failAt || checkAt > failAt {
		t.Errorf("src/main.ts: captureScreenshot handles the permission error after its generic showError fallback (#17)")
	}
}
