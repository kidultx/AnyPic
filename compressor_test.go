package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gen2brain/webp"
)

func gradientImage(width, height int, alpha bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			a := uint8(255)
			if alpha {
				a = uint8(80 + 175*x/max(1, width-1))
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(40 + 200*x/max(1, width-1)),
				G: uint8(30 + 180*y/max(1, height-1)),
				B: uint8(90 + 100*(x+y)/max(1, width+height-2)),
				A: a,
			})
		}
	}
	return img
}

func writeJPEG(t *testing.T, path string, img image.Image, quality int) int64 {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func writePNG(t *testing.T, path string, img image.Image) int64 {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestCompressJPEGProducesSmallerFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	original := writeJPEG(t, src, gradientImage(1200, 800, false), 96)

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 1, src, "JPEG", "photo.jpg", Settings{
		Format: "jpeg", Quality: 55, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.passedThrough {
		t.Fatal("expected a re-encoded JPEG, got passthrough")
	}
	if result.size >= original {
		t.Fatalf("compressed size %d is not smaller than original %d", result.size, original)
	}
	if result.outputName != "photo-anypic.jpg" {
		t.Fatalf("output name = %q", result.outputName)
	}
	f, err := os.Open(result.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" || cfg.Width != 1200 || cfg.Height != 800 {
		t.Fatalf("got %s %dx%d", format, cfg.Width, cfg.Height)
	}
}

func TestCompressResizesLongEdge(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "wide.png")
	writePNG(t, src, gradientImage(1600, 900, false))

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 2, src, "PNG", "wide.png", Settings{
		Format: "jpeg", Quality: 80, MaxDimension: 800, Suffix: "-min",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.width != 800 || result.height != 450 {
		t.Fatalf("resized to %dx%d, want 800x450", result.width, result.height)
	}
	if result.outputName != "wide-min.jpg" {
		t.Fatalf("output name = %q", result.outputName)
	}
}

func TestPNGKeepsTransparency(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "alpha.png")
	writePNG(t, src, gradientImage(320, 240, true))

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 3, src, "PNG", "alpha.png", Settings{
		Format: "auto", Quality: 82, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.hasAlpha {
		t.Fatal("expected alpha to survive")
	}
	if result.format != "PNG" {
		t.Fatalf("format = %s, want PNG", result.format)
	}
	f, err := os.Open(result.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoded, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := decoded.At(10, 10).RGBA(); a >= 0xffff {
		t.Fatal("expected a transparent pixel")
	}
}

func TestJPEGPassthroughWhenOriginalIsBetter(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tiny.jpg")
	original := writeJPEG(t, src, gradientImage(800, 600, false), 30)

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 4, src, "JPEG", "tiny.jpg", Settings{
		Format: "jpeg", Quality: 100, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.passedThrough {
		t.Fatalf("expected passthrough, got %d bytes (original %d)", result.size, original)
	}
	if result.path != src {
		t.Fatalf("passthrough path = %q, want %q", result.path, src)
	}
}

func TestWebPOutput(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.png")
	writePNG(t, src, gradientImage(900, 600, false))

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 5, src, "PNG", "photo.png", Settings{
		Format: "webp", Quality: 72, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.format != "WEBP" || result.outputName != "photo-anypic.webp" {
		t.Fatalf("got format %s, name %s", result.format, result.outputName)
	}
	f, err := os.Open(result.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if format != "webp" {
		t.Fatalf("decoded format = %s", format)
	}
}

func TestAlphaFlattenedForJPEG(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "alpha.png")
	writePNG(t, src, gradientImage(400, 300, true))

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 6, src, "PNG", "alpha.png", Settings{
		Format: "jpeg", Quality: 82, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.hasAlpha {
		t.Fatal("JPEG output must not have alpha")
	}
}

func TestCollectImagesRecursesAndSkips(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJPEG(t, filepath.Join(dir, "one.jpg"), gradientImage(20, 20, false), 80)
	writePNG(t, filepath.Join(dir, "nested", "two.png"), gradientImage(20, 20, false))
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, skipped := collectImages([]string{dir})
	if len(paths) != 2 {
		t.Fatalf("got %d images: %v", len(paths), paths)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skipped files: %v", skipped)
	}

	paths, skipped = collectImages([]string{filepath.Join(dir, "notes.txt")})
	if len(paths) != 0 || len(skipped) != 1 {
		t.Fatalf("paths=%v skipped=%v", paths, skipped)
	}
}

func TestNormalizeSettings(t *testing.T) {
	s := normalizeSettings(Settings{Format: "gif", Quality: 200, MaxDimension: 4, Suffix: "../../bad", Theme: "neon"})
	if s.Format != "auto" || s.Quality != 100 || s.MaxDimension != 320 || s.Theme != "system" {
		t.Fatalf("unexpected normalized settings: %+v", s)
	}
	if s.Suffix == "" || strings.ContainsAny(s.Suffix, `/:\`) {
		t.Fatalf("unsafe suffix: %q", s.Suffix)
	}
}

func TestOutputFilename(t *testing.T) {
	if got := outputFilename("holiday.JPG", "-small", "webp"); got != "holiday-small.webp" {
		t.Fatalf("got %q", got)
	}
	if got := outputFilename("noext", "-x", "jpg"); got != "noext-x.jpg" {
		t.Fatalf("got %q", got)
	}
}

func TestHasAlpha(t *testing.T) {
	if imageHasAlpha(gradientImage(10, 10, false), "PNG") {
		t.Fatal("opaque image reported alpha")
	}
	if !imageHasAlpha(gradientImage(10, 10, true), "PNG") {
		t.Fatal("transparent image reported opaque")
	}
	if imageHasAlpha(gradientImage(10, 10, true), "JPEG") {
		t.Fatal("JPEG is always treated as opaque")
	}
}

func TestWebPInputDecodes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "input.webp")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := webp.Encode(f, gradientImage(200, 150, false), webp.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 7, src, "WEBP", "input.webp", Settings{
		Format: "auto", Quality: 70, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.format != "WEBP" || result.width != 200 || result.height != 150 {
		t.Fatalf("got %s %dx%d", result.format, result.width, result.height)
	}
}

func newTestService(t *testing.T) *AnyPic {
	t.Helper()
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	return &AnyPic{
		byID:      map[int64]*Item{},
		settings:  defaultSettings(),
		statePath: filepath.Join(dir, "settings.json"),
		tempDir:   tmp,
		thumbSem:  make(chan struct{}, 3),
	}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestQueueCompressExportFlow(t *testing.T) {
	srcDir := t.TempDir()
	writeJPEG(t, filepath.Join(srcDir, "one.jpg"), gradientImage(800, 600, false), 92)
	writePNG(t, filepath.Join(srcDir, "two.png"), gradientImage(640, 480, true))

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

	exportDir := t.TempDir()
	if err := service.Compress(Settings{
		Format:       "webp",
		Quality:      70,
		MaxDimension: 400,
		OutputMode:   "folder",
		OutputFolder: exportDir,
		Suffix:       "-test",
		Theme:        "system",
	}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "compression", func() bool {
		state := service.State()
		if state.Batch.Running || state.Batch.Total != 2 {
			return false
		}
		for _, item := range state.Items {
			if item.Status == statusWorking || item.Status == statusReady {
				return false
			}
		}
		return true
	})

	state := service.State()
	for _, item := range state.Items {
		if item.Status != statusDone {
			t.Fatalf("%s: status %s (%s)", item.Name, item.Status, item.Error)
		}
		if item.OutputFormat != "WEBP" || item.CompressedSize <= 0 {
			t.Fatalf("%s: format %s size %d", item.Name, item.OutputFormat, item.CompressedSize)
		}
		if item.Width > 400 || item.Height > 400 {
			t.Fatalf("%s: not resized: %dx%d", item.Name, item.Width, item.Height)
		}
	}

	exported, err := service.ExportAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if exported.Count != 2 {
		t.Fatalf("exported %d, want 2", exported.Count)
	}
	for _, path := range exported.Paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() == 0 || filepath.Ext(path) != ".webp" {
			t.Fatalf("bad export %s (%d bytes)", path, info.Size())
		}
	}

	if _, err := service.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := service.State().Items; len(got) != 0 {
		t.Fatalf("queue not empty after clear: %d", len(got))
	}
}

func decodeDataURLConfig(t *testing.T, s string) image.Config {
	t.Helper()
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		t.Fatalf("malformed data URL: %.32q", s)
	}
	raw, err := base64.StdEncoding.DecodeString(s[comma+1:])
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// The comparison view is blown up far beyond a queue thumbnail, so its data
// URL must keep the full preview size instead of the thumbnail's 360 px cap.
func TestPreviewKeepsItsOwnResolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.png")
	writePNG(t, path, gradientImage(1200, 900, false))

	preview, w, h, err := previewImage(path)
	if err != nil {
		t.Fatal(err)
	}
	if w != 1200 || h != 900 {
		t.Fatalf("preview reports %dx%d, want 1200x900", w, h)
	}
	if !strings.HasPrefix(preview, "data:image/jpeg;base64,") {
		t.Fatalf("opaque preview should be a JPEG data URL")
	}
	if cfg := decodeDataURLConfig(t, preview); cfg.Width != 1200 || cfg.Height != 900 {
		t.Fatalf("preview image is %dx%d, want the full 1200x900", cfg.Width, cfg.Height)
	}
}

func TestThumbnailStaysThumbSized(t *testing.T) {
	s, err := dataURL(gradientImage(1200, 900, false), false, 80, thumbMaxSide)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeDataURLConfig(t, s); cfg.Width != thumbMaxSide {
		t.Fatalf("thumbnail is %d px wide, want %d", cfg.Width, thumbMaxSide)
	}
}

func TestPreviewWithAlphaStaysPNG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alpha.png")
	writePNG(t, path, gradientImage(800, 600, true))

	preview, _, _, err := previewImage(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(preview, "data:image/png;base64,") {
		t.Fatalf("transparent preview must keep the PNG branch, got %.24q", preview)
	}
	if cfg := decodeDataURLConfig(t, preview); cfg.Width != 800 || cfg.Height != 600 {
		t.Fatalf("alpha preview is %dx%d, want 800x600", cfg.Width, cfg.Height)
	}
}

func TestAnimatedGIFIsSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "loop.gif")
	first := image.NewPaletted(image.Rect(0, 0, 20, 20), color.Palette{color.Black, color.White})
	second := image.NewPaletted(image.Rect(0, 0, 20, 20), color.Palette{color.Black, color.White})
	for i := 0; i < 20; i++ {
		first.SetColorIndex(i, i, 0)
		second.SetColorIndex(i, 19-i, 1)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := gif.EncodeAll(f, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	service := newTestService(t)
	result, err := service.AddPaths([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 0 || len(result.Skipped) != 1 {
		t.Fatalf("added=%d skipped=%v", result.Added, result.Skipped)
	}
	if !strings.Contains(result.Skipped[0].Reason, "动态") {
		t.Fatalf("reason = %q", result.Skipped[0].Reason)
	}
}
