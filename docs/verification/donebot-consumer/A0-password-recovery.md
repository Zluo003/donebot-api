# A0：网关验证码找回验收

基线 `7716a67`（功能基线 `8cffc42` / 0.4.5）。本阶段给既有认证端点增加客户端验证码协议，不改价格、余额、2FA 设置或安装 Key 策略。生产部署由用户执行。

## 改动

- `auth_handler.go`：`delivery:"code"`、`email_code`、旧 token 二选一校验，原网页链接响应保持兼容。
- `password_reset_code.go`：验证码签发、密码指纹绑定的 HMAC、独立用途、队列、邮件、消费及密码完成逻辑。
- `password_reset_code_cache.go`：独立 Redis 空间及 Lua 原子重发、计数、一次消费与代次清理。
- `user_password_reset.go`：只更新密码的条件写入，保留其他并发账户更改。
- 邮件队列和通知模板：新增独立找回验证码任务及中英文模板。
- 2FA 登录：挑战绑定实际验证的密码版本；改密前挑战不能签发新会话。
- 审计：验证码字段脱敏，移除凭据前缀调试输出。

## 自动化证据

全部请求和存储使用测试桩、内存 SQLite、miniredis 或仅监听 loopback 的 SMTP；不对用户正在使用的应用或生产账户发起改密操作。

| 验收项 | 证据 |
|---|---|
| 六位、15 分钟、60 秒、第五次正确、第五次错误锁定 | `TestPasswordResetCodeCache*`、`TestPasswordRecoveryCodeRequestsDoNotEnumerateAccounts` |
| 原子并发、重发失效、代次隔离、TTL 不延长 | `TestPasswordResetCodeCacheConcurrent*`、`*ResendAndGenerationFences`、`*Attempts*` |
| 注册/2FA 不可混用、摘要无明文 | `*Purpose*`、`TestPasswordRecoveryRegistrationCodeCannotResetPassword`、HMAC 绑定测试 |
| 未知/停用邮箱统一；旧 token 错误不枚举 | `*RequestsDoNotEnumerateAccounts`、`*LegacyInvalidTokenDoesNotEnumerateAccountState` |
| 邮件、队列、Redis、数据库故障 | `*CacheFailure*`、`*MailFailure*`、`*VerificationInfrastructureFailures`、`*DatabaseFailure*` |
| 旧链接协议、消息式成功响应、参数拒绝 | `TestPasswordRecoveryHandler*`、`TestConsumePasswordResetToken*` |
| 会话失效、密码外字段保留、跨流程并发写保护 | `*SuccessRevokesSessionsAndPreservesAccount`、`*LegacyTokenSnapshotPrecedesConsume`、`TestUserPasswordReset*` |
| 成功的密码 2FA、OAuth 绑定 2FA；旧挑战作废 | `*PasswordLoginTwoFactorRegression`、`TestLogin2FACompletesPendingOAuthBindAndConsumesSession`、`*InvalidatesPendingTwoFactorLogin` |
| 审计不保留验证码或密码 | `TestRedactAuditBodyPasswordRecoveryCodes` |

最终目标验收命令及完整输出：`logs/a0-auth-acceptance.log`，退出 0，169 个顶层测试 / 207 项（含子项）。四个包分别通过：service 10.471s、repository 1.451s、handler 3.777s、routes 9.757s。

四包完整 unit 命令 `go test -p 1 -tags=unit ./internal/service ./internal/repository ./internal/handler ./internal/server/routes -count=1` 退出 0，完整输出见 `logs/a0-auth-full-unit.log`。结果：service 240.630s、repository 10.069s、handler 52.541s、routes 11.751s。

`logs/a0-service-handler-initial.log` 保留初次诊断失败：新增测试桩在成功登录路径缺少既有逻辑依赖的 entClient，并非产品错误。已改用现有 SQLite 夹具验证成功密码登录和 2FA；最终目标验收通过。`logs/a0-auth-before-enumeration-review.log` 是审查修复前的中间通过记录，不作为最终验收。

补充验收：网关前端生产构建通过（含 Vue 类型检查、3 项 i18n 检查），嵌入网页的服务构建和 `--version` 启动通过；发布矩阵 10 项测试通过。完整结果分别见 `logs/a0-frontend-build.log`、`logs/a0-embedded-build.log`、`logs/release-matrix.log`。发布版本为 0.4.6，生产部署由用户执行。

## 限制与回退

- 邮件仍为原有内存队列，重启可能丢失待发送任务；60 秒后可重发。邮件或 Redis 读取失败不能保证送达，记录最多保留到原 15 分钟期限。
- HMAC 复用既有 JWT 密钥并使用独立用途；更换 JWT 密钥会使待完成验证码失效。
- 升级前已经签发、缺少凭据版本的待完成 2FA 挑战要求重新登录；不更改用户的 2FA 配置。
- 消费验证码后数据库写入失败必须重新取码。
- 当前 staging 公开设置关闭邮箱验证与忘记密码，因此没有冒充真实 staging 邮件改密已通过。公开只读记录另见 `logs/staging-public-baseline.json`。
- 旧网页链接兼容；无需数据库迁移。使用 sh 安装器指定版本回退，保留 Redis、数据库、客户端数据及钥匙串。
