package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/* ------------------------------------------------------------------ */
/* fixture builders                                                    */
/* ------------------------------------------------------------------ */

func makePNGChunk(typ string, data []byte) []byte {
	chunk := make([]byte, 8+len(data)+4)
	binary.BigEndian.PutUint32(chunk[0:4], uint32(len(data)))
	copy(chunk[4:8], typ)
	copy(chunk[8:], data)
	crc := crc32.ChecksumIEEE(chunk[4 : 8+len(data)])
	binary.BigEndian.PutUint32(chunk[8+len(data):], crc)
	return chunk
}

func makeICCPChunk(t *testing.T, profile []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write(profile); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 0, 10+compressed.Len())
	data = append(data, "AnyPicTest\x00\x00"...)
	data = append(data, compressed.Bytes()...)
	return makePNGChunk("iCCP", data)
}

func makeJPEGSegment(marker byte, payload []byte) []byte {
	segment := make([]byte, 4+len(payload))
	segment[0], segment[1] = 0xFF, marker
	binary.BigEndian.PutUint16(segment[2:4], uint16(2+len(payload)))
	copy(segment[4:], payload)
	return segment
}

func makeICCSegments(profile []byte, parts int) [][]byte {
	segments := make([][]byte, 0, parts)
	chunk := (len(profile) + parts - 1) / parts
	for i := 0; i < parts; i++ {
		start := i * chunk
		end := min(start+chunk, len(profile))
		payload := make([]byte, 0, 14+end-start)
		payload = append(payload, "ICC_PROFILE\x00"...)
		payload = append(payload, byte(i+1), byte(parts))
		payload = append(payload, profile[start:end]...)
		segments = append(segments, makeJPEGSegment(0xE2, payload))
	}
	return segments
}

// buildEXIF writes a little-endian TIFF block with Make, Orientation and a
// GPS directory whose latitude is stored out of line. It returns the TIFF
// payload and the raw GPS rational bytes so a test can prove they are gone.
func buildEXIF(orientation uint16, makeName string, latitude [3][2]uint32) ([]byte, []byte) {
	const (
		ifd0Offset = 8
		entries    = 3
	)
	ifd0End := ifd0Offset + 2 + entries*12 + 4
	makeOffset := ifd0End
	makeBytes := append([]byte(makeName), 0)
	gpsOffset := makeOffset + len(makeBytes)
	gpsEntries := 2
	gpsTableEnd := gpsOffset + 2 + gpsEntries*12 + 4
	valuesOffset := gpsTableEnd
	buf := make([]byte, valuesOffset+3*8)

	copy(buf[0:2], "II")
	binary.LittleEndian.PutUint16(buf[2:4], 42)
	binary.LittleEndian.PutUint32(buf[4:8], ifd0Offset)

	p := ifd0Offset
	binary.LittleEndian.PutUint16(buf[p:p+2], entries)
	putTIFFEntry(buf[p+2:], 0x0112, 3, 1, 0)
	binary.LittleEndian.PutUint16(buf[p+2+8:p+2+10], orientation)
	putTIFFEntry(buf[p+14:], 0x010F, 2, uint32(len(makeBytes)), uint32(makeOffset))
	putTIFFEntry(buf[p+26:], 0x8825, 4, 1, uint32(gpsOffset))
	binary.LittleEndian.PutUint32(buf[p+2+entries*12:], 0)
	copy(buf[makeOffset:], makeBytes)

	p = gpsOffset
	binary.LittleEndian.PutUint16(buf[p:p+2], uint16(gpsEntries))
	putTIFFEntry(buf[p+2:], 0x0001, 2, 2, 0)
	copy(buf[p+2+8:p+2+12], []byte{'N', 0, 0, 0})
	putTIFFEntry(buf[p+14:], 0x0002, 5, 3, uint32(valuesOffset))
	binary.LittleEndian.PutUint32(buf[p+2+gpsEntries*12:], 0)

	values := buf[valuesOffset:]
	for i, rational := range latitude {
		binary.LittleEndian.PutUint32(values[i*8:], rational[0])
		binary.LittleEndian.PutUint32(values[i*8+4:], rational[1])
	}
	return buf, append([]byte(nil), values...)
}

func putTIFFEntry(dst []byte, tag, typ uint16, count, value uint32) {
	binary.LittleEndian.PutUint16(dst[0:2], tag)
	binary.LittleEndian.PutUint16(dst[2:4], typ)
	binary.LittleEndian.PutUint32(dst[4:8], count)
	binary.LittleEndian.PutUint32(dst[8:12], value)
}

func writePNGWithICC(t *testing.T, path string, img image.Image, profile []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	chunk := makeICCPChunk(t, profile)
	data := insertPNGChunk(buf.Bytes(), chunk)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return chunk
}

func writeJPEGWithEXIFAndICC(t *testing.T, path string, img image.Image, quality int, orientation uint16, profile []byte) ([]byte, [][]byte, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	exif, gpsValues := buildEXIF(orientation, "AnyPicCam", [3][2]uint32{{37, 1}, {46, 1}, {1234, 100}})
	app1 := makeJPEGSegment(0xE1, append([]byte("Exif\x00\x00"), exif...))
	icc := makeICCSegments(profile, 2)
	segments := append([][]byte{app1}, icc...)
	data := insertJPEGSegments(buf.Bytes(), segments)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return app1, icc, gpsValues
}

/* ------------------------------------------------------------------ */
/* readers for assertions                                              */
/* ------------------------------------------------------------------ */

type rawJPEGSegment struct {
	marker  byte
	payload []byte
}

func readJPEGSegments(t *testing.T, data []byte) []rawJPEGSegment {
	t.Helper()
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		t.Fatal("not a JPEG")
	}
	var out []rawJPEGSegment
	for pos := 2; pos+1 < len(data); {
		if data[pos] != 0xFF {
			pos++
			continue
		}
		marker := data[pos+1]
		if marker == 0xD8 || marker == 0xD9 || marker == 0xDA || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			if marker == 0xDA || marker == 0xD9 {
				break
			}
			pos += 2
			continue
		}
		if pos+4 > len(data) {
			break
		}
		length := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		if length < 2 || pos+2+length > len(data) {
			t.Fatalf("bad JPEG segment at %d", pos)
		}
		payload := data[pos+4 : pos+2+length]
		if marker == 0xE1 || marker == 0xE2 {
			out = append(out, rawJPEGSegment{marker: marker, payload: payload})
		}
		pos += 2 + length
	}
	return out
}

func readPNGChunks(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	if !bytes.HasPrefix(data, pngSignature) {
		t.Fatal("not a PNG")
	}
	out := map[string][]byte{}
	for pos := 8; pos+12 <= len(data); {
		length := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		typ := string(data[pos+4 : pos+8])
		if pos+12+length > len(data) {
			t.Fatalf("bad PNG chunk %q", typ)
		}
		out[typ] = append([]byte(nil), data[pos:pos+12+length]...)
		if typ == "IEND" {
			break
		}
		pos += 12 + length
	}
	return out
}

func tiffOrientation(t *testing.T, exif []byte) uint16 {
	t.Helper()
	entries, _, ok := readTIFFIFD(exif, int(binary.LittleEndian.Uint32(exif[4:8])), binary.LittleEndian)
	if !ok {
		t.Fatal("cannot read output EXIF IFD0")
	}
	for _, entry := range entries {
		if entry.tag == 0x0112 {
			return binary.LittleEndian.Uint16(exif[entry.offset+8:])
		}
	}
	t.Fatal("output EXIF has no Orientation")
	return 0
}

/* ------------------------------------------------------------------ */
/* tests                                                               */
/* ------------------------------------------------------------------ */

func TestPNGICCProfileSurvivesReencode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "shot.png")
	profile := make([]byte, 512)
	for i := range profile {
		profile[i] = byte(i * 7)
	}
	sourceChunk := writePNGWithICC(t, src, gradientImage(320, 240, false), profile)

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 1, src, "PNG", "shot.png", false, Settings{
		Format: "png", Quality: 82, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.passedThrough {
		t.Fatal("BestSpeed source should be beaten by a BestCompression re-encode")
	}
	if result.metadata == nil || !result.metadata.ICCKept {
		t.Fatalf("metadata = %+v, want ICCKept", result.metadata)
	}
	data, err := os.ReadFile(result.path)
	if err != nil {
		t.Fatal(err)
	}
	chunks := readPNGChunks(t, data)
	out, ok := chunks["iCCP"]
	if !ok {
		t.Fatal("output PNG has no iCCP chunk")
	}
	if !bytes.Equal(out, sourceChunk) {
		t.Fatal("iCCP chunk changed: the profile must be copied byte-for-byte")
	}
}

func TestJPEGMetadataSelective(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	profile := make([]byte, 700)
	for i := range profile {
		profile[i] = byte(255 - i)
	}
	_, sourceICC, gpsValues := writeJPEGWithEXIFAndICC(t, src, gradientImage(640, 480, false), 95, 6, profile)

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 2, src, "JPEG", "photo.jpg", false, Settings{
		Format: "jpeg", Quality: 50, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.passedThrough {
		t.Fatal("q50 re-encode should beat a q95 source")
	}
	if result.metadata == nil {
		t.Fatal("source had ICC and EXIF, output must report metadata")
	}
	m := result.metadata
	if !m.ICCKept || !m.EXIFKept || !m.GPSRemoved || !m.OrientationReset {
		t.Fatalf("metadata = %+v, want ICC kept, EXIF kept, GPS removed, orientation reset", m)
	}
	if m.Dropped {
		t.Fatalf("metadata = %+v, nothing should have been dropped", m)
	}

	data, err := os.ReadFile(result.path)
	if err != nil {
		t.Fatal(err)
	}
	segments := readJPEGSegments(t, data)
	var outICC [][]byte
	var outEXIF []byte
	for _, segment := range segments {
		switch segment.marker {
		case 0xE1:
			if bytes.HasPrefix(segment.payload, []byte("Exif\x00\x00")) {
				outEXIF = segment.payload[6:]
			}
		case 0xE2:
			if bytes.HasPrefix(segment.payload, []byte("ICC_PROFILE\x00")) {
				outICC = append(outICC, makeJPEGSegment(0xE2, segment.payload))
			}
		}
	}
	if len(outICC) != len(sourceICC) {
		t.Fatalf("output has %d ICC segments, want %d", len(outICC), len(sourceICC))
	}
	var joined []byte
	for i := range outICC {
		if !bytes.Equal(outICC[i], sourceICC[i]) {
			t.Fatalf("ICC segment %d changed", i+1)
		}
		joined = append(joined, outICC[i][4+14:]...)
	}
	if !bytes.Equal(joined, profile) {
		t.Fatal("reassembled ICC profile differs from the source profile")
	}
	if outEXIF == nil {
		t.Fatal("output JPEG has no EXIF")
	}
	if got := tiffOrientation(t, outEXIF); got != 1 {
		t.Fatalf("output Orientation = %d, want 1 (pixels are already rotated)", got)
	}
	if !bytes.Contains(outEXIF, []byte("AnyPicCam\x00")) {
		t.Fatal("EXIF Make was not preserved")
	}
	entries, _, ok := readTIFFIFD(outEXIF, int(binary.LittleEndian.Uint32(outEXIF[4:8])), binary.LittleEndian)
	if !ok {
		t.Fatal("cannot read output IFD0")
	}
	gpsFound := false
	for _, entry := range entries {
		if entry.tag != 0x8825 {
			continue
		}
		gpsFound = true
		gps := int(binary.LittleEndian.Uint32(outEXIF[entry.offset+8:]))
		gpsEntries, _, ok := readTIFFIFD(outEXIF, gps, binary.LittleEndian)
		if !ok || len(gpsEntries) != 0 {
			t.Fatalf("output GPS directory still has %d entries", len(gpsEntries))
		}
	}
	if !gpsFound {
		t.Fatal("output lost the GPS directory pointer entirely; the empty IFD is the intended shape")
	}
	if bytes.Contains(outEXIF, gpsValues) {
		t.Fatal("GPS rational bytes survived in the output EXIF")
	}
}

func TestJPEGIncompleteICCPIsDropped(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, gradientImage(640, 480, false), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	profile := make([]byte, 400)
	segments := makeICCSegments(profile, 2)[:1] // total says 2, only 1 present
	if err := os.WriteFile(src, insertJPEGSegments(buf.Bytes(), segments), 0o644); err != nil {
		t.Fatal(err)
	}

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 3, src, "JPEG", "photo.jpg", false, Settings{
		Format: "jpeg", Quality: 50, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.metadata == nil || result.metadata.ICCKept {
		t.Fatalf("metadata = %+v, incomplete ICC must not be reported as kept", result.metadata)
	}
	if result.metadata == nil || !result.metadata.Dropped {
		t.Fatalf("metadata = %+v, incomplete ICC must be reported as dropped", result.metadata)
	}
	data, err := os.ReadFile(result.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, segment := range readJPEGSegments(t, data) {
		if segment.marker == 0xE2 && bytes.HasPrefix(segment.payload, []byte("ICC_PROFILE\x00")) {
			t.Fatal("output carries an incomplete ICC profile")
		}
	}
}

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

// TestRealScreenshotKeepsItsP3Profile is the reproduction from the report:
// AnyPic's own screenshot carries a 512-byte Display P3 iCCP chunk. A
// re-encode forced by MaxDimension must still carry the same chunk; without
// the metadata pass, png.Encode writes IHDR/IDAT/IEND only and the profile
// disappears.
func TestRealScreenshotKeepsItsP3Profile(t *testing.T) {
	src := filepath.Join(benchmarkDir, "screenshot-real.png")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	source := readPNGChunks(t, raw)
	want, ok := source["iCCP"]
	if !ok {
		t.Fatal("benchmark screenshot-real.png no longer carries an iCCP chunk; the fixture changed")
	}

	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 8, src, "PNG", "screenshot-real.png", false, Settings{
		Format: "png", Quality: 82, MaxDimension: 1000, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.passedThrough {
		t.Fatal("MaxDimension forces a re-encode; passthrough means the test is not exercising the metadata path")
	}
	if result.metadata == nil || !result.metadata.ICCKept {
		t.Fatalf("metadata = %+v, want ICCKept", result.metadata)
	}
	data, err := os.ReadFile(result.path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := readPNGChunks(t, data)["iCCP"]
	if !ok {
		t.Fatal("re-encoded screenshot lost its iCCP profile")
	}
	if !bytes.Equal(want, got) {
		t.Fatal("re-encoded screenshot's iCCP chunk differs from the source")
	}
}

// TestReportedFalseOptimalFilesAreNowHonest pins the report's two examples.
// Both used to be labeled “已是最优” because the stdlib encoder could not beat
// them. logo.png (54,102 bytes) still cannot be beaten even by the §5③
// filter search, so it passes through with the honest reason not_smaller.
// screenshot-real.png (111,106 bytes) is now actually compressed, which makes
// the old claim doubly wrong: the original had headroom all along.
func TestReportedFalseOptimalFilesAreNowHonest(t *testing.T) {
	t.Run("logo.png", func(t *testing.T) {
		src := filepath.Join(benchmarkDir, "logo.png")
		service := &AnyPic{tempDir: t.TempDir()}
		result, err := service.compressOne(context.Background(), 9, src, "PNG", "logo.png", false, Settings{
			Format: "png", Quality: 82, Suffix: "-anypic",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.passedThrough {
			t.Fatalf("expected even the filter search to lose to the original, got %d bytes", result.size)
		}
		if result.keptOriginalReason != "not_smaller" {
			t.Fatalf("reason = %q, want not_smaller — the UI must not claim the original is already optimal", result.keptOriginalReason)
		}
		if result.path != src {
			t.Fatalf("passthrough path = %q, want the original", result.path)
		}
		if result.metadata != nil {
			t.Fatalf("passthrough is the untouched original, so it has no metadata report: %+v", result.metadata)
		}
	})

	t.Run("screenshot-real.png", func(t *testing.T) {
		src := filepath.Join(benchmarkDir, "screenshot-real.png")
		info, err := os.Stat(src)
		if err != nil {
			t.Fatal(err)
		}
		service := &AnyPic{tempDir: t.TempDir()}
		result, err := service.compressOne(context.Background(), 10, src, "PNG", "screenshot-real.png", false, Settings{
			Format: "png", Quality: 82, Suffix: "-anypic",
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.passedThrough {
			t.Fatalf("filter search should now beat the %d-byte original; got passthrough (%s)", info.Size(), result.keptOriginalReason)
		}
		if result.size >= info.Size() {
			t.Fatalf("result is %d bytes, not smaller than the %d-byte original", result.size, info.Size())
		}
		if result.metadata == nil || !result.metadata.ICCKept {
			t.Fatalf("metadata = %+v, want the real screenshot's ICC profile kept", result.metadata)
		}
	})
}

// TestLosslessGuardAcceptsJPEGToPNG keeps the guard from becoming a false
// alarm: a requested PNG output from a JPEG source is a lossless
// representation of the decoded pixels, so the baseline encoder must pass the
// pixel-for-pixel check. (YCbCr is not 8-bit RGB, so encodePNGLossless keeps
// the standard-library 16-bit output instead of the filter search.)
func TestLosslessGuardAcceptsJPEGToPNG(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	writeJPEG(t, src, gradientImage(320, 240, false), 85)
	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 10, src, "JPEG", "photo.jpg", false, Settings{
		Format: "png", Quality: 82, Suffix: "-anypic",
	})
	if err != nil {
		t.Fatalf("JPEG -> PNG was rejected by the lossless guard: %v", err)
	}
	if result.format != "PNG" || result.passedThrough {
		t.Fatalf("format = %s passedThrough = %v, want a PNG re-encode", result.format, result.passedThrough)
	}
}

// TestJPEGTargetBudgetIncludesMetadata keeps a result that fits the cap
// before its ICC profile is written back from overshooting it afterwards.
// The search must budget for the bytes the metadata pass adds.
func TestJPEGTargetBudgetIncludesMetadata(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	profile := make([]byte, 24<<10) // large enough that ignoring it would overshoot
	for i := range profile {
		profile[i] = byte(i)
	}
	writeJPEGWithEXIFAndICC(t, src, gradientImage(640, 480, false), 95, 1, profile)

	const target = 40 << 10
	service := &AnyPic{tempDir: t.TempDir()}
	result, err := service.compressOne(context.Background(), 11, src, "JPEG", "photo.jpg", false, Settings{
		Format: "jpeg", Quality: 82, Suffix: "-anypic", TargetSize: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.passedThrough {
		t.Fatal("q95 source is larger than 40 KB; the target pass must re-encode")
	}
	if result.metadata == nil || !result.metadata.ICCKept {
		t.Fatalf("metadata = %+v, want ICCKept", result.metadata)
	}
	if result.targetMissed {
		t.Fatalf("target %d was reported unreachable; the test needs a reachable cap to prove the budget", target)
	}
	data, err := os.ReadFile(result.path)
	if err != nil {
		t.Fatal(err)
	}
	if !result.targetMissed && int64(len(data)) > target {
		t.Fatalf("result is %d bytes including metadata, over the %d-byte cap it claims to meet", len(data), target)
	}
}
