package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	"github.com/disintegration/imaging"
)

// Screenshot appearance: two independent capture preferences — a border
// (rounded corners, hairline ring, soft shadow) and a background (an expanded
// canvas with a filled backdrop). Both default to off, so captures keep the
// old behavior until the user opts in. Decoration runs before the capture is
// copied, saved and queued, so every consumer sees the same framed image.

const (
	// shadowAlpha is the silhouette's peak alpha before blurring.
	shadowAlpha = 66
	// ringWidth is the hairline ring's stroke, in pixels. Screenshots are
	// usually retina 2x, where 2px read as a 1pt hairline.
	ringWidth = 2
)

// framePadding decides how much canvas the decoration adds around the
// capture. It scales with the image — retina captures are 2x — and is clamped
// so tiny shots keep a usable frame and huge ones don't grow absurd borders.
func framePadding(w, h int) int {
	p := max(w, h) / 25
	p = max(24, min(120, p))
	// Keep the padding from eating more than a quarter of the shorter side.
	if cap := min(w, h) / 4; p > cap {
		p = max(2, cap)
	}
	return p
}

// frameRadius scales the rounded-corner radius with the image, clamped to a
// band that reads as a window on retina captures and stays sane on tiny ones.
func frameRadius(w, h int) int {
	r := min(w, h) / 40
	r = max(8, min(32, r))
	if cap := min(w, h) / 2; r > cap {
		r = max(1, cap)
	}
	return r
}

// decorateScreenshot draws the capture onto an expanded canvas: rounded
// corners whenever either preference is on, a hairline ring and soft shadow
// when border is on, and a filled backdrop when background is not "off". The
// canvas is larger by framePadding on every side.
func decorateScreenshot(src image.Image, border bool, background string) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	p := framePadding(w, h)
	r := frameRadius(w, h)
	cw, ch := w+2*p, h+2*p

	canvas := image.NewRGBA(image.Rect(0, 0, cw, ch))

	if background != "off" {
		draw.Draw(canvas, canvas.Bounds(), newBackdrop(cw, ch, background), image.Point{}, draw.Src)
	}

	mask := roundedRectMask(w, h, r)

	if border {
		// Soft shadow: a black silhouette of the content rect, offset a
		// little and blurred, composited over the backdrop (or the
		// transparent padding when there is no backdrop).
		dy := max(2, p/10)
		silhouette := image.NewRGBA(image.Rect(0, 0, cw, ch))
		draw.DrawMask(silhouette, image.Rect(p, p+dy, p+w, p+dy+h),
			&image.Uniform{C: color.RGBA{A: shadowAlpha}}, image.Point{}, mask, image.Point{}, draw.Src)
		sigma := max(3.0, float64(p)/5)
		draw.Draw(canvas, canvas.Bounds(), imaging.Blur(silhouette, sigma), image.Point{}, draw.Over)

		// Hairline ring: a rounded rect one ringWidth larger than the
		// content, drawn under the content so only the band shows.
		ringMask := roundedRectMask(w+2*ringWidth, h+2*ringWidth, r+ringWidth)
		draw.DrawMask(canvas, image.Rect(p-ringWidth, p-ringWidth, p+w+ringWidth, p+h+ringWidth),
			&image.Uniform{C: ringColor(background)}, image.Point{}, ringMask, image.Point{}, draw.Over)
	}

	// Content: the capture masked to rounded corners, over everything else.
	masked := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.DrawMask(masked, masked.Bounds(), src, b.Min, mask, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(p, p, p+w, p+h), masked, image.Point{}, draw.Over)

	return canvas
}

// ringColor picks the hairline color that stays visible on each backdrop.
func ringColor(background string) color.Color {
	switch background {
	case "dark":
		return color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 45}
	case "gradient":
		return color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 70}
	case "light":
		return color.RGBA{R: 0, G: 0, B: 0, A: 30}
	}
	// Over transparent padding the ring has to read on any sharing surface.
	return color.RGBA{R: 0, G: 0, B: 0, A: 45}
}

// newBackdrop returns a two-stop linear gradient filling a cw×ch canvas. The
// light and dark styles are near-solid vertical washes; the gradient style is
// the app's indigo, laid out diagonally.
func newBackdrop(cw, ch int, background string) image.Image {
	switch background {
	case "light":
		return gradient{w: cw, h: ch, from: color.RGBA{R: 0xf2, G: 0xf2, B: 0xf4, A: 0xff}, to: color.RGBA{R: 0xe3, G: 0xe4, B: 0xe8, A: 0xff}}
	case "dark":
		return gradient{w: cw, h: ch, from: color.RGBA{R: 0x2e, G: 0x2e, B: 0x33, A: 0xff}, to: color.RGBA{R: 0x1e, G: 0x1e, B: 0x23, A: 0xff}}
	case "gradient":
		return gradient{w: cw, h: ch, diagonal: true, from: color.RGBA{R: 0xa5, G: 0xb4, B: 0xfc, A: 0xff}, to: color.RGBA{R: 0x63, G: 0x66, B: 0xf1, A: 0xff}}
	}
	return image.NewUniform(color.RGBA{R: 0xe7, G: 0xe7, B: 0xea, A: 0xff})
}

// gradient is an image.Image computing a two-stop linear gradient per pixel,
// so the backdrop needs no intermediate buffer.
type gradient struct {
	w, h     int
	diagonal bool
	from, to color.RGBA
}

func (g gradient) ColorModel() color.Model { return color.RGBAModel }
func (g gradient) Bounds() image.Rectangle { return image.Rect(0, 0, g.w, g.h) }

func (g gradient) At(x, y int) color.Color {
	var t float64
	switch {
	case g.diagonal && g.w+g.h > 0:
		t = float64(x+y) / float64(g.w+g.h)
	case g.h > 0:
		t = float64(y) / float64(g.h)
	}
	return color.RGBA{
		R: uint8(lerp(float64(g.from.R), float64(g.to.R), t)),
		G: uint8(lerp(float64(g.from.G), float64(g.to.G), t)),
		B: uint8(lerp(float64(g.from.B), float64(g.to.B), t)),
		A: 0xff,
	}
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}

// encodeDecoratedPNG re-encodes the decorated capture as PNG. It is lossless,
// so the framed capture keeps screenshot quality; nil means the caller falls
// back to the undecorated capture.
func encodeDecoratedPNG(img image.Image) []byte {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// roundedRectMask builds an anti-aliased alpha mask of a rounded rectangle.
// Coverage comes from the rounded-rect signed distance, so corners are smooth
// without supersampling.
func roundedRectMask(w, h, r int) *image.Alpha {
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	if w <= 0 || h <= 0 {
		return mask
	}
	r = max(1, min(r, min(w, h)/2))
	hw, hh := float64(w)/2, float64(h)/2
	fr := float64(r)
	for y := 0; y < h; y++ {
		qy := math.Abs(float64(y)+0.5-hh) - (hh - fr)
		row := mask.Pix[y*mask.Stride : y*mask.Stride+w]
		for x := 0; x < w; x++ {
			qx := math.Abs(float64(x)+0.5-hw) - (hw - fr)
			dx, dy := max(qx, 0), max(qy, 0)
			dist := math.Hypot(dx, dy) + min(max(qx, qy), 0) - fr
			coverage := max(0, min(1, 0.5-dist))
			row[x] = uint8(coverage*255 + 0.5)
		}
	}
	return mask
}
