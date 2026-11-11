# Subsystem design references

这些页面描述一个子系统的当前职责、运行路径、状态与契约，供改代码前核对。
产品事实（功能清单、格式限制、下载）归 [README.md](../../README.md)；
行为契约归这里，别两处都写。

| 子系统 | 参考 |
| --- | --- |
| 应用外壳：单实例、窗口、托盘与退出 | [app-shell.md](app-shell.md) |
| 界面外壳：register、约束、预算 | [Interface shell](ui.md) |
| 压缩管线：输出格式与诚实结果标记 | [compressor.md](compressor.md) |
| 截图链路（快捷键、美化工作台、归档） | [screenshot.md](screenshot.md) |
| 更新器（检查、下载、代开安装） | [updater.md](updater.md) |
| 文案与语言（目录、生成物、错误码） | [i18n.md](i18n.md) |

（一个子系统的契约第一次「跨文件」或「不那么显然」时，才为它写一页并在此
加一行。目前的候选块：截图链路 `screenshot.go`、UI 状态与渲染
`index.html` + `src/main.ts`、MyGo IPC 桥 `src/mygo.ts`（生成物）。）

## Maintain a reference

行为改变（职责、接口、状态转换、运行路径、兼容、失败行为）→ 同一 PR 更新
所属页；保契约的内部重构不需要改文档。

- 一个事实只写一页，别处链接过来。
- 链接源码文件与命名符号，不抄易变的实现细节。
- 每页六样：职责边界、source of truth、主要运行路径、状态转换、重要约束、
  验证命令（可复制粘贴的命令，不是对命令的形容）。
- 文档是声明，测试是证明；文档更新不替代测试与运行证据。

## Describe a PR's semantic changes

逐子系统说明：可观察的变化 + 触发条件 + before/after；涉及接口、状态、
兼容或失败行为时写明；链接参考页、实现与验证证据。说「重构未改行为」时
也要写明——这是作者提供、reviewer 核对的说明，不是自动得出的兼容结论。

## Review a change

1. 找到受影响子系统与真实运行路径（含调用方与状态转换，不止改动行）。
2. 对照 base/head 核对声称的 before/after。
3. 改动的契约是否进了所属参考页；页内源码链接是否仍解析。
4. 引用的测试是否真覆盖声称的行为；没跑的检查如实报告。
