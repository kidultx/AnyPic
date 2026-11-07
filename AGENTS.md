# Notes for coding agents

AnyPic：跨平台图片压缩桌面应用（MyGo + 系统 WebView；Go 压缩管线 + 原生
TypeScript 前端）。产品定位、功能与下载见 [README.md](README.md)。

## 不变量（绝不破坏）

1. **所有图片只在本机处理。** 应用本体不发起网络请求：没有上传、没有账户、
   没有遥测。新增依赖或代码路径前先确认它不引入网络访问；Linux 截图调用的
   外部工具（gnome-screenshot、spectacle、grim 等）也只做本机操作。
2. **绝不覆盖用户的原图。** 输出文件名一律是 `原名 + 后缀`（默认 `-anypic`，
   `outputFilename`）写在原图旁边，或写到用户指定的目录；写入先落临时文件。
3. **诚实的压缩结果。** 重新编码反而更大就用原图并标注「已是最优」；动态
   GIF/WebP 明确跳过，绝不只保留第一帧；透明不被压成黑底或白底；PNG 输出
   是无损重编码，质量滑杆对它不生效是格式限制——如实展示，不假装生效。
4. **只写自己的文件，默认不盖用户值。** 设置只进
   `mygo.App.Path(PathUserData)/settings.json`，截图只进用户图片目录的
   `AnyPic/`，退出时清理临时目录；默认值只在用户没设置过时生效。
5. **`src/mygo.ts` 是生成物**（文件头 `Code generated ... DO NOT EDIT`）。
   改 IPC 接口 = 改 Go 侧定义 + `bun run generate`，绝不手改。

改动一条不变量 = 破坏性变更：先开 issue 说明理由，获准后再动，并同步本文件。

## 改代码前先读

| 要改的东西 | 先读 |
| --- | --- |
| 支持的格式、上限、各平台注意事项 | [README.md](README.md)「支持格式与限制」 |
| 各文件的职责边界 | [README.md](README.md)「项目结构」 |
| 打包、签名与发版流程 | [README.md](README.md)「验证与打包」「发布与 CI」 |
| [LESSONS.md](LESSONS.md) | 改代码前扫一眼（目前为空，出现教训后必读） |

压缩管线、截图链路或 UI 状态的契约第一次「跨文件」或「不那么显然」时，
为它写一页并加进 [docs/subsystems/](docs/subsystems/README.md) 的索引。

## 验证（合入前必跑；跑了什么、没跑什么，如实写进提交说明）

```sh
bun run typecheck       # tsc --noEmit
go vet ./...
go test -race ./...     # 全量跑，不是只跑你以为改动的包
```

- 测试一律用 `t.TempDir()`，不读写真实的 `settings.json`、用户图片目录
  和 `~/Pictures/AnyPic`。
- 测试不碰网络（应用本身也不该有网络路径，见不变量 1）。
- 修复带一个「在旧代码上会红」的测试：改回旧代码 → 它必须 FAIL，
  提交说明里写它失败在哪。
- 界面改动额外过一遍：亮/暗两种主题、`bun run dev:web` 的 demo 模式、
  窄窗口（最小 940×640）。

## 提交与 PR

- 一个提交，一个目标。
- 大改动（>400 行或同时跨压缩 / 截图 / UI 三个面）的 body 必须写：
  what was run、what was broken by hand、what wasn't tried。
- 行为变了 → README 对应节或子系统页在同一 PR 里更新；保契约的重构
  不需要改文档。
- PR 描述写语义变更：触发条件 + before/after + 链到证明它的测试。
  这是给 reviewer 核对用的，不是 diff 转述。
- 发版：改 `mygo.config.ts` 的 `version` → tag `v*` → push → Actions 出
  三平台草稿 Release，确认后发布（细节见 README「发布与 CI」）。
