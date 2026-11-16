package main

// Metadata handling for re-encoded stills. AnyPic keeps the source's colour
// profile untouched (an iCCP chunk for PNG, the ICC_PROFILE APP2 segments for
// JPEG) so a Display P3 screenshot does not silently turn into an untagged
// image. EXIF is kept for JPEG but rebuilt selectively: the GPS directory is
// emptied (privacy first) and Orientation is rewritten to 1 because
// imaging.AutoOrientation already rotated the pixels. Nothing here converts
// colour spaces; the profile travels as opaque bytes.
//
// JPEG ICC profiles may span several APP2 segments (ICC_PROFILE\0, sequence,
// total, data). They are copied in sequence order and only when every segment
// is present; a partial profile would be worse than none.

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
)

// MetadataReport says what the output kept, changed or dropped from the
// source's metadata. Every field only speaks about metadata that was present
// in the source, so the page never claims to have removed something that was
// not there.
type MetadataReport struct {
	// ICCKept: the source's ICC colour profile is in the output unchanged.
	ICCKept bool `json:"iccKept"`
	// EXIFKept: the source's EXIF is in the output (JPEG only), minus GPS.
	EXIFKept bool `json:"exifKept"`
	// GPSRemoved: the EXIF GPS directory was emptied.
	GPSRemoved bool `json:"gpsRemoved"`
	// OrientationReset: EXIF Orientation was rewritten to 1 because the
	// pixels had already been rotated by AutoOrientation.
	OrientationReset bool `json:"orientationReset"`
	// ICCDroppedIncompatible: the source carried an ICC profile whose data
	// colour space does not match the pixels the output format writes (a
	// CMYK profile on a 3-channel JPEG, an RGB profile on a grayscale one).
	// Such a profile was dropped rather than attached to pixels it does not
	// describe, which would make colour-managed viewers reject or misrender
	// the file. ICCColorSpace names the source profile's space ("CMYK",
	// "RGB", "GRAY", ...).
	ICCDroppedIncompatible bool   `json:"iccDroppedIncompatible"`
	ICCColorSpace          string `json:"iccColorSpace,omitempty"`
	// ICCDroppedUnsupported: the source carried an ICC profile but the
	// output container cannot store one yet (WebP, P0-F). The profile was
	// dropped and is reported instead of vanishing silently.
	ICCDroppedUnsupported bool `json:"iccDroppedUnsupported"`
	// Dropped: metadata the source carried but the output could not keep
	// (XMP, IPTC, comments, PNG text chunks, or EXIF/ICC when the output
	// format cannot hold the source's representation).
	Dropped bool `json:"dropped"`
}

// jpegICCSegment is one raw APP2 segment, marker and length included. It is
// copied verbatim into the output, so the profile bytes and the segment
// boundaries stay exactly as the source wrote them.
type jpegICCSegment struct {
	seq   int
	total int
	raw   []byte
}

// sourceMetadata is what the source file carries and the output might need.
type sourceMetadata struct {
	pngICCChunk []byte
	jpegICC     []jpegICCSegment
	webpICC     []byte
	// iccSpace is the data colour space declared by the ICC profile header
	// ("RGB", "GRAY", "CMYK", ...), or "" when the profile is absent or its
	// header cannot be read. Output pixels must describe the same space
	// before the profile is carried over (P0-B).
	iccSpace string
	exif     []byte
	hadEXIF  bool
	hadICC   bool
	hadOther bool
}

// stillMetadata is the part of the source's metadata that will be written
// into the re-encoded file. Its size is known before encoding, so a target
// size search can budget for it instead of overshooting the cap.
type stillMetadata struct {
	pngICCChunk  []byte
	jpegSegments [][]byte
	exif         []byte
}

// readSourceMetadata scans the file header for metadata. It never fails the
// compression: a file the decoders accepted but this scanner cannot parse
// simply contributes no metadata (and no metadata report).
func readSourceMetadata(f *os.File, srcFormat string) sourceMetadata {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return sourceMetadata{}
	}
	switch displayFormat(srcFormat) {
	case "PNG":
		return readPNGMetadata(f)
	case "JPEG":
		return readJPEGMetadata(f)
	case "WEBP":
		return readWebPMetadata(f)
	}
	return sourceMetadata{}
}

// readPNGMetadata walks the chunk list. It keeps the iCCP chunk raw (length,
// type, data and CRC) so it can be inserted into the output byte-for-byte.
func readPNGMetadata(r io.Reader) sourceMetadata {
	var meta sourceMetadata
	sig := make([]byte, len(pngSignature))
	if _, err := io.ReadFull(r, sig); err != nil || !bytes.Equal(sig, pngSignature) {
		return meta
	}
	for {
		var head [8]byte
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return meta
		}
		length := binary.BigEndian.Uint32(head[:4])
		typ := string(head[4:8])
		if typ == "iCCP" {
			// A profile this large is not a colour profile worth carrying,
			// and allocating for a declared 4 GiB chunk would be a
			// decompression-bomb-ish surprise. Mark it present but dropped.
			if length > 16<<20 {
				meta.hadICC = true
				if _, err := io.CopyN(io.Discard, r, int64(length)+4); err != nil {
					return meta
				}
				continue
			}
			data := make([]byte, int(length))
			if _, err := io.ReadFull(r, data); err != nil {
				return meta
			}
			var crc [4]byte
			if _, err := io.ReadFull(r, crc[:]); err != nil {
				return meta
			}
			chunk := make([]byte, 0, 12+len(data))
			chunk = append(chunk, head[:]...)
			chunk = append(chunk, data...)
			chunk = append(chunk, crc[:]...)
			meta.pngICCChunk = chunk
			meta.iccSpace = pngICCColorSpace(chunk)
			meta.hadICC = true
			continue
		}
		if _, err := io.CopyN(io.Discard, r, int64(length)); err != nil {
			return meta
		}
		var crc [4]byte
		if _, err := io.ReadFull(r, crc[:]); err != nil {
			return meta
		}
		switch typ {
		case "eXIf":
			// EXIF is carried only in JPEG outputs for now; a PNG eXIf
			// block is dropped and reported as such.
			meta.hadEXIF = true
		case "tEXt", "zTXt", "iTXt":
			meta.hadOther = true
		case "IEND":
			return meta
		}
	}
}

// pngICCColorSpace decompresses an iCCP chunk just far enough to read the
// ICC header's data colour space. The chunk is stored deflated (name\0
// method\0 profile), so the header bytes are not visible in the file. A
// profile this code cannot open yields "", which makes prepare drop it.
func pngICCColorSpace(chunk []byte) string {
	if len(chunk) < 12 || string(chunk[4:8]) != "iCCP" {
		return ""
	}
	data := chunk[8 : len(chunk)-4]
	nameEnd := bytes.IndexByte(data, 0)
	if nameEnd < 1 || nameEnd+2 > len(data) || data[nameEnd+1] != 0 {
		return ""
	}
	zr, err := zlib.NewReader(bytes.NewReader(data[nameEnd+2:]))
	if err != nil {
		return ""
	}
	defer zr.Close()
	var header [20]byte
	if _, err := io.ReadFull(zr, header[:]); err != nil {
		return ""
	}
	return iccColorSpace(header[:])
}

// readWebPMetadata walks the RIFF chunk list. The encoder writes a fresh
// container, so ICC/EXIF/XMP chunks are never copied into the output; this
// scan exists so the drop is reported (P0-F) instead of silent.
func readWebPMetadata(r io.Reader) sourceMetadata {
	var meta sourceMetadata
	header := make([]byte, 12)
	if _, err := io.ReadFull(r, header); err != nil || string(header[:4]) != "RIFF" || string(header[8:12]) != "WEBP" {
		return meta
	}
	payload := int64(binary.LittleEndian.Uint32(header[4:8])) - 4
	var pos int64
	for pos+8 <= payload {
		var head [8]byte
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return meta
		}
		id := string(head[:4])
		size := int64(binary.LittleEndian.Uint32(head[4:8]))
		pos += 8
		if size < 0 || pos+size > payload {
			return meta
		}
		padded := size + size%2
		switch id {
		case "ICCP":
			meta.hadICC = true
			if size <= 16<<20 {
				data := make([]byte, size)
				if _, err := io.ReadFull(r, data); err != nil {
					return meta
				}
				meta.webpICC = data
				meta.iccSpace = iccColorSpace(data)
				if size%2 == 1 {
					if _, err := io.CopyN(io.Discard, r, 1); err != nil {
						return meta
					}
				}
			} else if _, err := io.CopyN(io.Discard, r, padded); err != nil {
				return meta
			}
		case "EXIF", "XMP ":
			meta.hadOther = true
			if _, err := io.CopyN(io.Discard, r, padded); err != nil {
				return meta
			}
		default:
			if _, err := io.CopyN(io.Discard, r, padded); err != nil {
				return meta
			}
		}
		pos += padded
	}
	return meta
}

// readJPEGMetadata walks the marker segments before the scan data. APP1
// (Exif, XMP), APP2 (ICC_PROFILE), APP13 (IPTC/Photoshop) and COM are kept
// track of; the image data itself is never read.
func readJPEGMetadata(r io.Reader) sourceMetadata {
	var meta sourceMetadata
	br := bufio.NewReader(r)
	var so [2]byte
	if _, err := io.ReadFull(br, so[:]); err != nil || so[0] != 0xFF || so[1] != 0xD8 {
		return meta
	}
	for {
		var marker byte
		for {
			b, err := br.ReadByte()
			if err != nil {
				return meta
			}
			if b != 0xFF {
				continue
			}
			m, err := br.ReadByte()
			if err != nil {
				return meta
			}
			if m == 0xFF {
				continue
			}
			marker = m
			break
		}
		// Start of scan and end of image: no more metadata follows.
		if marker == 0xDA || marker == 0xD9 {
			return meta
		}
		// Standalone markers have no length or payload.
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			continue
		}
		var lb [2]byte
		if _, err := io.ReadFull(br, lb[:]); err != nil {
			return meta
		}
		length := int(binary.BigEndian.Uint16(lb[:]))
		if length < 2 {
			return meta
		}
		payloadLen := length - 2
		keep := marker == 0xE1 || marker == 0xE2 || marker == 0xED || marker == 0xFE
		if !keep {
			if _, err := io.CopyN(io.Discard, br, int64(payloadLen)); err != nil {
				return meta
			}
			continue
		}
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(br, payload); err != nil {
			return meta
		}
		switch marker {
		case 0xE1:
			if bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
				meta.hadEXIF = true
				meta.exif = append([]byte(nil), payload[6:]...)
			} else {
				// XMP and other APP1 payloads are not carried.
				meta.hadOther = true
			}
		case 0xE2:
			if seg, ok := parseICCSegment(payload, lb); ok {
				meta.hadICC = true
				meta.jpegICC = append(meta.jpegICC, seg)
				if seg.seq == 1 {
					meta.iccSpace = jpegICCColorSpace(seg.raw)
				}
			} else {
				meta.hadOther = true
			}
		case 0xED, 0xFE:
			meta.hadOther = true
		}
	}
}

// parseICCSegment recognises one ICC_PROFILE APP2 payload. raw keeps the
// original marker and length so the output segment is byte-identical.
func parseICCSegment(payload []byte, length [2]byte) (jpegICCSegment, bool) {
	const id = "ICC_PROFILE\x00"
	if len(payload) < len(id)+2 || !bytes.HasPrefix(payload, []byte(id)) {
		return jpegICCSegment{}, false
	}
	raw := make([]byte, 0, 4+len(payload))
	raw = append(raw, 0xFF, 0xE2)
	raw = append(raw, length[:]...)
	raw = append(raw, payload...)
	return jpegICCSegment{
		seq:   int(payload[len(id)]),
		total: int(payload[len(id)+1]),
		raw:   raw,
	}, true
}

// jpegICCColorSpace reads the ICC header from the first APP2 segment: marker
// (2) + length (2) + "ICC_PROFILE\0" (12) + sequence (1) + total (1), then
// the profile itself.
func jpegICCColorSpace(raw []byte) string {
	const profileStart = 4 + 12 + 2
	if len(raw) < profileStart+20 {
		return ""
	}
	return iccColorSpace(raw[profileStart : profileStart+20])
}

// iccColorSpace reads the data colour space signature at bytes 16–20 of an
// ICC profile header. An unrecognised space is returned trimmed so the report
// can still name it; a missing header returns "".
func iccColorSpace(profile []byte) string {
	if len(profile) < 20 {
		return ""
	}
	switch string(profile[16:20]) {
	case "RGB ":
		return "RGB"
	case "GRAY":
		return "GRAY"
	case "CMYK":
		return "CMYK"
	}
	return strings.TrimSpace(string(profile[16:20]))
}

// outputColorSpace names the colour space the encoder will write for img. It
// must mirror what the encoders actually produce: only a grayscale image
// stays grayscale; image/png keeps paletted images in RGB, and every other
// model goes through the 8-bit RGB/RGBA path (png_lossless.go) or YCbCr
// (image/jpeg). A profile is carried only when its space equals this one.
func outputColorSpace(outFormat string, img image.Image) string {
	switch outFormat {
	case "PNG":
		switch img.(type) {
		case *image.Gray, *image.Gray16:
			return "GRAY"
		}
		return "RGB"
	case "JPEG":
		// image/jpeg writes single-channel data only for *image.Gray.
		if _, ok := img.(*image.Gray); ok {
			return "GRAY"
		}
		return "RGB"
	case "WEBP":
		return "RGB"
	}
	return ""
}

// prepare decides what the output format can keep from the source. Only
// same-format re-encodes copy the source's representation; converting
// between PNG and JPEG would need a colour-space-aware profile rewrite, and
// silently inventing one is worse than reporting the drop.
//
// outputSpace is the colour space the encoded pixels will carry. An ICC
// profile whose own space differs is dropped and reported: attaching a CMYK
// profile to 3-channel YCbCr writes a file colour-managed viewers cannot
// render (P0-B), and the same rule covers grayscale sources carrying an RGB
// profile.
func (m sourceMetadata) prepare(outFormat, srcFormat, outputSpace string) (stillMetadata, *MetadataReport) {
	var out stillMetadata
	report := MetadataReport{}
	same := strings.EqualFold(outFormat, srcFormat)
	iccCompatible := m.iccSpace != "" && strings.EqualFold(m.iccSpace, outputSpace)

	switch {
	case outFormat == "PNG" && same:
		if iccCompatible {
			out.pngICCChunk = m.pngICCChunk
			report.ICCKept = len(out.pngICCChunk) > 0
		}
	case outFormat == "JPEG" && same:
		if len(m.exif) > 0 {
			// An EXIF block we cannot parse is dropped whole: keeping it
			// verbatim could carry a GPS directory we failed to empty.
			if sanitized, gps, orientation, ok := sanitizeEXIF(m.exif); ok && len(sanitized)+10 <= 0xFFFF {
				out.exif = sanitized
				report.EXIFKept = true
				report.GPSRemoved = gps
				report.OrientationReset = orientation
			}
		}
		if segments, ok := completeICC(m.jpegICC); ok && iccCompatible {
			out.jpegSegments = segments
			report.ICCKept = len(segments) > 0
		}
	case outFormat == "WEBP":
		// The encoder writes a fresh RIFF container and AnyPic does not
		// rewrite metadata chunks into it, so a source ICC cannot travel
		// (P0-F). It is reported below instead of disappearing silently.
	}

	if m.hadICC && !report.ICCKept {
		report.Dropped = true
		if outFormat == "WEBP" {
			report.ICCDroppedUnsupported = true
		} else if m.iccSpace != "" && !iccCompatible {
			report.ICCDroppedIncompatible = true
			report.ICCColorSpace = m.iccSpace
		}
	}
	if !report.EXIFKept && m.hadEXIF {
		report.Dropped = true
	}
	if m.hadOther {
		report.Dropped = true
	}
	if !report.ICCKept && !report.EXIFKept && !report.GPSRemoved && !report.Dropped {
		return out, nil
	}
	return out, &report
}

// completeICC returns the ICC segments in sequence order, or nothing when
// the set is incomplete. A missing segment would produce a profile that
// colour-managed apps reject or, worse, misread.
func completeICC(segments []jpegICCSegment) ([][]byte, bool) {
	if len(segments) == 0 {
		return nil, false
	}
	total := segments[0].total
	if total < 1 || total != len(segments) {
		return nil, false
	}
	ordered := make([][]byte, total)
	for _, seg := range segments {
		if seg.total != total || seg.seq < 1 || seg.seq > total || ordered[seg.seq-1] != nil {
			return nil, false
		}
		ordered[seg.seq-1] = seg.raw
	}
	for _, raw := range ordered {
		if raw == nil {
			return nil, false
		}
	}
	return ordered, true
}

// size is how many bytes apply will add to the encoded file. The target-size
// search subtracts it from the cap before probing qualities.
func (m stillMetadata) size() int {
	n := len(m.pngICCChunk)
	if len(m.exif) > 0 {
		// marker (2) + length (2) + "Exif\0\0" (6)
		n += 10 + len(m.exif)
	}
	for _, segment := range m.jpegSegments {
		n += len(segment)
	}
	return n
}

// apply inserts the prepared metadata into an encoded PNG or JPEG. On an
// unexpected container it returns the data untouched rather than corrupting
// it; the caller's size and pixel checks still run on what is returned.
func (m stillMetadata) apply(data []byte) []byte {
	switch {
	case len(m.pngICCChunk) > 0:
		return insertPNGChunk(data, m.pngICCChunk)
	case len(m.exif) > 0 || len(m.jpegSegments) > 0:
		segments := make([][]byte, 0, 1+len(m.jpegSegments))
		if len(m.exif) > 0 {
			segment := make([]byte, 0, 10+len(m.exif))
			segment = append(segment, 0xFF, 0xE1, 0x00, 0x00)
			binary.BigEndian.PutUint16(segment[2:4], uint16(8+len(m.exif)))
			segment = append(segment, "Exif\x00\x00"...)
			segment = append(segment, m.exif...)
			segments = append(segments, segment)
		}
		segments = append(segments, m.jpegSegments...)
		return insertJPEGSegments(data, segments)
	}
	return data
}

// insertPNGChunk puts chunk directly after IHDR, where iCCP is allowed and
// before every IDAT.
func insertPNGChunk(data, chunk []byte) []byte {
	// signature (8) + IHDR length/type/data/CRC (25)
	const afterIHDR = 33
	if len(data) < afterIHDR || !bytes.Equal(data[:8], pngSignature) || string(data[12:16]) != "IHDR" {
		return data
	}
	out := make([]byte, 0, len(data)+len(chunk))
	out = append(out, data[:afterIHDR]...)
	out = append(out, chunk...)
	out = append(out, data[afterIHDR:]...)
	return out
}

// insertJPEGSegments inserts APP segments after SOI, keeping a leading JFIF
// APP0 first.
func insertJPEGSegments(data []byte, segments [][]byte) []byte {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return data
	}
	pos := 2
	if len(data) >= 6 && data[pos] == 0xFF && data[pos+1] == 0xE0 {
		length := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		if length >= 2 && pos+2+length <= len(data) {
			pos += 2 + length
		}
	}
	extra := 0
	for _, segment := range segments {
		extra += len(segment)
	}
	out := make([]byte, 0, len(data)+extra)
	out = append(out, data[:pos]...)
	for _, segment := range segments {
		out = append(out, segment...)
	}
	out = append(out, data[pos:]...)
	return out
}

// sanitizeEXIF copies an EXIF payload (TIFF bytes, without the "Exif\0\0"
// prefix) and edits it in place:
//
//   - Orientation (0x0112) becomes 1, because the pixels were already
//     physically rotated by AutoOrientation; keeping the original value would
//     rotate them a second time in viewers.
//   - The GPS directory (0x8825) is emptied: its entries and any out-of-line
//     value bytes are zeroed, leaving a valid empty IFD. That removes the
//     coordinates from the file instead of merely unreferencing them.
//
// ok is false when the payload is not a TIFF/EXIF structure we understand;
// the caller then drops EXIF rather than carrying a GPS block it could not
// clean.
func sanitizeEXIF(exif []byte) (out []byte, gpsRemoved, orientationReset, ok bool) {
	if len(exif) < 8 {
		return nil, false, false, false
	}
	var order binary.ByteOrder
	switch string(exif[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return nil, false, false, false
	}
	if order.Uint16(exif[2:4]) != 42 {
		return nil, false, false, false
	}
	ifd0 := int(order.Uint32(exif[4:8]))
	entries, _, ok := readTIFFIFD(exif, ifd0, order)
	if !ok {
		return nil, false, false, false
	}

	out = append([]byte(nil), exif...)
	for _, entry := range entries {
		if entry.tag != 0x0112 || entry.typ != 3 || entry.count < 1 {
			continue
		}
		if order.Uint16(out[entry.offset+8:entry.offset+10]) != 1 {
			order.PutUint16(out[entry.offset+8:entry.offset+10], 1)
			orientationReset = true
		}
	}
	for _, entry := range entries {
		if entry.tag != 0x8825 || entry.typ != 4 || entry.count != 1 {
			continue
		}
		gps := int(order.Uint32(out[entry.offset+8 : entry.offset+12]))
		if !zeroGPSIFD(out, gps, order) {
			return nil, false, false, false
		}
		gpsRemoved = true
	}
	return out, gpsRemoved, orientationReset, true
}

type tiffEntry struct {
	tag    uint16
	typ    uint16
	count  uint32
	offset int
}

// readTIFFIFD reads one image file directory. offset is where the entry
// count starts; the returned int is the next IFD's offset (0 when none).
func readTIFFIFD(b []byte, offset int, order binary.ByteOrder) ([]tiffEntry, int, bool) {
	if offset < 0 || offset+2 > len(b) {
		return nil, 0, false
	}
	count := int(order.Uint16(b[offset : offset+2]))
	tableEnd := offset + 2 + 12*count + 4
	if tableEnd > len(b) {
		return nil, 0, false
	}
	entries := make([]tiffEntry, 0, count)
	for i := 0; i < count; i++ {
		p := offset + 2 + 12*i
		entries = append(entries, tiffEntry{
			tag:    order.Uint16(b[p : p+2]),
			typ:    order.Uint16(b[p+2 : p+4]),
			count:  order.Uint32(b[p+4 : p+8]),
			offset: p,
		})
	}
	next := int(order.Uint32(b[offset+2+12*count : tableEnd]))
	return entries, next, true
}

// zeroGPSIFD empties a GPS directory in place. Entries with values longer
// than four bytes keep those values elsewhere; those ranges are zeroed too,
// so no coordinate bytes survive in the payload. The directory itself keeps
// its offset (IFD0 still points at it), which leaves a valid empty IFD
// instead of a dangling pointer.
func zeroGPSIFD(b []byte, offset int, order binary.ByteOrder) bool {
	entries, _, ok := readTIFFIFD(b, offset, order)
	if !ok {
		return false
	}
	tableEnd := offset + 2 + 12*len(entries) + 4
	for _, entry := range entries {
		unit := tiffTypeSize(entry.typ)
		if unit == 0 {
			continue
		}
		size := int64(unit) * int64(entry.count)
		if size <= 4 || size > int64(len(b)) {
			continue
		}
		start := int(order.Uint32(b[entry.offset+8 : entry.offset+12]))
		end := start + int(size)
		if start < 0 || end > len(b) || end < start {
			continue
		}
		// The table is zeroed below anyway; skip ranges that overlap it.
		if start < tableEnd && offset < end {
			continue
		}
		for i := start; i < end; i++ {
			b[i] = 0
		}
	}
	for i := offset; i < tableEnd; i++ {
		b[i] = 0
	}
	return true
}

func tiffTypeSize(typ uint16) int {
	switch typ {
	case 1, 2, 6, 7: // BYTE, ASCII, SBYTE, UNDEFINED
		return 1
	case 3, 8: // SHORT, SSHORT
		return 2
	case 4, 9, 11: // LONG, SLONG, FLOAT
		return 4
	case 5, 10, 12: // RATIONAL, SRATIONAL, DOUBLE
		return 8
	}
	return 0
}

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
