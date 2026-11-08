package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// The in-app updater (issue #3, the invariant-1 exception the repo owner
// approved): the app's only network traffic is checking GitHub Releases for
// a newer version and, after the user clicks, downloading that release's
// installer. Images never leave the machine; there are no accounts, no
// uploads and no telemetry.

// releaseAPIBase is the GitHub API root. A package variable so tests point
// it at a loopback server; the app itself talks to nothing else.
var releaseAPIBase = "https://api.github.com"

// openInstaller opens the downloaded installer for the user: mounting the
// DMG on macOS, running the Setup on Windows, showing the file on Linux. A
// package variable so tests can watch it instead of the OS.
var openInstaller = func(path string) error {
	if runtime.GOOS == "linux" {
		mygo.Shell.ShowItemInFolder(path)
		return nil
	}
	return mygo.Shell.OpenPath(path)
}

// downloadSizeCap refuses absurd responses rather than filling the disk.
const downloadSizeCap = 1 << 30

// releaseAsset is one downloadable file on a GitHub release.
type releaseAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// release is the slice of GitHub's "latest release" payload the updater
// reads.
type release struct {
	TagName string         `json:"tag_name"`
	HTMLURL string         `json:"html_url"`
	Assets  []releaseAsset `json:"assets"`
}

// UpdateInfo reports what CheckForUpdate found.
type UpdateInfo struct {
	HasUpdate bool   `json:"hasUpdate"`
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	PageURL   string `json:"pageURL"`
	AssetName string `json:"assetName"`
	AssetSize int64  `json:"assetSize"`
	AssetURL  string `json:"assetURL"`
}

// DownloadProgress reports the installer download's byte counts.
type DownloadProgress struct {
	Received int64 `json:"received"`
	Total    int64 `json:"total"`
}

// DownloadProgressChanged is sent while the installer downloads.
var DownloadProgressChanged = mygo.NewEvent[DownloadProgress]("anypic:download")

// compareVersions compares two dotted versions numerically, ignoring a
// leading v and any non-numeric tail, so 0.1.10 sorts above 0.1.9.
func compareVersions(a, b string) int {
	as := strings.Split(strings.TrimPrefix(strings.TrimSpace(a), "v"), ".")
	bs := strings.Split(strings.TrimPrefix(strings.TrimSpace(b), "v"), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		av, bv := versionSegment(as, i), versionSegment(bs, i)
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionSegment(segments []string, i int) int {
	if i >= len(segments) {
		return 0
	}
	n := 0
	for _, r := range segments[i] {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// selectReleaseAsset picks the installer for the running platform from the
// release's asset list, matching the file names release.yml produces.
func selectReleaseAsset(assets []releaseAsset) (releaseAsset, error) {
	for _, a := range assets {
		name := strings.ToLower(a.Name)
		switch runtime.GOOS {
		case "darwin":
			if strings.HasSuffix(name, ".dmg") {
				return a, nil
			}
		case "windows":
			if strings.HasSuffix(name, ".exe") && strings.Contains(name, "setup") {
				return a, nil
			}
		case "linux":
			if strings.HasSuffix(name, ".deb") {
				return a, nil
			}
		}
	}
	if runtime.GOOS == "linux" {
		for _, a := range assets {
			if strings.HasSuffix(strings.ToLower(a.Name), ".tar.gz") {
				return a, nil
			}
		}
	}
	return releaseAsset{}, errors.New("发布里没有适用于本平台的安装包")
}

// CheckForUpdate asks GitHub for the latest release and compares it with the
// running version. The result is remembered for DownloadUpdate.
func (a *AnyPic) CheckForUpdate() (UpdateInfo, error) {
	info, err := checkForUpdate()
	if err != nil {
		return UpdateInfo{}, err
	}
	a.mu.Lock()
	a.updateInfo = &info
	a.mu.Unlock()
	return info, nil
}

func checkForUpdate() (UpdateInfo, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(releaseAPIBase + "/repos/kidultx/AnyPic/releases/latest")
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("无法连接更新服务：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UpdateInfo{}, fmt.Errorf("更新服务返回 %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return UpdateInfo{}, fmt.Errorf("无法解析更新信息：%w", err)
	}
	info := UpdateInfo{
		Current: mygo.App.Version(),
		Latest:  strings.TrimPrefix(rel.TagName, "v"),
		PageURL: rel.HTMLURL,
	}
	if compareVersions(info.Latest, info.Current) > 0 {
		asset, err := selectReleaseAsset(rel.Assets)
		if err == nil {
			info.HasUpdate = true
			info.AssetName = asset.Name
			info.AssetSize = asset.Size
			info.AssetURL = asset.BrowserDownloadURL
		}
		// 有新版但选不出资产时仍返回 info:页面给出去发布页的路径。
	}
	return info, nil
}

// DownloadUpdate downloads the remembered release asset into the user's
// Downloads folder, reporting progress on anypic:download, then opens the
// installer. Only one download runs at a time.
func (a *AnyPic) DownloadUpdate() (string, error) {
	if !a.downloading.CompareAndSwap(false, true) {
		return "", errors.New("已经在下载中")
	}
	defer a.downloading.Store(false)

	a.mu.Lock()
	info := a.updateInfo
	a.mu.Unlock()
	if info == nil || !info.HasUpdate || info.AssetURL == "" {
		return "", errors.New("请先检查更新")
	}
	// The asset URL comes from the api.github.com response over HTTPS; only
	// https is trusted. Loopback http stays allowed for the tests.
	u, err := url.Parse(info.AssetURL)
	if err != nil || (u.Scheme != "https" && u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return "", errors.New("下载地址不可信，已取消")
	}

	destDir, err := mygo.App.Path(mygo.PathDownloads)
	if err != nil || destDir == "" {
		destDir, err = mygo.App.Path(mygo.PathCache)
		if err != nil {
			return "", fmt.Errorf("无法确定下载目录：%w", err)
		}
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("无法准备下载目录：%w", err)
	}
	dest := uniquePath(filepath.Join(destDir, info.AssetName))
	part := dest + ".part"

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Get(info.AssetURL)
	if err != nil {
		return "", fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败：更新服务返回 %s", resp.Status)
	}

	total := info.AssetSize
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	out, err := os.Create(part)
	if err != nil {
		return "", fmt.Errorf("无法写入下载文件：%w", err)
	}
	var received int64
	lastEmit := time.Now()
	buf := make([]byte, 256<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			received += int64(n)
			if received > downloadSizeCap {
				out.Close()
				_ = os.Remove(part)
				return "", errors.New("安装包超过大小上限，已取消下载")
			}
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				_ = os.Remove(part)
				return "", fmt.Errorf("无法写入下载文件：%w", werr)
			}
			if time.Since(lastEmit) >= 200*time.Millisecond {
				lastEmit = time.Now()
				_ = DownloadProgressChanged.Broadcast(DownloadProgress{Received: received, Total: total})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			out.Close()
			_ = os.Remove(part)
			return "", fmt.Errorf("下载中断：%w", readErr)
		}
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(part)
		return "", fmt.Errorf("无法写入下载文件：%w", err)
	}
	_ = DownloadProgressChanged.Broadcast(DownloadProgress{Received: received, Total: received})
	if err := os.Rename(part, dest); err != nil {
		_ = os.Remove(part)
		return "", fmt.Errorf("无法完成下载：%w", err)
	}
	if err := openInstaller(dest); err != nil {
		return dest, fmt.Errorf("已下载到 %s，但无法打开：%w", dest, err)
	}
	return dest, nil
}

// OpenReleasePage opens the latest release's page in the browser, the path
// when no installer asset matches this platform.
func (a *AnyPic) OpenReleasePage() error {
	a.mu.Lock()
	info := a.updateInfo
	a.mu.Unlock()
	if info == nil || info.PageURL == "" {
		return errors.New("请先检查更新")
	}
	return mygo.Shell.OpenExternal(info.PageURL)
}
