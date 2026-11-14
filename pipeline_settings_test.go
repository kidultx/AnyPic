package main

import "testing"

// Tests for the screenshot pipeline's opt-in default and migration. They live
// apart from target-size tests because the pipeline is a separate setting
// boundary: every write to the clipboard goes through it.

func TestNormalizeSettingsPipelineIsOptIn(t *testing.T) {
	if got := normalizeSettings(Settings{}).ScreenshotPipeline; got != "off" {
		t.Errorf("missing pipeline preference normalized to %q, want off", got)
	}
	if got := normalizeSettings(Settings{ScreenshotPipeline: "garbage"}).ScreenshotPipeline; got != "off" {
		t.Errorf("unknown pipeline preference normalized to %q, want off", got)
	}
	if got := normalizeSettings(Settings{ScreenshotPipeline: "on"}).ScreenshotPipeline; got != "on" {
		t.Errorf("explicit pipeline on normalized to %q, want on", got)
	}
	// A user who disabled capture copying must not get a pipeline write.
	got := normalizeSettings(Settings{ScreenshotPipeline: "on", ScreenshotClipboard: "off"})
	if got.ScreenshotPipeline != "off" {
		t.Errorf("pipeline with clipboard off normalized to %q, want off", got.ScreenshotPipeline)
	}
	if defaultSettings().ScreenshotPipeline != "off" {
		t.Error("default settings turn the pipeline on; it must be opt-in")
	}
}

// TestDefaultSettingsPipelineIsOff is the new-user guarantee: a settings file
// written for the first time must not make captures overwrite the clipboard.
func TestDefaultSettingsPipelineIsOff(t *testing.T) {
	settings := defaultSettings()
	if settings.ScreenshotPipeline != "off" {
		t.Fatalf("new-user ScreenshotPipeline = %q, want off", settings.ScreenshotPipeline)
	}
	if got := normalizeSettings(settings); got.ScreenshotPipeline != "off" {
		t.Fatalf("normalized new-user ScreenshotPipeline = %q, want off", got.ScreenshotPipeline)
	}
}

// TestPipelineMigrationKeepsClipboardOffUsersOff covers the upgrade path: an
// older settings file with capture copying already off has no pipeline field,
// and must not gain one.
func TestPipelineMigrationKeepsClipboardOffUsersOff(t *testing.T) {
	old := Settings{ScreenshotClipboard: "off"}
	if got := normalizeSettings(old); got.ScreenshotPipeline != "off" {
		t.Fatalf("migrated ScreenshotPipeline = %q, want off when the old clipboard preference was off", got.ScreenshotPipeline)
	}
	// A stored pipeline on must also lose to the explicit clipboard off.
	stale := Settings{ScreenshotClipboard: "off", ScreenshotPipeline: "on"}
	if got := normalizeSettings(stale); got.ScreenshotPipeline != "off" {
		t.Fatalf("migrated stale pipeline = %q, want off with clipboard off", got.ScreenshotPipeline)
	}
}
