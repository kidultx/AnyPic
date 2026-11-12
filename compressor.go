package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disintegration/imaging"
	"github.com/egoist/mygo"
	"github.com/gen2brain/webp"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
)

const (
	appVersion = "0.1.0"

	statusReady   = "ready"
	statusWorking = "working"
	statusDone    = "done"
	statusError   = "error"

	thumbMaxSide   = 360
	previewMaxSide = 1600
	maxBatchSize   = 500
	maxFileSize    = 250 << 20
)

var supportedExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	".gif": true, ".bmp": true, ".tif": true, ".tiff": true,
}

// Item is one image in the queue.
type Item struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Path           string `json:"path"`
	Dir            string `json:"dir"`
	Format         string `json:"format"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	OriginalSize   int64  `json:"originalSize"`
	CompressedSize int64  `json:"compressedSize"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	OutputFormat   string `json:"outputFormat"`
	OutputName     string `json:"outputName"`
	Thumb          string `json:"thumb,omitempty"`
	HasAlpha       bool   `json:"hasAlpha"`
	PassedThrough  bool   `json:"passedThrough"`
	// TransparencyKept reports that the user chose JPG but the image has
	// transparency, so the output was saved as PNG instead of being
	// flattened onto a background (#15). Passthrough results carry it too,
	// because the reason still holds.
	TransparencyKept bool    `json:"transparencyKept"`
	SavedPercent     float64 `json:"savedPercent"`
	// Animated marks a multi-frame GIF/WebP that converts to animated WebP
	// instead of a still re-encode (#8).
	Animated bool `json:"animated"`

	compressedPath string
	stale          bool
	// saved records that a copy of the compressed result already exists
	// outside the session temp directory (an export wrote it), so quitting
	// will not lose it. See UnsavedResults.
	saved bool
}

// Settings are the compression preferences, persisted between launches.
type Settings struct {
	Format       string `json:"format"`
	Quality      int    `json:"quality"`
	MaxDimension int    `json:"maxDimension"`
	OutputMode   string `json:"outputMode"`
	OutputFolder string `json:"outputFolder"`
	Suffix       string `json:"suffix"`
	Theme        string `json:"theme"`
	// Language is the language the interface shows: "system", "zh-Hans" or
	// "en" (#32).
	Language string `json:"language"`
	// AutoProcess compresses additions — files, folders and captures —
	// without the extra compress/export clicks: "on" or "off".
	AutoProcess string `json:"autoProcess"`
	// ScreenshotShortcut is the global accelerator that starts a capture.
	ScreenshotShortcut string `json:"screenshotShortcut"`
	// ScreenshotClipboard keeps a fresh capture on the clipboard: "on" or
	// "off". The capture tools write to the clipboard themselves, so "off"
	// clears it again once the image has been read.
	ScreenshotClipboard string `json:"screenshotClipboard"`
	// HideDockWhenTray removes the Dock icon while the window is hidden to
	// the tray: "on" or "off". macOS only.
	HideDockWhenTray string `json:"hideDockWhenTray"`
	// OpenAtLogin starts AnyPic when the user logs in: "on" or "off". The
	// system login item is toggled through mygo, not stored only here.
	OpenAtLogin string `json:"openAtLogin"`
}

// Batch is the state of a running compression pass.
type Batch struct {
	Running  bool `json:"running"`
	Total    int  `json:"total"`
	Done     int  `json:"done"`
	Failed   int  `json:"failed"`
	Canceled bool `json:"canceled"`
}

// Snapshot is everything the page needs to draw the queue.
type Snapshot struct {
	Items    []Item   `json:"items"`
	Settings Settings `json:"settings"`
	Batch    Batch    `json:"batch"`
}

// SkippedFile says why a file was not added.
type SkippedFile struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// AddResult reports what AddPaths accepted.
type AddResult struct {
	Snapshot Snapshot      `json:"snapshot"`
	Added    int           `json:"added"`
	Skipped  []SkippedFile `json:"skipped"`
	// Copied reports that a screenshot was put on the clipboard.
	Copied bool `json:"copied"`
}

// Preview holds before and after previews for the comparison view.
type Preview struct {
	Before       string `json:"before"`
	After        string `json:"after"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	BeforeWidth  int    `json:"beforeWidth"`
	BeforeHeight int    `json:"beforeHeight"`
	AfterWidth   int    `json:"afterWidth"`
	AfterHeight  int    `json:"afterHeight"`
}

// ExportResult reports where compressed images went.
type ExportResult struct {
	Count    int      `json:"count"`
	Folder   string   `json:"folder"`
	Paths    []string `json:"paths"`
	Canceled bool     `json:"canceled"`
}

// AppInfo describes the runtime of the app.
type AppInfo struct {
	Version            string `json:"version"`
	Platform           string `json:"platform"`
	Arch               string `json:"arch"`
	GoVersion          string `json:"goVersion"`
	ScreenshotShortcut string `json:"screenshotShortcut"`
	ScreenshotActive   bool   `json:"screenshotActive"`
}

// AnyPic is the service bound to the frontend.
type AnyPic struct {
	mu sync.Mutex

	items    []*Item
	byID     map[int64]*Item
	nextID   int64
	settings Settings
	batch    Batch
	running  bool
	cancel   context.CancelFunc

	statePath string
	tempDir   string
	thumbSem  chan struct{}

	// The updater's remembered check result; downloading guards one
	// installer download at a time, and pendingInstaller is the one path
	// InstallUpdate may open (set on Windows and macOS).
	updateInfo       *UpdateInfo
	downloading      atomic.Bool
	pendingInstaller string

	// Files the OS handed over before the page was ready to listen (#6).
	pendingOpen  []string
	pageAttached bool
}

// ItemChanged is sent whenever an image changes while a batch runs.
var ItemChanged = mygo.NewEvent[Item]("anypic:item")

// OpenRequested tells the page that the OS handed the app a file: a file
// association, a Dock drop, or a command line path.
var OpenRequested = mygo.NewEvent[string]("anypic:open")

// BatchChanged is sent while a compression batch progresses.
var BatchChanged = mygo.NewEvent[Batch]("anypic:batch")

// CommandRequested lets the native menu ask the page to do something.
var CommandRequested = mygo.NewEvent[string]("anypic:command")

// NewAnyPic creates the service and loads persisted settings.
func NewAnyPic() *AnyPic {
	cleanupStaleShotDirs()
	a := &AnyPic{
		byID:     map[int64]*Item{},
		settings: defaultSettings(),
		thumbSem: make(chan struct{}, 3),
	}
	if dir, err := mygo.App.Path(mygo.PathUserData); err == nil {
		a.statePath = filepath.Join(dir, "settings.json")
		a.loadSettings()
	}
	return a
}

func defaultSettings() Settings {
	return Settings{
		Format:              "auto",
		Quality:             82,
		OutputMode:          "alongside",
		Suffix:              "-anypic",
		Theme:               "system",
		Language:            "system",
		AutoProcess:         "on",
		ScreenshotShortcut:  DefaultScreenshotShortcut,
		ScreenshotClipboard: "on",
		HideDockWhenTray:    "on",
		OpenAtLogin:         "off",
	}
}

func (a *AnyPic) loadSettings() {
	data, err := os.ReadFile(a.statePath)
	if err != nil {
		return
	}
	var s Settings
	if json.Unmarshal(data, &s) != nil {
		return
	}
	a.settings = normalizeSettings(s)
	// An explicit choice is known before the app is ready; "system" waits for
	// the desktop's languages (resolveLanguageNow, called at ready).
	if a.settings.Language != "system" {
		uiLang.Store(a.settings.Language)
	}
}

// State returns a snapshot of the queue.
func (a *AnyPic) State() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshotLocked()
}

// HideDockWhenTray reports whether the Dock icon should be removed while the
// window is hidden to the tray.
func (a *AnyPic) HideDockWhenTray() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.HideDockWhenTray != "off"
}

// AddPaths adds files, or the images inside directories. Adding while a
// batch runs is allowed: the new images wait for the next pass, which the
// automatic flow starts as soon as the current one finishes.
func (a *AnyPic) AddPaths(paths []string) (AddResult, error) {
	return a.addPaths(paths)
}

// PasteImage stores the image currently on the clipboard in Pictures/AnyPic,
// the same place screenshots live, and adds it to the queue. An explicit paste
// is the only moment AnyPic reads the clipboard for images.
func (a *AnyPic) PasteImage() (AddResult, error) {
	data := mygo.Clipboard.ReadImage()
	if len(data) == 0 {
		return AddResult{}, errNoClipboardImage
	}
	base, err := mygo.App.Path(mygo.PathPictures)
	if err != nil || base == "" {
		base, err = mygo.App.Path(mygo.PathUserData)
		if err != nil {
			return AddResult{}, errors.New(tr().GoErrNoSaveLocation())
		}
	}
	path, err := savePastedImage(data, base, time.Now().Format("2006-01-02 15.04.05"))
	if err != nil {
		return AddResult{}, fmt.Errorf("%s: %w", tr().GoErrSavePaste(), err)
	}
	result, err := a.AddPaths([]string{path})
	if err != nil {
		return AddResult{}, fmt.Errorf("%s: %w", tr().GoErrPastedNotQueued(path), err)
	}
	return result, nil
}

// savePastedImage writes a clipboard image into base/AnyPic and never
// overwrites an earlier paste. base is injected so tests do not touch the
// user's pictures folder.
func savePastedImage(data []byte, base, stamp string) (string, error) {
	if len(data) == 0 {
		return "", errNoClipboardImage
	}
	dir := filepath.Join(base, "AnyPic")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := uniquePath(filepath.Join(dir, tr().GoPastedName(stamp)+".png"))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// OpenFile receives a path the OS opened with the app: a file association,
// a drop on the Dock or taskbar icon, or a command line argument (#6).
// Until the page drains the buffer it queues there; afterwards it goes out
// as an event.
func (a *AnyPic) OpenFile(path string) {
	a.mu.Lock()
	if !a.pageAttached {
		a.pendingOpen = append(a.pendingOpen, path)
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	_ = OpenRequested.Broadcast(path)
}

// TakePendingOpen returns the paths opened before the page was ready and
// marks the page attached, so later opens arrive as events instead.
func (a *AnyPic) TakePendingOpen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pageAttached = true
	paths := a.pendingOpen
	a.pendingOpen = nil
	return paths
}

// PickFiles shows the native open dialog and adds the chosen files.
func (a *AnyPic) PickFiles(ctx context.Context) (AddResult, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       tr().GoDialogPickTitle(),
		ButtonLabel: tr().GoDialogAddButton(),
		Message:     tr().GoDialogPickMessage(),
		Multiple:    true,
		Filters: []mygo.FileFilter{
			{Name: tr().GoDialogImagesFilter(), Extensions: []string{"jpg", "jpeg", "png", "webp", "gif", "bmp", "tif", "tiff"}},
		},
	})
	if err != nil {
		return AddResult{}, err
	}
	if len(paths) == 0 {
		a.mu.Lock()
		snap := a.snapshotLocked()
		a.mu.Unlock()
		return AddResult{Snapshot: snap}, nil
	}
	return a.AddPaths(paths)
}

func (a *AnyPic) addPaths(paths []string) (AddResult, error) {
	candidates, skipped := collectImages(paths)

	a.mu.Lock()
	existing := make(map[string]bool, len(a.items))
	for _, item := range a.items {
		existing[item.Path] = true
	}
	// Hold the lock while reading metadata only for the dedupe set; image
	// decoding happens outside so the UI never waits on a large photo.
	a.mu.Unlock()

	var newItems []*Item
	for _, path := range candidates {
		if len(newItems) >= maxBatchSize {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: tr().GoSkippedTooMany()})
			break
		}
		info, err := os.Stat(path)
		if err != nil {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: tr().GoSkippedUnreadable()})
			continue
		}
		if info.Size() > maxFileSize {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: tr().GoSkippedTooLarge()})
			continue
		}
		if existing[path] {
			continue
		}

		frames, err := animationFrameCount(path)
		if err != nil {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: tr().GoSkippedUnreadable()})
			continue
		}

		f, err := os.Open(path)
		if err != nil {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: tr().GoSkippedUnreadable()})
			continue
		}
		cfg, format, err := image.DecodeConfig(f)
		f.Close()
		if err != nil {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: tr().GoSkippedUnsupported()})
			continue
		}
		item := &Item{
			Name:         filepath.Base(path),
			Path:         path,
			Dir:          filepath.Dir(path),
			Format:       displayFormat(format),
			Width:        cfg.Width,
			Height:       cfg.Height,
			OriginalSize: info.Size(),
			Status:       statusReady,
			Animated:     frames > 1,
		}
		newItems = append(newItems, item)
		existing[path] = true
	}

	a.mu.Lock()
	for _, item := range newItems {
		a.nextID++
		item.ID = a.nextID
		a.items = append(a.items, item)
		a.byID[item.ID] = item
	}
	snap := a.snapshotLocked()
	a.mu.Unlock()

	for _, item := range newItems {
		go a.makeThumb(item.ID)
	}

	return AddResult{Snapshot: snap, Added: len(newItems), Skipped: skipped}, nil
}

// Remove deletes images from the queue.
func (a *AnyPic) Remove(ids []int64) (Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return Snapshot{}, errors.New(tr().GoErrBusyRemove())
	}
	drop := make(map[int64]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	kept := a.items[:0]
	for _, item := range a.items {
		if drop[item.ID] {
			a.removeTemp(item)
			delete(a.byID, item.ID)
			continue
		}
		kept = append(kept, item)
	}
	a.items = kept
	return a.snapshotLocked(), nil
}

// Clear empties the queue.
func (a *AnyPic) Clear() (Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return Snapshot{}, errors.New(tr().GoErrBusyClear())
	}
	for _, item := range a.items {
		a.removeTemp(item)
	}
	a.items = nil
	a.byID = map[int64]*Item{}
	a.nextID = 0
	a.batch = Batch{}
	return a.snapshotLocked(), nil
}

// setOpenAtLogin mirrors the preference to the system's login items. A
// variable so tests can refuse it without touching the real ones.
var setOpenAtLogin = func(on bool) error { return mygo.App.SetOpenAtLogin(on) }

// SaveSettings validates and persists settings.
func (a *AnyPic) SaveSettings(s Settings) (Settings, error) {
	s = normalizeSettings(s)
	a.mu.Lock()
	// The shortcut is only changed through SetScreenshotShortcut, which also
	// rebinds it; a stale page must not persist a binding it did not make.
	s.ScreenshotShortcut = a.settings.ScreenshotShortcut
	openAtLogin := s.OpenAtLogin == "on"
	changed := s.OpenAtLogin != a.settings.OpenAtLogin
	languageChanged := s.Language != a.settings.Language
	a.settings = s
	a.mu.Unlock()
	// Menus, dialogs and later errors take the new language at once (#32).
	if languageChanged {
		changeLanguage(s.Language)
		notifyLanguageChanged()
	}
	// Mirror the preference to the system login item before persisting; if
	// the system refuses, the stored value follows what the system has.
	if changed {
		if err := setOpenAtLogin(openAtLogin); err != nil {
			a.mu.Lock()
			a.settings.OpenAtLogin = "off"
			a.mu.Unlock()
			if openAtLogin {
				s.OpenAtLogin = "off"
			}
			_ = a.persistSettings()
			// The system's reason follows our own words (#32): on its own it
			// is the framework's English, whatever the app shows.
			return s, fmt.Errorf("%s: %w", tr().GoErrLoginItem(), err)
		}
	}
	if err := a.persistSettings(); err != nil {
		return s, err
	}
	return s, nil
}

// OpenAtLoginSetting reports the app's startup-at-login preference as the
// system sees it, so a stale page or another way of toggling the login item
// keeps the switch honest.
func (a *AnyPic) OpenAtLoginSetting() (bool, error) {
	enabled := mygo.App.OpenAtLogin()
	a.mu.Lock()
	if (a.settings.OpenAtLogin == "on") != enabled {
		a.settings.OpenAtLogin = map[bool]string{true: "on", false: "off"}[enabled]
		_ = a.persistSettings()
	}
	a.mu.Unlock()
	return enabled, nil
}

// SetScreenshotShortcut rebinds the global screenshot shortcut and saves it.
// An empty accelerator restores the default.
func (a *AnyPic) SetScreenshotShortcut(acc string) (Settings, error) {
	acc = strings.TrimSpace(acc)
	if acc == "" {
		acc = DefaultScreenshotShortcut
	}
	a.mu.Lock()
	prev := a.settings.ScreenshotShortcut
	a.mu.Unlock()
	if acc == prev {
		return a.State().Settings, nil
	}
	if err := replaceScreenshotShortcut(prev, acc); err != nil {
		return a.State().Settings, fmt.Errorf("%s", tr().GoErrShortcutTaken(acc))
	}
	a.mu.Lock()
	a.settings.ScreenshotShortcut = acc
	s := a.settings
	a.mu.Unlock()
	if err := a.persistSettings(); err != nil {
		return s, err
	}
	return s, nil
}

// SetShortcutRecording releases the global binding while the page records a
// new accelerator, so pressing the old one does not start a capture.
func (a *AnyPic) SetShortcutRecording(active bool) {
	a.mu.Lock()
	acc := a.settings.ScreenshotShortcut
	a.mu.Unlock()
	shortcutMu.Lock()
	defer shortcutMu.Unlock()
	if active {
		mygo.GlobalShortcut.Unregister(acc)
		return
	}
	if !mygo.GlobalShortcut.IsRegistered(acc) {
		_ = mygo.GlobalShortcut.Register(acc, screenshotRequested)
	}
}

// RegisterScreenshotShortcut binds the stored shortcut once the app is ready.
func (a *AnyPic) RegisterScreenshotShortcut() error {
	a.mu.Lock()
	acc := a.settings.ScreenshotShortcut
	a.mu.Unlock()
	return replaceScreenshotShortcut("", acc)
}

// DecorateItem re-renders an image in the queue with the given FrameStyle —
// the beautify workbench's apply — and makes the decorated file the item's
// new source. The original file stays on disk untouched; the decorated image
// is written beside it as 原名-美化.png and the item resets to ready so the
// automatic flow picks it up.
func (a *AnyPic) DecorateItem(id int64, style FrameStyle) (Snapshot, error) {
	style = normalizeFrameStyle(style)

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return Snapshot{}, errors.New(tr().GoErrBusy())
	}
	item := a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return Snapshot{}, errors.New(tr().GoErrNoItem())
	}
	if item.Animated {
		// Beautify re-renders the source as a still PNG; an animation would
		// silently lose its frames.
		a.mu.Unlock()
		return Snapshot{}, errors.New(tr().GoErrAnimatedBeautify())
	}
	path, dir, name := item.Path, item.Dir, item.Name
	a.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return Snapshot{}, errors.New(tr().GoErrReadImage())
	}
	img, err := decodeImage(f, true)
	f.Close()
	if err != nil {
		return Snapshot{}, errors.New(tr().GoErrDecodeImage())
	}

	out := decorateScreenshot(img, style)
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, out); err != nil {
		return Snapshot{}, errors.New(tr().GoErrEncodeBeautify())
	}

	dest := uniquePath(filepath.Join(dir, outputFilename(name, tr().GoDecoratedSuffix(), "png")))
	if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
		return Snapshot{}, errors.New(tr().GoErrSaveBeautify())
	}

	a.mu.Lock()
	if a.running {
		// A pass may have started while the image was being decorated; the
		// batch owns the item's status, so step aside and clean up.
		a.mu.Unlock()
		_ = os.Remove(dest)
		return Snapshot{}, errors.New(tr().GoErrBusy())
	}
	item = a.byID[id]
	if item == nil {
		// The item was removed while the image was being decorated.
		a.mu.Unlock()
		_ = os.Remove(dest)
		return Snapshot{}, errors.New(tr().GoErrNoItem())
	}
	a.removeTemp(item)
	item.Path = dest
	item.Dir = dir
	item.Name = filepath.Base(dest)
	item.Format = "PNG"
	item.OriginalSize = int64(buf.Len())
	item.Width = out.Bounds().Dx()
	item.Height = out.Bounds().Dy()
	item.Status = statusReady
	item.Error = ""
	item.OutputFormat = ""
	item.OutputName = ""
	item.PassedThrough = false
	item.TransparencyKept = false
	item.SavedPercent = 0
	item.CompressedSize = 0
	snapshot := *item
	snap := a.snapshotLocked()
	a.mu.Unlock()

	go a.makeThumb(id)
	_ = ItemChanged.Broadcast(snapshot)
	return snap, nil
}

// PreviewDecorated renders the beautify workbench preview for one item at the
// page's preview scale, as a data URL. It is read-only: nothing is written.
func (a *AnyPic) PreviewDecorated(id int64, style FrameStyle) (string, error) {
	a.mu.Lock()
	item := a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return "", errors.New(tr().GoErrNoItem())
	}
	if item.Animated {
		a.mu.Unlock()
		return "", errors.New(tr().GoErrAnimatedBeautify())
	}
	path := item.Path
	a.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return "", errors.New(tr().GoErrReadImage())
	}
	img, err := decodeImage(f, true)
	f.Close()
	if err != nil {
		return "", errors.New(tr().GoErrDecodeImage())
	}

	// Render the frame at the preview's own scale instead of drawing a
	// 4600px canvas for a 1600px stage. decorateScaled computes the geometry
	// at full size and shrinks every length together, so the preview is the
	// exported frame, smaller — corners, shadow and pattern grain included.
	ob := img.Bounds()
	g := computeFrame(ob.Dx(), ob.Dy(), style)
	scale := min(1.0, float64(previewMaxSide)/float64(max(g.canvasW, g.canvasH)))
	out := decorateScaled(img, style, scale)
	// Without a backdrop the canvas keeps transparent margins, so the preview
	// must carry alpha; a backdrop makes it fully opaque.
	return dataURL(out, style.Background == "none", 90, previewMaxSide)
}

// ChooseOutputFolder picks a folder where exports are written.
func (a *AnyPic) ChooseOutputFolder(ctx context.Context) (Settings, error) {
	folder, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       tr().GoDialogExportFolderTitle(),
		ButtonLabel: tr().GoDialogChooseButton(),
		Directory:   true,
	})
	if err != nil || len(folder) == 0 {
		a.mu.Lock()
		s := a.settings
		a.mu.Unlock()
		return s, err
	}
	a.mu.Lock()
	a.settings.OutputMode = "folder"
	a.settings.OutputFolder = folder[0]
	s := a.settings
	a.mu.Unlock()
	if err := a.persistSettings(); err != nil {
		return s, err
	}
	return s, nil
}

// Compress starts compressing every image in the queue.
func (a *AnyPic) Compress(s Settings) error {
	return a.startCompression(s, false)
}

// CompressReady starts a pass over the images that have not been compressed
// yet. The automatic flow uses it to pick up images added while an earlier
// pass was running without re-encoding the ones already finished.
func (a *AnyPic) CompressReady(s Settings) error {
	return a.startCompression(s, true)
}

// CompressFailed resets the images that failed the previous pass (#11) and
// starts the same kind of pass CompressReady runs, so finished images are
// never re-encoded by a retry.
func (a *AnyPic) CompressFailed(s Settings) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New(tr().GoErrAlreadyRunning())
	}
	found := false
	for _, item := range a.items {
		if item.Status == statusError {
			item.Status = statusReady
			item.Error = ""
			found = true
		}
	}
	a.mu.Unlock()
	if !found {
		return errors.New(tr().GoErrNoFailed())
	}
	return a.startCompression(s, true)
}

func (a *AnyPic) startCompression(s Settings, onlyReady bool) error {
	s = normalizeSettings(s)

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New(tr().GoErrAlreadyRunning())
	}
	ids := make([]int64, 0, len(a.items))
	for _, item := range a.items {
		if onlyReady && item.Status != statusReady {
			continue
		}
		ids = append(ids, item.ID)
		item.stale = false
		item.Status = statusReady
		item.Error = ""
		item.saved = false
	}
	if len(ids) == 0 {
		a.mu.Unlock()
		if onlyReady {
			// Nothing to do is not an error for an automatic pass.
			return nil
		}
		return errors.New(tr().GoErrAddFirst())
	}
	a.settings = s
	a.running = true
	a.batch = Batch{Running: true, Total: len(ids)}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.mu.Unlock()

	if err := a.persistSettings(); err != nil {
		// Settings persistence must not stop a compression pass.
		_ = err
	}
	a.emitBatch()

	go a.runBatch(ctx, ids, s)
	return nil
}

// Cancel stops a compression pass.
func (a *AnyPic) Cancel() {
	a.mu.Lock()
	if !a.running || a.cancel == nil {
		a.mu.Unlock()
		return
	}
	a.batch.Canceled = true
	cancel := a.cancel
	a.mu.Unlock()
	cancel()
	a.emitBatch()
}

func (a *AnyPic) runBatch(ctx context.Context, ids []int64, s Settings) {
	workers := min(3, max(1, runtime.NumCPU()))
	jobs := make(chan int64)
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				if ctx.Err() != nil {
					continue
				}
				a.compressItem(ctx, id, s)
			}
		}()
	}

	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		jobs <- id
	}
	close(jobs)
	wg.Wait()

	a.mu.Lock()
	a.running = false
	a.cancel = nil
	a.batch.Running = false
	a.batch.Canceled = a.batch.Canceled || ctx.Err() != nil
	batch := a.batch
	a.mu.Unlock()
	_ = BatchChanged.Broadcast(batch)
}

func (a *AnyPic) compressItem(ctx context.Context, id int64, s Settings) {
	a.mu.Lock()
	item := a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return
	}
	item.Status = statusWorking
	item.Error = ""
	item.PassedThrough = false
	item.TransparencyKept = false
	item.SavedPercent = 0
	item.CompressedSize = 0
	item.saved = false
	snapshot := *item
	a.mu.Unlock()
	_ = ItemChanged.Broadcast(snapshot)

	result, err := a.compressOne(ctx, id, item.Path, item.Format, item.Name, item.Animated, s)

	a.mu.Lock()
	item = a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return
	}
	if err != nil {
		item.Status = statusError
		item.Error = errorText(err)
	} else {
		a.removeTemp(item)
		item.Status = statusDone
		item.CompressedSize = result.size
		item.OutputFormat = result.format
		item.OutputName = result.outputName
		item.compressedPath = result.path
		item.Width = result.width
		item.Height = result.height
		item.HasAlpha = result.hasAlpha
		item.PassedThrough = result.passedThrough
		item.TransparencyKept = result.transparencyKept
		if item.OriginalSize > 0 {
			item.SavedPercent = float64(item.OriginalSize-result.size) / float64(item.OriginalSize) * 100
			if item.SavedPercent < 0 {
				item.SavedPercent = 0
			}
		}
	}
	if err != nil {
		a.batch.Failed++
	}
	a.batch.Done++
	batch := a.batch
	snapshot = *item
	a.mu.Unlock()

	_ = ItemChanged.Broadcast(snapshot)
	_ = BatchChanged.Broadcast(batch)
}

type compressResult struct {
	path             string
	size             int64
	format           string
	outputName       string
	width            int
	height           int
	hasAlpha         bool
	passedThrough    bool
	transparencyKept bool
}

func (a *AnyPic) compressOne(ctx context.Context, id int64, path, srcFormat, name string, animated bool, s Settings) (compressResult, error) {
	if animated {
		// Animations never take the still path: the pipeline must keep every
		// frame, never flatten to the first one (#8).
		return a.compressAnimated(ctx, id, path, srcFormat, name, s)
	}
	if err := ctx.Err(); err != nil {
		return compressResult{}, err
	}

	f, err := os.Open(path)
	if err != nil {
		return compressResult{}, errors.New(tr().GoErrReadOriginal())
	}
	img, err := decodeImage(f, true)
	f.Close()
	if err != nil {
		return compressResult{}, errors.New(tr().GoErrDecodeCorrupt())
	}

	resized := false
	if s.MaxDimension > 0 {
		b := img.Bounds()
		if max(b.Dx(), b.Dy()) > s.MaxDimension {
			img = imaging.Fit(img, s.MaxDimension, s.MaxDimension, imaging.Lanczos)
			resized = true
		}
	}

	hasAlpha := imageHasAlpha(img, srcFormat)
	outFormat := chooseOutputFormat(s.Format, srcFormat, hasAlpha)
	// The user asked for JPG but the image has transparency, so the output is
	// PNG; the page says so instead of silently flattening the image (#15).
	transparencyKept := s.Format == "jpeg" && hasAlpha && outFormat == "PNG"
	outputName := outputFilename(name, s.Suffix, extForFormat(outFormat))

	var buf bytes.Buffer
	switch outFormat {
	case "JPEG":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: s.Quality}); err != nil {
			return compressResult{}, errors.New(tr().GoErrEncodeJPEG())
		}
	case "PNG":
		enc := png.Encoder{CompressionLevel: pngCompressionLevel(img.Bounds().Dx() * img.Bounds().Dy())}
		if err := enc.Encode(&buf, img); err != nil {
			return compressResult{}, errors.New(tr().GoErrEncodePNG())
		}
	case "WEBP":
		opts := webp.Options{Quality: s.Quality, Method: 4}
		if err := webp.Encode(&buf, img, opts); err != nil {
			return compressResult{}, errors.New(tr().GoErrEncodeWebP())
		}
	default:
		return compressResult{}, errors.New(tr().GoErrUnsupportedOutput())
	}

	if err := ctx.Err(); err != nil {
		return compressResult{}, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return compressResult{}, errors.New(tr().GoErrReadOriginal())
	}
	b := img.Bounds()

	// Never make a file larger when the format stays the same and no resize
	// was requested: the original is already better.
	if !resized && strings.EqualFold(outFormat, srcFormat) && int64(buf.Len()) >= info.Size() {
		return compressResult{
			path:             path,
			size:             info.Size(),
			format:           displayFormat(strings.ToLower(srcFormat)),
			outputName:       outputFilename(name, s.Suffix, extForFormat(srcFormat)),
			width:            b.Dx(),
			height:           b.Dy(),
			hasAlpha:         hasAlpha,
			passedThrough:    true,
			transparencyKept: transparencyKept,
		}, nil
	}

	dir, err := a.ensureTempDir()
	if err != nil {
		return compressResult{}, errors.New(tr().GoErrCreateTemp())
	}
	ext := extForFormat(outFormat)
	tmp, err := os.CreateTemp(dir, fmt.Sprintf("%d-*.%s", id, ext))
	if err != nil {
		return compressResult{}, errors.New(tr().GoErrCreateTemp())
	}
	outPath := tmp.Name()
	tmp.Close()
	if err := os.WriteFile(outPath, buf.Bytes(), 0o600); err != nil {
		_ = os.Remove(outPath)
		return compressResult{}, errors.New(tr().GoErrWriteResult())
	}

	return compressResult{
		path:             outPath,
		size:             int64(buf.Len()),
		format:           outFormat,
		outputName:       outputName,
		width:            b.Dx(),
		height:           b.Dy(),
		hasAlpha:         hasAlpha,
		transparencyKept: transparencyKept,
	}, nil
}

// Preview returns before/after images for the comparison view.
func (a *AnyPic) Preview(id int64) (Preview, error) {
	a.mu.Lock()
	item := a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return Preview{}, errors.New(tr().GoErrNoItem())
	}
	if item.Status != statusDone {
		a.mu.Unlock()
		return Preview{}, errors.New(tr().GoErrCompressFirst())
	}
	src := item.Path
	out := item.compressedPath
	a.mu.Unlock()

	before, beforeW, beforeH, err := previewImage(src)
	if err != nil {
		return Preview{}, errors.New(tr().GoErrPreviewOriginal())
	}
	after, afterW, afterH, err := previewImage(out)
	if err != nil {
		return Preview{}, errors.New(tr().GoErrPreviewCompressed())
	}
	w, h := beforeW, beforeH
	if w < 1 || h < 1 {
		w, h = 1, 1
	}
	return Preview{
		Before: before, After: after, Width: w, Height: h,
		BeforeWidth: beforeW, BeforeHeight: beforeH,
		AfterWidth: afterW, AfterHeight: afterH,
	}, nil
}

// ExportAll writes every compressed image to the chosen folder.
func (a *AnyPic) ExportAll(ctx context.Context) (ExportResult, error) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return ExportResult{}, errors.New(tr().GoErrWaitCompression())
	}
	done := make([]Item, 0, len(a.items))
	for _, item := range a.items {
		if item.Status == statusDone {
			done = append(done, *item)
		}
	}
	folder := a.settings.OutputFolder
	mode := a.settings.OutputMode
	a.mu.Unlock()

	if len(done) == 0 {
		return ExportResult{}, errors.New(tr().GoErrNothingToExport())
	}
	if mode != "folder" || folder == "" {
		chosen, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      mygo.CallerWindow(ctx),
			Title:       tr().GoDialogExportFolderTitle(),
			ButtonLabel: tr().GoDialogExportHereButton(),
			Directory:   true,
		})
		if err != nil {
			return ExportResult{}, err
		}
		if len(chosen) == 0 {
			return ExportResult{Canceled: true}, nil
		}
		folder = chosen[0]
		a.mu.Lock()
		a.settings.OutputMode = "folder"
		a.settings.OutputFolder = folder
		a.mu.Unlock()
		_ = a.persistSettings()
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return ExportResult{}, errors.New(tr().GoErrCreateExportFolder())
	}

	result := ExportResult{Folder: folder}
	saved := make([]int64, 0, len(done))
	for _, item := range done {
		src := item.compressedPath
		if item.PassedThrough || src == "" {
			src = item.Path
		}
		dest := uniquePath(filepath.Join(folder, item.OutputName))
		if err := copyFile(src, dest); err != nil {
			a.markResultsSaved(saved)
			return result, fmt.Errorf("%s: %w", tr().GoErrExportItem(item.Name), err)
		}
		result.Paths = append(result.Paths, dest)
		result.Count++
		saved = append(saved, item.ID)
	}
	a.markResultsSaved(saved)
	return result, nil
}

// ExportProcessed saves the listed compressed images without asking where:
// into the configured output folder, or beside each original. It is the
// saving half of the automatic flow, so it never opens a dialog.
func (a *AnyPic) ExportProcessed(ids []int64) (ExportResult, error) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return ExportResult{}, errors.New(tr().GoErrWaitCompression())
	}
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	done := make([]Item, 0, len(ids))
	for _, item := range a.items {
		if want[item.ID] && item.Status == statusDone {
			done = append(done, *item)
		}
	}
	folder := a.settings.OutputFolder
	mode := a.settings.OutputMode
	a.mu.Unlock()

	if len(done) == 0 {
		return ExportResult{}, errors.New(tr().GoErrNothingToExport())
	}

	result := ExportResult{}
	dirs := map[string]bool{}
	saved := make([]int64, 0, len(done))
	for _, item := range done {
		destDir := item.Dir
		if mode == "folder" && folder != "" {
			destDir = folder
		}
		src := item.compressedPath
		if item.PassedThrough || src == "" {
			src = item.Path
		}
		dest := uniquePath(filepath.Join(destDir, item.OutputName))
		if err := copyFile(src, dest); err != nil {
			a.markResultsSaved(saved)
			return result, fmt.Errorf("%s: %w", tr().GoErrSaveItem(item.Name), err)
		}
		result.Paths = append(result.Paths, dest)
		result.Count++
		saved = append(saved, item.ID)
		if result.Folder == "" {
			result.Folder = destDir
		}
		dirs[destDir] = true
	}
	a.markResultsSaved(saved)
	// A single folder is worth offering to reveal; mixed "alongside" outputs
	// are spread over several folders, so the caller reveals the first file.
	if len(dirs) > 1 {
		result.Folder = ""
	}
	return result, nil
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// CopyItemToClipboard puts a compressed image on the clipboard, falling back
// to the original when compression did not finish. The clipboard takes PNG,
// so results encoded as JPEG or WebP are decoded and re-encoded first.
func (a *AnyPic) CopyItemToClipboard(id int64) error {
	a.mu.Lock()
	item := a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return errors.New(tr().GoErrNoItem())
	}
	src := item.Path
	if item.Status == statusDone && item.compressedPath != "" && !item.PassedThrough {
		src = item.compressedPath
	}
	a.mu.Unlock()

	data, err := os.ReadFile(src)
	if err != nil {
		return errors.New(tr().GoErrReadImage())
	}
	if !bytes.HasPrefix(data, pngSignature) {
		img, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
		if err != nil {
			return errors.New(tr().GoErrCopyImage())
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return errors.New(tr().GoErrCopyImage())
		}
		data = buf.Bytes()
	}
	if err := mygo.Clipboard.WriteImage(data); err != nil {
		return errors.New(tr().GoErrWriteClipboard())
	}
	return nil
}

// saveDialog asks where to write one exported image. A package variable so
// tests can answer without a native save dialog; the app always uses mygo's.
var saveDialog = mygo.Dialog.Save

// ExportOne asks where to save one compressed image.
func (a *AnyPic) ExportOne(ctx context.Context, id int64) (ExportResult, error) {
	a.mu.Lock()
	item := a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return ExportResult{}, errors.New(tr().GoErrNoItem())
	}
	if item.Status != statusDone {
		a.mu.Unlock()
		return ExportResult{}, errors.New(tr().GoErrCompressFirst())
	}
	copyItem := *item
	folder := a.settings.OutputFolder
	a.mu.Unlock()

	defaultPath := filepath.Join(copyItem.Dir, copyItem.OutputName)
	if folder != "" {
		defaultPath = filepath.Join(folder, copyItem.OutputName)
	}
	dest, err := saveDialog(mygo.SaveDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       tr().GoDialogExportImageTitle(),
		DefaultPath: defaultPath,
		ButtonLabel: tr().GoDialogExportButton(),
	})
	if err != nil {
		return ExportResult{}, err
	}
	if dest == "" {
		return ExportResult{Canceled: true}, nil
	}
	src := copyItem.compressedPath
	if copyItem.PassedThrough || src == "" {
		src = copyItem.Path
	}
	if err := copyFile(src, dest); err != nil {
		// As exporting every image says it (ExportAll).
		return ExportResult{}, fmt.Errorf("%s: %w", tr().GoErrExportItem(copyItem.Name), err)
	}
	a.markResultsSaved([]int64{id})
	return ExportResult{Count: 1, Folder: filepath.Dir(dest), Paths: []string{dest}}, nil
}

// RevealPath shows a file or folder in Finder or Explorer.
func (a *AnyPic) RevealPath(path string) {
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		_ = mygo.Shell.OpenPath(path)
		return
	}
	mygo.Shell.ShowItemInFolder(path)
}

// Info returns app and runtime information.
func (a *AnyPic) Info() AppInfo {
	a.mu.Lock()
	shortcut := a.settings.ScreenshotShortcut
	a.mu.Unlock()
	// The packaged app's version comes from the bundle (mygo.config.ts at
	// build time), so releases stop depending on remembering to bump a
	// constant; the constant only covers unpackaged dev builds.
	version := mygo.App.Version()
	if version == "" {
		version = appVersion
	}
	return AppInfo{
		Version:            version,
		Platform:           runtime.GOOS,
		Arch:               runtime.GOARCH,
		GoVersion:          runtime.Version(),
		ScreenshotShortcut: shortcut,
		ScreenshotActive:   mygo.GlobalShortcut.IsRegistered(shortcut),
	}
}

// markResultsSaved records that copies of the given items' compressed results
// now exist outside the session temp directory, so quitting will not lose
// them (see UnsavedResults).
func (a *AnyPic) markResultsSaved(ids []int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range ids {
		if item := a.byID[id]; item != nil {
			item.saved = true
		}
	}
}

// UnsavedResults reports how many compressed images exist only in this
// session's temporary directory: the item is done, it did not pass through
// (a passed-through result is the original file), and no export has written a
// copy to disk. Cleanup removes the temp files on quit (invariant 4), so
// these results would be lost.
func (a *AnyPic) UnsavedResults() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	count := 0
	for _, item := range a.items {
		if item.Status == statusDone && !item.saved && !item.PassedThrough && item.compressedPath != "" {
			count++
		}
	}
	return count
}

// Cleanup removes this session's temporary files.
func (a *AnyPic) Cleanup() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
	for _, item := range a.items {
		a.removeTemp(item)
	}
	if a.tempDir != "" {
		_ = os.RemoveAll(a.tempDir)
		a.tempDir = ""
	}
}

func (a *AnyPic) snapshotLocked() Snapshot {
	items := make([]Item, 0, len(a.items))
	for _, item := range a.items {
		items = append(items, *item)
	}
	return Snapshot{Items: items, Settings: a.settings, Batch: a.batch}
}

func (a *AnyPic) emitBatch() {
	a.mu.Lock()
	batch := a.batch
	a.mu.Unlock()
	_ = BatchChanged.Broadcast(batch)
}

func (a *AnyPic) persistSettings() error {
	a.mu.Lock()
	data, err := json.MarshalIndent(a.settings, "", "  ")
	a.mu.Unlock()
	if err == nil && a.statePath == "" {
		return nil
	}
	if err == nil {
		err = os.MkdirAll(filepath.Dir(a.statePath), 0o755)
	}
	if err == nil {
		err = os.WriteFile(a.statePath, data, 0o644)
	}
	if err != nil {
		// The system's reason follows our own words (#32).
		return fmt.Errorf("%s: %w", tr().GoErrSaveSettings(), err)
	}
	return nil
}

// decodeImage decodes an image, falling back to the JPEG-strip TIFF reader
// for files the standard decoder rejects (see tiff_jpeg.go).
func decodeImage(f *os.File, autoOrientation bool) (image.Image, error) {
	var img image.Image
	var err error
	if autoOrientation {
		img, err = imaging.Decode(f, imaging.AutoOrientation(true))
	} else {
		img, err = imaging.Decode(f)
	}
	if err == nil {
		return img, nil
	}
	if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
		return nil, err
	}
	if img, tiffErr := decodeJPEGTIFF(f); tiffErr == nil {
		return img, nil
	}
	return nil, err
}

func (a *AnyPic) makeThumb(id int64) {
	a.thumbSem <- struct{}{}
	defer func() { <-a.thumbSem }()

	a.mu.Lock()
	item := a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return
	}
	path := item.Path
	a.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return
	}
	img, err := decodeImage(f, false)
	f.Close()
	if err != nil {
		return
	}
	b := img.Bounds()
	hasAlpha := imageHasAlpha(img, "")
	if max(b.Dx(), b.Dy()) > thumbMaxSide {
		img = imaging.Fit(img, thumbMaxSide, thumbMaxSide, imaging.Lanczos)
	}
	thumb, err := dataURL(img, hasAlpha, 80, thumbMaxSide)
	if err != nil {
		return
	}

	a.mu.Lock()
	item = a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return
	}
	item.Thumb = thumb
	item.HasAlpha = hasAlpha
	snapshot := *item
	a.mu.Unlock()
	_ = ItemChanged.Broadcast(snapshot)
}

func (a *AnyPic) ensureTempDir() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tempDir != "" {
		return a.tempDir, nil
	}
	cache, err := mygo.App.Path(mygo.PathCache)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(cache, "session-")
	if err != nil {
		return "", err
	}
	a.tempDir = dir
	return dir, nil
}

func (a *AnyPic) removeTemp(item *Item) {
	if item.compressedPath != "" && item.compressedPath != item.Path {
		_ = os.Remove(item.compressedPath)
	}
	item.compressedPath = ""
}

// isAnimatedFile reports whether a GIF or WebP holds more than one frame.
// Static images of both formats are supported; animations are skipped rather
// than silently flattened to their first frame. Frame counting walks the file
// structure without decoding pixels (see animation.go).
func isAnimatedFile(path string) (bool, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gif", ".webp":
		n, err := animationFrameCount(path)
		if err != nil {
			return false, err
		}
		return n > 1, nil
	}
	return false, nil
}

func collectImages(paths []string) ([]string, []SkippedFile) {
	var out []string
	var skipped []SkippedFile
	seen := map[string]bool{}

	addFile := func(path string) {
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		if len(out) >= maxBatchSize {
			return
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !supportedExtensions[ext] {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: tr().GoSkippedUnsupportedShort()})
			return
		}
		seen[path] = true
		out = append(out, path)
	}

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: tr().GoSkippedUnreadableShort()})
			continue
		}
		if !info.IsDir() {
			addFile(path)
			continue
		}
		err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if p != path && (strings.HasPrefix(name, ".") || name == "node_modules") {
					return filepath.SkipDir
				}
				return nil
			}
			if supportedExtensions[strings.ToLower(filepath.Ext(p))] {
				addFile(p)
			}
			return nil
		})
		if err != nil {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: tr().GoSkippedFolderUnreadable()})
		}
	}
	return out, skipped
}

func normalizeSettings(s Settings) Settings {
	switch s.Format {
	case "auto", "jpeg", "png", "webp":
	default:
		s.Format = "auto"
	}
	if s.Quality < 1 {
		s.Quality = 82
	}
	if s.Quality > 100 {
		s.Quality = 100
	}
	if s.MaxDimension < 0 {
		s.MaxDimension = 0
	}
	if s.MaxDimension > 0 && s.MaxDimension < 320 {
		s.MaxDimension = 320
	}
	if s.OutputMode != "folder" {
		s.OutputMode = "alongside"
	}
	s.Suffix = strings.TrimSpace(s.Suffix)
	s.Suffix = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == 0 {
			return -1
		}
		return r
	}, s.Suffix)
	if s.Suffix == "" {
		s.Suffix = "-anypic"
	}
	if len([]rune(s.Suffix)) > 32 {
		s.Suffix = string([]rune(s.Suffix)[:32])
	}
	switch s.Theme {
	case "system", "light", "dark":
	default:
		s.Theme = "system"
	}
	switch s.Language {
	case "system", "zh-Hans", "en":
	default:
		s.Language = "system"
	}
	// Auto-process is the default: older settings files have no preference,
	// and turning the one-step flow on is the friendlier reading of that.
	if s.AutoProcess != "off" {
		s.AutoProcess = "on"
	}
	s.ScreenshotShortcut = strings.TrimSpace(s.ScreenshotShortcut)
	if s.ScreenshotShortcut == "" {
		s.ScreenshotShortcut = DefaultScreenshotShortcut
	}
	// Older settings files have no clipboard preference; copy is the default.
	if s.ScreenshotClipboard != "off" {
		s.ScreenshotClipboard = "on"
	}
	// Older settings files have no Dock preference; hiding it is the default.
	if s.HideDockWhenTray != "off" {
		s.HideDockWhenTray = "on"
	}
	// The stored value only mirrors the system login item; anything unknown
	// reads as off, which OpenAtLoginSetting reports.
	if s.OpenAtLogin != "on" {
		s.OpenAtLogin = "off"
	}
	return s
}

func displayFormat(format string) string {
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		return "JPEG"
	case "png":
		return "PNG"
	case "webp":
		return "WEBP"
	case "gif":
		return "GIF"
	case "bmp":
		return "BMP"
	case "tiff", "tif":
		return "TIFF"
	}
	return strings.ToUpper(format)
}

func extForFormat(format string) string {
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		return "jpg"
	case "png":
		return "png"
	case "webp":
		return "webp"
	case "gif":
		return "gif"
	case "bmp":
		return "bmp"
	case "tiff", "tif":
		return "tif"
	}
	return "jpg"
}

// pngCompressionLevel picks the deflate effort by pixel count (#10):
// BestCompression saves a few percent on small images but costs seconds on
// multi-megapixel ones, where DefaultCompression keeps the output lossless,
// nearly as small, and much faster to produce.
func pngCompressionLevel(pixels int) png.CompressionLevel {
	if pixels <= 16<<20 {
		return png.BestCompression
	}
	return png.DefaultCompression
}

func chooseOutputFormat(setting, srcFormat string, hasAlpha bool) string {
	switch setting {
	case "jpeg":
		if hasAlpha {
			// JPG cannot store transparency and flattening would silently
			// paint a white background, so a transparent image is saved as
			// lossless PNG instead (invariant 3, #15).
			return "PNG"
		}
		return "JPEG"
	case "png":
		return "PNG"
	case "webp":
		return "WEBP"
	}
	switch strings.ToLower(srcFormat) {
	case "jpeg", "jpg":
		return "JPEG"
	case "png":
		return "PNG"
	case "webp":
		return "WEBP"
	}
	if hasAlpha {
		return "PNG"
	}
	return "JPEG"
}

func outputFilename(name, suffix, ext string) string {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	if base == "" {
		base = "image"
	}
	return base + suffix + "." + ext
}

func imageHasAlpha(img image.Image, srcFormat string) bool {
	switch displayFormat(srcFormat) {
	case "JPEG", "BMP":
		return false
	}
	switch img.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16:
		return false
	}
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return !o.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0xffff {
				return true
			}
		}
	}
	return false
}

func flattenOnWhite(img image.Image) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, b, img, b.Min, draw.Over)
	return dst
}

// dataURL encodes an image for the page, capping its larger side at maxSide.
// The queue thumbnails and the larger preview use the same encoding but very
// different limits, so the limit belongs to the caller.
func dataURL(img image.Image, alpha bool, quality, maxSide int) (string, error) {
	b := img.Bounds()
	if max(b.Dx(), b.Dy()) > maxSide {
		img = imaging.Fit(img, maxSide, maxSide, imaging.Lanczos)
	}
	var buf bytes.Buffer
	mime := "image/jpeg"
	if alpha {
		// PNG keeps transparency; quality does not apply to this branch.
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := enc.Encode(&buf, img); err != nil {
			return "", err
		}
		mime = "image/png"
	} else {
		if err := jpeg.Encode(&buf, flattenOnWhite(img), &jpeg.Options{Quality: quality}); err != nil {
			return "", err
		}
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func previewImage(path string) (string, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer f.Close()
	img, err := decodeImage(f, true)
	if err != nil {
		return "", 0, 0, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if max(w, h) > previewMaxSide {
		img = imaging.Fit(img, previewMaxSide, previewMaxSide, imaging.Lanczos)
		b = img.Bounds()
		w, h = b.Dx(), b.Dy()
	}
	alpha := imageHasAlpha(img, "")
	s, err := dataURL(img, alpha, 90, previewMaxSide)
	return s, w, h, err
}

func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s %d%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
