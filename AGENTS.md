# Notes for coding agents

AnyPic：跨平台图片压缩桌面应用（MyGo + 系统 WebView；Go 压缩管线 + 原生
TypeScript 前端）。产品定位、功能与下载见 [README.md](README.md)。

## 不变量（绝不破坏）

1. **所有图片只在本机处理。** 应用不上传图片、没有账户、没有遥测；图片
   处理的字节从不离开本机。应用唯一的网络行为是**更新检查与安装包下载**
   （#3 获准的例外）：检查只在启动与用户点击时发生，下载只在用户点击后
   发生，且只访问 GitHub Releases。新增依赖或代码路径前先确认它不引入
   其他网络访问；Linux 截图调用的外部工具（gnome-screenshot、spectacle、
   grim 等）也只做本机操作。
2. **绝不覆盖用户的原图。** 输出文件名一律是 `原名 + 后缀`（默认 `-anypic`，
   `outputFilename`）写在原图旁边，或写到用户指定的目录；写入先落临时文件。
3. **诚实的压缩结果。** 重新编码反而更大就用原图并标注「已是最优」；动图
   绝不只保留第一帧——动态 GIF/WebP 转成保留帧数、延迟与透明的动画
   WebP（#8，2026-11-08 变更，原为明确跳过），转码反而更大就保留原图并
   标注；透明不被压成黑底或白底；PNG 输出是无损重编码，质量滑杆对它不
   生效是格式限制——如实展示，不假装生效。
4. **只写自己的文件，默认不盖用户值。** 设置只进
   `mygo.App.Path(PathUserData)/settings.json`，截图只进用户图片目录的
   `AnyPic/`，退出时清理临时目录；默认值只在用户没设置过时生效。
5. **生成物绝不手改。** `src/mygo.ts`（文件头 `Code generated ... DO NOT
   EDIT`）、`src/locales.ts` 与 `locales/catalog_gen.go` 都由
   `bun run generate` 写出。改 IPC 接口 = 改 Go 侧定义 + generate；改文案 =
   改 `locales/*.json` + generate。

改动一条不变量 = 破坏性变更：先开 issue 说明理由，获准后再动，并同步本文件。

## 改代码前先读

| 要改的东西 | 先读 |
| --- | --- |
| 支持的格式、上限、各平台注意事项 | [README.md](README.md)「支持格式与限制」 |
| 各文件的职责边界 | [README.md](README.md)「项目结构」 |
| 文案、语言与错误码 | [docs/subsystems/i18n.md](docs/subsystems/i18n.md) |
| 打包、签名与发版流程 | [README.md](README.md)「验证与打包」「发布与 CI」 |
| [LESSONS.md](LESSONS.md) | 改代码前扫一眼（目前为空，出现教训后必读） |

压缩管线、截图链路或 UI 状态的契约第一次「跨文件」或「不那么显然」时，
为它写一页并加进 [docs/subsystems/](docs/subsystems/README.md) 的索引。

## 报告与修复

**issue 是唯一需求入口。** 一切行为变化先有 issue——用户报的、自己想的
都一样（自己的想法自己开 issue），提交与 PR 引用 `#号`。issue 模板收的
字段（平台 × 图片特征 × 设置）就是复现表面。bug issue 关闭前等报告者
确认修好；自己报的自己确认。

**修复前先在报告者的场景复现。** fixture 用报告者的字面输入，不用相近
的自造样例：

- 原文件能提供 → 存 `testdata/issue-<n>/`，测试从那里读；
- 不能提供 → 代码合成最接近的案例，并在测试或 issue 里写明保留了报告者
  哪些特征（格式、透明、尺寸、EXIF 方向）；
- 平台相关（截图、托盘、窗口）在报告者的平台验证，或如实写明没跑。

复现不了就回到 issue 问信息，不开工。

**一个行为变化一个提交，红测试先行。** 先写「在旧代码上会红」的测试
（名字带 issue 号，如 `TestIssue12TIFFOrientation`），看它 FAIL，再修；
修类不修样例——grep 同型兄弟（其他格式、其他平台分支、同型调用方）一起
修。版本号与发布是独立提交。

## 验证（合入前必跑；跑了什么、没跑什么，如实写进提交说明）

```sh
bun run typecheck       # tsc --noEmit
go vet ./...
go test -race ./...     # 全量跑，不是只跑你以为改动的包
```

- 测试一律用 `t.TempDir()`，不读写真实的 `settings.json`、用户图片目录
  和 `~/Pictures/AnyPic`。
- 测试不碰网络（应用本身也不该有网络路径，见不变量 1）。
- 红测试怎么写、fixture 从哪来：见「报告与修复」。
- 界面改动额外过一遍：亮/暗两种主题、`bun run dev:web` 的 demo 模式、
  窄窗口（最小 940×640）。

## 提交与 PR

- 一个提交，一个目标：两份报告分开修成两个提交，各自的验证记录各自带
  ——这样 revert 拿走一个、留下另一个，review 也看得出哪个测试守哪个
  改动。顺手修的东西单独成提交。
- 提交主题写成「区域: 用户看得见的行为」，不写实现，如
  `compressor: 动态 GIF/WebP 被明确跳过并说明原因，不再只保留第一帧`。
- What was run 的记录住在提交 body 里——推送备注、聊天或只写在 PR 里都
  不算，事后能读到的只有 body。大改动（>400 行或同时跨压缩 / 截图 /
  UI 三个面）的 body 必须写：what was run、what was broken by hand、
  what wasn't tried。
- UI 改动的提交信息写明谁要的、为什么：issue `#号`、报告者，或自己的想法
  ——一句就够。每笔看得见的界面改动都要能回答「为什么存在」；站得住的
  设计理由沉淀为 `src/style.css` 的 token 注释。
- 行为变了 → README 对应节（`README.md` 与英文版 `README.en.md` 两份）
  或子系统页在同一 PR 里更新；保契约的重构不需要改文档。
- PR 描述写语义变更：触发条件 + before/after + 链到证明它的测试。
  这是给 reviewer 核对用的，不是 diff 转述。
- 审查（自己审和审别人一样）：读 diff 不算 review——把 head 拉下来、
  合到当前 main 上、跑一遍「验证」里的命令；再故意弄坏被守的代码（多坏
  几种方式），确认新测试每一种都能抓住。
- 提交后自查 `git show --stat HEAD`：列出的文件必须全是自己的——共用
  checkout 时会把别人的改动带进自己的提交。
- 发版：改 `mygo.config.ts` 的 `version` → tag `v*` → push → Actions 出
  三平台草稿 Release，确认后发布（细节见 README「发布与 CI」）。tag 前
  先 `git fetch --tags` 查下一个空闲版本；只有被 tag 的那个 commit 的 run
  绿了才算（被下一次 push 取消的 run 不算）；点发布前核对三平台产物都在
  ——tag 不是 release，对人说版本号之前先看产物。
