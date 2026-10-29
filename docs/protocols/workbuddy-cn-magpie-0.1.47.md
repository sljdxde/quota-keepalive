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

## 受管来源刷新

受管来源的 access token 距离到期不足 60 秒时，在业务请求前刷新；业务请求收到 401 时也走同一条刷新路径，并且只重试原请求一次。

1. `POST /v2/plugin/auth/token/refresh`，请求体为 `{}`。
2. 携带 `X-Refresh-Token`、`X-Auth-Refresh-Source: plugin`、`X-Domain`；企业身份另带 `X-Enterprise-Id` 与 `X-Tenant-Id`。
3. 刷新成功必须返回非空 `accessToken`；返回的 `refreshToken`、到期时间、`domain` 和 `tokenType` 非空时覆盖旧值，否则保留旧值。
4. 新 token bundle 只写 QKA 自己的 Keychain/受保护文件凭据存储，不写 WorkBuddy App 的登录文件，也不回写目标应用。
5. 同一 refresh token 的并发刷新必须去重；刷新结果安全持久化后才能用于业务请求。
6. HTTP 401/403、业务码 `12153`、`invalid_grant`、`token format error`、refresh token 已过期或不可刷新、刷新后请求再次 401，或新 token 无法安全持久化时，来源健康状态变为 `reauth_required`；账号和其他账号不受影响。
7. 网络失败、HTTP 408/429/5xx 或暂时无法解析刷新响应属于可重试失败，不改变来源健康状态。临期刷新失败时不得继续使用旧 access token 发业务请求；业务请求收到 401 后的刷新可重试失败也不得误标为需要重新登录。
8. 用户重新完成官方浏览器登录并通过只读验证后，同一账号与同一来源恢复 `healthy`，不得创建重复账号。

刷新成功或业务请求成功都不能单独证明登录态永久有效；下一次请求仍需按到期时间和 401 规则重新判断。

QKA 在受控测试中允许 `127.0.0.1` 的 HTTP 假目标；安装态使用腾讯官方 HTTPS API。动态 `authUrl` 的主机不在本地硬编码，界面只显示去掉查询参数的官方 origin。

完整 token、refresh token、uid、手机号和企业身份只写入 QKA 的待验证或受管凭据存储（Keychain 优先，0600 文件回退），不进入数据库、日志、错误、MCP 或 UI。升级上游插件协议前必须先补受控假目标对照测试并更新本基线。

## 每日签到

受管来源的签到使用与 Magpie WorkBuddy 插件一致的“先查状态、再领取”协议。两个请求都使用 `POST`，请求体为 `{}`，并携带受管来源的 `Authorization: Bearer`、`X-Domain`、`X-User-Id`、`X-Product: SaaS`、`X-IDE-Type: WorkBuddy` 和企业身份头（存在时）。

1. `POST /v2/billing/meter/checkin-activity-status`
   - 读取 `active`、`today_checked_in`、`streak_days`、`daily_credit`、`today_credit`。
   - `active=false` 时不得调用领取接口，返回活动未开始或已结束。
   - `today_checked_in=true` 时不得调用领取接口，返回今日已领取。
2. 仅当 `active=true && today_checked_in=false` 时调用 `POST /v2/billing/meter/daily-checkin`。
   - 成功数据读取 `credit`、`streak_days`、`is_streak_day`、`credit_delayed`、`message`。
   - `1001` 映射为已领取（`done`/`already`）。
   - `1002` 映射为无资格（`ineligible`）。
   - `1003` 映射为活动结束（`inactive`）。
   - 其他业务码或非 2xx 结果进入可重试失败；401 进入下方“应用内签到兜底”判定。

到账证据规则：

- 只有 `claimed` 或 `done` 且存在非零 `credit` 或明确上游消息时，才可按证据确认到账。
- `credit_delayed=true` 表示奖励尚未到账，即使 `credit` 非零或 `message` 非空，也只能记录为“已领取，待确认”。
- 领取成功但没有任何明确回执时，只能记录为“已领取，待确认”，不得写成“已到账”。
- 同一账号同一北京日进入 `claimed`、`done`、`ineligible`、`inactive` 或 `manual_required` 后，后续手动或宿主调用不再发起领取请求；单个账号失败不得中止其他账号。

## 应用内签到兜底与复查

WorkBuddy 5.6 及以后版本的自动签到可能缺少目标应用安全 SDK 生成的设备令牌。签到状态或领取接口返回 401 时，QKA 不得直接宣称签到成功，也不得仅凭该接口的 401 判定整个登录态失效。

1. 签到接口最终返回 401 后，使用同一受管凭据执行只读 `POST /billing/meter/get-user-resource-summary`。
2. 余额查询为 2xx：记录 `manual_required`，提示“自动签到被 WorkBuddy 拒绝；请回 WorkBuddy App 签到”，并抑制同一天再次发送领取请求。
3. 余额查询也返回 401/403 或刷新凭据被拒绝：来源标记 `reauth_required`，执行结果为可重登后重试。
4. 余额查询遇到网络失败、408、429、5xx 或响应不可读：执行结果保持 `failed_retryable`，不得误判为兜底或需重登。
5. 已处于 `manual_required` 时，同日再次执行 `checkin_run` 只调用 `POST /v2/billing/meter/checkin-activity-status` 复查，绝不调用领取接口。
6. 复查到 `today_checked_in=true` 时，将当日状态更新为 `already`，并在结果与日志中保留“只读复查 + HTTP 状态”的观察证据；复查仍为未领取时保留兜底提示。活动已结束时如实提示无需再次领取，不要求用户回 App 签到。
7. 复查本身 401 时同样执行余额只读判定：余额 2xx 时继续保留兜底；余额也 401 时标记需重登。

手动执行与宿主 `checkin_run` 使用同一状态机、同一北京日幂等键和同一受管凭据。结果与日志只包含脱敏账号、目标应用、原因和下一步，不包含 token、uid、完整手机号或原始响应。
