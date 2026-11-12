# 截图链路

> 职责边界、source of truth、主要运行路径、状态转换、重要约束、验证命令 ——
> 每页六样，事实只写这一页,别处链接过来。

## 职责边界

`screenshot.go` + `decorate.go`:全局快捷键、区域截图命令选择、美化渲染
(`decorateScreenshot`,纯函数)、截图归档(`writeScreenshot`)。美化工作台
的 IPC 入口(`PreviewDecorated`/`DecorateItem`)归
[compressor.go](../../compressor.go);页面同步归 `index.html` +
`src/main.ts`(预览模态的「对比/美化」tab)。

## Source of truth

- 快捷键与剪贴板偏好:`screenshot.go` 读 `Settings.ScreenshotShortcut` /
  `ScreenshotClipboard`。
- 美化样式:`FrameStyle`(decorate.go),六个旋钮——背景(无/纯色 hex/
  渐变预设/day-card 图案)、圆角四档、描边、投影两档、边距三档、画布比例
  (只外扩不裁切)。图案 id 是闭集(patterns.go),非法值回落 `teal`。
  **样式由页面持有,且只属于当前正在调的图**:换一张图,工作台即重置为
  默认样式、渲染缓存清空——上一张的样式与渲染结果绝不泄漏到下一张;
  不写 localStorage,不进 Settings/settings.json。后端只渲染,
  `normalizeFrameStyle` 把任何输入兜底成已定义渲染。
- 视觉参数(边距/圆角缩放、渐变预设表、图案表与平铺缩放、描边色、投影
  档位)定义在 `decorate.go` / `patterns.go`;页面侧的色板、渐变 chip 与
  图案 chip 列表在 `src/preview.ts`
  (`BG_SWATCHES`/`GRADIENT_PRESETS`/`PATTERN_PRESETS`),两边预设 id 与
  渐变 stop 对齐。

## 主要运行路径

1. 快捷键/托盘/菜单 → `screenshotRequested` → 页面 `captureScreenshot`
   → `AnyPic.CaptureScreenshot`:macOS 权限检查 → `captureRegion` 取 PNG
   → 剪贴板偏好 → `writeScreenshot`(Pictures/AnyPic)→ `AddPaths`。
   **截图从不被隐式美化。**
2. 美化工作台(预览模态「美化」tab,行内 ✨ 或 tab 进入):
   每次调旋钮 → `AnyPic.previewDecorated(id, style)` 返回 dataURL 实时
   上屏(结果按样式缓存)→「应用」→ `AnyPic.decorateItem(id, style)`
   → 写 `原名-美化.png` 在原文件旁 → item 重置为 ready → autoProcess
   开启时并入自动流程。
3. 对比 tab(status=done)保持原有压缩前后对比。

## 状态转换

- `captureBusy`(atomic):同一时刻只允许一次截图。
- 美化:只对 ready/done 条目开放;压缩中(`a.running`)时
  `DecorateItem` 拒绝并删除已写出的文件;应用后条目回到 ready。

## 权限与错误(#17)

- **权限检查**:macOS 上 `CaptureScreenshot` 在运行截图命令前调用
  `CGPreflightScreenCaptureAccess`(`screenshot_permission_darwin.go`,
  purego 解析 CoreGraphics 符号,不用 cgo);没有权限时调用
  `CGRequestScreenCaptureAccess` 让系统把 AnyPic 加进权限列表并弹出
  提示,但**不运行 screencapture**。其他平台视为有权限;符号解析失败
  也按有权限处理,退回旧行为。
- **错误文本契约**:Go 与页面靠错误文本识别。`已取消截图`、`正在截图中`
  静默;`需要屏幕录制权限` 由页面弹说明并打开系统设置。Go 侧
  `errScreenCapturePermission` 与 `src/main.ts` 的
  `isScreenCapturePermission` 由 `TestIssue17PageHandlesScreenPermission`
  锁在一起。文件型 screencapture 运行失败或没产出文件仍按取消处理
  (真机未验证 Esc 的实际退出码与 stderr);权限失败已由 preflight 在截图
  前拦下。
- **出错时窗口前置**:除取消与「正在截图中」外,`CaptureScreenshot` 的
  任何错误(权限、截图工具、保存、入队)都会恢复/显示/聚焦窗口——全局
  快捷键在后台触发时,错误提示才看得见。成功路径的零点击例外不变。

## 重要约束

- 全部在本机完成(不变量 1);`decorateScreenshot` 是纯 Go 绘制,无网络。
- 绝不覆盖原图(不变量 2):美化结果永远是 `原名-美化.png`,重名加序号。
- 美化是显式动作:没有全局设置、没有截图时静默套用;样式不记忆
  (不写 localStorage),每次打开都回到默认。
- 非法样式输入由 `normalizeFrameStyle` 兜底为默认渲染,不报错。

## 验证命令

```sh
go test -race -run 'TestIssue17' .   # 权限检查、错误文本契约、窗口前置
go test -race -run 'TestIssue1' .   # 样式归一化、各旋钮、条目应用、预览
go test -race -run 'TestPattern' .  # 图案闭集、平铺比例、边界画布、阴影色
go test -race ./...                 # 全量
bun run typecheck                   # tsc --noEmit
```
