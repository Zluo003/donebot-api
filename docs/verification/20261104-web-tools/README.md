# 2026-11-04 Search / Fetch 后端验收

测试仅使用隔离内存仓库、HTTP transport fixture 和 httptest 路由，不读取或修改用户应用、真实数据库或生产配置，不进行网关部署或付费供应商请求。

## 命令与完整结果

从 `backend/` 执行：

```sh
go run github.com/google/wire/cmd/wire ./cmd/server
go test -tags=unit ./internal/service ./internal/handler ./internal/handler/admin ./internal/server/routes ./internal/server/middleware -run 'TestDoneBot|TestGatewayRoutesGroupModelAllowlistMountedOnEveryGatewayRoute' -count=1 -v
go build -o /tmp/donebot-web-tools-server ./cmd/server
```

以上均 exit 0。完整原始输出分别保存于 [wire.log](wire.log)、[regression-tests.log](regression-tests.log) 和 [build.log](build.log)。构建成功没有输出，因此 build.log 为空。

覆盖 DoneBot 目录、安装、账户、推理档位、媒体恢复和原有网关中间件回归；新增覆盖 Search/Fetch 供应商字段与响应边界、密文存储、管理接口 key 不回显、安装 scope、当前分组、取消、本地限流、401/402/429 错误区分、后台 step-up，以及在现有认证链内注册且跳过模型选择。

## 限制和真实记录

- 按用户要求，网关部署由用户处理。本阶段没有真实 staging 请求 ID 或回执 ID。
- 本入口没有新增用户计价/扣费政策；成功返回 CNY 供应商参考成本和 `X-DoneBot-Tool-Billed:false`，不生成虚假费用回执。
- 供应商 API key 必须由管理员输入；保存要求稳定 `TOTP_ENCRYPTION_KEY`。缺 key 会触发客户端可识别的模型原生回退码。
- 限流和同时请求数为每进程保护，多副本独立。
- 客户端和管理前端测试在各自验收记录列出；本文件仅记录后端命令。

接口、配置、失败回退规则和回滚方式见 [web-tools.md](../../donebot/web-tools.md)。

## 管理前端

从 `frontend/` 执行，均 exit 0：

```sh
pnpm exec vitest run src/components/admin/donebot/__tests__/WebToolsPanel.spec.ts src/components/admin/donebot/__tests__/DoneBotView.spec.ts src/api/admin/__tests__/donebot.spec.ts src/components/admin/donebot/__tests__/StoragePanel.spec.ts
pnpm exec eslint src/components/admin/donebot/WebToolsPanel.vue src/components/admin/donebot/__tests__/WebToolsPanel.spec.ts src/views/admin/DoneBotView.vue src/components/admin/donebot/__tests__/DoneBotView.spec.ts src/api/admin/donebot.ts src/api/admin/__tests__/donebot.spec.ts
pnpm run typecheck
pnpm run build
```

组件/API/存储回归共 24/24 通过，见 [admin-regressions.log](admin-regressions.log)；定向测试 14/14，见 [admin-tests.log](admin-tests.log)。[admin-lint.log](admin-lint.log) 无诊断，[admin-typecheck.log](admin-typecheck.log) 类型检查通过，[admin-build.log](admin-build.log) 包含 i18n 3/3、类型检查和 Vite 构建全部结果，有既有 chunk-size、Browserslist 和弃用提示。后台新增「联网工具」标签和 DeepSeek API Key 密码输入框；输入为空保留、勾选清除删除，保存立即清空输入，失败不回显 Key，沿用 step-up。
