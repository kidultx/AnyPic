# 应用外壳：单实例、窗口、托盘与退出

描述 `main.go` 里的原生外壳：谁负责创建窗口、托盘与菜单，一次启动如何变成
「一份正在运行的应用」，以及退出为什么必须先清理。压缩、截图、更新各自有
自己的页；这里只管把它们挂起来的那层。

## 职责边界

- **管**：进程入口与启动顺序、单实例锁、窗口与托盘的生命周期、原生菜单、
  关闭窗口（缩到托盘）与真正退出（`App.Quit`）的区别、退出前清理。
- **不管**：页面内部状态（[ui.md](ui.md)）、压缩管线（[compressor.md](compressor.md)）、
  截图链路（[screenshot.md](screenshot.md)）、更新流程（[updater.md](updater.md)）。

## Source of truth

- [main.go](../../main.go)：`main`、`secondLaunchHandler`、`command`；
  窗口与托盘选项、关闭/激活/退出回调都在这里。
- [mygo.config.ts](../../mygo.config.ts)：应用名与标识符（`identifier`）——
  单实例锁按「标识符 + 应用名」标识，`mygo dev` 的 `<identifier>.dev` 因此
  与已安装的应用互不相干。
- 单实例锁本身在依赖里：`mygo.App.RequestSingleInstanceLock`
  （`github.com/egoist/mygo` 的 `single_instance.go`），本仓库不自己实现。
  它用的是用户临时目录下的本机 Unix 域 socket——进程间通信，不是网络
  访问，不触碰 AGENTS.md 的不变量 1。

## 主要运行路径

1. **启动**：`app.SetName("AnyPic")` → `RequestSingleInstanceLock()`。
   拿到锁的进程才是应用：先建服务（`NewAnyPic`，顺带清理旧截图目录 #18）、
   `mygo.Bind`；`WhenReady` 里创建窗口、托盘与菜单。
2. **第二次启动**（用户又点了一次图标，或从资源管理器打开一张图）：
   新进程在锁上被拒，把自己的命令行参数通过本机 socket 交给第一个进程后
   退出，`main` 直接 `return`。第一个进程收到后：
   - 文件参数 → `OnOpenFile` → `service.OpenFile(path)` + 显示窗口；
   - 无参数 → 只有 `OnSecondInstance`，也就是 `secondLaunchHandler`：
     把窗口 `Restore` + `Show` + `Focus` 带回前面（#14）。
   macOS 上从 Finder/Dock 再次打开不会起第二个进程（系统激活已在运行的
   实例，走 `OnActivate`），这条路径主要服务 Windows 与 Linux。
3. **关闭窗口**：托盘可用时 `OnClose` 拦下，窗口隐藏、应用留在托盘
   （macOS 另按「偏好设置」决定是否连 Dock 图标一起隐藏）；托盘不可用时
   照常退出，否则窗口关掉就再也找不回来。
4. **退出**（托盘菜单、`Cmd+Q`、应用菜单、安装更新前）：`OnBeforeQuit`
   置 `quitting` 并跑 `service.Cleanup()`（删各条目的临时文件，不变量 4）；
   关闭回调看到 `quitting` 就放行，不再拦截。

## 状态转换

- `singleInstance.locked`（依赖内）：一个进程一份；退出时释放，
  崩溃留下的死 socket 由下一次启动接管。
- `quitting`：`OnBeforeQuit` 置 true → 窗口关闭不再被托盘拦下 → 进程结束。
- `trayReady`：托盘创建成功才为 true；false 时关闭窗口 = 退出应用。
- `showWindow`：`WhenReady` 里赋值；此前为 nil。`OnOpenFile` 与
  `secondLaunchHandler` 在 `WhenReady` 之前注册，所以都先判 nil——
  第二次启动可能在窗口建好之前到达。

## 重要约束

- **同一时刻只有一份在运行**（#14）：锁在 `main` 最前面、任何应用级工作
  （建服务、绑 IPC、建窗口）之前拿到；被拒的启动必须 `return`，不能继续，
  否则会跑 `NewAnyPic` 的启动清理去删正在运行的实例要用的目录。
- **被拒的启动要让用户看得见结果**：Windows / Linux 上第二个进程除了
  交参数什么也做不了，用户「又点了一次图标」必须把已有窗口带到前面；
  不注册 `OnSecondInstance` 就是点了没反应（#14 修的就是这个）。
- **把窗口带回前面只有 `showWindow` 一个入口**：文件打开、托盘菜单的
  「打开 AnyPic」、macOS 的 Dock 激活与第二次启动都走它（最小化则还原、
  macOS 把 Dock 图标放回来、显示并聚焦）。在 `WhenReady` 之前注册的回调
  必须先判 nil。
- **退出只能走 `mygo.App.Quit()`**：`Cleanup` 挂在 `OnBeforeQuit` 上，
  绕过它退出就会留下临时文件（不变量 4）。更新器代开安装包后正是走这条路
  （[updater.md](updater.md)）。

## 验证命令

```sh
go test -race -run 'TestIssue14' .   # 单实例守卫、第二次启动把窗口带回前面
go test -race ./...                  # 全量
```

手工核对（打包后）：启动应用 → 关闭窗口（留在托盘）→ 再次运行同一个
可执行文件 → 窗口应回到前面，而不是毫无反应、也不出现第二份。
