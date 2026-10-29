# WorkBuddy CN 浏览器登录协议基线

基线：`@magpie-community/opencode-workbuddy-auth@0.1.47`。

- 官方 API：`https://copilot.tencent.com`
- 平台参数：`platform=workbuddy`
- 请求侧版本：`User-Agent: WorkBuddy/5.9.3`
- 登录页参数：`version=2.0.0`
- 轮询间隔：1 秒
- 登录总时限：5 分钟

## 流程

1. `POST /v2/plugin/auth/state?platform=workbuddy` 获取 `state` 与 `authUrl`。
2. 只接受 HTTPS `authUrl`，追加 `version=2.0.0` 与随机 `loginSessionId` 后打开系统浏览器。
3. `GET /v2/plugin/auth/token?state=...` 轮询 token；业务码 `11217`、HTTP `408`、HTTP `429` 表示继续等待。
4. token 必须包含 `accessToken` 与 `refreshToken`。
5. `GET /v2/plugin/login/account?state=...` 轮询身份；业务码 `12151`、HTTP `408`、HTTP `429` 表示继续等待。
6. 身份必须包含 `uid`；随后只进入 `pending_verification`，不创建账号或来源。

## 只读验证

浏览器授权拿到 token 后，QKA 只调用以下两个接口验证登录态：

1. `GET /v2/plugin/accounts`，携带 `Authorization: Bearer` 与 `X-Domain`。
2. `POST /billing/meter/get-user-resource-summary`，请求体为 `{}`，携带同一 `X-Domain`、`X-User-Id`、`X-Product: SaaS`、`X-IDE-Type: WorkBuddy`；企业身份另带 `X-Enterprise-Id` 与 `X-Tenant-Id`。

`token.domain` 是请求域的权威来源；缺失时回退官方 API host，并统一规范化后用于两个请求。`/v2/plugin/accounts` 不返回 domain，因此不得假设存在可用于比对的 domain 字段。

验证通过要求两个请求均为 2xx、业务码为 0，且 accounts 中存在唯一匹配本地 `uid` 的账号；本地 `enterpriseId` 存在时还必须匹配。通过后才写入 `managed` 来源、默认停用的账号和受管 token bundle，并删除待验证凭据。

- 401/403：清理待验证凭据，状态为 `reauth_required`，不创建账号或来源。
- 网络失败、408、429、5xx 或暂时无法解析：保留待验证凭据，状态为 `retryable`；重试复用原凭据，不重新打开登录页。
- 身份缺失或不一致：不创建账号或来源，状态为 `failed`。
- 验证阶段绝不调用 `/v2/billing/meter/checkin-activity-status` 或 `/v2/billing/meter/daily-checkin`。

QKA 在受控测试中允许 `127.0.0.1` 的 HTTP 假目标；安装态使用腾讯官方 HTTPS API。动态 `authUrl` 的主机不在本地硬编码，界面只显示去掉查询参数的官方 origin。

完整 token、refresh token、uid、手机号和企业身份只写入 QKA 的待验证或受管凭据存储（Keychain 优先，0600 文件回退），不进入数据库、日志、错误、MCP 或 UI。升级上游插件协议前必须先补受控假目标对照测试并更新本基线。
