# 网关媒体补充阶段

2026-11-04。媒体保持原生 JSON/二进制接口，不新增文本 BFF 或报价接口。

## 持久化与幂等

DoneBot 安装凭据提交目录声明的原生媒体操作时，必须发送 `Idempotency-Key`。在转发之前，PostgreSQL 的 `donebot_media_operations` 保存 UUID 回执、安装归属、操作键哈希和规范请求指纹；不保存提示词、参考素材请求或凭据。JSON 大整数不经浮点丢精度；multipart 忽略随机 boundary，同时保留同名参考文件顺序。

同键同参返回已保存的安全原生响应，并含 `Idempotency-Replayed:true`。不同参数返回 409。正在转发、崩溃或上游接受结果未知时，返回 409 并给出相同回执 ID；不得自动重新生成。无上游幂等保证时不能承诺 exactly-once。原始提交响应最多保存 2 MiB，超限保留未知结果并阻止重发；429 保存 Retry-After。

媒体回执与文本共用 `/api/v1/donebot/receipts/:id`。任务映射使用已有稳定计费事件 ID，实际金额只读现有 usage_logs；不创建第二账本。估算仅在目录价格、输入单位可确定时提供，未知为 null。CNY 数值直接使用现有人民币价卡，元转分乘 100；不改用户已经确认的后台计价设置。

接受的异步任务持续使用同一 APIKeyID/安装归属，Key 轮换不丢任务。原生查询同步更新公开回执状态，已有结算也能证明完成。模型下架后，已接受任务仍可查询；新提交按实时目录拒绝。取消、失败未有实际结算时 actual_cents=null，不虚构已退款。

## 原生媒体入口

- 图片：`/v1/images/generations`，APIMart 原生任务查询 `/v1/tasks/:id`；同步图片保留 OpenAI 原生结构。
- 视频：`/v1/videos` 对实际 Seedance 能力做路径适配，保留 `content[]` 等原生请求；查询 `/v1/videos/:id`，下载 `/v1/videos/:id/content`。
- 音乐：`/v1/music/generations`、`/v1/music/tasks/:id`。
- 配音：`/v1/audio/speech`，同步音频。

目录新增固定 adapter 白名单以及已验证的尺寸、比例、分辨率、时长、音色、格式和音乐版本；操作员配置只能收窄。客户端不能执行目录注入的任意 URL/路径/selector。未证明的参数不显示。参数省略时，原生默认值也必须处于目录允许范围；未知默认要求显式选择。

当前目录未声明 edits/extensions 操作，因此 DoneBot 新请求拒绝这些操作。旧异步图片包装不能在服务端重启后重放内部 goroutine，不作为可恢复 adapter 开放。DoneBot 拒绝旧媒体别名创建入口，个人 Key 保留原路径。

视频内容仅在确认持久结算后下载；检查所有权，公共目的地安全检查、禁止重定向，不转发安装 Key、上游 Key、Cookie。本地资产直接走所有权和签名读取，不借私网 HTTP 下载。余额耗尽不会阻挡已接受任务的读取。

## 验证、限制与回滚

完整命令和输出见 `gateway-media-test-results.txt`。真实 staging 单独记录；视频付费验收严格限制 720p、5 秒。

限制：未知结果需查询回执或核实上游，不能安全自动重发；失败退款尚无额外资金事件；通用同步图片未知尺寸不声明；超过 2 MiB 的同步音频不支持响应重放。Redis AOF/volume 仍是原生异步任务与后台结算恢复前提。

回滚先关闭 `DONEBOT_FEATURES_VIDEO` / `DONEBOT_FEATURES_AUDIO` 创建能力，再切回保留镜像。253 迁移是新增表，无既有资金或 usage 迁移；有已接受操作时回滚脚本拒绝删除表，以免丢失幂等与回执。旧二进制可保留该表，但其旧原生媒体创建路径不提供新幂等保障，不应继续开放给 DoneBot 客户端。
