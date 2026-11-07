# Interface shell

窗口的那一页：header、队列表格、侧栏设置、对话框。register 和约束住在
这里。每条约束按 magpie 的形状记录：**一句规则 → 一个 token/helper 名 →
一个守卫测试 → 一段判例**。判例目前是 magpie 的（每条注明出处）——
AnyPic 自己的第一起事故发生时，回来记在这里。

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
