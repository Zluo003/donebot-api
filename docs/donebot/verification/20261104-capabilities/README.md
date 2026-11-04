# DoneBot 模型能力目录修正

2026-11-04。所有可用文本 LLM 默认返回 `tool_calling: true`，不再受缺失或旧 false 元数据影响；媒体生成模型不启用 LLM 工具。机器人工具范围与审批仍生效。客户端阶段会兼容旧网关目录的 false，防止生产配置漏同步再次禁用所有工具。

新增可选 `capabilities.input_modalities`，仅登记精确模型配置的 text/image/audio/video；省略表示未登记，不按模型名或生成类别推测。严格拒绝 null、空数组、未知值、重复值和非字符串。模型能力变化会更新目录版本。现有十个生产模型的真实多模态能力没有逐一验证，因此没有捏造登记值。

修改：`backend/internal/service/donebot_catalog.go` 与测试，`docs/donebot/v3/catalog.schema.json`、`fixture.catalog.json`、`contract.test.mjs`、`README.md`、`phase1.md`。

验证（exit 0）：

- `go test ./internal/service -run 'TestDoneBot(PublicCatalog|Catalog|ValidateReasoning)' -count=1 -v`；Go 目录为 backend，结果见 [service-tests.log](service-tests.log)。
- `node --test docs/donebot/v3/contract.test.mjs`，21/21 通过，见 [contract-tests.log](contract-tests.log)。
- `git diff --check`，通过。

这是本地源码与合成测试验收；生产网关由用户部署，本轮未部署、未访问真实 Key 或调用付费模型。没有真实 staging 请求 ID 或结算回执。能力字段只登记模型支持；客户端目前原生文本协议有文本/图片编码器，原生音频和视频编码器不在本阶段。

回滚：回退本阶段提交并重新部署；无需迁移数据库。已保存的 input_modalities 若要回滚到不识别此字段的旧服务，先从能力设置中删除该新增字段。保留原值备份，不修改余额、价格和推理策略。
