package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
)

// ringWidth is the hairline ring's stroke, in pixels. Screenshots are usually
// retina 2x, where 2px read as a 1pt hairline.
const ringWidth = 2

// Screenshot beautify: an explicit FrameStyle describes one render — the
// backdrop, the content's corners, ring and shadow, the margin around it and
// the canvas aspect ratio. The page owns the style (beautify workbench in the
// preview modal), the backend only renders it; captures are never decorated
// implicitly.

// FrameStyle describes one beautify render. Every string knob is normalized
// by normalizeFrameStyle, so an IPC caller can only produce a defined render.
type FrameStyle struct {
	Background string `json:"background"`         // "none", "solid", "gradient"
	BGColor    string `json:"backgroundColor"`    // "#rrggbb" when solid
	BGGradient string `json:"backgroundGradient"` // preset id when gradient
	Radius     string `json:"radius"`             // "none", "small", "medium", "large"
	Ring       bool   `json:"ring"`               // hairline ring around the content
	Shadow     string `json:"shadow"`             // "off", "soft", "deep"
	Padding    string `json:"padding"`            // "compact", "normal", "roomy"
	Ratio      string `json:"ratio"`              // "origin", "1:1", "4:3", "16:9"
}

// defaultFrameStyle is what a fresh beautify workbench shows: the
// competitor-standard opening look — a gradient, medium corners, soft shadow.
func defaultFrameStyle() FrameStyle {
	return FrameStyle{
		Background: "gradient",
		BGColor:    "#e7e7ea",
		BGGradient: "indigo",
		Radius:     "medium",
		Ring:       true,
		Shadow:     "soft",
		Padding:    "normal",
		Ratio:      "origin",
	}
}

// gradientPresets are the hand-picked two-stop gradients offered in the
// workbench, each tagged with whether it reads as light (a dark hairline ring
// shows up on it) or dark (a white one does).
var gradientPresets = map[string]struct {
	from, to color.RGBA
	light    bool
}{
	"indigo":   {from: color.RGBA{R: 0xa5, G: 0xb4, B: 0xfc, A: 0xff}, to: color.RGBA{R: 0x4f, G: 0x46, B: 0xe5, A: 0xff}},
	"dusk":     {from: color.RGBA{R: 0x81, G: 0x8c, B: 0xf8, A: 0xff}, to: color.RGBA{R: 0x31, G: 0x2e, B: 0x81, A: 0xff}},
	"ocean":    {from: color.RGBA{R: 0x67, G: 0xe8, B: 0xf9, A: 0xff}, to: color.RGBA{R: 0x25, G: 0x63, B: 0xeb, A: 0xff}},
	"mint":     {from: color.RGBA{R: 0x6e, G: 0xe7, B: 0xb7, A: 0xff}, to: color.RGBA{R: 0x0d, G: 0x94, B: 0x88, A: 0xff}},
	"rose":     {from: color.RGBA{R: 0xfd, G: 0xa4, B: 0xaf, A: 0xff}, to: color.RGBA{R: 0xe1, G: 0x1d, B: 0x48, A: 0xff}},
	"sunset":   {from: color.RGBA{R: 0xfc, G: 0xd3, B: 0x4d, A: 0xff}, to: color.RGBA{R: 0xf9, G: 0x73, B: 0x16, A: 0xff}},
	"slate":    {from: color.RGBA{R: 0xf3, G: 0xf4, B: 0xf6, A: 0xff}, to: color.RGBA{R: 0x9c, G: 0xa3, B: 0xaf, A: 0xff}, light: true},
	"charcoal": {from: color.RGBA{R: 0x4b, G: 0x55, B: 0x63, A: 0xff}, to: color.RGBA{R: 0x11, G: 0x18, B: 0x27, A: 0xff}},
}

// normalizeFrameStyle maps any input onto a defined render. Unknown strings
// fall back to the documented defaults instead of erroring, so a stale page
// can never make the backend render garbage.
func normalizeFrameStyle(s FrameStyle) FrameStyle {
	switch s.Background {
	case "solid", "gradient":
	default:
		s.Background = "none"
	}
	s.BGColor = normalizeHexColor(s.BGColor)
	switch s.BGGradient {
	case "indigo", "dusk", "ocean", "mint", "rose", "sunset", "slate", "charcoal":
	default:
		s.BGGradient = "indigo"
	}
	switch s.Radius {
	case "none", "small", "medium", "large":
	default:
		s.Radius = "medium"
	}
	switch s.Shadow {
	case "off", "soft", "deep":
	default:
		s.Shadow = "soft"
	}
	switch s.Padding {
	case "compact", "normal", "roomy":
	default:
		s.Padding = "normal"
	}
	switch s.Ratio {
	case "1:1", "4:3", "16:9":
	default:
		s.Ratio = "origin"
	}
	return s
}

// normalizeHexColor accepts #rgb / #rrggbb and returns lowercase #rrggbb;
// anything else falls back to the neutral workbench gray.
func normalizeHexColor(s string) string {
	s = strings.TrimSpace(strings.ToLower(strings.TrimPrefix(s, "#")))
	switch len(s) {
	case 3:
		if isHex(s) {
			return "#" + string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
		}
	case 6:
		if isHex(s) {
			return "#" + s
		}
	}
	return "#e7e7ea"
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// framePadding turns the margin level into pixels for a content box of w×h.
// It scales with the image — retina captures are 2x — and is clamped so tiny
// shots keep a usable margin and huge ones don't grow absurd borders.
func framePadding(w, h int, level string) int {
	var pct int
	switch level {
	case "compact":
		pct = 2
	case "roomy":
		pct = 7
	default:
		pct = 4
	}
	p := max(w, h) * pct / 100
	p = max(16, min(240, p))
	// Keep the margin from eating more than a quarter of the shorter side.
	if cap := min(w, h) / 4; p > cap {
		p = max(2, cap)
	}
	return p
}

// frameRadius scales the corner-radius level with the image, so the same
// setting reads the same on a 2x retina capture and a small crop.
func frameRadius(w, h int, level string) int {
	if level == "none" {
		return 0
	}
	var pct int
	switch level {
	case "small":
		pct = 1
	case "large":
		pct = 3
	default:
		pct = 2
	}
	r := min(w, h) * pct / 100
	r = max(4, min(160, r))
	if cap := min(w, h) / 2; r > cap {
		r = max(1, cap)
	}
	return r
}

// contentBox returns the box that holds a w×h image centered while matching
// the requested canvas ratio. The box only ever grows — a ratio never crops.
func contentBox(w, h int, ratio string) (int, int) {
	switch ratio {
	case "1:1":
		if w != h {
			return max(w, h), max(w, h)
		}
	case "4:3":
		bw := max(w, h*4/3)
		return bw, bw * 3 / 4
	case "16:9":
		bw := max(w, h*16/9)
		return bw, bw * 9 / 16
	}
	return w, h
}

// decorateScreenshot renders src inside a styled canvas per the style. The
// canvas is the content box plus the margin on every side.
func decorateScreenshot(src image.Image, style FrameStyle) *image.RGBA {
	style = normalizeFrameStyle(style)
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	cw, ch := contentBox(w, h, style.Ratio)
	p := framePadding(cw, ch, style.Padding)
	r := frameRadius(w, h, style.Radius)
	cbw, cbh := cw+2*p, ch+2*p
	// The image is centered inside the content box, which is centered in
	// the canvas.
	ox, oy := p+(cw-w)/2, p+(ch-h)/2

	canvas := image.NewRGBA(image.Rect(0, 0, cbw, cbh))

	if style.Background != "none" {
		draw.Draw(canvas, canvas.Bounds(), newBackdrop(cbw, cbh, style), image.Point{}, draw.Src)
	}

	if style.Shadow != "off" {
		alpha, sigma, dy := shadowSpec(style.Shadow, p)
		mask := roundedRectMask(w, h, r)
		silhouette := image.NewRGBA(image.Rect(0, 0, cbw, cbh))
		draw.DrawMask(silhouette, image.Rect(ox, oy+dy, ox+w, oy+dy+h),
			&image.Uniform{C: color.RGBA{A: alpha}}, image.Point{}, mask, image.Point{}, draw.Src)
		draw.Draw(canvas, canvas.Bounds(), imaging.Blur(silhouette, sigma), image.Point{}, draw.Over)
	}

	if style.Ring {
		rw := ringWidth
		ringMask := roundedRectMask(w+2*rw, h+2*rw, r+rw)
		draw.DrawMask(canvas, image.Rect(ox-rw, oy-rw, ox+w+rw, oy+h+rw),
			&image.Uniform{C: ringColor(style)}, image.Point{}, ringMask, image.Point{}, draw.Over)
	}

	// Content: the image, corner-masked when a radius is set, centered.
	if r > 0 {
		masked := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.DrawMask(masked, masked.Bounds(), src, b.Min, roundedRectMask(w, h, r), image.Point{}, draw.Src)
		draw.Draw(canvas, image.Rect(ox, oy, ox+w, oy+h), masked, image.Point{}, draw.Over)
	} else {
		draw.Draw(canvas, image.Rect(ox, oy, ox+w, oy+h), src, b.Min, draw.Over)
	}

	return canvas
}

// shadowSpec maps the shadow level to silhouette alpha, blur sigma and the
// downward offset, scaled by the margin so bigger canvases get softer
// shadows.
func shadowSpec(level string, p int) (alpha uint8, sigma float64, dy int) {
	switch level {
	case "deep":
		return 110, math.Max(4, float64(p)/3.5), max(3, p/6)
	default:
		return 66, math.Max(3, float64(p)/5), max(2, p/10)
	}
}

// ringColor picks a hairline that stays visible on each backdrop: dark on
// light backdrops, white on dark ones.
func ringColor(style FrameStyle) color.Color {
	switch style.Background {
	case "gradient":
		if preset := gradientPresets[style.BGGradient]; preset.light {
			return color.RGBA{R: 0, G: 0, B: 0, A: 30}
		}
		return color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 45}
	case "solid":
		if hexLuminance(style.BGColor) > 0.6 {
			return color.RGBA{R: 0, G: 0, B: 0, A: 30}
		}
		return color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 45}
	}
	// Over transparent padding the ring has to read on any sharing surface.
	return color.RGBA{R: 0, G: 0, B: 0, A: 45}
}

// hexLuminance scores a #rrggbb color from 0 (black) to 1 (white).
func hexLuminance(hex string) float64 {
	c := normalizeHexColor(hex)
	r, _ := strconv.ParseUint(c[1:3], 16, 8)
	g, _ := strconv.ParseUint(c[3:5], 16, 8)
	b, _ := strconv.ParseUint(c[5:7], 16, 8)
	return (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 255
}

// newBackdrop returns the backdrop image for the canvas: a preset gradient or
// a solid color.
func newBackdrop(cw, ch int, style FrameStyle) image.Image {
	if style.Background == "gradient" {
		preset := gradientPresets[style.BGGradient]
		return gradient{w: cw, h: ch, diagonal: true, from: preset.from, to: preset.to}
	}
	return image.NewUniform(color.RGBA{R: uint8(hexAt(style.BGColor, 1)), G: uint8(hexAt(style.BGColor, 3)), B: uint8(hexAt(style.BGColor, 5)), A: 0xff})
}

func hexAt(hex string, i int) int {
	v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
	return int(v)
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

// roundedRectMask builds an anti-aliased alpha mask of a rounded rectangle.
// Coverage comes from the rounded-rect signed distance, so corners are smooth
// without supersampling. r=0 yields the sharp square.
func roundedRectMask(w, h, r int) *image.Alpha {
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	if w <= 0 || h <= 0 {
		return mask
	}
	r = min(r, min(w, h)/2)
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

// encodeDecoratedPNG re-encodes the decorated image as PNG. It is lossless,
// so the framed capture keeps screenshot quality; nil means the caller falls
// back to the undecorated image.
func encodeDecoratedPNG(img image.Image) []byte {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}
