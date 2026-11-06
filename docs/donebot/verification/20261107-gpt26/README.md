# GPT Image 2.6 网关接入验收

官方来源（2026-11-07 核对）：

- [Flare](https://developers.openai.com/api/docs/models/gpt-image-2.6-flare)、[Sunburst](https://developers.openai.com/api/docs/models/gpt-image-2.6-sunburst)
- [GIF 生成指南](https://developers.openai.com/api/docs/guides/image-generation#generate-animated-gif-output)
- [图片生成接口](https://developers.openai.com/api/reference/resources/images/methods/generate)、[图片编辑接口](https://developers.openai.com/api/reference/resources/images/methods/edit)

已加入两个官方模型 ID 的后台默认选择。真实可售目录仍依据管理员配置的账号映射、
分组、活跃原生路由和价格，加入默认名单不会自动创建生产模型、价格或授权映射。

DoneBot 请求固定 `complexity=high`，没有复杂度选项；显式 low/medium、SVG 输出和
独立 svg 配置在受理预扣费前拒绝，账号映射后再次检查，既有 mask 限制保留。
普通网关调用者的原生复杂度与 SVG 请求能力不因 DoneBot 的产品限制被删除。
JSON 顶层重复键、multipart 重复单值控制字段均先拒绝，保留合法的重复参考图上传。

GIF 由官方同步 Images API 提供，可通过 generations 文生图，也可通过 edits 输入
静态参考图；客户端仍使用网关持久异步入口，网关不会把 `/async` 转发供应商。
接受帧数 2–16（原生默认 9），每帧时长按指南 20–2000 毫秒（默认 200），原始毫秒
值透传，由供应商按 10 毫秒精度取整。GIF 尺寸使用 auto 或正的 16 倍数像素尺寸；
不套用静态图最小像素限制，不虚构供应商未公布的总像素/帧预算。GIF 不接受 stream、
partial_images、output_compression、mask 或透明背景；透明素材继续使用 PNG/WebP。

当前 GIF 仅选 API Key 原生 Images 账号；Responses 图片工具的 GIF 合同尚未核实，
OAuth/Setup Token 不参与 GIF 调度，防御性请求构造也提前拒绝。它们的 2.6 静态图
仍保留，并传递复杂度参数。混合账号池只要存在可用 API Key GIF 路由就登记 GIF，
再应用管理员明确配置的输出格式限制。GIF MIME、扩展名及全部动画帧原字节保留。

两个 2.6 型号有独立官方 token 回退价：文本输入/缓存 5/1.25 美元每百万 token，
图片输入/缓存 8/2，图片输出 30；明确配置的价格仍优先。
DoneBot 持久异步图片的用户收费继续采用已配置的每个输出文件图片价格，GIF 是
一个输出文件，不按帧数暗增费用。仍在生成前按最高尺寸档 × n 原子预扣，明确失败
独立退款，保留扣费和退款两条流水。缺少明确可用的图片/按次价格时拒绝派发，不能
仅靠官方 token 回退价启用 DoneBot 生成。部署方须为两个型号配置映射、分组与
合适的商业单价，并自行核对 GIF 上游成本；本次未改生产配置或实际账务。

离线验证命令：

```sh
cd backend
GOMAXPROCS=2 go test -p 2 -tags unit ./internal/service ./internal/handler -run 'TestGPTImage26|TestDoneBotGPTImage26|TestOpenAIImagesResponsesDriverAndImageModels|TestGPTImage25' -count=1
go test -tags integration ./internal/repository -run TestDoneBotGPTImage26GIFPrepayAndRefundLedger -count=1
cd ../frontend
pnpm typecheck
pnpm vitest run src/composables/__tests__/useModelWhitelist.spec.ts
```

测试覆盖 GIF 文生图/静态参考编辑、控制字段边界、原始 JSON 与 multipart 保留、
GIF 账号调度限制、混合池格式目录、重复字段绕过拒绝、SVG/high 预扣费前策略、
旧 2.5 参数保留、显式价格优先、GIF 多帧字节保存及固定文件单价预扣/失败退款。
数据库集成场景使用仓库既有 Docker 临时数据库，确认两条独立流水及重复退款幂等；
未调用付费供应商或访问用户运行数据。具体执行结果以本轮工具与 CI 输出为准，
不能将离线替身产物认定为生产上游实测成功。
