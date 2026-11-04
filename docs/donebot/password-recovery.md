# 客户端邮件验证码找回密码

DoneBot 客户端使用已有认证端点的验证码扩展。网关原网页链接找回协议仍可使用；本项不改变价格、余额、2FA 配置或安装 Key 策略。

管理员须在现有设置中启用邮箱验证和“忘记密码”，并配置可用邮件服务。关闭时，客户端应隐藏或禁用入口并显示关闭原因。验证码模式不依赖 `frontend_url`；旧网页链接模式仍需要该地址。

## 请求协议

`POST /api/v1/auth/forgot-password`

```json
{"email":"person@example.com","delivery":"code"}
```

需要验证码防护时，携带现有 CAPTCHA proof 字段。成功的 `data` 包含统一 `message`、`countdown:60`、`expires_in:900`。未注册和不可用邮箱返回相同提示，不能据响应判断账号是否存在。

`POST /api/v1/auth/reset-password`

```json
{"email":"person@example.com","email_code":"123456","new_password":"输入的新密码"}
```

`email_code` 与旧 `token` 必须且只能提供一个。新密码仍遵循网关已有密码校验。成功只返回 message，客户端清除输入并回到登录，不自动签发新登录会话。

未传 `delivery` 的 forgot-password 仍发送旧网页链接；旧 reset-password 的 `{email,token,new_password}` 保持兼容。

## 有效期与故障处理

- 六位随机验证码，15 分钟有效，60 秒重发间隔，最多五次校验。第五次正确输入可以成功；五次错误后须重新获取。
- 重发即使邮件尚未送达，也会使旧码失效。验证码独立于注册和 2FA 用途，缓存只存带密钥摘要。
- 并发提交最多一次改密成功；账号密码已被其他请求修改时，旧凭据不能覆盖新密码。
- 邮件发送失败或队列已满时不泄漏邮箱是否存在，用户可在倒计时后重发。邮件队列为现有内存队列，网关重启可能丢失尚未发送邮件。
- Redis 不可用时关闭操作；验证码消费后数据库写入失败，须重新获取验证码，不重复使用旧码。
- 改密后旧 access/refresh 会话和旧密码产生的待完成 2FA 登录挑战失效。用户重新登录，原 2FA 仍保留。
- 升级前尚未完成且不含密码版本的 2FA 挑战需要重新登录。轮换现有 JWT 密钥会使未使用的找回验证码失效，需重新获取。
- 客户端只短暂持有密码和验证码；不写配置、全局状态、日志或子进程环境。

## 升级与回退

使用现有 `deploy/install.sh` 安装对应 release。完整发布包含 `sub2api_<version>_linux_amd64.tar.gz`、`sub2api_<version>_linux_arm64.tar.gz` 和 `checksums.txt`；simple release 不提供 sh 安装器需要的完整产物。

本扩展不新增数据库列或资金迁移。需要回退时用原安装器的指定版本功能回装上一稳定版；验证码客户端在旧网关上须显示不支持或关闭状态，网页链接流程不受影响。不要清空 Redis、数据库或客户端钥匙串来回退。
