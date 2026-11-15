package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
)

func TestLosslessGuardKeepsOriginalWhenPixelsChange(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "shot.png")
	writePNG(t, src, gradientImage(320, 240, true))

	original := pngLosslessEncode
	defer func() { pngLosslessEncode = original }()
	pngLosslessEncode = func(img image.Image) ([]byte, error) {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, img); err != nil {
			return nil, err
		}
		decoded, err := png.Decode(bytes.NewReader(encoded.Bytes()))
		if err != nil {
			return nil, err
		}
		changed := image.NewNRGBA(decoded.Bounds())
		draw.Draw(changed, changed.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
		changed.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 0, B: 255, A: 255})
		encoded.Reset()
		if err := png.Encode(&encoded, changed); err != nil {
			return nil, err
		}
		return encoded.Bytes(), nil
	}

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 4, src, "PNG", "shot.png", false, Settings{
		Format: "png", Quality: 82, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.passedThrough || result.keptOriginalReason != "pixels_changed" {
		t.Fatalf("passthrough = %v reason = %q, want true/pixels_changed", result.passedThrough, result.keptOriginalReason)
	}
	if result.path != src {
		t.Fatalf("guard fallback path = %q, want original %q", result.path, src)
	}
}

func TestLosslessGuardFailsCrossFormatHonestly(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	writeJPEG(t, src, gradientImage(320, 240, false), 90)

	original := pngLosslessEncode
	defer func() { pngLosslessEncode = original }()
	pngLosslessEncode = func(img image.Image) ([]byte, error) {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, img); err != nil {
			return nil, err
		}
		decoded, err := png.Decode(bytes.NewReader(encoded.Bytes()))
		if err != nil {
			return nil, err
		}
		changed := image.NewNRGBA(decoded.Bounds())
		draw.Draw(changed, changed.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
		changed.SetNRGBA(0, 0, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
		encoded.Reset()
		if err := png.Encode(&encoded, changed); err != nil {
			return nil, err
		}
		return encoded.Bytes(), nil
	}

	service := &AnyPic{tempDir: t.TempDir()}
	_, err := service.compressOne(context.Background(), 5, src, "JPEG", "photo.jpg", false, Settings{
		Format: "png", Quality: 82, Suffix: "-anypic",
	})
	if err == nil || !strings.HasPrefix(err.Error(), tr().GoErrLosslessCheckFailed()) {
		t.Fatalf("error = %v, want %q", err, tr().GoErrLosslessCheckFailed())
	}
}

func TestPassthroughReasons(t *testing.T) {
	t.Run("not smaller", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "tiny.jpg")
		writeJPEG(t, src, gradientImage(640, 480, false), 30)
		service := &AnyPic{tempDir: t.TempDir()}
		result, err := service.compressOne(context.Background(), 6, src, "JPEG", "tiny.jpg", false, Settings{
			Format: "jpeg", Quality: 100, Suffix: "-anypic",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.passedThrough || result.keptOriginalReason != "not_smaller" {
			t.Fatalf("passthrough = %v reason = %q, want true/not_smaller", result.passedThrough, result.keptOriginalReason)
		}
	})

	t.Run("target met", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "shot.png")
		size := writePNG(t, src, gradientImage(200, 150, false))
		service := &AnyPic{tempDir: t.TempDir()}
		result, err := service.compressOne(context.Background(), 7, src, "PNG", "shot.png", false, Settings{
			Format: "png", Quality: 82, Suffix: "-anypic", TargetSize: size + 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.passedThrough || result.keptOriginalReason != "target_met" {
			t.Fatalf("passthrough = %v reason = %q, want true/target_met", result.passedThrough, result.keptOriginalReason)
		}
	})
}

func TestLosslessGuardAcceptsExactPixels(t *testing.T) {
	src := gradientImage(64, 48, true)
	data, err := pngLosslessEncode(src)
	if err != nil {
		t.Fatal(err)
	}
	if !losslessPixelsEqual(src, data) {
		t.Fatal("the standard PNG encoder must pass its own pixel guard")
	}
	changed := gradientImage(64, 48, true)
	changed.SetNRGBA(1, 1, color.NRGBA{R: 1, G: 2, B: 3, A: 4})
	if losslessPixelsEqual(changed, data) {
		t.Fatal("guard accepted an image with a different pixel")
	}
}
