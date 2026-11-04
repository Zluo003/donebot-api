# DoneBot Search / Fetch 网关契约

## 入口与权限

- `POST /v1/tools/search`：请求和成功响应遵循 [DeepSeek Search](https://api-docs.deepseek.com/zh-cn/api/search)。
- `POST /v1/tools/fetch`：请求和成功响应遵循 [DeepSeek Fetch](https://api-docs.deepseek.com/zh-cn/api/fetch)。
- 只接受经现有 API key 中间件认证的 `sk-donebot-...` 安装 key。再次核对当前 DoneBot 分组、数据库安装绑定、所属用户、安装状态及有效期。轮换或吊销后旧凭据由原认证链拒绝；调用方不能通过 `X-DoneBot-Installation` 或 `user_id` 选择其他所有者。
- 这两个入口不选择模型，不经过模型白名单或模型路由，也不增加文本推理 BFF。四类原生文本协议维持原路径。
- 网关固定访问 `https://api.deepseek.com/v1/tools/search` 或 `fetch`，仅注入后台保存的 DeepSeek key，不转发安装 key。拒绝重定向。网页 URL 交给 DeepSeek Fetch 读取，网关不直接请求该 URL。

Search 支持 `query`（1–128 个 Unicode 字符）、可选 `count`（1–50）、`ori_question`（null 或 1–128 字符）、`localization`（IP 或空字符串）及 `need_content`（默认 false）。Fetch 接受 `url`（绝对 HTTP/HTTPS URL，无用户信息，最多 8192 字符）。请求体上限 64 KiB，禁止重复 JSON 字段和未知字段。

上游超时 45 秒，成功响应上限 16 MiB。响应只保留官方字段并验证其类型；不返回上游诊断文本。当前保护为每进程每用户每分钟 60 次、全局最多 16 个同时请求；多副本限流独立。网关不重试请求。

## 后台配置

`GET /api/v1/admin/donebot/web-tools` 返回标准管理 API 包装：

```json
{"code":0,"message":"success","data":{"enabled":true,"key_configured":false}}
```

`PUT` 同一路径，要求现有管理员认证及 step-up：

```json
{"enabled":true,"api_key":"由管理员输入的 DeepSeek key"}
```

- 缺失或空 `api_key` 保留旧 key；`clear_api_key:true` 清除 key；非空 key 与 clear 不能同时提供。
- 数据库设置 `donebot.web_tools.v1` 只保存开关和 AES-256-GCM 密文，不保存明文。要求部署已配置稳定的 `TOTP_ENCRYPTION_KEY`；不能用启动时临时生成的密钥保存凭据。
- 管理 GET/PUT 只返回 `enabled` 和 `key_configured`，不回显 key 或密文。所有响应 `Cache-Control:no-store`。
- 默认 enabled=true，但没有 key 时直接返回可回退错误，不发送供应商请求。

## 主通道失败后的客户端回退

错误遵循原生 API 形式：

```json
{"error":{"code":"donebot_web_upstream_unavailable","type":"donebot_web_tools_error","message":"Web tools provider is unavailable"}}
```

客户端只允许在 exact code 为 `donebot_web_upstream_unavailable` 时执行一次模型原生回退。该码用于未配置/禁用/无法解密供应商 key、供应商网络失败或超时、供应商 401/402/408/429/5xx，以及成功体非法或超限。

供应商 401/402/429 映射为 HTTP 503 + 上述错误码，避免被误判为安装 key 失效或用户余额不足。供应商 400/403/404/413 返回原状态及 `DONEBOT_WEB_TOOLS_PROVIDER_ERROR`。本地认证/余额失败、限流（`DONEBOT_WEB_TOOLS_RATE_LIMIT`）、取消（`DONEBOT_WEB_TOOLS_CANCELLED`）和安装 scope 失败不允许回退。

客户端使用当前模型的原生协议：Responses `web_search`；Messages `web_search_20250305` / `web_fetch_20250910`；Gemini `googleSearch` / `urlContext`。Chat Completions 没有标准原生 Web 工具，需明确报告不支持。是否实际可用还取决于供应商对该模型的能力与账号权限。

## 费用与回执

官方非空 Search/Fetch 成功调用参考成本为人民币 ¥5/1000 次，即 ¥0.005/次。空 Search、空正文 Fetch 和错误参考成本为 ¥0。成功头 `X-DoneBot-Provider-Cost-CNY` 标记此参考值；`X-DoneBot-Tool-Billed:false` 明确本入口没有扣用户余额。这不是报价接口或用户计价策略。

本次没有新增用户扣费政策，因此不会伪造费用回执 ID 或写入模型 token 消费日志。若客户端触发模型原生回退，该模型请求仍由原生推理路径正常计费和生成回执。

## 验证与回滚

隔离测试覆盖 Search/Fetch 原生字段、无安装 key 上游泄漏、密文存储与管理接口不回显、缺 key 回退、供应商与本地 401/402/429 区分、取消、限流、有效安装和当前 group 校验、管理员 step-up。命令和完整日志见对应 `docs/verification/20261104-web-tools/` 记录。

本次不部署、不改生产配置，未使用真实供应商 key 发起付费请求，没有真实 staging 请求 ID/回执 ID。

回滚可在后台关闭 Search/Fetch 或清除供应商 key；客户端收到可回退码后使用已实现的原生协议回退。若需撤销代码，恢复本阶段提交即可，数据库只新增一个设置项，无 schema 迁移。关闭功能不会清除旧 key；清除 key 不影响安装凭据或模型推理入口。
