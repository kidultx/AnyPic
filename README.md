# AnyPic · 轻一点，依然惊艳

AnyPic 是一款使用 [MyGo](https://github.com/egoist/mygo) 构建的跨平台图片压缩桌面应用。界面取法简洁的桌面工具：中性画布上排布白色细边卡片，灰色药丸式控件、白色滑块的分段开关、发丝级分隔线与克制的靛蓝主色，设置收在侧栏，操作贴近内容。所有图片都只在本机处理。

![AnyPic 界面](docs/screenshot.png)

## 下载

| 平台 | 安装包 |
| --- | --- |
| macOS（Intel / Apple Silicon 通用） | `AnyPic <版本>.dmg` |
| Windows 10/11 | `AnyPic Setup <版本> amd64.exe` |
| Linux | `anypic_<版本>_amd64.deb` 或 `anypic-<版本>-linux-amd64.tar.gz` |

到 [Releases](../../releases) 页面下载对应平台的安装包。安装包由 GitHub Actions 在每次发布 tag 时自动构建（见下文「发布」）。装好之后，「关于 AnyPic → 检查更新」可以在应用内检查新版本并下载安装（macOS 挂载 DMG、Windows 运行安装器、Linux 定位到下载的文件）。

> macOS：安装包未经过 Apple 公证，首次打开如果被 Gatekeeper 拦截，请在「系统设置 → 隐私与安全性」里点「仍要打开」。Linux 运行需要系统 WebKitGTK（`libwebkit2gtk-4.1`），Windows 需要 WebView2（Windows 10/11 通常已预装）。

## 功能

- **批量处理**：拖入文件、整个文件夹，或使用系统选择器一次添加最多 500 张图片。
- **粘贴入队**：在其他应用复制图片后，按 ⌘V / Ctrl+V 直接加入队列；粘贴的图片存到「图片/AnyPic」，与截图同级。
- **快捷键截图**：默认 ⌘⌥S（Windows / Linux 为 Ctrl+Alt+S），在侧栏点击快捷键后可直接录制新的组合键；随时框选屏幕区域，截图自动存到「图片/AnyPic」并加入队列，还可选择是否同时复制到剪贴板。macOS 上通过临时文件取图，不读取剪贴板，因此不会反复弹出粘贴权限提示。
- **预览内美化**：预览界面里有「对比 / 美化」两个页签，美化工作台提供背景（纯色 / 渐变预设与自定义取色）、圆角、细描边、投影、边距与画布比例（1:1 / 4:3 / 16:9，只外扩不裁切）六组旋钮，调整实时反映在大图上，满意后一键应用——生成 `原名-美化.png` 旁存于原图旁，原图不动，随后照常压缩导出。美化完全在本机生成。
- **托盘常驻**：关闭窗口后 AnyPic 留在菜单栏 / 通知区域，截图快捷键继续可用；可在侧栏选择关闭窗口后是否隐藏 Dock 图标（默认隐藏），从菜单栏图标可重新打开窗口或退出。
- **开机启动**：可在侧栏开关「开机时启动 AnyPic」，写入系统的登录项（macOS 登录项 / Linux XDG autostart / Windows 注册表 Run），开关状态与系统实际状态保持同步。
- **智能压缩**：JPG/PNG/WebP 保持原格式；GIF/BMP/TIFF 自动转为更通用的 JPG 或 PNG；透明图片不会被悄悄压成黑底或白底。
- **三种输出**：自动 / JPG / PNG / WebP，质量滑杆覆盖 30–100。
- **按需缩放**：可把长边限制到 3840、2560、1920、1280 或 800 像素，使用 Lanczos 重采样。
- **不牺牲清晰度的诚实处理**：如果重新编码反而更大，会直接使用原图并标注“已是最优”；动态 GIF / WebP 会被明确跳过，而不是只保留第一帧。
- **前后对比**：在应用内拖动分割线查看压缩前后的细节差异。
- **灵活的保存方式**：保存到原图旁边或指定文件夹，支持自定义文件名后缀与批量导出。
- **简洁的桌面界面**：中性底色、白色细边卡片与清晰的表格，支持可隐藏式标题栏、原生菜单、快捷键、拖放、亮/暗主题和窗口状态记忆。
- **隐私优先**：不上传图片、没有账户、没有遥测，图片处理完全在本机完成；应用唯一的联网行为是检查更新与下载安装包（仅在你操作时，只访问 GitHub Releases）。关闭应用时会清理临时文件。

## 技术栈

| 层次 | 实现 |
| --- | --- |
| 桌面框架 | MyGo 0.6.4（Go + 系统 WebView，Typed IPC） |
| 前端 | 原生 TypeScript + Vite，无 UI 框架依赖 |
| 解码 / 缩放 | `disintegration/imaging`、Go 标准库 |
| JPEG / PNG | Go 标准库 |
| WebP | `gen2brain/webp`（纯 Go，内嵌 libwebp） |
| BMP / TIFF | `golang.org/x/image` |

MyGo 负责窗口、菜单、文件对话框、文件拖放与 Go ↔ TypeScript 事件桥接；`compressor.go` 中的 `AnyPic` 服务负责队列、压缩、缩略图、预览与导出。

## 开发

需要 **Go 1.27.3+** 和 **Bun**：

```bash
bun install
bun run dev          # 启动 MyGo 桌面开发环境
```

只预览前端（浏览器中会进入带演示数据的 demo 模式）：

```bash
bun run dev:web
```

## 验证与打包

```bash
bun run typecheck    # TypeScript 类型检查
go vet ./...
go test -race ./...  # 后端单元与集成测试
bun run build        # 生成 build/ 下的应用包与安装包
```

`bun run build` 会自动解锁本地签名钥匙串（`~/Library/Keychains/anypic-signing.keychain-db`，密码 `anypic-local`），并使用其中的自签名证书「AnyPic Local Signing」签名；证书身份固定，因此 macOS 的屏幕录制授权在重新打包后依然有效。在 CI 环境（设置了 `CI`）里构建会自动退回 ad-hoc 签名，不需要任何密钥。

跨平台打包（MyGo 不依赖 cgo，可在 macOS 上交叉构建）：

```bash
bunx mygo build -platform linux/amd64,windows/amd64
```

> Linux 运行需要系统 WebKitGTK，Windows 需要 WebView2（Windows 10/11 通常已预装）；MyGo 本身不依赖 cgo。

## 发布与 CI

仓库带两个 GitHub Actions 工作流：

- **`.github/workflows/ci.yml`**：push 到 `main` 或每个 pull request 时，跑 `tsc`、`go vet`、`go test -race` 并做一次 darwin/amd64 构建冒烟。
- **`.github/workflows/release.yml`**：推送 `v*` tag（或在 Actions 页手动触发）时，在三个矩阵系统上分别构建 `darwin/universal`、`linux/amd64`、`windows/amd64`，上传构建产物为 artifact；tag 构建还会汇总所有产物创建一个 GitHub Release（草稿，自动生成发布说明），确认后发布即可。

发一个新版本的流程：

```bash
# 1. 更新 mygo.config.ts 里的 version
# 2. 提交并打 tag
git tag v0.1.1
git push origin main v0.1.1
# 3. 等 Actions 跑完，在 Releases 里把草稿发布
```

CI 产物为 ad-hoc 签名（无 Apple 证书），与本地自签名构建相比，macOS 用户首次打开需要右键 → 打开，或在系统设置里放行；如需公证分发，给仓库配置 Apple Developer 证书与 `macos.notarize` 即可。

界面按一套扁平的设计 token 组织（界面语言参考 [magpie](https://github.com/yetone/magpie)），并使用 [Hallmark](https://github.com/Nutlope/hallmark) 的 app-scope 规则做过静态审计，检查颜色 token、对比度、焦点与禁用态、溢出和 ARIA。Hallmark 只参与设计检查，不进入构建产物。

## 支持格式与限制

- 可输入：JPG/JPEG、PNG、WebP、静态 GIF、BMP、TIFF/TIF。
- 可输出：JPG、PNG、WebP。
- 单文件上限 250 MB，单批最多 500 张。
- 暂不支持 HEIC、AVIF 与动态图片。
- PNG 输出为无损重编码，质量滑杆不生效；这是 PNG 格式本身的限制。
- 重新编码会移除 EXIF / XMP 等元数据（包括拍摄信息与方向标记），方向会在处理前自动校正。
- macOS 首次使用截图快捷键时，需在「系统设置 → 隐私与安全性 → 屏幕录制」中允许 AnyPic；没有该权限时只能截到桌面壁纸。侧栏的「截图只有壁纸？检查屏幕录制权限」可直接打开该设置。从源码构建时请参考上文「验证与打包」配置固定的签名身份，否则每次重新打包都要重新授权；对外分发时改用 Developer ID 证书并公证。Linux 需要 gnome-screenshot、spectacle 或 grim + slurp + wl-copy 之一。

## 项目结构

```text
main.go            应用入口、窗口、原生菜单、托盘与命令事件
compressor.go      图片队列、压缩管线、预览、导出、设置持久化
screenshot.go      快捷键截图、剪贴板取图与截图归档
decorate.go        截图外观美化：圆角、描边、投影与背景
updater.go         应用内更新：检查新版本、下载并代开安装包
compressor_test.go 后端测试
screenshot_test.go 截图与设置归一化测试
index.html         应用界面结构
src/main.ts        状态管理、渲染与交互
src/style.css      设计系统与响应式布局
src/mygo.ts        MyGo 自动生成的类型安全 IPC 客户端
mygo.config.ts     应用名称、标识符、版本与构建配置
.github/workflows  CI 与发布工作流
docs/              界面截图
```

## 开源致谢

- [MyGo](https://github.com/egoist/mygo) — MIT
- [disintegration/imaging](https://github.com/disintegration/imaging) — MIT
- [gen2brain/webp](https://github.com/gen2brain/webp) — BSD-3-Clause，内含 [libwebp](https://chromium.googlesource.com/webm/libwebp)（BSD-3-Clause）
- [golang.org/x/image](https://pkg.go.dev/golang.org/x/image) — BSD-3-Clause
- [magpie](https://github.com/yetone/magpie) — 界面语言参考（未包含其代码）

AnyPic 的界面与实现为本项目原创，以 [MIT](LICENSE) 许可证发布。
