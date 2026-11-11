package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// issue18ShotDirs lists the capture temp folders a test's TMPDIR still holds.
func issue18ShotDirs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "anypic-shot-") {
			dirs = append(dirs, entry.Name())
		}
	}
	return dirs
}

// issue18Capture runs one file-based capture with TMPDIR pointed at a test
// folder, so nothing can hide in the developer's real $TMPDIR.
func issue18Capture(t *testing.T, run func(*exec.Cmd) error) []string {
	t.Helper()
	issue17Seams(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	screenshotPlanFor = issue17Plan
	if run == nil {
		run = issue17Runner(issue17PNG(t))
	}
	runCaptureCommand = run
	if _, _, err := captureRegion(context.Background()); err != nil && err != errCaptureCanceled {
		t.Fatalf("captureRegion: %v", err)
	}
	return issue18ShotDirs(t, tmp)
}

// TestIssue18CaptureLeavesNoTempDir guards #18: every file-based capture used
// to create an anypic-shot-* folder in $TMPDIR and remove only the PNG inside,
// so each shot — and each canceled shot — left an empty folder behind that
// survived quitting the app.
func TestIssue18CaptureLeavesNoTempDir(t *testing.T) {
	if left := issue18Capture(t, nil); len(left) > 0 {
		t.Errorf("a successful capture left %d temp folder(s) behind: %v (#18)", len(left), left)
	}

	if left := issue18Capture(t, func(*exec.Cmd) error { return issue17ExitError{} }); len(left) > 0 {
		t.Errorf("a canceled capture left %d temp folder(s) behind: %v (#18)", len(left), left)
	}
}

// TestIssue18UnsupportedToolLeavesNoTempDir covers the path that fails before
// any file exists: the folder is created first, then the tool turns out not to
// support writing to a file.
func TestIssue18UnsupportedToolLeavesNoTempDir(t *testing.T) {
	issue17Seams(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	screenshotPlanFor = func() (screenshotCommand, error) {
		return screenshotCommand{name: "/usr/bin/gnome-screenshot", args: []string{"-a"}, blocking: true}, nil
	}
	runCaptureCommand = func(*exec.Cmd) error { return nil }
	if _, _, err := captureRegion(context.Background()); err == nil || err == errCaptureCanceled {
		t.Fatalf("unsupported tool return: got %v, want a plain error", err)
	}
	if left := issue18ShotDirs(t, tmp); len(left) > 0 {
		t.Errorf("the unsupported-tool path left %d temp folder(s) behind: %v (#18)", len(left), left)
	}
}

// TestIssue18StartupSweepsOldShotDirs covers the folders earlier versions
// already left behind: the fix cannot remove them retroactively, so the app
// sweeps anypic-shot-* from its temp directory at start, touching nothing else.
func TestIssue18StartupSweepsOldShotDirs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	stale := filepath.Join(tmp, "anypic-shot-123456")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(tmp, "someone-elses-dir")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}

	cleanupStaleShotDirs()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("anypic-shot-123456 survived the startup sweep (#18)")
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("the sweep removed a directory it did not create (#18): %v", err)
	}
}
