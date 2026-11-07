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

func TestIssue1NormalizeScreenshotAppearance(t *testing.T) {
	if got := normalizeSettings(Settings{}).ScreenshotBorder; got != "off" {
		t.Errorf("empty border preference normalized to %q, want off", got)
	}
	if got := normalizeSettings(Settings{ScreenshotBorder: "on"}).ScreenshotBorder; got != "on" {
		t.Errorf("on border preference normalized to %q, want on", got)
	}
	if got := normalizeSettings(Settings{ScreenshotBorder: "bogus"}).ScreenshotBorder; got != "off" {
		t.Errorf("invalid border preference normalized to %q, want off", got)
	}

	if got := normalizeSettings(Settings{}).ScreenshotBackground; got != "off" {
		t.Errorf("empty background preference normalized to %q, want off", got)
	}
	for _, style := range []string{"light", "dark", "gradient"} {
		if got := normalizeSettings(Settings{ScreenshotBackground: style}).ScreenshotBackground; got != style {
			t.Errorf("background %q normalized to %q, want %q", style, got, style)
		}
	}
	if got := normalizeSettings(Settings{ScreenshotBackground: "bogus"}).ScreenshotBackground; got != "off" {
		t.Errorf("invalid background preference normalized to %q, want off", got)
	}
}

func TestIssue1DecorateBackgroundLight(t *testing.T) {
	src := solidImage(400, 300, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	out := decorateScreenshot(src, false, "light")
	p := framePadding(400, 300)
	b := out.Bounds()
	if b.Dx() != 400+2*p || b.Dy() != 300+2*p {
		t.Fatalf("decorated size %dx%d, want %dx%d", b.Dx(), b.Dy(), 400+2*p, 300+2*p)
	}

	// The backdrop fills the whole canvas, so its corners are opaque.
	br, bg, bb, ba := out.At(2, 2).RGBA()
	if ba != 0xffff || br < 0xc000 {
		t.Errorf("backdrop corner not opaque light gray: %d %d %d %d", br, bg, bb, ba)
	}

	// The content sits centered and unchanged.
	if r, g, b, a := out.At(p+200, p+150).RGBA(); r != 0xe0e0 || g != 0x2020 || b != 0x2020 || a != 0xffff {
		t.Errorf("center pixel (%d,%d,%d,%d) changed by decoration", r, g, b, a)
	}

	// The content's corner is cut away by the rounded mask: the backdrop
	// shows through where the capture's own corner would have been red.
	if r, g, b, _ := out.At(p, p).RGBA(); r == 0xe0e0 && g == 0x2020 && b == 0x2020 {
		t.Error("content corner is still red, want the rounded cut")
	}
}

func TestIssue1DecorateBorderOnly(t *testing.T) {
	src := solidImage(400, 300, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	out := decorateScreenshot(src, true, "off")
	p := framePadding(400, 300)
	b := out.Bounds()
	if b.Dx() != 400+2*p || b.Dy() != 300+2*p {
		t.Fatalf("decorated size %dx%d, want %dx%d", b.Dx(), b.Dy(), 400+2*p, 300+2*p)
	}

	// Without a background the padding stays transparent, even the shadowed
	// corner far away from the content.
	if _, _, _, a := out.At(0, 0).RGBA(); a != 0 {
		t.Errorf("canvas corner alpha %d, want fully transparent", a)
	}

	// The shadow darkens the padding right below the content.
	if _, _, _, a := out.At(p+200, p+300+6).RGBA(); a == 0 {
		t.Error("no shadow below the content")
	}

	// The content sits centered and unchanged.
	if r, g, b, a := out.At(p+200, p+150).RGBA(); r != 0xe0e0 || g != 0x2020 || b != 0x2020 || a != 0xffff {
		t.Errorf("center pixel (%d,%d,%d,%d) changed by decoration", r, g, b, a)
	}
}

func TestIssue1DecorateGradient(t *testing.T) {
	src := solidImage(400, 300, color.RGBA{R: 0xe0, G: 0x20, B: 0x20, A: 0xff})
	out := decorateScreenshot(src, false, "gradient")
	p := framePadding(400, 300)
	tr, tg, tb, ta := out.At(3, 3).RGBA()
	br, bg, bb, ba := out.At(out.Bounds().Dx()-4, out.Bounds().Dy()-4).RGBA()
	if tr == br && tg == bg && tb == bb && ta == ba {
		t.Error("gradient backdrop has identical opposite corners")
	}
	// The content still sits centered and unchanged.
	if r, g, b, a := out.At(p+200, p+150).RGBA(); r != 0xe0e0 || g != 0x2020 || b != 0x2020 || a != 0xffff {
		t.Errorf("center pixel (%d,%d,%d,%d) changed by decoration", r, g, b, a)
	}
}

func TestIssue1DecorateTinyImage(t *testing.T) {
	src := solidImage(64, 64, color.RGBA{R: 0x20, G: 0x40, B: 0xe0, A: 0xff})
	out := decorateScreenshot(src, true, "light")
	b := out.Bounds()
	if b.Dx() < 64 || b.Dy() < 64 {
		t.Fatalf("decorated size %dx%d is smaller than the source", b.Dx(), b.Dy())
	}
	if b.Dx() > 64+2*framePadding(64, 64) || b.Dy() > 64+2*framePadding(64, 64) {
		t.Fatalf("decorated size %dx%d exceeds the clamped padding", b.Dx(), b.Dy())
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

	// With both preferences off the request is refused instead of silently
	// producing an identical file.
	if _, err := a.DecorateItem(1, Settings{}); err == nil {
		t.Fatal("DecorateItem with border and background off should refuse")
	}

	snap, err := a.DecorateItem(1, Settings{ScreenshotBorder: "on", ScreenshotBackground: "light"})
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
	p := framePadding(40, 30)
	if got.Width != 40+2*p || got.Height != 30+2*p {
		t.Errorf("decorated item size %dx%d, want %dx%d", got.Width, got.Height, 40+2*p, 30+2*p)
	}

	// The original file is untouched on disk (invariant 2).
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if !bytes.Equal(data, buf.Bytes()) {
		t.Error("original file bytes changed")
	}
	// And the decorated file really is the larger, framed capture.
	decorated, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read decorated file: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(decorated))
	if err != nil {
		t.Fatalf("decode decorated file: %v", err)
	}
	if img.Bounds().Dx() != 40+2*p || img.Bounds().Dy() != 30+2*p {
		t.Errorf("decorated file size %v, want %dx%d", img.Bounds(), 40+2*p, 30+2*p)
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
