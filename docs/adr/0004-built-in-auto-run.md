---
status: accepted
---

# QKA 内置周期自动执行，系统调度只负责启动

早期设计把“何时调用”完全交给宿主 Agent，导致只安装 QKA、未配置 Agent 时不会自动签到或保活。现在 QKA 自己提供无窗口的 `task-run` 入口：macOS 使用当前用户的 LaunchAgent `StartInterval`，Windows 使用当前用户的 Task Scheduler 重复触发器。该决定取代旧文档中“QKA 不持有任何业务定时器”的绝对边界，但不改变固定 8 个 MCP 工具，也不把调度能力暴露成 MCP 工具。

业务执行不复制：GUI 手动执行、MCP `checkin_run` 和 `task-run` 最终都进入同一套 `App.RunAll`、状态机与北京日幂等规则。`task-run` 只决定是否启动对应动作，并使用配置目录级跨进程锁避免托盘进程与系统任务同时领取。`schedule.json` 保存启用状态、5 分钟至 7 天的执行间隔、动作（签到、保活或全部）、本机时区与脱敏的最近结果；系统任务通过 `--config-dir` 明确指向同一配置目录，避免多个实例互相覆盖。

## Considered Options

- 继续只依赖宿主调度：边界最简单，但不满足“打开 QKA 即可自动执行”的产品承诺。
- 在 GUI 内跑 JavaScript 定时器：实现快，但窗口关闭、WebView 休眠和重启后都不可靠。
- 系统调度直接复制签到逻辑：会形成两条业务链，幂等和证据必然漂移。

## Consequences

macOS 的 LaunchAgent 使用 `RunAtLoad`，Windows 任务启用 `StartWhenAvailable` 与 `WakeToRun`；两者只承担“何时启动”，不包含 token、uid、账号或请求头。QKA 关闭主窗口后仍可由系统任务启动，若托盘进程未运行也能执行。已进入当日终态的账号不会重复领取，`manual_required` 只做只读复查。

Windows 代码可以交叉编译并通过结构与 XML 测试，但真实 Task Scheduler、睡眠补跑、WebView2 和托盘仍必须在 Windows 安装版上单独验收，不能仅凭交叉编译宣称 Windows 已完成真机验收。
