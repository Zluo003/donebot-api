# DoneBot 自有手机中枢

代码已实现，尚未部署。手机仅提供登录、Bot 列表、聊天和消息提醒。购买/开通 Cloud 的接口不在手机协议内。

## 服务组成

- 网关现有账号 JWT 负责手机身份；现有 `sk-donebot-` 安装凭据和 `RuntimeInstallation` 中间件验证电脑/Cloud 所属账号与安装。
- 迁移 `265_donebot_mobile.sql` 保存主机公钥、工作空间投影、手机登录绑定、授权摘要和通知 outbox。数据库不保存聊天正文或手机 bearer 明文。
- 桌面运行服务使用 `server/donebot/mobile-host.ts`；Cloud 使用相同运行服务和已经分配的安装凭据，无需迁移会话。必须发布包含该连接器的桌面构建/Cloud runtime 镜像。
- 自建 `DoneBot/relay/` 只转发 Noise 加密字节。网关 access token、模型密钥不会进入中继。

## 部署配置

网关环境变量：

| 名称 | 内容 |
| --- | --- |
| `DONEBOT_MOBILE_RELAY_SEED` | 独立生成的 32 字节 Ed25519 seed，无填充 base64url；只放网关 |
| `DONEBOT_MOBILE_RELAY_KID` | 如 `k1`，允许 k0 到 k999 |
| `DONEBOT_MOBILE_RELAY_NODES` | 1–4 个自有域名，逗号分隔，例如 `relay1.donebot.cn` |
| `DONEBOT_APNS_KEY_FILE` | 仓库外 Apple APNs `.p8` 的只读绝对路径 |
| `DONEBOT_APNS_KEY_ID` | Apple Key ID |
| `DONEBOT_APNS_TEAM_ID` | Apple Team ID |

Apple topic 固定为 `cn.donebot.mobile`，通知订阅区分 sandbox 和 production。APNs 配置缺失时订阅返回不可用，客户端不会显示已开启提醒。中继未配置时手机连接接口返回不可用。

在自己的 VPS 为 `relay1.donebot.cn` 配置 DNS 和 TLS，构建 `DoneBot/relay/Dockerfile`，使用现有 `relay/deploy/compose.yaml` 和 `Caddyfile`。`RELAY_DOMAIN=relay1.donebot.cn`；`relay.env` 中设置 `RELAY_NODE=relay1` 及 `RELAY_TOKEN_KEYS={"k1":"该 seed 对应的32字节 Ed25519 公钥 base64url"}`。中继只持有公钥，不能签发主机授权。TLS 对外开放 443，转发端口只绑定回环地址。Cloud 节点需要允许向网关和自有中继发起 HTTPS/WSS 出站连接，现有 public-web egress 可承载；未启用出站的实例不能通过手机连接。

发布网关并按现有流程应用迁移，再发布桌面/Cloud runtime 和手机 App。本次未执行上述线上操作，也未生成或保存真实签名密钥。不要把示例测试 seed 用于部署。

## 授权和生命周期

手机 `POST /api/v1/donebot/mobile/workspaces/:id/connection` 提交 `{installationId}`。网关验证原版登录会话的 refresh family、当前账号安全版本、工作空间和安装归属，生成会话绑定的 `omb_sess_` 凭据，只把 SHA-256 摘要交给主机。主机在原版 `SessionRegistry` 内登记 client scope，下一次 heartbeat 确认后才把连接描述返回手机。每账号最多 20 个活跃手机绑定。

连接器每 5 秒更新一次，手机会话租约最长 70 秒，主机中继 token 最长 120 秒。账号切换会立即清除本机连接；手机退出先撤销绑定，再退出原版账号。即使离线手机未能成功 DELETE，refresh family 撤销或账号密码变化也会在后续检查中阻止续租和通知。原版设备列表/`donebot devices revoke ID` 可撤销该手机会话；本机保存撤销摘要，旧登录会话不能自动恢复，重新登录的手机获得新凭据。

连接只放行 Bot 列表、消息历史、文本发送和已读四类操作，随后仍经过原版 session scope 与 Bot/thread 权限检查。主机身份按账号和安装持久化，手机在 Keychain 固定首次 HTTPS 授权拿到的公钥与 host ID；身份变化会拒绝连接，不自动覆盖。首次信任依赖自有账号服务，网关掌握签发密钥，不能把它描述为可抵御恶意中枢授权。

Cloud 列表投影使用现有实例、订阅和归属记录；已开通但尚未运行连接器的实例显示离线。取消续费但仍在已付费期限内的实例可继续显示；未开通、过期、清理锁定、已删除的实例不显示。电脑必须保持 DoneBot 运行，切换查看位置不会迁移任务。

## 消息通知

原版消息落入会话后由已验证安装上报 bot/thread/message ID。网关只信任该所属工作空间的运行服务作为消息来源，不让手机指定事件拥有者。`(workspace,messageId)` 去重，通知只包含“Bot 有新消息”和目标 ID。

outbox 持久化，由网关常驻发送循环每 5 秒领取发送，事件到来或主机 heartbeat 也会唤起发送，跨副本使用行锁和 120 秒领取期限。发送前再次验证当前手机、登录会话与工作空间归属。临时失败最多尝试 5 次、事件最长保留 5 分钟；无效 APNs token 停用，旧账号或旧 session 的队列不会发给后来绑定的账号。APNs 的网络失败存在送达结果未知的情况，不能承诺 exactly-once；消息正文由加密连接读取。

## 隔离验证

```sh
DONEBOT_MOBILE_PG_FIXTURE=1 DONEBOT_MOBILE_DESKTOP_ROOT=/Users/zluo/Dev/DoneBot go -C backend test -tags integration ./internal/mobilehub -count=1 -v -timeout 150s
go -C backend test ./internal/service ./internal/handler ./internal/server/routes -run 'TestDoneBot' -count=1
```

使用新建临时 PostgreSQL、合成账号/安装/推送通道和原版桌面隔离 fixture，验证真实中枢 SQL、原版加密中继、手机传输、消息往返、通知队列及退出撤销。真实 Apple 通知、真实 Cloud 节点和公网蜂窝网络尚未验收。
