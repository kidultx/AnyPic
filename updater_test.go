package main

import (
	"encoding/json"
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

	path, err := a.DownloadUpdate()
	if err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
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
