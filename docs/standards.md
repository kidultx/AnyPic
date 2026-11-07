# Code standards

这里的规则来自 review 反复要求的东西，reviewer 逐条检查。
怎么描述与审查一个变更见 [subsystems/README.md](subsystems/README.md)。

「界面」一节是 owner 从 magpie 成批移植的（那里的判例写明每条规则怎么
来的，见 [subsystems/ui.md](subsystems/ui.md)）；其余小节仍守"review
要求第二次才写入"。

## 界面

| 规则 | reviewer 检查什么 |
| --- | --- |
| 颜色一律 token：hex 只许住在 `:root` / `[data-theme]` 块里，每个新 token 的注释写 why。阴影的 rgba 字面量允许但不新增。 | `TestStyleColorsLiveInTokens` 红 = 有字面量逃进规则里；改 token 值要重算注释里的对比度数字。 |
| 不用原生 `<select>`，新下拉做成应用自己的控件。唯一例外 `maxDim`（已用 `.select-wrap` 包装）。 | `TestNoNewNativeSelects` 红 = 出现了第二个原生 select。 |
| 一次点击永不滚动页面。有长列表/复杂布局需求时，先建 `scrollOnPurpose` 等价物再写功能——这是唯一一条"机制先行"的规则。 | review 时查；将来守卫测试接手。判例：magpie 滚动类事故 #1554、#1672、#2134、#2636。 |
| 状态不用颜色单独表达，不用彩色左边条。状态用圆点、色块或形状（x ≠ 红点：色盲下绿红同色）。 | review 时查。 |
| 动效尊重 `prefers-reduced-motion`；全站一条 easing/一条 spring。 | style.css 里每个 `transition`/`animation` 有 reduced-motion 伴随；review 时查。 |
| main.ts / style.css / index.html 有大小预算。超了：挪去自己的模块，或抬预算并在测试注释的账本里写明理由。 | `TestScriptAndStyleBudgets` 红 = 超预算；错误信息就这么说。 |

