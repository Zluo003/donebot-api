# Muse 视频 API 对接

依据 [Token API Muse 视频文档](https://token.secure-skill.com/docs#muse-video) 接入。
本文描述本仓库实现，线上是否可用以部署版本和分组配置为准。

## 账号与价格配置

1. 在管理后台进入 **账号管理 → 添加视频账号 → Muse** 标签。面板自动带入上游地址 `https://token.secure-skill.com` 和模型映射 `muse-video` → `muse-video`；填写账号名称和上游 Muse 分组的 API Key，按需修改地址或下游别名。地址也可包含 `/v1`。
2. 将账号绑定到调用方分组，并允许对应公开模型。面板自动保存 Muse 端点能力；已有 Muse 专用账号也使用该面板编辑，留空密钥会保留原值。视频服务在账号创建后不可切换，已有任务继续绑定原账号。Muse 不支持 OAuth 账号。
3. DoneBot 管理页同步视频模型，配置 Muse 的 **720p 每秒售价**并启用。Muse 只支持 720p，提交时长限定 **4–30 秒（含边界）**。
4. 请求显式提交 `resolution: "720p"`。计价优先使用 `720p` 秒价，也支持已有平坦视频秒价；未配置价格不会当作免费，不回退到其他分辨率或 Seedance 价格。显式价格 `0` 表示免费。
5. 应用迁移 `267_muse_video_surcharge.sql`，保留现有 PostgreSQL、Redis 持久化与后台媒体工作进程。该迁移允许追加补扣流水；应用启动时沿用项目迁移机制。

分组原有 `model_pricing` 支持如下卡片；数字仅为演示，币种与倍率遵循现有分组计费链路。`per_request_price` 在 `billing_mode=video` 时表示**每秒**价格。

```json
{
  "models": ["muse-video"],
  "billing_mode": "video",
  "per_request_price": 0.2,
  "intervals": [
    {"tier_label": "720p", "per_request_price": 0.2}
  ]
}
```

分组价格优先于渠道价格。管理页保存使用已有 `video_prices`，Muse 只接受
`resolution: "720p"`、`reference_video: false` 的价格项。平坦的 `per_request_price`
可用于已有配置的 720p 视频秒价；显式 `720p` 档优先。其他分辨率即使配了价格也不能调用。
客户端本地登记 `duration` 最小 4、最大 30、步长 1、默认 6 秒，`resolution` 只有 `720p`；
网关校验原生请求，不向客户端下发媒体能力登记。

## 接口

所有接口使用 `Authorization: Bearer <网关密钥>`，任务查询和下载使用创建时同一用户、API Key、分组，网关始终绑定原上游账号。客户端不要保存或提交网关内部的 `muse:` 前缀。

| 操作 | 方法与路径 | 说明 |
| --- | --- | --- |
| 创建 | `POST /v1/videos` | `application/json`，异步任务 |
| 创建别名 | `POST /v1/videos/generations` | 与创建接口相同 |
| 查询 | `GET /v1/videos/{task_id}` | 使用创建响应中的 `task_id` 或 `id` |
| 下载 | `GET /v1/videos/{task_id}/content` | 完成且结算后返回视频二进制 |

Muse 不支持视频编辑、续写、删除、任务列表或流式生成。不要把上游 `/v1/videos/tasks/{id}` 查询别名当作网关契约。

### 创建参数

网关保留原生 JSON 字段，仅按路由配置改写 `model`；不替客户端选择模型或拼装语义请求。分辨率必须显式提交 `720p`；建议同时显式提交时长和比例，省略时长或比例时由上游应用默认值。

| 字段 | 类型 | 约定 |
| --- | --- | --- |
| `model` | string | 必填，分组允许的公开模型名，例如 `muse-video` |
| `prompt` | string | 必填，非空文本 |
| `duration` | integer 或 string | **4–30 整数秒**，例如 `6`、`"6"`、`"6s"`；省略时按上游默认 6 秒预估 |
| `seconds` / `video_length` | integer 或 string | 时长别名，读取优先级为 `duration` → `seconds` → `video_length`；建议只用一个 |
| `aspect_ratio` | string | 上游支持 `16:9` / `9:16`；别名 `aspectRatio`，再回退 `size`。不支持的值由上游忽略 |
| `resolution` | string | **必填，仅 `720p`**；不支持 480p、1080p、4K |
| `reference_images` | array | 最多 10 张，去重后计数；推荐公网图片 URL |
| `extra` | string | 上游追加到提示词；也参与网关内容审核 |
| `timeout` | integer | 可选，上游等待参数，正整数；客户端仍按异步任务轮询 |
| `stream` | boolean | 可省略或 `false`；`true` 被拒绝 |

参考图别名为 `images`、`image_urls`、`image_url`、`image`。可使用 URL 字符串、图片 data URL、原始 base64，以及含 `url`、`b64_json` 或 `image_url.url` 的对象。网关不会将参考图重新编码后覆盖原始请求。Muse 不接受参考视频或参考音频；不要传 `reference_videos`、`video_url`、`reference_audios` 等字段。

```bash
# GATEWAY_KEY 使用网关密钥；不要在客户端放置供应商密钥。
export GATEWAY_BASE_URL='https://donebot.cn'
# 调用前设置 GATEWAY_KEY；安装密钥还须持久化本次逻辑操作的 OPERATION_ID。
curl "$GATEWAY_BASE_URL/v1/videos" \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $OPERATION_ID" \
  -d '{
    "model": "muse-video",
    "prompt": "海浪轻轻拍打沙滩，镜头缓慢向前移动",
    "duration": 6,
    "aspect_ratio": "16:9",
    "resolution": "720p",
    "reference_images": ["https://example.com/reference.png"]
  }'
```

示例参考 URL 是占位符，联调时替换为真实可访问的图片。

### 创建响应与轮询

普通 API Key 沿用原生响应，HTTP `200` 表示任务受理，例如：

```json
{"id":"task_example","task_id":"task_example","object":"video.task","model":"muse-video","status":"queued","progress":0,"video_url":null,"url":null,"duration":"6s","seconds":"6"}
```

DoneBot 安装密钥 `sk-donebot-*` 使用现有持久化派发，付费 POST 前先预扣并保存任务，HTTP `202` 返回网关任务 ID，例如：

```json
{"id":"dbtask_example","task_id":"dbtask_example","status":"processing","receipt_id":"00000000-0000-4000-8000-000000000001"}
```

客户端应保存完整响应及 `X-DoneBot-Receipt-ID`，始终轮询响应中的同一 ID。上例为持久化派发的受理响应；任务 ID 和 receipt UUID 以实际返回为准。安装凭据必须发送并持久化 `Idempotency-Key`；同键同参重放复用既有受理记录，同键异参返回 `409`。普通 API Key 不提供安装凭据级的持久化幂等保证。

```bash
curl "$GATEWAY_BASE_URL/v1/videos/$TASK_ID" \
  -H "Authorization: Bearer $GATEWAY_KEY"

curl "$GATEWAY_BASE_URL/v1/videos/$TASK_ID/content" \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  --output muse-result.mp4
```

| `status` | 客户端处理 |
| --- | --- |
| `queued` / `in_progress` / `processing` | 继续查询，建议遵守 `Retry-After` 并退避 |
| `completed` | 读取 `video_url`，其次 `url`、`output[0].url`；可请求 content 下载 |
| `failed` | 读取错误并核对收据；明确失败按现有机制退款 |
| `uncertain` | 保留原任务与幂等键，等待核对，禁止重提付费请求 |

上游 `completed` 但缺少产物 URL 时，网关仍返回 `in_progress`。安装任务在测时、保存或结算阶段会返回 `status=processing`、`generation_status=completed`；客户端可释放生成名额，但必须继续查询，直到交付与结算完成。

下载未就绪时返回 `409`、`Retry-After: 3`。普通任务在计费不可用、测时失败时返回 `503`，不暴露未结算结果；继续查询原任务，不要重新创建。未知或越权任务返回 `404`。余额为零不阻止已受理任务的查询；未完成补扣仍须充值后才能交付。

## 计费与对账

**Muse 按实际生成的视频时长计费，预扣多退少补。** 这里的实际时长来自网关读取产物 MP4/MOV 的视频轨道，支持小数秒，不将响应中可能回显请求值的 `duration` / `seconds` 当作计费证据，也不按音轨时长收费。

DoneBot 安装任务在调用供应商前永久预扣 `提交时长 × 秒价 × 适用倍率`，冻结本次价格与倍率。成功后以实测秒数结算；不把秒数向上取整计费，金额沿用账本精度。省略时长的预扣按上游默认 6 秒。普通 API Key 使用原有临时余额预留与一次最终实际用量扣费，不产生安装收据的永久预扣/调整流水。

假设秒价 `0.2`、倍率 `1`、提交 `6` 秒：

| 实测时长 | 预扣 | 最终费用 | 调整 |
| --- | --- | --- | --- |
| 4.25 秒 | 1.20 | 0.85 | 退回 0.35 |
| 6 秒 | 1.20 | 1.20 | 无调整 |
| 8.5 秒 | 1.20 | 1.70 | 补扣 0.50 |

安装任务保留原 `dbm:charge:{receipt}` 流水。退款追加 `dbm:refund:{receipt}`，`event_type=refund`、金额为负；补扣追加 `dbm:surcharge:{receipt}`，`event_type=surcharge`、金额为正。调整的 `related_request_id` 指向原预扣，三种事件共用 `operation_receipt_id`，重复轮询与恢复不会重复扣退款。

补扣余额不足时不透支，任务保留在结算中，收据 `payment_state=prepaid`、`actual_cents=null`；充值后后台重试，继续查询同一任务即可。不要换幂等键或重新提交生成。Muse 测时支持 MP4/MOV，读取文件最多 256 MiB、元数据最多 16 MiB，单次测时限时 60 秒。成片无法下载、解析或没有可信时长时保留待核对，不回退到提交时长，不自动退款或重发生成请求；已保存的完成产物和实测结算输入可跨进程继续处理。

安装任务通过 `GET /api/v1/donebot/receipts/{receipt_id}` 对账，使用登录 access token 和 `X-DoneBot-Installation`。响应沿用标准 envelope，读取 `data.payment_state`、`data.prepaid_cents`、`data.actual_cents`、`data.refunded_cents`。这些金额是现有收据币种的分单位，可包含小数；补扣从已结算实际金额减预扣计算，原预扣金额不改写。账本三种事件的净和应等于最终费用。

**Seedance 与其他现有视频保持另一口径：** 按提交的成片时长收费，成功后不按实际成片时长退款，不读取生成视频实际时长。Seedance 带参考视频仍按 `（可信输入参考视频总时长 + 提交的生成视频时长）× 有参考视频秒价`；输入视频时长继续在受理前核验。Seedance 新请求必须提供正整数 `duration`，不能用自动时长 `-1`。

## 下游验收

- 在隔离环境验证文生视频、参考图、时长别名、公开模型映射；4 秒和 30 秒应通过，3 秒和 31 秒应在付费 POST 前被拒绝。非 720p、流式、多余参考图、参考音视频同样在付费 POST 前被拒绝。
- 分别用短于、等于、长于提交时长的合成 MP4 验证退款、等额、补扣；让响应 `duration` 与真实轨道不符，确认按轨道收费。
- 重复并发查询、重启工作进程，核对原预扣不可覆盖、调整只出现一次；补扣余额不足后充值，应恢复同一任务且不再发送生成 POST。
- 使用不同用户、API Key、分组访问任务应返回 `404`；余额为零仍能查询原任务，未就绪下载返回 `409`，已结算任务可继续下载。

接口与计费测试使用合成数据，不要求执行付费生成。本次源码修改不代表生产已部署，也不修改 DoneBot 客户端默认 Seedance 模型；客户端需自行登记 Muse 原生参数并实现上述适配。
