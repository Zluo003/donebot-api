# DoneBot 工具能力：网关阶段验收

2026-11-04，`https://test.donebot.cn`，Docker 部署 `/home/donebot-api`。

## 结果与变更

已在北京时间 13:45:13 通过事务与 JSONB 原值比较更新
`settings["donebot.model_capabilities.v3"]`，并独立 SELECT 回读一致。
本阶段仅修改 operator 能力配置；网关源码、路由、上游账户、价格、余额、币种和推理档位没有改变。
不需要构建或重启 Docker。

工具能力原来仅声明 `gpt-6.1-terra`、`deepseek-flash`，其他模型缺项默认 false。
按模型 ID 精确读取是 `backend/internal/service/donebot_catalog.go` 的既定契约。
客户端遵守该目录，导致 Claude/Gemini 收到工具调用后没有本地可用工具。

真实资格验证后只增加以下精确模型的 `tool_calling: true`：

- `gpt-6-astra`、`gpt-6.1-sol`
- `claude-opus-5-5`、`claude-sonnet-5-5`、`claude-fable-5-1`
- `gemini-3.8-flash`、`gemini-4-argon`

原 GPT Terra 的 reasoning 对象与所有其他字段保持原值。
[metadata-application.json](metadata-application.json) 保存执行命令和实际 BEGIN/DO/COMMIT 结果；
[metadata-after.json](metadata-after.json) 是独立数据库回读。

## 真实验收

[qualification-summary.json](qualification-summary.json) 包含 9 款模型各自两次请求与 CNY 回执。
每个模型必须调用只读随机 nonce 工具一次，再将事先未知的结果回传给模型并正确作答。
七个新增模型先在隔离验收进程中明确覆盖工具标记来证明通道资格，记录中
`capability_override: true` 不等同于正式目录已生效；已有 DeepSeek/Terra 使用真实目录。
配置生效后 Claude Opus 与 Gemini Flash 再通过无覆盖的正式目录闭环复验，结果另附。

Claude 资格阶段发现外部 relay 给空参数工具返回 `_noargs: ""`，原始严格工具 schema
拒绝该字段。客户端针对封闭空对象 schema 做精确协议适配后，资格验收通过。
未放宽通用参数校验。该修复属于客户端阶段，必须配套升级客户端。

该阶段不运行付费媒体。临时安装均撤销，系统钥匙串临时项删除后回读为空，Key 未落盘。
SSH 转发仅用于验收，保留网关 Host 与 HTTPS 转发头，不进入发行应用。

## DeepSeek 两次原始失败

客户端工具列表已发送。第一次本机桥 502 没有到达网关；第二次网关请求
`c1d18993-8307-4d6a-9da9-484f29794dad`（客户端/回执 ID
`d73e4c4c-bbcc-430e-ae28-625e74dc8e2c`）在 13:01:52.639 返回 200，
输入 32235、输出 141 tokens，耗时 2476ms，本机却在 13:02:41.528 断流。
原始请求没有进入工具执行阶段。见 [gateway-observations.json](gateway-observations.json)。

公网健康检查慢且不稳定，未找到支持修改 CF 配置的证据。本次未修改 CF、DNS、TLS 证书或公网路由。

## 回滚

保存的 `metadata-apply.sql` 在事务内锁行并核对原值后写入目标值；
`metadata-rollback.sql` 只在当前值仍等于本次目标值时恢复原值，避免覆盖并发修改。
运行方式如下，数据库密码由容器内部环境读取，不导出凭据：

```sh
docker exec -i sub2api-postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1' < metadata-rollback.sql
```

回滚将移除本次七个工具能力声明，旧 GPT/DeepSeek 及 reasoning 不变。
