# 压缩管线：输出格式与诚实结果

> 职责边界、source of truth、主要运行路径、状态转换、重要约束、验证命令 ——
> 每页六样，事实只写这一页，别处链接过来。

## 职责边界

`compressor.go`：队列、压缩管线、预览、导出与设置持久化。本页只固定
「输出格式怎么定」和「诚实结果标记」；文件增删、导出路径与设置字段以源码
为准。动图帧计数在 `animation.go`，美化在 `decorate.go`，截图链路见
[screenshot.md](screenshot.md)。

## Source of truth

- 输出格式只由 [`chooseOutputFormat`](../../compressor.go) 决定，入参是
  设置、源格式和缩放后的 `imageHasAlpha`。`compressOne` 消费结果，不在
  编码分支里二次判断。
- 用户选 JPG 且缩放后的图带透明 → 输出 PNG，保留 alpha，不铺白底
  （不变量 3，#15）。不透明图选 JPG 仍出 JPG；自动 / PNG / WebP 三个
  选项不受影响。
- 动图不看格式选项：`compressAnimated` 输出动画 WebP；转码反而更大就保留
  原动图（#8）。
- 诚实结果标记写在 `Item` 上并随 IPC 到页面：
  - [`PassedThrough`](../../compressor.go)：静态图同格式、未缩放且重编码
    不更小（动图为转码不更小），保留原图，页面显示「已是最优」。
  - [`Animated`](../../compressor.go)：多帧 GIF/WebP 走 `compressAnimated`。
  - [`TransparencyKept`](../../compressor.go)：仅当「用户选了 JPG、因图带
    透明而改存 PNG」时为真；passthrough 结果同样为真，因为原因相同（#15）。

## 主要运行路径

1. `Compress` / `CompressReady` / `CompressFailed` → `startCompression` →
   `runBatch`（最多 3 个 worker）→ `compressItem`。
2. `compressItem` → 动图？[`compressAnimated`](../../animation.go)：[`compressOne`](../../compressor.go)。
3. `compressOne`：解码 → 可选 `MaxDimension` 缩放 → `imageHasAlpha` →
   `chooseOutputFormat` → 编码 → 「同格式 + 未缩放 + 不更小」则 passthrough。
4. 结果写回 `Item` 的 `OutputFormat` / `OutputName` / `PassedThrough` /
   `TransparencyKept`，`ItemChanged` 广播给页面。

## 状态转换

- 每轮开始时 `compressItem` 清零 `PassedThrough`、`TransparencyKept` 等
  结果字段；成功结束按本次结果重写，失败只置 `Error`。
- 美化应用（`DecorateItem`）把条目重置为 ready，同样清空这些标记。
- `TransparencyKept` 只属于静态图路径；自动 / PNG / WebP 设置与动图路径
  永远为 false。

## 重要约束

- 不覆盖原图（不变量 2）：输出名 = `原名 + 后缀`，先写临时文件。
- 诚实结果（不变量 3）：透明不铺黑/白底；重编码更大保留原图并标注；
  PNG 无损，质量滑杆对它不生效。
- 新增格式或格式规则只改 `chooseOutputFormat`，不要在编码分支补条件。
- 页面据 `TransparencyKept` 把队列行写成「→ PNG · 保留透明」，标题说明
  原因；不新增视觉样式。

## 验证命令

```sh
go test -race -run 'TestIssue15' .
go test -race ./...
bun run typecheck
```
