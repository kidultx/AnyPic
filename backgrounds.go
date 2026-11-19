package main

// Photo backdrops for the beautify workbench.
//
// The four JPEGs in resources/backgrounds come from the day-card plugin by
// magpie-community/plugins (packages/day-card/page/bg), Copyright (c) 2026
// magpie-community contributors, and are used under that package's MIT
// license. The plugin's NOTICE excludes only the Amp logo; no exception
// applies to these photos. See resources/backgrounds/NOTICE.
//
// The bytes are embedded in the binary and decoded locally on first use.
// Nothing here fetches an image or reads one from the user's disk, which is
// what keeps the promise intact (AGENTS.md invariant 1).

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"sync"

	"github.com/disintegration/imaging"
)

// The files are embedded rather than copied into the frontend bundle: the
// same bytes draw both the full backdrop and (downscaled) the workbench's
// thumbnails, so the app carries one copy of each photo.
//
//go:embed resources/backgrounds/*.jpg
var backgroundPhotos embed.FS

// photoOrder is the workbench's display order. The ids are protocol: the page
// mirrors them, and normalizeFrameStyle holds a stale page to the same set.
var photoOrder = []string{"alpine", "dunes", "lake", "sea"}

// The ranges mirror day-card's BLUR and DIM controls. Blur is a downscale
// factor rather than a pixel radius, so the same slider has the same apparent
// softness on a 1200px preview and a 4K export; dim is a percentage of black.
const (
	photoBlurMax   = 20
	photoDimMax    = 60
	photoThumbSide = 96
)

// photoSource is one decoded embedded photo plus what the renderer needs to
// choose a shadow tint and a ring colour from it. Decoding happens once, on
// first use, so opening the photo group or dragging a slider does not decode
// a 1.5-megapixel JPEG again.
type photoSource struct {
	id   string
	once sync.Once
	img  *image.RGBA
	avg  color.NRGBA
	lum  float64
	err  error
}

var photoSources = func() map[string]*photoSource {
	m := make(map[string]*photoSource, len(photoOrder))
	for _, id := range photoOrder {
		m[id] = &photoSource{id: id}
	}
	return m
}()

func (p *photoSource) load() error {
	p.once.Do(func() {
		data, err := backgroundPhotos.ReadFile("resources/backgrounds/" + p.id + ".jpg")
		if err != nil {
			p.err = fmt.Errorf("read photo %s: %w", p.id, err)
			return
		}
		decoded, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			p.err = fmt.Errorf("decode photo %s: %w", p.id, err)
			return
		}
		b := decoded.Bounds()
		p.img = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(p.img, p.img.Bounds(), decoded, b.Min, draw.Src)
		p.avg, p.lum = averageColor(p.img)
	})
	return p.err
}

// photoByID returns the source for an id, falling back to the first photo for
// an unknown id just as normalizeFrameStyle does.
func photoByID(id string) *photoSource {
	if p, ok := photoSources[id]; ok {
		return p
	}
	return photoSources[photoOrder[0]]
}

func photoImage(id string) (image.Image, error) {
	p := photoByID(id)
	if err := p.load(); err != nil {
		return nil, err
	}
	return p.img, nil
}

// photoLuminance is the photo's average linear-light lightness, used by the
// ring and by the light/dark shadow choice.
func photoLuminance(id string) float64 {
	p := photoByID(id)
	if p.load() != nil {
		return 0.5
	}
	return p.lum
}

// averageColor is the photo's mean sRGB colour and its linear-light
// luminance. The shadow tint uses the colour; the ring and the light/dark
// shadow decision use the luminance.
func averageColor(img *image.RGBA) (color.NRGBA, float64) {
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return color.NRGBA{A: 0xff}, 0
	}
	var sr, sg, sb float64
	n := 0
	for y := 0; y < b.Dy(); y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+b.Dx()*4]
		for i := 0; i < len(row); i += 4 {
			sr += float64(row[i])
			sg += float64(row[i+1])
			sb += float64(row[i+2])
			n++
		}
	}
	avg := color.NRGBA{
		R: uint8(sr/float64(n) + 0.5),
		G: uint8(sg/float64(n) + 0.5),
		B: uint8(sb/float64(n) + 0.5),
		A: 0xff,
	}
	return avg, luminanceOf(avg)
}

// coverPhoto scales src to cover a w×h box and centre-crops the overflow.
// Keeping the crop in one named function makes the centring contract explicit
// (and testable) instead of spreading an imaging.Fill call through the blur
// and no-blur branches.
func coverPhoto(src image.Image, w, h int) image.Image {
	return imaging.Fill(src, w, h, imaging.Center, imaging.Lanczos)
}

// newPhotoBackdrop covers a cw×ch canvas with the chosen photo, centre
// cropped. Blur follows day-card's approach — draw small, scale back up — so
// it stays fast on a 4K canvas and has no hard edge at the frame; dim is a
// black wash over the result.
func newPhotoBackdrop(cw, ch int, id string, blur, dim int) image.Image {
	src, err := photoImage(id)
	if err != nil || cw <= 0 || ch <= 0 {
		// The photos are embedded, so this only runs if the binary is corrupt.
		// A neutral opaque fill is still a valid backdrop, never a transparent
		// hole or a panic in the middle of an export.
		return image.NewUniform(color.RGBA{R: 0x1f, G: 0x29, B: 0x37, A: 0xff})
	}
	blur = clampInt(blur, 0, photoBlurMax)
	dim = clampInt(dim, 0, photoDimMax)

	var img image.Image
	if blur > 0 {
		f := 1 / (1 + float64(blur)*0.55)
		sw := max(8, int(math.Round(float64(cw)*f)))
		sh := max(8, int(math.Round(float64(ch)*f)))
		img = coverPhoto(src, sw, sh)
		img = imaging.Resize(img, cw, ch, imaging.Lanczos)
	} else {
		img = coverPhoto(src, cw, ch)
	}
	if dim == 0 {
		return img
	}
	out := image.NewNRGBA(image.Rect(0, 0, cw, ch))
	draw.Draw(out, out.Bounds(), img, image.Point{}, draw.Src)
	k := 1 - float64(dim)/100
	for i := 0; i < len(out.Pix); i += 4 {
		out.Pix[i] = uint8(float64(out.Pix[i])*k + 0.5)
		out.Pix[i+1] = uint8(float64(out.Pix[i+1])*k + 0.5)
		out.Pix[i+2] = uint8(float64(out.Pix[i+2])*k + 0.5)
	}
	return out
}

// photoShadowTint derives the cast shadow's colour from the photo's own mean
// colour. A light photo takes a dark ink shadow of its hue; a dark photo takes
// a light haze, because the same ink would disappear into it. The switch is on
// linear luminance — the measure the ring already uses — not on a hardcoded
// id, so replacing a photo changes the tint with it.
func photoShadowTint(id string) color.NRGBA {
	p := photoByID(id)
	if p.load() != nil {
		return color.NRGBA{A: 0xff}
	}
	if p.lum >= 0.30 {
		return mixColor(p.avg, color.NRGBA{A: 0xff}, 0.80)
	}
	return mixColor(p.avg, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, 0.72)
}

// mixColor interpolates in sRGB, which is enough for a shadow tint: its own
// alpha profile decides the strength, here only the hue and lightness matter.
func mixColor(c, target color.NRGBA, t float64) color.NRGBA {
	return color.NRGBA{
		R: uint8(float64(c.R)*(1-t) + float64(target.R)*t + 0.5),
		G: uint8(float64(c.G)*(1-t) + float64(target.G)*t + 0.5),
		B: uint8(float64(c.B)*(1-t) + float64(target.B)*t + 0.5),
		A: 0xff,
	}
}

// PhotoBackground is one embedded photo backdrop with a thumbnail generated
// from the same JPEG at runtime. The page builds its chips from these, so the
// bundle does not carry a second set of thumbnail files.
type PhotoBackground struct {
	ID        string `json:"id"`
	Thumbnail string `json:"thumbnail"`
}

// PhotoBackgrounds lists the built-in photos for the workbench. A decode
// failure on an embedded, build-time asset is a build bug; the test below
// makes it fail CI rather than leaving a silent gap in the chips.
func (a *AnyPic) PhotoBackgrounds() []PhotoBackground {
	out := make([]PhotoBackground, 0, len(photoOrder))
	for _, id := range photoOrder {
		src, err := photoImage(id)
		if err != nil {
			continue
		}
		thumb := coverPhoto(src, photoThumbSide, photoThumbSide)
		url, err := dataURL(thumb, false, 80, photoThumbSide)
		if err != nil {
			continue
		}
		out = append(out, PhotoBackground{ID: id, Thumbnail: url})
	}
	return out
}
