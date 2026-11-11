package main

// x/image/tiff decodes the common TIFF layouts but not JPEG-compressed
// strips (compression 7), which cameras and image editors commonly write.
// Those files pass DecodeConfig, so they enter the queue, but every full
// decode fails; the thumbnail step swallows the error and the page falls
// back to the placeholder icon. The strips themselves hold ordinary JPEG
// streams, so decoding each strip and stacking the rows recovers them
// without adding a TIFF library.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
)

// errUnsupportedTIFF is internal: it only tells decodeImage to keep the
// standard decoder's error, which is what the user sees. It is not copy and
// stays out of the catalogs.
var errUnsupportedTIFF = errors.New("unsupported TIFF layout")

const (
	tiffTypeByte  = 1
	tiffTypeShort = 3
	tiffTypeLong  = 4
	tiffTypeSByte = 6
	tiffTypeUndef = 7
)

// Keep the parser's memory bounded when a corrupt header claims huge arrays.
const tiffMaxEntryValues = 1 << 20

func decodeJPEGTIFF(r io.ReaderAt) (image.Image, error) {
	var head [8]byte
	if _, err := r.ReadAt(head[:], 0); err != nil {
		return nil, errUnsupportedTIFF
	}
	var order binary.ByteOrder
	switch string(head[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return nil, errUnsupportedTIFF
	}
	if order.Uint16(head[2:]) != 42 {
		return nil, errUnsupportedTIFF
	}

	ifdOffset := int64(order.Uint32(head[4:]))
	var countBuf [2]byte
	if _, err := r.ReadAt(countBuf[:], ifdOffset); err != nil {
		return nil, errUnsupportedTIFF
	}
	entryCount := int(order.Uint16(countBuf[:]))
	if entryCount <= 0 || entryCount > 4096 {
		return nil, errUnsupportedTIFF
	}
	rawEntries := make([]byte, entryCount*12)
	if _, err := r.ReadAt(rawEntries, ifdOffset+2); err != nil {
		return nil, errUnsupportedTIFF
	}

	numbers := map[uint16][]uint32{}
	var jpegTables []byte
	for i := 0; i < entryCount; i++ {
		raw := rawEntries[i*12 : (i+1)*12]
		tag := order.Uint16(raw[0:])
		typ := order.Uint16(raw[2:])
		count := order.Uint32(raw[4:])
		data, ok := tiffEntryData(r, order, typ, count, raw[8:12])
		if !ok {
			continue
		}
		if tag == 347 { // JPEGTables
			jpegTables = data
			continue
		}
		numbers[tag] = tiffEntryNumbers(order, typ, data, int(count))
	}

	first := func(tag uint16) (uint32, bool) {
		values, ok := numbers[tag]
		if !ok || len(values) == 0 {
			return 0, false
		}
		return values[0], true
	}
	width, ok := first(256)
	if !ok || width == 0 {
		return nil, errUnsupportedTIFF
	}
	height, ok := first(257)
	if !ok || height == 0 {
		return nil, errUnsupportedTIFF
	}
	compression, ok := first(259)
	if !ok || compression != 7 {
		return nil, errUnsupportedTIFF
	}
	photometric, _ := first(262)
	if photometric != 1 && photometric != 2 {
		return nil, errUnsupportedTIFF
	}
	samples, _ := first(277)
	if samples != 1 && samples != 3 {
		return nil, errUnsupportedTIFF
	}
	if planar, ok := first(284); ok && planar != 1 {
		return nil, errUnsupportedTIFF
	}
	if bits, ok := numbers[258]; ok {
		for _, b := range bits {
			if b != 8 {
				return nil, errUnsupportedTIFF
			}
		}
	}
	offsets, ok := numbers[273]
	if !ok || len(offsets) == 0 {
		return nil, errUnsupportedTIFF
	}
	counts, ok := numbers[279]
	if !ok || len(counts) != len(offsets) {
		return nil, errUnsupportedTIFF
	}

	canvas := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	y := 0
	for i, offset := range offsets {
		if counts[i] == 0 || counts[i] > 64<<20 {
			return nil, errUnsupportedTIFF
		}
		strip := make([]byte, counts[i])
		if _, err := r.ReadAt(strip, int64(offset)); err != nil {
			return nil, errUnsupportedTIFF
		}
		img, err := decodeJPEGStrip(strip, jpegTables)
		if err != nil {
			return nil, err
		}
		b := img.Bounds()
		if b.Dx() != int(width) || b.Dy() <= 0 {
			return nil, errUnsupportedTIFF
		}
		rows := min(b.Dy(), int(height)-y)
		if rows <= 0 {
			break
		}
		draw.Draw(canvas, image.Rect(0, y, int(width), y+rows), img, b.Min, draw.Src)
		y += rows
	}
	if y < int(height) {
		return nil, errUnsupportedTIFF
	}
	return canvas, nil
}

// tiffEntryData returns the raw bytes of one IFD entry, following the
// offset when the value does not fit in the four-byte field.
func tiffEntryData(r io.ReaderAt, order binary.ByteOrder, typ uint16, count uint32, field []byte) ([]byte, bool) {
	size := 0
	switch typ {
	case tiffTypeByte, tiffTypeSByte, tiffTypeUndef:
		size = 1
	case tiffTypeShort:
		size = 2
	case tiffTypeLong:
		size = 4
	default:
		return nil, false
	}
	if count == 0 || count > tiffMaxEntryValues {
		return nil, false
	}
	total := int(count) * size
	if total > 4 {
		offset := int64(order.Uint32(field))
		data := make([]byte, total)
		if _, err := r.ReadAt(data, offset); err != nil {
			return nil, false
		}
		return data, true
	}
	return field[:total], true
}

func tiffEntryNumbers(order binary.ByteOrder, typ uint16, data []byte, count int) []uint32 {
	values := make([]uint32, count)
	for i := 0; i < count; i++ {
		switch typ {
		case tiffTypeByte, tiffTypeSByte:
			values[i] = uint32(data[i])
		case tiffTypeShort:
			values[i] = uint32(order.Uint16(data[i*2:]))
		case tiffTypeLong:
			values[i] = order.Uint32(data[i*4:])
		}
	}
	return values
}

// decodeJPEGStrip decodes one strip. New-style TIFF may keep the JPEG
// tables in their own tag and the strips as bare scans, so fall back to
// assembling a complete stream when the strip is not self-contained.
func decodeJPEGStrip(strip, tables []byte) (image.Image, error) {
	if img, err := jpeg.Decode(bytes.NewReader(strip)); err == nil {
		return img, nil
	}
	if len(tables) == 0 {
		return nil, errUnsupportedTIFF
	}
	body := tables
	if bytes.HasPrefix(body, []byte{0xFF, 0xD8}) {
		body = body[2:]
	}
	if bytes.HasSuffix(body, []byte{0xFF, 0xD9}) {
		body = body[:len(body)-2]
	}
	scan := strip
	if bytes.HasPrefix(scan, []byte{0xFF, 0xD8}) {
		scan = scan[2:]
	}
	if bytes.HasSuffix(scan, []byte{0xFF, 0xD9}) {
		scan = scan[:len(scan)-2]
	}
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xD8})
	buf.Write(body)
	buf.Write(scan)
	buf.Write([]byte{0xFF, 0xD9})
	return jpeg.Decode(bytes.NewReader(buf.Bytes()))
}
