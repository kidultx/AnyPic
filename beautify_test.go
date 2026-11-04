package main

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestDecorateScreenshotAddsPadding(t *testing.T) {
	src := gradientImage(400, 300, false)
	out := decorateScreenshot(src, ScreenshotStyle{Mode: "on", Background: "sunset", Padding: 10})
	b := out.Bounds()
	// 10% of the longer edge surrounds all four sides.
	// 10% of the longer edge (40 px) surrounds all four sides.
	if b.Dx() != 480 || b.Dy() != 380 {
		t.Fatalf("canvas %dx%d, want 480x380", b.Dx(), b.Dy())
	}
	// The corner pixel belongs to the background, the center to the shot.
	if _, _, _, a := out.At(0, 0).RGBA(); a != 0xffff {
		t.Fatal("background must be opaque")
	}
	if got := out.At(240, 180); !sameColor(got, src.At(200, 140)) {
		t.Fatalf("center pixel %v drifted from the source %v", got, src.At(200, 140))
	}
}

func TestDecorateScreenshotZeroPadding(t *testing.T) {
	src := gradientImage(200, 200, false)
	out := decorateScreenshot(src, ScreenshotStyle{Mode: "on", Background: "paper", Padding: 0})
	b := out.Bounds()
	if b.Dx() != 200 || b.Dy() != 200 {
		t.Fatalf("canvas %dx%d, want 200x200", b.Dx(), b.Dy())
	}
}

func TestDecorateScreenshotRoundsCorners(t *testing.T) {
	src := gradientImage(300, 200, false)
	out := decorateScreenshot(src, ScreenshotStyle{Mode: "on", Background: "midnight", Padding: 10})
	// pad is 40 and the radius is 12: two pixels into the shot's corner the
	// rounding has cut the shot away, so the pixel must be the background
	// (a deep teal-navy), not the source's dark corner.
	cut := out.At(42, 42)
	if _, _, _, a := cut.RGBA(); a != 0xffff {
		t.Fatalf("expected opaque background at the rounded corner, got %v", cut)
	}
	r, g, _, _ := cut.RGBA()
	if r < 40<<8 && g < 40<<8 {
		t.Fatalf("corner pixel %v looks like the undecorated shot, not the background", cut)
	}
}

func TestAutoBackgroundFollowsImageColor(t *testing.T) {
	// A screenshot whose edges are all dark blue should produce a dark
	// blue-ish gradient, not a random preset.
	src := image.NewNRGBA(image.Rect(0, 0, 80, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 80; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 10, G: 20, B: 90, A: 255})
		}
	}
	from, to := autoBackgroundColors(src)
	if from.B < 100 || from.B < from.R {
		t.Fatalf("from color %v is not a lightened navy", from)
	}
	if to.R >= from.R || to.B >= from.B {
		t.Fatalf("gradient should run light to dark, got %v -> %v", from, to)
	}
}

func TestAutoBackgroundClampsLightness(t *testing.T) {
	// A near-white screenshot would otherwise melt into a near-white
	// background: the top stop must stay clearly lighter, the bottom stop
	// clearly darker, than the shot's own edge tone.
	white := image.NewNRGBA(image.Rect(0, 0, 60, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 60; x++ {
			white.SetNRGBA(x, y, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
		}
	}
	from, to := autoBackgroundColors(white)
	// The top stop must be at most ~0.86 lightness so a 245-gray shot still
	// separates from it, and the bottom stop must sit well below the shot.
	if from.R < 200 || from.R > 225 {
		t.Fatalf("light screenshot gave a top stop %v with no headroom", from)
	}
	if to.R > 200 {
		t.Fatalf("light screenshot gave a bottom stop %v that matches the shot", to)
	}
	if !(to.R < from.R && to.G < from.G && to.B < from.B) {
		t.Fatalf("gradient should run light to dark, got %v -> %v", from, to)
	}

	// A near-black shot gets lifted off pure black for the same reason.
	black := image.NewNRGBA(image.Rect(0, 0, 60, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 60; x++ {
			black.SetNRGBA(x, y, color.NRGBA{A: 255})
		}
	}
	darkFrom, darkTo := autoBackgroundColors(black)
	if darkFrom.R < 60 || darkFrom.G < 60 || darkFrom.B < 60 {
		t.Fatalf("dark screenshot gave a top stop %v that matches the shot", darkFrom)
	}
	if !(darkTo.R < darkFrom.R && darkTo.G < darkFrom.G && darkTo.B < darkFrom.B) {
		t.Fatalf("gradient should run light to dark, got %v -> %v", darkFrom, darkTo)
	}
}

func TestCornerRadiusScalesWithResolution(t *testing.T) {
	if got := cornerRadius(400); got != 16 {
		t.Fatalf("small shot radius %d, want 16", got)
	}
	if got := cornerRadius(2160); got != 86 {
		t.Fatalf("4K short-edge radius %d, want 86", got)
	}
}

func TestAutoBackgroundFallsBackOnTransparentImage(t *testing.T) {
	blank := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	from, to := autoBackgroundColors(blank)
	pair := backgroundPresets["breeze"]
	wantFrom := mustHex(pair[0])
	if from != wantFrom || to != mustHex(pair[1]) {
		t.Fatalf("transparent image gave %v -> %v, want the breeze preset", from, to)
	}
}

func TestNormalizeScreenshotStyle(t *testing.T) {
	s := normalizeScreenshotStyle(ScreenshotStyle{Mode: "yes", Background: "nope", Padding: 7})
	if s.Mode != "off" || s.Background != "auto" || s.Padding != 10 {
		t.Fatalf("unexpected normalization: %+v", s)
	}
	s = normalizeScreenshotStyle(ScreenshotStyle{Mode: "on", Background: "sunset", Padding: 16})
	if s.Mode != "on" || s.Background != "sunset" || s.Padding != 16 {
		t.Fatalf("valid style was mangled: %+v", s)
	}
}

func TestCompressDecoratesScreenshot(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "shot.png")
	writePNG(t, src, gradientImage(600, 400, false))

	service := &AnyPic{tempDir: t.TempDir(), settings: defaultSettings()}
	service.settings.ScreenshotStyle = ScreenshotStyle{Mode: "on", Background: "sunset", Padding: 6}
	plain, err := service.compressOne(context.Background(), 1, src, "PNG", "shot.png", false, Settings{
		Format: "png", Suffix: "-plain",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plain.width != 600 || plain.height != 400 {
		t.Fatalf("plain image grew to %dx%d", plain.width, plain.height)
	}
	decorated, err := service.compressOne(context.Background(), 2, src, "PNG", "shot.png", true, Settings{
		Format: "png", Suffix: "-pretty",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 6% padding of the long edge on all four sides: 600 + 2*36 = 672.
	if decorated.width != 672 || decorated.height != 472 {
		t.Fatalf("decorated size %dx%d, want 672x472", decorated.width, decorated.height)
	}
	f, err := os.Open(decorated.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoded, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := decoded.At(0, 0).RGBA(); a != 0xffff {
		t.Fatal("corner of the decorated output is not the background")
	}
}

func sameColor(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
