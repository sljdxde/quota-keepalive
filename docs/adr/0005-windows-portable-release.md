---
status: accepted
---

# Windows 采用双可执行文件与便携 ZIP，不伪装成安装器

Windows 版沿用同一套 Go 业务代码，但桌面 GUI 使用 `-H windowsgui`，双击后直接进入托盘；控制台和系统任务入口保留为独立的 `QKA-cli.exe`。两者都通过 `go-winres` 写入图标、版本、DPI 与 `asInvoker` 清单，结构对齐 Magpie 的 Windows 构建方式。

发布产物是按架构区分的便携 ZIP，包含 `QKA.exe`、`QKA-cli.exe`、`adapters/`、固定 `Bun` 版本解出的 `bun.exe` 和简短说明。Bun 从官方 `@oven/bun-windows-*` npm 包获取，并按包内完整性值校验；不把 macOS 的 Bun 错放进 Windows 包。ZIP 生成 SHA-256 校验文件。

## Considered Options

- 只发布单个 GUI `.exe`：双击体验简单，但 `task-run`、`doctor` 等控制台工作流无法可靠输出，也会漏掉 Bun 和适配器。
- 立刻制作 MSI/NSIS 安装器：更像正式产品，但当前没有 Windows 真机验收、签名和卸载验证，容易形成“看起来可安装、实际未验收”的假完成。
- 与 Magpie 完全共用发布仓库：可以复用签名和分发流水线，但 QKA 当前没有对应的发布仓库和密钥边界。

## Consequences

`make release-windows` 可以在 macOS 或 CI 上交叉编译并生成 amd64、arm64 便携包，但只能证明代码可编译、包结构正确。真实 Windows 安装、WebView2、托盘、任务计划程序和睡眠补跑仍必须在 Windows 机器上单独验收。卸载时删除用户选择的便携目录即可，不需要管理员权限。
