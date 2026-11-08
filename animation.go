package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"io"
	"os"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/gen2brain/webp"
)

// animationFrameCount reports how many frames a GIF or WebP file holds by
// walking its file structure — block headers for GIF, RIFF chunks for WebP —
// without decoding any pixels (issue #7). The old detection decoded every
// frame just to count them, which stalled AddPaths on folders of animations.
// Any structural surprise falls back to the decoders, so a miscount is never
// worse than the old behavior.
func animationFrameCount(path string) (int, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, err
	}
	switch strings.ToLower(path[strings.LastIndexByte(path, '.'):]) {
	case ".gif":
		if n, err := countGIFFrames(path); err == nil {
			return n, nil
		}
		return countGIFFramesByDecoding(path)
	case ".webp":
		if n, err := countWebPFrames(path); err == nil {
			return n, nil
		}
		return countWebPFramesByDecoding(path)
	}
	return 1, nil
}

// countGIFFramesByDecoding is the ground truth the GIF scanner falls back to.
func countGIFFramesByDecoding(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	anim, err := gif.DecodeAll(f)
	if err != nil {
		return 0, err
	}
	return len(anim.Image), nil
}

// countWebPFramesByDecoding is the ground truth the WebP scanner falls back to.
func countWebPFramesByDecoding(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	anim, err := webp.DecodeAll(f)
	if err != nil {
		return 0, err
	}
	return len(anim.Image), nil
}

// countGIFFrames walks GIF blocks: 0x21 extension, 0x2C image descriptor
// (one frame), 0x3B trailer. Extension and image data are skipped through
// their sub-block chains without touching the pixels.
func countGIFFrames(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := bufio.NewReader(f)

	header := make([]byte, 6)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, err
	}
	if string(header[:3]) != "GIF" {
		return 0, errors.New("not a GIF")
	}
	lsd := make([]byte, 7)
	if _, err := io.ReadFull(r, lsd); err != nil {
		return 0, err
	}
	if lsd[6]&0x80 != 0 {
		if err := skipBytes(r, 3*(2<<(lsd[6]&0x07))); err != nil {
			return 0, err
		}
	}

	frames := 0
	for {
		marker, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		switch marker {
		case 0x3B: // trailer
			return frames, nil
		case 0x21: // extension: label byte, then a sub-block chain
			if _, err := r.ReadByte(); err != nil {
				return 0, err
			}
			if err := skipSubBlocks(r); err != nil {
				return 0, err
			}
		case 0x2C: // image descriptor: one frame
			frames++
			desc := make([]byte, 9)
			if _, err := io.ReadFull(r, desc); err != nil {
				return 0, err
			}
			if desc[8]&0x80 != 0 {
				if err := skipBytes(r, 3*(2<<(desc[8]&0x07))); err != nil {
					return 0, err
				}
			}
			if _, err := r.ReadByte(); err != nil { // LZW minimum code size
				return 0, err
			}
			if err := skipSubBlocks(r); err != nil {
				return 0, err
			}
		default:
			return 0, errors.New("unknown GIF block")
		}
	}
}

// skipSubBlocks consumes a GIF sub-block chain: one size byte followed by
// that much data, until a zero size ends the chain.
func skipSubBlocks(r *bufio.Reader) error {
	for {
		n, err := r.ReadByte()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if err := skipBytes(r, int(n)); err != nil {
			return err
		}
	}
}

func skipBytes(r *bufio.Reader, n int) error {
	const scratch = 4096
	buf := make([]byte, min(n, scratch))
	for n > 0 {
		chunk := min(n, scratch)
		if _, err := io.ReadFull(r, buf[:chunk]); err != nil {
			return err
		}
		n -= chunk
	}
	return nil
}

// countWebPFrames walks the RIFF container's top-level chunks and counts
// ANMF (animation frame) records; a container without them is a still image.
// Chunk bounds are checked against the declared RIFF size, so a truncated
// file is reported as an error instead of a bogus frame count.
func countWebPFrames(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := bufio.NewReader(f)

	header := make([]byte, 12)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, err
	}
	if string(header[:4]) != "RIFF" || string(header[8:12]) != "WEBP" {
		return 0, errors.New("not a WebP")
	}
	// The RIFF size covers everything after the 8-byte prefix, including the
	// 4-byte form type; what is left for chunks is size minus that type.
	payload := int(binary.LittleEndian.Uint32(header[4:8])) - 4

	frames := 0
	sawImageChunk := false
	pos := 0
	for pos+8 <= payload {
		chunk := make([]byte, 8)
		if _, err := io.ReadFull(r, chunk); err != nil {
			return 0, err
		}
		id := string(chunk[:4])
		size := int(binary.LittleEndian.Uint32(chunk[4:8]))
		pos += 8
		switch {
		case id == "ANMF":
			frames++
		case id == "VP8 " || id == "VP8L":
			sawImageChunk = true
		}
		// Chunk payloads pad to an even offset; the pad byte is not counted
		// in the size field.
		skip := size + size%2
		if pos+skip > payload {
			return 0, errors.New("chunk overruns the declared RIFF size")
		}
		if skip > 0 {
			if _, err := r.Discard(skip); err != nil {
				return 0, err
			}
		}
		pos += skip
	}
	// Animated containers carry their image data inside ANMF frames and have
	// no top-level VP8/VP8L chunk. A still image has image data but no frames.
	if frames == 0 {
		if !sawImageChunk {
			return 0, errors.New("no image data")
		}
		return 1, nil
	}
	return frames, nil
}

// compressAnimated converts an animated GIF or WebP into animated WebP,
// preserving frames, delays, loop count and transparency (#8). Like the
// still pipeline it is honest about the result: an output that would be
// larger keeps the original and reports the item as already optimal.
func (a *AnyPic) compressAnimated(ctx context.Context, id int64, path, srcFormat, name string, s Settings) (compressResult, error) {
	if err := ctx.Err(); err != nil {
		return compressResult{}, err
	}

	var frames []image.Image
	var delays []int
	loop := 0
	switch displayFormat(srcFormat) {
	case "GIF":
		f, err := os.Open(path)
		if err != nil {
			return compressResult{}, errors.New("无法读取原图")
		}
		anim, err := gif.DecodeAll(f)
		f.Close()
		if err != nil {
			return compressResult{}, errors.New("图片解码失败，可能已损坏")
		}
		frames, delays = compositeGIF(anim)
		loop = webpLoopCount(anim.LoopCount)
	case "WEBP":
		f, err := os.Open(path)
		if err != nil {
			return compressResult{}, errors.New("无法读取原图")
		}
		anim, err := webp.DecodeAll(f)
		f.Close()
		if err != nil {
			return compressResult{}, errors.New("图片解码失败，可能已损坏")
		}
		frames, delays = anim.Image, make([]int, len(anim.Delay))
		copy(delays, anim.Delay)
		loop = anim.LoopCount
	default:
		return compressResult{}, errors.New("不支持的动图格式")
	}
	if len(frames) == 0 {
		return compressResult{}, errors.New("动图没有可用的帧")
	}
	for i := range delays {
		// libwebp rejects zero-length frames; GIF encoders use 0 or 1
		// centisecond to mean "as fast as possible".
		if delays[i] < 10 {
			delays[i] = 10
		}
	}

	b := frames[0].Bounds()
	width, height := b.Dx(), b.Dy()
	resized := false
	if s.MaxDimension > 0 && max(width, height) > s.MaxDimension {
		for i, frame := range frames {
			frames[i] = imaging.Fit(frame, s.MaxDimension, s.MaxDimension, imaging.Lanczos)
		}
		b = frames[0].Bounds()
		width, height = b.Dx(), b.Dy()
		resized = true
	}

	var buf bytes.Buffer
	if err := webp.EncodeAll(&buf, &webp.WEBP{Image: frames, Delay: delays, LoopCount: loop}, webp.Options{Quality: s.Quality, Method: 4}); err != nil {
		return compressResult{}, errors.New("WebP 编码失败")
	}
	if err := ctx.Err(); err != nil {
		return compressResult{}, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return compressResult{}, errors.New("无法读取原图")
	}
	if !resized && int64(buf.Len()) >= info.Size() {
		return compressResult{
			path:          path,
			size:          info.Size(),
			format:        displayFormat(srcFormat),
			outputName:    outputFilename(name, s.Suffix, extForFormat(srcFormat)),
			width:         width,
			height:        height,
			hasAlpha:      true,
			passedThrough: true,
		}, nil
	}

	dir, err := a.ensureTempDir()
	if err != nil {
		return compressResult{}, errors.New("无法创建临时文件")
	}
	tmp, err := os.CreateTemp(dir, fmt.Sprintf("%d-*.webp", id))
	if err != nil {
		return compressResult{}, errors.New("无法创建临时文件")
	}
	outPath := tmp.Name()
	tmp.Close()
	if err := os.WriteFile(outPath, buf.Bytes(), 0o600); err != nil {
		_ = os.Remove(outPath)
		return compressResult{}, errors.New("无法写入压缩结果")
	}
	return compressResult{
		path:       outPath,
		size:       int64(buf.Len()),
		format:     "WEBP",
		outputName: outputFilename(name, s.Suffix, "webp"),
		width:      width,
		height:     height,
		hasAlpha:   true,
	}, nil
}

// compositeGIF renders every GIF frame onto one canvas, honouring disposal,
// so WebP receives full-canvas frames with the animation intact.
func compositeGIF(anim *gif.GIF) ([]image.Image, []int) {
	w, h := anim.Config.Width, anim.Config.Height
	if w <= 0 || h <= 0 {
		b := anim.Image[0].Bounds()
		w, h = b.Dx(), b.Dy()
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, w, h))
	frames := make([]image.Image, 0, len(anim.Image))
	delays := make([]int, len(anim.Image))
	for i, src := range anim.Image {
		// GIF delays are centiseconds; 0 means "as fast as possible" and
		// libwebp needs at least one millisecond, so 10ms is the floor.
		delay := 10
		if i < len(anim.Delay) {
			delay = anim.Delay[i] * 10
			if delay < 10 {
				delay = 10
			}
		}
		delays[i] = delay

		disposal := byte(gif.DisposalNone)
		if i < len(anim.Disposal) {
			disposal = anim.Disposal[i]
		}
		var before *image.NRGBA
		if disposal == gif.DisposalPrevious {
			before = cloneNRGBA(canvas)
		}
		draw.Draw(canvas, src.Bounds(), src, src.Bounds().Min, draw.Over)
		frames = append(frames, cloneNRGBA(canvas))
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, src.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			if before != nil {
				canvas = before
			}
		}
	}
	return frames, delays
}

func cloneNRGBA(img *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(img.Bounds())
	copy(dst.Pix, img.Pix)
	return dst
}

// webpLoopCount maps the GIF loop count to WebP's: 0 is infinite in both,
// and Go's -1 means play once.
func webpLoopCount(gifLoop int) int {
	if gifLoop < 0 {
		return 1
	}
	return gifLoop
}
