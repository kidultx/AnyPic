# 更新器（#3 之后，#14 安装前退出）

> 职责边界、source of truth、主要运行路径、状态转换、重要约束、验证命令 ——
> 每页六样，事实只写这一页,别处链接过来。

## 职责边界

`updater.go`：版本比较、GitHub Releases 检查、安装包下载、打开安装包并
退出应用（#14）。「关于」对话框的检查/下载/确认 UI 归 `index.html` +
`src/main.ts`；发版产物由 release.yml 构建。

## Source of truth

- 当前版本:`mygo.App.Version()`（打包时来自 mygo.config.ts →
  Info.plist，见 #2）；`appVersion` 常量只是未打包 dev 的兜底。
- 检查结果:`UpdateInfo`（hasUpdate/latest/pageURL/asset 三元组），存在
  服务字段 `updateInfo` 里，`DownloadUpdate`/`OpenReleasePage` 只消费
  一次成功检查后的结果。
- 下载结果:`UpdateDownload`（path + needsQuit）。Windows 和 macOS 的
  `needsQuit` 为 true，表示安装包还没打开，等页面确认后由
  `InstallUpdate(path)` 打开。
- 安装包路径:`pendingInstaller`，只有最近一次 `DownloadUpdate` 在
  Windows/macOS 上写出的路径；`InstallUpdate` 只接受它。新一轮下载开始
  时清空。
- 未保存结果:`Item.saved`（进程内状态，不落盘）与 `UnsavedResults()`
  ——已压缩、非「已是最优」直通、且没有任何一次导出写过副本的结果数。
- 网络端点:`releaseAPIBase` 包级变量，默认 `https://api.github.com`，
  测试指向回环服务器。
- 平台判断:`installerPlatform` 包级变量，默认 `runtime.GOOS`，测试因此
  能把三个平台分支都跑到；`openInstaller`、`quitApp` 同样是包级变量，
  测试用来观察，而不真的打开安装包或退出进程。

## 主要运行路径

1. 启动:页面 boot 后调 `AnyPic.checkForUpdate()`（静默——失败不提示，
   有新版弹一条带「查看更新」动作的 toast）。
2. 「关于」里手动检查:同一个方法，非静默，结果落在对话框状态行
   （检查中 / 已是最新 / 新版本可用 + 下载/发布页按钮）。
3. 下载:`AnyPic.downloadUpdate()` 把选定资产流式写入用户 Downloads
   （`.part` 落盘再改名，`uniquePath` 防覆盖），进度走 `anypic:download`
   事件。
   - Linux:完成后 `openInstaller` 用 `ShowItemInFolder` 定位文件，行为
     与 #14 之前一致——包管理器在应用外安装，替换运行中的二进制没有
     问题。
   - Windows / macOS:只登记 `pendingInstaller` 并返回 `needsQuit`，不打开
     安装包。页面先关闭「关于」，再确认「安装新版本需要先退出 AnyPic」
     （有未保存结果时如实写张数与「退出后会丢失」）；「稍后」在文件夹里
     显示安装包，并提示先从托盘退出 AnyPic；「退出并安装」调
     `AnyPic.installUpdate(path)`。
4. 安装:`InstallUpdate` 校验 path 正是 `pendingInstaller`、文件仍然存在，
   先调 `openInstaller`（Windows 运行 Setup、macOS 挂载 DMG），成功后才
   调 `quitApp()`。`mygo.App.Quit()` 走正常退出：`OnBeforeQuit` 把
   `quitting` 设为 true 并执行 `service.Cleanup()`，`win.OnClose` 因此
   放行，不会被托盘的隐藏逻辑拦下。打开失败就如实报错，应用继续运行。

## 状态转换

- `updateInfo`：nil → 一次成功检查后为最新结果，被下一次检查覆盖。
- `downloading`（atomic）：同一时刻至多一个下载；进行中拒绝并发请求。
- `pendingInstaller`：每次 `DownloadUpdate` 开始时清空 → Windows/macOS
  下载成功后写入 → `InstallUpdate` 打开成功后清空。Linux 永不写入。
- `saved`：压缩开始时置 false；该结果的任一次导出（`ExportAll` /
  `ExportProcessed` / `ExportOne`）写出文件后置 true。`UnsavedResults`
  只数 done、非直通、未保存的结果——退出时 `Cleanup` 会删掉临时目录
  （不变量 4），这些结果才会真的丢；已导出的副本和直通的原图不受影响。

## 重要约束

- 本文件是 AGENTS.md 不变量 1 唯一获准的网络路径：只访问 GitHub
  Releases（检查 + 下载），不上传、无遥测；资产 URL 仅接受 https
  （回环 http 只为测试豁免）；下载有 1GB 上限与整体超时。
- 检查失败静默（启动路径）或如实报错（手动路径），绝不假装已是最新。
- `InstallUpdate` 不是「打开任意路径并退出」：只认最近一次
  `DownloadUpdate` 登记的路径，且文件必须仍然存在。
- 安装包先打开成功、再退出；打开失败绝不退出。
- 退出必须走 `mygo.App.Quit()` 的正常路径，让 `OnBeforeQuit` 清理临时
  目录；不得用 `Exit` 绕过。
- 不改 mygo 的 NSIS 脚本：安装器不检查也不关闭运行中的应用，关闭应用
  是 AnyPic 自己的责任（#14 根因）。

## 验证命令

```sh
go test -race -run 'TestIssue3|TestIssue14' .   # 版本比较、资产选择、检查、下载、安装前退出
go test -race ./...                              # 全量
bun run typecheck                                # tsc --noEmit
```
