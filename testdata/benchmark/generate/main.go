// Command benchgen writes the fixed benchmark images under
// testdata/benchmark. The output is deterministic: same code, same bytes.
//
// Run from the repository root:
//
//	go run ./testdata/benchmark/generate
//
// The images are small on purpose (the whole set stays under 1 MB), so the
// benchmark test can run on every checkout and a regression in encoder
// parameters shows up as a size/SSIM change in baseline.json.
package main

import (
	"flag"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand"
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"
)

func main() {
	out := flag.String("out", filepath.Join("testdata", "benchmark"), "output directory")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		panic(err)
	}
	writePNG(filepath.Join(*out, "screenshot.png"), screenshotImage())
	writePNG(filepath.Join(*out, "transparent.png"), transparentImage())
	writeJPEG(filepath.Join(*out, "photo.jpg"), photoImage())
	writeGIF(filepath.Join(*out, "animated.gif"), animatedGIF())
	writeGIF(filepath.Join(*out, "animated-real.gif"), realScreenshotGIF())
}

func writePNG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func writeJPEG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 88}); err != nil {
		panic(err)
	}
}

func writeGIF(path string, anim *gif.GIF) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := gif.EncodeAll(f, anim); err != nil {
		panic(err)
	}
}

func fill(img *image.NRGBA, x0, y0, x1, y1 int, c color.NRGBA) {
	for y := max(0, y0); y < min(img.Bounds().Dy(), y1); y++ {
		for x := max(0, x0); x < min(img.Bounds().Dx(), x1); x++ {
			img.SetNRGBA(x, y, c)
		}
	}
}

// screenshotImage is a flat, UI-like 1280×800 frame: large solid areas,
// hairlines and small accent blocks. It stands in for the desktop captures
// AnyPic sees most often, where PNG is cheap and WebP mostly has to preserve
// edges.
func screenshotImage() *image.NRGBA {
	const w, h = 1280, 800
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	fill(img, 0, 0, w, h, color.NRGBA{R: 0xf6, G: 0xf6, B: 0xf7, A: 255})    // page
	fill(img, 0, 0, w, 64, color.NRGBA{R: 255, G: 255, B: 255, A: 255})      // title bar
	fill(img, 0, 64, 272, h, color.NRGBA{R: 0xfb, G: 0xfb, B: 0xfc, A: 255}) // sidebar
	fill(img, 272, 64, 273, h, color.NRGBA{R: 0xe7, G: 0xe7, B: 0xea, A: 255})

	// Toolbar: three pills, one highlighted.
	fill(img, 304, 24, 376, 48, color.NRGBA{R: 0x4f, G: 0x46, B: 0xe5, A: 255})
	fill(img, 392, 24, 464, 48, color.NRGBA{R: 0xec, G: 0xec, B: 0xef, A: 255})
	fill(img, 480, 24, 552, 48, color.NRGBA{R: 0xec, G: 0xec, B: 0xef, A: 255})
	fill(img, 24, 24, 96, 48, color.NRGBA{R: 0xe3, G: 0xe3, B: 0xe8, A: 255})

	// Sidebar rows: label, control, note.
	greys := []color.NRGBA{
		{R: 0xd9, G: 0xd9, B: 0xde, A: 255},
		{R: 0xe4, G: 0xe4, B: 0xe8, A: 255},
		{R: 0xef, G: 0xef, B: 0xf2, A: 255},
	}
	for row := 0; row < 7; row++ {
		y := 104 + row*86
		fill(img, 24, y, 112, y+12, greys[row%len(greys)])
		fill(img, 24, y+24, 248, y+56, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		fill(img, 32, y+36, 120+row*8, y+44, greys[(row+1)%len(greys)])
	}

	// Content cards, 3×2, with a thumbnail band and text lines.
	accents := []color.NRGBA{
		{R: 0x7b, G: 0xc9, B: 0xcb, A: 255},
		{R: 0xf5, G: 0xa1, B: 0xa7, A: 255},
		{R: 0x9c, G: 0x8f, B: 0xe0, A: 255},
		{R: 0xff, G: 0xb0, B: 0x7a, A: 255},
		{R: 0x8d, G: 0xc2, B: 0x8a, A: 255},
		{R: 0x6f, G: 0x9f, B: 0xd8, A: 255},
	}
	for i := 0; i < 6; i++ {
		col, row := i%3, i/3
		x := 304 + col*312
		y := 104 + row*300
		fill(img, x, y, x+288, y+264, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		fill(img, x, y, x+288, y+148, accents[i])
		fill(img, x+16, y+164, x+200, y+176, greys[0])
		fill(img, x+16, y+188, x+256, y+198, greys[1])
		fill(img, x+16, y+208, x+156, y+218, greys[2])
		fill(img, x+16, y+236, x+76, y+252, accents[(i+1)%len(accents)])
	}
	return img
}

// transparentImage is a 512×384 alpha logo: an opaque disc with a soft
// anti-aliased rim on a fully transparent canvas. It checks that the
// pipeline never flattens transparency.
func transparentImage() *image.NRGBA {
	const w, h = 512, 384
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := math.Hypot(float64(x)-256, float64(y)-192)
			a := uint8(0)
			switch {
			case d <= 112:
				a = 255
			case d <= 124:
				a = uint8(255 * (1 - (d-112)/12))
			}
			if a == 0 {
				continue
			}
			t := math.Hypot(float64(x)-220, float64(y)-160) / 200
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(120 + 110*min(1, t)),
				G: uint8(60 + 70*min(1, t)),
				B: uint8(200 - 80*min(1, t)),
				A: a,
			})
		}
	}
	return img
}

// photoImage is a 1200×800 landscape with smooth gradients, a sun and
// textured hills: the lossy encoders' hard case.
func photoImage() *image.NRGBA {
	const w, h = 1200, 800
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(20261113))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fy := float64(y) / h
			// Sky: blue at the top to warm at the horizon.
			r := 120 + 130*fy
			g := 165 + 60*fy
			b := 220 - 40*fy
			// Sun glow.
			dx, dy := float64(x)-880, float64(y)-210
			glow := math.Exp(-(dx*dx + dy*dy) / (2 * 90 * 90))
			r += 110 * glow
			g += 80 * glow
			b -= 20 * glow
			// Hills, drawn as a noisy ridge with two bands.
			ridge := h*0.52 + 34*math.Sin(float64(x)/130) + 18*math.Sin(float64(x)/47)
			if float64(y) > ridge {
				depth := math.Min(1, (float64(y)-ridge)/(h-ridge))
				r = 54 + 46*depth
				g = 92 + 50*depth
				b = 66 + 30*depth
				texture := 14 * math.Sin(float64(x)/9+float64(y)/17)
				r += texture
				g += texture * 0.8
				b += texture * 0.4
			}
			// A gentle sensor-like grain keeps the encoders honest.
			n := float64(rng.Intn(13) - 6)
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(max(0, min(255, r+n))),
				G: uint8(max(0, min(255, g+n))),
				B: uint8(max(0, min(255, b+n))),
				A: 255,
			})
		}
	}
	return img
}

// realScreenshotGIF derives a 360×240, 6-frame pan from the app's real
// screenshot in docs/. It brings a real UI capture into the animation
// category (cropping, palette quantization, motion) without a second
// hand-maintained image. Run the generator from the repository root.
func realScreenshotGIF() *gif.GIF {
	f, err := os.Open(filepath.Join("docs", "screenshot.png"))
	if err != nil {
		panic(err)
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		panic(err)
	}
	const w, h = 360, 240
	anim := &gif.GIF{LoopCount: 0}
	for frame := 0; frame < 6; frame++ {
		x0 := 40 + frame*30
		crop := imaging.Crop(src, image.Rect(x0, 80, x0+900, 80+600))
		fit := imaging.Fit(crop, w, h, imaging.Lanczos)
		frameImg := image.NewPaletted(image.Rect(0, 0, w, h), palette.Plan9)
		draw.FloydSteinberg.Draw(frameImg, frameImg.Bounds(), fit, image.Point{})
		anim.Image = append(anim.Image, frameImg)
		anim.Delay = append(anim.Delay, 15)
	}
	return anim
}

// animatedGIF is a 320×240, 10-frame loop: a moving disc over a gradient.
// Multi-frame input is what the animated path must preserve.
func animatedGIF() *gif.GIF {
	const w, h = 320, 240
	pal := make(color.Palette, 128)
	for i := range pal {
		t := float64(i) / float64(len(pal)-1)
		pal[i] = color.NRGBA{
			R: uint8(40 + 180*t),
			G: uint8(30 + 120*(1-t)),
			B: uint8(120 + 100*math.Sin(t*math.Pi)),
			A: 255,
		}
	}
	anim := &gif.GIF{LoopCount: 0}
	for frame := 0; frame < 10; frame++ {
		img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.SetColorIndex(x, y, uint8((x*3+y*2)/12+frame*2)%128)
			}
		}
		cx := 40 + frame*25
		cy := 150 - int(60*math.Sin(float64(frame)/10*2*math.Pi))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if math.Hypot(float64(x-cx), float64(y-cy)) <= 28 {
					img.SetColorIndex(x, y, 127)
				}
			}
		}
		anim.Image = append(anim.Image, img)
		anim.Delay = append(anim.Delay, 10)
	}
	return anim
}
