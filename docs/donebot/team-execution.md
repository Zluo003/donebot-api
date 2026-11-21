# 团队设备与执行授权

团队仍默认关闭，只声明 `donebot.teams.v1` 的 `identity` 能力。本文记录已接入的授权基础，不代表团队模型调用、计费或跨设备执行已可用。

## 设备参与

登录后的账号接口：

| 方法 | 路径 | 行为 |
|---|---|---|
| GET | `/api/v1/donebot/teams/:team/devices` | 当前成员可见的设备、持有人、名称、在线状态与授权版本 |
| PUT | 同上 | 持有人显式提交自己的有效 installation_id、name、enabled |
| DELETE | `/api/v1/donebot/teams/:team/devices/:device` | 持有人撤销自己，Owner 撤销任意，Admin 仅撤销 Member |

启用不等于在线。`POST /v1/donebot/runtime/teams/:team/devices/:device/heartbeat` 必须经过普通 API-key 鉴权和 RuntimeInstallation 验证，正文只有 version。成员及安装身份来自已验证的安装凭据，不能由正文、查询或转发头指定；此端点不要求成员个人余额，撤销和禁用凭据仍拒绝。

心跳有效期为 30 秒。改名或同一授权重放保留心跳；禁用、撤销、重新启用、成员版本变化会隔离旧授权，重新启用须取得新版本并重新报告。离线设备保留，不接收新执行。响应不包含安装凭据或 installation_id。

## 执行记录

迁移 273 新增不可变的执行归属及短期授权哈希。PrepareExecution 由可信协调器调用，调用前必须验证真实会话和受众；目前没有向客户端开放发行或受理接口。调用不得直接信任客户端提交的 Bot、会话、私聊或付款字段。

服务端锁定团队、付款人、实际成员、执行设备、安装、Cloud 订阅和节点状态。记录固定团队、发起成员及版本、执行成员及版本、设备及授权版本、用途、Bot、会话、受众、结构化请求哈希和幂等键。付款人只能由团队 Owner 记录导出。私聊仅本人本机或所属团队 Cloud。即使本机执行，团队 Cloud 也必须就绪。

短期令牌五分钟有效，只存哈希。AdmitExecution 在事务内将 prepared 改为 accepted；并发重放仅一次返回 first=true。调用者必须先提交受理，再派发供应商。响应丢失、uncertain 或已终结任务不得重新派发。成员撤权、角色变化、设备撤销及重开、安装撤销、Cloud 到期或离线均拒绝旧授权。后续结果查询、实际计费和任务终态适配仍待接入，不能把该记录当作已完成结算。

## 验证（2026-11-21）

`/tmp/donebot-team-execution-final.log`：一次性 PostgreSQL 17、真实 HTTP 生命周期、Go race 检查通过，覆盖两团队、多个角色、私聊受众、身份字段注入、设备授权、八路并发受理、不可变归属、撤权和不确定状态。HTTP 测试使用合成认证身份和真实 RuntimeInstallation 中间件；生产路由另有继承鉴权测试。真实 API-key 中间件验证个人零余额仅放行心跳、撤销仍拒绝。

`/tmp/donebot-team-execution-cloud-migration.log`：独立启动的 PostgreSQL 集群应用 273 后，既有个人/团队购买、代理撤权和节点配置测试通过；需要真实运行镜像的两个测试明确跳过。`/tmp/donebot-team-execution-compile-all.log`：全部 Go 包编译通过，不等同运行全部测试。

未使用真实数据库、账号、供应商或生产网关。团队执行、实际扣费、Linux 恶意隔离与最终平台验收仍需继续。回退关闭能力并使用兼容版本，保留新增表和归属记录，不降级数据库删除团队数据。
