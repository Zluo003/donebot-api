# 视频目录账号池分辨率修复验收

基线：`8281bcfe9fde1288494307dabb9532a3cb8b9de4`（`v0.4.7`）。本变更只修复公共模型目录的能力合并，不改价格、账号配置、安装 Key、开关或客户端。

## 根因与实现

本轮由主 agent 在已登录后台只读核对：19 个启用模型中包含 4 个视频模型；四个新下游 ID、官方规格、价格及账号绑定均已配置。可调度账号 A 允许 `720p`，账号 B 允许 `480p / 1080p / 4k`。旧实现跨账号取分辨率交集得到空集，从公共目录隐藏四个模型，客户端因而只收到其余 15 个。

`doneBotPublicMediaParameters` 现对视频分辨率合并取可调度账号池的并集，并按 `480p, 720p, 1080p, 4k` 固定排序。单账号仍先通过官方型号规格、原生适配器、账号限制和 standard 价格校验；无可用分辨率的账号不参与合并。时长及其他参数继续取保守交集，配置元数据仍只能缩窄能力。

调度安全依据：`internal/handler/grok_media.go` 在转发前跳过不支持请求分辨率的账号、释放槽位并排除该账号后重选；该跳过不消耗上游故障切换次数。时长不适配则直接拒绝，故本变更没有扩大时长或模式范围。

## 隔离验证与完整结果

以下均为本地内存 stub、临时文件及 `httptest` 验证，不读取运行中客户端数据或真实 Key，不提交生产变更，也不调用付费媒体接口。每条完整命令、工作目录和退出码保存在对应 `*-command.json`，全部 stdout/stderr 保存在对应 `.log`。

| 记录 | 命令（在 `backend`） | 结果 |
| --- | --- | --- |
| `before` | `go test ./internal/service -run '^TestDoneBotPublicCatalogSeedanceResolutionPool' -count=1 -v` | 基线退出 1，4 个顶层用例失败；生产互斥配置、空权限账号先后顺序、跨规格保守参数及价格过滤均复现目录丢失 |
| `after` | 同上 | 退出 0，4 个顶层用例及 6 个账号排除子用例通过 |
| `service-regression` | `go test -tags=unit ./internal/service -run '^(TestDoneBot\|TestSeedance)' -count=1 -v` | 退出 0，161 个顶层通过；1 个既有 subprocess helper 按设计跳过，0 失败 |
| `handler-resolution` | `go test -tags=unit ./internal/handler -run '^TestSeedanceResolutionAccountAdmission$' -count=1 -v` | 退出 0，1 个顶层、5 个子用例通过，涵盖跳过 1/4/8 个受限账号及全受限零上游请求 |
| `vet` | `go vet -tags=unit ./internal/service ./internal/handler` | 退出 0，无输出 |

新增测试覆盖四个新下游 ID 的正确官方分辨率、每个广告分辨率的可用账号和 standard 售价、原生路径准入、账号顺序不改变目录版本/ETag、移除唯一分辨率账号后版本/ETag 改变、禁用/不可调度/未绑定/无适配器账号排除、空分辨率账号不缩短时长、混合规格保守时长、元数据缩窄及禁止扩大、reference-only 价格不冒充 standard 能力。

## 限制、发布与回滚

本目录只提供合成回归和只读配置核查证据，不宣称已部署生产或生成真实视频。发布包、CI lint 和部署后目录验收由主 agent 的发布阶段补充。本机没有 `golangci-lint`，未把 lint 记为通过。

本修复不需要改动后台配置或部署环境变量。回滚至上一网关二进制即可；账号、价格、任务、回执和存储数据均保持原样。旧版在互斥分辨率账号池下仍会隐藏视频目录，因此回滚会恢复该已知缺陷。
