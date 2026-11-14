# DoneBot 媒体运行契约

此文档描述网关源码的生产接口；本地测试不证明 `https://donebot.cn` 已部署对应提交。
目录 schema 仍为 v3，`docs/donebot/v3/` 的旧静态夹具不能作为可售模型列表。

## 凭据与提交

客户端使用登录 access token 领取安装凭据，使用安装凭据请求模型与 `/v1/assets`。
目录来自 `GET /api/v1/donebot/models`；登录 token 加 `X-DoneBot-Installation`。
所有媒体生成必须在调用前持久化 operation ID、scope、参数指纹，并发送同一 `Idempotency-Key`。
同键同参最多提交一次；处理中、结果不确定或没有可回放音频时返回 409，不重新向供应商提交。
同键异参返回 409。安装密钥轮换保留底层安装身份与 API Key 记录。

`X-DoneBot-Receipt-ID` 在返回媒体响应头时即可取得。响应未送达时可重放同键同参获取相同
receipt 或明确的 409；不得换 operation key、模型或账号隐式重试。网关的 DoneBot 图片路径也禁止
供应商失败后的账号切换与同账号重发。旧的非 DoneBot API Key 保留既有行为。

## 图片异步适配器

目录对使用原生同步 Images API 的图片模型发布固定适配器 `donebot.images.async.v1`：

| 操作 | 路径 |
| --- | --- |
| 生成 | `POST /v1/images/generations/async` |
| 编辑 | `POST /v1/images/edits/async` |
| 查询 | `GET /v1/images/tasks/{task_id}` |

提交与编辑接受原有 Images 请求格式。202 包含 `id`、`task_id`、`object`、`status`、
`created_at`、`expires_at`、`poll_url`、`receipt_id`。查询的 `status` 为 `processing`、
`completed`、`failed` 或 `uncertain`；完成结果在 `result.data`，便捷 URL 为 `image_url`。
任务 ID 由服务端 receipt UUID 去掉连字符后加 `imgtask_` 前缀生成，在付费上游请求前写入
PostgreSQL 操作记录，因此丢失提交响应时也能从 receipt 找回任务 ID。

任务状态、幂等绑定与已完成结果在 Redis 中保存，结果文件先存入网关对象存储，再保存任务结果。
依赖异步包装的图片模型在任务存储或结果存储关闭时不进入客户端目录。已创建任务仍可查询。
APIMart 原生异步图片继续使用 `apimart.images.v1`，不经过这个同步包装。

同步图片上游没有可查询的供应商任务。网关进程在付费 POST 后、结果写入前退出时，无法安全
重建丢失的结果。超过执行期限的任务查询返回 `uncertain`；不能假装失败后自动重提，也不宣称
恰好一次或执行中任务可以跨进程继续。已完成结果和幂等记录可在服务重启后读取，前提是 Redis
及结果存储保持持久化。晚到的有效完成结果仍可覆盖执行期限的查询投影。

DoneBot 的 GPT Image 系列禁止 mask 编辑，包括账号模型映射后的别名；普通无 mask 编辑保留。
其它模型是否支持 mask 由客户端本地官方能力登记及相应原生适配器决定。

## 视频与音乐

Seedance 继续使用 `seedance.videos.v1`，生成、编辑、续写分别为 `/v1/videos`、
`/v1/videos/edits`、`/v1/videos/extensions`。允许路径不代表每个型号支持每种操作；原生
解析器继续检查具体模型、内容、时长、比例及参考素材。Suno 保持 `apimart.music.v1`。
固定适配器不允许客户端执行目录里任意 URL 或 JSON 路径。

## 流式语音

`POST /v1/audio/speech` 返回原生二进制音频，逐块读取上游并立即 flush，工作缓冲 32 KiB。
不等待整段音频生成完成，不将音频编码成 JSON。JSON/HTTP 上游错误保持错误响应，不当作音频。
客户端下载有背压；断开后网关限时排空已接受的供应商请求，最多十分钟，以便完整输出可以结算。
空输出、上游中断及结算失败不会标记音频成功，不重发付费 POST。

响应头在第一块音频前包含 `X-DoneBot-Receipt-ID`。完整读取上游并持久计费成功后才标记操作
`completed`；未完成为 `uncertain`。客户端必须在本地音频 EOF 后查询
`GET /api/v1/donebot/receipts/{receipt_id}`，使用登录 access token 与
`X-DoneBot-Installation`，读取标准 envelope 的 `data.status`。`dispatching` 可短暂重查，
`uncertain` 保留待核实；仅 HTTP 200 或 EOF 不能证明音频已完整结算。费用仍来自 `usage_logs`。
HTTP trailers `X-DoneBot-Stream-Status` 与 `X-DoneBot-Stream-Error` 提供额外信号；标准
browser/Node fetch 可能不暴露 trailers，不能依赖它们。

最多 2 MiB 的完整音频可幂等回放。更大音频或客户端断开后的输出不缓存为可回放结果，
已结算 receipt 仍是 `completed`，同 key 重提返回 409。部分音频不会被缓存为成功回放。
供应商没有可恢复的 TTS 任务；进程强制退出可能留下不确定结果及未结算成本，需要运营核对，
不能靠换 key 重发来补偿。

## 参考文件

DoneBot 客户端将所有本地图片、视频、音频参考文件通过 `POST /v1/assets` 上传，multipart
只含一个 `file`。成功返回 `id`、`sha256`、`mime`、`size`、`url`、`created_at`、
`expires_at`、`deduplicated`。缓存按账号/安装 scope 与文件 SHA-256 隔离。
每次复用前请求 `GET /v1/assets/{id}` 确认所有权及存在性，并更新签名 URL；404 表示失效，
重新上传，不能保留过期 URL。当前过期资源也返回 404，不能假设一定返回 410。
这一客户端策略不删除网关原生 `asset://` 扩展或上游已有素材输入能力。

## 部署条件

保留 PostgreSQL `donebot_media_operations` 与现有 usage 账本。开启 Redis 持久化并持久挂载数据卷；
结果对象存储或本地素材目录也需持久化。反向代理不得缓存或缓冲 `/v1/audio/speech`，且超时应
覆盖合法的语音输出时长。发布后需验证目录中的异步 adapter、202/轮询、第一块音频到达、receipt
终态及当前部署 SHA。此轮本地验证不读取生产凭据、不请求付费模型。

## 预扣、退款与并发

版本 0.4.9 应用迁移 `254_donebot_media_prepaid.sql`。图片、视频、音乐在付费供应商 POST
之前，在同一 PostgreSQL 事务内锁定用户、检查余额和媒体并发、永久扣减钱包、写入原扣费
流水及价格快照。余额不足时不得调用供应商；多进程并发也不能使这些异步任务透支钱包。
原 Redis 钱包临时预留不与这笔永久预扣叠加。

预扣为本次请求的保守上限：图片按可产生尺寸档的最高价与输出数量计算；Seedream 图层分解
最多 17 张；Seedance 按型号最大输出时长（2.0 为 15 秒、2.5 为 30 秒）加经过校验的参考视频
总时长计算；Suno 按普通模式 1 单位、Max 模式 2 单位。价格、倍率、可信参考时长均在准入时
冻结，后续改价不会改变已经接受的任务。成功后按实际输出数量、尺寸和时长结算，退回差额；
若结果超过已登记上限，保留预扣等待核对，不追加透支扣费。

`usage_logs` 保留不可覆盖的原扣费 `dbm:charge:{receipt_uuid}`。明确失败、取消、过期时，
单独追加 `dbm:refund:{receipt_uuid}`，`event_type=refund`，金额为负，
`related_request_id` 指向原扣费，两条记录的 `operation_receipt_id` 相同。成功时的差额
退款使用同一退款事件类型。唯一约束与事务防止并发查询、工作进程恢复重复退款。网络中断、
408、499、5xx 或供应商结果不明均不能自动退款或重发付费 POST。

媒体 receipt 新增 `payment_state`（`prepaid`、`settled`、`refunded`）、
`prepaid_cents`、`refunded_cents`。预扣记录本身不代表任务成功，`prepaid` 时
`actual_cents` 为 null；全部退款时 `status=refunded`、`actual_cents=0`。金额为 CNY 分，
可以有小数。已获得实测用量后先持久化结算输入再执行钱包/退款事务；进程重启可继续结算。
原生异步任务的供应商 ID 在返回后立即绑定 PostgreSQL，工作进程可重建 Redis 查询队列。
同步 GPT Image 在付费执行后、实测结果首次持久化前退出仍无法恢复供应商结果；30 分钟后
未结算收据显示 `uncertain`，保留预扣，不能假装失败或重提。实测计费已持久化、
结果文件尚未保存时退出，也可能出现已经结算但无法恢复结果的窄窗；收据的
`completed` 只确认结算，客户端仍须取得任务结果后才能展示生成成功。

`GET /api/v1/donebot/account` 的 `concurrency` 返回网关用户账户实际总并发。媒体任务在
所有安装之间共享 `max(1,concurrency)` 上限，PostgreSQL 准入事务为最终裁决。
仅未结束的生成任务占媒体名额；`retrieving`、`settling`、`refunding`、终态或已有持久化
结算输入的任务不占生成名额。没有终态证据的 `uncertain` 仍保留名额，不能按超时退款
或重发收费请求。客户端同时统计自身活动文本、语音与媒体生成，不再预留一半额度。
媒体任务排队及派发时间持久化，相邻付费提交至少间隔 1 秒。网关原有总并发守卫统计活动 HTTP 请求，异步 POST 返回后的后台任务另由上述媒体
上限限制；跨多个客户端的“HTTP 加后台任务”总数尚非单一原子守卫。

任务查询在原生响应的状态所在对象中增补 `generation_status=completed|failed`，表示
生成已确定结束。该字段允许客户端在资源保存、下载、结算或退款仍待完成时持久释放
本地生成名额；`status=processing` 仍须继续查询，不能据此宣称已交付或已结清。
准入返回 `429/DONEBOT_MEDIA_CONCURRENCY` 时，客户端使用原幂等键、原参数与同一
receipt 持久排队并退避重试。网关仅允许未预扣、无 dispatch 的本地并发拒绝重新准入；
已付费、上游 429、未知结果及已创建调度记录均不能借此重发。

媒体结算使用 DoneBot 分组中对应模型在准入时冻结的价格、倍率和实际原生用量。
Seedream 的尺寸按 APIMart 像素面积规则核算，不在预扣结算时套用通用长边档位。
已持久化结果可从输出尺寸重建原生档位；真实超出输出上限或价格快照仍保留核对，并在
日志中记录具体档位、数量或金额。已完成生成的结算及明确失败的退款不依赖原上游账号
继续可调度；被软删除的 key/账号仍保留原财务归属，停用其配额计数不阻止钱包结算。
这条财务恢复路径不恢复被删除 key 的鉴权或新生成能力。

自动恢复复用现有持久化调度器，执行租约 90 秒、每 20 秒续租；失联工作进程不能继续
用旧租约覆盖新进程的结果。过期 `dispatching` 和 `uncertain` 若有可查询的上游任务 ID，
只恢复该 ID 的状态查询；若有完整持久化结果或已存资源，则继续下载保存、结算。
自动恢复不重发结果未知的付费 POST。没有任务 ID 或完整结果的 `uncertain` 保留待核对，
不反复空跑；不能仅按任务年龄删除账本、退款或宣告失败。明确失败继续幂等退款，
可结算结果继续幂等收尾，财务异常保留原预扣与具体诊断，不占已结束生成的名额。

只读诊断使用 `psql -v user_id=2 -f docs/donebot/media-concurrency-diagnostic.sql`，
对照未结资金与实际生成占用。不要通过清空预扣记录来释放并发。

部署必须备份并持久保留 `donebot_media_precharges`、`donebot_media_operations` 与
`usage_logs`。已有金融记录时迁移 254 的 down 脚本拒绝删除；回滚应保留数据结构、先停止
新媒体提交并核对未结算任务，再回滚应用二进制。不得删除原扣费、退款或未决资金记录。
