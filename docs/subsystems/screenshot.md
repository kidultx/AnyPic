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
  `ScreenshotClipboard` / `ScreenshotPipeline`。`ScreenshotPipeline`
  （**默认 off**，只有显式 `"on"` 才开启）表示截图后自动压缩、并在压缩完成
  时用压缩结果替换剪贴板里的原图；它依赖 `ScreenshotClipboard`，后者关闭
  时 `normalizeSettings` 强制把管线也关掉，`CaptureScreenshot` 仍会再判一次，
  绝不在用户禁止复制时写剪贴板。
- 剪贴板读写全部经 `clipboard.go` 的包级函数（`clipboardReadState` /
  `clipboardReadImage` / `clipboardWriteImage` / `clipboardLeaseValid` /
  `clipboardWriteText` / `clipboardClear`），测试替换它们即可不碰真实
  剪贴板。所有权比较从不依赖容器字节：主判据是
  `clipboardPixelDigest` 对**解码后的预乘像素**求摘要（透明像素的隐藏 RGB
  差异被忽略），它跨 Windows CF_DIB / Linux 的重新编码仍成立，也能在平台
  释放回调滞后时证明当前图片仍是我们那张。写入同时经 MyGo
  `Clipboard.Write` + `ClipboardOptions.OnRelease` 返回租约 token，平台在
  剪贴板被替换、清空或 AnyPic 再次写入时使租约失效；捕获来源的复制还要求
  租约仍有效。租约不是摘要缺失时的替代品：原图无法解码就没有所有权记录，
  后续复制直接跳过。
- `CopyCaptureResult` 先要求剪贴板里仍有**图片**：空剪贴板、纯文字、文件都
  不是 AnyPic 的捕获，直接跳过。没有像素摘要也直接跳过；捕获来源（lease
  != 0）还要求租约有效；当前图像的像素摘要必须与记录相等，否则跳过——像素
  不等时即使租约仍显示有效也跳过（Windows 的释放回调可能滞后）。截图原始
  写入失败时租约 token 为 0，`rememberClipboardImage` 不记录任何所有权，
  之后绝不会覆盖剪贴板。
- 托盘「再次复制上一张压缩结果」读 `AnyPic.lastResultID`（最近一次成功
  压缩的条目）→ `CopyLastResult` → `copyItemToClipboard`（非 PNG 结果
  先解码重编码为 PNG）。右键菜单「复制为 Markdown 图片」走
  `CopyItemAsMarkdown`：先确保结果在磁盘上有耐久副本（已导出则用
  `savedPath`，否则写到原图旁或输出目录），再把 `![名称](本地路径)` 写进
  剪贴板；不上传任何内容。
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
   → 读剪贴板/自动处理/管线偏好。`ScreenshotClipboard` 开启时原图**立刻**
   写进剪贴板并拿到所有权租约（压缩期间粘贴拿到的是原图，不是旧内容）；
   复制关闭时不写，若截图工具自己写了剪贴板则清掉。随后 `writeScreenshot`
   (Pictures/AnyPic)→ `AddPaths` → 写入成功才 `rememberClipboardImage`。
   **截图从不被隐式美化。**
2. 页面把这次加入当作 `capture` 来源：管线开启时即使 autoProcess 关闭也
   启动自动压缩；批处理结束后调用 `AnyPic.copyCaptureResult(id)`，后端
   先确认剪贴板仍有图片、捕获的租约仍有效、且解码像素与记录相等，才换成
   压缩结果；否则跳过并提示「剪贴板有新内容，已保留」。
3. 美化工作台(预览模态「美化」tab,行内 ✨ 或 tab 进入):
   每次调旋钮 → `AnyPic.previewDecorated(id, style)` 返回 dataURL 实时
   上屏(结果按样式缓存)→「应用」→ `AnyPic.decorateItem(id, style)`
   → 写 `原名-美化.png` 在原文件旁 → item 重置为 ready → autoProcess
   开启时并入自动流程。
4. 对比 tab(status=done)保持原有压缩前后对比。

## 状态转换

- `captureBusy`(atomic):同一时刻只允许一次截图。
- 管线开启时,成功的截图不抢焦点:用户下一步是粘贴,不是打开队列。
  错误路径仍然前置窗口;`ScreenshotClipboard` 与 `ScreenshotPipeline` 都关
  时成功截图也会前置窗口,让队列确认截图已加入。
- 管线开关在界面上依赖「截图后复制到剪贴板」：后者关闭时前者禁用并显示
  原因；关闭复制会同时把已存的管线设置改回 off，重新打开复制不会悄悄恢复
  剪贴板替换。
- 自动流程把同时到达的多张截图并入同一批;压缩结束时只替换**最后一次**
  截图对应的剪贴板内容,更早的截图结果留在队列里。
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

- 全部在本机完成(不变量 1);`decorateScreenshot` 是纯 Go 绘制,无网络;
  Markdown 复制只写本地路径,不上传。
- 绝不覆盖原图(不变量 2):美化结果永远是 `原名-美化.png`,重名加序号。
- 绝不覆盖用户更新的剪贴板:压缩结果要求捕获租约仍有效且解码像素仍相等；
  没有像素摘要直接跳过，绝不退回租约去覆盖未知图片；空剪贴板、纯文字剪贴板
  一律不写。没有所有权记录的条目不会覆盖任何非空剪贴板；原始写入失败也不
  记录所有权。
- 跨平台回读差异是已知事实：Windows（CF_DIB）与 Linux 会返回重新编码过的
  图片，因此所有权比较从不依赖容器字节；本机只在 macOS
  验证过真实剪贴板往返，Windows/Linux 路径由单元测试模拟重编码字节守契约。
- 美化是显式动作:没有全局设置、没有截图时静默套用;样式不记忆
  (不写 localStorage),每次打开都回到默认。
- 非法样式输入由 `normalizeFrameStyle` 兜底为默认渲染,不报错。

## 验证命令

```sh
go test -race -run 'TestIssue17' .   # 权限检查、错误文本契约、窗口前置
go test -race -run 'TestScreenshotPipeline|TestScreenshotClipboard|TestCopy' .  # 管线、租约/像素守卫、Markdown
go test -race -run 'TestIssue1' .   # 样式归一化、各旋钮、条目应用、预览
go test -race -run 'TestPattern' .  # 图案闭集、平铺比例、边界画布、阴影色
go test -race ./...                 # 全量
bun run typecheck                   # tsc --noEmit
```
