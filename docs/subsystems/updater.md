# 更新器（#3 之后）

> 职责边界、source of truth、主要运行路径、状态转换、重要约束、验证命令 ——
> 每页六样，事实只写这一页,别处链接过来。

## 职责边界

`updater.go`：版本比较、GitHub Releases 检查、安装包下载与代开。
「关于」对话框的检查/下载 UI 归 `index.html` + `src/main.ts`；发版产物
由 release.yml 构建。

## Source of truth

- 当前版本:`mygo.App.Version()`（打包时来自 mygo.config.ts →
  Info.plist，见 #2）；`appVersion` 常量只是未打包 dev 的兜底。
- 检查结果:`UpdateInfo`（hasUpdate/latest/pageURL/asset 三元组），存在
  服务字段 `updateInfo` 里，`DownloadUpdate`/`OpenReleasePage` 只消费
  一次成功检查后的结果。
- 网络端点:`releaseAPIBase` 包级变量，默认 `https://api.github.com`，
  测试指向回环服务器。

## 主要运行路径

1. 启动:页面 boot 后调 `AnyPic.checkForUpdate()`（静默——失败不提示，
   有新版弹一条带「查看更新」动作的 toast）。
2. 「关于」里手动检查:同一个方法，非静默，结果落在对话框状态行
   （检查中 / 已是最新 / 新版本可用 + 下载/发布页按钮）。
3. 下载:`AnyPic.downloadUpdate()` 把选定资产流式写入用户 Downloads
   （`.part` 落盘再改名，`uniquePath` 防覆盖），进度走 `anypic:download`
   事件；完成后 `openInstaller`——macOS `Shell.OpenPath` 挂载 DMG、
   Windows 运行 Setup、Linux `ShowItemInFolder` 定位文件。

## 状态转换

- `updateInfo`：nil → 一次成功检查后为最新结果，被下一次检查覆盖。
- `downloading`（atomic）：同一时刻至多一个下载；进行中拒绝并发请求。

## 重要约束

- 本文件是 AGENTS.md 不变量 1 唯一获准的网络路径：只访问 GitHub
  Releases（检查 + 下载），不上传、无遥测；资产 URL 仅接受 https
  （回环 http 只为测试豁免）；下载有 1GB 上限与整体超时。
- 检查失败静默（启动路径）或如实报错（手动路径），绝不假装已是最新。

## 验证命令

```sh
go test -race -run 'TestIssue3' .   # 版本比较、资产选择、检查、下载
go test -race ./...                 # 全量
bun run typecheck                   # tsc --noEmit
```
