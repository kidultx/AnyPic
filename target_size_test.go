package main

import (
	"bytes"
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/disintegration/imaging"
)

// targetFixture writes a noisy JPEG (expensive to encode, so the search has
// room to move) and returns its path and original size.
func targetFixture(t *testing.T) (string, int64) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.jpg")
	original := writeJPEG(t, path, noiseImage(480, 360, 11), 98)
	return path, original
}

func decodeFileImage(t *testing.T, path string) image.Image {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// TestTargetSizeSearchNeverExceedsTheCap: JPEG and WebP search their quality
// for the highest value whose output fits, and the reported result is the
// file that was actually written.
func TestTargetSizeSearchNeverExceedsTheCap(t *testing.T) {
	path, original := targetFixture(t)
	service := newTestService(t)

	for _, outFormat := range []string{"jpeg", "webp"} {
		outFormat := outFormat
		t.Run(outFormat, func(t *testing.T) {
			decoded := decodeFileImage(t, path)
			// Pick a target between the quality floor and the quality
			// ceiling, so reaching it takes a real search rather than the
			// first or last probe.
			floor, err := encodeStill(decoded, strings.ToUpper(outFormat), targetMinQuality)
			if err != nil {
				t.Fatal(err)
			}
			ceiling, err := encodeStill(decoded, strings.ToUpper(outFormat), targetMaxQuality)
			if err != nil {
				t.Fatal(err)
			}
			if int64(len(floor)) >= int64(len(ceiling)) {
				t.Fatalf("fixture does not exercise the search: floor %d, ceiling %d", len(floor), len(ceiling))
			}
			target := int64(len(floor)+len(ceiling)) / 2
			if target >= original {
				t.Fatalf("fixture target %d not below the original %d", target, original)
			}

			result, err := service.compressOne(context.Background(), 1, path, "JPEG", "photo.jpg", false, Settings{
				Format:     outFormat,
				Quality:    82,
				TargetSize: target,
				Suffix:     "-target",
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.targetMissed {
				t.Fatalf("target %d reported missed; floor encode was %d bytes", target, len(floor))
			}
			if result.size > target {
				t.Fatalf("wrote %d bytes over the %d-byte target", result.size, target)
			}
			if result.targetBytes != target {
				t.Fatalf("targetBytes = %d, want %d", result.targetBytes, target)
			}
			if result.ssim <= 0 || result.ssim > 1 {
				t.Fatalf("ssim = %v, want (0,1]", result.ssim)
			}
			if !result.ssimMeasured {
				t.Fatal("a still result was reported as not measured")
			}
			if result.targetQuality <= 0 {
				t.Fatalf("targetQuality = %d, want the searched quality", result.targetQuality)
			}
			atQuality, err := encodeStill(decoded, strings.ToUpper(outFormat), result.targetQuality)
			if err != nil {
				t.Fatal(err)
			}
			if int64(len(atQuality)) != result.size {
				t.Fatalf("result size %d is not the encode at quality %d (%d bytes)", result.size, result.targetQuality, len(atQuality))
			}
			if result.targetQuality < targetMaxQuality {
				higher, err := encodeStill(decoded, strings.ToUpper(outFormat), result.targetQuality+1)
				if err != nil {
					t.Fatal(err)
				}
				if int64(len(higher)) <= target {
					t.Fatalf("quality %d also fits (%d bytes), result used %d", result.targetQuality+1, len(higher), result.targetQuality)
				}
			}
			written, err := os.Stat(result.path)
			if err != nil {
				t.Fatal(err)
			}
			if written.Size() != result.size {
				t.Fatalf("reported %d bytes, file is %d", result.size, written.Size())
			}
		})
	}
}

// TestTargetSizeReportsTheSmallestWhenUnreachable: a cap below the quality
// floor is never silently abandoned — the smallest result is kept and marked.
func TestTargetSizeReportsTheSmallestWhenUnreachable(t *testing.T) {
	path, _ := targetFixture(t)
	service := newTestService(t)

	decoded := decodeFileImage(t, path)
	floor, err := encodeStill(decoded, "JPEG", targetMinQuality)
	if err != nil {
		t.Fatal(err)
	}
	target := int64(len(floor) / 2)
	if target < 1 {
		t.Fatal("fixture is too small")
	}

	result, err := service.compressOne(context.Background(), 1, path, "JPEG", "photo.jpg", false, Settings{
		Format:     "jpeg",
		Quality:    82,
		TargetSize: target,
		Suffix:     "-target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.targetMissed {
		t.Fatalf("target %d reported reached, but the floor encode is %d bytes", target, len(floor))
	}
	if result.size <= target {
		t.Fatalf("missed result is %d bytes, inside the %d-byte target", result.size, target)
	}
	if result.targetBytes != target {
		t.Fatalf("targetBytes = %d, want %d", result.targetBytes, target)
	}
	if result.ssim <= 0 {
		t.Fatalf("smallest result was not measured: ssim %v", result.ssim)
	}
	if !result.ssimMeasured {
		t.Fatal("the smallest result was reported as not measured")
	}
	if result.targetQuality <= 0 {
		t.Fatalf("targetQuality = %d, want the smallest probed quality", result.targetQuality)
	}
	if result.targetQuality >= targetMaxQuality {
		t.Fatalf("missed target used quality %d, want a searched value", result.targetQuality)
	}
}

// TestPNGTargetOnlyReportsTheMiss: PNG is lossless and has no quality knob,
// so target mode cannot tune it; the page must be told the result is larger.
func TestPNGTargetOnlyReportsTheMiss(t *testing.T) {
	path, _ := targetFixture(t)
	service := newTestService(t)

	result, err := service.compressOne(context.Background(), 1, path, "JPEG", "photo.jpg", false, Settings{
		Format:     "png",
		Quality:    82,
		TargetSize: 1024,
		Suffix:     "-target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.format != "PNG" {
		t.Fatalf("output format = %s, want PNG", result.format)
	}
	if !result.targetMissed {
		t.Fatal("a lossless PNG larger than 1 KB reported reaching the target")
	}
	if result.size <= 1024 {
		t.Fatalf("PNG result is %d bytes, expected over the 1 KB target", result.size)
	}
	if result.ssim <= 0 {
		t.Fatalf("PNG result was not measured: ssim %v", result.ssim)
	}
	if !result.ssimMeasured {
		t.Fatal("PNG result was reported as not measured")
	}
	if result.targetQuality != 0 {
		t.Fatalf("PNG targetQuality = %d, want 0: PNG has no quality knob", result.targetQuality)
	}
}

// TestTargetSizeFormatMatrix walks every output format through target mode.
// JPG and WebP search quality and must fit; PNG is lossless, so the same mode
// can only report the miss honestly. This is the test that keeps PNG in the
// traversal instead of leaving it to a single hand-written case.
func TestTargetSizeFormatMatrix(t *testing.T) {
	path, _ := targetFixture(t)
	service := newTestService(t)
	decoded := decodeFileImage(t, path)

	type formatCase struct {
		format      string
		target      int64
		wantMissed  bool
		wantQuality bool
	}
	var cases []formatCase
	for _, format := range []string{"jpeg", "webp"} {
		floor, err := encodeStill(decoded, strings.ToUpper(format), targetMinQuality)
		if err != nil {
			t.Fatal(err)
		}
		ceiling, err := encodeStill(decoded, strings.ToUpper(format), targetMaxQuality)
		if err != nil {
			t.Fatal(err)
		}
		if len(floor) >= len(ceiling) {
			t.Fatalf("%s fixture does not exercise the search: floor %d, ceiling %d", format, len(floor), len(ceiling))
		}
		cases = append(cases, formatCase{
			format:      format,
			target:      int64(len(floor)+len(ceiling)) / 2,
			wantQuality: true,
		})
	}
	pngBytes, err := encodeStill(decoded, "PNG", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pngBytes) < 2048 {
		t.Fatalf("PNG fixture is only %d bytes; it cannot demonstrate a miss", len(pngBytes))
	}
	cases = append(cases, formatCase{
		format:     "png",
		target:     int64(len(pngBytes) / 2),
		wantMissed: true,
	})

	for _, tc := range cases {
		tc := tc
		t.Run(tc.format, func(t *testing.T) {
			result, err := service.compressOne(context.Background(), 1, path, "JPEG", "photo.jpg", false, Settings{
				Format:     tc.format,
				Quality:    82,
				TargetSize: tc.target,
				Suffix:     "-matrix",
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.targetMissed != tc.wantMissed {
				t.Fatalf("targetMissed = %v, want %v for %s at %d bytes (result %d)", result.targetMissed, tc.wantMissed, tc.format, tc.target, result.size)
			}
			if !tc.wantMissed && result.size > tc.target {
				t.Fatalf("%s wrote %d bytes over its %d-byte target", tc.format, result.size, tc.target)
			}
			if tc.wantMissed && result.size <= tc.target {
				t.Fatalf("%s reported a miss at %d bytes, inside the %d-byte target", tc.format, result.size, tc.target)
			}
			if tc.wantQuality && result.targetQuality <= 0 {
				t.Fatalf("%s targetQuality = %d, want the searched quality", tc.format, result.targetQuality)
			}
			if !tc.wantQuality && result.targetQuality != 0 {
				t.Fatalf("%s targetQuality = %d, want 0: no quality knob", tc.format, result.targetQuality)
			}
			if !result.ssimMeasured {
				t.Fatalf("%s result was reported as not measured", tc.format)
			}
		})
	}
}

// TestTargetSizeKeepsAnOriginalAlreadyInsideTheCap: re-encoding a file that
// already fits would only lose quality, so the original stays the result.
func TestTargetSizeKeepsAnOriginalAlreadyInsideTheCap(t *testing.T) {
	path, original := targetFixture(t)
	service := newTestService(t)

	result, err := service.compressOne(context.Background(), 1, path, "JPEG", "photo.jpg", false, Settings{
		Format:     "auto",
		Quality:    82,
		TargetSize: original + 4096,
		Suffix:     "-target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.passedThrough {
		t.Fatal("an image already under its target was re-encoded")
	}
	if result.path != path {
		t.Fatalf("result path = %s, want the original %s", result.path, path)
	}
	if result.targetMissed {
		t.Fatal("an image under its target was marked missed")
	}
	if result.ssim != 1 {
		t.Fatalf("passed-through ssim = %v, want 1", result.ssim)
	}
	if !result.ssimMeasured {
		t.Fatal("passed-through result should report an exact score, not unmeasured")
	}
	if result.targetQuality != 0 {
		t.Fatalf("passed-through targetQuality = %d, want 0 (no encode ran)", result.targetQuality)
	}
}

// TestTargetSizeAnimatedHonorsTheCap: an animation has a WebP quality knob
// too, so a target cap applies to it while keeping every frame; a cap below
// the floor is reported rather than silently ignored. The quality search
// itself is covered by TestSearchEncode*; here the cap equals the q95 result,
// which the encoder can reach.
func TestTargetSizeAnimatedHonorsTheCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anim.gif")
	writeAnimatedGIF(t, path, 4, 320, 240)
	service := newTestService(t)

	// q95 measured on the same resized frames gives a cap the encoder can
	// reach; the target pass must honor it without dropping frames.
	baseline, err := service.compressOne(context.Background(), 1, path, "GIF", "anim.gif", true, Settings{
		Format: "auto", Quality: 95, MaxDimension: 160, Suffix: "-anim",
	})
	if err != nil {
		t.Fatal(err)
	}
	target := baseline.size

	result, err := service.compressOne(context.Background(), 2, path, "GIF", "anim.gif", true, Settings{
		Format: "auto", Quality: 82, MaxDimension: 160, TargetSize: target, Suffix: "-anim",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.targetMissed {
		t.Fatalf("target %d reported missed; q95 produced %d bytes", target, baseline.size)
	}
	if result.size > target {
		t.Fatalf("animation wrote %d bytes over the %d-byte target", result.size, target)
	}
	if result.targetBytes != target {
		t.Fatalf("targetBytes = %d, want %d", result.targetBytes, target)
	}
	if result.format != "WEBP" {
		t.Fatalf("animated output = %s, want WEBP", result.format)
	}
	if result.ssim != 0 {
		t.Fatalf("animated ssim = %v, want 0 (not measured)", result.ssim)
	}
	if result.ssimMeasured {
		t.Fatal("animated result was reported as measured")
	}
	if result.targetQuality <= 0 {
		t.Fatalf("animated targetQuality = %d, want the searched quality", result.targetQuality)
	}

	miss, err := service.compressOne(context.Background(), 3, path, "GIF", "anim.gif", true, Settings{
		Format: "auto", Quality: 82, MaxDimension: 160, TargetSize: 100, Suffix: "-anim",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !miss.targetMissed || miss.size <= 100 {
		t.Fatalf("tiny target: missed=%v size=%d, want an honest miss above 100 bytes", miss.targetMissed, miss.size)
	}
	if miss.targetBytes != 100 {
		t.Fatalf("tiny target: targetBytes = %d, want 100", miss.targetBytes)
	}
	if miss.targetQuality <= 0 {
		t.Fatalf("tiny target: targetQuality = %d, want the smallest probed quality", miss.targetQuality)
	}
}

// TestTargetSizePassthroughMissHasNoQuality: when every re-encode is larger
// than an over-target original, the original is kept and the miss is honest.
// No quality ran, so TargetQuality stays 0 and the page says "original kept"
// instead of pretending a lossless encode produced it.
func TestTargetSizePassthroughMissHasNoQuality(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "already-small.jpg")
	original := writeJPEG(t, path, noiseImage(480, 360, 21), 1)
	target := original - 1
	if target < 1024 {
		t.Fatalf("fixture original %d is too small for a valid target", original)
	}
	service := newTestService(t)

	result, err := service.compressOne(context.Background(), 1, path, "JPEG", "already-small.jpg", false, Settings{
		Format:     "jpeg",
		Quality:    82,
		TargetSize: target,
		Suffix:     "-target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.passedThrough {
		t.Fatalf("result was re-encoded (%d bytes vs original %d); fixture no longer exercises passthrough", result.size, original)
	}
	if !result.targetMissed {
		t.Fatalf("passthrough original %d exceeds the %d-byte target but no miss was reported", original, target)
	}
	if result.targetQuality != 0 {
		t.Fatalf("passthrough miss targetQuality = %d, want 0 (no encode ran)", result.targetQuality)
	}
}

func TestNormalizeSettingsTargetSize(t *testing.T) {
	if got := normalizeSettings(Settings{TargetSize: -5}).TargetSize; got != 0 {
		t.Errorf("negative target normalized to %d, want 0", got)
	}
	if got := normalizeSettings(Settings{TargetSize: 500}).TargetSize; got != 1024 {
		t.Errorf("sub-kilobyte target normalized to %d, want 1024", got)
	}
	if got := normalizeSettings(Settings{TargetSize: maxFileSize + 1}).TargetSize; got != maxFileSize {
		t.Errorf("oversized target normalized to %d, want %d", got, maxFileSize)
	}
	if got := normalizeSettings(Settings{TargetSize: 2 << 20}).TargetSize; got != 2<<20 {
		t.Errorf("valid target normalized to %d", got)
	}
}
