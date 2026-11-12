package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

// solidImage returns a w×h image filled with a single opaque color.
func solidImage(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// mergeStyle overlays the string fields of extra onto base, so a test can
// vary one knob of a style without rewriting all of them. Bool fields keep
// base's value.
func mergeStyle(base, extra FrameStyle) FrameStyle {
	if extra.Background != "" {
		base.Background = extra.Background
	}
	if extra.BGColor != "" {
		base.BGColor = extra.BGColor
	}
	if extra.BGGradient != "" {
		base.BGGradient = extra.BGGradient
	}
	if extra.Radius != "" {
		base.Radius = extra.Radius
	}
	if extra.Shadow != "" {
		base.Shadow = extra.Shadow
	}
	if extra.Padding != "" {
		base.Padding = extra.Padding
	}
	if extra.Ratio != "" {
		base.Ratio = extra.Ratio
	}
	return base
}

func TestIssue1NormalizeFrameStyle(t *testing.T) {
	s := normalizeFrameStyle(FrameStyle{})
	if s.Background != "none" || s.BGGradient != "indigo" || s.Radius != "medium" ||
		s.Shadow != "soft" || s.Padding != "normal" || s.Ratio != "origin" {
		t.Errorf("zero style normalized to %+v, want the documented defaults", s)
	}
	if s.BGColor == "" {
		t.Error("zero style has no fallback solid color")
	}

	valid := normalizeFrameStyle(FrameStyle{
		Background: "solid", BGColor: "#AABBCC", BGGradient: "ocean",
		Radius: "large", Ring: true, Shadow: "deep", Padding: "roomy", Ratio: "1:1",
	})
	if valid.Background != "solid" || valid.BGColor != "#aabbcc" || valid.BGGradient != "ocean" ||
		valid.Radius != "large" || !valid.Ring || valid.Shadow != "deep" ||
		valid.Padding != "roomy" || valid.Ratio != "1:1" {
		t.Errorf("valid style mangled: %+v", valid)
	}

	bogus := normalizeFrameStyle(FrameStyle{
		Background: "wallpaper", BGColor: "zzz", BGGradient: "nope",
		Radius: "round", Shadow: "huge", Padding: "vast", Ratio: "21:9",
	})
	if bogus.Background != "none" || bogus.BGColor == "zzz" || bogus.BGGradient != "indigo" ||
		bogus.Radius != "medium" || bogus.Shadow != "soft" || bogus.Padding != "normal" || bogus.Ratio != "origin" {
		t.Errorf("bogus style not normalized to defaults: %+v", bogus)
	}
}

func TestIssue1DecorateSolidColor(t *testing.T) {
	src := solidImage(400, 300, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	style := FrameStyle{Background: "solid", BGColor: "#112233", Radius: "none", Shadow: "off"}
	out := decorateScreenshot(src, style)
	p := framePadding(400, 300, "normal")
	if b := out.Bounds(); b.Dx() != 400+2*p || b.Dy() != 300+2*p {
		t.Fatalf("decorated size %v, want %dx%d", b, 400+2*p, 300+2*p)
	}
	if r, g, b, a := out.At(2, 2).RGBA(); r != 0x1111 || g != 0x2222 || b != 0x3333 || a != 0xffff {
		t.Errorf("backdrop pixel (%d,%d,%d,%d), want #112233", r, g, b, a)
	}
	if r, g, b, a := out.At(p+200, p+150).RGBA(); r != 0xe0e0 || g != 0x2020 || b != 0x2020 || a != 0xffff {
		t.Errorf("center pixel (%d,%d,%d,%d) changed by decoration", r, g, b, a)
	}
}

func TestIssue1DecorateGradientPreset(t *testing.T) {
	src := solidImage(400, 300, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	out := decorateScreenshot(src, FrameStyle{Background: "gradient", BGGradient: "indigo"})
	if r, g, b, a := out.At(3, 3).RGBA(); a != 0xffff || r == 0xe0e0 {
		t.Errorf("gradient backdrop corner (%d,%d,%d,%d) not an opaque backdrop", r, g, b, a)
	}
	tr, tg, tb, _ := out.At(3, 3).RGBA()
	br, bg, bb, _ := out.At(out.Bounds().Dx()-4, out.Bounds().Dy()-4).RGBA()
	if tr == br && tg == bg && tb == bb {
		t.Error("gradient backdrop has identical opposite corners")
	}
}

func TestIssue1DecorateRadius(t *testing.T) {
	src := solidImage(400, 300, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	base := FrameStyle{Background: "none", Shadow: "off"}

	square := decorateScreenshot(src, FrameStyle(mergeStyle(base, FrameStyle{Radius: "none"})))
	p := framePadding(400, 300, "normal")
	// No radius: the content's own corner survives untouched.
	if r, g, b, a := square.At(p, p).RGBA(); r != 0xe0e0 || g != 0x2020 || b != 0x2020 || a != 0xffff {
		t.Errorf("square corner (%d,%d,%d,%d), want the red content corner", r, g, b, a)
	}

	rounded := decorateScreenshot(src, FrameStyle(mergeStyle(base, FrameStyle{Radius: "medium"})))
	if _, _, _, a := rounded.At(p, p).RGBA(); a != 0 {
		t.Errorf("rounded corner alpha %d, want fully transparent", a)
	}
}

func TestIssue1DecorateRatio(t *testing.T) {
	src := solidImage(400, 300, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	out := decorateScreenshot(src, FrameStyle{Background: "solid", BGColor: "#112233", Ratio: "1:1"})
	b := out.Bounds()
	if b.Dx() != b.Dy() {
		t.Fatalf("1:1 canvas is %dx%d, want square", b.Dx(), b.Dy())
	}
	// The image sits centered inside the square content box, backdrop all around.
	p := framePadding(out.Bounds().Dx(), out.Bounds().Dy(), "normal")
	if r, g, b, a := out.At(p+3, p+3).RGBA(); a != 0xffff || r != 0x1111 || g != 0x2222 || b != 0x3333 {
		t.Errorf("extended canvas area not filled with the backdrop: (%d,%d,%d,%d)", r, g, b, a)
	}
}

func TestIssue1DecoratePaddingLevels(t *testing.T) {
	// A big enough image that the smallest margin clears the 16px floor.
	src := solidImage(900, 600, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	widths := map[string]int{}
	for _, level := range []string{"compact", "normal", "roomy"} {
		out := decorateScreenshot(src, FrameStyle{Background: "none", Shadow: "off", Radius: "none", Padding: level})
		widths[level] = out.Bounds().Dx()
	}
	if !(widths["compact"] < widths["normal"] && widths["normal"] < widths["roomy"]) {
		t.Errorf("padding levels not increasing: %v", widths)
	}
}

func TestIssue1DecorateShadowLevels(t *testing.T) {
	src := solidImage(400, 300, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	alphaAt := func(style FrameStyle) uint32 {
		out := decorateScreenshot(src, style)
		p := framePadding(400, 300, "normal")
		_, _, _, a := out.At(p+200, p+300+6).RGBA()
		return a
	}
	off := alphaAt(FrameStyle{Background: "none", Shadow: "off", Radius: "none"})
	soft := alphaAt(FrameStyle{Background: "none", Shadow: "soft", Radius: "none"})
	deep := alphaAt(FrameStyle{Background: "none", Shadow: "deep", Radius: "none"})
	if off != 0 || soft == 0 || deep <= soft {
		t.Errorf("shadow levels wrong: off=%d soft=%d deep=%d", off, soft, deep)
	}
}

// TestFrameShadowFitsMargin pins the geometry contract behind the padding
// tiers: every shadow layer's visible extent (two sigmas past its offset) has
// to stay inside the margin the padding bought, or the canvas edge would cut
// the shadow off — visibly so on the deep level.
func TestFrameShadowFitsMargin(t *testing.T) {
	for _, level := range []string{"soft", "deep"} {
		for _, p := range []float64{24, 76, 120, 345} {
			extent := 0.0
			for _, layer := range shadowStack(level, p) {
				extent = max(extent, 2*layer.sigma+layer.dy)
			}
			if extent > p {
				t.Errorf("%s shadow at margin %.0f reaches %.1fpx, past the canvas edge", level, p, extent)
			}
		}
	}
}

func TestIssue1DecorateRing(t *testing.T) {
	src := solidImage(400, 300, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	base := FrameStyle{Background: "none", Shadow: "off", Radius: "none"}
	p := framePadding(400, 300, "normal")

	withRing := base
	withRing.Ring = true
	ringOut := decorateScreenshot(src, withRing)
	if _, _, _, a := ringOut.At(p+200, p-1).RGBA(); a == 0 {
		t.Error("ring disabled: nothing drawn just outside the content edge")
	}
	withoutRing := decorateScreenshot(src, base)
	if _, _, _, a := withoutRing.At(p+200, p-1).RGBA(); a != 0 {
		t.Errorf("ring off but edge pixel has alpha %d, want fully transparent", a)
	}
}

func TestIssue1DecorateTinyImage(t *testing.T) {
	src := solidImage(64, 64, color.RGBA{R: 0x20, G: 0x40, B: 0xe0, A: 0xff})
	out := decorateScreenshot(src, FrameStyle{Background: "solid", BGColor: "#112233"})
	b := out.Bounds()
	p := framePadding(64, 64, "normal")
	if b.Dx() != 64+2*p || b.Dy() != 64+2*p {
		t.Fatalf("decorated size %dx%d, want %dx%d with clamped padding", b.Dx(), b.Dy(), 64+2*p, 64+2*p)
	}
}

func TestIssue1DecorateItem(t *testing.T) {
	dir := t.TempDir()
	src := solidImage(40, 30, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	original := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(original, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	a := &AnyPic{
		byID:     map[int64]*Item{},
		settings: defaultSettings(),
		thumbSem: make(chan struct{}, 1),
	}
	item := &Item{
		ID:           1,
		Name:         "shot.png",
		Path:         original,
		Dir:          dir,
		Format:       "PNG",
		Width:        40,
		Height:       30,
		OriginalSize: int64(buf.Len()),
		Status:       statusDone,
	}
	a.items = []*Item{item}
	a.byID[1] = item

	// A zero style still normalizes to the documented defaults and renders.
	snap, err := a.DecorateItem(1, FrameStyle{})
	if err != nil {
		t.Fatalf("DecorateItem: %v", err)
	}
	got := snap.Items[0]
	if got.Path == original {
		t.Fatal("DecorateItem overwrote or kept the original file as the item source")
	}
	if !strings.HasSuffix(got.Name, "-美化.png") {
		t.Errorf("decorated name %q, want the 原名-美化 form", got.Name)
	}
	if filepath.Dir(got.Path) != dir {
		t.Errorf("decorated file %s left the original folder", got.Path)
	}
	if got.Status != statusReady {
		t.Errorf("decorated item status %q, want ready", got.Status)
	}

	// The original file is untouched on disk (invariant 2).
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if !bytes.Equal(data, buf.Bytes()) {
		t.Error("original file bytes changed")
	}
	// The decorated file is the framed capture: wider than the original.
	decorated, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read decorated file: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(decorated))
	if err != nil {
		t.Fatalf("decode decorated file: %v", err)
	}
	if img.Bounds().Dx() <= 40 || img.Bounds().Dy() <= 30 {
		t.Errorf("decorated file size %v is not framed", img.Bounds())
	}
}

func TestIssue1PreviewDecorated(t *testing.T) {
	dir := t.TempDir()
	src := solidImage(400, 300, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	a := &AnyPic{
		byID:     map[int64]*Item{},
		settings: defaultSettings(),
		thumbSem: make(chan struct{}, 1),
	}
	a.items = []*Item{{ID: 7, Name: "shot.png", Path: path, Dir: dir, Status: statusDone}}
	a.byID[7] = a.items[0]

	s, err := a.PreviewDecorated(7, FrameStyle{Background: "gradient", BGGradient: "indigo"})
	if err != nil {
		t.Fatalf("PreviewDecorated: %v", err)
	}
	if !strings.HasPrefix(s, "data:image/") {
		t.Errorf("preview %q is not an image data URL", s[:min(40, len(s))])
	}

	// The pattern path goes through the scaled preview renderer; a silent
	// failure there would only show up as a blank stage in the workbench.
	if s, err := a.PreviewDecorated(7, FrameStyle{Background: "pattern", BGPattern: "polka"}); err != nil {
		t.Fatalf("PreviewDecorated pattern: %v", err)
	} else if !strings.HasPrefix(s, "data:image/") {
		t.Errorf("pattern preview %q is not an image data URL", s[:min(40, len(s))])
	}

	if _, err := a.PreviewDecorated(99, FrameStyle{}); err == nil {
		t.Error("PreviewDecorated for a missing item should fail")
	}
}

func TestWriteScreenshotKeepsEveryCapture(t *testing.T) {
	pictures := t.TempDir()
	mygo.App.SetPath(mygo.PathPictures, pictures)
	t.Cleanup(func() { mygo.App.SetPath(mygo.PathPictures, "") })

	first, err := writeScreenshot([]byte("first"))
	if err != nil {
		t.Fatalf("writeScreenshot: %v", err)
	}
	second, err := writeScreenshot([]byte("second"))
	if err != nil {
		t.Fatalf("writeScreenshot: %v", err)
	}
	if first == second {
		t.Fatalf("two captures in the same second overwrote each other: %s", first)
	}

	wantDir := filepath.Join(pictures, "AnyPic")
	for path, want := range map[string]string{first: "first", second: "second"} {
		if filepath.Dir(path) != wantDir {
			t.Errorf("%s is outside %s", path, wantDir)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(data) != want {
			t.Errorf("%s holds %q, want %q", path, data, want)
		}
	}
}

func TestScreenshotPlanExists(t *testing.T) {
	plan, err := screenshotPlan()
	if err != nil {
		t.Skipf("no screenshot tool on this machine: %v", err)
	}
	if plan.name == "" {
		t.Fatal("screenshot plan has no command")
	}
	if _, err := exec.LookPath(plan.name); err != nil && !filepath.IsAbs(plan.name) {
		t.Fatalf("screenshot command %q is not available: %v", plan.name, err)
	}
}

func TestNormalizeSettingsKeepsScreenshotShortcut(t *testing.T) {
	if got := normalizeSettings(Settings{}).ScreenshotShortcut; got != DefaultScreenshotShortcut {
		t.Errorf("empty shortcut normalized to %q, want %q", got, DefaultScreenshotShortcut)
	}
	const custom = "Cmd+Alt+X"
	if got := normalizeSettings(Settings{ScreenshotShortcut: custom}).ScreenshotShortcut; got != custom {
		t.Errorf("custom shortcut normalized to %q, want %q", got, custom)
	}
}

func TestNormalizeSettingsScreenshotClipboard(t *testing.T) {
	if got := normalizeSettings(Settings{}).ScreenshotClipboard; got != "on" {
		t.Errorf("empty clipboard preference normalized to %q, want on", got)
	}
	if got := normalizeSettings(Settings{ScreenshotClipboard: "off"}).ScreenshotClipboard; got != "off" {
		t.Errorf("off preference normalized to %q, want off", got)
	}
	if got := normalizeSettings(Settings{ScreenshotClipboard: "bogus"}).ScreenshotClipboard; got != "on" {
		t.Errorf("invalid clipboard preference normalized to %q, want on", got)
	}
}

func TestNormalizeSettingsHideDockWhenTray(t *testing.T) {
	if got := normalizeSettings(Settings{}).HideDockWhenTray; got != "on" {
		t.Errorf("empty dock preference normalized to %q, want on", got)
	}
	if got := normalizeSettings(Settings{HideDockWhenTray: "off"}).HideDockWhenTray; got != "off" {
		t.Errorf("off preference normalized to %q, want off", got)
	}
	if got := normalizeSettings(Settings{HideDockWhenTray: "bogus"}).HideDockWhenTray; got != "on" {
		t.Errorf("invalid dock preference normalized to %q, want on", got)
	}
}

func TestNormalizeSettingsOpenAtLogin(t *testing.T) {
	if got := normalizeSettings(Settings{}).OpenAtLogin; got != "off" {
		t.Errorf("empty login preference normalized to %q, want off", got)
	}
	if got := normalizeSettings(Settings{OpenAtLogin: "on"}).OpenAtLogin; got != "on" {
		t.Errorf("on preference normalized to %q, want on", got)
	}
	if got := normalizeSettings(Settings{OpenAtLogin: "bogus"}).OpenAtLogin; got != "off" {
		t.Errorf("invalid login preference normalized to %q, want off", got)
	}
}
