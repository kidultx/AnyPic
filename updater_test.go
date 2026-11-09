package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo"
)

const testReleaseJSON = `{
  "tag_name": "v0.2.0",
  "html_url": "https://github.com/kidultx/AnyPic/releases/tag/v0.2.0",
  "assets": [
    {"name": "AnyPic.0.2.0.dmg", "size": 7300000, "browser_download_url": "https://github.com/kidultx/AnyPic/releases/download/v0.2.0/AnyPic.0.2.0.dmg"},
    {"name": "AnyPic.Setup.0.2.0.amd64.exe", "size": 8200000, "browser_download_url": "https://github.com/kidultx/AnyPic/releases/download/v0.2.0/AnyPic.Setup.0.2.0.amd64.exe"},
    {"name": "anypic_0.2.0_amd64.deb", "size": 7100000, "browser_download_url": "https://github.com/kidultx/AnyPic/releases/download/v0.2.0/anypic_0.2.0_amd64.deb"},
    {"name": "anypic-0.2.0-linux-amd64.tar.gz", "size": 6900000, "browser_download_url": "https://github.com/kidultx/AnyPic/releases/download/v0.2.0/anypic-0.2.0-linux-amd64.tar.gz"}
  ]
}`

func TestIssue3CompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.4", "0.1.4", 0},
		{"0.1.4", "0.1.10", -1}, // 逐段数值比较,不是字符串比较
		{"v0.1.4", "0.1.4", 0},
		{"0.2.0", "0.1.9", 1},
		{"1.0", "0.9.9", 1},
		{"", "0.1.4", -1},
		{"0.1.4-beta", "0.1.4", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIssue3SelectReleaseAsset(t *testing.T) {
	var rel release
	if err := json.Unmarshal([]byte(testReleaseJSON), &rel); err != nil {
		t.Fatal(err)
	}
	asset, err := selectReleaseAsset(rel.Assets)
	if err != nil {
		t.Fatalf("selectReleaseAsset: %v", err)
	}
	switch runtime.GOOS {
	case "darwin":
		if !strings.HasSuffix(asset.Name, ".dmg") {
			t.Errorf("darwin asset = %q, want the dmg", asset.Name)
		}
	case "windows":
		if !strings.HasSuffix(asset.Name, ".exe") {
			t.Errorf("windows asset = %q, want the Setup exe", asset.Name)
		}
	case "linux":
		if !strings.HasSuffix(asset.Name, ".deb") {
			t.Errorf("linux asset = %q, want the deb", asset.Name)
		}
	}
	if _, err := selectReleaseAsset(nil); err == nil {
		t.Error("empty asset list should fail")
	}
}

// pointReleaseAPI spins up a loopback server answering the latest-release
// path with body(base) — called at request time, so the response can embed
// the server's own URL — or 500 when the function is nil, and points
// releaseAPIBase at it. Returns the base URL. Loopback only: the updater
// tests never touch the real network.
func pointReleaseAPI(t *testing.T, body func(base string) string, asset []byte) string {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/asset") {
			_, _ = w.Write(asset)
			return
		}
		switch r.URL.Path {
		case "/repos/kidultx/AnyPic/releases/latest":
			if body == nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body(srvURL)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	srvURL = srv.URL
	old := releaseAPIBase
	releaseAPIBase = srv.URL
	t.Cleanup(func() { releaseAPIBase = old })
	return srv.URL
}

func staticBody(s string) func(string) string {
	return func(string) string { return s }
}

func TestIssue3CheckForUpdate(t *testing.T) {
	mygo.App.SetVersion("0.1.4")
	t.Cleanup(func() { mygo.App.SetVersion("") })

	pointReleaseAPI(t, staticBody(testReleaseJSON), nil)
	a := &AnyPic{settings: defaultSettings()}

	info, err := a.CheckForUpdate()
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if !info.HasUpdate || info.Latest != "0.2.0" || info.Current != "0.1.4" {
		t.Errorf("info = %+v, want an update to 0.2.0", info)
	}
	if info.AssetURL == "" || info.PageURL == "" || info.AssetSize == 0 {
		t.Errorf("info = %+v, want asset and page details", info)
	}
	if !strings.HasPrefix(info.AssetURL, "https://") {
		t.Errorf("asset url %q must be https", info.AssetURL)
	}

	// 当前版本就是最新:不再提示
	mygo.App.SetVersion("0.2.0")
	info, err = a.CheckForUpdate()
	if err != nil {
		t.Fatalf("CheckForUpdate same version: %v", err)
	}
	if info.HasUpdate {
		t.Errorf("same version reported as update: %+v", info)
	}

	// 服务端错误要如实报出来
	pointReleaseAPI(t, nil, nil)
	if _, err := a.CheckForUpdate(); err == nil {
		t.Error("HTTP 500 should surface as an error")
	}
}

func TestIssue3DownloadUpdate(t *testing.T) {
	mygo.App.SetVersion("0.1.4")
	t.Cleanup(func() { mygo.App.SetVersion("") })
	// Since #14 the Windows/macOS flows open the installer only after the
	// quit confirmation; this test keeps covering the download's own
	// progress/cleanup plus Linux's reveal, which still happens on download.
	oldPlatform := installerPlatform
	installerPlatform = "linux"
	t.Cleanup(func() { installerPlatform = oldPlatform })

	const assetSize = 1 << 20
	asset := make([]byte, assetSize)
	for i := range asset {
		asset[i] = byte(i)
	}
	// 资产 URL 全部指到回环服务器:selectReleaseAsset 按 GOOS 挑的可能是
	// 任何一个资产,漏改哪个,哪个平台就会打到真实的 github.com(v0.2.0
	// 不存在 → 404,还违反测试不碰外网)。
	pointReleaseAPI(t, func(base string) string {
		return strings.ReplaceAll(testReleaseJSON,
			"https://github.com/kidultx/AnyPic/releases/download/v0.2.0/",
			base+"/asset/")
	}, asset)

	downloads := t.TempDir()
	mygo.App.SetPath(mygo.PathDownloads, downloads)
	t.Cleanup(func() { mygo.App.SetPath(mygo.PathDownloads, "") })

	var mu sync.Mutex
	opened := ""
	oldOpen := openInstaller
	openInstaller = func(path string) error {
		mu.Lock()
		opened = path
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { openInstaller = oldOpen })

	a := &AnyPic{settings: defaultSettings()}
	if _, err := a.CheckForUpdate(); err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}

	download, err := a.DownloadUpdate()
	if err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
	path := download.Path
	mu.Lock()
	openedPath := opened
	mu.Unlock()
	if openedPath != path {
		t.Errorf("installer opened %q, want the downloaded %q", openedPath, path)
	}
	if filepath.Dir(path) != downloads {
		t.Errorf("download went to %s, want %s", filepath.Dir(path), downloads)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if len(data) != assetSize {
		t.Errorf("downloaded %d bytes, want %d", len(data), assetSize)
	}
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Error(".part file left behind")
	}

	// 并发下载被拒绝
	a.downloading.Store(true)
	if _, err := a.DownloadUpdate(); err == nil || !strings.Contains(err.Error(), "下载") {
		t.Errorf("concurrent download error = %v, want a busy refusal", err)
	}
	a.downloading.Store(false)

	// 未检查就下载被拒绝
	b := &AnyPic{settings: defaultSettings()}
	if _, err := b.DownloadUpdate(); err == nil {
		t.Error("DownloadUpdate without a check should refuse")
	}
}

/* ------------------------------------------------------------------ */
/* issue #14: installing an update must quit the running app first     */
/* ------------------------------------------------------------------ */

// installerHooks replaces the two OS-facing calls behind InstallUpdate and
// records the order they happened in. Tests never open a real installer or
// quit the test process.
type installerHooks struct {
	mu      sync.Mutex
	calls   []string
	openErr error
}

func stubInstallerHooks(t *testing.T) *installerHooks {
	t.Helper()
	h := &installerHooks{}
	oldOpen, oldQuit := openInstaller, quitApp
	openInstaller = func(string) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.calls = append(h.calls, "open")
		return h.openErr
	}
	quitApp = func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.calls = append(h.calls, "quit")
	}
	t.Cleanup(func() {
		openInstaller = oldOpen
		quitApp = oldQuit
	})
	return h
}

func (h *installerHooks) sequence() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.calls, ",")
}

func stubInstallerPlatform(t *testing.T, goos string) {
	t.Helper()
	old := installerPlatform
	installerPlatform = goos
	t.Cleanup(func() { installerPlatform = old })
}

// prepareUpdateDownload points the updater at a loopback release server and
// a temporary Downloads folder, runs one successful check, and returns a
// service ready to download.
func prepareUpdateDownload(t *testing.T) (*AnyPic, string) {
	t.Helper()
	mygo.App.SetVersion("0.1.4")
	t.Cleanup(func() { mygo.App.SetVersion("") })

	pointReleaseAPI(t, func(base string) string {
		return strings.ReplaceAll(testReleaseJSON,
			"https://github.com/kidultx/AnyPic/releases/download/v0.2.0/",
			base+"/asset/")
	}, []byte("installer"))

	downloads := t.TempDir()
	mygo.App.SetPath(mygo.PathDownloads, downloads)
	t.Cleanup(func() { mygo.App.SetPath(mygo.PathDownloads, "") })

	service := &AnyPic{settings: defaultSettings()}
	if _, err := service.CheckForUpdate(); err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	return service, downloads
}

// TestIssue14InstallsByOpeningThenQuitting: on Windows and macOS the
// downloaded installer must not be opened before the user confirms, and the
// confirmed install opens it first — only a successful open may quit, so the
// NSIS setup / Finder replace no longer races the running app.
func TestIssue14InstallsByOpeningThenQuitting(t *testing.T) {
	stubInstallerPlatform(t, "windows")
	hooks := stubInstallerHooks(t)
	service, downloads := prepareUpdateDownload(t)

	result, err := service.DownloadUpdate()
	if err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
	if !result.NeedsQuit {
		t.Errorf("result = %+v, want NeedsQuit on windows", result)
	}
	if filepath.Dir(result.Path) != downloads {
		t.Errorf("download went to %s, want %s", filepath.Dir(result.Path), downloads)
	}
	if got := hooks.sequence(); got != "" {
		t.Fatalf("DownloadUpdate ran %q, want no installer action before confirmation", got)
	}

	if err := service.InstallUpdate(result.Path); err != nil {
		t.Fatalf("InstallUpdate: %v", err)
	}
	if got := hooks.sequence(); got != "open,quit" {
		t.Fatalf("installer sequence = %q, want open,quit", got)
	}
	// A successful install consumes the pending path: the same path must not
	// open and request another quit.
	if err := service.InstallUpdate(result.Path); err == nil {
		t.Error("InstallUpdate replayed the same path after a successful install")
	}
	if got := hooks.sequence(); got != "open,quit" {
		t.Fatalf("replay changed installer sequence to %q, want open,quit", got)
	}
}

// TestIssue14InstallRejectsMissingInstaller: the pending path must still name
// an existing file; a deleted installer leaves the app running.
func TestIssue14InstallRejectsMissingInstaller(t *testing.T) {
	stubInstallerPlatform(t, "darwin")
	hooks := stubInstallerHooks(t)
	service, _ := prepareUpdateDownload(t)

	result, err := service.DownloadUpdate()
	if err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
	if err := os.Remove(result.Path); err != nil {
		t.Fatal(err)
	}
	if err := service.InstallUpdate(result.Path); err == nil {
		t.Fatal("InstallUpdate accepted a path whose installer is gone")
	}
	if got := hooks.sequence(); got != "" {
		t.Fatalf("missing installer ran %q, want nothing", got)
	}
}

// TestIssue14OpenFailureDoesNotQuit: when the installer cannot be opened the
// app must stay running and say so instead of quitting into nothing.
func TestIssue14OpenFailureDoesNotQuit(t *testing.T) {
	stubInstallerPlatform(t, "darwin")
	hooks := stubInstallerHooks(t)
	hooks.openErr = errors.New("no handler")
	service, _ := prepareUpdateDownload(t)

	result, err := service.DownloadUpdate()
	if err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
	err = service.InstallUpdate(result.Path)
	if err == nil {
		t.Fatal("InstallUpdate with a failing open returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "无法打开") {
		t.Errorf("error = %v, want it to say the installer could not be opened", err)
	}
	if got := hooks.sequence(); got != "open" {
		t.Fatalf("installer sequence = %q, want only open (no quit)", got)
	}
}

// TestIssue14InstallOnlyAcceptsTheDownloadedPath: InstallUpdate is not a
// "open any path and quit" endpoint. Only the file this download wrote is
// accepted; a newer download invalidates the previous one.
func TestIssue14InstallOnlyAcceptsTheDownloadedPath(t *testing.T) {
	stubInstallerPlatform(t, "windows")
	hooks := stubInstallerHooks(t)
	service, downloads := prepareUpdateDownload(t)

	first, err := service.DownloadUpdate()
	if err != nil {
		t.Fatalf("first DownloadUpdate: %v", err)
	}
	other := filepath.Join(downloads, "elsewhere.exe")
	if err := os.WriteFile(other, []byte("not the installer"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{other, "", filepath.Join(downloads, "missing.exe")} {
		if err := service.InstallUpdate(path); err == nil {
			t.Errorf("InstallUpdate(%q) accepted a path this download did not write", path)
		}
	}
	if got := hooks.sequence(); got != "" {
		t.Fatalf("rejected paths ran %q, want nothing", got)
	}

	// A new attempt invalidates the previous installer even when that
	// attempt fails: a directory where the second .part file would go makes
	// the download fail after DownloadUpdate has started.
	service.mu.Lock()
	assetName := service.updateInfo.AssetName
	service.mu.Unlock()
	secondPath := uniquePath(filepath.Join(downloads, assetName))
	if secondPath == first.Path {
		t.Fatalf("second download would reuse %q", first.Path)
	}
	obstruction := secondPath + ".part"
	if err := os.MkdirAll(obstruction, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DownloadUpdate(); err == nil {
		t.Fatal("download into a blocked .part path should fail")
	}
	if err := service.InstallUpdate(first.Path); err == nil {
		t.Error("InstallUpdate accepted the previous download after a failed new attempt")
	}
	if got := hooks.sequence(); got != "" {
		t.Fatalf("failed attempt left %q, want nothing", got)
	}
	if err := os.RemoveAll(obstruction); err != nil {
		t.Fatal(err)
	}

	second, err := service.DownloadUpdate()
	if err != nil {
		t.Fatalf("second DownloadUpdate: %v", err)
	}
	if second.Path == first.Path {
		t.Fatalf("second download reused %q, want a fresh file", second.Path)
	}
	if err := service.InstallUpdate(first.Path); err == nil {
		t.Error("InstallUpdate accepted the previous download after a new one")
	}
	if got := hooks.sequence(); got != "" {
		t.Fatalf("stale path ran %q, want nothing", got)
	}
	if err := service.InstallUpdate(second.Path); err != nil {
		t.Fatalf("InstallUpdate(latest): %v", err)
	}
	if got := hooks.sequence(); got != "open,quit" {
		t.Fatalf("installer sequence = %q, want open,quit", got)
	}
}

// TestIssue14PlatformBranches pins every platform's share of the flow:
// windows and darwin wait for confirmation, linux keeps revealing the file
// after download and never quits the app.
func TestIssue14PlatformBranches(t *testing.T) {
	for _, goos := range []string{"windows", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			stubInstallerPlatform(t, goos)
			hooks := stubInstallerHooks(t)
			service, _ := prepareUpdateDownload(t)

			result, err := service.DownloadUpdate()
			if err != nil {
				t.Fatalf("DownloadUpdate: %v", err)
			}
			if !result.NeedsQuit {
				t.Fatalf("%s download = %+v, want NeedsQuit", goos, result)
			}
			if got := hooks.sequence(); got != "" {
				t.Fatalf("%s DownloadUpdate ran %q, want no installer action", goos, got)
			}
			if err := service.InstallUpdate(result.Path); err != nil {
				t.Fatalf("%s InstallUpdate: %v", goos, err)
			}
			if got := hooks.sequence(); got != "open,quit" {
				t.Fatalf("%s installer sequence = %q, want open,quit", goos, got)
			}
		})
	}

	t.Run("linux", func(t *testing.T) {
		stubInstallerPlatform(t, "linux")
		hooks := stubInstallerHooks(t)
		service, _ := prepareUpdateDownload(t)

		result, err := service.DownloadUpdate()
		if err != nil {
			t.Fatalf("DownloadUpdate: %v", err)
		}
		if result.NeedsQuit {
			t.Errorf("linux download = %+v, want the old reveal-only behavior", result)
		}
		if got := hooks.sequence(); got != "open" {
			t.Fatalf("linux DownloadUpdate ran %q, want the reveal (open) and no quit", got)
		}
		if err := service.InstallUpdate(result.Path); err == nil {
			t.Error("linux InstallUpdate accepted a path, but linux installs outside the app")
		}
		if got := hooks.sequence(); got != "open" {
			t.Fatalf("linux sequence after InstallUpdate = %q, want still no quit", got)
		}
	})
}

// compressOneForUnsavedTest adds one image to service, compresses it, and
// returns the settings plus the source path. Each export-path test starts
// from this state.
func compressOneForUnsavedTest(t *testing.T, service *AnyPic) (Settings, string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "one.png")
	writePNG(t, src, gradientImage(320, 200, false))
	if _, err := service.AddPaths([]string{src}); err != nil {
		t.Fatal(err)
	}
	settings := defaultSettings()
	settings.Format = "webp"
	settings.Quality = 70
	if err := service.Compress(settings); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "compression", func() bool {
		state := service.State()
		return !state.Batch.Running && len(state.Items) == 1 && state.Items[0].Status == statusDone
	})
	return settings, src
}

func recompressForUnsavedTest(t *testing.T, service *AnyPic, settings Settings) {
	t.Helper()
	if err := service.Compress(settings); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "recompression", func() bool {
		state := service.State()
		return !state.Batch.Running && len(state.Items) == 1 && state.Items[0].Status == statusDone
	})
}

// TestIssue14UnsavedResultsExportProcessed covers the automatic flow's save
// path: an exported result survives a quit, a recompression makes the fresh
// temp-only result unsaved again, and a passed-through result is the original
// file and never lost.
func TestIssue14UnsavedResultsExportProcessed(t *testing.T) {
	service := newTestService(t)
	settings, src := compressOneForUnsavedTest(t, service)
	if got := service.UnsavedResults(); got != 1 {
		t.Fatalf("after compression UnsavedResults() = %d, want 1", got)
	}

	state := service.State()
	if _, err := service.ExportProcessed([]int64{state.Items[0].ID}); err != nil {
		t.Fatalf("ExportProcessed: %v", err)
	}
	if got := service.UnsavedResults(); got != 0 {
		t.Fatalf("after export UnsavedResults() = %d, want 0", got)
	}

	recompressForUnsavedTest(t, service, settings)
	if got := service.UnsavedResults(); got != 1 {
		t.Fatalf("after recompression UnsavedResults() = %d, want 1", got)
	}

	service.mu.Lock()
	passthrough := &Item{
		ID:             999,
		Name:           "two.png",
		Path:           src,
		Dir:            filepath.Dir(src),
		Status:         statusDone,
		PassedThrough:  true,
		compressedPath: src,
		OutputName:     "two-anypic.png",
	}
	service.items = append(service.items, passthrough)
	service.byID[passthrough.ID] = passthrough
	service.mu.Unlock()
	if got := service.UnsavedResults(); got != 1 {
		t.Fatalf("with a passed-through result UnsavedResults() = %d, want still 1", got)
	}
}

// TestIssue14UnsavedResultsExportAll: ExportAll marks every result it copied
// as saved, and a later compression pass puts the result back into the
// unsaved count.
func TestIssue14UnsavedResultsExportAll(t *testing.T) {
	service := newTestService(t)
	settings, _ := compressOneForUnsavedTest(t, service)
	if got := service.UnsavedResults(); got != 1 {
		t.Fatalf("after compression UnsavedResults() = %d, want 1", got)
	}

	out := t.TempDir()
	service.mu.Lock()
	service.settings.OutputMode = "folder"
	service.settings.OutputFolder = out
	service.mu.Unlock()

	result, err := service.ExportAll(context.Background())
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	if result.Count != 1 || len(result.Paths) != 1 {
		t.Fatalf("ExportAll wrote %d paths, want 1: %+v", len(result.Paths), result)
	}
	if filepath.Dir(result.Paths[0]) != out {
		t.Errorf("ExportAll wrote %s, want it in %s", result.Paths[0], out)
	}
	if info, err := os.Stat(result.Paths[0]); err != nil || info.Size() == 0 {
		t.Fatalf("missing or empty export %s: %v", result.Paths[0], err)
	}
	if got := service.UnsavedResults(); got != 0 {
		t.Fatalf("after ExportAll UnsavedResults() = %d, want 0", got)
	}

	recompressForUnsavedTest(t, service, settings)
	if got := service.UnsavedResults(); got != 1 {
		t.Fatalf("after recompression UnsavedResults() = %d, want 1", got)
	}
}

// TestIssue14UnsavedResultsExportOne: the single-image save also marks its
// result as saved; the native save dialog is injected so no real file picker
// opens.
func TestIssue14UnsavedResultsExportOne(t *testing.T) {
	service := newTestService(t)
	settings, _ := compressOneForUnsavedTest(t, service)
	if got := service.UnsavedResults(); got != 1 {
		t.Fatalf("after compression UnsavedResults() = %d, want 1", got)
	}

	dest := filepath.Join(t.TempDir(), "chosen.webp")
	oldSave := saveDialog
	saveDialog = func(mygo.SaveDialogOptions) (string, error) { return dest, nil }
	t.Cleanup(func() { saveDialog = oldSave })

	state := service.State()
	result, err := service.ExportOne(context.Background(), state.Items[0].ID)
	if err != nil {
		t.Fatalf("ExportOne: %v", err)
	}
	if len(result.Paths) != 1 || result.Paths[0] != dest {
		t.Fatalf("ExportOne paths = %v, want %q", result.Paths, dest)
	}
	if info, err := os.Stat(dest); err != nil || info.Size() == 0 {
		t.Fatalf("missing or empty export %s: %v", dest, err)
	}
	if got := service.UnsavedResults(); got != 0 {
		t.Fatalf("after ExportOne UnsavedResults() = %d, want 0", got)
	}

	recompressForUnsavedTest(t, service, settings)
	if got := service.UnsavedResults(); got != 1 {
		t.Fatalf("after recompression UnsavedResults() = %d, want 1", got)
	}
}
