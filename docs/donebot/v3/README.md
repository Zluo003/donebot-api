# DoneBot 网关契约 v3：阶段 0

日期：2026-11-04。此目录提供可执行的**纯本机假网关**与契约样例。它不调用真实账号、模型、支付或系统钥匙串，不是生产网关实现，也不是本机凭据桥。

当前工作遵循《03-AI 服务接入与推理档位方案》及用户澄清：现有 DoneBot 余额、价格数值原本就是 CNY，增加币种标识而不换算、不重算旧账。示例价格与 10000 分余额均为合成夹具数据，不是线上售价。旧文档关于隐藏 Key、文本 BFF、报价单和余额不足 403 的内容不适用；余额不足使用 402。

## 冻结的目录结构

`catalog.schema.json` 使用 JSON Schema 2020-12，`fixture.catalog.json` 是合法合成样例。

- `gateway_contract_version` 固定为 `3`。
- 目录顶层为 `catalog_scope_id`、`catalog_version`、`defaults`、`models`。
- 模型的 `platforms`、`interfaces` 位于顶层，`media_types` 区分文本与媒体。
- `capabilities.reasoning` 保留 `supported`、`control`、`levels`、`default`、`native_field` 与可选 `mapping`。未知默认值为 `null`。
- 同模型多协议的字段不同时，使用 `capabilities.by_interface[interface]` 声明具体推理能力。不能安全概括时，顶层 reasoning 为不支持，客户端按当前协议读取 by_interface。
- 不支持推理时：`supported:false`、`control:"none"`、`levels:[]`、`default:null`、`native_field:null`。不得显示或发送推理字段。
- `budget` 的 `mapping` 必须给出正整数 token 预算；`variant` 必须给出明确变体映射；未知能力不猜测。
- Chat 使用 `reasoning_effort`；Responses 使用 `reasoning.effort`；Anthropic 可按真实模型能力使用 `output_config.effort` 或预算；Gemini 以完整原生路径 `generationConfig.thinkingConfig.*` 声明。目录路径不是任意对象赋值指令，客户端按协议白名单处理。
- 用户未选择档位时不填默认值。模型不支持的档位拒绝；模型支持但超过分组上限时按策略拒绝或降级。降级回执同时提供 `requested_reasoning`、`effective_reasoning`。
- `pricing.currency` 固定 CNY。`input_per_million`、`output_per_million` 是元/百万 token 数值；媒体可使用 `per_request`、`image_sizes`、`tiers`、`unit`。所有 cents 字段是非负人民币分数值，允许小数分以保留现有账本的微元精度；由十进制单位换算得到，不能把不足 1 分的请求四舍五入为零，不涉及外汇换算。已结算文本回执必须有实际金额，不能用 `null` 冒充结算。
- 目录只能来自 `GET /api/v1/donebot/models`。此静态 JSON 仅测试夹具，不能用作客户端可用模型表。`GET /v1/models` 的引擎自检职责不变。
- 正式目录必须由已启用、可售、有实际价格和可用账号的模型生成。`defaults` 只能指向该目录中相应接口可用的模型。多渠道能力须保证所有实际路由均能履行，不能简单取并集。

先鉴权和确认安装所有权，再处理 `If-None-Match`。ETag 含目录 scope；跨账号不能获得旧 scope 的 304。目录内容相同则版本稳定，价格、能力、可用性变化则更新版本。200 响应整体替换，不能增量混合；缓存策略为 `private, no-cache`。

## 安装凭据

假登录 `POST /api/v1/auth/login` 仅接受 `fixture_user:a|b` 与 `fixture_password:fixture-only`，返回测试 access token。它不是生产登录 API 的实现或完整认证规范。

| 路径 | 夹具行为 |
| --- | --- |
| `POST /api/v1/donebot/installations/claim` | body 含 `installation_id`、`install_name`、`fingerprint`；首次返回 `install` 和一次性 `credential`；重放仅返回元数据 |
| `GET /api/v1/donebot/installations` | 仅本用户安全元数据 |
| `POST /api/v1/donebot/installations/:id/rotate` | 一次返回新 `credential`，旧 Key 立即 401 |
| `DELETE /api/v1/donebot/installations/:id` | 吊销安装，Key 立即 401 |
| `GET /api/v1/donebot/account` | 合成 CNY 余额 |

所有明文凭据响应 `Cache-Control:no-store`。真实客户端必须将安装 Key 与登录 token 分开保存到系统钥匙串。claim 响应丢失或钥匙串写入失败后，不可重放取回旧明文，只能认证后轮换。生产轮换需事务与鉴权缓存失效共同保证原子性；夹具仅演示单进程状态。

## 原生协议与回执

四条文本路径保持原生：

- `/v1/messages`
- `/v1/responses`
- `/v1/chat/completions`
- `/v1beta/models/:model:generateContent` 与 `:streamGenerateContent`

测试包括普通响应、原生工具调用/工具结果往返、流事件顺序、Anthropic beta 头、thinking signature 与 Responses reasoning item。夹具产生协议样例，不证明真实 CLI 或上游全部能力兼容。SSE 不插入 receipt 事件，不把四协议转换为 Chat。

文本回执采用 `GET /api/v1/donebot/receipts/:id`，使用登录 access token 和 `X-DoneBot-Installation` 校验本用户本安装 scope。正式关联约定为：服务端生成的 `X-Client-Request-ID` UUID 同时作为 `X-DoneBot-Receipt-ID`；只查询已有 `usage_logs.request_id = client:<UUID>` 的已结算记录。它是 **completed-only 的结算查询，不是提交已接收的凭证**。暂无行返回 404 和 Retry-After，客户端可以稍后重查，不能据此重新生成。原生 SSE 不追加回执事件。回执包含 receipt/request ID、CNY 费用及 requested/effective；小数分保留微元精度。夹具为明显合成 `fixture-*` 关联 ID，只模拟已结算响应、scope 和待查 404，不读取真实 usage_logs。正式数据库、响应头与结算语义须在生产代码阶段单独验收，不能凭夹具标为已上线。

本机桥的生产验收还必须覆盖随机 bearer、任意上游 URL/重定向防护、退出关闭监听、SSE 字节透传、背压和取消；此目录未实现该桥。

## 媒体与恢复

生成使用原生 `/v1/images/generations|edits`、`/v1/videos`（及 edits/extensions）、`/v1/music/generations`、`/v1/audio/speech`。speech 是同步音频，不能按旧方案读 chat-completions 音频字段。

模型顶层 `media_parameters` 可包含已知的 `sizes`、`aspect_ratios`、`resolutions`、`duration_seconds`、`voices`、`formats`、`music_versions` 与 `protocol`。未知字段省略。时长范围含 `min/max/step`；`automatic:true` 表示原生 `duration:-1`，`requires_custom:true` 表示仅在 Suno custom 模式接受时长。参数由实时可售模型对应账号的原生解析器和规格生成，并与账号分辨率限制、实际价格及所有可路由账号能力取交集。静态 schema 和夹具不能增加可用模型。

`protocol.adapter` 只允许下表的固定实现。submit/poll 路径、任务 ID、状态与结果字段是其协议描述，客户端选择内置适配器，不执行目录提供的任意 URL、路径模板或对象选择器。schema 和服务端元数据验证要求完整描述与白名单精确一致。`donebot.model_capabilities.v3` 的 `media_parameters` 只能缩小已知参数，不能增加不存在的音色、格式、版本、时长或协议。

| adapter | 原生提交与返回 | 原生轮询 |
| --- | --- | --- |
| `openai.images.v1` | `POST /v1/images/generations`，JSON 图片结果 | 同步，无任务轮询 |
| `apimart.images.v1` | `POST /v1/images/generations`，任务 ID 在 `data.0.task_id` | `GET /v1/tasks/{task_id}`；`data.status` 或 `status` 为 `success` 时读取 images |
| `apimart.music.v1` | `POST /v1/music/generations`，任务 ID 在 `data.0.task_id` | `GET /v1/music/tasks/{task_id}`；状态 `completed` 时读取 music |
| `apimart.speech.v1` | `POST /v1/audio/speech`，同步二进制音频 | 无任务轮询 |
| `seedance.videos.v1` | `POST /v1/videos`，保持 Seedance `content[]` 请求体，返回 `id` | `GET /v1/videos/{task_id}`；状态 `succeeded` 时读取 `content.video_url`，内容代理为 `GET /v1/videos/{task_id}/content` |

APIMart 图片的尺寸、比例、分辨率来自 `apimartImageTier`，未知输出格式不声明。Suno 只显示已有本地 tariff 的 `v6/v6-wild/v6-mini`；TTS 只声明原生解析器接受的六种音色与五种显式格式，不把省略字段的上游默认值硬填进请求。Seedance 使用账号配置的官方规格，2.0 时长为 4–15 秒，2.5 为 4–30 秒；多个账号取最窄范围，分辨率还必须有已定价的标准生成 tier。参数不是任意组合：2.5 首尾帧需要 `ratio:adaptive`，2.0 音频参考需要图像或视频参考，edit/extend 的输入约束继续由原生适配器和网关验证。

默认恢复夹具保留 `{id,status,receipt_id,poll_path}` 合成响应，用来独立验证 journal、轮换与进程重启；它不代表生产统一媒体 BFF。额外的 `nativeMedia:true` 夹具模式使用上述 APIMart 和 Seedance 原生 envelope，并验证同步 speech 与视频内容代理的二进制形状。生产已有可选 `/v1/images/generations/async`、`/v1/images/edits/async` 和 `/v1/images/tasks/:id` 包装路径；本目录不在未确认该功能启用时对外宣告这些路径。

幂等绑定稳定安装身份和 operation key，不能绑定会轮换的 API Key ID。同键同参返回同任务，同键异参 409。余额不足 402 发生在创建任务前；429 保留 Retry-After，不隐式重提或切模型。

恢复测试启动两个独立 Node 客户端夹具进程。第一个轮询后退出，第二个从无密钥 journal 恢复相同 task/receipt；中间轮换安装 Key，并重放原操作确认不新增任务。`resume-media.mjs` 是这个可执行客户端夹具，只接受显式 `127.0.0.1` 测试地址。合成 Key 通过 stdin 交给该客户端进程，在内存中使用，不进入 journal、stdout、stderr、命令行参数或环境变量。它不是引擎子进程，不是系统钥匙串实现。

生产客户端需要在提交前持久化 operation ID、请求指纹与 scope；响应丢失时先查询，不盲重提。终态附件去重、失败退款、真实媒体下载另行验收。

## 运行与结果

需要 Node 24+，不安装依赖，不读取真实凭据，所有网络请求均使用动态分配的 IPv4 loopback 端口。

```sh
node --test docs/donebot/v3/contract.test.mjs
```

单独启动夹具：

```sh
node docs/donebot/v3/fake-gateway.mjs
```

它仅输出 `{fixture:true,url,currency}`，Ctrl-C 关闭。测试运行时只会在本目录创建随机 `.fixture-run-*` 临时目录，正常 teardown 后删除。完整本机测试结果见 `test-results.txt`。

## 真实 staging 记录与限制

| 项目 | 状态 |
| --- | --- |
| 本机契约夹具 | 已执行，见 test-results.txt |
| 生产网关与客户端实现 | 此目录不提供，按后续阶段单独提交和验收 |
| 系统钥匙串与真实引擎集成 | 未执行，不能由无密钥 journal 测试代替 |
| 新接口真实 staging | 等待生产代码实现及部署；本阶段阻塞于部署，未发送真实请求 |
| staging 请求 ID / 回执 ID | 无；所有 `fixture-*` ID 均为合成测试 ID，不能充当真实证据 |
| 真实视频与费用 | 未下单、未扣费；后续 staging 视频仅 720p/5 秒 |

不记录或打印测试账号密码、access token、安装 Key。真实验收须记录部署 SHA、时间、脱敏请求摘要、响应码、真实请求 ID/回执 ID 与结算结果。

## 回滚

阶段 0 仅添加文档和夹具，不影响现有生产路由。回滚其提交即可移除此目录。正式 feature flag、环境变量、旧路径的回滚名称与行为必须由对应生产实现定义、测试；此阶段不虚构已存在的开关，也不恢复文本 BFF 或报价接口。
