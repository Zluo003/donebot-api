# Cloud 管理

启用 DoneBot 后，管理员侧边栏独立显示“Cloud 管理”，不再嵌在模型管理页面内：

| 页面 | 路径 | 功能 |
| --- | --- | --- |
| 概览 | `/admin/cloud/overview` | 在线节点、可分配容量、实际运行实例及异常 |
| 服务器节点 | `/admin/cloud/nodes` | 登记、暂停分配、令牌轮换/撤销、空节点删除 |
| 用户实例 | `/admin/cloud/instances` | 搜索归属、期望与实际状态、错误、付费截止及删除状态 |
| 套餐与保留 | `/admin/cloud/plan` | 月费、开通/续费开关、到期保留天数 |
| 账单 | `/admin/cloud/billing` | 最近 100 笔扣款及退款 |
| 运行策略 | `/admin/cloud/runtime-policy` | 主控失联容错窗口 |

`/admin/cloud` 和旧收藏 `/admin/donebot?tab=cloud` 转向概览。现有管理 API 路径保持 `/api/v1/admin/donebot/cloud/...`，页面均要求管理员身份，敏感写入继续要求二次验证。节点凭据只显示一次，离开节点页即丢弃；已分配节点不能删除，停机和待删除实例也占用容量。

运行策略独立存储在 `donebot_cloud_runtime_policy`，不会被套餐保存覆盖。管理员通过 `GET/PUT /api/v1/admin/donebot/cloud/runtime-policy` 读写 `{"outage_grace_minutes":30}`；范围 1–60 的整数，未配置时默认 30。无新增数据库迁移。

节点正常每 3 秒续租，主控将授权截止时间限制为“当前时间 + 窗口”和付费截止时间的较早值。网络失败、超时、5xx、无效响应不延长也不撤销原有效租约。成功响应移除授权、明确停机或主控 401/403 会触发停机。已运行实例在主控失联期间保持运行资源，但新开通、主控代理和网关请求仍依赖对应服务可达。

失联期间尚未送达的撤销最迟在原租约到期后生效。策略更改在下一次成功续租时生效。节点在线判断仍为 30 秒，命令租约仍为 90 秒。此策略不提供跨节点迁移或自动切换；失联节点可能仍在运行，不能据此在另一台节点启动同一实例。

发布需同步更新主控、DoneBot-Cloud worker 和 agent。旧 worker 会将租约截为 20 秒，旧 agent 会拒绝长租约。维护窗口内先更新节点，再发布主控；按既有停止、恢复与租约流程操作。无需因此替换 DoneBot 运行服务或桌面镜像，保持原节点 token 和主控签名 seed。


## 本地验证

相关前端 Vitest 78 项、生产构建（含类型检查）、新增组件 ESLint 和 Cloud 后端三个包测试通过。Cloud 集成测试使用自建临时 PostgreSQL；worker/agent 的 34 项测试使用合成运行服务与时间推进检查长租约、到期及明确撤销。Playwright 已在无生产请求的隔离页面检查实际侧边栏和六页面切换。未更新生产主控、节点或镜像。

## Cloud 切换缓存与退出恢复

网关浏览器代理允许成功 GET 的带版本 hash `/assets/*.js`、`.css`、`.woff2` 使用浏览器私有长期缓存；HTML、API、附件、带查询参数和错误响应保持 `no-store`。前置代理禁止共享缓存并保留主控 Cache-Control。缓存不改变归属、订阅、Cookie 有效期或撤销验证；新版桌面客户端在 Cookie 失效后通过本机 DoneBot 账号自动续连，不注销手机的独立连接。

网关更新只提供上述静态资源缓存优化。用户运行服务意外退出后的自动拉起位于 DoneBot-Cloud agent/worker，需更新节点代码；避免重复写响应导致进程崩溃的修复位于 DoneBot 服务镜像，需另发新镜像并保留资料升级实际容器。自动续连及消息缓存需新版桌面客户端。仅更新网关不能安装这些节点、镜像或客户端修复，不需要因此更新独立手机中继。

本轮 `TestBrowser` 单元测试及临时 PostgreSQL 的 `TestPostgresBrowserCredentialLifetimeAndStorage` 通过。新增缓存规则覆盖版本资源、HTML/API/附件、查询、错误和 POST；PostgreSQL 检查覆盖原生/网页会话及期限，未进行生产访问或镜像发布。节点部署步骤见相邻 DoneBot-Cloud 的 `docs/deployment.md`。
