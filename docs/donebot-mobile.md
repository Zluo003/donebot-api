# DoneBot 自有手机中枢

手机仅提供登录、Bot 列表、聊天和消息提醒。购买/开通 Cloud 的接口不在手机协议内。

## 服务组成

- 网关现有账号 JWT 负责手机身份；现有 `sk-donebot-` 安装凭据和 `RuntimeInstallation` 中间件验证电脑/Cloud 所属账号与安装。
- 迁移 `265_donebot_mobile.sql` 保存主机公钥、工作空间投影、手机登录绑定、授权摘要和通知 outbox；`266_donebot_mobile_management.sql` 增加设备状态和节点管理。数据库不保存聊天正文或手机 bearer 明文。
- 桌面运行服务使用 `server/donebot/mobile-host.ts`；Cloud 使用相同运行服务和已经分配的安装凭据，无需迁移会话。必须发布包含该连接器的桌面构建/Cloud runtime 镜像。
- 独立项目 `DoneBot-Relay` 只转发 Noise 加密字节，源码不纳入网关或桌面客户端。网关 access token、模型密钥不会进入中继；部署和升级见 [独立中继节点部署](../deploy/MOBILE_RELAY.md)。

## 部署配置

网关管理后台入口为「DoneBot → 手机连接」。网关签名与 APNs 凭据存于现有 settings 数据库，使用已有持久 `TOTP_ENCRYPTION_KEY` 加密；不回显 seed 或 `.p8` 私钥。部署脚本已生成此主密钥的环境可以直接使用，不需要再增加中继 / APNs 环境变量。主密钥必须保留并随部署备份，不要在升级时替换。未配置持久主密钥时后台显示原因并禁止保存凭据。

1. 发布更新后的网关镜像并应用迁移 265、266，打开「DoneBot → 手机连接」。
2. 在「手机连接与消息推送 → 中继签名」填写 Key ID（首次如 `k1`），选择「由网关生成新密钥」并保存。也可导入已有 32 字节 Ed25519 seed（无填充 base64url）。新密钥仅由网关生成并加密保存，不进入手机或节点。修改使用原有管理员二次验证。
3. 在下方登记 `relay1`，地址填 `relay1.donebot.cn`，设置最大手机会话数。保存一次性显示的节点凭据及验签公钥。节点地址与容量始终由后台管理。
4. 从独立 `DoneBot-Relay` 项目构建节点镜像，在节点服务器配置 `relay.env`，按 [节点部署指南](../deploy/MOBILE_RELAY.md) 启动中继及其 HTTPS 代理。节点回环监听 18080，公网入口为 `wss://relay1.donebot.cn/v1/phone`；网关沿用 8080。保存后台配置不会启动节点服务。
5. 在同页「Apple 消息推送」上传启用 APNs 的 Apple `.p8` 文件，填写 10 位 Key ID / Team ID，勾选启用并保存。私钥输入保存后清空，未上传替换文件时保留已有私钥。
6. 发布桌面、Cloud runtime 和手机 App。同账号登录后手机按网关下发的节点连接，无需原版扫码配对。在手机中开启消息提醒，并进行真机送达验证。

后台保存后当前网关立即采用配置，其他副本最长约 3 秒刷新；无需重启网关。首次保存会继承原有环境凭据，随后数据库记录为准；停用推送不会重新启用环境配置。网关数据库与加密主密钥必须一起备份。独立节点仍有自己的部署配置，不保存网关私钥。

中继签名轮换需要新的 Key ID；保存后复制下方最新验签公钥到所有独立节点的 `RELAY_TOKEN_KEYS` 并重启节点，手机与电脑会重新连接。不要在每次升级时生成新密钥；只调容量或推送时保留签名密钥。

Apple topic 固定为 `cn.donebot.mobile`，订阅自动区分 sandbox / production。开发签名真机可以使用 APNs，无需 App Store 正式发布，但 App ID 必须开启 Push Notifications 并使用含推送 entitlement 的有效签名。后台缺少或停用 APNs 时订阅返回 503 `MOBILE_PUSH_UNAVAILABLE`，手机明确提示后台配置 APNs。后台显示「已启用」表示凭据格式验证通过，不代表已通过 Apple 真实送达测试；错误的 Apple 归属或已撤销密钥仍可能被 Apple 拒绝。

旧环境变量仅作尚未保存后台配置时的兼容入口：

| 名称 | 内容 |
| --- | --- |
| `DONEBOT_MOBILE_RELAY_SEED` | 32 字节 Ed25519 seed，无填充 base64url，只放网关 |
| `DONEBOT_MOBILE_RELAY_KID` | 如 `k1`，兼容 k0 到 k999 |
| `DONEBOT_MOBILE_RELAY_NODES` | 无后台节点记录时的旧地址，节点登记后以数据库为准 |
| `DONEBOT_APNS_KEY_FILE` | 网关容器内 Apple `.p8` 的只读绝对路径 |
| `DONEBOT_APNS_KEY_ID` / `DONEBOT_APNS_TEAM_ID` | Apple Key ID / Team ID |

没有 Apple 凭据仍可验证登录和聊天。Cloud 节点需要允许向网关与中继发出 HTTPS/WSS 连接；不开通手机侧 Cloud 购买接口。

中继在网关暂不可用时仍可启动并监听 18080；未获取或已过期的策略会使 `/healthz` 返回 503、拒绝新接入，进程不会因此退出。网关恢复后中继自动恢复策略。独立部署不意味着移除账号授权、接入策略或会话租约校验。

发布网关并按现有流程应用迁移，再发布桌面/Cloud runtime 和手机 App。本次未执行上述线上操作，也未生成或保存真实签名密钥。不要把示例测试 seed 用于部署。

## 节点选择、容量与设备列表

最多同时启用 4 个节点；后续节点可使用 `relay2.donebot.cn` 等独立域名与对应服务器。网关只下发已启用且 45 秒内上报过的节点。新电脑优先使用尚有会话余量、电脑占用比例较低的节点；已连接的电脑和访问它的手机优先使用该电脑当前节点，其余地址作为候选。桌面一次维持一条中继控制连接，手机按候选地址尝试，属于重连回退，不是正在传输的会话无缝迁移。

节点每 10 秒上报 `hosts/sessions/sockets` 并获取后台策略。管理员只设置最大手机会话数：一个会话是一部手机到一台电脑的加密连接，包含正在建立的连接。每台电脑有 1 条控制 WebSocket，每个已建立会话另有 2 条数据 WebSocket；1 台电脑加 2 个会话通常是 5 条 WebSocket。连接建立、关闭期间统计可暂时不同。电脑和 WebSocket 的保护上限由程序计算，不是管理员额外配置项。容量是接入门槛，不代表机器性能已通过相同并发量压测。

下调上限不主动断开已有会话；停用节点后不再分配新连接。节点超过 45 秒无法获取有效策略时停止新接入，中继不会主动关闭已经建立的传输，但传输仍受正常授权租约约束；停用全部节点或网关持续不可用，会使租约失效并停止远程访问。桌面收到更新后的可用节点列表时会重连，已建立数据通道保留到正常结束或租约失效；不保证会话无缝迁移。扩容需另行部署服务、配置对应入口并在后台登记。重置节点凭据后，需更新该节点 `relay.env` 并重启容器；旧凭据立即不能续报。

用户个人资料页通过 `GET /api/v1/donebot/devices` 分别显示已授权电脑与有效手机登录。手机每 15 秒调用 `PUT /api/v1/donebot/mobile/session` 上报安装 ID、名称和平台，即使中继未配置也可登记。在线表示 45 秒内收到心跳，手机进入后台后可能显示后台或离线；电脑「可连接」还要求中继已连接。电脑授权记录会保留到安装被撤销，不能把它当作原版桌面 JWT 的实时登录列表。手机登录记录逐条校验原版 refresh family，失效或撤销的会话不显示；列表不包含密钥或推送 token。

## 授权和生命周期

手机 `POST /api/v1/donebot/mobile/workspaces/:id/connection` 提交 `{installationId}`。网关验证原版登录会话的 refresh family、当前账号安全版本、工作空间和安装归属，生成会话绑定的 `omb_sess_` 凭据，只把 SHA-256 摘要交给主机。主机在原版 `SessionRegistry` 内登记 client scope，下一次 heartbeat 确认后才把连接描述返回手机。每账号最多 20 个活跃手机绑定。

连接器每 5 秒更新一次，手机会话租约最长 70 秒，主机中继 token 最长 120 秒。账号切换会立即清除本机连接；手机退出先撤销绑定，再退出原版账号。即使离线手机未能成功 DELETE，refresh family 撤销或账号密码变化也会在后续检查中阻止续租和通知。原版设备列表/`donebot devices revoke ID` 可撤销该手机会话；本机保存撤销摘要，旧登录会话不能自动恢复，重新登录的手机获得新凭据。

连接只放行 Bot / 部门 / 群聊列表、消息历史、Bot 与群聊的发送和已读、图片上传及消息范围内的附件下载，随后仍经过原版 session scope 与 Bot/thread 权限检查。主机身份按账号和安装持久化，手机在 Keychain 固定首次 HTTPS 授权拿到的公钥与 host ID；身份变化会拒绝连接，不自动覆盖。首次信任依赖自有账号服务，网关掌握签发密钥，不能把它描述为可抵御恶意中枢授权。

Cloud 列表投影使用现有实例、订阅和归属记录；已开通但尚未运行连接器的实例显示离线。取消续费但仍在已付费期限内的实例可继续显示；未开通、过期、清理锁定、已删除的实例不显示。电脑必须保持 DoneBot 运行，切换查看位置不会迁移任务。

## 消息通知

原版消息落入会话后由已验证安装上报 bot/thread/message ID。网关只信任该所属工作空间的运行服务作为消息来源，不让手机指定事件拥有者。`(workspace,messageId)` 去重，通知只包含“Bot 有新消息”和目标 ID。

outbox 持久化，由网关常驻发送循环每 5 秒领取发送，事件到来或主机 heartbeat 也会唤起发送，跨副本使用行锁和 120 秒领取期限。发送前再次验证当前手机、登录会话与工作空间归属。临时失败最多尝试 5 次、事件最长保留 5 分钟；无效 APNs token 停用，旧账号或旧 session 的队列不会发给后来绑定的账号。APNs 的网络失败存在送达结果未知的情况，不能承诺 exactly-once；消息正文由加密连接读取。

## 隔离验证

```sh
DONEBOT_MOBILE_PG_FIXTURE=1 DONEBOT_MOBILE_DESKTOP_ROOT=/Users/zluo/Dev/DoneBot DONEBOT_MOBILE_RELAY_ROOT=/Users/zluo/Dev/DoneBot-Relay go -C backend test -tags integration ./internal/mobilehub -count=1 -v -timeout 150s
go -C backend test -tags unit ./internal/service -run TestDoneBotMobileSettings -count=1
go -C backend test ./internal/service ./internal/handler ./internal/server/routes -run 'TestDoneBot' -count=1
```

完整联调还需同级桌面、手机及独立中继项目的源码和依赖，并显式传入 `DONEBOT_MOBILE_RELAY_ROOT`。中继自身的安装、测试和镜像构建只需要 `DoneBot-Relay`，在该项目根目录运行 `pnpm check`、`pnpm test`，或 `docker build .`。

使用新建临时 PostgreSQL、合成账号/安装/推送通道和原版桌面隔离 fixture，验证真实中枢 SQL、节点策略、独立节点域名和路径保留、Capacitor Origin、设备分组、原版加密传输、消息往返、通知队列及退出撤销。独立节点域名在夹具内映射到回环代理；真实 Apple 通知、真实 Cloud 节点和公网蜂窝网络尚未验收。
