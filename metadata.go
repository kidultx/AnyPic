package main

// The runtime half of the lossless invariant: a PNG result is delivered only
// when it decodes back to exactly the pixels handed to the encoder.

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// losslessPixelsEqual decodes a PNG re-encode and compares every pixel with
// the image handed to the encoder. This is the runtime half of invariant 3:
// a lossless result is delivered only when it is pixel-identical to its
// input, so swapping encoders can never silently change an image.
func losslessPixelsEqual(src image.Image, encoded []byte) bool {
	decoded, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		return false
	}
	return imagesPixelEqual(src, decoded)
}

// imagesPixelEqual compares two images through the non-premultiplied RGBA
// colour model, which is the representation PNG stores. The 16-bit model is
// used so two different 16-bit pixels that share their top eight bits cannot
// compare equal.
func imagesPixelEqual(a, b image.Image) bool {
	ab, bb := a.Bounds(), b.Bounds()
	if ab.Dx() != bb.Dx() || ab.Dy() != bb.Dy() {
		return false
	}
	if an, ok := a.(*image.NRGBA); ok {
		if bn, ok := b.(*image.NRGBA); ok && an.Rect == bn.Rect && an.Stride == bn.Stride {
			rowBytes := ab.Dx() * 4
			for y := 0; y < ab.Dy(); y++ {
				arow := an.Pix[y*an.Stride : y*an.Stride+rowBytes]
				brow := bn.Pix[y*bn.Stride : y*bn.Stride+rowBytes]
				if !bytes.Equal(arow, brow) {
					return false
				}
			}
			return true
		}
	}
	for y := 0; y < ab.Dy(); y++ {
		for x := 0; x < ab.Dx(); x++ {
			ca := color.NRGBA64Model.Convert(a.At(ab.Min.X+x, ab.Min.Y+y)).(color.NRGBA64)
			cb := color.NRGBA64Model.Convert(b.At(bb.Min.X+x, bb.Min.Y+y)).(color.NRGBA64)
			if ca != cb {
				return false
			}
		}
	}
	return true
}
