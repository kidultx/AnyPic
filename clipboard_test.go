package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type clipboardStub struct {
	read     []byte
	readText string
	writes   [][]byte
	text     string
	cleared  bool
	// activeLease is the token the fake clipboard currently considers its own
	// write; 0 means another app took the clipboard. nextLease numbers writes.
	activeLease uint64
	nextLease   uint64
	writeErr    error
}

// stubClipboard replaces every desktop clipboard seam for one test, so
// nothing touches the developer's real clipboard. The fake clipboard models
// ownership the way MyGo does: only the most recent write's lease is valid.
func stubClipboard(t *testing.T) *clipboardStub {
	t.Helper()
	stub := &clipboardStub{}
	readState := clipboardReadState
	readImage := clipboardReadImage
	writeImage := clipboardWriteImage
	leaseValid := clipboardLeaseValid
	writeText := clipboardWriteText
	clear := clipboardClear
	clipboardReadState = func() clipboardState {
		return clipboardState{image: stub.read, text: stub.readText}
	}
	clipboardReadImage = func() []byte { return stub.read }
	clipboardWriteImage = func(data []byte) (uint64, error) {
		if stub.writeErr != nil {
			return 0, stub.writeErr
		}
		stub.writes = append(stub.writes, append([]byte(nil), data...))
		stub.nextLease++
		stub.activeLease = stub.nextLease
		return stub.nextLease, nil
	}
	clipboardLeaseValid = func(token uint64) bool {
		return token != 0 && stub.activeLease == token
	}
	clipboardWriteText = func(text string) { stub.text = text }
	clipboardClear = func() { stub.cleared = true }
	t.Cleanup(func() {
		clipboardReadState = readState
		clipboardReadImage = readImage
		clipboardWriteImage = writeImage
		clipboardLeaseValid = leaseValid
		clipboardWriteText = writeText
		clipboardClear = clear
	})
	return stub
}

// clipboardFixture builds a finished queue item whose compressed result is a
// WebP in the session temp dir, plus the PNG bytes its capture would have put
// on the clipboard.
func clipboardFixture(t *testing.T) (*AnyPic, *Item, []byte) {
	t.Helper()
	service := newTestService(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "my photo (1).png")
	src := gradientImage(120, 90, false)
	original := writePNG(t, srcPath, src)
	data, err := encodeStill(src, "WEBP", 80)
	if err != nil {
		t.Fatal(err)
	}
	compPath := filepath.Join(service.tempDir, "1-result.webp")
	if err := os.WriteFile(compPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	item := &Item{
		ID:             1,
		Name:           "my photo (1).png",
		Path:           srcPath,
		Dir:            dir,
		Format:         "PNG",
		Width:          120,
		Height:         90,
		OriginalSize:   original,
		CompressedSize: int64(len(data)),
		Status:         statusDone,
		OutputFormat:   "WEBP",
		OutputName:     "my photo (1)-anypic.webp",
		compressedPath: compPath,
	}
	service.items = []*Item{item}
	service.byID = map[int64]*Item{item.ID: item}
	service.lastResultID = item.ID
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	return service, item, raw
}

// reencodePNG returns PNG bytes for the same pixels through a different
// encoder setting, the way Windows (CF_DIB) and Linux return a re-encoded
// image rather than the exact bytes AnyPic wrote.
func reencodePNG(t *testing.T, raw []byte) []byte {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(buf.Bytes(), raw) {
		t.Fatal("fixture re-encode produced identical bytes")
	}
	return buf.Bytes()
}

func otherClipboardPNG(t *testing.T, width, height int, alpha bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, gradientImage(width, height, alpha)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCopyCaptureResultReplacesAnyPicsOwnClipboardImage(t *testing.T) {
	service, item, raw := clipboardFixture(t)
	stub := stubClipboard(t)
	stub.read = raw
	stub.activeLease = 1
	service.rememberClipboardImage(item.Path, raw, 1)

	outcome, err := service.CopyCaptureResult(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Copied || outcome.Skipped {
		t.Fatalf("outcome = %+v, want copied", outcome)
	}
	if outcome.Name != item.OutputName {
		t.Fatalf("name = %q, want %q", outcome.Name, item.OutputName)
	}
	if len(stub.writes) != 1 {
		t.Fatalf("clipboard writes = %d, want 1", len(stub.writes))
	}
	if !bytes.HasPrefix(stub.writes[0], pngSignature) {
		t.Fatal("the clipboard received something that is not a PNG")
	}
}

// TestCopyCaptureResultSurvivesPlatformReEncoding: the bytes the platform
// hands back after its own round-trip may be a different PNG container, but
// the decoded pixels still match the remembered capture; the live lease
// confirms that write was not replaced. Identity comes from the pixel digest,
// not from the container bytes or a lease fallback.
func TestCopyCaptureResultSurvivesPlatformReEncoding(t *testing.T) {
	service, item, raw := clipboardFixture(t)
	stub := stubClipboard(t)
	stub.read = reencodePNG(t, raw)
	stub.activeLease = 3
	service.rememberClipboardImage(item.Path, raw, 3)

	outcome, err := service.CopyCaptureResult(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Copied {
		t.Fatalf("outcome = %+v, want copied despite platform re-encoding", outcome)
	}
	if len(stub.writes) != 1 {
		t.Fatalf("clipboard writes = %d, want 1", len(stub.writes))
	}
}

// TestCopyCaptureResultKeepsTextCopiedDuringCompression is the timing
// guarantee: text copied while compression runs is never overwritten, even
// though the capture's ownership lease may not have been released yet.
func TestCopyCaptureResultKeepsTextCopiedDuringCompression(t *testing.T) {
	service, item, raw := clipboardFixture(t)
	stub := stubClipboard(t)
	stub.read = nil
	stub.readText = "notes the user copied during compression"
	// The lease is still live here; text must be kept because of the content
	// check, not because ownership happened to have been released.
	stub.activeLease = 1
	service.rememberClipboardImage(item.Path, raw, 1)

	outcome, err := service.CopyCaptureResult(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Copied || !outcome.Skipped {
		t.Fatalf("outcome = %+v, want skipped", outcome)
	}
	if len(stub.writes) != 0 {
		t.Fatalf("clipboard writes = %d, want 0", len(stub.writes))
	}
}

// TestCopyCaptureResultLeavesAnEmptyClipboardAlone: an empty clipboard is
// not proof that the capture is still there, so it is never filled.
func TestCopyCaptureResultLeavesAnEmptyClipboardAlone(t *testing.T) {
	service, item, raw := clipboardFixture(t)
	stub := stubClipboard(t)
	stub.read = nil
	stub.readText = ""
	// A live lease must not be enough on its own: the capture is only still on
	// the clipboard if something is actually there.
	stub.activeLease = 1
	service.rememberClipboardImage(item.Path, raw, 1)

	outcome, err := service.CopyCaptureResult(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Skipped || outcome.Copied {
		t.Fatalf("outcome = %+v, want skipped", outcome)
	}
	if len(stub.writes) != 0 {
		t.Fatalf("clipboard writes = %d, want 0", len(stub.writes))
	}
}

// TestCopyCaptureResultKeepsANewerImageEvenWithALiveLease: platform release
// callbacks can lag (Windows notices ownership changes on a later native
// call), so a stale lease must never override a pixel mismatch.
func TestCopyCaptureResultKeepsANewerImageEvenWithALiveLease(t *testing.T) {
	service, item, raw := clipboardFixture(t)
	stub := stubClipboard(t)
	stub.read = otherClipboardPNG(t, 64, 64, true)
	stub.activeLease = 4
	service.rememberClipboardImage(item.Path, raw, 4)

	outcome, err := service.CopyCaptureResult(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Copied || !outcome.Skipped {
		t.Fatalf("outcome = %+v, want skipped despite the live lease", outcome)
	}
	if len(stub.writes) != 0 {
		t.Fatalf("clipboard writes = %d, want 0", len(stub.writes))
	}
}

// TestCopyCaptureResultRequiresALiveLeaseForCaptures: matching pixels alone
// do not keep a capture's ownership after MyGo reports its write was replaced;
// a pasted image (lease 0) is the only path identified by pixels alone.
func TestCopyCaptureResultRequiresALiveLeaseForCaptures(t *testing.T) {
	service, item, raw := clipboardFixture(t)
	stub := stubClipboard(t)
	stub.read = raw
	stub.activeLease = 0
	service.rememberClipboardImage(item.Path, raw, 2)

	outcome, err := service.CopyCaptureResult(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Skipped || outcome.Copied {
		t.Fatalf("outcome = %+v, want skipped after the lease was released", outcome)
	}
	if len(stub.writes) != 0 {
		t.Fatalf("clipboard writes = %d, want 0", len(stub.writes))
	}
}

// TestCopyCaptureResultKeepsANewerImage: a different image on the clipboard
// is someone else's, even if it decoded successfully.
func TestCopyCaptureResultKeepsANewerImage(t *testing.T) {
	service, item, raw := clipboardFixture(t)
	stub := stubClipboard(t)
	stub.read = otherClipboardPNG(t, 64, 64, true)
	stub.activeLease = 0
	service.rememberClipboardImage(item.Path, raw, 1)

	outcome, err := service.CopyCaptureResult(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Copied || !outcome.Skipped {
		t.Fatalf("outcome = %+v, want skipped", outcome)
	}
	if len(stub.writes) != 0 {
		t.Fatalf("clipboard writes = %d, want 0", len(stub.writes))
	}
}

// TestCopyCaptureResultMatchesAPastedImageByPixels: a pasted image has no
// ownership lease, so the compressed result may replace it only when the
// decoded pixels match — the platform may have re-encoded the PNG.
func TestCopyCaptureResultMatchesAPastedImageByPixels(t *testing.T) {
	service, item, raw := clipboardFixture(t)
	stub := stubClipboard(t)
	stub.read = reencodePNG(t, raw)
	service.rememberClipboardImage(item.Path, raw, 0)

	outcome, err := service.CopyCaptureResult(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Copied {
		t.Fatalf("outcome = %+v, want copied after a pixel match", outcome)
	}
	if len(stub.writes) != 1 {
		t.Fatalf("clipboard writes = %d, want 1", len(stub.writes))
	}
}

func TestCopyCaptureResultRefusesAnUnrecordedClipboard(t *testing.T) {
	service, item, _ := clipboardFixture(t)
	stub := stubClipboard(t)
	stub.read = otherClipboardPNG(t, 40, 30, false)

	// No rememberClipboardImage: the app cannot prove the clipboard is its
	// own, so it must not overwrite it.
	outcome, err := service.CopyCaptureResult(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Skipped || outcome.Copied {
		t.Fatalf("outcome = %+v, want skipped", outcome)
	}
	if len(stub.writes) != 0 {
		t.Fatalf("clipboard writes = %d, want 0", len(stub.writes))
	}
}

// TestCopyCaptureResultSkipsWhenRememberedBytesCannotBeDecoded: without a
// pixel digest there is no identity to compare. A live lease must never let
// the compressed result overwrite a different, decodable image.
func TestCopyCaptureResultSkipsWhenRememberedBytesCannotBeDecoded(t *testing.T) {
	service, item, _ := clipboardFixture(t)
	stub := stubClipboard(t)
	stub.read = otherClipboardPNG(t, 32, 32, false)
	stub.activeLease = 9
	service.rememberClipboardImage(item.Path, []byte("not a decodable PNG"), 9)
	if item.clipboardHasPixels || item.clipboardLease != 0 {
		t.Fatal("undecodable remembered bytes were recorded as ownership")
	}

	outcome, err := service.CopyCaptureResult(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Skipped || outcome.Copied {
		t.Fatalf("outcome = %+v, want skipped without a pixel identity", outcome)
	}
	if len(stub.writes) != 0 {
		t.Fatalf("clipboard writes = %d, want 0", len(stub.writes))
	}
}

func TestCopyLastResultUsesTheMostRecentFinishedImage(t *testing.T) {
	service, item, _ := clipboardFixture(t)
	stub := stubClipboard(t)

	outcome, err := service.CopyLastResult()
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Copied || outcome.Name != item.OutputName {
		t.Fatalf("outcome = %+v, want %q copied", outcome, item.OutputName)
	}
	if len(stub.writes) != 1 {
		t.Fatalf("clipboard writes = %d, want 1", len(stub.writes))
	}

	if _, err := service.Remove([]int64{item.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CopyLastResult(); err == nil {
		t.Fatal("copying the last result succeeded after it was removed")
	}
}

func TestCopyItemAsMarkdownWritesADurableFileFirst(t *testing.T) {
	service, item, _ := clipboardFixture(t)
	stub := stubClipboard(t)

	result, err := service.CopyItemAsMarkdown(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path == "" || result.Path == item.compressedPath {
		t.Fatalf("markdown path %q is not a durable copy outside the session temp dir", result.Path)
	}
	if filepath.Dir(result.Path) != item.Dir {
		t.Fatalf("markdown file written to %s, want beside the original in %s", result.Path, item.Dir)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("markdown target does not exist: %v", err)
	}
	if stub.text != result.Markdown {
		t.Fatalf("clipboard text %q, want %q", stub.text, result.Markdown)
	}
	if !strings.HasPrefix(result.Markdown, "![my photo (1)-anypic](<") {
		t.Fatalf("markdown = %q, want an angle-bracketed link for the spaces and parentheses", result.Markdown)
	}
	if !strings.Contains(result.Markdown, filepath.ToSlash(result.Path)) {
		t.Fatalf("markdown %q does not point at %s", result.Markdown, result.Path)
	}
	if !item.saved || item.savedPath != result.Path {
		t.Fatalf("item saved state = %v at %q, want %q", item.saved, item.savedPath, result.Path)
	}
}

func TestCopyItemAsMarkdownOfAPassedThroughResultPointsAtTheOriginal(t *testing.T) {
	service, item, _ := clipboardFixture(t)
	stub := stubClipboard(t)
	item.PassedThrough = true

	result, err := service.CopyItemAsMarkdown(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != item.Path {
		t.Fatalf("markdown path = %s, want the original %s", result.Path, item.Path)
	}
	if !strings.Contains(stub.text, filepath.ToSlash(item.Path)) {
		t.Fatalf("markdown %q does not point at the original", stub.text)
	}
}

func TestCopyItemAsMarkdownNeedsAFinishedResult(t *testing.T) {
	service, item, _ := clipboardFixture(t)
	stubClipboard(t)
	item.Status = statusReady

	if _, err := service.CopyItemAsMarkdown(item.ID); err == nil {
		t.Fatal("markdown copy succeeded for an image that was never compressed")
	}
}
