package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"math/rand"
	"testing"

	"github.com/disintegration/imaging"
)

// noiseImage is a deterministic, photograph-like source: smooth color sweeps
// with per-pixel noise, so JPEG/WebP actually have to throw information away.
func noiseImage(width, height int, seed int64) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	rng := rand.New(rand.NewSource(seed))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			n := rng.Intn(25) - 12
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(max(0, min(255, 40+200*x/max(1, width-1)+n))),
				G: uint8(max(0, min(255, 30+180*y/max(1, height-1)+n/2))),
				B: uint8(max(0, min(255, 90+100*(x+y)/max(1, width+height-2)-n))),
				A: 255,
			})
		}
	}
	return img
}

// TestWebPDecodeExpandsLimitedRange: libwebp returns limited-range YUV for a
// lossy still, where black is 16. Treating that as full-range made every
// WebP result look 8 levels too bright and a q82 real photo score SSIM 0.55.
// Decoded WebP images must be expanded before thumbnails or SSIM see them.
func TestWebPDecodeExpandsLimitedRange(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 128, 96))
	for y := 40; y < 56; y++ {
		for x := 32; x < 96; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
		}
	}
	data, err := encodeStill(src, "WEBP", 82)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	normalized := normalizeDecodedImage(decoded)
	r, g, b, _ := normalized.At(0, 0).RGBA()
	if r>>8 > 2 || g>>8 > 2 || b>>8 > 2 {
		t.Fatalf("black pixel decoded as %d,%d,%d; limited-range YUV was not expanded", r>>8, g>>8, b>>8)
	}
	if score, measured := ssimScore(src, normalized); !measured || score < 0.98 {
		t.Fatalf("q82 WebP of a flat image scored SSIM %.4f (measured %v), want >= 0.98 after range expansion", score, measured)
	}
}

func TestSSIMIdenticalImagesScoreOne(t *testing.T) {
	img := gradientImage(64, 48, false)
	if got, measured := ssimScore(img, img); !measured || got < 0.999999 {
		t.Fatalf("identical images scored %.9f (measured %v), want 1", got, measured)
	}
}

// TestSSIMReportsUnmeasuredDimensions keeps "could not measure" separate from
// a real zero: a zero-sized image returns measured=false, while a valid pair
// returns measured=true.
func TestSSIMReportsUnmeasuredDimensions(t *testing.T) {
	valid := gradientImage(16, 12, false)
	emptyWidth := image.NewNRGBA(image.Rect(0, 0, 0, 12))
	emptyHeight := image.NewNRGBA(image.Rect(0, 0, 16, 0))
	if score, measured := ssimScore(emptyWidth, valid); measured || score != 0 {
		t.Fatalf("empty-width score = %.3f, measured %v; want 0, false", score, measured)
	}
	if score, measured := ssimScore(valid, emptyHeight); measured || score != 0 {
		t.Fatalf("empty-height score = %.3f, measured %v; want 0, false", score, measured)
	}
	if _, measured := ssimScore(valid, valid); !measured {
		t.Fatal("a valid image pair was reported as not measured")
	}
}

// TestSSIMRanksEncodesByQuality: the metric has to see what the encoder lost,
// not just whether the files differ.
func TestSSIMRanksEncodesByQuality(t *testing.T) {
	src := noiseImage(320, 240, 1)
	low, err := encodeStill(src, "JPEG", 25)
	if err != nil {
		t.Fatal(err)
	}
	high, err := encodeStill(src, "JPEG", 95)
	if err != nil {
		t.Fatal(err)
	}
	lowImg, err := imaging.Decode(bytes.NewReader(low))
	if err != nil {
		t.Fatal(err)
	}
	highImg, err := imaging.Decode(bytes.NewReader(high))
	if err != nil {
		t.Fatal(err)
	}
	lowScore, lowMeasured := ssimScore(src, lowImg)
	highScore, highMeasured := ssimScore(src, highImg)
	if !lowMeasured || !highMeasured {
		t.Fatalf("valid encodes reported as not measured: low=%v high=%v", lowMeasured, highMeasured)
	}
	if !(highScore > lowScore) {
		t.Fatalf("q95 SSIM %.6f is not above q25 SSIM %.6f", highScore, lowScore)
	}
	if highScore >= 0.999999 {
		t.Fatalf("a lossy q95 encode scored %.9f; the metric is not measuring", highScore)
	}
	if lowScore <= 0 {
		t.Fatalf("q25 SSIM %.6f, want a positive score", lowScore)
	}
}

// TestSSIMMatchesSlowReference recomputes SSIM with a direct 11×11 loop over
// the definition and compares it with the separable implementation. The two
// share only the window weights and mirror mapping, so a bug in the blur
// passes or the local statistics shows up here.
func TestSSIMMatchesSlowReference(t *testing.T) {
	a := noiseImage(19, 17, 3)
	b := image.NewNRGBA(a.Bounds())
	for y := 0; y < 17; y++ {
		for x := 0; x < 19; x++ {
			c := a.NRGBAAt(x, y)
			// A different but related image: shift the channels a little so
			// variance and covariance both matter.
			b.SetNRGBA(x, y, color.NRGBA{R: uint8(min(255, int(c.R)+9)), G: c.G, B: uint8(max(0, int(c.B)-7)), A: 255})
		}
	}
	got, measured := ssimScore(a, b)
	want := slowSSIM(a, b)
	if !measured {
		t.Fatal("a valid image pair was reported as not measured")
	}
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("ssimScore = %.12f, slow reference = %.12f", got, want)
	}
	if got >= 0.999999 || got <= 0 {
		t.Fatalf("different images scored %.9f, want strictly between 0 and 1", got)
	}
}

func slowSSIM(a, b image.Image) float64 {
	pa := grayPlane(a)
	pb := grayPlane(b)
	width, height := a.Bounds().Dx(), a.Bounds().Dy()
	total := 0.0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var ma, mb, va, vb, cov float64
			for ky := -ssimRadius; ky <= ssimRadius; ky++ {
				wy := ssimWeights[ky+ssimRadius]
				yy := mirrorIndex(y+ky, height)
				for kx := -ssimRadius; kx <= ssimRadius; kx++ {
					weight := wy * ssimWeights[kx+ssimRadius]
					x1 := mirrorIndex(x+kx, width)
					av := pa[yy*width+x1]
					bv := pb[yy*width+x1]
					ma += weight * av
					mb += weight * bv
					va += weight * av * av
					vb += weight * bv * bv
					cov += weight * av * bv
				}
			}
			va -= ma * ma
			vb -= mb * mb
			cov -= ma * mb
			if va < 0 {
				va = 0
			}
			if vb < 0 {
				vb = 0
			}
			total += ((2*ma*mb + ssimC1) * (2*cov + ssimC2)) /
				((ma*ma + mb*mb + ssimC1) * (va + vb + ssimC2))
		}
	}
	return total / float64(width*height)
}

func TestMirrorIndexWrapsWithoutRepeatingTheEdge(t *testing.T) {
	cases := []struct{ index, n, want int }{
		{-1, 5, 0},
		{5, 5, 4},
		{-2, 5, 1},
		{6, 5, 3},
		{10, 5, 0},
		{0, 1, 0},
		{7, 1, 0},
	}
	for _, c := range cases {
		if got := mirrorIndex(c.index, c.n); got != c.want {
			t.Errorf("mirrorIndex(%d, %d) = %d, want %d", c.index, c.n, got, c.want)
		}
	}
}

// TestSearchEncodeRecoversFromNonMonotonicSizes: encoders like animated WebP
// can occasionally produce a smaller file at a higher quality. The binary
// search alone would never probe q95 here and would call the cap unreachable,
// so the fallback ladder has to find it.
func TestSearchEncodeRecoversFromNonMonotonicSizes(t *testing.T) {
	sizes := map[int]int{100: 1000, 95: 500}
	encode := func(quality int) ([]byte, error) {
		size := 1000
		if s, ok := sizes[quality]; ok {
			size = s
		}
		return make([]byte, size), nil
	}

	data, quality, missed, err := searchEncode(context.Background(), 550, encode)
	if err != nil {
		t.Fatal(err)
	}
	if missed {
		t.Fatal("a reachable cap was reported missed")
	}
	if len(data) != 500 || quality != 95 {
		t.Fatalf("picked quality %d at %d bytes, want q95 at 500", quality, len(data))
	}
}

// TestSearchEncodeReportsTheSmallestProbeWhenMissed: when no quality fits,
// the caller gets the smallest file the probes produced and an honest miss.
func TestSearchEncodeReportsTheSmallestProbeWhenMissed(t *testing.T) {
	encode := func(quality int) ([]byte, error) {
		if quality == targetMinQuality {
			return make([]byte, 900), nil
		}
		return make([]byte, 1000), nil
	}

	data, quality, missed, err := searchEncode(context.Background(), 100, encode)
	if err != nil {
		t.Fatal(err)
	}
	if !missed {
		t.Fatal("an unreachable cap reported reached")
	}
	if len(data) != 900 || quality != targetMinQuality {
		t.Fatalf("reported quality %d at %d bytes, want q%d at 900", quality, len(data), targetMinQuality)
	}
}

// TestSearchEncodeBoundaryQualities covers the two ends: the q100 result
// already fitting, and a cap that only the floor reaches.
func TestSearchEncodeBoundaryQualities(t *testing.T) {
	monotonic := func(quality int) ([]byte, error) {
		return make([]byte, quality*10), nil
	}
	// q100 is 1000 bytes; a cap of exactly 1000 takes it without searching.
	if _, quality, missed, err := searchEncode(context.Background(), 1000, monotonic); err != nil || missed || quality != targetMaxQuality {
		t.Fatalf("cap at the ceiling: quality=%d missed=%v err=%v, want q%d", quality, missed, err, targetMaxQuality)
	}
	// A cap of 100 bytes is exactly the floor (q10).
	data, quality, missed, err := searchEncode(context.Background(), 100, monotonic)
	if err != nil || missed {
		t.Fatalf("cap at the floor: missed=%v err=%v", missed, err)
	}
	if quality != targetMinQuality || len(data) != 100 {
		t.Fatalf("cap at the floor picked q%d at %d bytes, want q%d at 100", quality, len(data), targetMinQuality)
	}
}

// TestSearchEncodePicksTheHighestFittingQuality pins the "approach the cap
// from below" guarantee: the result is at most target, and one quality step
// higher does not fit.
func TestSearchEncodePicksTheHighestFittingQuality(t *testing.T) {
	encode := func(quality int) ([]byte, error) {
		return make([]byte, quality*10), nil
	}
	const target = 505
	data, quality, missed, err := searchEncode(context.Background(), target, encode)
	if err != nil {
		t.Fatal(err)
	}
	if missed {
		t.Fatal("a reachable cap reported missed")
	}
	if len(data) > target {
		t.Fatalf("returned %d bytes over the %d-byte target", len(data), target)
	}
	if quality != 50 || len(data) != 500 {
		t.Fatalf("picked q%d at %d bytes, want q50 at 500", quality, len(data))
	}
	higher, err := encode(quality + 1)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(higher)) <= target {
		t.Fatalf("q%d also fits in %d bytes, so q%d is not the highest fitting quality", quality+1, target, quality)
	}
}

// TestSearchEncodePropagatesEncoderErrors: a failed probe stops the search;
// the caller gets the encoder's error, never a fallback file that pretends
// the target was searched.
func TestSearchEncodePropagatesEncoderErrors(t *testing.T) {
	want := errors.New("encoder exploded")
	calls := 0
	_, _, _, err := searchEncode(context.Background(), 100, func(int) ([]byte, error) {
		calls++
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want the encoder error", err)
	}
	if calls != 1 {
		t.Fatalf("encoder calls = %d, want 1", calls)
	}
}

// TestSearchEncodeHonorsCancellation checks the guard at entry and between
// probes: a canceled pass returns no file, so a canceled compression cannot
// publish a half-searched result.
func TestSearchEncodeHonorsCancellation(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	probes := 0
	encode := func(quality int) ([]byte, error) {
		probes++
		return make([]byte, quality*10), nil
	}
	if _, _, _, err := searchEncode(canceled, 1, encode); err == nil {
		t.Fatal("searchEncode ran with an already canceled context")
	}
	if probes != 0 {
		t.Fatalf("a canceled context still probed %d times", probes)
	}

	ctx, stop := context.WithCancel(context.Background())
	probes = 0
	_, _, _, err := searchEncode(ctx, 500, func(quality int) ([]byte, error) {
		probes++
		stop()
		return make([]byte, 1000), nil
	})
	if err == nil {
		t.Fatal("searchEncode ignored cancellation after the first probe")
	}
	if probes != 1 {
		t.Fatalf("probes after cancellation = %d, want 1", probes)
	}
}
