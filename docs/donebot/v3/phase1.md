# 阶段 1：生产网关目录与安装管理

2026-11-04。本阶段不改变既有余额或价格数值；DoneBot 部署原值已经是人民币。`DONEBOT_CATALOG_CURRENCY=CNY` 显式声明币种，其他值不能启动。分与元只做十进制单位转换，保留微元精度。

## 接口

以下接口使用登录 access token；目录、账户和回执还要求 `X-DoneBot-Installation` UUID：

| 接口 | 行为 |
| --- | --- |
| GET `/api/v1/donebot/models` | 原始 JSON v3 目录，private/no-cache，鉴权与安装归属先于 ETag/304 |
| GET `/api/v1/donebot/account` | CNY 余额、冻结余额、本用户 DoneBot 分组累计用量及目录版本 |
| GET `/api/v1/donebot/installations` | 仅当前用户安装的安全元数据 |
| POST `/api/v1/donebot/installations/:uuid/rotate` | 一次返回新 credential；保留 APIKeyID、配额与任务归属 |
| DELETE `/api/v1/donebot/installations/:uuid` | 吊销当前用户安装；不能通过再次 claim/rotate 复活 |
| GET `/api/v1/donebot/receipts/:uuid` | 已结算文本回执；不存在时 404/Retry-After:3，不能自动重发生成 |

`sk-donebot-` 的每次认证读取数据库，绕过 L1/L2/singleflight，以保证跨节点轮换/吊销后下一次认证失效。已在执行中的请求不被追溯取消。普通个人 Key 的缓存行为保持原有规则。

目录仅含已启用、有可用绑定账号、有价格且可履行原生协议的模型。路由按实际 endpoint、优先级、目标平台和上游映射评估；可路由账号之间没有原生能力交集则保守隐藏相应接口。已有 Chat/Responses 转换不能当作原生能力证明。视频暂不公布，待 `/v1/videos*` 的真实适配完成后开放。

## 推理能力与回执

能力来自操作员设置 `donebot.model_capabilities.v3`，以精确公开模型 ID 为键，值为目录 `capabilities` 对象。全部对话 LLM 的工具调用默认开启，旧的 `tool_calling:false` 也不会将其降为纯文本；媒体生成模型仍为 false。可选 `input_modalities` 是非空、不重复的 `text`、`image`、`audio`、`video` 数组，只按精确模型 ID 登记，未登记则省略并表示未知；它与输出 `media_types` 独立，不按模型名猜测。推理等其它未知能力保持不支持。该配置尚无独立管理 UI，可通过现有设置存储维护。每个接口只接受白名单原生字段，具体结构和样例见本目录 schema/fixture。推理档位再应用分组上限、拒绝/降级映射，显式选择不支持的模型、协议、字段或档位返回 400。重复 JSON 键、歧义字段拒绝，原始请求体不被重序列化。

四类原生文本响应增加 `X-DoneBot-Receipt-ID`，值为服务端生成的 `X-Client-Request-ID`。SSE 不插帧、不改变顺序。回执只读取 `usage_logs.request_id=client:<UUID>` 的本用户、本安装 Key、本分组记录；返回实际 CNY 金额以及 `requested_reasoning` 和 `effective_reasoning`。文本预估金额未知为 null。此接口尚不表达媒体、失败或退款回执，不能把暂未结算的 404 当作生成失败。

DoneBot Key 的余额不足在原生鉴权和计费错误路径返回 402；429 的 Retry-After 继续保留。其他产品的个人 Key 保留原状态码。

## 验证与回滚

完整命令和结果见 `phase1-test-results.txt`。集成测试使用临时 PostgreSQL/Redis，不修改真实用户数据。staging 部署与真实请求 ID 单独记录，不能由夹具证明线上功能。

`DONEBOT_ENABLED=false` 关闭安装管理和目录；它不会撤销已有安装 Key，也不会删除余额或账本。`DONEBOT_FEATURES_VIDEO=false`、`DONEBOT_FEATURES_AUDIO=false` 关闭相应创建入口。完整回滚使用部署前保留的镜像或回退本提交；无数据库结构迁移，无资金迁移。回滚不会恢复文本 BFF 或报价接口。

当前限制：未知/混合原生协议能力保守隐藏；variant 控制未开放；有分组 effort 策略时不开放 budget；未提供显式档位时仍使用上游默认值。实际 CLI、钥匙串和客户端生命周期留待客户端阶段单独验收。
