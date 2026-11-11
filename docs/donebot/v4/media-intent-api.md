# 客户端原生媒体请求（替代已移除的 Intent API）

`POST /v1/donebot/media/intent` 已移除。
客户端不能提交语义需求让网关选模型、转换参数或组装请求。对应 planner、handler 和
intent option 的字段映射及默认值生成代码已删除。数据库中旧任务和结算字段保留，
以供已受理任务恢复及账单核对。`GET /v1/donebot/media/jobs/{id}`、
`GET /v1/donebot/media/jobs/by-idempotency/{key}` 及旧产物下载地址只读保留，
防止已有付费任务失去查询和取回结果的入口；不能据此重新开放 Intent 提交。

## 职责

DoneBot 客户端登记媒体模型能力、原生参数、默认值及限制，生成完整请求。
网关基于 sub2api 提供注册登录、鉴权、分组授权、上游账号路由、原生协议转发、
幂等、异步任务持久化、计费和回执。原生字段解析仅用于安全检查和计费，
不替客户端解释自然语言或选择其他媒体模型。

`GET /api/v1/donebot/models` 仅为客户端提供已开放模型、接口和价格等访问资料，
不发布 `media_parameters`、`intent_options`、媒体 fallback 优先级或媒体默认模型。
原有内部协议校验及价格可用性检查保留，不能用其为客户端组装请求。

## 固定客户端模型与异步接口

| 类型 | 客户端 model | 提交 | 查询 |
| --- | --- | --- | --- |
| 图片 | `seedream-5-5` | `POST /v1/images/generations` | `GET /v1/tasks/{task_id}` |
| 视频 | `seedance-2-5` | `POST /v1/videos` | `GET /v1/videos/{task_id}` |
| 音乐 | `suno`，`version=v6` | `POST /v1/music/generations` | `GET /v1/music/tasks/{task_id}` |

视频下载契约为 `GET /v1/videos/{task_id}/content`，接受创建及查询返回的
`dbtask_...` ID。网关按当前用户与 API Key 从持久化 dispatch 读取已完成结果，
再按资源 ID 和 API Key 归属读取已存文件；不依赖上游账号、provider task 或
sticky-session，不重复提交生成或结算。原生上游任务 ID 的旧下载接口继续保留。
查询响应保留 `content.video_url`，客户端无需依赖该字段中的临时签名进行任务下载。
本地已存资源通过归属鉴权直接读取，旧链接签名过期不妨碍仍在保留期内的资源下载；
资源本身过期、已清理或无归属时返回404，不延长资源保留期。
下载成功返回200，支持文件范围请求返回206；处理中返回409及 `Retry-After: 3`，
失败任务返回409且不提示继续等待；不存在或非归属任务返回404。数据库读取失败
返回5xx，不能降级为旧任务绑定查询。

视频账号需将公开 ID `seedance-2-5` 映射至上游 `doubao-seedance-2-5-260628`，
并给公开 ID 配置价格与分组授权；这是同一个模型的账号路由，不能换用其他模型。
图片模型公开 ID 和上游 ID 均为 `seedream-5-5`。

Suno 自定义歌曲及纯音乐的 `duration` 均为可选字段；只有调用方提供时才检查
10–360 秒整数限制，网关不补目标时长。官方契约：
<https://docs.apimart.ai/cn/api-reference/audios/suno/generation>。
在 DoneBot 媒体校验阶段被拒绝、明确未转发的请求不记录上游端点；音乐端点
单独识别为 `/v1/music/generations` 和 `/v1/music/tasks`，不套用文本 Responses 的
日志推导规则。该识别只影响日志归因，不改变原生转发路由。

客户端提交原生 JSON，携带安装身份、Authorization 与 `Idempotency-Key`。
图片返回 `data[0].task_id` 后立即结束提交等待；客户端在后台查询任务、下载产物并
投递原会话。前 30 秒以 3 秒为默认查询间隔，此后 7.5 秒；失败查询单独退避。
任务未决或结果不确定时不得换一个幂等键重复付费提交。上传参考文件仍使用原有
`/v1/assets` 以及归属验证，原生请求中引用上传后返回的 URL。

例如客户端直接提交：

```json
{"model":"seedream-5-5","prompt":"An orange cat sticker","background":"transparent","output_format":"png","size":"2K"}
```

参考编辑同样走 generation 请求的 `image_urls`，图层拆分加
`layer_decomposition:true` 及可选 `layer_decomposition_options`。网关转发客户端的
原生字段，不生成 `options`、不转换语义参数、不调用 LLM 规划。

## Seedream 5.5 计费与配置

官方契约：<https://docs.apimart.ai/cn/api-reference/images/seedream-5-5/generation>。

管理员先配置 APIMart 图片账号并选择 `seedream-5-5`，为模型设置输出 `1K`（含
1.5K）、`2K` 和 `input`（每张输入参考图）价格，再在 DoneBot 聚合分组中开放。
仅更改代码不会部署服务、添加上游密钥、设置真实价格或改变线上开放模型。
旧模型的账号、价格和已受理任务不会自动删除，停用由管理员配置。

输出按每张实际像素计费：<=2,610,000 像素为 1K 档，其余为 2K 档。
每张输入参考图另计费，没有首张免费。图层最大图片数为 2..100，默认30，包含底图；
按请求上限和输出档位预扣，再逐张按实际像素与实际输出数结算，输入费用单独相加。
缺输入价格时，带参考图请求在付费提交前拒绝。缺少有效实际产出尺寸时，
不按请求档位猜测结算，保留任务等待核对。失败和不确定任务沿用已有退款及
核对机制，不绕过账本、不重复预扣。

TTS 保留 `/v1/audio/speech` 的 PCM 流与既有完成回执；它是朗读功能，不改为
音乐任务。`gpt-4o-mini-tts` 的音色与输出格式同样登记在客户端。

## 验证

所有测试使用合成账号、价格和产物，没有真实上游密钥或付费请求：

```sh
cd backend
go test -tags=unit ./internal/service ./internal/handler ./internal/server/routes ./internal/server/middleware -run 'Test(APIMart|DoneBot|GatewayRoutesGroup|PromptAuditRoute)' -count=1
cd ../frontend
pnpm exec vitest run src/components/account/__tests__/ApimartAccountModal.spec.ts src/components/admin/donebot/__tests__/ModelPricingPanel.spec.ts --minWorkers=1 --maxWorkers=1
pnpm exec vue-tsc --noEmit
```

覆盖原生请求透传、5.5参数模式、输入计费、图层实际像素结算、旧入口拒绝、
目录不下发媒体能力，以及管理界面的5.5模型与输入价格保存。
DoneBot 仓库另有隔离 HOME/data/server 的媒体端到端验证。

### 按视频任务 ID 下载回归

2026-11-11：`TestDoneBotDispatchVideoContentSurvivesRestartAndAccountFailover`
使用真实查询 repository、SQL mock 的已完成 dispatch、临时视频文件和独立 HTTP
服务器，验证查询返回 `dbtask_...` 及原生结果，再用该 ID 下载完整文件和范围片段。
在无 sticky-session、含失败账号及最终成功账号记录、重新构建 handler/service 实例、
保存的资源签名已过期时，仍返回200/206；文件自身必须在保留期内。
用 Go overlay 仅移除新增入口分支时，同一用例稳定复现原来的404。

`TestDoneBotDispatchVideoContentEnforcesOwnershipAndState` 覆盖跨用户、跨 Key、
不存在、数据库故障、未完成、失败、非视频协议、资源过期及资源归属缺失。
下载只执行归属限定的 SELECT；测试不允许额外 SQL、模型上游请求或计费变更。
这是隔离的 SQL mock/文件/HTTP 验证，不是线上数据库或实际进程重启验收。

```sh
cd backend
go test -tags=unit ./internal/service ./internal/handler ./internal/repository ./internal/server/routes ./internal/server/middleware -run '^Test(DoneBot|GrokMedia|ForwardGrokMedia|Seedance|Asset|GatewayRoutes)' -count=1
```

本次专项977个测试及子用例通过。DoneBot 客户端原有 `server/donebot.e2e.test.ts`
另有2项隔离端到端通过，覆盖视频提交、后台查询、附件投递与账号隔离。未修改
客户端代码、用户数据或生产服务，未发起真实媒体生成。

## 媒体参考上传容量

客户端登记的 Seedance 2.5 原生视频输入上限为每个 200 MB。网关上传存储的未配置
默认值为 200 MiB；已有显式 asset_storage.max_bytes 和后台保存的上传策略继续生效，
不会自动改写。生产管理员需核对上传策略、gateway.max_body_size 以及反代请求体上限，
并为 multipart 预留额外容量。生成接口的 64 MB 原生 JSON 限制不用于限制 URL 引用
文件的大小总和。此处仅调整存储传输容量，不在网关登记模型能力或组装客户端请求。
