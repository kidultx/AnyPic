package main

import (
	"path/filepath"
	"testing"
)

// TestCompressIDsLeavesOtherReadyImagesAlone is the pipeline's scoping rule:
// with auto-process off, compressing a capture must not also compress files
// the user deliberately left waiting.
func TestCompressIDsLeavesOtherReadyImagesAlone(t *testing.T) {
	srcDir := t.TempDir()
	writeJPEG(t, filepath.Join(srcDir, "one.jpg"), gradientImage(320, 240, false), 92)
	writeJPEG(t, filepath.Join(srcDir, "two.jpg"), gradientImage(320, 240, false), 92)

	service := newTestService(t)
	result, err := service.AddPaths([]string{srcDir})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 2 {
		t.Fatalf("added %d images, want 2: %+v", result.Added, result.Skipped)
	}
	waitFor(t, "thumbnails", func() bool {
		state := service.State()
		if len(state.Items) != 2 {
			return false
		}
		for _, item := range state.Items {
			if item.Thumb == "" {
				return false
			}
		}
		return true
	})

	state := service.State()
	compressed := state.Items[0].ID
	waiting := state.Items[1].ID
	if err := service.CompressIDs([]int64{compressed}, Settings{
		Format:  "webp",
		Quality: 70,
		Suffix:  "-scoped",
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "scoped compression", func() bool {
		state := service.State()
		return !state.Batch.Running && state.Batch.Done == 1
	})

	for _, item := range service.State().Items {
		switch item.ID {
		case compressed:
			if item.Status != statusDone {
				t.Fatalf("requested image status = %s (%s), want done", item.Status, item.Error)
			}
		case waiting:
			if item.Status != statusReady {
				t.Fatalf("unrequested image status = %s, want ready", item.Status)
			}
		}
	}
}
