package main

import (
	"context"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"

	"github.com/gen2brain/webp"
)

// countFramesByDecoding is the old detection path: decode every frame and
// count. The structural scanners must agree with it on every fixture.
func countFramesByDecoding(t *testing.T, path string) int {
	t.Helper()
	frames, err := animationFrameCount(path)
	if err != nil {
		t.Fatalf("animationFrameCount(%s): %v", filepath.Base(path), err)
	}
	return frames
}

func writeAnimatedGIF(t *testing.T, path string, frames int, width, height int) {
	t.Helper()
	anim := &gif.GIF{}
	for i := range frames {
		p := image.NewPaletted(image.Rect(0, 0, width, height), color.Palette{
			color.RGBA{R: uint8(40 * i), G: 120, B: 200, A: 255},
			color.RGBA{R: 250, G: uint8(30 * i), B: 90, A: 255},
		})
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				p.Set(x, y, p.Palette[(x+y+i)%(len(p.Palette))])
			}
		}
		anim.Image = append(anim.Image, p)
		anim.Delay = append(anim.Delay, 10)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := gif.EncodeAll(f, anim); err != nil {
		t.Fatal(err)
	}
}

func TestIssue7GIFScanAgreesWithDecoding(t *testing.T) {
	dir := t.TempDir()

	single := filepath.Join(dir, "single.gif")
	writeAnimatedGIF(t, single, 1, 64, 48)
	if frames := countFramesByDecoding(t, single); frames != 1 {
		t.Fatalf("static GIF frame count = %d", frames)
	}

	many := filepath.Join(dir, "many.gif")
	writeAnimatedGIF(t, many, 3, 64, 48)
	if frames := countFramesByDecoding(t, many); frames != 3 {
		t.Fatalf("animated GIF frame count = %d, want 3", frames)
	}

	animation, err := isAnimatedFile(many)
	if err != nil {
		t.Fatal(err)
	}
	static, err := isAnimatedFile(single)
	if err != nil {
		t.Fatal(err)
	}
	if !animation || static {
		t.Fatalf("isAnimatedFile many=%v single=%v", animation, static)
	}
}

func TestIssue7GIFScanSurvivesTruncation(t *testing.T) {
	dir := t.TempDir()
	many := filepath.Join(dir, "truncated.gif")
	writeAnimatedGIF(t, many, 3, 64, 48)
	data, err := os.ReadFile(many)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cut.gif")
	if err := os.WriteFile(path, data[:len(data)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	// The scanner must never disagree with the decoder it falls back to: on a
	// malformed file both paths are exercised, and the outcome is whatever the
	// decoder reports (an error, or a lenient partial count).
	got, gotErr := animationFrameCount(path)
	want, wantErr := countGIFFramesByDecoding(path)
	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("scanner err = %v, decoder err = %v", gotErr, wantErr)
	}
	if gotErr == nil && got != want {
		t.Fatalf("scanner frames = %d, decoder frames = %d", got, want)
	}
}

func movingFrame(i int) *image.NRGBA {
	// A block that shifts with i, so no two frames are identical — libwebp
	// coalesces identical consecutive frames, which would make the fixture
	// hold fewer frames than the test asks for.
	img := gradientImage(48, 32, false)
	for y := 8; y < 24; y++ {
		for x := 4 + i*12; x < 16+i*12; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	return img
}

func TestIssue7WebPScanAgreesWithDecoding(t *testing.T) {
	dir := t.TempDir()

	single := filepath.Join(dir, "single.webp")
	buf, err := os.Create(single)
	if err != nil {
		t.Fatal(err)
	}
	if err := webp.Encode(buf, gradientImage(48, 32, false), webp.Options{Quality: 80, Method: 4}); err != nil {
		t.Fatal(err)
	}
	buf.Close()
	if frames := countFramesByDecoding(t, single); frames != 1 {
		t.Fatalf("static WebP frame count = %d", frames)
	}

	many := filepath.Join(dir, "many.webp")
	anim := &webp.WEBP{LoopCount: 0}
	for i := range 3 {
		anim.Image = append(anim.Image, movingFrame(i))
		anim.Delay = append(anim.Delay, 100)
	}
	buf, err = os.Create(many)
	if err != nil {
		t.Fatal(err)
	}
	if err := webp.EncodeAll(buf, anim); err != nil {
		t.Fatal(err)
	}
	buf.Close()
	if frames := countFramesByDecoding(t, many); frames != 3 {
		t.Fatalf("animated WebP frame count = %d, want 3", frames)
	}

	animation, err := isAnimatedFile(many)
	if err != nil {
		t.Fatal(err)
	}
	static, err := isAnimatedFile(single)
	if err != nil {
		t.Fatal(err)
	}
	if !animation || static {
		t.Fatalf("isAnimatedFile many=%v single=%v", animation, static)
	}
}

func TestIssue7WebPScanSurvivesTruncation(t *testing.T) {
	dir := t.TempDir()
	many := filepath.Join(dir, "many.webp")
	anim := &webp.WEBP{LoopCount: 0}
	for i := range 3 {
		anim.Image = append(anim.Image, movingFrame(i))
		anim.Delay = append(anim.Delay, 100)
	}
	f, err := os.Create(many)
	if err != nil {
		t.Fatal(err)
	}
	if err := webp.EncodeAll(f, anim); err != nil {
		t.Fatal(err)
	}
	f.Close()
	data, err := os.ReadFile(many)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cut.webp")
	if err := os.WriteFile(path, data[:len(data)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	// Same contract as the GIF case: the scanner may reject what a lenient
	// decoder accepts, but the supported outcome is the decoder's.
	got, gotErr := animationFrameCount(path)
	want, wantErr := countWebPFramesByDecoding(path)
	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("scanner err = %v, decoder err = %v", gotErr, wantErr)
	}
	if gotErr == nil && got != want {
		t.Fatalf("scanner frames = %d, decoder frames = %d", got, want)
	}
}

func TestIssue7OtherFormatsReportSingleFrame(t *testing.T) {
	dir := t.TempDir()
	jpg := filepath.Join(dir, "photo.jpg")
	writeJPEG(t, jpg, gradientImage(64, 48, false), 90)
	frames, err := animationFrameCount(jpg)
	if err != nil {
		t.Fatal(err)
	}
	if frames != 1 {
		t.Fatalf("JPEG frame count = %d, want 1", frames)
	}
	missing := filepath.Join(dir, "gone.png")
	if _, err := animationFrameCount(missing); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

/* ------------------------------------------------------------------ */
/* issue #8: animated GIF/WebP become animated WebP                    */
/* ------------------------------------------------------------------ */

// threeFrameGIFFixture builds a GIF whose middle frame is fully transparent
// after a background disposal, so transparency survival is observable.
func threeFrameGIFFixture(t *testing.T, path string, width, height int) int64 {
	t.Helper()
	pal := color.Palette{
		color.RGBA{A: 0},
		color.RGBA{R: 220, G: 60, B: 40, A: 255},
		color.RGBA{R: 40, G: 90, B: 220, A: 255},
	}
	full := func(index uint8) *image.Paletted {
		f := image.NewPaletted(image.Rect(0, 0, width, height), pal)
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				f.SetColorIndex(x, y, index)
			}
		}
		return f
	}
	anim := &gif.GIF{
		Image:    []*image.Paletted{full(1), full(0), full(2)},
		Delay:    []int{10, 10, 10},
		Disposal: []byte{gif.DisposalBackground, gif.DisposalNone, gif.DisposalNone},
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := gif.EncodeAll(f, anim); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestIssue8AnimatedGIFIsAddedNotSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "loop.gif")
	threeFrameGIFFixture(t, path, 32, 24)

	service := newTestService(t)
	result, err := service.AddPaths([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 || len(result.Skipped) != 0 {
		t.Fatalf("added=%d skipped=%v", result.Added, result.Skipped)
	}
	if !result.Snapshot.Items[0].Animated {
		t.Fatal("animated GIF lost its animated flag")
	}
}

func TestIssue8AnimatedGIFBecomesAnimatedWebP(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "loop.gif")
	original := threeFrameGIFFixture(t, src, 64, 48)

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 1, src, "GIF", "loop.gif", true, Settings{
		Format: "auto", Quality: 82, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.passedThrough {
		t.Fatal("expected a converted animation, got passthrough")
	}
	if result.format != "WEBP" || result.outputName != "loop-anypic.webp" {
		t.Fatalf("format %q name %q", result.format, result.outputName)
	}
	if result.width != 64 || result.height != 48 {
		t.Fatalf("canvas %dx%d", result.width, result.height)
	}

	f, err := os.Open(result.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	anim, err := webp.DecodeAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(anim.Image) != 3 {
		t.Fatalf("frames = %d, want 3", len(anim.Image))
	}
	if len(anim.Delay) != 3 || anim.Delay[0] != 100 || anim.Delay[2] != 100 {
		t.Fatalf("delays = %v, want 100ms each", anim.Delay)
	}
	if anim.LoopCount != 0 {
		t.Fatalf("loop count = %d, want infinite", anim.LoopCount)
	}
	// The middle frame must still be transparent after conversion, and the
	// last one fully opaque: disposal and transparency both survived.
	_, _, _, a1 := anim.Image[1].At(0, 0).RGBA()
	_, _, _, a2 := anim.Image[2].At(0, 0).RGBA()
	if a1 != 0 {
		t.Fatalf("frame 2 alpha = %d, want fully transparent", a1)
	}
	if a2>>8 != 255 {
		t.Fatalf("frame 3 alpha = %d, want opaque", a2>>8)
	}
	if result.size >= original {
		t.Fatalf("converted %d bytes is not smaller than the GIF's %d", result.size, original)
	}
}

func writeAnimatedWebP(t *testing.T, path string, quality int, width, height int) int64 {
	t.Helper()
	anim := &webp.WEBP{LoopCount: 0}
	for i := range 2 {
		anim.Image = append(anim.Image, movingFrameAt(width, height, i))
		anim.Delay = append(anim.Delay, 120)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := webp.EncodeAll(f, anim, webp.Options{Quality: quality, Method: 4}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

// movingFrameAt is movingFrame with a configurable canvas, so WebP fixtures
// can pick their own dimensions.
func movingFrameAt(width, height, i int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(30 + 180*x/max(1, width-1)),
				G: uint8(20 + 150*y/max(1, height-1)),
				B: 120, A: 255,
			})
		}
	}
	for y := height / 4; y < height*3/4; y++ {
		for x := width/8 + i*width/4; x < width/8+width/8+i*width/4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	return img
}

func TestIssue8AnimatedWebPReencodesWithQuality(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "loop.webp")
	original := writeAnimatedWebP(t, src, 95, 320, 240)

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 2, src, "WEBP", "loop.webp", true, Settings{
		Format: "auto", Quality: 40, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.passedThrough {
		t.Fatal("expected a re-encoded animation, got passthrough")
	}
	if result.format != "WEBP" || result.outputName != "loop-anypic.webp" {
		t.Fatalf("format %q name %q", result.format, result.outputName)
	}
	if result.size >= original {
		t.Fatalf("re-encoded %d bytes is not smaller than the quality-95 source's %d", result.size, original)
	}
	f, err := os.Open(result.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	anim, err := webp.DecodeAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(anim.Image) != 2 || len(anim.Delay) != 2 || anim.Delay[0] != 120 {
		t.Fatalf("frames=%d delays=%v", len(anim.Image), anim.Delay)
	}
}

func TestIssue8AnimatedPassthroughWhenLarger(t *testing.T) {
	dir := t.TempDir()
	// A quality-30 source re-encoded at 95 would come out larger: the honest
	// answer is to keep the original and mark it as already optimal.
	src := filepath.Join(dir, "loop.webp")
	writeAnimatedWebP(t, src, 30, 320, 240)

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 3, src, "WEBP", "loop.webp", true, Settings{
		Format: "auto", Quality: 95, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.passedThrough {
		t.Fatalf("expected passthrough, got a %d-byte re-encode", result.size)
	}
	if result.path != src || result.outputName != "loop-anypic.webp" {
		t.Fatalf("path %q name %q", result.path, result.outputName)
	}
}

func TestIssue8AnimatedResizeAppliesToEveryFrame(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.gif")
	threeFrameGIFFixture(t, src, 800, 600)

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 4, src, "GIF", "big.gif", true, Settings{
		Format: "auto", Quality: 82, MaxDimension: 400, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.width != 400 || result.height != 300 {
		t.Fatalf("canvas %dx%d, want 400x300", result.width, result.height)
	}
	f, err := os.Open(result.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	anim, err := webp.DecodeAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(anim.Image) != 3 {
		t.Fatalf("frames = %d, want 3", len(anim.Image))
	}
	for i, frame := range anim.Image {
		if frame.Bounds().Dx() != 400 || frame.Bounds().Dy() != 300 {
			t.Fatalf("frame %d is %dx%d", i, frame.Bounds().Dx(), frame.Bounds().Dy())
		}
	}
}

func TestIssue8AnimatedCannotBeDecorated(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "loop.gif")
	threeFrameGIFFixture(t, src, 32, 24)
	service := newTestService(t)
	result, err := service.AddPaths([]string{src})
	if err != nil {
		t.Fatal(err)
	}
	id := result.Snapshot.Items[0].ID
	if _, err := service.DecorateItem(id, FrameStyle{}); err == nil {
		t.Fatal("expected an error beautifying an animation")
	}
}
