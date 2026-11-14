# DoneBot 独立中继节点部署

中继转发服务属于独立项目 `DoneBot-Relay`。本仓库只保留账号授权、节点登记、容量策略和状态管理，不包含节点转发服务源码，也不启动节点进程。

网关更新和中继升级是两套独立流程。后台显示节点记录、勾选“启用”，或完成网关在线升级，都不代表节点服务器上的中继已经运行。

## 组件与端口

| 组件 | 部署位置 | 入口 |
| --- | --- | --- |
| donebot-api | 网关服务器 | HTTPS `donebot.cn:443`，网关内部端口沿用 8080 |
| DoneBot-Relay | 独立节点服务器，可有多个 | 回环 `127.0.0.1:18080` |
| 节点 HTTPS 代理 | 与该中继同一台服务器 | `relay1.donebot.cn:443` → `127.0.0.1:18080` |

独立表示构建、部署和生命周期独立。账号授权及节点策略仍通过 HTTPS 使用网关；中继不需要网关二进制、数据库或 Redis，且不持有签名私钥、账号 JWT 或模型密钥。

## 1. 配置网关签名

在网关配置中设置：

```dotenv
DONEBOT_MOBILE_RELAY_SEED=独立生成并持久保存的32字节Ed25519_seed的无填充base64url值
DONEBOT_MOBILE_RELAY_KID=k1
```

可在管理员终端用 `python3 -c 'import secrets; print(secrets.token_urlsafe(32))'` 生成一次 seed，安全保存到网关环境配置。不要把真实值提交到仓库、复制到节点或在每次升级时重新生成。网关多副本必须使用相同的签名配置。

二进制部署写入 `/etc/sub2api/environment` 并重启网关服务。Compose 部署写入网关的 `.env`，随后重建应用容器使环境变量生效。已有配置须保留数据库、JWT 和其他凭据。该步骤仅更新网关，不启动中继。

后台“网关尚未配置中继签名密钥”的提示必须消失后，才能取得对应的中继验签公钥。

## 2. 登记独立节点

打开「DoneBot → 手机中继」：

- 节点编号：`relay1`，后续节点可以使用 `relay2` 等。
- 连接地址：`relay1.donebot.cn`，不填写 `wss://`、路径或端口。
- 设置最大手机会话数，勾选启用并保存。
- 保存一次性显示的 `RELAY_NODE`、`RELAY_NODE_TOKEN`、`RELAY_TOKEN_KEYS`。签名配置稍后才完成时，可重新展开“中继验签公钥”复制，不必重置节点凭据。

节点密钥文件保存在节点服务器，权限 0600；seed 只在网关。重置节点凭据会使旧凭据立即失效，需要更新节点配置并重启中继。

## 3. 构建和配置节点

从独立 `DoneBot-Relay` 项目获取源码，在它的根目录执行：

```sh
docker build --tag donebot-relay:local .
cd deploy
cp .env.example .env
cp relay.env.example relay.env
chmod 600 relay.env
```

填入 `relay.env` 的三个节点配置值，再检查节点部署目录 `.env` 中的镜像和节点域名：

```dotenv
RELAY_IMAGE=donebot-relay:local
RELAY_DOMAIN=relay1.donebot.cn
```

节点 Compose 只有中继与可选 Caddy。不要把中继代码复制到本仓库或网关容器；不要修改网关 Compose 来启动节点。

## 4. 配置节点 HTTPS 并启动

将 `relay1.donebot.cn` 的 DNS 指向节点服务器，允许服务器出站访问 `https://donebot.cn`，客户端访问节点公网 443。中继内部端口 18080 无需对公网开放。

如果已经给该节点域名配置了 Nginx/HTTPS，运行 `docker compose up -d relay` 只启动中继，将节点项目的 `deploy/nginx.location.conf` 合入该节点域名的 HTTPS server。该配置保留路径并转发 WebSocket 到 `127.0.0.1:18080`；校验后重载现有代理。

如果没有现有代理，确认节点服务器 80/443 空闲且允许公网访问，在节点的 `deploy/` 目录运行：

```sh
docker compose config --quiet
docker compose up -d
```

Caddy 为节点域名提供 TLS，转发到内部 18080。客户端连接 `wss://relay1.donebot.cn/v1/phone`；桌面连接同一节点的 `/v1/host` 和 `/v1/host/data`。后续节点各自在自己的服务器部署，再在网关登记对应域名。客户端通过网关下发的节点信息选择，无需用户手工指定。

## 5. 检查与排障

在节点部署目录执行：

```sh
docker compose ps
docker compose logs --tail 50 relay
curl -i http://127.0.0.1:18080/healthz
```

| 现象 | 含义和处理 |
| --- | --- |
| 没有监听 18080 | 先检查节点是否单独部署和启动，再看配置错误或端口占用；升级网关不会创建监听 |
| 返回 503 | 进程已监听，但尚未取得有效策略；检查签名、节点是否启用、节点凭据及出站 HTTPS |
| 返回 200 | 节点已取得有效策略，后台应在下一次心跳后显示在线 |
| 本机正常，公网失败 | 检查 DNS、TLS、443、防火墙、WebSocket Upgrade 和路径转发 |

节点每 10 秒上报状态。网关不可用时，中继仍可启动和保持监听；首次策略未获取或超过 45 秒未刷新时拒绝新接入，并在网关恢复后自动恢复策略。已有传输仍受授权租约约束，不能承诺网关停机后持续聊天。

## 6. 独立升级

在节点项目构建并审核新镜像，把节点部署目录 `.env` 的 `RELAY_IMAGE` 更新为新版本，再运行 `docker compose up -d relay`。保留节点的 `relay.env`，不要重新生成身份。已有 TLS 代理和网关不需要一起升级。

节点重启会通知客户端重连，不保证正在传输的会话无缝续接。`docker compose stop relay` 只停止中继。网关在线升级、节点登记和策略开关始终不控制节点进程生命周期。

登录、Cloud 归属、手机通知及隔离联调的协议说明见 [手机中枢说明](../docs/donebot-mobile.md)。
