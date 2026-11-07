# 截图链路（#1 之后)

> 职责边界、source of truth、主要运行路径、状态转换、重要约束、验证命令 ——
> 每页六样，事实只写这一页,别处链接过来。

## 职责边界

`screenshot.go` + `decorate.go`：全局快捷键、区域截图命令选择、截图外观
美化（`decorateScreenshot`）、截图归档（`writeScreenshot`）。压缩管线归
[compressor.go](../../compressor.go)（其 `DecorateItem` 是美化在队列里的
入口）；页面同步归 `index.html` + `src/main.ts`。

## Source of truth

- 快捷键与剪贴板偏好:`screenshot.go` 读 `Settings.ScreenshotShortcut` /
  `ScreenshotClipboard`。
- 外观偏好:`Settings.ScreenshotBorder`（on/off）与
  `Settings.ScreenshotBackground`（off/light/dark/gradient），两个独立
  设置，默认都是 off;页面通过 `AnyPic.decorateItem(id, settings)` 把
  当前值传给 `DecorateItem`。
- 外观参数(padding/radius/配色)定义在 `decorate.go` 的
  `framePadding`/`frameRadius`/`newBackdrop`/`ringColor`。

## 主要运行路径

1. 快捷键/托盘/菜单 → `screenshotRequested` → 页面 `captureScreenshot`
   → `AnyPic.CaptureScreenshot`。
2. `captureRegion` 取到 PNG 后、复制/落盘/入队之前:若边框或背景开启,
   `decorateScreenshot` 重绘画布(圆角、描边、投影、背景),解码或编码
   失败则退回原始截图。之后剪贴板、`writeScreenshot`(Pictures/AnyPic)、
   `AddPaths` 拿到的都是同一份美化后的图。
3. 队列补加:页面行内「美化」按钮 → `AnyPic.decorateItem(id, settings)`
   → 解码 `item.Path` → `decorateScreenshot` → 写 `原名-美化.png` 在
   原文件旁 → 原地重置该 item 为 ready 并重生成缩略图 → autoProcess
   开启时页面把它并入自动流程。

## 状态转换

- `captureBusy`(atomic):同一时刻只允许一次截图。
- 队列 item:美化把 item 重置为 ready;压缩中(`a.running`)时
  `DecorateItem` 拒绝并删除已写出的文件。

## 重要约束

- 全部在本机完成(不变量 1);`decorateScreenshot` 是纯 Go 绘制,无网络。
- 绝不覆盖原图(不变量 2):美化结果永远是 `原名-美化.png`,重名加序号;
  截图链路的解码失败退回原图。
- 两个外观偏好默认 off,非法值归一化为 off(旧 settings.json 无此键,
  行为不变)。
- PNG 输出无损(不变量 3)。

## 验证命令

```sh
go test -race -run 'TestIssue1' .   # 外观归一化、三种样式、装饰条目
go test -race ./...                 # 全量
bun run typecheck                   # tsc --noEmit
```
