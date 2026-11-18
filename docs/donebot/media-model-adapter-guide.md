# 图片／视频新模型适配指南

先阅读[统一异步协议](media-protocol.md)。目标是：**接一个新模型，只增加网关适配和客户端能力登记，不改变下游请求方式。**

## 1. 接入前记录

使用模型官方文档确认输入／输出模态、模式、时长、输出分辨率和格式、比例、参考数量／文件限制、默认值和独有参数；同时核对实际使用的上游 API 文档。
记录来源和核验日期。官网支持但当前上游不支持的能力，不得标为客户端可用。

区分三个名字：下游公开 ID、上游模型／endpoint ID、用于校验的规格 ID。账号别名只做确定性路由；不能根据 prompt 选规格。
同一公开别名的候选上游必须具有兼容的规格，冲突应拒绝，不能由调度偶然决定能力。

Seedance 当前新增上游工作仅针对 NewToken，不增加本地订阅转 API 或新的供应商接入。

## 2. 网关实现清单

### A. 路由与规格

- 在现有账号与模型映射机制中增加型号，绑定正确 endpoint capability 和允许的分组。
- 在 `backend/internal/service/donebot_media_protocol.go` 的 `UnifiedMediaNativeModel` 中确保能识别新规格；复用同系列映射时不增加新 HTTP 入口。
- Seedance 规格与账号分辨率在 `seedance_account.go`；Muse 在 `muse.go`；APIMart 图片在 `apimart_images.go`；GPT Images 复用现有 Images 解析。
- 同步实际可售分辨率／时长的价格校验、管理页及计费分档。未配置价格不能被当作免费；不能默默借用其他型号的价格。

### B. 请求转换

扩展 `ConvertUnifiedMedia`，只读取显式结构化字段：

1. 验证 `mode` 与素材角色，拒绝该型号不支持的操作。
2. 将公共字段按供应商文档映射，例如 `references` → Seedance `content`／Muse `reference_images`，`count` → `n`。
3. 公共字段存在但供应商无法表示时明确报错；不忽略、裁剪、改写 prompt 或悄悄降档。
4. 独有控制在 `options` 白名单中增加，并交由原生解析器验证类型、范围和计费影响。
5. 图片编辑内部可以路由到原生 edits，但下游仍调用同一个异步图片创建入口。
6. 不接收语义工作计划、不选择备用、不询问 LLM、不向客户端下发能力表。
7. 转换必须在 DoneBot 中间件的原始请求幂等处理之后、原生校验/估价之前执行；同时返回原生内部路径，保留统一协议标志及可重读的 body。不能只在最终 handler 转换。

### C. 任务与结果

- 已有供应商协议继续走原持久任务派发，统一任务保存 `UnifiedProtocol`，`AcceptedBody`／`PublicBody` 输出统一结构。
- 新供应商如需新增 dispatch protocol，必须补持久化／重启、唯一提交、任务归属、轮询、下载、预扣／结算／退款和回执映射，不能只接一个 POST。
- 图片结果统一到 `result.data[]`，保留图层和图片元数据；视频统一到同一 content 下载入口。
- 普通 Key 也要统一输出。现有 `handler/unified_image_task.go`、`handler/unified_video_task.go` 仅包装已有任务与归属；不能声称它们新增了持久化 POST 幂等。
- 上游完成但产物／结算未就绪时，仍保持外层 `processing`；结果不明用 `uncertain`，不能包装成可安全再生成的失败。
- 不改变旧 native 请求和已有任务的语义。

### D. 最少回归

```sh
cd backend
go test -tags unit ./internal/service ./internal/handler \
  ./internal/server/middleware ./internal/server/routes \
  -run 'TestUnified|TestDoneBotUnified|TestDoneBotMedia|TestDoneBotDispatch|TestDoneBotVideo|TestMuse|TestSeedance|TestAsyncImage' -count=1
```

新增型号测试至少包含：

- 相同结构请求跨已有／新增型号转换，公开别名、原生 ID 和规格 ID 正确。
- 各模式成功、非法模式／角色失败；素材数量／类型、分辨率／时长／格式边界；未知字段和 options 拒绝。
- 完整 handler 创建→查询→下载或取图；跨用户／Key／分组拒绝，不接触上游。
- 持久序列化恢复、同幂等键重复不新建收费请求、异参冲突、提交超时后不重发。
- 生成完成但交付／结算待定、缺结果 URL、下载 409、结算失败与真实失败退款。
- 老原生请求不带 protocol 时不变；历史任务仍可查询、下载、核账。

全部用内存 mock、临时数据库或隔离 fixture；不使用真实付费供应商、线上账号或用户桌面数据。

## 3. DoneBot 客户端登记清单

在客户端仓库完成以下项目，不能仅把模型名称塞进下拉框：

| 位置 | 应登记／检查 |
| --- | --- |
| `server/donebot/capabilities.ts` 与型号专用 `*-capabilities.ts` | 官方 ID、输入输出、模式、参数范围、默认值、文件策略、核验日期和来源；官网能力与当前接线可用能力分开 |
| `server/donebot/media-registry.ts`、`registeredMediaAdapters` | 账号实际启用 ∩ 本地支持；正确媒体类型与接口；不为新增型号擅自替换用户首选 |
| `server/donebot/media-model-schema.ts`、`unified-media.ts` | LLM 工具只看公共 schema，新增型号正确派生 modes／枚举／options；HTTP 固定带 `donebot.media.v1` |
| `server/donebot/media-assets.ts` 与型号文件策略 | 会话文件权限、本地 MIME／大小／数量／角色校验、上传、内容指纹；等待中变更文件应拒绝 |
| `server/donebot/media-guidance.ts`、`media-tools.ts` | 清楚说明默认参考语义；不要求 LLM 拼供应商 content 或第二套 fallback 参数 |
| `server/donebot/media-fallback.ts`、`media.ts` | 同一语义规格复用、首备兼容性、仅明确失败、视频确认、最多一次；不兼容备用不阻断首选 |
| 设置和交付测试 | 已启用模型可选、停用值保留、异步结果回原会话；既有任务仍用保存的 adapter |

新任务实际 HTTP adapter 为 `donebot.media.images.v1`／`donebot.media.videos.v1`。
现有注册中的供应商 adapter 标识仍用于识别旧任务／能力来源，不能批量改写持久历史记录。

客户端最少回归：

```sh
pnpm typecheck
pnpm exec vitest run server/donebot/unified-media.test.ts \
  server/donebot/media-guidance.test.ts server/donebot/media-routes.test.ts \
  server/donebot/media-fallback.test.ts server/donebot/media-settings.e2e.test.ts --maxWorkers=1
```

端到端必须走真实工具 schema（含实际使用的 Gemini 等 LLM 工具传输）→隔离网关→后台查询→原会话附件；不能只调内部 submit 函数。
覆盖首备不同供应商、备用不兼容、视频允许／拒绝／撤销确认、一次受理与重启恢复。
启动与证据规范遵循客户端 `docs/verification/README.md`。

## 4. 文档与发布验收

- [ ] 更新统一协议的模式／字段／options／支持矩阵，修改示例并按相同参数加入回归。
- [ ] 更新本指南涉及的实现位置；供应商原生文档明确只是 native 兼容，不再作为新客户端请求格式。
- [ ] 同步客户端 `docs/donebot-media-protocol.md`、`docs/donebot-media-models.md` 和验证配方。
- [ ] 先部署网关和必要迁移，启用账号映射、分组权限与价格，再发布客户端／Cloud 运行服务。
- [ ] 明确区分隔离验证、生产部署和真实供应商验收；不能用 mock 通过声称线上已成功。
- [ ] 网关代码由项目负责人自行推送时，只交付改动与测试记录，不代为推送。

适配回归须覆盖完整中间件顺序（原始请求指纹 → 转换 → 原生路径校验 → 原生参数计价 → 单派发 → 重放）、中间件已转换请求进入实际异步 handler，以及真实目录配置的校验与计价。仅验证转换器输出或只挂假 handler 不能证明统一入口可用。隔离假上游测试不等同于生产收费任务验收。
