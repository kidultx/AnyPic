package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"strings"
	"testing"
)

// TestPhotoSourcesLoad guards the embedded files: four 1000x1500 JPEGs, each
// decodable locally, with a usable average lightness. A missing or corrupt
// embed would otherwise show up only as a blank backdrop in the app.
func TestPhotoSourcesLoad(t *testing.T) {
	for _, id := range photoOrder {
		img, err := photoImage(id)
		if err != nil {
			t.Fatalf("photo %s: %v", id, err)
		}
		if b := img.Bounds(); b.Dx() != 1000 || b.Dy() != 1500 {
			t.Errorf("photo %s is %dx%d, want the embedded 1000x1500", id, b.Dx(), b.Dy())
		}
		if lum := photoLuminance(id); lum <= 0 || lum >= 1 {
			t.Errorf("photo %s luminance %.3f is not in (0,1)", id, lum)
		}
	}
}

// TestPhotoBackgroundsThumbnails guards the IPC shape the workbench consumes:
// one entry per photo, in photoOrder, with a JPEG data URL generated from the
// embedded image rather than a separately packaged thumbnail.
func TestPhotoBackgroundsThumbnails(t *testing.T) {
	got := (&AnyPic{}).PhotoBackgrounds()
	if len(got) != len(photoOrder) {
		t.Fatalf("PhotoBackgrounds returned %d entries, want %d", len(got), len(photoOrder))
	}
	const prefix = "data:image/jpeg;base64,"
	for i, bg := range got {
		if bg.ID != photoOrder[i] {
			t.Errorf("entry %d is %q, want %q", i, bg.ID, photoOrder[i])
		}
		if !strings.HasPrefix(bg.Thumbnail, prefix) {
			t.Errorf("%s thumbnail is not a JPEG data URL: %.40q", bg.ID, bg.Thumbnail)
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(bg.Thumbnail, prefix))
		if err != nil {
			t.Fatalf("%s thumbnail base64: %v", bg.ID, err)
		}
		img, err := jpeg.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s thumbnail decode: %v", bg.ID, err)
		}
		if b := img.Bounds(); b.Dx() != photoThumbSide || b.Dy() != photoThumbSide {
			t.Errorf("%s thumbnail is %dx%d, want %dx%d", bg.ID, b.Dx(), b.Dy(), photoThumbSide, photoThumbSide)
		}
	}
}

// TestPhotoCoverCenter pins the cover rule: a wide source fills the height and
// is cropped from the middle, not stretched and not anchored to a corner.
func TestPhotoCoverCenter(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 300, 100))
	for _, band := range []struct {
		x0, x1 int
		c      color.RGBA
	}{
		{0, 100, color.RGBA{R: 0xff, A: 0xff}},
		{100, 200, color.RGBA{G: 0xff, A: 0xff}},
		{200, 300, color.RGBA{B: 0xff, A: 0xff}},
	} {
		draw.Draw(src, image.Rect(band.x0, 0, band.x1, 100), image.NewUniform(band.c), image.Point{}, draw.Src)
	}
	out := coverPhoto(src, 100, 100)
	if b := out.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("coverPhoto returned %v, want 100x100", b)
	}
	for _, p := range []image.Point{{10, 10}, {50, 50}, {90, 90}} {
		r, g, b, _ := out.At(p.X, p.Y).RGBA()
		if r>>8 > 40 || g>>8 < 200 || b>>8 > 40 {
			t.Errorf("cover pixel %v is (%d,%d,%d), want the green middle band", p, r>>8, g>>8, b>>8)
		}
	}
}

// TestPhotoStyleNormalizes covers the serialization contract: a zero value or
// an older payload without the two slider fields renders at 0/0, an unknown
// photo id falls back to the first photo, and out-of-range values are pinned
// to day-card's ranges.
func TestPhotoStyleNormalizes(t *testing.T) {
	var old FrameStyle
	if err := json.Unmarshal([]byte(`{"background":"photo","backgroundPhoto":"lake"}`), &old); err != nil {
		t.Fatalf("unmarshal old payload: %v", err)
	}
	if old.PhotoBlur != 0 || old.PhotoDim != 0 {
		t.Errorf("missing slider fields unmarshalled to %d/%d, want 0/0", old.PhotoBlur, old.PhotoDim)
	}
	old = normalizeFrameStyle(old)
	if old.Background != "photo" || old.BGPhoto != "lake" || old.PhotoBlur != 0 || old.PhotoDim != 0 {
		t.Errorf("old photo payload normalized to %+v, want photo/lake/0/0", old)
	}

	s := normalizeFrameStyle(FrameStyle{
		Background: "photo", BGPhoto: "not-a-photo",
		PhotoBlur: -5, PhotoDim: photoDimMax + 30,
	})
	if s.Background != "photo" || s.BGPhoto != photoOrder[0] {
		t.Errorf("bogus photo normalized to %q/%q, want photo/%q", s.Background, s.BGPhoto, photoOrder[0])
	}
	if s.PhotoBlur != 0 || s.PhotoDim != photoDimMax {
		t.Errorf("out-of-range sliders normalized to %d/%d, want 0/%d", s.PhotoBlur, s.PhotoDim, photoDimMax)
	}

	if s := normalizeFrameStyle(FrameStyle{Background: "photo", BGPhoto: "sea", PhotoBlur: 999, PhotoDim: 999}); s.PhotoBlur != photoBlurMax || s.PhotoDim != photoDimMax {
		t.Errorf("max sliders normalized to %d/%d, want %d/%d", s.PhotoBlur, s.PhotoDim, photoBlurMax, photoDimMax)
	}
}

// TestPhotoBackdropRenders checks the three visual knobs: the result covers
// the canvas opaquely, blur changes pixels without losing coverage, and dim
// actually darkens.
func TestPhotoBackdropRenders(t *testing.T) {
	const w, h = 400, 300
	plain := newPhotoBackdrop(w, h, "alpine", 0, 0)
	if b := plain.Bounds(); b.Dx() != w || b.Dy() != h {
		t.Fatalf("plain photo backdrop is %v, want %dx%d", b, w, h)
	}
	for _, p := range []image.Point{{0, 0}, {w - 1, h - 1}} {
		if _, _, _, a := plain.At(p.X, p.Y).RGBA(); a != 0xffff {
			t.Errorf("plain photo backdrop is transparent at %v (alpha %d)", p, a>>8)
		}
	}

	blurred := newPhotoBackdrop(w, h, "alpine", photoBlurMax, 0)
	diff := 0
	for y := 0; y < h; y += 20 {
		for x := 0; x < w; x += 20 {
			r0, g0, b0, _ := plain.At(x, y).RGBA()
			r1, g1, b1, _ := blurred.At(x, y).RGBA()
			diff += absInt(int(r0>>8)-int(r1>>8)) + absInt(int(g0>>8)-int(g1>>8)) + absInt(int(b0>>8)-int(b1>>8))
		}
	}
	if diff == 0 {
		t.Error("maximum blur left the photo backdrop unchanged")
	}

	dimmed := newPhotoBackdrop(w, h, "alpine", 0, photoDimMax)
	if b := dimmed.Bounds(); b.Dx() != w || b.Dy() != h {
		t.Fatalf("dimmed photo backdrop is %v, want %dx%d", b, w, h)
	}
	if avgLuminance(dimmed) >= avgLuminance(plain)*0.6 {
		t.Errorf("dim %d did not darken: dimmed %.3f, plain %.3f", photoDimMax, avgLuminance(dimmed), avgLuminance(plain))
	}
}

// TestPhotoShadowTintFollowsBrightness is the anti-hardcode guard: light and
// dark photos must take opposite sides of their own average, and the ring
// score must follow the same measured photo.
func TestPhotoShadowTintFollowsBrightness(t *testing.T) {
	for _, id := range photoOrder {
		lum := photoLuminance(id)
		tint := shadowTint(FrameStyle{Background: "photo", BGPhoto: id})
		tintLum := luminanceOf(tint)
		if tint.A != 0xff {
			t.Errorf("%s shadow tint has alpha %d, want opaque", id, tint.A)
		}
		if lum < 0.30 && tintLum <= lum {
			t.Errorf("%s: dark photo (%.3f) took a dark shadow (%.3f); the tint must lighten", id, lum, tintLum)
		}
		if lum >= 0.30 && tintLum >= lum {
			t.Errorf("%s: light photo (%.3f) took a light shadow (%.3f); the tint must darken", id, lum, tintLum)
		}
		if got := backdropLuminance(FrameStyle{Background: "photo", BGPhoto: id}); got != lum {
			t.Errorf("%s: backdropLuminance %.4f, want the photo's %.4f", id, got, lum)
		}
	}
}

func avgLuminance(img image.Image) float64 {
	b := img.Bounds()
	var sum float64
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y += 10 {
		for x := b.Min.X; x < b.Max.X; x += 10 {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			sum += luminanceOf(c)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
