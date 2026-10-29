---
status: accepted
---

# WorkBuddy 适配器复用 Magpie 协议而不依赖 Magpie 运行时

WorkBuddy 的浏览器登录、token 刷新、请求头和签到语义已经有 Magpie 及其社区插件实现，但 qka 的适配器运行在 Bun/JSON-RPC 宿主中，而 Magpie 插件依赖 OpenCode/Magpie 自己的插件宿主。为避免两套运行时生命周期、认证存储和更新节奏互相耦合，我们决定在 qka 的 WorkBuddy 适配器内按 Magpie 当前行为实现同一协议，不把 Magpie 进程或插件包作为运行时依赖。协议基线以安装时核验的 Magpie WorkBuddy 插件版本为准，升级上游协议时必须先补充对照证据和受控假目标测试。

## Considered Options

- 直接加载 Magpie 插件：能减少协议复制，但需要模拟其宿主接口，且 qka 与 Magpie 的登录、凭据、错误和更新边界会纠缠在一起。
- 读取 Magpie 已保存的登录态：接入较快，但会让 qka 的账号生命周期依赖另一个应用，并可能与 Magpie 争抢刷新责任。
- 在 qka 适配器内对齐协议：需要维护协议差异，但运行时边界、故障定位和发布节奏最清楚。

## Consequences

qka 必须为 WorkBuddy 浏览器登录、刷新和签到保留自己的受控测试与协议变更记录；上游 Magpie 的变化不会自动生效。作为交换，qka 不要求用户安装或保持 Magpie 运行，也不会读取 Magpie 的私有认证文件。
