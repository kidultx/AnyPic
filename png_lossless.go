package main

// Pure-Go PNG lossless layer (report §5③, wired in after the pixel guard of
// §5①).
//
// image/png always applies one fixed filter choice; the benchmark report
// measured that choosing the filter per image (and per row) recovers most of
// oxipng's advantage, because the remaining gap is the deflate
// implementation. This file implements that filter search on top of
// compress/zlib at BestCompression.
//
// It deliberately does not do palette/grey/alpha reduction yet; that is the
// second half of the lossless layer and is what makes screenshot.png (16
// colours) shrink to a palette image. No wazero, no libdeflate, no new
// dependency.

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
)

// pngFilterStrategy names a filter policy. Adaptive is libpng's per-row
// minimum-sum heuristic; the fixed strategies encode every row with one
// filter, which is what the report measured.
type pngFilterStrategy int

const (
	pngFilterAdaptive pngFilterStrategy = iota
	pngFilterNone
	pngFilterSub
	pngFilterUp
	pngFilterAverage
	pngFilterPaeth
)

func (s pngFilterStrategy) String() string {
	switch s {
	case pngFilterAdaptive:
		return "adaptive"
	case pngFilterNone:
		return "none"
	case pngFilterSub:
		return "sub"
	case pngFilterUp:
		return "up"
	case pngFilterAverage:
		return "average"
	case pngFilterPaeth:
		return "paeth"
	}
	return "unknown"
}

// encodePNGLossless is the encoder behind every lossless PNG result. The
// standard library output at the #10 effort level is always a candidate, so
// the search can never be larger than the encoder it replaced, and greyscale
// / paletted / 16-bit images keep the compact representation the standard
// library picks for them. For 8-bit truecolour images within the #10 pixel
// budget it also tries the adaptive policy and all five fixed filters and
// returns the smallest result; the second result is false (no fast mode).
//
// The search costs several full BestCompression passes (measured: about 6x
// one standard-library pass). Above the #10 threshold a pixel count alone is
// a poor predictor of that cost: a 6K UI screenshot recompresses in ~0.6s
// while a 6K photo takes ~30s for a 9% gain (measured on the round-2
// fixtures). So above the budget the decision is made from the baseline's
// own compression ratio: content that compressed to at most 1/50 of its raw
// RGB(A) stream (flat UI, gradients, screenshots) is cheap to search again
// and gets one Paeth pass at BestCompression; anything denser keeps the
// baseline and reports fast mode so the page can say the file may not be the
// smallest possible (round-2 P0-C).
func encodePNGLossless(img image.Image) ([]byte, bool, error) {
	b := img.Bounds()
	pixels := b.Dx() * b.Dy()
	baseline, err := encodePNGStdlib(img, pngCompressionLevel(pixels))
	if err != nil {
		return nil, false, err
	}
	searchable := pngSearchable(img)
	if pixels <= pngBestCompressionMaxPixels {
		if !searchable {
			return baseline, false, nil
		}
		best := baseline
		strategies := []pngFilterStrategy{
			pngFilterAdaptive,
			pngFilterNone,
			pngFilterSub,
			pngFilterUp,
			pngFilterAverage,
			pngFilterPaeth,
		}
		for _, strategy := range strategies {
			data, err := encodePNGWithFilter(img, strategy)
			if err != nil {
				return nil, false, err
			}
			if len(data) < len(best) {
				best = data
			}
		}
		return best, false, nil
	}

	if !pngCheapToSearchAgain(img, b, len(baseline)) {
		return baseline, true, nil
	}
	var candidate []byte
	if searchable {
		candidate, err = encodePNGWithFilter(img, pngFilterPaeth)
	} else {
		candidate, err = encodePNGStdlib(img, png.BestCompression)
	}
	if err != nil {
		return nil, false, err
	}
	if len(candidate) < len(baseline) {
		return candidate, false, nil
	}
	return baseline, false, nil
}

// pngSearchable reports whether the custom filter search can reproduce this
// image's pixels. It writes 8-bit RGB/RGBA only; greyscale, palette, 16-bit
// and foreign models (YCbCr, CMYK) keep image/png's own representation.
func pngSearchable(img image.Image) bool {
	switch img.ColorModel() {
	case color.RGBAModel, color.NRGBAModel, color.AlphaModel:
		return true
	}
	return false
}

// pngCheapSearchRatio is how much of the raw stream the baseline may take
// and still be treated as cheap to compress again: 1/50 = 2%. Measured 6K
// fixtures sit far apart — flat UI 0.5%, gradients 0.6%, a real photo 10%,
// sensor-like noise 48% — so the split is not near either cluster's edge.
const pngCheapSearchRatio = 50

// pngCheapToSearchAgain predicts whether another BestCompression pass will
// finish in well under a second: content that already compressed to at most
// 2% of its raw bytes did so because it has long runs and few matches, which
// is exactly the case where zlib's highest level stays cheap.
func pngCheapToSearchAgain(img image.Image, b image.Rectangle, baselineSize int) bool {
	bpp := 3
	if !imageIsOpaque(img) {
		bpp = 4
	}
	raw := int64(b.Dx()) * int64(b.Dy()) * int64(bpp)
	return int64(baselineSize)*pngCheapSearchRatio <= raw
}

// encodePNGStdlib is the candidate that preserves image/png's own choice of
// colour type (grey, palette, 16-bit) and its filter heuristic.
func encodePNGStdlib(img image.Image, level png.CompressionLevel) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: level}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodePNGWithFilter writes an 8-bit RGB or RGBA PNG using one filter
// policy. 16-bit images fall back to image/png, which is outside the
// measured benchmark set.
func encodePNGWithFilter(img image.Image, strategy pngFilterStrategy) ([]byte, error) {
	switch img.(type) {
	case *image.NRGBA64, *image.RGBA64, *image.Gray16:
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	if width <= 0 || height <= 0 {
		return nil, errors.New("png: empty image")
	}
	opaque := imageIsOpaque(img)
	bpp := 3
	colorType := byte(2) // truecolour
	if !opaque {
		bpp = 4
		colorType = 6 // truecolour with alpha
	}

	prev := make([]byte, width*bpp)
	cur := make([]byte, width*bpp)
	candidate := make([]byte, width*bpp)
	choice := make([]byte, width*bpp)
	var filtered bytes.Buffer
	filtered.Grow(height * (width*bpp + 1))

	for y := 0; y < height; y++ {
		fillPNGRow(cur, img, b.Min.Y+y, width, bpp, b.Min.X)
		filterType, dst := choosePNGFilter(cur, prev, candidate, choice, bpp, strategy)
		filtered.WriteByte(filterType)
		filtered.Write(dst)
		prev, cur = cur, prev
	}

	var compressed bytes.Buffer
	zw, err := zlib.NewWriterLevel(&compressed, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(filtered.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.Write(pngSignature)
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8 // bit depth
	ihdr[9] = colorType
	writePNGChunk(&out, "IHDR", ihdr)
	writePNGChunk(&out, "IDAT", compressed.Bytes())
	writePNGChunk(&out, "IEND", nil)
	return out.Bytes(), nil
}

// choosePNGFilter returns the filter byte and the filtered row to write.
// For the adaptive policy every filter is computed and scored; for a fixed
// policy only that one is computed into choice.
func choosePNGFilter(cur, prev, candidate, choice []byte, bpp int, strategy pngFilterStrategy) (byte, []byte) {
	if strategy != pngFilterAdaptive {
		ft := byte(strategy - pngFilterNone)
		applyPNGFilter(candidate, cur, prev, bpp, ft)
		return ft, candidate
	}
	bestFilter := byte(0)
	var bestScore uint64
	best := choice
	for ft := byte(0); ft <= 4; ft++ {
		applyPNGFilter(candidate, cur, prev, bpp, ft)
		score := pngFilterScore(candidate)
		if ft == 0 || score < bestScore {
			bestScore = score
			bestFilter = ft
			copy(best, candidate)
		}
	}
	return bestFilter, best
}

// applyPNGFilter writes the filtered form of cur into dst. Filter types are
// PNG's 0 None, 1 Sub, 2 Up, 3 Average, 4 Paeth.
func applyPNGFilter(dst, cur, prev []byte, bpp int, ft byte) {
	n := len(cur)
	switch ft {
	case 0:
		copy(dst, cur)
	case 1:
		for i := 0; i < n; i++ {
			left := byte(0)
			if i >= bpp {
				left = cur[i-bpp]
			}
			dst[i] = cur[i] - left
		}
	case 2:
		for i := 0; i < n; i++ {
			dst[i] = cur[i] - prev[i]
		}
	case 3:
		for i := 0; i < n; i++ {
			left, up := 0, int(prev[i])
			if i >= bpp {
				left = int(cur[i-bpp])
			}
			dst[i] = cur[i] - byte((left+up)/2)
		}
	case 4:
		for i := 0; i < n; i++ {
			var left, upLeft int
			up := int(prev[i])
			if i >= bpp {
				left = int(cur[i-bpp])
				upLeft = int(prev[i-bpp])
			}
			dst[i] = cur[i] - byte(paethPredictor(left, up, upLeft))
		}
	}
}

// pngFilterScore is libpng's minimum-sum-of-absolute-differences heuristic:
// treat each byte as a signed difference and sum its magnitude.
func pngFilterScore(row []byte) uint64 {
	var sum uint64
	for _, v := range row {
		if v < 0x80 {
			sum += uint64(v)
		} else {
			sum += uint64(0x100 - int(v))
		}
	}
	return sum
}

func paethPredictor(a, b, c int) int {
	p := a + b - c
	pa, pb, pc := absInt(p-a), absInt(p-b), absInt(p-c)
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// fillPNGRow converts one source row to 8-bit RGB (bpp 3) or RGBA (bpp 4).
func fillPNGRow(dst []byte, img image.Image, y, width, bpp, x0 int) {
	switch m := img.(type) {
	case *image.NRGBA:
		row := m.Pix[(y-m.Rect.Min.Y)*m.Stride+(x0-m.Rect.Min.X)*4:]
		if bpp == 4 {
			copy(dst[:width*4], row[:width*4])
			return
		}
		for x := 0; x < width; x++ {
			dst[x*3] = row[x*4]
			dst[x*3+1] = row[x*4+1]
			dst[x*3+2] = row[x*4+2]
		}
		return
	case *image.RGBA:
		row := m.Pix[(y-m.Rect.Min.Y)*m.Stride+(x0-m.Rect.Min.X)*4:]
		if bpp == 3 {
			for x := 0; x < width; x++ {
				dst[x*3] = row[x*4]
				dst[x*3+1] = row[x*4+1]
				dst[x*3+2] = row[x*4+2]
			}
			return
		}
		// PNG stores straight (non-premultiplied) alpha, so a translucent
		// RGBA pixel must go through the colour model instead of copying.
		for x := 0; x < width; x++ {
			c := color.NRGBAModel.Convert(m.At(x0+x, y)).(color.NRGBA)
			dst[x*4] = c.R
			dst[x*4+1] = c.G
			dst[x*4+2] = c.B
			dst[x*4+3] = c.A
		}
		return
	case *image.Gray:
		row := m.Pix[(y-m.Rect.Min.Y)*m.Stride+(x0-m.Rect.Min.X):]
		for x := 0; x < width; x++ {
			dst[x*3] = row[x]
			dst[x*3+1] = row[x]
			dst[x*3+2] = row[x]
			if bpp == 4 {
				dst[x*4+3] = 0xFF
			}
		}
		return
	}
	for x := 0; x < width; x++ {
		c := color.NRGBAModel.Convert(img.At(x0+x, y)).(color.NRGBA)
		if bpp == 3 {
			dst[x*3] = c.R
			dst[x*3+1] = c.G
			dst[x*3+2] = c.B
		} else {
			dst[x*4] = c.R
			dst[x*4+1] = c.G
			dst[x*4+2] = c.B
			dst[x*4+3] = c.A
		}
	}
}

func imageIsOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xFFFF {
				return false
			}
		}
	}
	return true
}

func writePNGChunk(out *bytes.Buffer, typ string, data []byte) {
	var head [8]byte
	binary.BigEndian.PutUint32(head[:4], uint32(len(data)))
	copy(head[4:], typ)
	out.Write(head[:])
	out.Write(data)
	crc := crc32.NewIEEE()
	crc.Write(head[4:])
	crc.Write(data)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	out.Write(sum[:])
}
