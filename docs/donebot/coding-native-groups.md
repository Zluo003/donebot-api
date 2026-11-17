# Codex 与 Claude Code 原生分组

DoneBot 网关启用时，数据库迁移后自动确保三个独立绑定：

| 设置 | 默认分组名 | 平台 | 编程协议 |
| --- | --- | --- | --- |
| `donebot.group_id` | 原配置名称 | composite | 普通 DoneBot 聚合模型 |
| `donebot.codex_group_id` | DoneBot Codex | openai | `/v1/responses` |
| `donebot.claude_group_id` | DoneBot Claude Code | anthropic | `/v1/messages`、`/v1/messages/count_tokens` |

绑定使用持久化 ID，改名和重启不会覆盖管理员的倍率、模型、上游账号或停用状态。绑定组受删除保护。`donebot.auto_create_group=false` 时必须预先创建兼容的同名分组或配置绑定。

## 配置与计费

新编程组默认倍率为 1，模型白名单启用且为空。管理员需在原有分组管理中绑定对应平台上游账号、开放实际可用的编程模型，并配置原有模型价格／推理策略。不会自动复制聚合组的路由或覆盖运营定价。

DoneBot 管理页面增加“编程”页签，可分别设置两个组的倍率；保存仍调用标准分组更新接口，沿用管理员权限和二次验证。0.8 表示八折，原有用户分组专属倍率、模型定价、缓存、推理等级计费和余额／用量流水继续由 sub2api 原生链路处理。客户端不计算价格、不扣款。

## 用户密钥与安装鉴权

首次 DoneBot 登录或恢复安装时，客户端分别刷新两个编程组的原生模型列表。网关自动为用户补齐每组一把普通 API 密钥，在现有 API 密钥管理中可见。已有用户按相同流程补齐；并发和多个安装不会重复发放。数据库唯一绑定为 `(user_id, engine)`。用户主动停用、删除或改变组归属后，自动补齐不会重新发放或重新启用该密钥。

客户端请求仍使用当前安装凭据，附加 `X-DoneBot-Coding-Engine: codex|claude`。网关先执行原有安装 API Key 鉴权，再校验安装归属、当前凭据、撤销／轮换状态；随后在网关内部切换为该用户对应的原生 API Key，再次执行同一套标准 API Key 鉴权、ACL、组授权、配额和余额检查。实际转发和账单使用编程组及其密钥。此标识只允许原生组模型发现和对应的原生执行器端点，不能选择其他用户或任意接口。

每次委派均重新验证安装；Cloud 安装模式无需用户 JWT。原生密钥不会下发给 CLI。CLI 使用现有本地临时代理凭据，代理绑定当前账号／安装，退出或切换账号、停止任务时沿用原来的撤销和取消机制。用户直接使用普通编程 API Key 时，生命周期依照现有 API Key 管理，不依附某一个 DoneBot 安装。

## 推理与兼容性

原生编程组不经过 composite 专用的 DoneBot catalog 推理校验，直接复用对应平台的原生转发与推理策略。`reasoning.effort`、`thinking`、`output_config` 保留原始值；不会以删除推理参数来回避不兼容。上游实际支持哪些模型／等级，仍由原生上游和管理员配置决定。

原生模型发现使用 `/v1/models`，回复必须带有匹配的 `X-DoneBot-Coding-Engine`。客户端单独缓存两个编程目录，不回退到聚合目录；聚合目录的 ETag 未变化也会刷新编程目录。未升级网关不能被误判为已有编程分组。订阅登录保持独立，可随时切换。

## 发布与验证

先发布网关（包含 `268_donebot_coding_keys.sql`）并配置两个编程组，再发布 DoneBot 客户端和 Cloud 运行服务。更新客户端或 Cloud 镜像本身不会更新 donebot.cn 网关。

隔离验证命令：

```sh
cd backend
go test -tags=unit ./internal/service ./internal/handler ./internal/handler/admin ./internal/server/middleware ./internal/server/routes ./migrations
go test -tags=integration ./internal/repository -run 'TestDoneBot(NativeCodingDelegation|InitEntMigratesBeforeSetupAndRestarts|SetupFreshDatabaseAndRepeatedStartup|SetupConcurrentStartup)' -count=1
cd ../frontend
pnpm typecheck
pnpm exec vitest run src/components/admin/donebot/__tests__/DoneBotView.spec.ts src/components/admin/donebot/__tests__/CodingGroupsPanel.spec.ts
```

数据库测试由 testcontainers 启动一次性 PostgreSQL／Redis，使用合成账号。验证原生推理字段通过鉴权链且保持不变、实际组／倍率／Key 身份、并发幂等、安装撤销／轮换、多安装共享密钥、停用密钥、余额不足和无余额时的模型发现。客户端隔离服务验证参见 DoneBot 的 `docs/verification/donebot-coding.md`。这些离线测试不证明运营上游账号的可用性，不会调用付费模型或修改生产数据。
