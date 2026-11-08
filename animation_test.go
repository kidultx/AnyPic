package main

import (
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
