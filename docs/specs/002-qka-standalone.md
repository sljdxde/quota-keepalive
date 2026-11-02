---
id: qka-spec-002
title: QKA 独立桌面应用规格
labels: [accepted]
status: accepted
created: 2026-10-30
supersedes: qka-spec-001
---

## Product Boundary

QKA 是独立运行的桌面额度签到与保活工具，不依赖 Codex、WorkBuddy、Magpie 或其他 Agent 启动业务。MCP 仍可作为高级兼容入口保留，但不是自动执行、账号管理或用户旅程的必要条件。

主窗口固定五个页签：

1. 应用管理：供应商、账号、登录、启停、额度与历史。
2. 签到管理：按供应商折叠账号，分别执行签到、保活和刷新额度。
3. 日志：按定时任务、签到、保活分类查看脱敏记录。
4. Token 速度：读取本机 Agent 会话，展示速率、工具与动态称号。
5. 设置：外观、背景、毛玻璃、开机启动、通知、自动执行和软件更新。

设置必须是最后一个页签，不提供独立 Agent 接入页。

## Provider And Account Lifecycle

- 供应商运行时复用 Magpie/OpenCode provider plugin 机制，通过 Bun 子进程执行。
- 应用管理可添加多个供应商，每个供应商可有多个账号。
- 登录方式以供应商插件声明的 Magpie 能力为准，包括官方浏览器登录和 API Key。
- 登录完成后必须通过只读身份或额度验证才创建受管来源；取消、超时或验证失败不得留下账号。
- 新账号创建后默认启用。
- 每个账号独立保存启用状态、签到开关和保活开关；停用账号不进入计划、不读凭据、不访问供应商网络。
- 删除账号只清理 QKA 数据库映射、来源记录和受管凭据，不影响供应商自身账号与本地登录文件。
- 签到成功后同时记录可观察的本次领取额度和当前余额；任一值未知时保持未知，不显示为 0。

## Execution

- GUI 单账号执行、GUI 全量执行、MCP `checkin_run` 与 `task-run` 必须复用同一业务服务与状态机。
- 同一配置目录的手动执行与定时执行通过跨进程锁互斥。
- WorkBuddy CN 的日边界继续使用 `Asia/Shanghai`。
- `signed_confirmed`、`already`、`manual_required`、`ineligible`、`inactive` 等当日终态不得再次领取。
- `manual_required` 的同日后续执行只允许只读签到状态复查。
- 一个账号失败不得中止同一批次中的其他账号。
- 签到与保活是独立操作，任一成功不得掩盖另一项失败。

## Built-In Auto Run

- 无界面入口为 `QKA task-run`，支持 `--json`、`--scheduled` 和 `--config-dir`。
- `schedule.json` 保存 `enabled`、`interval_minutes`、`action`、`timezone` 与脱敏 `last_run`。
- 默认间隔 30 分钟，允许 5 分钟至 7 天；动作允许 `all`、`checkin`、`keepalive`。
- 第一版只使用本机时区，UI 必须明确显示“本机时间”。
- macOS 使用当前用户 LaunchAgent；Windows 使用当前用户 Task Scheduler。
- 系统任务只启动 `task-run`，不得在 plist、任务 XML 或命令参数中写入账号、token、uid、请求头或原始响应。
- Windows 任务启用 `StartWhenAvailable`、`WakeToRun`、最低权限和多实例忽略策略。
- 自动执行为关闭时，`task-run` 必须零网络、零业务写入直接成功退出。
- 关闭自动执行必须删除对应系统任务；不同配置目录使用不同任务 ID。

## Update

- 更新检查读取固定官方 `feed.json`，按 `GOOS/GOARCH` 选择制品。
- 下载后必须校验 SHA-256；镜像只能替换主机前缀，不能改变版本、路径或哈希。
- macOS 安装版随应用退出替换 `.app`，失败时恢复旧版。
- Windows 安装包必须发布 ZIP；更新器解压到临时目录后整目录替换，不能把 ZIP 写入 EXE。
- Windows 替换范围包括 GUI、CLI、适配器、供应商插件和 Bun 运行时；新进程启动失败时恢复旧目录。
- 更新 UI 必须显示当前版本、最新版本、制品名、下载/安装结果与失败原因。

## Privacy And Security

- QKA 不收集密码、验证码或二维码内容。
- 官方登录页面只能由供应商声明的 HTTPS 域名或授权深链打开，QKA 不注入输入、不读取 DOM、不记录剪贴板。
- 日志、UI、JSON、构建产物与诊断输出不得包含 token、完整身份、完整手机号、请求头或原始上游响应。
- 自动化测试只允许 `127.0.0.1` 受控假目标。
- 真实账号、真实登录文件和真实供应商网络验收必须单独取得用户确认。

## Packaging

- macOS 发布 `QKA-<version>-<arch>.dmg`，应用名与 Dock 名称显示为 `Quota Keepalive`。
- Windows 发布 `QKA-<version>-windows-<arch>.zip`，包含 `QKA.exe`、`QKA-cli.exe`、`adapters/`、`provider-plugins/`、`bun.exe` 和说明文件。
- `scripts/build-release-feed.sh` 扫描已构建制品，生成 `feed.json` 与 `SHA256SUMS`。
- macOS 可使用 ad-hoc 签名；不得宣称已完成 Apple Developer ID 公证。
- Windows 交叉编译与结构测试不等于 Windows 真机安装验收。

## Acceptance

代码级验收要求：

- `go test ./...` 全部通过。
- 所有含语句包达到 `100.0%` statements。
- `go vet ./...`、`git diff --check` 通过。
- Windows GUI、CLI 与调度包可交叉编译。
- 前端模块通过语法检查。

安装级验收要求：

- 本地安装后五页签可打开，菜单栏面板可收起，打开主窗口时面板隐藏。
- 深浅色、毛玻璃、背景切换、窗口拖动和 Token 速度正常。
- 无真实账号时，受控假目标覆盖登录成功、验证失败、刷新失败、签到成功、签到兜底、只读复查、同日幂等、停用跳过、删除隔离与额度展示。
- 自动执行关闭时零网络；启用时可由系统调度启动同一 `RunAll`。
- 更新检查、下载、哈希校验和安装交接可验证。

真实账号验收和 Windows 真机验收必须分别记录证据，不能由受控测试或交叉编译替代。
