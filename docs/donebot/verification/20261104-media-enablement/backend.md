# 后端：取消媒体全局开关

日期：2026-11-04。范围为本地源码与隔离测试；未请求生产、staging 或收费供应商，未部署、推送或提交。

视频、音乐和 TTS 使用现有模型权限、账号映射、渠道、定价、安装凭据、余额与限流链路。删除按 `donebot.features.video/audio` 拦截原生路由的中间件，以及公开目录按该配置删除模型的过滤。未修改 Seedance 账号映射、原生路径、任务持久化或收费规则。

旧 `donebot.features.video/audio` 配置和 `DONEBOT_FEATURES_VIDEO/AUDIO` 环境变量仍能解析，显式 `false` 不再影响运行时。保留 Overview 的 `features` 响应字段兼容旧客户端，值为 `audio: true`、`video: true`；该字段不保证账号已配置任何可用模型。

## 改动文件

- `backend/internal/config/config.go`：旧字段标为弃用，保留解析兼容。
- `backend/internal/config/donebot_config_test.go`：旧 YAML 与显式 false 环境变量解析回归。
- 删除 `backend/internal/server/routes/donebot_media_gate.go`。
- 删除 `backend/internal/server/routes/donebot_media_gate_test.go`。
- `backend/internal/server/routes/gateway.go`：移除 `/v1`、根路径别名和 Codex 兼容链的媒体 gate。
- `backend/internal/server/routes/donebot_media_enablement_test.go`：17 条媒体创建、状态与内容路由在旧开关 false 时仍进入鉴权。
- `backend/internal/server/routes/apimart_test.go`：DoneBot 启用和关闭时，APIMart 路由均不受旧音频开关阻止。
- `backend/internal/server/routes/gateway_model_allowlist_test.go`：保留根路径模型权限中间件顺序断言。
- `backend/internal/service/donebot_admin_service.go`：旧 Overview 字段报告媒体无需额外启用。
- `backend/internal/service/donebot_catalog.go`：删除全局视频、音频目录过滤。
- `backend/internal/service/donebot_catalog_media_test.go`：false/true 均返回图片、视频、音乐、语音；禁用模型及无定价仍隐藏。
- `backend/internal/service/donebot_media_validation_test.go`：默认 false 配置的隔离夹具执行媒体权限、参数及 CNY 计费验证。
- `backend/internal/service/donebot_seedance_public_models_test.go`：四个明确公开型号的目录、限制、原生上游映射与持久化结算归属回归。

## 验证

在 `backend` 目录执行：

```sh
go test ./internal/config ./internal/service ./internal/server/routes ./internal/server/middleware ./internal/handler ./internal/handler/admin -run 'TestDoneBot|TestGatewayRoutes' -count=1 -v
go test ./internal/server/routes -count=1 -v
```

第一条：退出码 0；6 个包全部通过，150 个顶级测试、316 个含子用例结果，0 失败。完整原始输出见 [backend-tests.log](backend-tests.log)。包括模型目录、scope、安装凭据、媒体参数、异步操作与恢复、CNY、HTTP 回执、鉴权、模型白名单及管理 step-up 回归。

第二条：退出码 0；整个路由包通过，69 个顶级测试、129 个含子用例结果，0 失败。完整原始输出见 [backend-routes-tests.log](backend-routes-tests.log)。

四个公开型号另以 `unit` 标签测试，复用现有 Seedance 原生转发与结算 worker 的隔离替身：

| 公开 ID | 配置的官方上游 ID |
| --- | --- |
| `seedance-2-0` | `doubao-seedance-2-0-260128` |
| `seedance-2-0-fast` | `doubao-seedance-2-0-fast-260128` |
| `seedance-2-0-mini` | `doubao-seedance-2-0-mini-260615` |
| `seedance-2-5` | `doubao-seedance-2-5-260628` |

```sh
go test -tags unit ./internal/service -run 'TestDoneBotSeedancePublicModelsKeepCatalogMappingAndBilling|TestSeedanceCustomMappingsKeepPublicTariffAcrossDurableSettlement' -count=1 -v
```

每个型号在旧视频开关 `false` 下读取公开目录，检查公开 ID、`seedance.videos.v1`、分辨率与时长上限，验证超时长请求拒绝，再提交 720p、5 秒的内存替身请求。请求仅替换为配置的官方上游 ID，素材及生成参数保持；模拟任务持久化及重启结算后，usage 的 requested/model 仍为公开 ID，upstream 单独记录官方 ID。公开售价 0.2 与故意配置的上游售价 99 不同，实际结算按公开价格为 1 元。

结果：退出码 0；2 个顶级测试、8 个含子用例结果全部通过，0 失败，测试耗时 2.666 秒。完整原始输出见 [backend-seedance-public-models-tests.log](backend-seedance-public-models-tests.log)。所有上游与计费存储均为内存替身，没有网络生成或真实扣费。

`git diff --check` 通过。静态检索确认生产代码不再读取 `Features.Audio/Video`；保留的读取仅在兼容配置与回归测试中。

初轮路由测试发现既有源码断言仍包含已删除的 `mediaGate`，已更新后通过。APIMart 原有“开关关闭应返回 404”的断言也已改为验证原生处理器可达，并通过整个路由包。

最终只读复核后端、管理页和部署模板差异：业务改动限于移除媒体总开关、旧 Overview 兼容响应、管理页旧禁用提示及部署配置注入；未改 Seedance 账号映射、转发器、模型价格或任务结算规则。旧配置仍可读取，历史验收文档保留原记录并注明开关说明失效。未发现新增范围或功能缺陷。

## 边界与回滚

上述结果证明本地隔离服务与路由行为；没有真实请求 ID 或回执 ID，不证明部署中的公网目录已经更新。媒体模型仍须具备明确账号映射、可用渠道、模型权限、有效定价和已支持的原生协议；取消全局开关不会绕过这些检查。

无需数据库迁移。需要停用模型时使用现有 DoneBot 模型开关或渠道权限。旧 `DONEBOT_FEATURES_*` 已不作为运行时开关；恢复整个旧行为需回退本次源码或重新部署修改前镜像。
