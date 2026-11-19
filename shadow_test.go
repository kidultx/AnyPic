package main

import (
	"image"
	"image/color"
	"testing"
)

// TestShadowBlurKeepsSilhouetteAndFades guards the separable box blur behind
// shadows. The vertical pass used to read and overwrite the same buffer while
// it was still running its sum, so a rectangle came out as an oscillating
// plateau instead of a solid silhouette with a smooth falloff. That plateau
// is the wide striped band users saw under the card.
func TestShadowBlurKeepsSilhouetteAndFades(t *testing.T) {
	const w, h = 240, 180
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 40; y < 140; y++ {
		row := mask.Pix[y*mask.Stride : y*mask.Stride+w]
		for x := 40; x < 200; x++ {
			row[x] = 255
		}
	}
	gaussianBlurAlpha(mask, 8)
	for y := 60; y <= 120; y += 10 {
		if v := mask.Pix[y*mask.Stride+120]; v < 250 {
			t.Fatalf("interior alpha at y=%d is %d, want ~255; the blur is feeding its output back into its own sum", y, v)
		}
	}
	prev := 256
	for y := 140; y < h; y++ {
		v := int(mask.Pix[y*mask.Stride+120])
		if v > prev {
			t.Fatalf("alpha rose from %d to %d at y=%d; a shadow must fade away from the silhouette", prev, v, y)
		}
		prev = v
	}
	if prev != 0 {
		t.Fatalf("alpha is %d at the bottom edge, want 0; clamp-to-edge holds the shadow up", prev)
	}
}

// TestShadowDownscaleCoversCanvasEdges guards the integer downscale: dropping
// the right/bottom remainder shrinks the work buffer away from the canvas
// edge, which cuts the shadow and shifts it relative to the content.
func TestShadowDownscaleCoversCanvasEdges(t *testing.T) {
	src := image.NewAlpha(image.Rect(0, 0, 7, 5))
	for i := range src.Pix {
		src.Pix[i] = 0
	}
	src.SetAlpha(6, 4, color.Alpha{A: 255})
	out := downscaleAlpha(src, 2)
	if b := out.Bounds(); b.Dx() != 4 || b.Dy() != 3 {
		t.Fatalf("downscaleAlpha(7x5, 2) = %dx%d, want 4x3; the remainder cells must be kept", b.Dx(), b.Dy())
	}
	if got := out.AlphaAt(3, 2).A; got != 255 {
		t.Fatalf("bottom-right work pixel is %d, want 255; the dropped remainder carried the last row/column", got)
	}
}

// TestShadowFadesBeforeCanvasEdge is the user-visible contract: however wide
// or deep the shadow, it has to reach zero before the canvas edge. A hard cut
// shows up as a bright edge in the corner and a flat band under the card.
func TestShadowFadesBeforeCanvasEdge(t *testing.T) {
	src := solidImage(400, 300, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	for _, level := range []string{"soft", "deep"} {
		for _, padding := range []string{"compact", "normal", "roomy"} {
			style := FrameStyle{Background: "none", Radius: "none", Shadow: level, Padding: padding}
			out := decorateScreenshot(src, style)
			b := out.Bounds()
			x := b.Dx() / 2
			if got := out.RGBAAt(x, b.Dy()-1).A; got > 1 {
				t.Errorf("%s/%s: shadow alpha is %d at the bottom canvas edge, want ~0; the canvas cuts the shadow", level, padding, got)
			}
			p := framePadding(400, 300, padding)
			if got := out.RGBAAt(x, p+300+6).A; got == 0 {
				t.Errorf("%s/%s: no shadow just below the content", level, padding)
			}
		}
	}
}

// TestShadowFallsToZeroOnLargeFrames extends the edge contract to frames that
// are downscaled for the blur (k up to 6). That work scale is where the
// reported stripes lived: a plateau left by the blur is upscaled bilinearly
// into horizontal bands, and the canvas edge then cuts them off flat. The
// check is structural rather than a fixed profile: no alpha may survive in the
// last quarter of the margin, and the profile may not rise on its way down.
func TestShadowFallsToZeroOnLargeFrames(t *testing.T) {
	shapes := []struct {
		name string
		w, h int
	}{
		{"screenshot", 1220, 820},
		{"ultrawide", 1600, 200},
		{"4k", 3840, 2581},
	}
	for _, shape := range shapes {
		src := solidImage(shape.w, shape.h, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
		for _, level := range []string{"soft", "deep"} {
			for _, padding := range []string{"compact", "normal"} {
				style := FrameStyle{Background: "none", Radius: "none", Shadow: level, Padding: padding}
				out := decorateScreenshot(src, style)
				b := out.Bounds()
				p := framePadding(shape.w, shape.h, padding)
				contentBottom := p + shape.h
				x := b.Dx() / 2
				tailStart := contentBottom + (b.Dy()-contentBottom)*3/4
				maxTail, maxRise, prev := 0, 0, -1
				for y := contentBottom; y < b.Dy(); y++ {
					a := int(out.RGBAAt(x, y).A)
					if y >= tailStart && a > maxTail {
						maxTail = a
					}
					if prev >= 0 && a-prev > maxRise {
						maxRise = a - prev
					}
					prev = a
				}
				if maxTail > 1 {
					t.Errorf("%s %dx%d %s/%s: shadow alpha %d in the last quarter of the margin, want ~0; the canvas cuts a plateau into a band",
						shape.name, shape.w, shape.h, level, padding, maxTail)
				}
				if maxRise > 2 {
					t.Errorf("%s %dx%d %s/%s: shadow alpha rises by %d below the content; the blur leaves stripes",
						shape.name, shape.w, shape.h, level, padding, maxRise)
				}
				if got := out.RGBAAt(x, b.Dy()-1).A; got > 1 {
					t.Errorf("%s %dx%d %s/%s: shadow alpha is %d at the bottom canvas edge, want ~0",
						shape.name, shape.w, shape.h, level, padding, got)
				}
			}
		}
	}
}
