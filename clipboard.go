package main

// Clipboard actions: copy one compressed result, replace a capture's raw
// clipboard image with its compressed version without ever clobbering
// something the user copied meanwhile, and copy a Markdown image link that
// points at a file that outlives the session.
//
// The clipboard calls are package variables so tests can stand in for a
// desktop without a running app; production uses MyGo's.
//
// Protection never compares container bytes. Windows CF_DIB and Linux
// clipboard round-trips re-encode images, so the bytes the OS hands back are
// not the bytes AnyPic wrote. The primary identity is a digest of decoded,
// premultiplied pixels, which survives re-encoding and also proves the image
// is still ours when a platform release callback lags. A successful write
// also returns an ownership lease token that MyGo invalidates when the
// clipboard no longer holds that write (replacement, Clear, Flush, shutdown).
// A live lease is required for capture-origin items, but it is never a
// substitute for the pixel digest: if the remembered PNG could not be decoded
// there is no identity to prove, so the copy skips instead of overwriting.
// Empty and text-only clipboards are never ours.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
)

// clipboardState is what the current clipboard holds. Text is read so a text
// copy is distinguishable in tests and code from an empty clipboard; both
// are "not an image" and neither may be overwritten.
type clipboardState struct {
	image []byte
	text  string
}

var (
	clipboardReadState = func() clipboardState {
		return clipboardState{image: mygo.Clipboard.ReadImage(), text: mygo.Clipboard.ReadText()}
	}
	clipboardReadImage = func() []byte { return mygo.Clipboard.ReadImage() }
	// clipboardWriteImage returns a lease token. A successful write whose
	// token is still active proves the clipboard still holds AnyPic's image.
	clipboardWriteImage = writeClipboardImageWithLease
	clipboardLeaseValid = clipboardLeaseStillActive
	clipboardWriteText  = func(text string) { mygo.Clipboard.WriteText(text) }
	clipboardClear      = func() { mygo.Clipboard.Clear() }
)

var (
	clipboardLeaseMu     sync.Mutex
	clipboardLeaseNext   uint64
	clipboardLeaseActive uint64
)

// writeClipboardImageWithLease writes the PNG and returns a token that goes
// stale as soon as MyGo reports the clipboard no longer holds this write.
// OnRelease is the cross-platform ownership signal: the platform backends
// invoke it on replacement, Clear, Flush and shutdown.
func writeClipboardImageWithLease(data []byte) (uint64, error) {
	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		return 0, err
	}
	clipboardLeaseMu.Lock()
	clipboardLeaseNext++
	token := clipboardLeaseNext
	clipboardLeaseActive = token
	clipboardLeaseMu.Unlock()

	err := mygo.Clipboard.Write(
		transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, data))),
		mygo.ClipboardOptions{OnRelease: func() {
			clipboardLeaseMu.Lock()
			if clipboardLeaseActive == token {
				clipboardLeaseActive = 0
			}
			clipboardLeaseMu.Unlock()
		}},
	)
	if err != nil {
		clipboardLeaseMu.Lock()
		if clipboardLeaseActive == token {
			clipboardLeaseActive = 0
		}
		clipboardLeaseMu.Unlock()
		return 0, err
	}
	return token, nil
}

func clipboardLeaseStillActive(token uint64) bool {
	clipboardLeaseMu.Lock()
	defer clipboardLeaseMu.Unlock()
	return token != 0 && clipboardLeaseActive == token
}

// CopyOutcome reports what a clipboard copy did.
type CopyOutcome struct {
	Copied bool `json:"copied"`
	// Skipped means the compressed image was not copied because the clipboard
	// no longer holds the capture AnyPic put there: the user copied something
	// else while compression ran, and that newer content is kept.
	Skipped bool   `json:"skipped"`
	Name    string `json:"name"`
}

// MarkdownCopy reports the link that was written to the clipboard and the
// file it points at.
type MarkdownCopy struct {
	Markdown string `json:"markdown"`
	Path     string `json:"path"`
}

// rememberClipboardImage records what a capture (lease != 0) or a pasted
// image (lease == 0) put on the clipboard, or what the user pasted, so
// CopyCaptureResult can later tell it apart from a newer user copy. Captures
// only call this after a successful write; a failed write records nothing.
// Only a decodable PNG becomes ownership: without a pixel digest a later
// clipboard image cannot be proven ours, so nothing is recorded.
func (a *AnyPic) rememberClipboardImage(path string, data []byte, lease uint64) {
	digest, hasPixels := clipboardPixelDigest(data)
	if !hasPixels {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, item := range a.items {
		if item.Path == path {
			item.clipboardLease = lease
			item.clipboardPixels = digest
			item.clipboardHasPixels = hasPixels
			return
		}
	}
}

// CopyCaptureResult replaces the raw capture on the clipboard with its
// compressed result — but only while the clipboard still holds an image and
// that image is still AnyPic's. Empty and text-only clipboards are never
// overwritten.
func (a *AnyPic) CopyCaptureResult(id int64) (CopyOutcome, error) {
	a.mu.Lock()
	item := a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return CopyOutcome{}, errors.New(tr().GoErrNoItem())
	}
	if item.Status != statusDone {
		a.mu.Unlock()
		return CopyOutcome{}, errors.New(tr().GoErrCompressFirst())
	}
	name := item.OutputName
	lease := item.clipboardLease
	pixels := item.clipboardPixels
	hasPixels := item.clipboardHasPixels
	a.mu.Unlock()

	current := clipboardReadState()
	// No image at all — empty, text, files — is someone else's clipboard or
	// no clipboard, never a capture to replace.
	if len(current.image) == 0 {
		return CopyOutcome{Skipped: true, Name: name}, nil
	}
	// Decoded pixels are the identity for every capture AnyPic can decode:
	// they survive Windows/Linux re-encoding, and they prove the current image
	// is still ours even if the platform's release callback lags behind an
	// external copy. If the remembered raw PNG could not be decoded there is
	// no identity to compare — skip rather than let a live lease overwrite a
	// different image.
	if !hasPixels {
		return CopyOutcome{Skipped: true, Name: name}, nil
	}
	// A capture's lease must still be active: MyGo invalidates it when the
	// clipboard no longer holds that write. A pasted image has no lease and
	// is identified by pixels alone.
	if lease != 0 && !clipboardLeaseValid(lease) {
		return CopyOutcome{Skipped: true, Name: name}, nil
	}
	if digest, ok := clipboardPixelDigest(current.image); !ok || digest != pixels {
		// Undecodable or different: someone else's image. A stale live lease
		// never overrides this.
		return CopyOutcome{Skipped: true, Name: name}, nil
	}
	if err := a.copyItemToClipboard(id); err != nil {
		return CopyOutcome{}, err
	}
	return CopyOutcome{Copied: true, Name: name}, nil
}

// clipboardPixelDigest hashes the decoded, premultiplied RGBA pixels of a
// PNG. Premultiplied values make fully transparent pixels compare equal even
// when their hidden RGB bytes differ, and the digest is independent of the
// encoder/container that produced the PNG.
func clipboardPixelDigest(data []byte) ([32]byte, bool) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		var zero [32]byte
		return zero, false
	}
	return imagePixelDigest(img), true
}

func imagePixelDigest(img image.Image) [32]byte {
	b := img.Bounds()
	h := sha256.New()
	var header [8]byte
	binary.LittleEndian.PutUint32(header[0:4], uint32(b.Dx()))
	binary.LittleEndian.PutUint32(header[4:8], uint32(b.Dy()))
	h.Write(header[:])
	var pixel [8]byte
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			binary.LittleEndian.PutUint16(pixel[0:2], uint16(r))
			binary.LittleEndian.PutUint16(pixel[2:4], uint16(g))
			binary.LittleEndian.PutUint16(pixel[4:6], uint16(bl))
			binary.LittleEndian.PutUint16(pixel[6:8], uint16(a))
			h.Write(pixel[:])
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// CopyLastResult puts the most recently finished compressed result on the
// clipboard again, for the tray's "copy the last result" item.
func (a *AnyPic) CopyLastResult() (CopyOutcome, error) {
	a.mu.Lock()
	id := a.lastResultID
	item := a.byID[id]
	if id == 0 || item == nil || item.Status != statusDone {
		a.mu.Unlock()
		return CopyOutcome{}, errors.New(tr().GoErrNoLastResult())
	}
	name := item.OutputName
	a.mu.Unlock()

	if err := a.copyItemToClipboard(id); err != nil {
		return CopyOutcome{}, err
	}
	return CopyOutcome{Copied: true, Name: name}, nil
}

// CopyItemAsMarkdown makes sure the compressed result has a durable copy on
// disk (in the configured output folder, or beside the original) and puts
// ![name](path) on the clipboard. Nothing is uploaded; the link is a local
// path for documents and issue drafts.
func (a *AnyPic) CopyItemAsMarkdown(id int64) (MarkdownCopy, error) {
	a.mu.Lock()
	item := a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return MarkdownCopy{}, errors.New(tr().GoErrNoItem())
	}
	if item.Status != statusDone {
		a.mu.Unlock()
		return MarkdownCopy{}, errors.New(tr().GoErrCompressFirst())
	}
	snapshot := *item
	folder := a.settings.OutputFolder
	mode := a.settings.OutputMode
	suffix := a.settings.Suffix
	a.mu.Unlock()

	dest := ""
	if snapshot.PassedThrough {
		// The result is the original file; it already lives on disk.
		dest = snapshot.Path
	} else {
		if snapshot.savedPath != "" {
			if _, err := os.Stat(snapshot.savedPath); err == nil {
				dest = snapshot.savedPath
			}
		}
		if dest == "" {
			src := snapshot.compressedPath
			if src == "" {
				src = snapshot.Path
			}
			destDir := snapshot.Dir
			if mode == "folder" && folder != "" {
				destDir = folder
			}
			name := snapshot.OutputName
			if name == "" {
				name = outputFilename(snapshot.Name, suffix, extForFormat(snapshot.OutputFormat))
			}
			dest = uniquePath(filepath.Join(destDir, name))
			if err := copyFile(src, dest); err != nil {
				return MarkdownCopy{}, fmt.Errorf("%s: %w", tr().GoErrMarkdownSave(), err)
			}
			a.markResultsSaved([]savedCopy{{id: id, path: dest}})
		}
	}

	alt := strings.TrimSuffix(filepath.Base(dest), filepath.Ext(dest))
	markdown := markdownImage(alt, dest)
	clipboardWriteText(markdown)
	return MarkdownCopy{Markdown: markdown, Path: dest}, nil
}

// markdownImage renders ![alt](path) for a local file. Paths with spaces,
// parentheses or angle brackets are wrapped as CommonMark's <...> form so a
// Markdown renderer does not split them.
func markdownImage(alt, path string) string {
	alt = strings.NewReplacer(`\`, `\\`, `]`, `\]`).Replace(alt)
	link := filepath.ToSlash(path)
	if strings.ContainsAny(link, " ()<>") {
		link = "<" + link + ">"
	}
	return "![" + alt + "](" + link + ")"
}
