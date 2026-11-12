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

// Screenshot beautify: an explicit FrameStyle describes one render — the
// backdrop, the content's corners, ring and shadow, the margin around it and
// the canvas aspect ratio. The page owns the style (beautify workbench in the
// preview modal), the backend only renders it; captures are never decorated
// implicitly.
//
// The renderer is shaped like the thing it imitates rather than a pile of
// per-pixel effects, because the difference between a cheap frame and a
// native-looking one is entirely in these details:
//
//   - the backdrop is interpolated in OKLab and dithered, so a wide gradient
//     has neither the muddy midtones of sRGB interpolation nor 8-bit banding;
//   - pattern backdrops are the day-card fills (patterns.go), rasterised at
//     the canvas's own resolution and repeated at one scalar, so their grain
//     survives 4K and never stretches;
//   - the shadow is three stacked blurs (contact / key / ambient) cast from
//     the content's *alpha silhouette*, never from its bounding box, so a
//     transparent PNG casts its own shape and nothing shows through its holes;
//     it is composited behind the content, so the content's antialiased rim is
//     never darkened by it;
//   - corners come from a signed-distance mask, the ring and the content share
//     one corner centre (concentric radii), and every visible length is
//     derived from the image's own size so a 4K capture is not framed like a
//     thumbnail;
//   - the blur runs at a reduced resolution and the result is scaled back up,
//     which is invisible on a soft shadow and keeps a live workbench usable on
//     a 4K capture.

// ringWidthBase is the hairline ring's stroke at the reference size, in
// pixels. Screenshots are usually retina 2x, where 2px read as a 1pt hairline.
const ringWidthBase = 2

// FrameStyle describes one beautify render. Every string knob is normalized
// by normalizeFrameStyle, so an IPC caller can only produce a defined render.
type FrameStyle struct {
	Background string `json:"background"`         // "none", "solid", "gradient", "pattern"
	BGColor    string `json:"backgroundColor"`    // "#rrggbb" when solid
	BGGradient string `json:"backgroundGradient"` // preset id when gradient
	BGPattern  string `json:"backgroundPattern"`  // day-card pattern id when pattern
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
		BGPattern:  "teal",
		Radius:     "medium",
		Ring:       true,
		Shadow:     "soft",
		Padding:    "normal",
		Ratio:      "origin",
	}
}

// gradientPresets are the two-stop gradients offered in the workbench. They
// are hand-mixed rather than pulled from a UI palette: each pair keeps its two
// stops within a couple of OKLab lightness steps and rotates hue only a
// little, which is what reads as designed rather than as a saturated primary
// beside its own shade. The ids are protocol — the workbench's swatches mirror
// these stops exactly (src/preview.ts).
var gradientPresets = map[string]struct {
	from, to color.RGBA
}{
	"indigo":   {from: color.RGBA{R: 0xc3, G: 0xcb, B: 0xfd, A: 0xff}, to: color.RGBA{R: 0x4f, G: 0x46, B: 0xe5, A: 0xff}},
	"dusk":     {from: color.RGBA{R: 0xa5, G: 0xb2, B: 0xf0, A: 0xff}, to: color.RGBA{R: 0x3b, G: 0x35, B: 0x8c, A: 0xff}},
	"ocean":    {from: color.RGBA{R: 0xa9, G: 0xe4, B: 0xf2, A: 0xff}, to: color.RGBA{R: 0x2c, G: 0x5f, B: 0xd1, A: 0xff}},
	"mint":     {from: color.RGBA{R: 0xb2, G: 0xe8, B: 0xd1, A: 0xff}, to: color.RGBA{R: 0x14, G: 0x86, B: 0x7f, A: 0xff}},
	"rose":     {from: color.RGBA{R: 0xf6, G: 0xc2, B: 0xc6, A: 0xff}, to: color.RGBA{R: 0xc7, G: 0x2a, B: 0x5b, A: 0xff}},
	"sunset":   {from: color.RGBA{R: 0xf7, G: 0xdd, B: 0xb4, A: 0xff}, to: color.RGBA{R: 0xd9, G: 0x6a, B: 0x3c, A: 0xff}},
	"slate":    {from: color.RGBA{R: 0xf6, G: 0xf7, B: 0xf9, A: 0xff}, to: color.RGBA{R: 0xa8, G: 0xaf, B: 0xba, A: 0xff}},
	"charcoal": {from: color.RGBA{R: 0x45, G: 0x4b, B: 0x57, A: 0xff}, to: color.RGBA{R: 0x14, G: 0x18, B: 0x20, A: 0xff}},
}

// normalizeFrameStyle maps any input onto a defined render. Unknown strings
// fall back to the documented defaults instead of erroring, so a stale page
// can never make the backend render garbage.
func normalizeFrameStyle(s FrameStyle) FrameStyle {
	switch s.Background {
	case "solid", "gradient", "pattern":
	default:
		s.Background = "none"
	}
	s.BGColor = normalizeHexColor(s.BGColor)
	switch s.BGGradient {
	case "indigo", "dusk", "ocean", "mint", "rose", "sunset", "slate", "charcoal":
	default:
		s.BGGradient = "indigo"
	}
	// The pattern set is closed on both sides of the IPC bridge; an unknown
	// id (a page from a newer build, say) renders the reference's own
	// fallback instead of an undefined backdrop.
	if _, ok := patternPresets[s.BGPattern]; !ok {
		s.BGPattern = "teal"
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

/* ------------------------------------------------------------------ */
/* geometry                                                            */
/* ------------------------------------------------------------------ */

// frameGeometry is every length one render needs, derived from the source size
// and the style. Splitting geometry from drawing is what lets the preview
// render the same frame at a fraction of the size (see scaled): the lengths
// shrink together, so the preview is the exported picture, smaller — not a
// differently proportioned one.
type frameGeometry struct {
	w, h             int     // source image size
	boxW, boxH       int     // ratio-matched content box the image sits centred in
	pad              int     // margin from the content box to the canvas edge
	radius           int     // content corner radius
	ringW            int     // hairline stroke
	patternScale     float64 // device pixels per pattern unit
	ox, oy           int     // the image's offset inside the content box
	canvasW, canvasH int
}

// refSize is the image's reference size: the geometric mean of its sides. It
// is deliberately not the longer side (which frames an ultra-wide strip with
// thumbnail-sized corners) and not the shorter one (which makes a 4K capture
// look like a phone screenshot).
func refSize(w, h int) float64 {
	if w <= 0 || h <= 0 {
		return 0
	}
	return math.Sqrt(float64(w) * float64(h))
}

// computeFrame derives the frame for a w×h source at full size.
func computeFrame(w, h int, style FrameStyle) frameGeometry {
	style = normalizeFrameStyle(style)
	w, h = max(1, w), max(1, h)
	boxW, boxH := contentBox(w, h, style.Ratio)
	g := frameGeometry{
		w: w, h: h,
		boxW:    boxW,
		boxH:    boxH,
		pad:     framePadding(boxW, boxH, style.Padding),
		radius:  frameRadius(w, h, style.Radius),
		ringW:   ringWidth(w, h),
		ox:      (boxW - w) / 2,
		oy:      (boxH - h) / 2,
		canvasW: boxW + 2*framePadding(boxW, boxH, style.Padding),
		canvasH: boxH + 2*framePadding(boxW, boxH, style.Padding),
	}
	g.patternScale = patternScaleFor(g.canvasW, g.canvasH)
	if !style.Ring {
		g.ringW = 0
	}
	return g
}

// scaled returns the same frame at a fraction of its size. Rounding is the
// only difference from computing the geometry at the smaller size, which is
// what keeps the preview honest.
func (g frameGeometry) scaled(scale float64) frameGeometry {
	if scale >= 1 {
		return g
	}
	at := func(v int) int { return int(math.Round(float64(v) * scale)) }
	out := g
	out.w, out.h = at(g.w), at(g.h)
	out.pad = at(g.pad)
	out.radius = at(g.radius)
	out.ringW = at(g.ringW)
	out.patternScale = g.patternScale * scale
	out.ox, out.oy = at(g.ox), at(g.oy)
	out.boxW, out.boxH = at(g.boxW), at(g.boxH)
	out.canvasW, out.canvasH = at(g.canvasW), at(g.canvasH)
	// Never let rounding shrink the canvas below the content it has to hold.
	out.boxW = max(out.boxW, out.w)
	out.boxH = max(out.boxH, out.h)
	out.canvasW = max(out.canvasW, out.boxW+2*out.pad)
	out.canvasH = max(out.canvasH, out.boxH+2*out.pad)
	out.ox = clampInt(out.ox, 0, max(0, out.boxW-out.w))
	out.oy = clampInt(out.oy, 0, max(0, out.boxH-out.h))
	return out
}

// contentOrigin is the image's top-left corner on the canvas: the margin, plus
// the image's centring inside the content box.
func (g frameGeometry) contentOrigin() (int, int) {
	return g.pad + g.ox, g.pad + g.oy
}

// framePadding turns the margin level into pixels for a content box of w×h.
// The margin follows the longer side — the way a page margin does — so a wide
// capture keeps a balanced frame; the clamp keeps an ultra-wide strip from
// disappearing into a letterbox and a 4K capture from growing an absurd one.
func framePadding(w, h int, level string) int {
	var pct int
	switch level {
	case "compact":
		pct = 5
	case "roomy":
		pct = 15
	default:
		pct = 9
	}
	p := max(w, h) * pct / 100
	p = max(24, min(480, p))
	// Never eat more than a little under two fifths of the shorter side: on a
	// wide strip the longer side's share would otherwise dominate the canvas.
	if cap := min(w, h) * 38 / 100; p > cap {
		p = max(12, cap)
	}
	return p
}

// frameRadius scales the corner-radius level with the image, so the same
// setting reads the same on a 2x retina capture and a small crop.
func frameRadius(w, h int, level string) int {
	if level == "none" {
		return 0
	}
	var pct float64
	switch level {
	case "small":
		pct = 1.1
	case "large":
		pct = 3.8
	default:
		pct = 2.2
	}
	r := int(math.Round(refSize(w, h) * pct / 100))
	r = max(5, min(64, r))
	if cap := min(w, h) / 2; r > cap {
		r = max(1, cap)
	}
	return r
}

// ringWidth keeps the hairline reading as a hairline: a single pixel on a
// small crop, the classic two at the sizes a retina capture is normally shot
// at, three on a 4K canvas — where a fixed 2px would disappear into the
// image's own edge.
func ringWidth(w, h int) int {
	switch r := refSize(w, h); {
	case r < 700:
		return 1
	case r > 2200:
		return ringWidthBase + 1
	default:
		return ringWidthBase
	}
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

/* ------------------------------------------------------------------ */
/* color                                                               */
/* ------------------------------------------------------------------ */

// srgbToLinear / linearToSrgb: sRGB is a storage encoding, not a colour space.
// Mixing two stops in it dips through a darker, greyer middle.
func srgbToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func linearToSrgb(v float64) float64 {
	if v <= 0.0031308 {
		return v * 12.92
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

// linearToOKLab and oklabToLinear are Björn Ottosson's OKLab, the closest
// simple space to how the eye spaces lightness and chroma, so a two-stop ramp
// keeps its hue and its chroma through the middle instead of sagging.
func linearToOKLab(r, g, b float64) (float64, float64, float64) {
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return 0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		0.0259040371*l + 0.7827717662*m - 0.8086757660*s
}

func oklabToLinear(L, a, b float64) (float64, float64, float64) {
	l := L + 0.3963377774*a + 0.2158037573*b
	m := L - 0.1055613458*a - 0.0638541728*b
	s := L - 0.0894841775*a - 1.2914855480*b
	l, m, s = l*l*l, m*m*m, s*s*s
	return 4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s
}

// oklabLerpColor mixes two sRGB stops through OKLab, returning an sRGB colour.
func oklabLerpColor(from, to color.RGBA, t float64) color.RGBA {
	to255 := func(v float64) uint8 {
		return uint8(math.Round(clamp01(linearToSrgb(v)) * 255))
	}
	r1, g1, b1 := linearToOKLab(srgbToLinear(float64(from.R)/255), srgbToLinear(float64(from.G)/255), srgbToLinear(float64(from.B)/255))
	r2, g2, b2 := linearToOKLab(srgbToLinear(float64(to.R)/255), srgbToLinear(float64(to.G)/255), srgbToLinear(float64(to.B)/255))
	lr, lg, lb := oklabToLinear(lerp(r1, r2, t), lerp(g1, g2, t), lerp(b1, b2, t))
	return color.RGBA{R: to255(lr), G: to255(lg), B: to255(lb), A: 0xff}
}

// ditherBias returns a deterministic sub-LSB bias in (-0.5, 0.5) for pixel
// (x,y), in 0..255 units. It is what turns an 8-bit ramp's visible steps into
// imperceptible noise: a 4K gradient left alone bands roughly every 23px.
func ditherBias(x, y int) float64 {
	h := uint32(x)*0x9e3779b1 ^ uint32(y)*0x85ebca6b
	h ^= h >> 15
	h *= 0xc2b2ae35
	h ^= h >> 13
	return float64(h&0xffff)/65536 - 0.5
}

// hexLuminance scores a #rrggbb color from 0 (black) to 1 (white) in linear
// light, which is what decides whether a dark or a light hairline reads on it.
func hexLuminance(hex string) float64 {
	c := normalizeHexColor(hex)
	return luminanceOf(color.NRGBA{
		R: uint8(hexAt(c, 1)),
		G: uint8(hexAt(c, 3)),
		B: uint8(hexAt(c, 5)),
		A: 0xff,
	})
}

// luminanceOf is hexLuminance for a color in hand (a pattern's ground).
func luminanceOf(c color.NRGBA) float64 {
	return 0.2126*srgbToLinear(float64(c.R)/255) +
		0.7152*srgbToLinear(float64(c.G)/255) +
		0.0722*srgbToLinear(float64(c.B)/255)
}

func hexAt(hex string, i int) int {
	v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
	return int(v)
}

// newBackdrop returns the backdrop image for the canvas: a preset gradient, a
// day-card pattern, or a solid color. patternScale is the frame's tile scale —
// carried in the geometry, not re-derived here, so a scaled preview repeats
// the pattern exactly as the full-size export does.
func newBackdrop(cw, ch int, style FrameStyle, patternScale float64) image.Image {
	switch style.Background {
	case "gradient":
		preset := gradientPresets[style.BGGradient]
		return newGradient(cw, ch, preset.from, preset.to)
	case "pattern":
		return newPatternBackdrop(cw, ch, style.BGPattern, patternScale)
	}
	return image.NewUniform(color.RGBA{
		R: uint8(hexAt(style.BGColor, 1)),
		G: uint8(hexAt(style.BGColor, 3)),
		B: uint8(hexAt(style.BGColor, 5)),
		A: 0xff,
	})
}

// backdropLuminance scores a backdrop from 0 to 1 so the ring can pick a
// hairline that stays visible on it. A gradient is scored at its own middle,
// which is where most of the ring sits; a pattern at its ground colour, which
// is what most of the ring runs over.
func backdropLuminance(style FrameStyle) float64 {
	switch style.Background {
	case "solid":
		return hexLuminance(style.BGColor)
	case "pattern":
		return luminanceOf(patternPresets[style.BGPattern].ground)
	}
	preset := gradientPresets[style.BGGradient]
	return hexLuminance(hexOf(oklabLerpColor(preset.from, preset.to, 0.5)))
}

func hexOf(c color.RGBA) string {
	const digits = "0123456789abcdef"
	out := []byte{'#'}
	for _, v := range []uint8{c.R, c.G, c.B} {
		out = append(out, digits[v>>4], digits[v&0xf])
	}
	return string(out)
}

// gradient is an image.Image computing a two-stop linear gradient. The ramp is
// baked into a lookup table with one entry per diagonal — mixing in OKLab per
// pixel would be millions of cube roots for nothing — and every pixel adds a
// sub-LSB dither on top, so a wide canvas shows no banding.
type gradient struct {
	w, h     int
	from, to color.RGBA
	diagonal bool
	lut      []float64 // 3 entries per step, 0..255
	steps    int
}

func newGradient(w, h int, from, to color.RGBA) *gradient {
	g := &gradient{w: w, h: h, from: from, to: to, diagonal: true}
	g.steps = max(2, w+h+1)
	g.lut = make([]float64, g.steps*3)
	for i := 0; i < g.steps; i++ {
		c := oklabLerpColor(from, to, float64(i)/float64(g.steps-1))
		g.lut[i*3] = float64(c.R)
		g.lut[i*3+1] = float64(c.G)
		g.lut[i*3+2] = float64(c.B)
	}
	return g
}

func (g *gradient) ColorModel() color.Model { return color.RGBAModel }
func (g *gradient) Bounds() image.Rectangle { return image.Rect(0, 0, g.w, g.h) }

func (g *gradient) At(x, y int) color.Color {
	var i int
	switch {
	case g.diagonal && g.w+g.h > 0:
		// Iso-lines at 45°, t=0 at the top-left corner and t=1 at the
		// bottom-right — the same ramp CSS paints for linear-gradient(135deg).
		i = x + y
	case g.h > 0:
		i = y * (g.steps - 1) / g.h
	}
	i = clampInt(i, 0, g.steps-1) * 3
	bias := ditherBias(x, y)
	ch := func(v float64) uint8 {
		return uint8(math.Round(clamp01((v+bias)/255) * 255))
	}
	return color.RGBA{R: ch(g.lut[i]), G: ch(g.lut[i+1]), B: ch(g.lut[i+2]), A: 0xff}
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}

func clamp01(v float64) float64 {
	return min(1, max(0, v))
}

func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

/* ------------------------------------------------------------------ */
/* masks and blur                                                      */
/* ------------------------------------------------------------------ */

// roundedRectCoverage is the antialiased coverage of a rounded rectangle at
// the centre of pixel (px,py), from the shape's signed distance. Corners are
// smooth without supersampling; r=0 yields the sharp square.
func roundedRectCoverage(px, py float64, w, h, r int) float64 {
	fw, fh := float64(w), float64(h)
	fr := float64(min(r, min(w, h)/2))
	qx := math.Abs(px-fw/2) - (fw/2 - fr)
	qy := math.Abs(py-fh/2) - (fh/2 - fr)
	dx, dy := max(qx, 0), max(qy, 0)
	// The hypotenuse is only needed where the corner actually bends; along the
	// straight runs one of the two is zero.
	var dist float64
	if dx == 0 || dy == 0 {
		dist = dx + dy + min(max(qx, qy), 0) - fr
	} else {
		dist = math.Hypot(dx, dy) + min(max(qx, qy), 0) - fr
	}
	return clamp01(0.5 - dist)
}

// roundedRectMask builds an anti-aliased alpha mask of a rounded rectangle.
func roundedRectMask(w, h, r int) *image.Alpha {
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	if w <= 0 || h <= 0 {
		return mask
	}
	for y := 0; y < h; y++ {
		row := mask.Pix[y*mask.Stride : y*mask.Stride+w]
		py := float64(y) + 0.5
		for x := 0; x < w; x++ {
			row[x] = uint8(roundedRectCoverage(float64(x)+0.5, py, w, h, r)*255 + 0.5)
		}
	}
	return mask
}

// roundedRingMask builds the coverage of a stroke of width rw hugging a w×h
// rounded rectangle with corner radius r: the band between that rectangle and
// a concentric one rw further out. A filled rounded rectangle instead would
// paint a card behind the content — plainly wrong the moment the content has
// transparent areas of its own, as a transparent PNG does.
func roundedRingMask(w, h, r, rw int) *image.Alpha {
	ow, oh := w+2*rw, h+2*rw
	mask := image.NewAlpha(image.Rect(0, 0, ow, oh))
	if ow <= 0 || oh <= 0 || rw <= 0 {
		return mask
	}
	for y := 0; y < oh; y++ {
		row := mask.Pix[y*mask.Stride : y*mask.Stride+ow]
		py := float64(y) + 0.5
		for x := 0; x < ow; x++ {
			px := float64(x) + 0.5
			outer := roundedRectCoverage(px, py, ow, oh, r+rw)
			inner := roundedRectCoverage(px-float64(rw), py-float64(rw), w, h, r)
			row[x] = uint8(clamp01(outer*(1-inner))*255 + 0.5)
		}
	}
	return mask
}

// shadowLayer is one blurred silhouette in the stack. A single Gaussian reads
// as a dirty smudge; a native shadow is a tight dark contact edge, the shadow
// the card actually casts, and a wide faint halo.
type shadowLayer struct {
	sigma float64
	alpha float64
	dy    float64
}

// shadowStack returns the layers for a shadow level, in pixels at full size,
// derived from the margin p. Every layer is kept inside the margin — its
// visible extent (2σ past its offset) is under p — so the canvas edge never
// cuts a shadow off.
func shadowStack(level string, p float64) []shadowLayer {
	if level == "off" || p <= 0 {
		return nil
	}
	depth := 1.0
	if level == "deep" {
		depth = 1.55
	}
	spread := math.Sqrt(depth)
	return []shadowLayer{
		{sigma: math.Max(0.8, p*0.055) * spread, alpha: 0.15 * depth, dy: math.Max(0.5, p*0.022) * spread},
		{sigma: p * 0.20 * spread, alpha: 0.16 * depth, dy: p * 0.075 * spread},
		{sigma: p * 0.34 * spread, alpha: 0.09 * depth, dy: p * 0.10 * spread},
	}
}

// shadowWorkTarget is the resolution the silhouette is blurred at. A shadow is
// low frequency by construction, so blurring it at ~700px and scaling the
// result back up is invisible — and it is the difference between a frame that
// renders in a few hundred milliseconds and one that takes seconds on 4K.
const shadowWorkTarget = 700

// downscaleAlpha box-averages an alpha mask by an integer factor.
func downscaleAlpha(src *image.Alpha, k int) *image.Alpha {
	if k <= 1 {
		out := image.NewAlpha(src.Bounds())
		copy(out.Pix, src.Pix)
		return out
	}
	b := src.Bounds()
	w := max(1, b.Dx()/k)
	h := max(1, b.Dy()/k)
	dst := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := dst.Pix[y*dst.Stride : y*dst.Stride+w]
		for x := 0; x < w; x++ {
			sum, n := 0, 0
			for sy := y * k; sy < min((y+1)*k, b.Dy()); sy++ {
				srow := src.Pix[sy*src.Stride : sy*src.Stride+b.Dx()]
				for sx := x * k; sx < min((x+1)*k, b.Dx()); sx++ {
					sum += int(srow[sx])
					n++
				}
			}
			if n > 0 {
				row[x] = uint8((sum + n/2) / n)
			}
		}
	}
	return dst
}

// boxBlurAlpha blurs an alpha mask in place with a separable box blur, keeping
// a running sum so the cost is independent of the radius.
func boxBlurAlpha(a *image.Alpha, radius int) {
	if radius < 1 {
		return
	}
	b := a.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return
	}
	div := int32(2*radius + 1)
	tmp := make([]int32, w)
	for y := 0; y < h; y++ {
		row := a.Pix[y*a.Stride : y*a.Stride+w]
		var sum int32
		for i := -radius; i <= radius; i++ {
			sum += int32(row[clampInt(i, 0, w-1)])
		}
		for x := 0; x < w; x++ {
			tmp[x] = (sum + div/2) / div
			sum += int32(row[clampInt(x+radius+1, 0, w-1)]) - int32(row[clampInt(x-radius, 0, w-1)])
		}
		for x := 0; x < w; x++ {
			row[x] = uint8(tmp[x])
		}
	}
	for x := 0; x < w; x++ {
		var sum int32
		for i := -radius; i <= radius; i++ {
			sum += int32(a.Pix[clampInt(i, 0, h-1)*a.Stride+x])
		}
		for y := 0; y < h; y++ {
			v := (sum + div/2) / div
			sum += int32(a.Pix[clampInt(y+radius+1, 0, h-1)*a.Stride+x]) - int32(a.Pix[clampInt(y-radius, 0, h-1)*a.Stride+x])
			a.Pix[y*a.Stride+x] = uint8(v)
		}
	}
}

// gaussianBlurAlpha approximates a Gaussian with three box passes, the
// standard way to get Gaussian falloff at running-sum cost.
func gaussianBlurAlpha(a *image.Alpha, sigma float64) {
	if sigma < 0.5 {
		return
	}
	const passes = 3
	wIdeal := math.Sqrt(12*sigma*sigma/float64(passes) + 1)
	wl := math.Floor(wIdeal)
	if int(wl)%2 == 0 {
		wl--
	}
	wu := wl + 2
	mIdeal := (12*sigma*sigma - float64(passes)*wl*wl - 4*float64(passes)*wl - 3*float64(passes)) / (-4*wl - 4)
	m := math.Round(mIdeal)
	for i := 0; i < passes; i++ {
		size := wu
		if float64(i) < m {
			size = wl
		}
		boxBlurAlpha(a, int((size-1)/2))
	}
}

// shadowAtWorkScale blurs the silhouette once per layer and sums the layers,
// each pushed down by its own offset, into one small alpha buffer.
func shadowAtWorkScale(silhouette *image.Alpha, k int, layers []shadowLayer) *image.Alpha {
	small := downscaleAlpha(silhouette, k)
	b := small.Bounds()
	acc := make([]float64, b.Dx()*b.Dy())
	for _, layer := range layers {
		blurred := image.NewAlpha(b)
		copy(blurred.Pix, small.Pix)
		gaussianBlurAlpha(blurred, layer.sigma/float64(k))
		shift := int(math.Round(layer.dy / float64(k)))
		for y := 0; y < b.Dy(); y++ {
			sy := clampInt(y-shift, 0, b.Dy()-1)
			srow := blurred.Pix[sy*blurred.Stride : sy*blurred.Stride+b.Dx()]
			arow := acc[y*b.Dx() : y*b.Dx()+b.Dx()]
			for x := 0; x < b.Dx(); x++ {
				arow[x] += float64(srow[x]) / 255 * layer.alpha
			}
		}
	}
	out := image.NewAlpha(b)
	for y := 0; y < b.Dy(); y++ {
		orow := out.Pix[y*out.Stride : y*out.Stride+b.Dx()]
		arow := acc[y*b.Dx() : y*b.Dx()+b.Dx()]
		for x := 0; x < b.Dx(); x++ {
			orow[x] = uint8(math.Round(clamp01(arow[x]) * 255))
		}
	}
	return out
}

/* ------------------------------------------------------------------ */
/* render                                                              */
/* ------------------------------------------------------------------ */

// decorateScreenshot renders src inside a styled canvas per the style.
func decorateScreenshot(src image.Image, style FrameStyle) *image.RGBA {
	return renderFrame(src, style, 1)
}

// decorateScaled renders the same frame at a fraction of its size, for the
// workbench preview: the geometry is computed at full size and then scaled, so
// what the workbench shows is the exported frame at a smaller size. It is what
// makes a 4K capture re-render on every knob movement.
func decorateScaled(src image.Image, style FrameStyle, scale float64) *image.RGBA {
	return renderFrame(src, style, scale)
}

func renderFrame(src image.Image, style FrameStyle, scale float64) *image.RGBA {
	style = normalizeFrameStyle(style)
	b := src.Bounds()
	ow, oh := b.Dx(), b.Dy()
	if ow <= 0 || oh <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	if scale <= 0 || scale > 1 {
		scale = 1
	}
	g := computeFrame(ow, oh, style)
	if scale < 1 {
		g = g.scaled(scale)
		// Resizing the source first keeps the content crisp: framing at full
		// size and shrinking afterwards would throw the detail away.
		src = imaging.Resize(src, g.w, g.h, imaging.Lanczos)
		b = src.Bounds()
	}
	w, h := b.Dx(), b.Dy()
	g.w, g.h = w, h

	content := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(content, content.Bounds(), src, b.Min, draw.Src)
	if g.radius > 0 {
		applyMask(content, roundedRectMask(w, h, g.radius))
	}

	canvas := image.NewRGBA(image.Rect(0, 0, g.canvasW, g.canvasH))
	if style.Background != "none" {
		draw.Draw(canvas, canvas.Bounds(), newBackdrop(g.canvasW, g.canvasH, style, g.patternScale), image.Point{}, draw.Src)
	}
	ox, oy := g.contentOrigin()
	if layers := shadowStack(style.Shadow, float64(g.pad)); len(layers) > 0 {
		drawShadow(canvas, content, image.Rect(ox, oy, ox+w, oy+h), layers, shadowTint(style))
	}
	if g.ringW > 0 {
		ring := roundedRingMask(w, h, g.radius, g.ringW)
		draw.DrawMask(canvas, image.Rect(ox-g.ringW, oy-g.ringW, ox+w+g.ringW, oy+h+g.ringW),
			image.NewUniform(ringColor(style)), image.Point{}, ring, image.Point{}, draw.Over)
	}
	draw.Draw(canvas, image.Rect(ox, oy, ox+w, oy+h), content, image.Point{}, draw.Over)
	return canvas
}

// applyMask multiplies an image's pixels by an alpha mask. The mask is
// coverage, so it scales all four channels and the image stays premultiplied.
func applyMask(img *image.RGBA, mask *image.Alpha) {
	b := img.Bounds()
	for y := 0; y < b.Dy(); y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+b.Dx()*4]
		mrow := mask.Pix[y*mask.Stride : y*mask.Stride+b.Dx()]
		for x := 0; x < b.Dx(); x++ {
			a := uint32(mrow[x])
			if a == 255 {
				continue
			}
			i := x * 4
			for c := 0; c < 4; c++ {
				row[i+c] = uint8(uint32(row[i+c]) * a / 255)
			}
		}
	}
}

// shadowTint colours the cast shadow. Plain backdrops take the neutral black
// a designed shadow is made of; a pattern takes the hue the day-card reference
// gives that paper, so the cast reads as falling on the material rather than
// on a printout of it.
func shadowTint(style FrameStyle) color.NRGBA {
	if style.Background == "pattern" {
		if info, ok := patternPresets[style.BGPattern]; ok {
			return info.shadow
		}
	}
	return color.NRGBA{A: 0xff}
}

// drawShadow casts the content's silhouette as a stack of blurred layers and
// composites it into the canvas, which is drawn before the content.
//
// Two things make this read as a real shadow rather than a smudge. The
// silhouette comes from the content's own alpha, so a transparent PNG casts
// its shape instead of the rectangle around it. And each pixel is scaled by
// (1 - content alpha) before it is composited: the shadow is *behind* the
// content, so it must not tint the content's antialiased rim, and it must not
// show at all through a transparent hole.
func drawShadow(canvas *image.RGBA, content *image.RGBA, box image.Rectangle, layers []shadowLayer, tint color.NRGBA) {
	cb := canvas.Bounds()
	if cb.Dx() <= 0 || cb.Dy() <= 0 {
		return
	}
	k := clampInt(int(math.Ceil(float64(max(cb.Dx(), cb.Dy()))/shadowWorkTarget)), 1, 6)

	// The silhouette lives on canvas-sized coordinates, at the content's place
	// in them, so the blur's offsets and the canvas' margins agree.
	silhouette := image.NewAlpha(cb)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		if y < 0 || y >= cb.Dy() {
			continue
		}
		srow := silhouette.Pix[y*silhouette.Stride : y*silhouette.Stride+cb.Dx()]
		crow := content.Pix[(y-box.Min.Y)*content.Stride:]
		for x := box.Min.X; x < box.Max.X; x++ {
			if x < 0 || x >= cb.Dx() {
				continue
			}
			srow[x] = crow[(x-box.Min.X)*4+3]
		}
	}
	small := shadowAtWorkScale(silhouette, k, layers)
	sb := small.Bounds()

	for y := 0; y < cb.Dy(); y++ {
		fy := (float64(y)+0.5)/float64(k) - 0.5
		y0 := int(math.Floor(fy))
		ty := fy - float64(y0)
		y0c, y1c := clampInt(y0, 0, sb.Dy()-1), clampInt(y0+1, 0, sb.Dy()-1)
		r0 := small.Pix[y0c*small.Stride : y0c*small.Stride+sb.Dx()]
		r1 := small.Pix[y1c*small.Stride : y1c*small.Stride+sb.Dx()]
		crow := canvas.Pix[y*canvas.Stride : y*canvas.Stride+cb.Dx()*4]
		var contentRow []uint8
		if y >= box.Min.Y && y < box.Max.Y {
			contentRow = content.Pix[(y-box.Min.Y)*content.Stride:]
		}
		for x := 0; x < cb.Dx(); x++ {
			var contentAlpha uint8
			if contentRow != nil && x >= box.Min.X && x < box.Max.X {
				contentAlpha = contentRow[(x-box.Min.X)*4+3]
				if contentAlpha == 255 {
					continue // the content covers this pixel itself
				}
			}
			fx := (float64(x)+0.5)/float64(k) - 0.5
			x0 := int(math.Floor(fx))
			tx := fx - float64(x0)
			x0c, x1c := clampInt(x0, 0, sb.Dx()-1), clampInt(x0+1, 0, sb.Dx()-1)
			v := lerp(
				lerp(float64(r0[x0c]), float64(r0[x1c]), tx),
				lerp(float64(r1[x0c]), float64(r1[x1c]), tx), ty)
			v *= 1 - float64(contentAlpha)/255
			if v < 0.25 {
				continue
			}
			a := clamp01((v + ditherBias(x, y)) / 255)
			if a <= 0 {
				continue
			}
			i := x * 4
			inv := 1 - a
			crow[i] = uint8(float64(tint.R)*a + float64(crow[i])*inv)
			crow[i+1] = uint8(float64(tint.G)*a + float64(crow[i+1])*inv)
			crow[i+2] = uint8(float64(tint.B)*a + float64(crow[i+2])*inv)
			crow[i+3] = uint8(min(255, float64(crow[i+3])*inv+a*255+0.5))
		}
	}
}

// ringColor picks a hairline that stays visible on the backdrop: dark on light
// backdrops, white on dark ones. The colours are non-premultiplied: a
// color.RGBA with a channel above its alpha is not a valid pixel, and
// compositing one wraps the result to a dark, dirty edge.
func ringColor(style FrameStyle) color.Color {
	if style.Background == "none" {
		// Over transparent padding the ring has to read on any sharing surface.
		return color.NRGBA{R: 0, G: 0, B: 0, A: 64}
	}
	if backdropLuminance(style) > 0.35 {
		return color.NRGBA{R: 0, G: 0, B: 0, A: 46}
	}
	return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 46}
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
