# DoneBot 分组保护与模型目录验证

日期：2026-11-07。修改限 DoneBot API 网关，未修改 DoneBot 客户端或 OpenMausBot 底座。

## 行为

- 按持久绑定 ID 保护 DoneBot 分组，重命名或功能停用仍保护。
- 管理 DTO 提供删除保护标志，按钮禁用；HTTP API 返回 409 / DONEBOT_GROUP_PROTECTED。
- repository 的 Delete、DeleteCascade、DeleteCascadeIfEmpty 在事务中校验绑定，并与初始化串行。
- 映射内部目标不独立成为 DoneBot 售价目录；账号映射只公开左侧型号，不猜测白名单型号后缀。

## 只读生产核查

生产界面版本 0.4.17。DoneBot 分组没有 Composite 路由，OpenAI 渠道没有模型映射。账号 #17 在模型白名单中直接声明 gpt-image-2.5-sunburst-zx，因此当前配置确实将它作为独立请求名；需要将账号配置改为公开 Sunburst 名到该供应商名的映射。未保存、删除或修改任何线上设置，没有发起模型生成。

## 隔离验证

- Go service、DTO、HTTP 回归：通过，覆盖配置读取失败、两种管理删除入口、重命名、功能停用、普通同名组和明确错误码。
- PostgreSQL/Redis 独立 Docker 容器中的完整 integration suite：通过，包含绑定分组三条 repository 删除入口、损坏绑定和普通组删除的真实事务验证。
- 前端必需 lint、typecheck、关键 Vitest：30 文件、578 测试通过。
- 分组与 DoneBot 相关前端回归：8 文件、53 测试通过。
- 前端完整生产构建：通过。
- 完整 unit suite 首轮有既有数据库计时测试在并行负载下失败；该测试单独连续运行三次通过，最终完整 unit suite 已通过。

不在用户生产数据库或运行中客户端验证删除，不自动改变账号映射或商业价格。此版本不声称完成异步任务的多上游持久失败转移。
