# QKA WorkBuddy CN 单账号闭环验收

日期：2026-10-29
分支：`codex/workbuddy-mvp-integration`
协议基线：`@magpie-community/opencode-workbuddy-auth@0.1.47`

## 结论

- 受控假目标上的单账号闭环已通过自动化验收：登录、只读验证、默认停用、启用、签到、同日幂等、停用隔离、删除清理和重新登录恢复。
- 安装态默认入口固定为 `https://copilot.tencent.com`；`127.0.0.1` 假目标只作为测试覆盖注入。
- 尚未使用真实 WorkBuddy CN 账号、真实登录文件或腾讯网络执行验收；真实站点行为仍属未知项。
- 安装版 DMG 的构建、签名、版本与 SHA-256 核对记录在本轮构建后补入。

## 受控测试通过

执行命令：

```sh
go test ./internal/app -run TestWorkBuddyControlledAccountAcceptance -count=1
go test ./internal/app -run 'TestWorkBuddy(ManagedRefreshFailureIsolatesAccountAndReloginRecovers|ManualFallbackReviewFindsClaimedToday|CheckinSameDayDoesNotClaimAgain|Checkin401WithWorkingBalanceEntersOwnAppFallback)' -count=1
```

覆盖事实：

| 场景 | 结果 |
|---|---|
| 受控浏览器登录 | 只打开回环假目标；成功后先进入待只读验证 |
| 只读验证 | 仅请求账号与余额接口；身份一致后创建脱敏账号与健康受管来源 |
| 首次账号状态 | 默认停用；启用前要求健康来源 |
| 签到 | 有到账回执时记为 `signed_confirmed` |
| 同日重复执行 | 第二次不发送新的领取请求 |
| 应用内兜底 | 领取 401 且余额可用时进入 `manual_required`，不宣称自动成功 |
| 只读复查 | 用户在官方 App 完成后，复查观察到已领取并更新为 `already`，不再领取 |
| 刷新失败 | 仅当前来源变为 `reauth_required`；重新登录恢复同一账号，不产生重复账号 |
| 停用 | 账号保留来源与凭据，但不进入状态视图和执行计划 |
| 删除 | QKA 账号、来源和受管凭据被清理；同一上游身份可再次登录并恢复映射 |
| 脱敏 | 登录状态、账号列表、签到结果和日志均不含 access/refresh token、完整 uid、手机号或请求头 |

自动化实现：`internal/app/workbuddy_acceptance_test.go`，配套单点测试位于
`internal/app/workbuddy_checkin_test.go`、`internal/app/workbuddy_refresh_test.go`。

## 安装态入口

- `internal/cli/appbuild.go` 的生产配置固定 WorkBuddy CN 基址为官方 API，并仅允许官方主机。
- `internal/app/gui.go` 在未注入覆盖时使用 `workbuddy.DefaultBaseURL`。
- `internal/cli/appbuild_test.go` 与 `internal/app/gui_test.go` 断言安装态不是假目标地址。
- 受控测试通过 `BaseURLs` 注入 `127.0.0.1`，不改变生产默认值。

## 真实账号观察到的事实

无。本轮未获得真实账号测试的单独确认，未执行余额查询、签到状态查询或领取接口。

## 未知项

- 真实腾讯 WorkBuddy CN 登录页的浏览器跳转、轮询时序和账号身份字段是否与协议基线完全一致。
- 真实账号的签到活动状态、401 兜底条件、到账回执字段和跨日行为。
- 真实 Keychain 授权弹窗在安装签名包上的具体表现。
- 真实账号删除后，厂商侧账号是否仍可正常使用（QKA 只验证了不修改目标应用数据的设计边界）。

## 发布前剩余动作

1. 构建新 DMG，核对 `QKA.app` 的 bundle 版本、可执行文件架构、ad-hoc 签名与 DMG SHA-256。
2. 经用户单独确认后，在真实账号上执行一次登录、只读验证、启用和签到尝试；若进入兜底，再由用户在官方 App 完成后复查。
3. 对集成分支运行完整代码审查与 `go test ./...`、`go vet ./...`，确认无回归后按用户指令发布。
