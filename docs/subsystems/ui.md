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

## 布局契约（#19 / #22 / #28）

三条 2026-11 走查定下的骨架规则，改布局前先对照：

- **一个实心主按钮**：住在页脚右端，位置不随模式移动；待处理或设置已改
  时是「压缩全部 / 重新压缩」，全部完成后是「导出」。截图在标题栏，不再
  借用页脚按钮。唯一例外是空状态——页脚此时隐藏，「选择图片…」就是下一
  步。守卫：`TestIssue19OnePrimaryAction`。
- **标题栏承载入口**：「截图 / 添加」在 46px 标题栏右侧（no-drag），页头
  只有一行：计数 + 原始→压缩后·节省 + 未完成数。窗口按钮所在位置由
  `--mygo-titlebar-inset-*` 留出，别在标题栏右侧加固定宽度。守卫：
  `TestIssue28TitlebarCarriesTheActions`。
- **侧栏只回答「这批图怎么压」**：首屏是质量、输出格式、保存到；应用偏好
  （主题、开机启动、Dock、截图快捷键与剪贴板）在「偏好设置」对话框
  （⌘,，菜单「视图」与侧栏底部都有入口）。守卫：
  `TestIssue22PreferencesLiveInADialog`。
- **窄窗口不许吞掉实话**：未完成数用 `.summary-pending`，不是会被
  ≤1040px 规则隐藏的 `.summary-foot`。守卫：
  `TestIssue31PendingNoteSurvivesNarrowWindows`。

## 预览的缩放与翻页（#21）

缩放只作用在两张 `<img>` 上（`--compare-zoom` / `--compare-pan-*`），分割线与
透明棋盘格的裁切留在画框坐标里——所以放大后分割线仍在鼠标下，仍把两张图切在
同一个源像素。别把 transform 挪到 `.compare-frame` 或 `.compare-after-wrap` 上。

- 比例以「每源像素几个 CSS 像素」记：适应窗口每次从画框重算；「100%」固定为 1
  （小图被适应窗口放大时也回得到 1）；滚轮走自定义档，读数只在有歧义时才有意义，
 但仍始终显示。滚到正好是适应窗口时吸附回该档，免得两个按钮都不亮。
- 翻页顺序 = 队列当前看到的顺序（状态页签 + 文件名过滤），由
  `PreviewContext.visibleIds()` 提供；对比页签只翻 `done`，美化页签也含 `ready`。
- ←/→ 全局翻页，焦点在分割线手柄上时归手柄（滑杆自己的方向键），输入框里不翻页。

守卫：`TestIssue21PreviewZoomsAndNavigates`。

## 队列的键盘与右键（#20）

队列是一张可选列表，不是一堆可点的 div；行为住在 `src/queue-keys.ts`，
main.ts 只提供状态（`QueueKeysContext`）。

- **一个 Tab 站**：行用 roving tabindex（`queueRowTabIndex`），只有当前行是
  `0`，其余 `-1`；`ensureQueueFocus` 在过滤 / 渲染后把焦点留在仍可见的行上。
  ↑/↓ 移动焦点；Shift+↑/↓ 以锚点扩选。Shift+点击的锚点在 `pointerdown` 时
  记住（点击会先把焦点移到新行，click 里再读就已经太晚）。
- **快捷键分层**：⌘/Ctrl+A 选可见项、Delete/Backspace 移除所选；⌘⌫ 仍是
  「清空队列」（main.ts 拥有，列表快捷键遇 meta 直接让路）。输入框与打开的
  对话框期间一律不接管按键。
- **右键菜单**（`.context-menu`）只做行上已有的操作加「在文件夹中显示」；
  右击未选中的行先把选择收成这一行，右击已选中的行保留整组，移除项的文案
  随之变成「移除所选的 N 张」。菜单用 `role="menu"`、方向键 / Escape / Tab
  / 点击外部都能收，Escape 把焦点还给行。
- 列表与网格渲染的是同一个行元素，所以两种视图天然行为一致。

守卫：`TestIssue20QueueIsAKeyboardList`。

## 字号、圆角与控件高度（#23）

三组阶梯都定义在 `src/style.css` 的 `:root` 里，token 注释写明每档用途。
新控件不是新数值，而是最近的一档：

| 组 | token | 档位 | 用在哪 |
| --- | --- | --- | --- |
| 字号 | `--fs-micro` / `--fs-small` / `--fs-body` / `--fs-title` / `--fs-display` | 11 / 12 / 13 / 15 / 18 | 元信息与徽标 / 次级标签与按钮 / 正文与行名 / 标题与主操作 / 空状态大标题 |
| 圆角 | `--radius-control` / `--radius-card` / `--radius-dialog` / `--radius-pill` | 6 / 10 / 14 / 全圆 | 按钮、输入、页签 / 卡片、菜单、浮层 / 对话框 / 开关与徽标 |
| 控件高度 | `--control-sm` / `--control-md` | 26 / 32 | 页签、图标按钮、次级小按钮 / 按钮、输入、主操作 |

规则：token 块之外不写 px 的 `font-size` 或 `border-radius`；百分比圆角
（`50%`、logo 的 `23%`）与 `inherit` 描述形状而不是阶梯，可以留在规则里。
半像素字号已全部移除。守卫：`TestIssue23TypeAndRadiusLadders`。

## 约束与执行

| 约束 | 现在由谁执行 |
| --- | --- |
| 颜色一律 token：hex 只许住在 token 块里；阴影的 rgba 字面量允许但不新增 | `TestStyleColorsLiveInTokens` |
| 不新增原生 `<select>`；例外 `maxDim`（`.select-wrap` 包装） | `TestNoNewNativeSelects` |
| 字号 / px 圆角 / 控件高度只用阶梯 token | `TestIssue23TypeAndRadiusLadders` |
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
