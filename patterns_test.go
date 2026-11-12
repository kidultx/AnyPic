package main

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// TestPatternBackdropsRender walks the closed pattern set and asserts the
// properties a backdrop must have regardless of motif: it covers the whole
// margin opaquely, it actually varies (a silently-uniform fill would mean a
// painter never ran), and it leaves the content untouched.
func TestPatternBackdropsRender(t *testing.T) {
	src := solidImage(240, 160, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	p := framePadding(240, 160, "normal")
	for _, id := range patternOrder {
		style := FrameStyle{
			Background: "pattern", BGPattern: id,
			Radius: "none", Shadow: "off", Ring: false,
		}
		out := decorateScreenshot(src, style)
		b := out.Bounds()
		if b.Dx() != 240+2*p || b.Dy() != 160+2*p {
			t.Fatalf("%s: canvas %v, want %dx%d", id, b, 240+2*p, 160+2*p)
		}

		seen := map[color.RGBA]bool{}
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				if x >= p && x < p+240 && y >= p && y < p+160 {
					continue // the content
				}
				r, g, bl, a := out.At(x, y).RGBA()
				if a != 0xffff {
					t.Fatalf("%s: margin pixel (%d,%d) alpha %d, want opaque", id, x, y, a)
				}
				seen[color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8), A: 0xff}] = true
			}
		}
		if len(seen) < 2 {
			t.Errorf("%s: backdrop is a uniform fill (%d colors); the motif never painted", id, len(seen))
		}
		if r, g, bl, a := out.At(p+120, p+80).RGBA(); r != 0xe0e0 || g != 0x2020 || bl != 0x2020 || a != 0xffff {
			t.Errorf("%s: content pixel (%d,%d,%d,%d) changed by the backdrop", id, r, g, bl, a)
		}
	}
}

// TestPatternFallback pins the normalization contract: the pattern id is a
// closed set on the Go side, and a stale or bogus page gets the reference's
// own fallback (teal), never an undefined render.
func TestPatternFallback(t *testing.T) {
	for _, bogus := range []string{"", "nope", "TEAL", "gingham "} {
		s := normalizeFrameStyle(FrameStyle{Background: "pattern", BGPattern: bogus})
		if s.Background != "pattern" || s.BGPattern != "teal" {
			t.Errorf("pattern %q normalized to %q/%q, want pattern/teal", bogus, s.Background, s.BGPattern)
		}
	}
	for _, id := range patternOrder {
		if s := normalizeFrameStyle(FrameStyle{Background: "pattern", BGPattern: id}); s.BGPattern != id {
			t.Errorf("valid pattern %q normalized to %q", id, s.BGPattern)
		}
	}
	// A zero style keeps the documented defaults even though the pattern is
	// not selected.
	if s := normalizeFrameStyle(FrameStyle{}); s.BGPattern != "teal" {
		t.Errorf("zero style pattern %q, want teal", s.BGPattern)
	}
}

// TestPatternScale pins the tile scale's contract: one scalar, floored so a
// tiny crop still shows its motif, capped so a huge export does not turn into
// a single giant dot, orientation-independent, and carried into a scaled
// preview proportionally so the preview is the export, smaller.
func TestPatternScale(t *testing.T) {
	if got := patternScaleFor(100, 100); got != 0.75 {
		t.Errorf("tiny canvas scale %v, want the 0.75 floor", got)
	}
	if got := patternScaleFor(100000, 100); got != 4 {
		t.Errorf("huge canvas scale %v, want the 4 cap", got)
	}
	if got := patternScaleFor(1300, 800); math.Abs(got-1) > 1e-9 {
		t.Errorf("1300px canvas scale %v, want 1", got)
	}
	if a, b := patternScaleFor(800, 1600), patternScaleFor(1600, 800); a != b {
		t.Errorf("scale depends on orientation: portrait %v, landscape %v", a, b)
	}
	if a, b := patternScaleFor(900, 600), patternScaleFor(1800, 1200); b <= a {
		t.Errorf("scale does not grow with the canvas: %v then %v", a, b)
	}

	g := computeFrame(400, 300, FrameStyle{Background: "pattern", BGPattern: "dots"})
	half := g.scaled(0.5)
	if want := g.patternScale * 0.5; math.Abs(half.patternScale-want) > 1e-9 {
		t.Errorf("scaled preview pattern scale %v, want %v", half.patternScale, want)
	}
}

// TestPatternSurvivesDegenerateCanvases keeps the painter total: a 1×1 canvas,
// an odd tile remainder and a one-pixel source must all come out opaque and
// panic-free.
func TestPatternSurvivesDegenerateCanvases(t *testing.T) {
	for _, id := range patternOrder {
		for _, size := range []image.Point{{1, 1}, {3, 7}, {37, 11}} {
			img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
			paintPattern(img, id, patternScaleFor(size.X, size.Y))
			for y := 0; y < size.Y; y++ {
				for x := 0; x < size.X; x++ {
					if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
						t.Fatalf("%s on %v: pixel (%d,%d) alpha %d, want opaque", id, size, x, y, a)
					}
				}
			}
		}
	}

	src := solidImage(1, 1, color.RGBA{R: 0x20, G: 0x40, B: 0xe0, A: 0xff})
	out := decorateScreenshot(src, FrameStyle{Background: "pattern", BGPattern: "seigaiha"})
	p := framePadding(1, 1, "normal")
	if b := out.Bounds(); b.Dx() != 1+2*p || b.Dy() != 1+2*p {
		t.Errorf("1×1 source framed to %v, want %dx%d with the minimum margin", b, 1+2*p, 1+2*p)
	}
}

// TestDecorateScaledIsTheFrameSmaller protects the preview path: the
// workbench renders through decorateScaled, so the small frame must be the
// export's geometry halved — same margins, same content, opaque pattern —
// not a differently proportioned render.
func TestDecorateScaledIsTheFrameSmaller(t *testing.T) {
	src := solidImage(900, 600, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	style := FrameStyle{Background: "pattern", BGPattern: "gingham"}
	full := decorateScreenshot(src, style)
	half := decorateScaled(src, style, 0.5)
	fb, hb := full.Bounds(), half.Bounds()
	if abs(hb.Dx()*2-fb.Dx()) > 2 || abs(hb.Dy()*2-fb.Dy()) > 2 {
		t.Errorf("half-size frame %v, want it within 2px of half of %v", hb, fb)
	}
	if r, g, b, a := half.At(hb.Dx()/2, hb.Dy()/2).RGBA(); r != 0xe0e0 || g != 0x2020 || b != 0x2020 || a != 0xffff {
		t.Errorf("scaled content pixel (%d,%d,%d,%d), want the red source", r, g, b, a)
	}
	if _, _, _, a := half.At(1, 1).RGBA(); a != 0xffff {
		t.Errorf("scaled pattern corner alpha %d, want opaque", a)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestPatternShadowIsTinted checks the one visual nod to the reference's
// per-paper shadows: the cast on a pattern takes the paper's hue, while plain
// backdrops keep a neutral black.
func TestPatternShadowIsTinted(t *testing.T) {
	if got := shadowTint(FrameStyle{Background: "pattern", BGPattern: "velvet"}); got != (color.NRGBA{A: 0xff}) {
		t.Errorf("velvet shadow tint %+v, want neutral black", got)
	}
	if got := shadowTint(FrameStyle{Background: "pattern", BGPattern: "gingham"}); got.R != 0x5a || got.A != 0xff {
		t.Errorf("gingham shadow tint %+v, want the reference's brown", got)
	}
	if got := shadowTint(FrameStyle{Background: "gradient"}); got != (color.NRGBA{A: 0xff}) {
		t.Errorf("gradient shadow tint %+v, want neutral black", got)
	}
}
