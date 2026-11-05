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
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

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
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Path           string  `json:"path"`
	Dir            string  `json:"dir"`
	Format         string  `json:"format"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	OriginalSize   int64   `json:"originalSize"`
	CompressedSize int64   `json:"compressedSize"`
	Status         string  `json:"status"`
	Error          string  `json:"error,omitempty"`
	OutputFormat   string  `json:"outputFormat"`
	OutputName     string  `json:"outputName"`
	Thumb          string  `json:"thumb,omitempty"`
	HasAlpha       bool    `json:"hasAlpha"`
	PassedThrough  bool    `json:"passedThrough"`
	SavedPercent   float64 `json:"savedPercent"`

	compressedPath string
	stale          bool
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
}

// ItemChanged is sent whenever an image changes while a batch runs.
var ItemChanged = mygo.NewEvent[Item]("anypic:item")

// BatchChanged is sent while a compression batch progresses.
var BatchChanged = mygo.NewEvent[Batch]("anypic:batch")

// CommandRequested lets the native menu ask the page to do something.
var CommandRequested = mygo.NewEvent[string]("anypic:command")

// NewAnyPic creates the service and loads persisted settings.
func NewAnyPic() *AnyPic {
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

// AddPaths adds files, or the images inside directories.
func (a *AnyPic) AddPaths(paths []string) (AddResult, error) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return AddResult{}, errors.New("正在压缩，请稍候再添加图片")
	}
	a.mu.Unlock()
	return a.addPaths(paths)
}

// PickFiles shows the native open dialog and adds the chosen files.
func (a *AnyPic) PickFiles(ctx context.Context) (AddResult, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       "选择图片",
		ButtonLabel: "添加",
		Message:     "选择要压缩的图片或文件夹",
		Multiple:    true,
		Filters: []mygo.FileFilter{
			{Name: "图片", Extensions: []string{"jpg", "jpeg", "png", "webp", "gif", "bmp", "tif", "tiff"}},
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
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: "一次最多添加 500 张图片"})
			break
		}
		info, err := os.Stat(path)
		if err != nil {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: "无法读取文件"})
			continue
		}
		if info.Size() > maxFileSize {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: "文件超过 250 MB"})
			continue
		}
		if existing[path] {
			continue
		}

		animated, err := isAnimatedFile(path)
		if err != nil {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: "无法读取文件"})
			continue
		}
		if animated {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: "暂不支持动态图片"})
			continue
		}

		f, err := os.Open(path)
		if err != nil {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: "无法读取文件"})
			continue
		}
		cfg, format, err := image.DecodeConfig(f)
		f.Close()
		if err != nil {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: "不支持的图片格式"})
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
		return Snapshot{}, errors.New("正在压缩，暂时不能移除图片")
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
		return Snapshot{}, errors.New("正在压缩，请先取消")
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

// SaveSettings validates and persists settings.
func (a *AnyPic) SaveSettings(s Settings) (Settings, error) {
	s = normalizeSettings(s)
	a.mu.Lock()
	// The shortcut is only changed through SetScreenshotShortcut, which also
	// rebinds it; a stale page must not persist a binding it did not make.
	s.ScreenshotShortcut = a.settings.ScreenshotShortcut
	openAtLogin := s.OpenAtLogin == "on"
	changed := s.OpenAtLogin != a.settings.OpenAtLogin
	a.settings = s
	a.mu.Unlock()
	// Mirror the preference to the system login item before persisting; if
	// the system refuses, the stored value follows what the system has.
	if changed {
		if err := mygo.App.SetOpenAtLogin(openAtLogin); err != nil {
			a.mu.Lock()
			a.settings.OpenAtLogin = "off"
			a.mu.Unlock()
			if openAtLogin {
				s.OpenAtLogin = "off"
			}
			_ = a.persistSettings()
			return s, err
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
		return a.State().Settings, fmt.Errorf("无法使用 %s：可能已被其他应用占用", acc)
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

// ChooseOutputFolder picks a folder where exports are written.
func (a *AnyPic) ChooseOutputFolder(ctx context.Context) (Settings, error) {
	folder, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       "选择导出文件夹",
		ButtonLabel: "选择",
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
	s = normalizeSettings(s)

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New("已经有一轮压缩正在进行")
	}
	if len(a.items) == 0 {
		a.mu.Unlock()
		return errors.New("请先添加图片")
	}
	ids := make([]int64, 0, len(a.items))
	for _, item := range a.items {
		ids = append(ids, item.ID)
		item.stale = false
		item.Status = statusReady
		item.Error = ""
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
	item.SavedPercent = 0
	item.CompressedSize = 0
	snapshot := *item
	a.mu.Unlock()
	_ = ItemChanged.Broadcast(snapshot)

	result, err := a.compressOne(ctx, id, item.Path, item.Format, item.Name, s)

	a.mu.Lock()
	item = a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return
	}
	if err != nil {
		item.Status = statusError
		item.Error = err.Error()
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
	path          string
	size          int64
	format        string
	outputName    string
	width         int
	height        int
	hasAlpha      bool
	passedThrough bool
}

func (a *AnyPic) compressOne(ctx context.Context, id int64, path, srcFormat, name string, s Settings) (compressResult, error) {
	if err := ctx.Err(); err != nil {
		return compressResult{}, err
	}

	f, err := os.Open(path)
	if err != nil {
		return compressResult{}, errors.New("无法读取原图")
	}
	img, err := decodeImage(f, true)
	f.Close()
	if err != nil {
		return compressResult{}, errors.New("图片解码失败，可能已损坏")
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
	outputName := outputFilename(name, s.Suffix, extForFormat(outFormat))

	var buf bytes.Buffer
	switch outFormat {
	case "JPEG":
		toEncode := img
		if hasAlpha {
			toEncode = flattenOnWhite(img)
			hasAlpha = false
		}
		if err := jpeg.Encode(&buf, toEncode, &jpeg.Options{Quality: s.Quality}); err != nil {
			return compressResult{}, errors.New("JPEG 编码失败")
		}
	case "PNG":
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return compressResult{}, errors.New("PNG 编码失败")
		}
	case "WEBP":
		opts := webp.Options{Quality: s.Quality, Method: 4}
		if err := webp.Encode(&buf, img, opts); err != nil {
			return compressResult{}, errors.New("WebP 编码失败")
		}
	default:
		return compressResult{}, errors.New("不支持的输出格式")
	}

	if err := ctx.Err(); err != nil {
		return compressResult{}, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return compressResult{}, errors.New("无法读取原图")
	}
	b := img.Bounds()

	// Never make a file larger when the format stays the same and no resize
	// was requested: the original is already better.
	if !resized && strings.EqualFold(outFormat, srcFormat) && int64(buf.Len()) >= info.Size() {
		return compressResult{
			path:          path,
			size:          info.Size(),
			format:        displayFormat(strings.ToLower(srcFormat)),
			outputName:    outputFilename(name, s.Suffix, extForFormat(srcFormat)),
			width:         b.Dx(),
			height:        b.Dy(),
			hasAlpha:      hasAlpha,
			passedThrough: true,
		}, nil
	}

	dir, err := a.ensureTempDir()
	if err != nil {
		return compressResult{}, errors.New("无法创建临时文件")
	}
	ext := extForFormat(outFormat)
	tmp, err := os.CreateTemp(dir, fmt.Sprintf("%d-*.%s", id, ext))
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
		format:     outFormat,
		outputName: outputName,
		width:      b.Dx(),
		height:     b.Dy(),
		hasAlpha:   hasAlpha,
	}, nil
}

// Preview returns before/after images for the comparison view.
func (a *AnyPic) Preview(id int64) (Preview, error) {
	a.mu.Lock()
	item := a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return Preview{}, errors.New("图片不存在")
	}
	if item.Status != statusDone {
		a.mu.Unlock()
		return Preview{}, errors.New("请先压缩这张图片")
	}
	src := item.Path
	out := item.compressedPath
	a.mu.Unlock()

	before, beforeW, beforeH, err := previewImage(src)
	if err != nil {
		return Preview{}, errors.New("无法生成原图预览")
	}
	after, afterW, afterH, err := previewImage(out)
	if err != nil {
		return Preview{}, errors.New("无法生成压缩图预览")
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
		return ExportResult{}, errors.New("请等待压缩完成")
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
		return ExportResult{}, errors.New("还没有可导出的图片")
	}
	if mode != "folder" || folder == "" {
		chosen, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      mygo.CallerWindow(ctx),
			Title:       "选择导出文件夹",
			ButtonLabel: "导出到这里",
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
		return ExportResult{}, errors.New("无法创建导出文件夹")
	}

	result := ExportResult{Folder: folder}
	for _, item := range done {
		src := item.compressedPath
		if item.PassedThrough || src == "" {
			src = item.Path
		}
		dest := uniquePath(filepath.Join(folder, item.OutputName))
		if err := copyFile(src, dest); err != nil {
			return result, fmt.Errorf("导出 %s 失败: %w", item.Name, err)
		}
		result.Paths = append(result.Paths, dest)
		result.Count++
	}
	return result, nil
}

// ExportOne asks where to save one compressed image.
func (a *AnyPic) ExportOne(ctx context.Context, id int64) (ExportResult, error) {
	a.mu.Lock()
	item := a.byID[id]
	if item == nil {
		a.mu.Unlock()
		return ExportResult{}, errors.New("图片不存在")
	}
	if item.Status != statusDone {
		a.mu.Unlock()
		return ExportResult{}, errors.New("请先压缩这张图片")
	}
	copyItem := *item
	folder := a.settings.OutputFolder
	a.mu.Unlock()

	defaultPath := filepath.Join(copyItem.Dir, copyItem.OutputName)
	if folder != "" {
		defaultPath = filepath.Join(folder, copyItem.OutputName)
	}
	dest, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       "导出压缩图片",
		DefaultPath: defaultPath,
		ButtonLabel: "导出",
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
		return ExportResult{}, err
	}
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
	return AppInfo{
		Version:            appVersion,
		Platform:           runtime.GOOS,
		Arch:               runtime.GOARCH,
		GoVersion:          runtime.Version(),
		ScreenshotShortcut: shortcut,
		ScreenshotActive:   mygo.GlobalShortcut.IsRegistered(shortcut),
	}
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
	if err != nil {
		return err
	}
	if a.statePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(a.statePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(a.statePath, data, 0o644)
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
// than silently flattened to their first frame.
func isAnimatedFile(path string) (bool, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gif":
		f, err := os.Open(path)
		if err != nil {
			return false, err
		}
		defer f.Close()
		anim, err := gif.DecodeAll(f)
		if err != nil {
			return false, err
		}
		return len(anim.Image) > 1, nil
	case ".webp":
		f, err := os.Open(path)
		if err != nil {
			return false, err
		}
		defer f.Close()
		anim, err := webp.DecodeAll(f)
		if err != nil {
			return false, err
		}
		return len(anim.Image) > 1, nil
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
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: "不支持的格式"})
			return
		}
		seen[path] = true
		out = append(out, path)
	}

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: "无法读取"})
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
			skipped = append(skipped, SkippedFile{Name: filepath.Base(path), Reason: "无法读取文件夹"})
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
	}
	return "jpg"
}

func chooseOutputFormat(setting, srcFormat string, hasAlpha bool) string {
	switch setting {
	case "jpeg":
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
