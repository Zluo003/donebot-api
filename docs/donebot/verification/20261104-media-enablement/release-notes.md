# DoneBot API v0.4.5

图片、视频、音乐和 TTS 统一按账号能力、模型权限及有效价格开放。取消独立的视频/音频总开关，修复已配置的媒体模型因旧总开关为 false 从客户端目录消失、原生调用被拦截的问题；管理页也移除“调用未启用”提示。

- sh/systemd 和 Compose 部署无需再配置媒体总开关。已有 `DONEBOT_FEATURES_VIDEO=false`、`DONEBOT_FEATURES_AUDIO=false` 或 `donebot.features` 旧值兼容保留，不需要手动清理，升级后不再限制媒体调用。
- 每模型启用状态、账号映射、渠道和分组权限、原生协议、参数限制、有效售价、安装凭据、余额与限流规则继续生效。费用沿用 CNY 数值，不修改用户价格或余额。
- 四个公开 Seedance ID（`seedance-2-0`、`seedance-2-0-fast`、`seedance-2-0-mini`、`seedance-2-5`）的能力适配在客户端完成；网关仅新增既有公开映射、规格和结算回归，没有改写账号或模型配置。
- 无数据库迁移。需要停用媒体时使用现有模型开关或渠道/分组权限；需要回退版本时恢复升级前完整二进制或镜像，并保留数据库、Redis/AOF、资产与已接受任务记录。

本轮验收为本地隔离测试，没有新的真实 staging 请求 ID 或回执 ID。完整文件清单、测试命令、原始日志及限制见 [验收记录](https://github.com/Zluo003/donebot-api/blob/main/docs/donebot/verification/20261104-media-enablement/README.md)。
