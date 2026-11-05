package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/tiff"
)

// x/image/tiff rejects JPEG-compressed TIFFs, which used to leave the queue
// thumbnail empty. The fallback stitches the JPEG strips back together.
func TestJPEGCompressedTIFFGetsThumbnail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.tiff")
	writeJPEGTIFF(t, path, gradientImage(800, 600, false), 200)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := decodeImage(f, false)
	f.Close()
	if err != nil {
		t.Fatalf("decode JPEG-compressed TIFF: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 800 || b.Dy() != 600 {
		t.Fatalf("decoded %dx%d, want 800x600", b.Dx(), b.Dy())
	}

	service := newTestService(t)
	result, err := service.AddPaths([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 {
		t.Fatalf("added=%d skipped=%+v", result.Added, result.Skipped)
	}
	waitFor(t, "JPEG TIFF thumbnail", func() bool {
		state := service.State()
		return len(state.Items) == 1 && state.Items[0].Thumb != ""
	})
	if cfg := decodeDataURLConfig(t, service.State().Items[0].Thumb); cfg.Width == 0 || cfg.Height == 0 {
		t.Fatalf("thumbnail carries no image: %+v", cfg)
	}
}

func TestStandardTIFFGetsThumbnail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.tiff")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := tiff.Encode(f, gradientImage(640, 480, false), nil); err != nil {
		t.Fatal(err)
	}
	f.Close()

	service := newTestService(t)
	if _, err := service.AddPaths([]string{path}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "TIFF thumbnail", func() bool {
		state := service.State()
		return len(state.Items) == 1 && state.Items[0].Thumb != ""
	})
}

// writeJPEGTIFF writes a multi-strip, JPEG-compressed TIFF the way cameras
// and image editors do: the strip table points at ordinary JPEG streams.
func writeJPEGTIFF(t *testing.T, path string, img image.Image, rowsPerStrip int) {
	t.Helper()
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()

	var strips [][]byte
	for y := 0; y < height; y += rowsPerStrip {
		rows := min(rowsPerStrip, height-y)
		part := image.NewRGBA(image.Rect(0, 0, width, rows))
		draw.Draw(part, part.Bounds(), img, image.Pt(b.Min.X, b.Min.Y+y), draw.Src)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, part, &jpeg.Options{Quality: 88}); err != nil {
			t.Fatal(err)
		}
		strips = append(strips, buf.Bytes())
	}

	const entryCount = 10
	ifdOffset := int64(8)
	bitsOffset := ifdOffset + 2 + entryCount*12 + 4
	offsetsOffset := bitsOffset + 6
	countsOffset := offsetsOffset + int64(4*len(strips))
	dataOffset := countsOffset + int64(4*len(strips))

	var buf bytes.Buffer
	write := func(v any) {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	buf.WriteString("II")
	write(uint16(42))
	write(uint32(ifdOffset))
	write(uint16(entryCount))

	entry := func(tag, typ uint16, count, value uint32) {
		write(tag)
		write(typ)
		write(count)
		if typ == tiffTypeShort && count == 1 {
			write(uint16(value))
			write(uint16(0))
			return
		}
		write(value)
	}
	entry(256, tiffTypeLong, 1, uint32(width))
	entry(257, tiffTypeLong, 1, uint32(height))
	entry(258, tiffTypeShort, 3, uint32(bitsOffset))
	entry(259, tiffTypeShort, 1, 7) // JPEG compression
	entry(262, tiffTypeShort, 1, 2) // RGB
	entry(273, tiffTypeLong, uint32(len(strips)), uint32(offsetsOffset))
	entry(277, tiffTypeShort, 1, 3)
	entry(278, tiffTypeLong, 1, uint32(rowsPerStrip))
	entry(279, tiffTypeLong, uint32(len(strips)), uint32(countsOffset))
	entry(284, tiffTypeShort, 1, 1) // chunky
	write(uint32(0))                // no next IFD

	write(uint16(8))
	write(uint16(8))
	write(uint16(8))

	stripOffsets := make([]uint32, len(strips))
	offset := uint32(dataOffset)
	for i, strip := range strips {
		stripOffsets[i] = offset
		offset += uint32(len(strip))
	}
	for _, off := range stripOffsets {
		write(off)
	}
	for _, strip := range strips {
		write(uint32(len(strip)))
	}
	for _, strip := range strips {
		buf.Write(strip)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
