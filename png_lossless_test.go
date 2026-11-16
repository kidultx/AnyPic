package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestPNGLosslessBeatsStdlibOnBenchmark proves the production encoder is
// worth its extra passes: on the committed benchmark PNGs it is never larger
// than image/png at the same effort level, it is strictly smaller where the
// report found headroom, and every byte it writes decodes back to exactly the
// pixels it was given (the §5① guard would otherwise send the result back to
// the original).
func TestPNGLosslessBeatsStdlibOnBenchmark(t *testing.T) {
	// logo.png is the tight case: the search saves only 13 bytes over the
	// standard library, so it is held to "no larger" instead of "smaller".
	tight := map[string]bool{"logo.png": true}
	for _, name := range []string{"screenshot-real.png", "transparent.png", "logo.png", "screenshot.png"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(benchmarkDir, name))
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			pixels := img.Bounds().Dx() * img.Bounds().Dy()
			baseline, err := encodePNGStdlib(img, pngCompressionLevel(pixels))
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := encodePNGLossless(img)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) > len(baseline) {
				t.Fatalf("filter search produced %d bytes, larger than png.Encode's %d", len(got), len(baseline))
			}
			if !tight[name] && len(got) >= len(baseline) {
				t.Fatalf("filter search produced %d bytes, not smaller than png.Encode's %d", len(got), len(baseline))
			}
			if !losslessPixelsEqual(img, got) {
				t.Fatal("filter search changed pixels")
			}
			// The production seam must actually use the search, not the
			// standard-library encoder alone.
			wired, err := encodeStill(img, "PNG", 82)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(wired, got) {
				t.Fatalf("encodeStill wrote %d bytes, search returned %d; the encoder is not wired", len(wired), len(got))
			}
		})
	}
}

// TestPNGLosslessTinyImagesDoNotPanic covers the degenerate dimensions the
// filter loop indexes into: a single pixel, one row, and one flat colour.
func TestPNGLosslessTinyImagesDoNotPanic(t *testing.T) {
	transparent := color.NRGBA{R: 9, G: 8, B: 7, A: 0}
	opaque := color.NRGBA{R: 1, G: 2, B: 3, A: 0xFF}
	cases := []struct {
		name string
		img  image.Image
	}{
		{"1x1-opaque", image.NewNRGBA(image.Rect(0, 0, 1, 1))},
		{"1x1-transparent", image.NewNRGBA(image.Rect(0, 0, 1, 1))},
		{"2x2-flat", image.NewNRGBA(image.Rect(0, 0, 2, 2))},
		{"1x8-single-column", image.NewNRGBA(image.Rect(0, 0, 1, 8))},
	}
	// Fill after construction so the opaque case really has full alpha.
	cases[0].img.(*image.NRGBA).SetNRGBA(0, 0, opaque)
	cases[1].img.(*image.NRGBA).SetNRGBA(0, 0, transparent)
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			cases[2].img.(*image.NRGBA).SetNRGBA(x, y, color.NRGBA{R: 4, G: 5, B: 6, A: 0xFF})
		}
	}
	for y := 0; y < 8; y++ {
		cases[3].img.(*image.NRGBA).SetNRGBA(0, y, color.NRGBA{R: uint8(y), G: 0, B: 0, A: 0xFF})
	}
	for _, c := range cases {
		data, _, err := encodePNGLossless(c.img)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		decoded, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: output is not a decodable PNG: %v", c.name, err)
		}
		if got, want := decoded.Bounds().Size(), c.img.Bounds().Size(); got != want {
			t.Fatalf("%s: size = %v, want %v", c.name, got, want)
		}
		if !losslessPixelsEqual(c.img, data) {
			t.Fatalf("%s: encoded pixels differ from the source", c.name)
		}
	}
}

// TestPNGLosslessFilterOutputIsLossless proves the filter-search encoder is a
// real PNG encoder before it is used to measure anything: every filter policy
// must decode back to the same pixels, for opaque, translucent and grey
// sources.
func TestPNGLosslessFilterOutputIsLossless(t *testing.T) {
	cases := []struct {
		name string
		img  image.Image
	}{
		{"opaque", gradientImage(37, 23, false)},
		{"alpha", gradientImage(37, 23, true)},
		{"grey", grayImage(37, 23)},
	}
	for _, c := range cases {
		for _, strategy := range []pngFilterStrategy{
			pngFilterAdaptive, pngFilterNone, pngFilterSub,
			pngFilterUp, pngFilterAverage, pngFilterPaeth,
		} {
			data, err := encodePNGWithFilter(c.img, strategy)
			if err != nil {
				t.Fatalf("%s/%s: %v", c.name, strategy, err)
			}
			if !losslessPixelsEqual(c.img, data) {
				t.Fatalf("%s/%s: encoded pixels differ from the source", c.name, strategy)
			}
			decoded, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("%s/%s: output is not a decodable PNG: %v", c.name, strategy, err)
			}
			if got, want := decoded.Bounds().Size(), c.img.Bounds().Size(); got != want {
				t.Fatalf("%s/%s: size = %v, want %v", c.name, strategy, got, want)
			}
		}
	}
}

func grayImage(width, height int) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8((x*7 + y*3) % 256)})
		}
	}
	return img
}

// TestPNGLosslessFeasibility measures the production filter search on the
// committed benchmark PNGs and compares it with image/png. The numbers are
// logged rather than asserted: this is the report's feasibility check (§5③).
// It runs six zlib passes per image (and the search again to prove its choice
// matches the per-strategy minimum), so the normal suite skips it; run it
// explicitly with
//
//	ANYPIC_PNG_FEASIBILITY=1 go test -run TestPNGLosslessFeasibility -v .
func TestPNGLosslessFeasibility(t *testing.T) {
	if os.Getenv("ANYPIC_PNG_FEASIBILITY") == "" {
		t.Skip("set ANYPIC_PNG_FEASIBILITY=1 to measure the encoder against the benchmark set")
	}
	files := []string{"screenshot-real.png", "transparent.png", "logo.png", "screenshot.png"}
	strategies := []pngFilterStrategy{
		pngFilterAdaptive, pngFilterNone, pngFilterSub,
		pngFilterUp, pngFilterAverage, pngFilterPaeth,
	}
	for _, name := range files {
		path := filepath.Join(benchmarkDir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		baseline, err := encodePNGStdlib(img, pngCompressionLevel(img.Bounds().Dx()*img.Bounds().Dy()))
		if err != nil {
			t.Fatal(err)
		}
		baselineSize := len(baseline)
		t.Logf("%s: source %d, png.Encode BestCompression %d", name, len(raw), baselineSize)
		bestSize, bestName := baselineSize, "png.Encode.BestCompression"
		for _, strategy := range strategies {
			data, err := encodePNGWithFilter(img, strategy)
			if err != nil {
				t.Fatalf("%s/%s: %v", name, strategy, err)
			}
			if !losslessPixelsEqual(img, data) {
				t.Fatalf("%s/%s: encoded pixels differ", name, strategy)
			}
			saved := float64(baselineSize-len(data)) / float64(baselineSize) * 100
			t.Logf("  %-9s %7d bytes  (%+.1f%% vs current)", strategy, len(data), saved)
			if len(data) < bestSize {
				bestSize, bestName = len(data), strategy.String()
			}
		}
		saved := float64(baselineSize-bestSize) / float64(baselineSize) * 100
		best, _, err := encodePNGLossless(img)
		if err != nil {
			t.Fatal(err)
		}
		if len(best) != bestSize {
			t.Fatalf("%s: encodePNGLossless picked %d bytes, per-strategy best was %d", name, len(best), bestSize)
		}
		if !losslessPixelsEqual(img, best) {
			t.Fatalf("%s: encodePNGLossless changed pixels", name)
		}
		t.Logf("  best: %s %d bytes (%.1f%% smaller than current)", bestName, bestSize, saved)
	}
}
