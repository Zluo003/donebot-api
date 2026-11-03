# APIMart 图片与音频接入

本页只描述 donebot-api 网关。默认上游为 `https://api.apimart.ai`，使用供应商 Bearer API Key。没有新增账本、普通用户 Key 管理、报价单或预授权接口。

## 模型与原生接口

| 能力 | 模型 | 提交 | 查询 |
| --- | --- | --- | --- |
| Seedream 5.0 Flash 文生图、参考图编辑 | `seedream-5-0-flash`，兼容 `seedream-5.0-pro` | `POST /v1/images/generations` | `GET /v1/tasks/{task_id}` |
| Suno 歌曲、器乐生成 | `suno`，`version` 为 `v6` / `v6-wild` / `v6-mini` | `POST /v1/music/generations` | `GET /v1/music/tasks/{task_id}` |
| TTS 文字转语音 | `gpt-4o-mini-tts` | `POST /v1/audio/speech` | 同步返回音频二进制 |

图片和音乐的任务 ID 来自原生响应 `data[0].task_id`。查询沿用原生状态与结果结构，Suno 的 `data.result.music[]` 不固定为两首。不增加 Suno 延长、翻唱、歌词工具、视频或训练端点。

文档依据：[Seedream Flash](https://docs.apimart.ai/cn/api-reference/images/seedream-5-0-flash)、[Suno 通用约定](https://docs.apimart.ai/cn/api-reference/audios/suno)、[Suno 主生成](https://docs.apimart.ai/cn/api-reference/audios/suno/generation)、[TTS](https://docs.apimart.ai/cn/api-reference/audios/tts)。

## 管理端配置

1. 账号管理点击“添加 APIMart 账号”，填写名称、供应商密钥，选择图片 / Suno / TTS 模型并绑定 DoneBot 聚合分组。专用面板支持编辑；修改时留空密钥表示保留原值。
2. 在 DoneBot 图片或音频页为每个模型设置最终售价，价格单位见下节与[模型定价说明](模型定价说明.md)。价格写入既有分组模型价格卡，优先于渠道默认价。
3. 同步模型后按需开关；未配置可用价格的模型不能启用。已有渠道默认价格仍可作为未覆盖模型的价格来源。
4. 使用 Suno / TTS 时，在 `.env` 或 `/etc/sub2api/environment` 设置 `DONEBOT_FEATURES_AUDIO=true` 并重启。音频默认关闭；关闭时音频接口和管理页不可用。图片不依赖音频开关。

账号内部仍是现有 `openai` / `apikey`，通过 `openai_capabilities` 明确启用 `apimart_images`、`apimart_suno`、`apimart_tts`。普通 OpenAI 账号不会自动取得这些能力。模型清单与别名沿用 `credentials.model_mapping`。

## 本地定价与结算

上游响应中的 `cost` / `credits_cost` 原样返回，但不作为网关扣款金额。运营方在 DoneBot 管理页按模型配置最终售价；底层仍通过已有分组价格卡、倍率、余额和 `RecordUsage` 结算。用户专属倍率继续按原链路生效。无有效本地价格的生成请求在调用上游前拒绝。

| 能力 | 渠道计费模式 | `tier_label` | 本地单位 |
| --- | --- | --- | --- |
| 图片 | `image` / `per_request`，可复用分组图片覆盖价 | `1K`、`2K` | 每张实际产出图片/图层；`1.5K` 使用 `1K` 价格 |
| Suno | `per_request` | `v6`、`v6-wild`、`v6-mini`、可选 `custom-model` | 每次生成；`max_mode=true` 计两单位，与返回音轨数量无关 |
| TTS | `per_request` | `tts` | 每百万 Unicode 字符；输入字符数 ÷ 1,000,000 |

未设置该模型的分组直接覆盖时，可沿用渠道默认 `per_request_price`。一旦设置模型直接覆盖，留空的媒体档位表示未配置，不逐档回退渠道价。图片沿用已有图片价格与倍率链路。精确像素按实际输出面积分档：面积不超过 2,601,124 像素使用 `1K` 价格，更大使用 `2K`；`1.5K` 同样使用 `1K`。

图层拆分按每张实际产出的图片/图层分别计费，按各自产出尺寸求和。提交前用最多 17 张估算余额，自动或未指定尺寸保守按 `2K` 估算；最终按实际输出结算。供应商可能对第二张起的参考图、审核等另收费用，运营方需在售价中覆盖这些成本；网关不添加供应商附加费用的独立账目。

异步任务复用 Redis 的任务归属、账号绑定与计费 claim，归属绑定到提交用户和安装凭据，查询固定走原账号；完成并取得有效产物后通过现有 `RecordUsage` 扣款，多次查询只结算一次。失败任务不计费，任务查询不因余额耗尽被拦截。TTS 成功取得音频后结算，原生音频 MIME 和字节保持不变。

图片与音乐受理后写入 Redis 持久化结算队列，由后台固定使用提交账号查询至终态并调用现有 `RecordUsage`；结算不依赖客户端持续查询。进程重启后恢复待办，只有成功记账或上游失败才移除队列项。队列不保存另一套余额或账本。任务归属映射保留 24 小时，与待结算队列的保留期分开。必须配置 Redis 持久化和数据卷。客户端仍可查询原生任务状态。

网关不会自动重试收费 POST。网络超时或已受理后本地持久化失败时，提交结果可能不确定；已取得任务 ID 时错误会带回该 ID，请先在供应商控制台核查，避免重复生成。GET 可以重试。

## 参数约束

- Flash 单次生成一张；分辨率仅 `1K`、`1.5K`、`2K`。支持文档中的比例与精确像素；精确像素时 `resolution` 不参与尺寸选择。参考图支持供应商文档的公网 HTTP(S) URL 或图片 Data URI，最多十张。
- Flash 的 `size` 档位优先于 `resolution`；图层拆分、透明背景、坐标提示及供应商新增字段按原生协议传递。`asset://` 是 Seedance 的素材协议，APIMart 文档未声明支持它；使用本仓库上传接口返回的公网 URL 即可。
- Suno 灵感模式 `custom=false`，自定义歌词模式 `custom=true`，纯音乐可设 `instrumental=true`；`custom_model_id` 与公共 `version` 不能并存。主生成端点的 `style` 和 `weirdness_constraint` 不替换成其他 Suno 端点的字段名，数值 `0` 保留。
- TTS 输入最多 4096 个 Unicode 字符，音色 `alloy` / `echo` / `fable` / `onyx` / `nova` / `shimmer`，格式 `wav` / `opus` / `aac` / `flac` / `pcm`，速度 0.25–4。

## 请求示例

`GATEWAY` 是网关地址，`INSTALL_CREDENTIAL` 是当前登录用户领取的安装凭据。不要将真实凭据写入仓库。

```bash
curl "$GATEWAY/v1/images/generations" \
  -H "Authorization: Bearer $INSTALL_CREDENTIAL" -H 'Content-Type: application/json' \
  -d '{"model":"seedream-5-0-flash","prompt":"雨夜街道","resolution":"1.5K","size":"16:9"}'
curl "$GATEWAY/v1/tasks/<task_id>" -H "Authorization: Bearer $INSTALL_CREDENTIAL"

curl "$GATEWAY/v1/music/generations" \
  -H "Authorization: Bearer $INSTALL_CREDENTIAL" -H 'Content-Type: application/json' \
  -d '{"model":"suno","version":"v6","custom":false,"instrumental":true,"prompt":"安静的钢琴曲"}'
curl "$GATEWAY/v1/music/tasks/<task_id>?language=zh" -H "Authorization: Bearer $INSTALL_CREDENTIAL"

curl "$GATEWAY/v1/audio/speech" \
  -H "Authorization: Bearer $INSTALL_CREDENTIAL" -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini-tts","input":"你好，欢迎使用。","voice":"alloy","response_format":"wav"}' \
  --output speech.wav
```

## 后台在线升级

管理员点击侧栏版本标记进入更新窗口：检查版本 → 更新 → 重启。升级从本仓库 Release 选择对应 Linux 架构，校验 `checksums.txt`，备份原二进制并替换；重启后可回滚二进制。正式 Linux Release 内含前端页面，不需单独部署静态文件。

Docker 的在线替换只作用于当前容器可写层。要持久升级镜像，请运行 `docker compose pull && docker compose up -d`。源码 `BuildType=source` 构建需自行重新构建。二进制回滚不恢复数据库或配置；升级前备份数据库，完整说明见 [部署文档](../../deploy/README.md)。

本地测试使用模拟上游与隔离的升级子进程，未使用真实 APIMart 密钥消费。正式上线前仍需核对账号模型权限、真实产物下载、供应商账单与所设本地价格。
