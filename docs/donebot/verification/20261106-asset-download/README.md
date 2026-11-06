# 本地资产下载与存储驱动隔离

日期：2026-11-06。发布版本：`v0.4.11`。

## 现场事实

只读查看 DoneBot 本地媒体任务记录：新 GPT 图片任务
`imgtask_4c4fb96ff59c4e4eac55a873429b2427` 已进入 `downloading`，实际诊断为
`Media artifact has an unsupported content type.`。旧 GPT 图片任务记录同样诊断。
该错误在客户端校验 HTTP 响应 MIME 时抛出，先于本地目录创建和文件写入。

已登录网关管理页只读查询新任务为 `completed`。只读查看当前生成存储配置为本地、
公网地址 `https://donebot.cn`、根目录 `data/assets/generated`；未保存或修改生产设置。

使用非真实资产 ID、无签名的只读 GET 检查公网路由：

```text
GET https://donebot.cn/assets/ast_00000000000000000000000000000000
Accept: image/*
HTTP 200
Content-Type: text/html; charset=utf-8
Body prefix: <!doctype html>
```

请求被嵌入式前端 SPA fallback 截获，未到签名资产处理器；真实处理器应拒绝无签名。
这证实存在返回 HTML 的下载路径缺陷，不是将目录权限错误改写为成功的图片响应。
没有连接生产服务器检查文件权限，不能据此证明全部磁盘目录健康。

## 修复

- 对 `/assets/ast_<32 位小写十六进制>` 精确绕过前端，进入原资产处理器。
  JS/CSS 静态文件、SPA 导航、签名校验、有效期与归属检查保留。
- 本地地址 `local_public_base_url` 与 S3/R2 地址 `s3.public_base_url` 独立保存。
  顶层 `public_base_url` 保留为当前驱动的兼容镜像；显式空 S3 地址仍允许预签名。
  旧配置只将旧共用地址归入其保存时的驱动，不猜测域名归属。
- 后台切换方式时恢复各自的地址，本地根目录与 S3/R2 对象前缀保持独立。
  旧 API 请求与旧资产存储快照继续兼容，不自动迁移文件或重写历史任务。
- 本地存储运行时不解密闲置的 S3/R2 密钥；配置继续保留加密后的密钥，管理读取仍脱敏。

`data/assets/generated/generated` 来源于本地根目录 `data/assets/generated`
加对象键 `generated/<hash-prefix>/<hash>.<ext>`；Save/Open 使用同一拼接方式，
与 R2 prefix 无关。本轮不更改现有键或搬移文件。

## 隔离验证

嵌入前端的完整 web 测试覆盖两种中间件、真实 AssetHandler 和 AssetService，
正常签名返回 PNG，缺签、伪签、过期签名、过期资产、签名绑定错误和已删除资产
均拒绝，同时覆盖 JS/CSS 内容及缓存策略和 SPA fallback。临时 Go overlay 恢复
旧逻辑时，同一测试稳定复现期望 image/png、实际 text/html；overlay 已移除。

```sh
cd backend
go test -tags embed ./internal/web -count=1
```

存储服务回归覆盖跨驱动切换、保存后重新加载、显式空 S3 公网地址、旧配置/旧 PUT、
密钥脱敏与保留、闲置密钥不可解时本地上传与旧快照可用，以及旧快照原驱动/路径/地址读取。

```sh
go test -tags unit ./internal/service ./internal/repository -run 'TestDoneBotStorage|TestAsset' -count=1
```

前端 StoragePanel 与管理 API 共 16 项回归、语言键完整性 3 项、ESLint、类型检查
和生产构建通过，覆盖先 R2 再本地、来回切换、保存并重新打开，以及预签名链接。
默认构建及 `embed` 构建的 web 静态检查通过；嵌入代码的 Buffer 写入显式处理
其始终为 nil 的错误返回，不改变 HTML 内容。

## 部署边界

部署并重启新版网关后，客户端下载已有本地资产的后台重试可使用修复的路由。
不需要重新生成或提交新的付费任务。此处不承诺已经过期、被清理或此前存储配置
本身错误的旧文件一定可恢复。

切换到新驱动不会自动迁移旧文件；旧文件保留原存储快照。已保存的错误公网域名
不会按名称猜测修正，需要在对应驱动的地址字段中填写实际可访问地址。

## 媒体诊断与状态

用户随后提供的客户端截图只有固定的未确认提示。只读查询本机最新
`gpt-image-2.5-flare` 任务及网关管理页，其任务
`imgtask_6a49f9ec901b456283f3982fb4e49fed` 为 `uncertain`。现场没有取得足以
判断该次上游最终成功或失败的诊断，不能把用户描述或 HTTP 码单独作为退款依据。

客户端任务轮询曾用固定英文覆盖网关的 `error` 和 `http_status`，失败分支也只保留
通用提示。网关回执恢复的 replay sanitizer 另外会将具体 message 替换成固定提示，
且原回执没有诊断字段。两端修复统一使用可选的
`diagnostic: {http_status, code, type, request_id, message}`，保留脱敏、限长的原因，
不把原始供应商正文、URL、凭据或用户输入复制进诊断。

明确尚未进入上游 Forward 的本地错误使用内部可信的未派发标记；普通超时和
连接中断仍保守核对。诊断仅用于解释，不授予重新提交、退款或改变任务归属的权限。
完整详情展示须使用更新后的客户端；存储与下载修复仅部署新版网关即可生效。

定向 service、handler 和 middleware 回归通过，覆盖可信内部标记与 body/header 伪造、
408/499 原生任务轮询到回执仍保留预扣且不退款、可信未派发 503 的幂等退款、具体诊断
进入 Bot 输入、未知任务恢复不增加付费 POST。真实隔离 PostgreSQL 回归验证诊断增量
合并不改写原接受响应、跨账号/安装拒绝、重建服务后回执仍带具体原因、泛化错误不擦除
既有原因，以及原始凭据、供应商 URL 与请求正文不进入诊断。全量 golangci-lint 通过。

```sh
cd backend
go test -tags unit ./internal/service -run 'TestDoneBotMedia|TestOpenAIImages|TestForwardImages|TestImageTask' -count=1
go test -tags integration ./internal/repository -run 'TestDoneBotMediaDatabase' -count=1 -timeout 5m
```
