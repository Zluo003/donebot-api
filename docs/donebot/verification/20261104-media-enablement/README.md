# 媒体按模型开放验收

日期：2026-11-04。目标版本：v0.4.5；版本号、发布和部署由发布流程处理。本目录记录本地代码和隔离测试结果，不代表已发布或已部署。

## 最终行为

撤销 DoneBot 独立的视频和音频总开关。公开目录不再因为旧 `donebot.features.video/audio=false` 删除视频、音乐和 TTS；原生媒体路由也不再被该配置提前拦截。媒体可用性继续取决于现有账号能力、公开模型映射、分组模型启用状态、可用渠道、有效价格、安装凭据、余额和限流。

旧 YAML 字段和 `DONEBOT_FEATURES_VIDEO`、`DONEBOT_FEATURES_AUDIO` 环境变量保持解析兼容，但不再控制调用；sh/systemd 旧部署中的 `false` 无需手动清理。为兼容旧前端，Overview 的 `features.audio/video` 保留且恒为 `true`；这不表示账号已配置可用媒体模型。当前前端不再读取该状态，也不显示“调用未启用”。

四个公开 ID `seedance-2-0`、`seedance-2-0-fast`、`seedance-2-0-mini`、`seedance-2-5` 的能力档案适配属于 DoneBot 客户端。本轮网关只增加隔离回归，验证既有公开 ID → 上游 ID 映射、目录规格、原生提交、持久任务及按公开模型价格结算；不新增或修改用户的账号、映射、模型启用状态、售价或余额配置。

## 改动文件清单

以下列表是本轮功能与回归的文件清单；版本和发布产物由主发布流程另行记录。

| 文件 | 改动 |
| --- | --- |
| `backend/internal/config/config.go` | 标记媒体总开关为弃用兼容字段 |
| `backend/internal/config/donebot_config_test.go` | 旧 YAML / 环境变量 false 仍可解析 |
| `backend/internal/server/routes/donebot_media_gate.go` | 删除总开关门禁 |
| `backend/internal/server/routes/donebot_media_gate_test.go` | 删除旧门禁行为测试 |
| `backend/internal/server/routes/gateway.go` | 移除原生、别名及 Codex 链路中的媒体门禁 |
| `backend/internal/server/routes/donebot_media_enablement_test.go` | 17 条媒体创建/查询/内容路由在旧 false 下仍进入鉴权 |
| `backend/internal/server/routes/apimart_test.go` | APIMart 路由忽略旧音频开关 |
| `backend/internal/server/routes/gateway_model_allowlist_test.go` | 更新中间件顺序断言，保留模型权限校验 |
| `backend/internal/service/donebot_admin_service.go` | Overview 兼容状态恒为 true |
| `backend/internal/service/donebot_catalog.go` | 删除按全局媒体开关过滤目录的逻辑 |
| `backend/internal/service/donebot_catalog_media_test.go` | false / true 均提供可用媒体，禁用模型及无价模型继续隐藏 |
| `backend/internal/service/donebot_media_validation_test.go` | 默认旧 false 下验证媒体权限、参数和 CNY 计费 |
| `backend/internal/service/donebot_seedance_public_models_test.go` | 四个公开 Seedance ID 的映射、规格、原生提交、持久任务和售价回归 |
| `frontend/src/api/admin/donebot.ts` | 当前前端类型不再依赖 Overview 总开关 |
| `frontend/src/views/admin/DoneBotView.vue` | 删除音频/视频“调用未启用”提示 |
| `frontend/src/components/admin/donebot/__tests__/DoneBotView.spec.ts` | 无状态字段、旧 false、兼容 true 均展示媒体页且不出现旧提示 |
| `deploy/.env.example` | 删除媒体总开关默认值 |
| `deploy/config.example.yaml` | 删除媒体总开关示例 |
| `deploy/docker-compose.yml` | 停止传递媒体总开关 |
| `deploy/docker-compose.local.yml` | 停止传递媒体总开关 |
| `deploy/docker-compose.standalone.yml` | 停止传递媒体总开关 |
| `deploy/docker-compose.dev.yml` | 停止传递媒体总开关 |
| `deploy/install.sh` | sh 安装生成的 systemd 单元不再写入总开关 |
| `deploy/sub2api.service` | 静态 systemd 单元不再写入总开关 |
| `deploy/tests/docker-compose-donebot-env-test.sh` | 四份 Compose 默认值、旧 false 环境隔离及 sh/systemd/YAML 检查 |
| `deploy/README.md`、`deploy/DOCKER.md` | 当前部署改为按模型开放，说明旧 false 无需清理 |
| `README.md`、`README_CN.md`、`README_JA.md` | 更新三语言当前部署与接入说明 |
| `docs/donebot/README.md`、`模型定价说明.md`、`Seedance-接入说明.md`、`APIMart-接入说明.md` | 更新当前模型开放规则与操作说明 |
| `docs/donebot/验收记录.md`、`DoneBot-网关改造方案.md` | 为历史范围及总开关行为增加版本说明，保留原记录 |
| `docs/donebot/v3/phase1.md`、`gateway-media.md`、`staging-final.md` | 标明旧开关回滚说明仅适用历史版本，保留历史请求证据 |
| 本目录 `README.md`、`backend.md`、`release-notes.md` 及日志 | 本轮验收与发布说明 |

## 测试命令与完整结果

所有命令在本地执行。前端测试使用模拟 API；Go 测试使用隔离夹具；Compose 检查只渲染配置，不启动容器。日志链接指向完整命令输出，不以摘要替代原始结果。

| 工作目录 | 命令 | 结果 | 完整输出 |
| --- | --- | --- | --- |
| `backend` | `go test ./internal/config ./internal/service ./internal/server/routes ./internal/server/middleware ./internal/handler ./internal/handler/admin -run 'TestDoneBot\|TestGatewayRoutes' -count=1 -v` | exit 0；6 包通过，150 个顶级测试 / 316 个含子用例结果，0 失败 | [backend-tests.log](backend-tests.log) |
| `backend` | `go test ./internal/server/routes -count=1 -v` | exit 0；69 个顶级测试 / 129 个含子用例结果，0 失败 | [backend-routes-tests.log](backend-routes-tests.log) |
| `backend` | `go test -tags unit ./internal/service -run 'TestDoneBotSeedancePublicModelsKeepCatalogMappingAndBilling\|TestSeedanceCustomMappingsKeepPublicTariffAcrossDurableSettlement' -count=1 -v` | exit 0；2 个顶级测试 / 8 个含子用例结果通过，0 失败，2.666 秒 | [backend-seedance-public-models-tests.log](backend-seedance-public-models-tests.log) |
| `frontend` | `pnpm exec vitest run src/components/admin/donebot/__tests__ src/api/admin/__tests__/donebot.spec.ts` | exit 0；8 文件、44 测试通过，0 失败 | [frontend-tests.log](frontend-tests.log) |
| 仓库根 | `sh deploy/tests/docker-compose-donebot-env-test.sh` | exit 0；四份 Compose 默认值和旧 false 隔离、sh/systemd/YAML 检查通过 | [deployment-tests.log](deployment-tests.log) |
| 仓库根 | `bash -n deploy/install.sh deploy/docker-deploy.sh` | exit 0 | [deployment-tests.log](deployment-tests.log) |
| 仓库根 | `sh -n deploy/tests/docker-compose-donebot-env-test.sh` | exit 0 | [deployment-tests.log](deployment-tests.log) |
| 仓库根 | `git diff --check` | exit 0 | [deployment-tests.log](deployment-tests.log) |
| `frontend` | `pnpm run build` | exit 0；i18n 3 测试、`vue-tsc -b` 及 Vite 生产构建通过，Vite 耗时 4 分 4 秒 | [frontend-build.log](frontend-build.log) |

另已执行 `pnpm exec vue-tsc --noEmit` 和只针对改动三个前端文件的 `pnpm exec eslint`，均 exit 0、无输出；该先前检查没有独立完整日志，最终类型验证以本轮生产构建日志为准。UI 日志中的 Browserslist 数据库提示和构建日志中的运行时告警保留在原始输出中，不将其隐藏或计为测试失败。

后端详细范围、兼容策略及初轮断言修正见 [backend.md](backend.md)。本轮未执行整个仓库全量测试，不能将定向通过表述为全量通过。

## 真实环境记录与限制

本轮没有请求生产或 staging、没有付费媒体调用，因此没有新的真实请求 ID 或回执 ID。历史记录中的 ID 不作为本轮线上成功证据；本地隔离测试不能证明线上网关已升级或真实供应商生成成功。若后续做真实视频验收，仍遵守用户限定的 720p、5 秒。

取消总开关不扩展供应商本身的能力，也不替用户启用模型或补全售价。无可用账号、无原生协议、禁用模型或无有效价格仍可能不出现在目录或被调用前拒绝。账号/分组权限、安装鉴权、CNY 数值、余额、限流、参数校验及任务恢复沿用现有实现。同步 TTS 超过 2 MiB 的重放限制等既有媒体恢复边界不在本轮修复范围。

本轮没有数据库结构迁移、数据迁移、余额换算、价格迁移或用户配置写入。发布、tag、GitHub Release、服务器升级与真实线上验收由主发布流程另行完成。

## 回滚

仅需停止某类媒体的新调用时，在 DoneBot 管理页关闭对应模型或收窄渠道/分组模型权限；保留已接受任务记录、查询和恢复数据。旧 `DONEBOT_FEATURES_VIDEO/AUDIO=false` 不能作为当前版本的停用手段，`DONEBOT_ENABLED=false` 也不能替代安装吊销和各原生路由的权限控制。

sh/systemd 部署回滚整个行为时，使用安装器现有 `rollback <修改前版本>` 命令或恢复升级前保留的完整二进制（包含匹配的嵌入前端），再重启 `sub2api`。Compose 部署重新指定升级前镜像并重建应用服务。没有本轮数据库 down 脚本；不要删除 PostgreSQL、Redis/AOF、任务映射、安装记录或资产卷。回到旧版本后，旧媒体总开关会重新生效，需按该版本的配置管理可用性。
