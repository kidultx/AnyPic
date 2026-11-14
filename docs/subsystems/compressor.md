# 压缩管线：输出格式与诚实结果

> 职责边界、source of truth、主要运行路径、状态转换、重要约束、验证命令 ——
> 每页六样，事实只写这一页，别处链接过来。

## 职责边界

`compressor.go`：队列、压缩管线、预览、导出与设置持久化。本页固定
「输出格式怎么定」「目标体积怎么搜」和「诚实结果标记」；文件增删、导出
路径与设置字段以源码为准。目标体积搜索与 SSIM 测量在 `quality.go`，动图
帧计数在 `animation.go`，美化在 `decorate.go`，截图链路见
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
- 目标体积只由 `Settings.TargetSize` 决定（0 = 关闭）。静态 JPG/WebP 与动画
  WebP 最终都走 [`searchEncode`](../../quality.go)：先试质量 100，超限则在
  10–99 间二分找「能放进上限的最高质量」。二分以「质量越高体积不会更小」
  为前提，个别扁平动图会打破它，所以二分失败后再按 5 级阶梯从高到低探测，
  避免把本可命中的上限误报为未达。阶梯每次下降固定 5 级，循环必然结束；
  入口和每次探测前都检查 `ctx`，取消的批次不会发布半搜索的结果。连质量 10
  都超限时返回探测到的最小结果并置 `TargetMissed`，绝不悄悄放弃；动图每次
  探测都保留全部帧、延迟与循环。PNG 无损、没有质量旋钮，只能在自身结果超过
  上限时报 `TargetMissed`。已在 `TargetSize` 内且同格式、未缩放的原图直接
  passthrough（`SSIM = 1`）；原图超限但重编码只会更大时也保留原图，同样
  如实标 `TargetMissed`，此时 `TargetQuality = 0`（没有质量可报）。
  `TestTargetSizeFormatMatrix` 显式遍历 JPG / WebP / PNG：前两者必须命中且
  给出搜索质量，PNG 必须以 `TargetMissed` 和 `TargetQuality = 0` 结束；只
  覆盖可调质量格式的 `TestTargetSizeSearchNeverExceedsTheCap` 不能替代它。
- `TargetQuality` 只记录目标体积搜索实际落到的质量；普通质量模式的编码即使
  用了 `Settings.Quality` 也不填，避免页面把设置值说成搜索结果。PNG 目标
  结果同样为 0；页面据此显示「无损」，passthrough 则显示「保留原图」。
- 画质分数只由 [`ssimScore`](../../quality.go) 决定：Rec.709 灰度、
  11×11 高斯窗（σ=1.5，镜像边界）、C1/C2 取 8-bit 标准值，对「送进编码器
  的图」与「解码后的结果」求均值，因此 MaxDimension 的有意缩放不计为压缩
  损失。最长边超过 1024 时先等比缩小再测，控制大图耗时。动图不测
  （`SSIM = 0`，`SSIMMeasured = false`），passthrough 记 1 且视为测量过；
  真实的 0 分仍是测量结果，页面照常显示「明显损失」，两者不会混淆。
- libwebp 的 lossy still 解码结果是 limited-range YUV（黑 = 16）。
  `normalizeDecodedImage` 在 SSIM 与缩略图之前把它展开成全范围 NRGBA；不
  展开会把范围错配当成压缩损失（真实照片 q82 WebP 会从 0.986 掉到 0.55）。
- 诚实结果标记写在 `Item` 上并随 IPC 到页面：
  - [`PassedThrough`](../../compressor.go)：静态图同格式、未缩放且重编码
    不更小（动图为转码不更小），保留原图，页面显示「已是最优」。
  - [`Animated`](../../compressor.go)：多帧 GIF/WebP 走 `compressAnimated`。
  - [`TransparencyKept`](../../compressor.go)：仅当「用户选了 JPG、因图带
    透明而改存 PNG」时为真；passthrough 结果同样为真，因为原因相同（#15）。
  - [`SSIM` / `SSIMMeasured` / `TargetBytes` / `TargetQuality` /
    `TargetMissed`](../../compressor.go)：本次结果的实测画质、是否测量过，
    以及目标体积信息。页面用 SSIM ≥ 0.98 / ≥ 0.92 分「几乎无差别 / 轻微
    损失 / 明显损失」三档；未达目标的行同时标出最小体积和实际质量（无损
    PNG 标「无损」，passthrough 标「保留原图」）。

## 主要运行路径

1. `Compress` / `CompressReady` / `CompressFailed` → `startCompression` →
   `runBatch`（最多 3 个 worker）→ `compressItem`。
2. `compressItem` → 动图？[`compressAnimated`](../../animation.go)：解码全部帧、
   可选缩放 →（目标体积？`searchEncode`；否则按设置质量 `webp.EncodeAll`）
   → 转码不更小则 passthrough → 写临时文件。静态图走 [`compressOne`](../../compressor.go)。
3. `compressOne`：解码 → 可选 `MaxDimension` 缩放 → `imageHasAlpha` →
   `chooseOutputFormat` →（目标体积？`encodeToTarget`；否则 `encodeStill`）
   → 「同格式 + 未缩放 + 不更小」则 passthrough → SSIM 测量 → 写临时文件。
4. 结果写回 `Item` 的 `OutputFormat` / `OutputName` / `PassedThrough` /
   `TransparencyKept` / `SSIM` / `SSIMMeasured` / `TargetBytes` /
   `TargetQuality` / `TargetMissed`，`ItemChanged` 广播给页面。

## 状态转换

- 每轮开始时 `compressItem` 清零 `PassedThrough`、`TransparencyKept`、
  `SSIM`、`SSIMMeasured`、`TargetBytes`、`TargetQuality`、`TargetMissed`
  等结果字段；成功结束按本次结果重写，失败只置 `Error`。
- 美化应用（`DecorateItem`）把条目重置为 ready，同样清空这些标记。
- `TransparencyKept` 只属于静态图路径；自动 / PNG / WebP 设置与动图路径
  永远为 false。

## 重要约束

- 不覆盖原图（不变量 2）：输出名 = `原名 + 后缀`，先写临时文件。
- 诚实结果（不变量 3）：透明不铺黑/白底；重编码更大保留原图并标注；
  PNG 无损，质量滑杆与目标质量搜索对它不生效，只能如实报告未达目标。
- 目标体积是上限：可命中的结果绝不超过 `TargetSize`；无法命中时保留探测到
  的最小结果并置 `TargetMissed`，页面不得把它画成成功达标。
- 新增格式或格式规则只改 `chooseOutputFormat`，不要在编码分支补条件。
- 页面据 `TransparencyKept` 把队列行写成「→ PNG · 保留透明」，标题说明
  原因；不新增视觉样式。

## 基准集

`testdata/benchmark/` 固定了 19 个用例、`du` 832 KB，四类各至少两张：合成
截图 + 应用真实截图（`docs/screenshot.png`）、合成照片 + 两张 NASA JPL
公版地球照片、合成透明图 + 项目 logo、合成动图 + 从真实截图派生的动图。
合成图由 `testdata/benchmark/generate` 确定性生成；真实素材的来源与许可见
`testdata/benchmark/README.md`。`TestBenchmarkSet` 用各输出格式跑一遍，记录
体积、SSIM、动图的 `ssimMeasured=false` 与目标搜索的 `targetQuality`，并
对照 `baseline.json`：体积涨幅 >2% 或 SSIM 跌幅 >0.002 即失败。有意调整
编码参数后用 `-update-benchmark` 重录基线，并在提交里说明取舍。

三档阈值 0.98 / 0.92 取自这组数据在质量 82 下的分布：真实截图 WebP
0.9994 / 0.9987、logo 0.9908，真实照片 WebP 0.9865–0.9909、JPEG q60
0.9909–0.9944，合成照片 WebP 0.9329、目标体积结果 0.9421。0.98 让前两组
稳定落在「几乎无差别」且不把 q82 照片压在边界；0.92 让合成照片留在「轻微
损失」并保留两侧余量；测得的 0 分仍按「明显损失」展示，只有
`SSIMMeasured=false` 才是「未测量」。改阈值必须同时更新 `src/main.ts` 的
注释、`testdata/benchmark/README.md` 和本页。

## 验证命令

```sh
go test -race -run 'TestIssue15' .
go test -race -run 'TestTargetSize|TestSSIM|TestBenchmarkSet|TestCopy' .
go test -race -run 'TestTargetSizeFormatMatrix' .
go test -race ./...
bun run typecheck
```
