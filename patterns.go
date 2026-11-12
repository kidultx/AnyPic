package main

// Day-card pattern backdrops.
//
// The fifteen fills below are a Go port of day-card's backgrounds.js (MIT).
// The reference describes each backdrop as a ground colour plus one repeating
// canvas tile — or, for three of them, a whole-canvas wash. Its colours, tile
// sizes and alphas are kept as written; only the scale is re-derived. day-card
// draws its tiles once at the page's device pixel ratio, while AnyPic renders
// at the capture's own size, so a fixed 9px dot would vanish on a 4K export
// and a fixed 96px fibre tile would swallow a small crop. patternScaleFor ties
// one scalar to the canvas so the motif keeps its apparent grain; because the
// same scalar multiplies both axes, a tile is never stretched on an ultra-wide
// or a tall strip.
//
// Tiles are rasterised with a one-pixel analytic antialias (rectangles by
// exact area coverage, circles and strokes by signed distance, the one polygon
// by supersampling) and repeated with image/draw. Every ground is opaque, so a
// pattern never depends on what is behind it — and the content's shadow still
// falls across it exactly as it does on a plain backdrop.

import (
	"image"
	"image/color"
	"image/draw"
	"math"
)

// patternInfo is what a pattern contributes besides its pixels: the ground
// colour the ring checks for contrast, and the shadow tint the reference gives
// that paper. The reference's shadow alphas are not ported — AnyPic's own
// shadow levels decide strength; only the hue is taken.
type patternInfo struct {
	ground color.NRGBA
	shadow color.NRGBA
}

// patternPresets mirrors the reference's pattern list, in its order. The ids
// are protocol: the workbench's chips mirror them (src/preview.ts), and
// normalizeFrameStyle holds a stale page to the same closed set.
var patternPresets = map[string]patternInfo{
	"teal":       {ground: color.NRGBA{R: 0x5f, G: 0xe3, B: 0xcb, A: 0xff}, shadow: color.NRGBA{R: 0x00, G: 0x3c, B: 0x37, A: 0xff}},
	"gingham":    {ground: color.NRGBA{R: 0xfb, G: 0xf6, B: 0xef, A: 0xff}, shadow: color.NRGBA{R: 0x5a, G: 0x1e, B: 0x14, A: 0xff}},
	"grid":       {ground: color.NRGBA{R: 0xf3, G: 0xf0, B: 0xe6, A: 0xff}, shadow: color.NRGBA{R: 0x1e, G: 0x32, B: 0x5a, A: 0xff}},
	"kraft":      {ground: color.NRGBA{R: 0xc4, G: 0xa2, B: 0x74, A: 0xff}, shadow: color.NRGBA{R: 0x3c, G: 0x24, B: 0x0c, A: 0xff}},
	"cyan":       {ground: color.NRGBA{R: 0x1b, G: 0x3a, B: 0x7a, A: 0xff}, shadow: color.NRGBA{R: 0x00, G: 0x08, B: 0x1e, A: 0xff}},
	"dots":       {ground: color.NRGBA{R: 0x15, G: 0x15, B: 0x15, A: 0xff}, shadow: color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}},
	"deck":       {ground: color.NRGBA{R: 0xf2, G: 0xea, B: 0xd8, A: 0xff}, shadow: color.NRGBA{R: 0x0a, G: 0x28, B: 0x20, A: 0xff}},
	"linen":      {ground: color.NRGBA{R: 0x6f, G: 0x7a, B: 0x52, A: 0xff}, shadow: color.NRGBA{R: 0x14, G: 0x1a, B: 0x0a, A: 0xff}},
	"seigaiha":   {ground: color.NRGBA{R: 0x24, G: 0x50, B: 0x6a, A: 0xff}, shadow: color.NRGBA{R: 0x00, G: 0x10, B: 0x1e, A: 0xff}},
	"xuan":       {ground: color.NRGBA{R: 0xef, G: 0xe9, B: 0xdb, A: 0xff}, shadow: color.NRGBA{R: 0x50, G: 0x40, B: 0x28, A: 0xff}},
	"zigzag":     {ground: color.NRGBA{R: 0xd6, G: 0xc4, B: 0xa2, A: 0xff}, shadow: color.NRGBA{R: 0x46, G: 0x32, B: 0x14, A: 0xff}},
	"terracotta": {ground: color.NRGBA{R: 0xbf, G: 0x67, B: 0x48, A: 0xff}, shadow: color.NRGBA{R: 0x3c, G: 0x10, B: 0x04, A: 0xff}},
	"lilac":      {ground: color.NRGBA{R: 0xd9, G: 0xd3, B: 0xe6, A: 0xff}, shadow: color.NRGBA{R: 0x32, G: 0x24, B: 0x5a, A: 0xff}},
	"velvet":     {ground: color.NRGBA{R: 0x17, G: 0x32, B: 0x2a, A: 0xff}, shadow: color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}},
	"polka":      {ground: color.NRGBA{R: 0xf4, G: 0xe4, B: 0xcf, A: 0xff}, shadow: color.NRGBA{R: 0x78, G: 0x32, B: 0x14, A: 0xff}},
}

// patternOrder is the reference's display order, oldest first. It exists so
// tests and the workbench can walk the closed set without relying on map
// iteration order.
var patternOrder = []string{
	"teal", "gingham", "grid", "kraft", "cyan", "dots", "deck", "linen",
	"seigaiha", "xuan", "zigzag", "terracotta", "lilac", "velvet", "polka",
}

// patternScaleFor is the one scalar every tile is drawn at: roughly 1 at
// screenshot sizes, growing on a 4K canvas, floored so a tiny crop still shows
// its motif. It follows the longer side (a wide strip has room for a bigger
// weave) and is shared by both axes, which is what keeps tiles square.
func patternScaleFor(w, h int) float64 {
	if w <= 0 || h <= 0 {
		return 1
	}
	return min(4.0, max(0.75, float64(max(w, h))/1300))
}

// newPatternBackdrop paints a pattern into a fresh canvas at the given tile
// scale. Unknown ids paint the reference's own fallback, teal.
func newPatternBackdrop(cw, ch int, id string, scale float64) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, cw, ch))
	paintPattern(img, id, scale)
	return img
}

// paintPattern fills dst — an opaque backdrop canvas — with the pattern.
func paintPattern(dst *image.RGBA, id string, scale float64) {
	info, ok := patternPresets[id]
	if !ok {
		id = "teal"
		info = patternPresets[id]
	}
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		scale = 1
	}
	draw.Draw(dst, dst.Bounds(), image.NewUniform(info.ground), image.Point{}, draw.Src)
	switch id {
	case "teal":
		patternTeal(dst)
	case "gingham":
		patternGingham(dst, scale)
	case "grid":
		patternGrid(dst, scale)
	case "kraft":
		patternKraft(dst, scale)
	case "cyan":
		patternCyan(dst, scale)
	case "dots":
		patternDots(dst, scale)
	case "deck":
		patternDeck(dst, scale)
	case "linen":
		patternLinen(dst, scale)
	case "seigaiha":
		patternSeigaiha(dst, scale)
	case "xuan":
		patternXuan(dst, scale)
	case "zigzag":
		patternZigzag(dst, scale)
	case "terracotta":
		patternTerracotta(dst, scale)
	case "lilac":
		patternLilac(dst, scale)
	case "velvet":
		patternVelvet(dst)
	case "polka":
		patternPolka(dst, scale)
	}
}

/* ------------------------------------------------------------------ */
/* whole-canvas washes                                                 */
/* ------------------------------------------------------------------ */

// patternTeal is a diagonal teal-to-mint wash with the reference's white
// sheen growing towards the right edge. The stops are interpolated in sRGB,
// as a canvas gradient does, and a sub-LSB dither keeps a 4K ramp from
// banding where the palette changes slowly.
func patternTeal(dst *image.RGBA) {
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()
	from := [3]float64{0x1d, 0xd8, 0xd1}
	to := [3]float64{0x92, 0xec, 0xc6}
	vx, vy := 0.35*float64(w), float64(h)
	den := vx*vx + vy*vy
	if den == 0 {
		den = 1
	}
	for y := 0; y < h; y++ {
		row := dst.Pix[y*dst.Stride : y*dst.Stride+w*4]
		for x := 0; x < w; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			t := clamp01((px*vx + py*vy) / den)
			sheen := 0.14 * clamp01(px/float64(w))
			bias := ditherBias(x, y)
			i := x * 4
			for c := 0; c < 3; c++ {
				v := lerp(from[c], to[c], t)
				v += (255 - v) * sheen
				row[i+c] = uint8(math.Round(clamp01((v+bias)/255) * 255))
			}
			row[i+3] = 0xff
		}
	}
}

// patternVelvet is the reference's dark velvet: a soft spotlight from the
// upper left over an almost-black green.
func patternVelvet(dst *image.RGBA) {
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()
	cx, cy := 0.3*float64(w), 0.15*float64(h)
	radius := 0.9 * float64(max(w, h))
	if radius <= 0 {
		return
	}
	c := color.NRGBA{R: 80, G: 140, B: 110, A: 0xff}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			t := math.Hypot(dx, dy) / radius
			// Sub-LSB dither: the spotlight spans a whole canvas, and an
			// undithered 8-bit alpha ramp bands across it.
			cov := clamp01(0.45*clamp01(1-t/0.7) + ditherBias(x, y)/255)
			blendPatternPixel(dst, x, y, c, cov)
		}
	}
}

/* ------------------------------------------------------------------ */
/* tiles                                                               */
/* ------------------------------------------------------------------ */

// patternTile rasterises one repeat at s device pixels per reference unit.
// The pixel size is rounded per axis (the reference rounds too), but the
// drawing inside uses the single s for both axes, so nothing is stretched.
func patternTile(tw, th, s float64, ground color.NRGBA, paint func(*image.RGBA, float64)) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, max(1, int(math.Round(tw*s))), max(1, int(math.Round(th*s)))))
	draw.Draw(img, img.Bounds(), image.NewUniform(ground), image.Point{}, draw.Src)
	paint(img, s)
	return img
}

// patternAlphaTile is patternTile without a ground, for the one layer drawn
// over another pattern (xuan's fibre specks over its ruled paper).
func patternAlphaTile(tw, th, s float64, paint func(*image.RGBA, float64)) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, max(1, int(math.Round(tw*s))), max(1, int(math.Round(th*s)))))
	paint(img, s)
	return img
}

// tileOver repeats tile across dst, clipping the last row and column. Over,
// not Src: a tile may be fully transparent where its motif is clipped.
func tileOver(dst, tile *image.RGBA) {
	tb := tile.Bounds()
	tw, th := tb.Dx(), tb.Dy()
	if tw <= 0 || th <= 0 {
		return
	}
	db := dst.Bounds()
	for y := db.Min.Y; y < db.Max.Y; y += th {
		for x := db.Min.X; x < db.Max.X; x += tw {
			r := image.Rect(x, y, min(x+tw, db.Max.X), min(y+th, db.Max.Y))
			if r.Empty() {
				continue
			}
			sp := image.Pt(r.Min.X-x+tb.Min.X, r.Min.Y-y+tb.Min.Y)
			draw.Draw(dst, r, tile, sp, draw.Over)
		}
	}
}

func patternGingham(dst *image.RGBA, s float64) {
	red := color.NRGBA{R: 214, G: 72, B: 56, A: 107} // rgba(214, 72, 56, .42)
	tile := patternTile(36, 36, s, patternPresets["gingham"].ground, func(t *image.RGBA, s float64) {
		fillPatternRect(t, 0, 0, 36*s, 18*s, red)
		fillPatternRect(t, 0, 0, 18*s, 36*s, red)
	})
	tileOver(dst, tile)
}

func patternGrid(dst *image.RGBA, s float64) {
	line := color.NRGBA{R: 60, G: 110, B: 180, A: 51} // rgba(60, 110, 180, .2)
	tile := patternTile(22, 22, s, patternPresets["grid"].ground, func(t *image.RGBA, s float64) {
		fillPatternRect(t, 0, 0, 22*s, 1*s, line)
		fillPatternRect(t, 0, 1*s, 1*s, 21*s, line)
	})
	tileOver(dst, tile)
}

func patternKraft(dst *image.RGBA, s float64) {
	dark := color.NRGBA{R: 80, G: 52, B: 24, A: 56}     // rgba(80, 52, 24, .22)
	light := color.NRGBA{R: 255, G: 255, B: 255, A: 41} // rgba(255, 255, 255, .16)
	seed := int64(7)                                    // fixed: every card is the same
	rnd := func() float64 {
		seed = seed * 16807 % 2147483647
		return float64(seed) / 2147483647
	}
	tile := patternTile(96, 96, s, patternPresets["kraft"].ground, func(t *image.RGBA, s float64) {
		for i := 0; i < 260; i++ {
			c := dark
			if i%3 == 0 {
				c = light
			}
			fillPatternCircle(t, rnd()*96*s, rnd()*96*s, (0.5+rnd()*0.8)*s, c)
		}
	})
	tileOver(dst, tile)
}

func patternCyan(dst *image.RGBA, s float64) {
	dot := color.NRGBA{R: 255, G: 255, B: 255, A: 31} // rgba(255, 255, 255, .12)
	tile := patternTile(9, 9, s, patternPresets["cyan"].ground, func(t *image.RGBA, s float64) {
		fillPatternCircle(t, 4.5*s, 4.5*s, 1.3*s, dot)
	})
	tileOver(dst, tile)
}

func patternDots(dst *image.RGBA, s float64) {
	dot := color.NRGBA{R: 0x3d, G: 0x3d, B: 0x3d, A: 0xff}
	tile := patternTile(12, 12, s, patternPresets["dots"].ground, func(t *image.RGBA, s float64) {
		fillPatternCircle(t, 6*s, 6*s, 1.5*s, dot)
	})
	tileOver(dst, tile)
}

func patternDeck(dst *image.RGBA, s float64) {
	stripe := color.NRGBA{R: 0x2f, G: 0x5d, B: 0x50, A: 0xff}
	tile := patternTile(56, 8, s, patternPresets["deck"].ground, func(t *image.RGBA, s float64) {
		fillPatternRect(t, 28*s, 0, 28*s, 8*s, stripe)
	})
	tileOver(dst, tile)
}

func patternLinen(dst *image.RGBA, s float64) {
	warp := color.NRGBA{R: 255, G: 255, B: 255, A: 15} // rgba(255, 255, 255, .06)
	weft := color.NRGBA{R: 0, G: 0, B: 0, A: 20}       // rgba(0, 0, 0, .08)
	tile := patternTile(12, 12, s, patternPresets["linen"].ground, func(t *image.RGBA, s float64) {
		for y := 0.0; y < 12; y += 3 {
			fillPatternRect(t, 0, y*s, 12*s, 1*s, warp)
		}
		for x := 0.0; x < 12; x += 4 {
			fillPatternRect(t, x*s, 0, 1*s, 12*s, weft)
		}
	})
	tileOver(dst, tile)
}

func patternSeigaiha(dst *image.RGBA, s float64) {
	ground := patternPresets["seigaiha"].ground
	arc := color.NRGBA{R: 240, G: 232, B: 214, A: 102} // rgba(240, 232, 214, .4)
	tile := patternTile(24, 12, s, ground, func(t *image.RGBA, s float64) {
		for row := -2; row <= 4; row++ {
			y := float64(row) * 6 * s
			var xs []float64
			if row%2 != 0 {
				xs = []float64{-12, 12, 36}
			} else {
				xs = []float64{0, 24}
			}
			for _, x := range xs {
				cx, cy := x*s, y
				// Each fan hides the ones behind it, exactly as the
				// reference paints an opaque disc before its rings.
				fillPatternCircle(t, cx, cy, 11.5*s, ground)
				for _, r := range []float64{11, 7.5, 4} {
					strokePatternArc(t, cx, cy, r*s, 0.45*s, arc)
				}
			}
		}
	})
	tileOver(dst, tile)
}

func patternXuan(dst *image.RGBA, s float64) {
	// Ruled paper first: a 1-in-6 line, rotated 115° as the reference's
	// pattern transform does, then the fixed-seed fibre specks.
	rotatedLines(dst, 6*s, 1*s, 115, color.NRGBA{R: 120, G: 98, B: 66, A: 15})
	seed := int64(11)
	rnd := func() float64 {
		seed = seed * 16807 % 2147483647
		return float64(seed) / 2147483647
	}
	fleck := color.NRGBA{R: 120, G: 98, B: 66, A: 41} // rgba(120, 98, 66, .16)
	tile := patternAlphaTile(80, 80, s, func(t *image.RGBA, s float64) {
		for i := 0; i < 70; i++ {
			fillPatternCircle(t, rnd()*80*s, rnd()*80*s, (0.4+rnd()*0.7)*s, fleck)
		}
	})
	tileOver(dst, tile)
}

func patternZigzag(dst *image.RGBA, s float64) {
	band := color.NRGBA{R: 0xeb, G: 0xdf, B: 0xc8, A: 0xff}
	tile := patternTile(32, 16, s, patternPresets["zigzag"].ground, func(t *image.RGBA, s float64) {
		pts := []float64{
			0, 8, 16, 0, 32, 8, 32, 16, 16, 8, 0, 16,
		}
		for i := range pts {
			pts[i] *= s
		}
		fillPatternPolygon(t, pts, band)
	})
	tileOver(dst, tile)
}

func patternTerracotta(dst *image.RGBA, s float64) {
	dot := color.NRGBA{R: 255, G: 236, B: 214, A: 56} // rgba(255, 236, 214, .22)
	tile := patternTile(10, 10, s, patternPresets["terracotta"].ground, func(t *image.RGBA, s float64) {
		fillPatternCircle(t, 5*s, 5*s, 1.1*s, dot)
	})
	tileOver(dst, tile)
}

func patternLilac(dst *image.RGBA, s float64) {
	rotatedLines(dst, 10*s, 1*s, 45, color.NRGBA{R: 78, G: 60, B: 120, A: 36}) // rgba(78, 60, 120, .14)
}

func patternPolka(dst *image.RGBA, s float64) {
	dot := color.NRGBA{R: 0xe0, G: 0x79, B: 0x5a, A: 0xff}
	tile := patternTile(22, 22, s, patternPresets["polka"].ground, func(t *image.RGBA, s float64) {
		for _, p := range [][2]float64{{0, 0}, {22, 0}, {0, 22}, {22, 22}, {11, 11}} {
			fillPatternCircle(t, p[0]*s, p[1]*s, 3*s, dot)
		}
	})
	tileOver(dst, tile)
}

/* ------------------------------------------------------------------ */
/* rasterisation                                                       */
/* ------------------------------------------------------------------ */

// blendPatternPixel composites c over img at (x,y) with coverage cov, in
// premultiplied sRGB — the same arithmetic a canvas context uses.
func blendPatternPixel(img *image.RGBA, x, y int, c color.NRGBA, cov float64) {
	b := img.Bounds()
	if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
		return
	}
	cov = clamp01(cov)
	sa := float64(c.A) / 255 * cov
	if sa <= 0 {
		return
	}
	i := (y-b.Min.Y)*img.Stride + (x-b.Min.X)*4
	inv := 1 - sa
	img.Pix[i] = uint8(math.Round(float64(c.R)*sa + float64(img.Pix[i])*inv))
	img.Pix[i+1] = uint8(math.Round(float64(c.G)*sa + float64(img.Pix[i+1])*inv))
	img.Pix[i+2] = uint8(math.Round(float64(c.B)*sa + float64(img.Pix[i+2])*inv))
	img.Pix[i+3] = uint8(math.Round(255*sa + float64(img.Pix[i+3])*inv))
}

// fillPatternRect fills an axis-aligned rectangle with exact area coverage.
func fillPatternRect(img *image.RGBA, x, y, w, h float64, c color.NRGBA) {
	if w <= 0 || h <= 0 {
		return
	}
	b := img.Bounds()
	x0 := max(b.Min.X, int(math.Floor(x)))
	x1 := min(b.Max.X, int(math.Ceil(x+w)))
	y0 := max(b.Min.Y, int(math.Floor(y)))
	y1 := min(b.Max.Y, int(math.Ceil(y+h)))
	for py := y0; py < y1; py++ {
		cy := spanOverlap(float64(py), float64(py+1), y, y+h)
		if cy <= 0 {
			continue
		}
		for px := x0; px < x1; px++ {
			cx := spanOverlap(float64(px), float64(px+1), x, x+w)
			if cx > 0 {
				blendPatternPixel(img, px, py, c, cx*cy)
			}
		}
	}
}

func spanOverlap(a0, a1, b0, b1 float64) float64 {
	return max(0, min(a1, b1)-max(a0, b0))
}

// fillPatternCircle fills a disc with signed-distance antialiasing.
func fillPatternCircle(img *image.RGBA, cx, cy, r float64, c color.NRGBA) {
	if r <= 0 {
		return
	}
	b := img.Bounds()
	x0 := max(b.Min.X, int(math.Floor(cx-r-1)))
	x1 := min(b.Max.X, int(math.Ceil(cx+r+1)))
	y0 := max(b.Min.Y, int(math.Floor(cy-r-1)))
	y1 := min(b.Max.Y, int(math.Ceil(cy+r+1)))
	for py := y0; py < y1; py++ {
		dy := float64(py) + 0.5 - cy
		for px := x0; px < x1; px++ {
			dx := float64(px) + 0.5 - cx
			cov := clamp01(0.5 + r - math.Hypot(dx, dy))
			if cov > 0 {
				blendPatternPixel(img, px, py, c, cov)
			}
		}
	}
}

// strokePatternArc strokes the upper half of a circle — the reference draws
// each seigaiha ring from π to 0 with a butt end on the diameter.
func strokePatternArc(img *image.RGBA, cx, cy, r, halfWidth float64, c color.NRGBA) {
	if r <= 0 || halfWidth <= 0 {
		return
	}
	b := img.Bounds()
	x0 := max(b.Min.X, int(math.Floor(cx-r-halfWidth-1)))
	x1 := min(b.Max.X, int(math.Ceil(cx+r+halfWidth+1)))
	y0 := max(b.Min.Y, int(math.Floor(cy-r-halfWidth-1)))
	y1 := min(b.Max.Y, int(math.Ceil(cy+0.5)))
	for py := y0; py < y1; py++ {
		fy := float64(py) + 0.5
		dy := fy - cy
		// Fade the last row above the diameter so the two endpoints do not
		// snap to a hard line.
		edge := clamp01(0.5 + cy - fy)
		if edge <= 0 {
			continue
		}
		for px := x0; px < x1; px++ {
			dx := float64(px) + 0.5 - cx
			cov := clamp01(0.5 + halfWidth - math.Abs(math.Hypot(dx, dy)-r))
			if cov > 0 {
				blendPatternPixel(img, px, py, c, cov*edge)
			}
		}
	}
}

// fillPatternPolygon fills a closed polygon by 4×4 supersampling — the one
// shape whose exact area is not worth deriving.
func fillPatternPolygon(img *image.RGBA, pts []float64, c color.NRGBA) {
	if len(pts) < 6 || len(pts)%2 != 0 {
		return
	}
	b := img.Bounds()
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for i := 0; i < len(pts); i += 2 {
		minX, maxX = min(minX, pts[i]), max(maxX, pts[i])
		minY, maxY = min(minY, pts[i+1]), max(maxY, pts[i+1])
	}
	const ss = 4
	x0 := max(b.Min.X, int(math.Floor(minX)))
	x1 := min(b.Max.X, int(math.Ceil(maxX)))
	y0 := max(b.Min.Y, int(math.Floor(minY)))
	y1 := min(b.Max.Y, int(math.Ceil(maxY)))
	for py := y0; py < y1; py++ {
		for px := x0; px < x1; px++ {
			hits := 0
			for sy := 0; sy < ss; sy++ {
				y := float64(py) + (float64(sy)+0.5)/ss
				for sx := 0; sx < ss; sx++ {
					x := float64(px) + (float64(sx)+0.5)/ss
					if pointInPolygon(x, y, pts) {
						hits++
					}
				}
			}
			if hits > 0 {
				blendPatternPixel(img, px, py, c, float64(hits)/(ss*ss))
			}
		}
	}
}

func pointInPolygon(x, y float64, pts []float64) bool {
	inside := false
	n := len(pts) / 2
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		xi, yi := pts[i*2], pts[i*2+1]
		xj, yj := pts[j*2], pts[j*2+1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			inside = !inside
		}
	}
	return inside
}

// rotatedLines paints parallel lines every `period` units, `width` units
// thick, rotated by deg — the analytic form of the reference's line tile put
// through a rotateSelf transform. Doing the two rotated patterns per pixel
// instead of tiling a rotated bitmap leaves no seams and no aliasing crawl.
func rotatedLines(dst *image.RGBA, period, width, deg float64, c color.NRGBA) {
	if period <= 0 || width <= 0 {
		return
	}
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()
	rad := deg * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)
	for y := 0; y < h; y++ {
		py := float64(y) + 0.5
		for x := 0; x < w; x++ {
			// Pattern space is the canvas rotated backwards; a horizontal
			// band there is a line at `deg` here.
			u := -sin*(float64(x)+0.5) + cos*py
			phase := math.Mod(u, period)
			if phase < 0 {
				phase += period
			}
			cov := clamp01(0.5 + width/2 - math.Abs(phase-width/2))
			if cov > 0 {
				blendPatternPixel(dst, x, y, c, cov)
			}
		}
	}
}
