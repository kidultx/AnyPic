# Interface shell

窗口的那一页：header、队列表格、侧栏设置、对话框。register 和约束住在
这里。每条约束按 magpie 的形状记录：**一句规则 → 一个 token/helper 名 →
一个守卫测试 → 一段判例**。借来的判例注明出处；自己的事故记在后面。

## Register

One quiet surface, pill controls that read as pop-up buttons, the accent
kept for what acts（`src/style.css` 第一行）。中性底、白色细边卡片、
发丝分隔线、唯一靛蓝 accent；阴影只给浮层和对话框，卡片靠边框。

## 四层

| 层 | 住在哪 | 管什么 |
| --- | --- | --- |
| 字面规则 | [docs/standards.md](../standards.md)「界面」 | reviewer 逐条查 |
| 约束的名字 | `style.css` token + 注释；（将来）helper 名 | 做成只能正确使用的工具 |
| 守卫测试 | `ui_rules_test.go` | 违反 = 红，没人能忘掉 |
| 判例 | magpie 的见下；自己的待第一起事故 | 规则 + 证据 + 测试名 |

## 约束与执行

| 约束 | 现在由谁执行 |
| --- | --- |
| 颜色一律 token：hex 只许住在 token 块里；阴影的 rgba 字面量允许但不新增 | `TestStyleColorsLiveInTokens` |
| 不新增原生 `<select>`；例外 `maxDim`（`.select-wrap` 包装） | `TestNoNewNativeSelects` |
| main.ts / style.css / index.html 大小预算 | `TestScriptAndStyleBudgets` |
| 一次点击永不滚动页面；有长列表需求时先建 `scrollOnPurpose` 等价物再写功能 | 规则已立，review 时查 |
| 动效尊重 `prefers-reduced-motion`；全站一条 easing | style.css `@media`；review 时查 |
| 不用彩色左边条标状态；状态用圆点或色块 | review 时查 |
| 对比度：token 注释里的测量数字是承诺（`--ctl-fg` 在它坐的每个面上 ≥4.5:1） | 改 token 值时重算并更新注释 |
| 退场状态（`.leaving` 等）不接点击；移除只走 [`removeAfterExit`](../../src/ui.ts)，事件之外必须有定时器兜底；action 在 dismiss 后失效 | `TestIssue16ExitStatesIgnorePointerEvents`、`TestIssue16ExitRemovalHasTimerFallback` |

## 退场的契约（#16）

淡出中的元素不再属于界面：它不接点击，也不许无限期留在页面上。

- **不接点击**：退场状态类（`.toast.leaving`，以及 `.hidden` 这类同类
  关闭状态）在自己的规则里写 `pointer-events: none`，或从同一元素的
  基础规则继承（`.compare-hint.hidden` 来自 `.compare-hint`）；`display:
  none` 的状态本身安全。[style.css](../../src/style.css) 的
  `.toast.leaving` 带一条注释说明它为什么不能只压低 opacity。
- **移除走 helper**：[`showToast`](../../src/ui.ts) 的 `dismiss()` 只加
  `.leaving` 并把元素交给 [`removeAfterExit`](../../src/ui.ts)。该
  helper 监听元素自己的 `transitionend`（忽略子元素冒泡），另有一个略长
  于 CSS 0.14s 过渡的定时器兜底；两条路谁先到谁移除，只移除一次。
  `animationend`/`transitionend` 只许出现在这个 helper 里，别处直接挂
  监听会红。
- **action 随提示失效**：`dismiss()` 幂等，dismiss 后 mouseleave /
  focusout 不再重排定时器；带「撤销」等 action 的按钮在 handler 里检查
  dismissed，即使点击绕过 CSS 也不会执行。撤销入口随提示一起消失，
  兑现 #12 的「toast 消失后入口随之失效」。

同型审计：弹窗（`.modal` 去掉 `.open`）、拖放遮罩（`.drop-overlay`
去掉 `.visible`）和快捷键提示（`[hidden]`）都用 `display: none`，关闭后
不占点击区域；`.item-actions` 的 `opacity: 0` 是 hover 才显形的常驻
控件，不是退场状态。

## 借来的判例（magpie 的，Why these rules exist）

- **滚动是 UI 事故最多的一类**：非读者引发的滚动要放回（#1554）、点击不
  能把页面带走（#2134）、加载落在滚轮下会抖（#1672）、自动增高文本域
  挤压滚动（#2636）。所以"点击不引发滚动"是唯一一条要求先建机制再加
  功能的规则。
- **状态不用颜色单独表达**：红绿圆点对色盲是一个颜色（magpie 的
  coolmantou），失败态画 x；左彩条被明确禁用。
- **图表色板用 Okabe–Ito 色盲安全系列**（magpie #2073）——AnyPic 出现
  图表时照此选色。
- **大小预算**是账本不是枷锁：每个字节都能回答"谁要的、怎么付的"
  （magpie core_budget_test.go 的编年账）。
