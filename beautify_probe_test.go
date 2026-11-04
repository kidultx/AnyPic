package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestBeautifyProbe renders a spread of real-world shapes through the
// decorator and writes the results to $BEAUTIFY_OUT for visual inspection.
// It also asserts the invariants a decorated picture must keep: the canvas
// grows by the padding on every side, the shot stays centered, and the
// corners belong to the background.
func TestBeautifyProbe(t *testing.T) {
	out := os.Getenv("BEAUTIFY_OUT")
	if out != "" {
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name   string
		src    image.Image
		styles []ScreenshotStyle
	}{
		{"light-window", windowShot(1280, 800, true), []ScreenshotStyle{
			{Mode: "on", Background: "auto", Padding: 10},
			{Mode: "on", Background: "auto", Padding: 0},
		}},
		{"dark-window", windowShot(1280, 800, false), []ScreenshotStyle{
			{Mode: "on", Background: "auto", Padding: 10},
		}},
		{"paper-preset", windowShot(900, 600, true), []ScreenshotStyle{
			{Mode: "on", Background: "paper", Padding: 16},
			{Mode: "on", Background: "ink", Padding: 6},
		}},
		{"transparent", transparentShot(600, 400), []ScreenshotStyle{
			{Mode: "on", Background: "auto", Padding: 10},
		}},
		{"tiny", windowShot(40, 30, true), []ScreenshotStyle{
			{Mode: "on", Background: "auto", Padding: 10},
		}},
		{"panorama", windowShot(300, 1800, false), []ScreenshotStyle{
			{Mode: "on", Background: "auto", Padding: 10},
		}},
		{"4k", windowShot(3840, 2160, false), []ScreenshotStyle{
			{Mode: "on", Background: "auto", Padding: 10},
		}},
	}

	for _, tc := range cases {
		src := tc.src
		sb := src.Bounds()
		w, h := sb.Dx(), sb.Dy()
		for i, style := range tc.styles {
			outImg := decorateScreenshot(src, style)
			ob := outImg.Bounds()
			pad := paddingPx(style.Padding, max(w, h))
			if wantW, wantH := w+2*pad, h+2*pad; ob.Dx() != wantW || ob.Dy() != wantH {
				t.Errorf("%s/%d: canvas %dx%d, want %dx%d", tc.name, i, ob.Dx(), ob.Dy(), wantW, wantH)
			}
			if _, _, _, a := outImg.At(0, 0).RGBA(); a != 0xffff {
				t.Errorf("%s/%d: background corner transparent", tc.name, i)
			}
			// Shot center must be the source center (symmetric padding).
			if got, want := outImg.At(pad+w/2, pad+h/2), src.At(sb.Min.X+w/2, sb.Min.Y+h/2); !sameColor(got, want) {
				t.Errorf("%s/%d: shot is not centered: %v vs %v", tc.name, i, got, want)
			}
			if out != "" {
				name := filepath.Join(out, tc.name+"-"+style.Background+"-pad"+itoa(style.Padding)+".png")
				f, err := os.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, outImg); err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
		}
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// windowShot fakes an application window: chrome, sidebar and content.
func windowShot(w, h int, light bool) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	bar, body, side := color.NRGBA{R: 236, G: 237, B: 240, A: 255}, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, color.NRGBA{R: 246, G: 247, B: 249, A: 255}
	text := color.NRGBA{R: 209, G: 213, B: 219, A: 255}
	if !light {
		bar, body, side = color.NRGBA{R: 40, G: 41, B: 45, A: 255}, color.NRGBA{R: 24, G: 25, B: 28, A: 255}, color.NRGBA{R: 32, G: 33, B: 37, A: 255}
		text = color.NRGBA{R: 70, G: 72, B: 78, A: 255}
	}
	chrome := max(8, h/32)
	sidebar := max(8, w/5)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case y < chrome:
				img.SetNRGBA(x, y, bar)
			case x < sidebar:
				img.SetNRGBA(x, y, side)
			default:
				img.SetNRGBA(x, y, body)
			}
		}
	}
	draw.Draw(img, image.Rect(sidebar+20, chrome+20, w-20, chrome+20+max(4, h/40)), &image.Uniform{C: text}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(sidebar+20, chrome+40+max(4, h/40), w*2/3, chrome+40+2*max(4, h/40)), &image.Uniform{C: text}, image.Point{}, draw.Src)
	return img
}

func transparentShot(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Opaque core with a fully transparent margin.
			if x > w/8 && x < w*7/8 && y > h/8 && y < h*7/8 {
				img.SetNRGBA(x, y, color.NRGBA{R: 120, G: 160, B: 220, A: 255})
			}
		}
	}
	return img
}
