package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/disintegration/imaging"
)

// A visual harness, not an assertion test: it renders the beautify pipeline
// over the six shapes the workbench has to survive — a normal screenshot, a
// transparent PNG, an ultra-wide strip, a 4K capture, a tall column and a tiny
// crop — and writes them to build/beautify-samples/<label>/ so a human can
// look at the result and compare renders before/after a change (label is
// ANYPIC_SAMPLES_LABEL).
//
//	go test -run TestRenderBeautifySamples -v ./...   # with ANYPIC_SAMPLES=1
//
// It is skipped unless ANYPIC_SAMPLES is set, so the normal suite never writes
// files or spends time on it.
func TestRenderBeautifySamples(t *testing.T) {
	if os.Getenv("ANYPIC_SAMPLES") == "" {
		t.Skip("set ANYPIC_SAMPLES=1 to render the beautify sample sheet")
	}
	label := os.Getenv("ANYPIC_SAMPLES_LABEL")
	if label == "" {
		label = "current"
	}
	outDir := filepath.Join("build", "beautify-samples", label)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir samples: %v", err)
	}

	for _, sample := range beautifySamples(t) {
		for _, style := range sampleStyleVariants() {
			out := decorateScreenshot(sample.img, style)
			name := sample.name + "-" + styleName(style) + ".png"
			path := filepath.Join(outDir, name)
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("create %s: %v", path, err)
			}
			if err := png.Encode(f, out); err != nil {
				f.Close()
				t.Fatalf("encode %s: %v", path, err)
			}
			f.Close()
			t.Logf("%s  %dx%d -> %dx%d", name, sample.img.Bounds().Dx(), sample.img.Bounds().Dy(), out.Bounds().Dx(), out.Bounds().Dy())
		}
	}
	t.Logf("samples written to %s", outDir)
}

type beautifySample struct {
	name string
	img  image.Image
}

// sampleStyleVariants are the looks worth eyeballing: every gradient preset
// (including the light and dark ends where the hairline ring flips), the
// transparent-background case, the deep shadow, the compact and roomy
// margins, and every day-card pattern. Rendering the full pattern set against
// all six shapes is what shows whether a 9px motif survives 4K, whether an
// ultra-wide or tall frame keeps its tiles square, and whether a transparent
// capture still casts its own silhouette.
func sampleStyleVariants() []FrameStyle {
	def := defaultFrameStyle()
	none := def
	none.Background = "none"
	deep := def
	deep.Shadow = "deep"
	compact := def
	compact.Padding = "compact"
	roomy := def
	roomy.Padding = "roomy"
	variants := []FrameStyle{none, deep, compact, roomy}
	for _, id := range []string{"indigo", "dusk", "ocean", "mint", "rose", "sunset", "slate", "charcoal"} {
		gradient := def
		gradient.BGGradient = id
		variants = append(variants, gradient)
	}
	for _, id := range patternOrder {
		pattern := def
		pattern.Background = "pattern"
		pattern.BGPattern = id
		variants = append(variants, pattern)
	}
	return variants
}

func styleName(s FrameStyle) string {
	var name string
	switch s.Background {
	case "none":
		name = "nobg"
	case "pattern":
		name = "pattern-" + normalizeFrameStyle(s).BGPattern
	case "gradient":
		name = s.BGGradient
	default:
		name = "solid"
	}
	if s.Shadow == "deep" {
		name += "-deep"
	}
	if s.Padding != "" && s.Padding != "normal" {
		name += "-" + s.Padding
	}
	return name
}

// beautifySamples loads the four shapes. The normal and 4K cases reuse the
// shipped README screenshot so the samples show real UI content rather than
// synthetic blocks.
func beautifySamples(t *testing.T) []beautifySample {
	t.Helper()
	var base image.Image
	if f, err := os.Open("docs/screenshot.png"); err == nil {
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("decode docs/screenshot.png: %v", err)
		}
		base = img
	} else {
		t.Logf("docs/screenshot.png unavailable (%v); using a synthetic capture", err)
		base = syntheticCapture(1220, 820)
	}

	wide := cropStrip(base, 1600, 200)
	tall := cropColumn(base, 200, 820)
	tiny := imaging.Resize(base, 64, 0, imaging.Lanczos)
	return []beautifySample{
		{name: "1-normal", img: base},
		{name: "2-transparent", img: transparentLogo(800, 600)},
		{name: "3-ultrawide", img: wide},
		{name: "4-4k", img: imaging.Resize(base, 3840, 0, imaging.Lanczos)},
		{name: "5-ultratall", img: tall},
		{name: "6-tiny", img: tiny},
	}
}

// syntheticCapture stands in for the README shot: a plausible window — a
// toolbar strip over a content area — so samples stay readable without it.
func syntheticCapture(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{0xfa, 0xfa, 0xfb, 0xff}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, w, 52), image.NewUniform(color.RGBA{0xf1, 0xf1, 0xf3, 0xff}), image.Point{}, draw.Src)
	for i := 0; i < 6; i++ {
		x := 40 + i*180
		draw.Draw(img, image.Rect(x, 200, x+150, 300), image.NewUniform(color.RGBA{0x4f, 0x46, 0xe5, 0xff}), image.Point{}, draw.Src)
	}
	return img
}

// cropStrip takes a full-width band from the middle of src, the shape of a
// menu-bar or toolbar capture.
func cropStrip(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	if b.Dx() < w {
		src = imaging.Resize(src, w, 0, imaging.Lanczos)
		b = src.Bounds()
	}
	y := b.Min.Y + (b.Dy()-h)/2
	if y < b.Min.Y {
		y = b.Min.Y
	}
	return imaging.Crop(src, image.Rect(b.Min.X, y, b.Min.X+w, y+h))
}

// cropColumn takes a full-height column from the middle of src, the shape of a
// long page or chat sidebar capture.
func cropColumn(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	if b.Dy() < h {
		src = imaging.Resize(src, 0, h, imaging.Lanczos)
		b = src.Bounds()
	}
	x := b.Min.X + (b.Dx()-w)/2
	if x < b.Min.X {
		x = b.Min.X
	}
	return imaging.Crop(src, image.Rect(x, b.Min.Y, x+w, b.Min.Y+h))
}

// transparentLogo is a rounded badge with fully transparent surroundings —
// the case that shows a shadow leaking under the content or a backdrop
// painted where the user asked for none.
func transparentLogo(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	badge := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(badge, badge.Bounds(), image.NewUniform(color.RGBA{0x1d, 0x4e, 0xd8, 0xff}), image.Point{}, draw.Src)
	mask := roundedRectMask(w, h, min(w, h)/6)
	draw.DrawMask(img, img.Bounds(), badge, image.Point{}, mask, image.Point{}, draw.Src)
	// A transparent hole in the middle, so partial alpha is exercised too.
	inner := image.NewRGBA(image.Rect(0, 0, w/3, h/3))
	draw.DrawMask(img, image.Rect(w/3, h/3, 2*w/3, 2*h/3), inner, image.Point{}, roundedRectMask(w/3, h/3, 40), image.Point{}, draw.Src)
	return img
}
