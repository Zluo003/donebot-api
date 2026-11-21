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

## 可信协调器（2026-11-21）

迁移 274 给执行记录增加不可变的协调实例和运行代次。受信 worker 的 runtime-configuration 响应增加独立 coordinator 字段；其 HMAC 凭据绑定团队、实例和 generation，与成员请求签名及普通 API Key 分离。签名种子只在网关持有，响应使用 no-store。个人实例不获得该字段，团队实例不获得个人安装凭据。

`POST /api/v1/donebot/cloud/coordinators/:instance/executions` 仅接受当前协调器凭据。团队来自实例记录，付款人来自 Owner；发起成员、Bot、会话和受众是可信协调器在读取真实会话后作出的断言，不能从成员请求体直接转发。成员 JWT、个人安装凭据、query 身份及多余 JSON 字段不能替代协调器。幂等键从 Idempotency-Key 取得。响应只有执行 ID、状态、短期 token 和到期时间，不含付款人、个人余额或安装身份。

申请和事务内受理均重新检查 Cloud 的 generation；旧请求键不能在新代次重新绑定。Cloud 节点禁用也立即拒绝协调器和执行授权。Cloud agent 将凭据加密保存在宿主机 metadata，并在启动时原子写入运行容器的 `/run/donebot/team-coordinator.json`（0600），不写入团队卷或容器环境；bot-box 的独立 `/run` 和环境过滤继续适用。

`/tmp/donebot-team-coordinator-accepted-http.log`：真实 HTTP、一次性 PostgreSQL schema 和 race 验证通过，包含八路并发、字段注入、私聊跨成员拒绝、旧代次和禁用节点。`/tmp/donebot-team-coordinator-pg.log`：独立 PostgreSQL 集群的既有团队订阅、节点配置和代理撤权通过。`/tmp/donebot-team-coordinator-accepted-cloud.log`：51 项 Cloud 测试通过，包含真实 HTTP/Unix socket/WebSocket 的合成控制链和配置代次拒绝。

此切片尚未在运行服务接入实际会话到协调器的调用，也未接入供应商派发和团队用量结算。没有新增成员可用的发行授权接口；`cloud_access` 和其他执行能力继续关闭。真实 Linux、生产镜像、实际计费与全平台验收不由上述模拟运行服务测试替代。

## 团队计费身份与模型目录（2026-11-21）

迁移 275 保存团队、Owner 付款账号、DoneBot 分组和专用计费 Key 的不可变绑定。并发首次读取只创建一个身份，不创建个人安装。专用 Key 仅在网关内部持有，普通 API 鉴权、Key 详情、列表、搜索、修改、删除和批量分组迁移均不能取得或改动它。停用或删除后拒绝调用，不自动换 Key、更换付款人或重新绑定分组。

成员通过 `GET /api/v1/donebot/teams/:team/models` 读取授权团队模型；受信运行服务使用 `GET /api/v1/donebot/cloud/coordinators/:instance/models`。目录复用现有启用模型、分组准入与人民币价格计算，团队有独立 catalog_scope_id，不返回 Owner 余额、身份或凭据。移除成员后立即拒绝后续读取。

验证日志：`/tmp/donebot-team-billing-tests.log`（服务并发、身份保护、目录范围及 SQLite 管理过滤），`/tmp/donebot-team-billing-pg.log`（真实 PostgreSQL、八路并发及不可变约束），`/tmp/donebot-team-models-http.log`（真实 HTTP/PostgreSQL、成员与协调器鉴权、撤权和路由继承），`/tmp/donebot-team-billing-compile.log`（全部 Go 包编译）。测试使用独立 schema、合成身份和目录数据，未调用真实供应商。

专用计费身份尚未接入模型派发和实际扣费，运行服务也未消费团队目录。执行、账单隔离、真实 Linux 隔离及平台整体验收继续实施，能力保持关闭。

## 模型请求受理与回执（2026-11-21）

迁移 276 记录执行到内部计费 Key 的不可变派发绑定，禁止修改或删除。协调器申请授权须明确提交 `member_version` 和 `node_version`，过期的成员及设备授权不能获得新令牌。请求哈希固定为 SHA-256(`METHOD + "\n" + RequestURI + "\n" + 原始正文`)，在任何路由、模型别名和思考参数改写前核验。

仅原生 LLM POST 端点接受短期执行令牌：`/v1/messages`、`/v1/responses`、`/v1/chat/completions` 及已登记 Gemini 文本模型的 generateContent/streamGenerateContent。安装、账号、编码凭据、媒体、缓存资源、任意 query 和端点别名不能借此访问。令牌经数据库重新授权后复用标准 API-key 鉴权、余额、模型白名单、原生路由和思考参数校验；内部 Key 不放入 HTTP Authorization 头，也不交给成员。真实供应商处理器之前再事务核验成员、执行端、Cloud 代次和计费 Key，提交受理后最多进入处理器一次。

执行 ID 同时作为稳定的 X-Client-Request-ID 与回执 ID，复用既有 `client:<id>` 计费幂等。已受理、响应丢失或不确定的重放返回 409，不再次派发。HTTP 返回只标记 uncertain，不把传输完成当作供应商结果或结算已完成。协调器通过 `GET /api/v1/donebot/cloud/coordinators/:instance/executions/:execution` 查询原执行和仅该团队/付款人/Key/分组/回执匹配的用量；`billing_state=recorded` 只表示用量已记录。个人 DoneBot Account 用量汇总排除团队计费身份，保留多个个人安装的汇总行为。

该链路还需显式设置 `DONEBOT_TEAM_EXECUTION_ENABLED=1`，默认关闭，不扩大对外声明的 identity 能力。测试日志 `/tmp/donebot-team-model-current-grants.log`、`/tmp/donebot-team-model-finance.log`、`/tmp/donebot-team-model-boundaries.log` 和 `/tmp/donebot-team-model-routes.log` 均通过。验证包括真实 HTTP、一次性 PostgreSQL、真实 Key 鉴权和扣费事务、八路重放仅一次模拟供应商调用、Owner 一次扣费/成员零余额不变、重复扣费命令幂等、重启不重派、错误请求拒绝、撤权和个人用量隔离。`/tmp/donebot-team-model-compile-all.log` 为全部 Go 包编译通过。

模拟供应商处理器手动调用既有扣费仓库，不证明生产供应商协议及其异步用量链完整可用。运行服务尚未把规范会话身份、任务受众和请求重试接到这条链路；媒体、终态确认、跨位置执行与真实 Linux 隔离继续实施。不得启用生产团队执行。

## 稳定操作编号（2026-11-21）

执行请求键接受规范UUID v4或v5；团队、节点和其他资源ID继续仅接受v4。运行服务按不可变会话绑定与原生轮次导出v5，因此本地派发日志丢失不会发行第二笔操作。网关仍匹配原始身份、受众、请求哈希和代次，已受理操作不可再次受理。一次性PostgreSQL和race验证通过，日志 `/tmp/donebot-team-stable-key-race.log`。不改变团队能力默认关闭及生产发布约束。

## 供应商结束与持久结算回执（2026-11-21）

迁移 277 增加不可变的团队模型结算表。结算在既有幂等扣费事务中同时落盘，绑定受理的执行、内部 Key、Owner 付款人及请求指纹；身份、请求编号或受理状态不符时，扣款、限额和幂等占位一同回滚。用量日志清理不删除结算记录。查询的 `billing_state=settled` 表示该事务已提交，`recorded` 仅兼容旧用量记录，`pending` 不表示免费或可以重提。

团队用量任务仅携带执行编号，不携带协调器和执行凭据；队列溢出或停止时使用有界同步回退，不沿用个人用量的可丢弃策略。团队调用拒绝 simple mode、非 CNY、丢失可信来源、订阅计费及无持久仓库的旧扣费路径。该队列仍不是持久 outbox，进程崩溃窗口的待结算恢复尚未实现。

原生 raw Chat Completions 仅在合法响应的所有已观察 choice 都有明确 finish_reason，且流未读错或中断时标记 completed；usage、[DONE]、HTTP 200、错误 JSON 或半截回答不构成结束证明。结束与费用独立，uncertain 也可有 settled 回执，completed 也可能尚待结算。其它供应商协议继续保守返回 uncertain。所有已受理状态都拒绝再次派发。

验证日志 `/tmp/donebot-team-settlement-race.log`：一次性 PostgreSQL、真实 HTTP、生产 ForwardAsChatCompletions、生产 RecordUsage 和真实扣费仓库通过 race 检查。覆盖八路并发仅一次上游、两次记录仅一次扣费及一行真实用量、Owner 付款且成员余额不变、满队列回退、取消后的独立计费 context、不可变回执、清理日志后查询、篡改事务回滚、正常/截断/错误/仅用量流、丢失响应、重启查询及撤权。夹具显式选择合成账号，未覆盖生产处理器的完整调度选路。

`/tmp/donebot-team-settlement-billing-regression.log` 记录用量、原生转发及 worker 相关 service/handler 回归通过；repository 包在该筛选下无匹配测试，真实仓库由上述 PostgreSQL 流程覆盖。`/tmp/donebot-team-settlement-compile.log` 为全部 Go 包仅编译通过。未访问真实供应商、用户数据库或生产网关；媒体、其它协议、持久待结算恢复和真实 Linux 团队执行仍须继续。
