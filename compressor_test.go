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
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/gen2brain/webp"
	"golang.org/x/image/tiff"
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

func writeWebP(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := webp.Encode(f, img, webp.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeTransparentGIF writes a single-frame GIF whose background uses the
// transparent palette index, the sibling case for indexed transparency.
func writeTransparentGIF(t *testing.T, path string, img image.Image) {
	t.Helper()
	pal := color.Palette{
		color.NRGBA{A: 0},
		color.NRGBA{R: 220, G: 60, B: 90, A: 255},
	}
	b := img.Bounds()
	p := image.NewPaletted(b, pal)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a == 0xffff {
				p.SetColorIndex(x, y, 1)
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(f, p, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTIFF(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := tiff.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// logoImage synthesizes the issue-15 report scene in code: a static image
// with a fully transparent background, an opaque core and a semi-transparent
// anti-aliased edge. The reporter's original file was not attached; this keeps
// the features that matter here (static, alpha with both 0 and intermediate
// values) and the same scene is written as PNG, WebP, GIF and TIFF.
func logoImage(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	cx, cy := float64(width)/2, float64(height)/2
	radius := float64(min(width, height)) / 3
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			a := uint8(0)
			switch {
			case d <= radius-1:
				a = 255
			case d <= radius:
				a = 128
			}
			img.SetNRGBA(x, y, color.NRGBA{R: 220, G: 60, B: 90, A: a})
		}
	}
	return img
}

func decodePNGFile(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// alphaChannelNRGBA keeps the PNG encoder on the RGBA path even when every
// pixel is opaque, so the fixture really has an alpha channel. Go's encoder
// otherwise notices an opaque *image.NRGBA and writes RGB (color type 2),
// which would make the "alpha channel but fully opaque" case a duplicate of
// the opaque RGB one.
type alphaChannelNRGBA struct{ *image.NRGBA }

func (alphaChannelNRGBA) Opaque() bool { return false }

func TestCompressJPEGProducesSmallerFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	original := writeJPEG(t, src, gradientImage(1200, 800, false), 96)

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 1, src, "JPEG", "photo.jpg", false, Settings{
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
	result, err := service.compressOne(context.Background(), 2, src, "PNG", "wide.png", false, Settings{
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
	result, err := service.compressOne(context.Background(), 3, src, "PNG", "alpha.png", false, Settings{
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
	result, err := service.compressOne(context.Background(), 4, src, "JPEG", "tiny.jpg", false, Settings{
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
	result, err := service.compressOne(context.Background(), 5, src, "PNG", "photo.png", false, Settings{
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

func TestIssue15JPEGKeepsTransparency(t *testing.T) {
	// Synthesized report scenario (the reporter's original file was not
	// attached): a static PNG with a fully transparent background and a
	// semi-transparent anti-aliased edge, compressed with Format "jpeg",
	// Quality 82 and suffix "-anypic". The same scene covers the sibling
	// inputs: static WebP, a static GIF with a transparent palette index, an
	// alpha TIFF, and a transparent PNG that is resized before encoding.
	const w, h = 240, 180
	cases := []struct {
		name      string
		ext       string
		srcFormat string
		write     func(t *testing.T, path string, img image.Image)
	}{
		{"PNG", ".png", "PNG", func(t *testing.T, path string, img image.Image) { writePNG(t, path, img) }},
		{"WebP", ".webp", "WEBP", writeWebP},
		{"GIF", ".gif", "GIF", writeTransparentGIF},
		{"TIFF", ".tif", "TIFF", writeTIFF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "logo"+tc.ext)
			tc.write(t, src, logoImage(w, h))

			service := &AnyPic{tempDir: t.TempDir()}
			result, err := service.compressOne(context.Background(), 15, src, tc.srcFormat, "logo"+tc.ext, false, Settings{
				Format: "jpeg", Quality: 82, Suffix: "-anypic",
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.format != "PNG" {
				t.Fatalf("format = %s, want PNG (JPG must not flatten transparency)", result.format)
			}
			if !strings.HasSuffix(result.outputName, "-anypic.png") {
				t.Fatalf("output name = %q, want a .png name", result.outputName)
			}
			if !result.transparencyKept {
				t.Fatal("transparencyKept = false, want true")
			}
			if !result.hasAlpha {
				t.Fatal("result reports no alpha; PNG output must keep it")
			}

			decoded := decodePNGFile(t, result.path)
			if _, _, _, a := decoded.At(0, 0).RGBA(); a != 0 {
				t.Fatalf("background alpha = %#x, want fully transparent", a)
			}
			if !imageHasAlpha(decoded, "PNG") {
				t.Fatal("decoded PNG has no transparency")
			}
			if tc.name == "PNG" {
				// PNG output is a lossless re-encode, so every alpha value
				// must survive exactly, including the half-transparent edge.
				orig := logoImage(w, h)
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						_, _, _, want := orig.At(x, y).RGBA()
						_, _, _, got := decoded.At(x, y).RGBA()
						if got != want {
							t.Fatalf("alpha at (%d,%d) = %#x, want %#x", x, y, got, want)
						}
					}
				}
			}
		})
	}

	t.Run("PNGResized", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "logo.png")
		writePNG(t, src, logoImage(600, 400))

		service := &AnyPic{tempDir: t.TempDir()}
		result, err := service.compressOne(context.Background(), 16, src, "PNG", "logo.png", false, Settings{
			Format: "jpeg", Quality: 82, MaxDimension: 100, Suffix: "-anypic",
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.format != "PNG" {
			t.Fatalf("format = %s, want PNG", result.format)
		}
		if max(result.width, result.height) != 100 {
			t.Fatalf("resized to %dx%d, want long edge 100", result.width, result.height)
		}
		if !result.transparencyKept || !result.hasAlpha {
			t.Fatalf("transparencyKept=%v hasAlpha=%v, want both true", result.transparencyKept, result.hasAlpha)
		}
		decoded := decodePNGFile(t, result.path)
		if _, _, _, a := decoded.At(0, 0).RGBA(); a != 0 {
			t.Fatalf("background alpha after resize = %#x, want fully transparent", a)
		}
	})
}

func TestIssue15JPEGWithoutTransparencyStaysJPEG(t *testing.T) {
	// The JPG choice is only overridden when the post-resize pixels actually
	// carry transparency. Opaque images keep the requested JPG, and the other
	// format choices are untouched.
	gray := image.NewGray(image.Rect(0, 0, 240, 180))
	for i := range gray.Pix {
		gray.Pix[i] = uint8(i)
	}
	transparent := logoImage(240, 180)

	cases := []struct {
		name      string
		img       image.Image
		srcFormat string
		format    string
		want      string
		wantExt   string
		colorType byte // nonzero: assert the PNG IHDR color type of the source
	}{
		{"opaque PNG keeps JPG", gray, "PNG", "jpeg", "JPEG", "-anypic.jpg", 0},
		{"alpha channel but fully opaque keeps JPG", alphaChannelNRGBA{gradientImage(240, 180, false)}, "PNG", "jpeg", "JPEG", "-anypic.jpg", 6},
		{"transparent PNG with auto stays PNG", transparent, "PNG", "auto", "PNG", "-anypic.png", 0},
		{"transparent PNG with WebP stays WEBP", transparent, "PNG", "webp", "WEBP", "-anypic.webp", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "input.png")
			writePNG(t, src, tc.img)
			if tc.colorType != 0 {
				data, err := os.ReadFile(src)
				if err != nil {
					t.Fatal(err)
				}
				if len(data) < 26 {
					t.Fatalf("PNG is %d bytes, too short to carry an IHDR", len(data))
				}
				// IHDR color type: signature(8) + length(4) + "IHDR"(4) +
				// width(4) + height(4) + bit depth(1) = offset 25.
				if data[25] != tc.colorType {
					t.Fatalf("PNG color type = %d, want %d (fixture must carry an alpha channel)", data[25], tc.colorType)
				}
			}

			service := &AnyPic{tempDir: t.TempDir()}
			result, err := service.compressOne(context.Background(), 17, src, tc.srcFormat, "input.png", false, Settings{
				Format: tc.format, Quality: 82, Suffix: "-anypic",
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.format != tc.want {
				t.Fatalf("format = %s, want %s", result.format, tc.want)
			}
			if result.transparencyKept {
				t.Fatal("transparencyKept = true, want false")
			}
			if !strings.HasSuffix(result.outputName, tc.wantExt) {
				t.Fatalf("output name = %q, want a %s name", result.outputName, tc.wantExt)
			}
		})
	}
}

func TestIssue15PassthroughKeepsTransparency(t *testing.T) {
	// A transparent PNG already encoded as well as the pipeline can do: the
	// re-encode cannot beat it, so the original is kept. The output format is
	// still PNG (the JPG request was overridden) and the flag stays true.
	dir := t.TempDir()
	src := filepath.Join(dir, "logo.png")
	img := logoImage(400, 300)
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	enc := png.Encoder{CompressionLevel: pngCompressionLevel(img.Bounds().Dx() * img.Bounds().Dy())}
	if err := enc.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 19, src, "PNG", "logo.png", false, Settings{
		Format: "jpeg", Quality: 82, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.passedThrough {
		t.Fatalf("expected passthrough, got %d bytes", result.size)
	}
	if result.path != src {
		t.Fatalf("passthrough path = %q, want %q", result.path, src)
	}
	if result.format != "PNG" {
		t.Fatalf("format = %s, want PNG", result.format)
	}
	if !result.transparencyKept {
		t.Fatal("passthrough must still report transparencyKept")
	}
}

func TestIssue15TransparencyFlagReachesItemAndResets(t *testing.T) {
	srcDir := t.TempDir()
	writePNG(t, filepath.Join(srcDir, "logo.png"), logoImage(200, 150))
	service := newTestService(t)
	if _, err := service.AddPaths([]string{srcDir}); err != nil {
		t.Fatal(err)
	}

	jpegSettings := Settings{Format: "jpeg", Quality: 82, Suffix: "-anypic", Theme: "system"}
	if err := service.Compress(jpegSettings); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "jpeg pass", func() bool {
		state := service.State()
		return !state.Batch.Running && state.Batch.Total == 1 && allStatus(state, statusDone, statusError)
	})
	item := service.State().Items[0]
	if item.Status != statusDone {
		t.Fatalf("status = %s (%s)", item.Status, item.Error)
	}
	if item.OutputFormat != "PNG" || !item.TransparencyKept {
		t.Fatalf("after JPG pass: format=%s transparencyKept=%v", item.OutputFormat, item.TransparencyKept)
	}
	if !strings.HasSuffix(item.OutputName, "-anypic.png") {
		t.Fatalf("output name = %q", item.OutputName)
	}

	// A new pass recomputes the flags from its own result; the WebP pass must
	// clear the JPG-specific flag.
	webpSettings := Settings{Format: "webp", Quality: 82, Suffix: "-anypic", Theme: "system"}
	if err := service.Compress(webpSettings); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "webp pass", func() bool {
		state := service.State()
		return !state.Batch.Running && state.Batch.Total == 1 && allStatus(state, statusDone, statusError)
	})
	item = service.State().Items[0]
	if item.OutputFormat != "WEBP" || item.TransparencyKept {
		t.Fatalf("after WebP pass: format=%s transparencyKept=%v", item.OutputFormat, item.TransparencyKept)
	}
}

func TestIssue15AnimatedKeepsItsOwnPath(t *testing.T) {
	// Animated files ignore the format setting (#8), so the JPG request must
	// not flip them to PNG and the transparency flag stays false.
	dir := t.TempDir()
	src := filepath.Join(dir, "loop.gif")
	threeFrameGIFFixture(t, src, 48, 32)

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 20, src, "GIF", "loop.gif", true, Settings{
		Format: "jpeg", Quality: 82, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.format == "PNG" {
		t.Fatal("animated path must not switch to PNG")
	}
	if result.transparencyKept {
		t.Fatal("animated path must not report transparencyKept")
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
	result, err := service.compressOne(context.Background(), 7, src, "WEBP", "input.webp", false, Settings{
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

func TestAutoProcessDefaultsOn(t *testing.T) {
	if got := normalizeSettings(Settings{}).AutoProcess; got != "on" {
		t.Fatalf("missing preference normalized to %q, want on", got)
	}
	if got := normalizeSettings(Settings{AutoProcess: "off"}).AutoProcess; got != "off" {
		t.Fatalf("explicit off normalized to %q", got)
	}
	if got := normalizeSettings(Settings{AutoProcess: "yes"}).AutoProcess; got != "on" {
		t.Fatalf("unknown value normalized to %q, want on", got)
	}
}

func TestCompressReadySkipsFinishedImages(t *testing.T) {
	srcDir := t.TempDir()
	writeJPEG(t, filepath.Join(srcDir, "one.jpg"), gradientImage(400, 300, false), 92)
	writeJPEG(t, filepath.Join(srcDir, "two.jpg"), gradientImage(400, 300, false), 92)

	service := newTestService(t)
	if _, err := service.AddPaths([]string{srcDir}); err != nil {
		t.Fatal(err)
	}
	settings := Settings{Format: "jpeg", Quality: 70, Suffix: "-anypic"}
	if err := service.Compress(settings); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first pass", func() bool {
		state := service.State()
		return !state.Batch.Running && state.Batch.Total == 2 && allStatus(state, statusDone, statusError)
	})

	// A third image arrives while the user keeps working; the automatic
	// flow compresses only this one.
	writeJPEG(t, filepath.Join(srcDir, "three.jpg"), gradientImage(400, 300, false), 92)
	if _, err := service.AddPaths([]string{filepath.Join(srcDir, "three.jpg")}); err != nil {
		t.Fatal(err)
	}
	if err := service.CompressReady(settings); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ready pass", func() bool {
		state := service.State()
		return !state.Batch.Running && state.Batch.Total == 1 && state.Items[2].Status == statusDone
	})
	if state := service.State(); state.Batch.Total != 1 {
		t.Fatalf("second pass covered %d images, want only the new one", state.Batch.Total)
	}
}

func TestExportProcessedWritesBesideOriginals(t *testing.T) {
	srcDir := t.TempDir()
	one := filepath.Join(srcDir, "one.jpg")
	two := filepath.Join(srcDir, "two.png")
	writeJPEG(t, one, gradientImage(400, 300, false), 92)
	writePNG(t, two, gradientImage(400, 300, true))

	service := newTestService(t)
	if _, err := service.AddPaths([]string{srcDir}); err != nil {
		t.Fatal(err)
	}
	if err := service.Compress(Settings{Format: "jpeg", Quality: 70, Suffix: "-small"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "compression", func() bool {
		state := service.State()
		return !state.Batch.Running && state.Batch.Total == 2 && allStatus(state, statusDone, statusError)
	})

	state := service.State()
	exported, err := service.ExportProcessed([]int64{state.Items[0].ID, state.Items[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if exported.Count != 2 || len(exported.Paths) != 2 {
		t.Fatalf("exported %d paths %v", exported.Count, exported.Paths)
	}
	for _, path := range exported.Paths {
		if filepath.Dir(path) != srcDir {
			t.Fatalf("exported to %s, want it beside the original in %s", path, srcDir)
		}
		if !strings.Contains(filepath.Base(path), "-small") {
			t.Fatalf("exported name %q ignores the suffix", filepath.Base(path))
		}
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("missing or empty export %s: %v", path, err)
		}
	}
}

func allStatus(state Snapshot, statuses ...string) bool {
	for _, item := range state.Items {
		ok := false
		for _, status := range statuses {
			if item.Status == status {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
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

func TestIssue2InfoVersionFollowsTheApp(t *testing.T) {
	// The About dialog must show the packaged app's version, not a constant
	// that every release has to remember to bump.
	const test = "9.9.9-test"
	mygo.App.SetVersion(test)
	t.Cleanup(func() { mygo.App.SetVersion("") })
	a := &AnyPic{settings: defaultSettings(), thumbSem: make(chan struct{}, 1)}
	if got := a.Info().Version; got != test {
		t.Errorf("Info().Version = %q, want the runtime's app version %q", got, test)
	}
}

func TestIssue4PasteImageStoredOutsideUserFiles(t *testing.T) {
	// A pasted image has no source file, so it is stored in Pictures/AnyPic
	// next to screenshots; saving twice must never overwrite the first copy.
	base := t.TempDir()
	var buf bytes.Buffer
	if err := png.Encode(&buf, gradientImage(320, 200, true)); err != nil {
		t.Fatal(err)
	}
	first, err := savePastedImage(buf.Bytes(), base, "2026-11-08 15.30.00")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) != filepath.Join(base, "AnyPic") {
		t.Fatalf("saved to %q, want Pictures/AnyPic", first)
	}
	if !strings.HasPrefix(filepath.Base(first), "粘贴 2026-11-08 15.30.00") || filepath.Ext(first) != ".png" {
		t.Fatalf("name = %q", filepath.Base(first))
	}
	saved, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, buf.Bytes()) {
		t.Fatal("saved bytes differ from the clipboard image")
	}

	second, err := savePastedImage(buf.Bytes(), base, "2026-11-08 15.30.00")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("second paste overwrote the first: %q", first)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("first paste disappeared: %v", err)
	}

	if _, err := savePastedImage(nil, base, "2026-11-08 15.30.00"); err == nil {
		t.Fatal("expected an error for empty clipboard data")
	}
}

func TestIssue10PNGCompressionLevelByPixels(t *testing.T) {
	// BestCompression buys a few percent on small images but costs seconds on
	// multi-megapixel ones; the threshold picks speed without giving up the
	// lossless guarantee.
	if got := pngCompressionLevel(16 << 20); got != png.BestCompression {
		t.Fatalf("level at 16M pixels = %v", got)
	}
	if got := pngCompressionLevel(16<<20 + 1); got != png.DefaultCompression {
		t.Fatalf("level above 16M pixels = %v", got)
	}
	if got := pngCompressionLevel(1); got != png.BestCompression {
		t.Fatalf("level at 1 pixel = %v", got)
	}
}

func TestIssue11CompressFailedRetriesOnlyFailures(t *testing.T) {
	srcDir := t.TempDir()
	writeJPEG(t, filepath.Join(srcDir, "good.jpg"), gradientImage(800, 600, false), 92)
	// A PNG whose header parses (so DecodeConfig accepts it at add time) but
	// whose data is missing, so the full decode fails mid-pass.
	corrupt := filepath.Join(srcDir, "broken.png")
	full := filepath.Join(srcDir, "seed.png")
	writePNG(t, full, gradientImage(64, 48, false))
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corrupt, data[:33], 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(full)

	service := newTestService(t)
	if _, err := service.AddPaths([]string{srcDir}); err != nil {
		t.Fatal(err)
	}
	settings := Settings{Format: "auto", Quality: 82, Suffix: "-anypic", Theme: "system"}
	if err := service.Compress(settings); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first pass", func() bool {
		state := service.State()
		return !state.Batch.Running && state.Batch.Done == 2
	})
	state := service.State()
	for _, item := range state.Items {
		if item.Name == "good.jpg" && item.Status != statusDone {
			t.Fatalf("good.jpg: %s (%s)", item.Status, item.Error)
		}
		if item.Name == "broken.png" && item.Status != statusError {
			t.Fatalf("broken.png: %s", item.Status)
		}
	}
	var goodBefore Item
	for _, item := range state.Items {
		if item.Name == "good.jpg" {
			goodBefore = item
		}
	}

	if err := service.CompressFailed(settings); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "retry pass", func() bool {
		state := service.State()
		return !state.Batch.Running && state.Batch.Total == 1
	})
	for _, item := range service.State().Items {
		if item.Name == "broken.png" && item.Status != statusError {
			t.Fatalf("broken.png after retry: %s (%s)", item.Status, item.Error)
		}
		if item.Name == "good.jpg" {
			if item.Status != statusDone || item.CompressedSize != goodBefore.CompressedSize {
				t.Fatalf("good.jpg was re-encoded: size %d → %d", goodBefore.CompressedSize, item.CompressedSize)
			}
		}
	}

	if err := service.CompressFailed(settings); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second retry", func() bool {
		state := service.State()
		return !state.Batch.Running && state.Batch.Total == 1
	})
}

func TestIssue11CompressFailedWithoutFailuresIsAnError(t *testing.T) {
	srcDir := t.TempDir()
	writeJPEG(t, filepath.Join(srcDir, "good.jpg"), gradientImage(800, 600, false), 92)
	service := newTestService(t)
	if _, err := service.AddPaths([]string{srcDir}); err != nil {
		t.Fatal(err)
	}
	settings := Settings{Format: "auto", Quality: 82, Suffix: "-anypic", Theme: "system"}
	if err := service.Compress(settings); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "pass", func() bool {
		state := service.State()
		return !state.Batch.Running && state.Batch.Done == 1
	})
	if err := service.CompressFailed(settings); err == nil {
		t.Fatal("expected an error when nothing failed")
	}
}

func TestIssue5RemoveAcceptsMultipleIDs(t *testing.T) {
	// The batch-selection UI removes many rows in one call; the backend
	// contract it leans on is that Remove accepts an arbitrary id list.
	dir := t.TempDir()
	writeJPEG(t, filepath.Join(dir, "one.jpg"), gradientImage(64, 48, false), 90)
	writeJPEG(t, filepath.Join(dir, "two.jpg"), gradientImage(64, 48, false), 90)
	writeJPEG(t, filepath.Join(dir, "three.jpg"), gradientImage(64, 48, false), 90)
	service := newTestService(t)
	if _, err := service.AddPaths([]string{dir}); err != nil {
		t.Fatal(err)
	}
	state := service.State()
	if len(state.Items) != 3 {
		t.Fatalf("items = %d", len(state.Items))
	}
	snap, err := service.Remove([]int64{state.Items[0].ID, state.Items[2].ID})
	if err != nil {
		t.Fatal(err)
	}
	// WalkDir is lexical: one.jpg, three.jpg, two.jpg — so removing the
	// first and third entries leaves three.jpg.
	if len(snap.Items) != 1 || snap.Items[0].Name != "three.jpg" {
		t.Fatalf("left = %+v, want only three.jpg", snap.Items)
	}
}

func TestIssue6OpenFileBuffersUntilPageAttaches(t *testing.T) {
	// Files opened from the file manager can arrive before the page has
	// loaded its event listeners (cold start, or the app waking in the tray).
	// Until the page drains them, paths wait in the service; afterwards they
	// go out as events and are not buffered again.
	service := newTestService(t)
	service.OpenFile("/tmp/one.jpg")
	service.OpenFile("/tmp/two.png")
	paths := service.TakePendingOpen()
	if len(paths) != 2 || paths[0] != "/tmp/one.jpg" || paths[1] != "/tmp/two.png" {
		t.Fatalf("pending = %v", paths)
	}
	if again := service.TakePendingOpen(); len(again) != 0 {
		t.Fatalf("draining twice returned %v", again)
	}
	service.OpenFile("/tmp/three.webp")
	if buffered := service.TakePendingOpen(); len(buffered) != 0 {
		t.Fatalf("open after the page attached was buffered: %v", buffered)
	}
}
