# DoneBot 图片与视频统一异步协议

版本：`donebot.media.v1`。本文是网关对下游的协议契约，描述仓库实现，不代表生产已部署。
新增模型的实现步骤见[新模型适配指南](media-model-adapter-guide.md)。

## 1. 职责和边界

- 客户端登记每个模型的输入输出能力、支持模式、默认值、参数和素材限制，选择模型，完成语义规划及首选／备用确认。
- 网关接受**已经选好模型、模式和参数**的结构化请求，将其确定性转换成配置的上游格式，复用账号、鉴权、路由、任务归属、预扣／结算和回执。
- 不因为更换供应商而要求客户端改发 `content`、`image_urls`、`reference_images` 等原生字段。不按提示词猜模型、模式，不静默删除参考素材或降分辨率／时长。
- 统一协议能表达一种模式，不等于所有模型均支持该模式；不支持必须明确拒绝。
- 音乐、TTS 和用户自行选择的第三方媒体渠道不属于此协议。

## 2. HTTP 接口

Base URL 示例：`https://donebot.cn`。所有创建、查询、视频下载和素材管理均使用
`Authorization: Bearer <网关凭据>`；不向客户端或素材服务暴露上游密钥。

| 操作 | 方法、路径 | 内容 |
| --- | --- | --- |
| 图片创建（含参考、编辑、拆层） | `POST /v1/images/generations/async` | JSON，异步受理 |
| 图片查询 | `GET /v1/images/tasks/{task_id}` | 统一任务和图片结果 |
| 视频创建（所有模式） | `POST /v1/videos` | JSON，异步受理 |
| 视频查询 | `GET /v1/videos/{task_id}` | 统一任务 |
| 视频下载 | `GET /v1/videos/{task_id}/content` | 视频二进制；未就绪继续等原任务 |
| 上传参考文件 | `POST /v1/assets` | multipart/form-data，字段 `file` |
| 刷新素材地址 | `GET /v1/assets/{asset_id}` | 当前可访问的素材元数据 |

新请求明确包含 `protocol: "donebot.media.v1"`。图片编辑仍走统一创建入口，不要求下游
改调 `/images/edits/async`。视频编辑／延长也只通过 `mode` 区分，不要求改路径。
素材上传权限与上传限制独立存在，不能由模型素材上限推断网关一定允许该大小的上传。

## 3. 请求字段

顶层拒绝未知字段；不能在统一请求中混入原生参数。JSON 数字不能用字符串代替。

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| `protocol` | string | 必填，固定 `donebot.media.v1` |
| `model` | string | 必填，账号分组已启用的公开模型 ID 或配置别名 |
| `mode` | string | 必填，见下表；网关不从提示词推断 |
| `prompt` | string | 视频必填非空；图片通常必填，明确拆层等允许省略的情形以模型登记为准 |
| `references` | array | 可选，顺序保留，结构见下一节 |
| `duration` | integer | 视频必填正整数，单位秒，且在模型区间内；不接受自动时长 `-1`；图片禁止 |
| `resolution` | string | 视频必填，例如 `720p`；图片按模型支持填写，例如 `2K`；大小写不能混用 |
| `aspect_ratio` | string | 视频必填，例如 `16:9`、`adaptive`；图片客户端使用 `size`，网关图片适配仅在支持时接受比例别名，不能与 `size` 同时提交 |
| `size` | string | 仅图片，像素尺寸、比例或分档由模型登记约束 |
| `count` | integer | 仅图片，正整数，且不超过模型上限；不是拆层的层数 |
| `output_format` | string | 按模型支持，例如 `png`、`jpeg`、`mp4`、`mov` |
| `options` | object | 可选，供应商特有的显式控制，按模型白名单校验；禁止通过此处覆盖公共字段 |

网关不为统一视频请求补猜时长、比例、分辨率。DoneBot 客户端在发送前默认补齐
6 秒、720p；Muse 默认 `16:9`，Seedance 默认 `adaptive`，最终仍校验所选模型。
Seedance 的客户端集成固定带声音，网关转换为 `generate_audio=true`；统一协议不开放
`options.generate_audio`。静音诉求不能假装已通过该参数实现。

### 模式与素材角色

| 类型 | `mode` | 素材要求 |
| --- | --- | --- |
| 图片／视频 | `text` | 不得有参考素材 |
| 图片／视频 | `reference` | 至少一份，全部 `role=reference` |
| 图片 | `edit` | 至少一张参考图片，全部 `role=reference` |
| 图片 | `layers` | 至少一张参考图片；当前支持模型要求恰好一张，按模型校验 |
| 视频 | `first_frame` | 恰好一张图片，`role=first_frame` |
| 视频 | `first_last_frame` | 恰好两张图片，一张 `first_frame`、一张 `last_frame` |
| 视频 | `edit` / `extend` | 至少一个视频，全部素材 `role=reference`；模型还可有额外限制 |

Seedance 有任意参考时默认使用 `reference`，单张图也不自动当成首帧。只有用户明确
要求才选择首帧、首尾帧、编辑或延长。延长的 `duration` 是最终总时长，不是追加秒数。

当前接线边界（不是模型官网的完整能力列表）：

| 适配 | 已接入模式 | 限制摘要 |
| --- | --- | --- |
| Muse | `text`、`reference` | 只接受图片参考，720p，4–10 秒，16:9／9:16，MP4 |
| Seedance 2.0 系列 | `text`、`reference`、首帧、首尾帧、`edit`、`extend` | 编辑／延长保留视频参考和明确提示词；不发送 2.5 的任务类型字段；4–15 秒 |
| Seedance 2.5 | `text`、`reference`、首帧、首尾帧、`extend` | 4–30 秒；本接线未开放原生自动时长编辑；帧／延长模式要求 adaptive |
| Seedance 2.5 Flash | `text`、`reference`、首帧、首尾帧 | 4–30 秒，480p／720p；不开放编辑、延长、草稿 |
| Seedream 已适配型号 | `text`、`reference`、`edit`、支持型号的 `layers` | 素材、大小、透明背景和拆层限制按型号分别登记 |
| GPT Image 已适配型号 | `text`、`reference`、`edit` | 带图时网关内部转原生编辑入口；未开放 mask、拆层 |

型号分辨率、格式、素材数量等完整值以客户端登记和网关账户配置的交集为准。
例如本仓库 Seedance 2.5 当前新请求接线仍限 480p／720p／1080p，不能把官网宣称支持
4K 当作本次统一协议已经放开 4K；扩档需要同步校验与价格测试。

### 参考素材

线上 HTTP 只接受 HTTPS URL，不接受本地路径、base64 或 `asset://`：

```json
{"type":"image","role":"reference","url":"https://assets.example.invalid/reference.png"}
```

- `type` 为 `image`、`video`、`audio`，`role` 为 `reference`、`first_frame`、`last_frame`。
- 帧角色只能用于图片，不能和普通参考混用，也不能只传尾帧。
- 图片任务只收图片；Muse 只收图片；Seedance 按具体型号收取多模态素材。
- `url` 不允许凭据或 fragment；网关继续应用素材访问与上游安全策略。
- DoneBot 的 LLM 工具中用 `url: {"local_path":"/allowed/reference.png"}`，这是**客户端内部表示**，不是网关 wire 格式。客户端按现有会话权限读取、检查文件、上传后替换成 HTTPS URL。
- 文件格式、大小、数量与角色本地校验；尺寸、编解码、帧率、时长及肖像授权中尚未实现本地检查的部分仍由供应商校验。普通上传不能证明肖像授权。

### `options` 接线白名单

白名单不是“所有型号都支持”，还必须通过相应型号的原生参数、类型及安全校验：

| 适配 | 可选控制 |
| --- | --- |
| Muse | `extra`、`timeout` |
| Seedance | `watermark`、`return_last_frame`、`execution_expires_after`、`priority`、`safety_identifier` |
| Seedream／APIMart 图片 | `background`、`watermark`、`optimize_prompt_options`、`layer_decomposition_options` |
| GPT Image | `quality`、`background`、`output_compression`、`moderation`、`user`、`safety_identifier`、`response_format`、支持型号的 `complexity`、`frame_count`、`frame_duration_ms` |

未知 option 或 option 值 `null` 被拒绝。`stream` 不开放；原生内容数组、参考字段、任务类型、
`layer_decomposition` 等不能夹在 options；拆层由 `mode=layers` 表达。

## 4. 完整请求示例

```bash
# 在环境中提供网关凭据，不在源码中保存密钥。
# 对同一逻辑操作持久保存 OPERATION_ID，不能每次重试都随机生成。
curl "$GATEWAY_BASE_URL/v1/videos" \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H "Idempotency-Key: $OPERATION_ID" \
  -H 'Content-Type: application/json' \
  -d '{"protocol":"donebot.media.v1","model":"seedance-2-5-flash","mode":"reference","prompt":"保持图片1主体，镜头缓慢前推","duration":6,"resolution":"720p","aspect_ratio":"16:9","output_format":"mp4","references":[{"type":"image","role":"reference","url":"https://assets.example.invalid/reference.png"}]}'
```

将 `model` 换为 Muse 时，请求公共结构不变，但必须满足 Muse 的能力限制。
图片同样只改模型和支持的控制：

```json
{
  "protocol": "donebot.media.v1",
  "model": "seedream-5-5",
  "mode": "edit",
  "prompt": "只将背景改成浅蓝色，主体不变",
  "size": "1:1",
  "resolution": "2K",
  "count": 1,
  "output_format": "png",
  "references": [{"type":"image","role":"reference","url":"https://assets.example.invalid/reference.png"}]
}
```

以上素材域名均为占位符，不是真实资源。

## 5. 受理、查询与结果

统一创建成功返回 HTTP `202`；任务 ID 不透明，不能解析前缀、从 ID 猜上游或自己构造上游路径。
保存返回的 ID、可用的 receipt ID 和幂等键；始终查询原 ID。

```json
{"id":"opaque-task-id","task_id":"opaque-task-id","status":"processing","receipt_id":"optional-receipt-id"}
```

`id` 与 `task_id` 相同。`receipt_id` 不是所有凭据路径都提供；有 `X-DoneBot-Receipt-ID`
时一并保存。响应可能增加元数据，客户端应容忍额外响应字段。`Location` 和 `Retry-After`
可用时优先遵守；没有 Retry-After 时客户端使用有上限的退避，不忙轮询。

| `status` | 行为 |
| --- | --- |
| `processing` | 已受理，继续查询同一 ID |
| `completed` | 结果可交付；读取图片结果或下载视频 |
| `failed` | 明确失败；展示原因、核对回执，然后按客户端规则处理备用 |
| `uncertain` | 提交／结果／结算不确定，继续核对原任务，禁止重新创建收费任务 |

图片完成：

```json
{"id":"opaque-task-id","task_id":"opaque-task-id","status":"completed","result":{"data":[{"url":"https://assets.example.invalid/output.png"}]}}
```

`result.data[]` 的图片支持 `url`，原生兼容路径也可能有 `b64_json`。拆层时每张图的
`layer` 保留 `z_index`、`bounding_box`、名称、尺寸、格式、背景及文字识别等已提供元数据；
不能只保留图片 URL 而丢层次。结果链接有效期受存储策略约束，客户端按原附件机制下载保存。

视频完成：

```json
{"id":"opaque-task-id","task_id":"opaque-task-id","status":"completed","content_url":"/v1/videos/opaque-task-id/content"}
```

`content_url` 是相对网关路径，带同一凭据下载；不能转而把网关凭据发给任意外部结果 URL。
`generation_status=completed` 但 `status=processing` 表示生成结束、交付／结算仍未结束；
可以释放本地生成并发槽，但必须继续查询。content 返回 `409`／Retry-After 时继续下载
原任务，不重新生成。`generation_status=failed` 也不替代外层最终状态与回执核对。

## 6. 幂等、失败与备用

- DoneBot 安装凭据必须持久发送 `Idempotency-Key`；同键同参复用任务，同键异参 `409`。不要依赖 JSON 键顺序／省略字段变化构造重试，重放已保存的请求。
- 普通 API Key：GPT 异步图片沿用图片任务幂等服务；视频与 APIMart 普通 Key 包装不新增安装凭据级持久化 POST 幂等保证。响应统一不等于重试保证升级；需要可靠收费派发的 DoneBot 客户端使用安装凭据。
- 统一协议转换只发生于创建；已受理任务固定原供应商账号、请求快照和价格快照。重启／升级不选择新模型或改规格。
- 参数／能力不符通常 HTTP `400`／`422`，鉴权 `401`／`403`，任务不存在或不属于调用方 `404`，幂等冲突或产物未就绪 `409`。错误沿用 `{"error":{"type":"...","message":"..."}}`；任务 `error` 应兼容字符串／对象。
- 超时、断网、`429`、`5xx`、成功但缺任务 ID、结算中或下载失败都不是“确定没花钱”，不能据此派发备用；先核对原任务和回执。
- 备用是客户端计划，不传第二套供应商参数给网关。客户端复用同一请求结构，为备用重新验证能力；不能丢参考或把 1080p 改 720p 凑成功。不兼容备用不会阻断可用首选，会记录不可用原因。
- 仅明确失败才进入备用；图片先告知再继续，视频先告知再确认，已有明确免确认授权除外。最多一次备用，使用独立持久幂等键，并保留首选任务和回执。
- 停止客户端等待不等于取消上游或退款；协议未新增通用任务取消接口。

## 7. 兼容和部署

先部署网关，再发布客户端／Cloud 运行服务。先开模型账号、映射、分组授权和价格，再启用客户端选项。

不带 `protocol` 的旧原生请求和旧任务继续使用既有格式、查询及下载，不批量改写历史任务。
新持久任务保存协议标记，重启后仍返回统一结果。普通 Key 的统一任务 ID 包装只改变下游表示，
仍使用已有归属检查，不因知道一个 ID 就允许跨用户、Key 或分组查询。

新增模式／字段／option 必须同时修改协议文档和测试；更改已有字段语义或删除能力时升级协议版本，
不能让 `donebot.media.v1` 对同一请求产生不兼容的含义。
