package main

// Two honest answers next to "saved 63%": how close the result looks to its
// source (SSIM), and whether a target-size pass could actually reach its cap.
//
// SSIM follows Wang, Bovik, Sheikh and Simoncelli (2004): grayscale planes,
// an 11×11 Gaussian window (σ = 1.5), local means/variances/covariance, and
// C1 = (0.01·L)², C2 = (0.03·L)² for an 8-bit L. The score is the mean over
// every pixel; the window reaches past the edge by mirroring it, so no pixel
// is left unmeasured. Comparing the image handed to the encoder with the
// decoded output means a deliberate MaxDimension resize is not counted as
// compression damage.

import (
	"context"
	"image"
	"image/color"
	"math"

	"github.com/disintegration/imaging"
)

const (
	ssimRadius  = 5
	ssimSigma   = 1.5
	ssimMaxSide = 1024
	ssimL       = 255.0
	ssimC1      = (0.01 * ssimL) * (0.01 * ssimL)
	ssimC2      = (0.03 * ssimL) * (0.03 * ssimL)

	// targetMinQuality is the floor the target-size search may reach. Below
	// this the result stops being a smaller version of the picture and starts
	// being a different one; a target that needs less is honestly reported as
	// missed instead.
	targetMinQuality = 10
	targetMaxQuality = 100
)

// ssimScore measures two images' structural similarity in [0, 1]. The bool
// is false when the score could not be measured (a zero-sized image), so
// callers never mistake "not measured" for a real zero.
func ssimScore(a, b image.Image) (float64, bool) {
	aw, ah := a.Bounds().Dx(), a.Bounds().Dy()
	bw, bh := b.Bounds().Dx(), b.Bounds().Dy()
	if aw <= 0 || ah <= 0 || bw <= 0 || bh <= 0 {
		return 0, false
	}
	// Compare at the smaller image's dimensions — normally the result — and
	// cap the working size so a 24-megapixel photo does not spend hundreds of
	// milliseconds per item on the metric alone.
	w, h := aw, ah
	if bw*bh < aw*ah {
		w, h = bw, bh
	}
	if long := max(w, h); long > ssimMaxSide {
		w = max(1, w*ssimMaxSide/long)
		h = max(1, h*ssimMaxSide/long)
	}

	planeA := grayPlane(scaleTo(a, w, h))
	planeB := grayPlane(scaleTo(b, w, h))

	muA := blurPlane(planeA, w, h)
	muB := blurPlane(planeB, w, h)
	aa := make([]float64, len(planeA))
	bb := make([]float64, len(planeA))
	ab := make([]float64, len(planeA))
	for i := range planeA {
		aa[i] = planeA[i] * planeA[i]
		bb[i] = planeB[i] * planeB[i]
		ab[i] = planeA[i] * planeB[i]
	}
	varA := blurPlane(aa, w, h)
	varB := blurPlane(bb, w, h)
	cov := blurPlane(ab, w, h)

	total := 0.0
	for i := range planeA {
		ma, mb := muA[i], muB[i]
		va := varA[i] - ma*ma
		vb := varB[i] - mb*mb
		c := cov[i] - ma*mb
		// Rounding can push a constant patch a hair below zero.
		if va < 0 {
			va = 0
		}
		if vb < 0 {
			vb = 0
		}
		numerator := (2*ma*mb + ssimC1) * (2*c + ssimC2)
		denominator := (ma*ma + mb*mb + ssimC1) * (va + vb + ssimC2)
		total += numerator / denominator
	}
	score := total / float64(len(planeA))
	return min(1, max(0, score)), true
}

// normalizeDecodedImage expands the limited-range YUV that libwebp hands
// back for a lossy still (Y 16–235, Cb/Cr 16–240) into full-range NRGBA.
// image.NYCbCrA.At treats those samples as full range, so leaving them alone
// lifts every black to 16/255 and darkens highlights: thumbnails look washed
// out, and SSIM measures the range mismatch instead of the compression. WebP
// encoders are not the only source of *image.NYCbCrA, but libwebp is the one
// AnyPic decodes, and its lossy still output is always limited-range BT.601.
func normalizeDecodedImage(img image.Image) image.Image {
	ycc, ok := img.(*image.NYCbCrA)
	if !ok {
		return img
	}
	b := ycc.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			yi := ycc.Y[ycc.YOffset(b.Min.X+x, b.Min.Y+y)]
			ci := ycc.COffset(b.Min.X+x, b.Min.Y+y)
			cb := float64(ycc.Cb[ci]) - 128
			cr := float64(ycc.Cr[ci]) - 128
			luma := 1.164383 * (float64(yi) - 16)
			out.SetNRGBA(x, y, color.NRGBA{
				R: clampByte(luma + 1.596027*cr),
				G: clampByte(luma - 0.391762*cb - 0.812968*cr),
				B: clampByte(luma + 2.017232*cb),
				A: ycc.A[ycc.AOffset(b.Min.X+x, b.Min.Y+y)],
			})
		}
	}
	return out
}

func clampByte(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return uint8(v + 0.5)
	}
}

// scaleTo resizes an image to exactly w×h unless it already is that size.
func scaleTo(img image.Image, w, h int) image.Image {
	if b := img.Bounds(); b.Dx() == w && b.Dy() == h {
		return img
	}
	return imaging.Resize(img, w, h, imaging.Lanczos)
}

// grayPlane converts an image to Rec. 709 luma, one float per pixel.
func grayPlane(img image.Image) []float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	plane := make([]float64, w*h)
	for y := 0; y < h; y++ {
		row := plane[y*w : (y+1)*w]
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			row[x] = 0.2126*float64(r>>8) + 0.7152*float64(g>>8) + 0.0722*float64(bl>>8)
		}
	}
	return plane
}

// ssimWeights is the normalized 1-D Gaussian half of the 11×11 window.
var ssimWeights = func() []float64 {
	weights := make([]float64, ssimRadius*2+1)
	sum := 0.0
	for i := range weights {
		x := float64(i - ssimRadius)
		weights[i] = math.Exp(-(x * x) / (2 * ssimSigma * ssimSigma))
		sum += weights[i]
	}
	for i := range weights {
		weights[i] /= sum
	}
	return weights
}()

// blurPlane applies the Gaussian separably: one horizontal pass, one vertical.
func blurPlane(src []float64, w, h int) []float64 {
	tmp := make([]float64, len(src))
	dst := make([]float64, len(src))

	for y := 0; y < h; y++ {
		row := src[y*w : (y+1)*w]
		out := tmp[y*w : (y+1)*w]
		for x := 0; x < w; x++ {
			acc := 0.0
			for k, weight := range ssimWeights {
				acc += weight * row[mirrorIndex(x+k-ssimRadius, w)]
			}
			out[x] = acc
		}
	}
	for y := 0; y < h; y++ {
		out := dst[y*w : (y+1)*w]
		for k, weight := range ssimWeights {
			yy := mirrorIndex(y+k-ssimRadius, h)
			row := tmp[yy*w : (yy+1)*w]
			for x := range out {
				out[x] += weight * row[x]
			}
		}
	}
	return dst
}

// mirrorIndex maps an out-of-range index back into [0, n) by mirroring the
// edge without repeating it: -1 → 0, n → n-1, -2 → 1, n+1 → n-2.
func mirrorIndex(i, n int) int {
	if n <= 1 {
		return 0
	}
	for i < 0 || i >= n {
		if i < 0 {
			i = -i - 1
		} else {
			i = 2*n - 1 - i
		}
	}
	return i
}

// encodeToTarget encodes img in outFormat at the highest quality whose output
// fits target, searching the integer quality range. When even the floor is too
// large it returns that smallest result with missed = true, so the caller can
// say the cap was not reachable rather than dropping the image.
func encodeToTarget(ctx context.Context, img image.Image, outFormat string, target int64) (data []byte, quality int, missed bool, err error) {
	return searchEncode(ctx, target, func(quality int) ([]byte, error) {
		return encodeStill(img, outFormat, quality)
	})
}

// searchEncode is the quality search shared by stills and animations. It
// probes quality 100, then binary-searches 10–99; because some encoders are
// not strictly monotonic in quality, a failed search falls back to a coarse
// ladder before reporting a miss. encode must produce the whole file at a
// given quality; the caller keeps the frames, delays and loop count intact.
func searchEncode(ctx context.Context, target int64, encode func(quality int) ([]byte, error)) (data []byte, quality int, missed bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, false, err
	}
	// Every branch below either returns a file at most target bytes (missed
	// false) or the smallest probed file with missed true; the ladder always
	// steps down by a positive amount, so the loop terminates.
	best, err := encode(targetMaxQuality)
	if err != nil {
		return nil, 0, false, err
	}
	if int64(len(best)) <= target {
		return best, targetMaxQuality, false, nil
	}

	lo, hi := targetMinQuality, targetMaxQuality-1
	var found []byte
	foundQuality := 0
	for lo <= hi {
		if err := ctx.Err(); err != nil {
			return nil, 0, false, err
		}
		mid := (lo + hi) / 2
		candidate, err := encode(mid)
		if err != nil {
			return nil, 0, false, err
		}
		if int64(len(candidate)) <= target {
			found, foundQuality = candidate, mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if found != nil {
		return found, foundQuality, false, nil
	}

	// The binary search assumes a higher quality never produces a smaller
	// file. That holds for most stills but not always — flat animated frames
	// and tiny images can invert it — so before declaring the cap
	// unreachable, walk a coarse ladder from the top. Otherwise a reachable
	// cap (say exactly the q95 size) could be reported as missed because the
	// search never probed that quality.
	const fallbackStep = 5
	smallest := best
	smallestQuality := targetMaxQuality
	for quality := targetMaxQuality - fallbackStep; quality >= targetMinQuality; quality -= fallbackStep {
		if err := ctx.Err(); err != nil {
			return nil, 0, false, err
		}
		candidate, err := encode(quality)
		if err != nil {
			return nil, 0, false, err
		}
		if int64(len(candidate)) <= target {
			// Refine upward inside this band, so the answer is still the
			// highest quality that fits as far as probing can tell.
			chosen, chosenQuality := candidate, quality
			for higher := quality + fallbackStep - 1; higher > quality; higher-- {
				if err := ctx.Err(); err != nil {
					return nil, 0, false, err
				}
				probe, err := encode(higher)
				if err != nil {
					return nil, 0, false, err
				}
				if int64(len(probe)) <= target {
					chosen, chosenQuality = probe, higher
					break
				}
			}
			return chosen, chosenQuality, false, nil
		}
		if int64(len(candidate)) < int64(len(smallest)) {
			smallest = candidate
			smallestQuality = quality
		}
	}
	return smallest, smallestQuality, true, nil
}
