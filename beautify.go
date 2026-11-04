package main

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"sort"
	"strconv"

	"github.com/disintegration/imaging"
)

// ScreenshotStyle decorates fresh captures: the shot gets rounded corners, a
// soft shadow and a background that extends beyond it, so a bare screenshot
// reads as a finished picture. Mode "on" applies it to screenshots during
// compression; other images are never decorated.
type ScreenshotStyle struct {
	// Mode is "off" or "on".
	Mode string `json:"mode"`
	// Background picks a preset id from backgroundPresets, or "auto" to
	// derive a matching gradient from the screenshot's own edge colors.
	Background string `json:"background"`
	// Padding is the breathing room around the shot as a percentage of its
	// longer edge: 0, 6, 10 or 16. Zero keeps the rounded corners but adds
	// no background or shadow, so the output is the bare rounded shot.
	Padding int `json:"padding"`
}

// backgroundPresets holds the decorative backgrounds. Gradient presets are
// two-stop diagonal gradients; the values come from ray.so's open-source
// theme list, a palette this space has converged on. Solids repeat one color.
var backgroundPresets = map[string][2]string{
	"breeze":   {"#CF2F98", "#6A3DEC"},
	"candy":    {"#A58EFB", "#E9BFF8"},
	"crimson":  {"#FF6363", "#733434"},
	"falcon":   {"#BDE3EC", "#363654"},
	"meadow":   {"#59D499", "#A0872D"},
	"midnight": {"#4CC8C8", "#202033"},
	"raindrop": {"#8EC7FB", "#1C55AA"},
	"sunset":   {"#FFCF73", "#FF7A2F"},
	"ink":      {"#4F46E5", "#4F46E5"},
	"graphite": {"#1F2937", "#1F2937"},
	"paper":    {"#F4F1EA", "#F4F1EA"},
}

func normalizeScreenshotStyle(s ScreenshotStyle) ScreenshotStyle {
	if s.Mode != "on" {
		s.Mode = "off"
	}
	if s.Background != "auto" {
		if _, ok := backgroundPresets[s.Background]; !ok {
			s.Background = "auto"
		}
	}
	switch s.Padding {
	case 0, 6, 10, 16:
	default:
		s.Padding = 10
	}
	return s
}

// decorateScreenshot draws the decorated picture: a background canvas, a soft
// shadow and the shot with rounded corners centered on it.
func decorateScreenshot(src image.Image, style ScreenshotStyle) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}

	pad := paddingPx(style.Padding, max(w, h))
	radius := cornerRadius(min(w, h))

	shot := roundCorners(src, radius)
	from, to := backgroundColors(style, src)

	canvas := image.NewNRGBA(image.Rect(0, 0, w+2*pad, h+2*pad))
	paintGradient(canvas, from, to)
	if pad > 0 {
		drawShadow(canvas, w, h, pad, radius)
	}
	draw.Draw(canvas, image.Rect(pad, pad, pad+w, pad+h), shot, shot.Bounds().Min, draw.Over)
	return canvas
}

func paddingPx(percent, longEdge int) int {
	if percent <= 0 {
		return 0
	}
	return clampInt(longEdge*percent/100, 20, 420)
}

// BackgroundSwatch is one preset rendered against the sample screenshot, for
// the settings page's live previews.
type BackgroundSwatch struct {
	ID    string `json:"id"`
	Image string `json:"image"`
}

// beautifySwatchSide is the rendered preview's larger side; small enough for
// a settings grid, large enough for the shadow to stay legible.
const beautifySwatchSide = 96

// BeautifySwatches renders every background preset plus the auto gradient
// against a sample screenshot — the newest queued screenshot, or a synthetic
// window when none is queued — so picking a background shows the real style
// instead of a bare color chip.
func (a *AnyPic) BeautifySwatches() ([]BackgroundSwatch, error) {
	sample, err := a.screenshotSample()
	if err != nil {
		return nil, err
	}
	style := ScreenshotStyle{Mode: "on", Padding: 6}
	swatchMax := beautifySwatchSide
	ids := make([]string, 0, len(backgroundPresets)+1)
	ids = append(ids, "auto")
	for id := range backgroundPresets {
		ids = append(ids, id)
	}
	sort.Strings(ids[1:])

	swatches := make([]BackgroundSwatch, 0, len(ids))
	for _, id := range ids {
		style.Background = id
		decorated := decorateScreenshot(sample, style)
		img := imaging.Fit(decorated, swatchMax, swatchMax, imaging.Lanczos)
		s, err := dataURL(img, false, 85)
		if err != nil {
			return nil, err
		}
		swatches = append(swatches, BackgroundSwatch{ID: id, Image: s})
	}
	return swatches, nil
}

// screenshotSample picks the image the swatches render against: the newest
// screenshot in the queue so the preview matches what the user will ship,
// scaled down for speed, or a synthetic window mock when the queue is empty.
func (a *AnyPic) screenshotSample() (image.Image, error) {
	a.mu.Lock()
	var path string
	for i := len(a.items) - 1; i >= 0; i-- {
		if a.items[i].Screenshot {
			path = a.items[i].Path
			break
		}
	}
	a.mu.Unlock()

	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		img, err := imaging.Decode(f)
		if err != nil {
			return nil, err
		}
		// The style math is resolution aware; a mid-size proxy is enough
		// for a 96px preview and keeps the render instant.
		return imaging.Fit(img, 640, 640, imaging.Linear), nil
	}
	return syntheticWindowShot(), nil
}

// syntheticWindowShot draws a small mock app window — a title bar, a sidebar
// and a content area — so the swatches still read as real screenshot styles
// before anything is queued.
func syntheticWindowShot() image.Image {
	const w, h, bar, side = 320, 200, 24, 76
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.NRGBA
			switch {
			case y < bar:
				c = color.NRGBA{R: 236, G: 237, B: 240, A: 255}
			case x < side:
				c = color.NRGBA{R: 246, G: 247, B: 249, A: 255}
			default:
				c = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	// Traffic lights and a couple of content strokes give the sampler
	// something with actual hue to pick up.
	for i, col := range [3]color.NRGBA{
		{R: 255, G: 95, B: 87, A: 255},
		{R: 255, G: 189, B: 46, A: 255},
		{R: 39, G: 201, B: 63, A: 255},
	} {
		fillRoundedRect(img, image.Rect(10+i*14, 9, 20+i*14, 19), 5, col)
	}
	fillRoundedRect(img, image.Rect(88, 38, 300, 52), 4, color.NRGBA{R: 229, G: 231, B: 235, A: 255})
	fillRoundedRect(img, image.Rect(88, 62, 260, 76), 4, color.NRGBA{R: 229, G: 231, B: 235, A: 255})
	fillRoundedRect(img, image.Rect(88, 86, 285, 100), 4, color.NRGBA{R: 79, G: 70, B: 229, A: 255})
	return img
}

// cornerRadius scales with the shot — 4% of the short edge — so a 4K
// capture keeps the same visual weight as a small one, inside a band where
// it still reads as a rounded window rather than a circle or a sharp card.
func cornerRadius(shortEdge int) int {
	return clampInt(shortEdge*4/100, 10, 120)
}

// backgroundColors resolves the two gradient stops. Unknown ids fall through
// to the auto gradient, matching normalizeScreenshotStyle.
func backgroundColors(style ScreenshotStyle, src image.Image) (color.NRGBA, color.NRGBA) {
	if pair, ok := backgroundPresets[style.Background]; ok {
		if from, err := parseHex(pair[0]); err == nil {
			if to, err := parseHex(pair[1]); err == nil {
				return from, to
			}
		}
	}
	return autoBackgroundColors(src)
}

// autoBackgroundColors samples the screenshot's own edges — where a window
// shot meets the desktop — and extends that tone into a soft light-to-dark
// gradient, the same idea Xnapper sells as automatic prettifying.
func autoBackgroundColors(src image.Image) (color.NRGBA, color.NRGBA) {
	b := src.Bounds()
	var rs, gs, bs, n int64
	add := func(x, y int) {
		r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
		if a < 1<<14 {
			return
		}
		rs += int64(r >> 8)
		gs += int64(g >> 8)
		bs += int64(bl >> 8)
		n++
	}
	step := max(1, max(b.Dx(), b.Dy())/96)
	for x := 0; x < b.Dx(); x += step {
		add(x, 0)
		add(x, b.Dy()-1)
	}
	for y := 0; y < b.Dy(); y += step {
		add(0, y)
		add(b.Dx()-1, y)
	}
	if n == 0 {
		// A fully transparent shot gives nothing to sample from; fall back
		// to a preset rather than an all-black gradient.
		pair := backgroundPresets["breeze"]
		return mustHex(pair[0]), mustHex(pair[1])
	}
	base := color.NRGBA{R: uint8(rs / n), G: uint8(gs / n), B: uint8(bs / n), A: 255}
	// The gradient frames the shot, so its ends need to sit clearly away
	// from the shot's own tones: a light screenshot against a light top
	// stop reads as one blurred blob. Clamping the base lightness before
	// mixing keeps both stops at a usable distance for any source.
	base = clampLightness(base, 0.30, 0.72)
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	black := color.NRGBA{A: 255}
	return mixColor(base, white, 0.45), mixColor(base, black, 0.30)
}

// clampLightness pins a color's perceived lightness to [lo, hi] by scaling
// all channels by one factor — hue ratios survive, chroma shrinks or grows
// with the tone, which is what a background tint wants.
func clampLightness(c color.NRGBA, lo, hi float64) color.NRGBA {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	mx, mn := max(r, g, b), min(r, g, b)
	l := (mx + mn) / 2
	switch {
	case l < lo:
		// Lift off black; stop early if a channel would clip to white.
		t := min(lo/max(l, 1e-6), 1/max(mx, 1e-6))
		r, g, b = r*t, g*t, b*t
	case l > hi:
		// Pull back from white; lightness scales with the same factor.
		r, g, b = r*(hi/l), g*(hi/l), b*(hi/l)
	}
	return color.NRGBA{
		R: uint8(math.Round(r * 255)),
		G: uint8(math.Round(g * 255)),
		B: uint8(math.Round(b * 255)),
		A: 255,
	}
}

// paintGradient fills the canvas with a diagonal two-stop gradient.
func paintGradient(canvas *image.NRGBA, from, to color.NRGBA) {
	b := canvas.Bounds()
	denom := max(1, b.Dx()+b.Dy()-2)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			t := float64(x+y) / float64(denom)
			i := canvas.PixOffset(b.Min.X+x, b.Min.Y+y)
			p := canvas.Pix[i : i+4 : i+4]
			p[0] = uint8(float64(from.R) + (float64(to.R)-float64(from.R))*t)
			p[1] = uint8(float64(from.G) + (float64(to.G)-float64(from.G))*t)
			p[2] = uint8(float64(from.B) + (float64(to.B)-float64(from.B))*t)
			p[3] = 255
		}
	}
}

// drawShadow blurs a rounded-rect silhouette of the shot and pastes it
// slightly below the image position. The layer is kept to the silhouette
// plus a blur margin so a 4K screenshot does not blur a 4K canvas.
//
// The shot itself is centered; only the shadow is nudged down, so the
// visible breathing room stays even while the shadow still reads as cast
// from above. The blur margin absorbs the offset: pasting shifted up-left
// by margin keeps the blurred layer inside the canvas.
func drawShadow(canvas *image.NRGBA, w, h, pad, radius int) {
	sigma := clampFloat(float64(radius)*1.1, 10, 28)
	offset := clampInt(radius*3/5, 6, 18)
	margin := int(math.Ceil(sigma * 3)) + offset

	layer := image.NewNRGBA(image.Rect(0, 0, w+2*margin, h+2*margin))
	fillRoundedRect(layer, image.Rect(margin, margin, margin+w, margin+h), radius, color.NRGBA{A: 70})
	blurred := imaging.Blur(layer, sigma)
	draw.Draw(canvas, canvas.Bounds(), blurred, image.Pt(pad-margin, pad+offset-margin), draw.Over)
}

// roundCorners copies src and multiplies the alpha of the four corner
// regions by the rounded-rect coverage, leaving everything else untouched.
func roundCorners(src image.Image, radius int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	if radius <= 0 {
		return dst
	}
	w, h := b.Dx(), b.Dy()
	scaleAlpha := func(x, y int) {
		i := dst.PixOffset(x, y)
		c := roundedCoverage(float64(x)+0.5, float64(y)+0.5, w, h, float64(radius))
		dst.Pix[i+3] = uint8(float64(dst.Pix[i+3]) * c)
	}
	for _, corner := range [4]image.Rectangle{
		image.Rect(0, 0, radius, radius),
		image.Rect(w-radius, 0, w, radius),
		image.Rect(0, h-radius, radius, h),
		image.Rect(w-radius, h-radius, w, h),
	} {
		for y := corner.Min.Y; y < corner.Max.Y; y++ {
			for x := corner.Min.X; x < corner.Max.X; x++ {
				scaleAlpha(x, y)
			}
		}
	}
	return dst
}

// fillRoundedRect paints an anti-aliased rounded rectangle.
func fillRoundedRect(dst *image.NRGBA, rect image.Rectangle, radius int, fill color.NRGBA) {
	w, h := rect.Dx(), rect.Dy()
	r := math.Min(float64(radius), math.Min(float64(w), float64(h))/2)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			c := roundedCoverage(float64(x-rect.Min.X)+0.5, float64(y-rect.Min.Y)+0.5, w, h, r)
			if c <= 0 {
				continue
			}
			i := dst.PixOffset(x, y)
			p := dst.Pix[i : i+4 : i+4]
			p[0] = fill.R
			p[1] = fill.G
			p[2] = fill.B
			p[3] = uint8(float64(fill.A) * c)
		}
	}
}

// roundedCoverage reports how much of the pixel centered at (x, y) falls
// inside a w×h rounded rectangle of the given radius, using the signed
// distance to the shape for a smooth one-pixel edge.
func roundedCoverage(x, y float64, w, h int, r float64) float64 {
	hx, hy := float64(w)/2, float64(h)/2
	px, py := math.Abs(x-hx), math.Abs(y-hy)
	qx, qy := px-hx+r, py-hy+r
	dx, dy := math.Max(qx, 0), math.Max(qy, 0)
	d := math.Hypot(dx, dy) + math.Min(math.Max(qx, qy), 0) - r
	return math.Max(0, math.Min(1, 0.5-d))
}

func mixColor(a, b color.NRGBA, t float64) color.NRGBA {
	mix := func(x, y uint8) uint8 {
		return uint8(float64(x) + (float64(y)-float64(x))*t)
	}
	return color.NRGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: 255}
}

func parseHex(s string) (color.NRGBA, error) {
	if len(s) != 7 || s[0] != '#' {
		return color.NRGBA{}, strconv.ErrSyntax
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.NRGBA{}, err
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, nil
}

func mustHex(s string) color.NRGBA {
	c, err := parseHex(s)
	if err != nil {
		panic("invalid preset color " + s)
	}
	return c
}

func clampInt(v, lo, hi int) int {
	return min(hi, max(lo, v))
}

func clampFloat(v, lo, hi float64) float64 {
	return math.Min(hi, math.Max(lo, v))
}
