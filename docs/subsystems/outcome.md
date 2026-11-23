# 一次压缩的结果：Outcome

## 职责边界

[`outcome.go`](../../outcome.go) 定义一次压缩的不可变结果；编码、目标体积
搜索、文件写入策略仍由[压缩管线](compressor.md)负责，没有更换编码器或
改变取舍。导出后的保存记录和剪贴板所有权仍是 `Item` 的会话状态，不属于
「这次压缩做了什么」。

## Source of truth

`Item.Outcome` 是唯一的结果，`nil` 表示当前没有压缩结果。`Outcome` 的字段
全部私有，没有 setter；`Snapshot()` 返回边界副本，`MetadataReport` 在构造
时和取快照时各拷贝一次，不能通过副本改变已发布的结果。Go 允许外部构造
任何导出结构体的零值，因此 `Outcome{}` 被定义为安全的 `Failed / Unknown`
结果，而不是成功、保留原图或「未压缩」。

- `OutcomeKind`：`encoded`、`passed_through`、`failed`、`canceled`。
- `OutcomeReason`：重编码的空 reason；保留原图的 `not_smaller`、`target_met`、
  `pixels_changed`、`animated_png`、`color_space`；取消的 `canceled`；各编码、
  解码、读取、写入失败的稳定原因，以及未知错误的 `unknown_error`。
- `newEncodedOutcome`：要求文件路径、体积、格式、文件名和尺寸，固定 kind / reason，
  从原体积计算节省比例。
- `newPassedThroughOutcome`：唯一的保留原图构造器。要求有效 reason，收集原文件
  事实，统一文件名、目标体积标记和画质测量标记，不声明清洗过元数据。
- `newFailedOutcome`：错误归类为稳定 reason；`context.Canceled` 单独归为
  `canceled`，失败和取消不携带成功结果的文件或体积。

所有构造路径都经过 `newOutcome` 的 kind / reason 配对守卫。非法调用是程序
错误，会 panic；文件读取等可预期失败则返回失败 Outcome，不 panic。

`OutcomeSnapshot` 是一次性序列化值，集中声明体积、输出格式与文件名、尺寸与
透明、元数据、节省比例、SSIM 与是否测量、PNG 快速模式和目标体积信息；它不是
构造 Outcome 的公开入口。新增结果字段不需要给 `Item` 再加复本或修改重置块。

## 主要运行路径

1. `compressOne` / `compressAnimated` 返回一个 `*Outcome`，没有第二套结果字段
   或独立错误状态；底层编码器仍按原有接口返回字节和 error。
2. `compressItem` 在锁外编码，在锁内只替换 `Item.Outcome`；不逐字段写回结果。
3. `Item.snapshot()` 生成 `ItemSnapshot`。源图尺寸仍留在 `Item`；成功结果在
   快照中投影本次输出尺寸与透明信息，保持页面原有展示。
4. [`src/outcome.ts`](../../src/outcome.ts) 在 IPC 入口检查 kind / reason，复制并
   freeze 结果及元数据。`presentOutcome` 统一保留原图、SSIM、目标未达和错误
   的显示判断；队列、预览只渲染它。demo 也一次赋值整个 Outcome。

### IPC 明确升级

快照与 `anypic:item` 事件都使用 `ItemSnapshot`。原来平铺在 item 上的结论字段
已移入 `item.outcome`，`passedThrough` 改为 `kind === "passed_through"`，
`keptOriginalReason` 改为 `reason`；不保留第二份兼容复本。无结果明确发送
`"outcome": null`。`status` 的 ready / working / done / error 值保持不变。

Go 私有字段不能被 MyGo 反射成类型安全的前端字段，因此边界使用独立、脱离
后端存储的 `OutcomeSnapshot`；不依赖手写 `MarshalJSON` 或手改 `src/mygo.ts`。
前后端随本次生成同步升级，旧的平铺协议客户端不能混用。会话临时结果路径
不发送到前端。

## 状态转换

| 事件 | 当前结果 |
| --- | --- |
| 加入队列 / 新一轮排队 / 开始工作 | `nil` |
| 重编码成功 | `encoded / 空 reason` |
| 保留原文件 | `passed_through / 必填 reason` |
| 编码、读取等失败 | `failed / 稳定错误 reason` |
| 实际工作的图片被取消 | `canceled / canceled` |
| 重试 / 美化替换源图 / 清理 | `nil` |

`resetOutcome` 删除旧的重编码临时结果、置 `Outcome = nil` 并清除保存记录；保留
原图的路径绝不删除。取消的批次中尚未开始的图片仍为 ready / nil，已开始且
收到取消的图片仍走原有 error 状态与失败计数，没有改变批次取消行为。

## 重要约束

- 不变量 1–6 不变：不增加网络、编码器、用户文件写入口或设置。
- 保留原图没有空 reason；失败和取消不能借用成功的体积或文件名。
- `compressionError` 存原因，不存翻译。Go 的 `Err().Error()` 与前端
  `presentOutcome` 在显示时读当前目录；切语言后旧结果仍能重新显示。
- 未知底层错误的 `ErrorDetail` 只保留原始诊断，不猜原因、不伪装成译文。
- `Item.completed()` 要求成功 Outcome，只有手改 status 的伪完成态不能导出
  或被当成截图压缩结果；截图所有权测试通过实际压缩获得完成结果。

## 验证命令

```sh
bun run generate
bun run typecheck
bun run test
go vet ./...
go test -race -count=1 -timeout 25m ./...
```

`outcome_test.go` 覆盖私有字段、非法配对、零值、五种 passthrough、元数据
值语义、排队及工作时的重置、临时文件与原图、失败/取消、美化和 IPC 协议。
`tests/outcome.test.ts` 覆盖入口拒绝非法状态、freeze、画质边界、未测量动画、
目标未达、保留原图原因和显示时本地化。原有格式、透明、元数据、动图、
目标体积与压缩基准测试继续验证同一组编码行为。
