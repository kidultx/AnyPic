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
  渐变预设)、圆角四档、描边、投影两档、边距三档、画布比例(只外扩不裁切)。
  **样式由页面持有**(美化工作台的状态,存 localStorage 的
  `anypic-frame-style`),不在 Settings/settings.json 里;后端只渲染,
  `normalizeFrameStyle` 把任何输入兜底成已定义渲染。
- 视觉参数(边距/圆角缩放、渐变预设表、描边色、投影档位)定义在
  `decorate.go`;页面侧的色板与渐变 chip 列表在 `src/main.ts`
  (`BG_SWATCHES`/`GRADIENT_PRESETS`),两边预设 id 对齐。

## 主要运行路径

1. 快捷键/托盘/菜单 → `screenshotRequested` → 页面 `captureScreenshot`
   → `AnyPic.CaptureScreenshot`:`captureRegion` 取 PNG → 剪贴板偏好 →
   `writeScreenshot`(Pictures/AnyPic)→ `AddPaths`。**截图从不被隐式
   美化。**
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

## 重要约束

- 全部在本机完成(不变量 1);`decorateScreenshot` 是纯 Go 绘制,无网络。
- 绝不覆盖原图(不变量 2):美化结果永远是 `原名-美化.png`,重名加序号。
- 美化是显式动作:没有全局设置、没有截图时静默套用;样式记忆只存页面
  localStorage。
- 非法样式输入由 `normalizeFrameStyle` 兜底为默认渲染,不报错。

## 验证命令

```sh
go test -race -run 'TestIssue1' .   # 样式归一化、各旋钮、条目应用、预览
go test -race ./...                 # 全量
bun run typecheck                   # tsc --noEmit
```
