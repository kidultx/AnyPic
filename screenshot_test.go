package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
)

func TestWriteScreenshotKeepsEveryCapture(t *testing.T) {
	pictures := t.TempDir()
	mygo.App.SetPath(mygo.PathPictures, pictures)
	t.Cleanup(func() { mygo.App.SetPath(mygo.PathPictures, "") })

	first, err := writeScreenshot([]byte("first"))
	if err != nil {
		t.Fatalf("writeScreenshot: %v", err)
	}
	second, err := writeScreenshot([]byte("second"))
	if err != nil {
		t.Fatalf("writeScreenshot: %v", err)
	}
	if first == second {
		t.Fatalf("two captures in the same second overwrote each other: %s", first)
	}

	wantDir := filepath.Join(pictures, "AnyPic")
	for path, want := range map[string]string{first: "first", second: "second"} {
		if filepath.Dir(path) != wantDir {
			t.Errorf("%s is outside %s", path, wantDir)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(data) != want {
			t.Errorf("%s holds %q, want %q", path, data, want)
		}
	}
}

func TestScreenshotPlanExists(t *testing.T) {
	plan, err := screenshotPlan()
	if err != nil {
		t.Skipf("no screenshot tool on this machine: %v", err)
	}
	if plan.name == "" {
		t.Fatal("screenshot plan has no command")
	}
	if _, err := exec.LookPath(plan.name); err != nil && !filepath.IsAbs(plan.name) {
		t.Fatalf("screenshot command %q is not available: %v", plan.name, err)
	}
}

func TestNormalizeSettingsKeepsScreenshotShortcut(t *testing.T) {
	if got := normalizeSettings(Settings{}).ScreenshotShortcut; got != DefaultScreenshotShortcut {
		t.Errorf("empty shortcut normalized to %q, want %q", got, DefaultScreenshotShortcut)
	}
	const custom = "Cmd+Alt+X"
	if got := normalizeSettings(Settings{ScreenshotShortcut: custom}).ScreenshotShortcut; got != custom {
		t.Errorf("custom shortcut normalized to %q, want %q", got, custom)
	}
}

func TestNormalizeSettingsScreenshotClipboard(t *testing.T) {
	if got := normalizeSettings(Settings{}).ScreenshotClipboard; got != "on" {
		t.Errorf("empty clipboard preference normalized to %q, want on", got)
	}
	if got := normalizeSettings(Settings{ScreenshotClipboard: "off"}).ScreenshotClipboard; got != "off" {
		t.Errorf("off preference normalized to %q, want off", got)
	}
	if got := normalizeSettings(Settings{ScreenshotClipboard: "bogus"}).ScreenshotClipboard; got != "on" {
		t.Errorf("invalid clipboard preference normalized to %q, want on", got)
	}
}

func TestNormalizeSettingsHideDockWhenTray(t *testing.T) {
	if got := normalizeSettings(Settings{}).HideDockWhenTray; got != "on" {
		t.Errorf("empty dock preference normalized to %q, want on", got)
	}
	if got := normalizeSettings(Settings{HideDockWhenTray: "off"}).HideDockWhenTray; got != "off" {
		t.Errorf("off preference normalized to %q, want off", got)
	}
	if got := normalizeSettings(Settings{HideDockWhenTray: "bogus"}).HideDockWhenTray; got != "on" {
		t.Errorf("invalid dock preference normalized to %q, want on", got)
	}
}

func TestNormalizeSettingsOpenAtLogin(t *testing.T) {
	if got := normalizeSettings(Settings{}).OpenAtLogin; got != "off" {
		t.Errorf("empty login preference normalized to %q, want off", got)
	}
	if got := normalizeSettings(Settings{OpenAtLogin: "on"}).OpenAtLogin; got != "on" {
		t.Errorf("on preference normalized to %q, want on", got)
	}
	if got := normalizeSettings(Settings{OpenAtLogin: "bogus"}).OpenAtLogin; got != "off" {
		t.Errorf("invalid login preference normalized to %q, want off", got)
	}
}
