# DoneBot API v0.4.13 公开附件验收

公开发布：https://github.com/Zluo003/donebot-api/releases/tag/v0.4.13
源码提交：`0eb2f07f8b246043be21719d8a581b820152995f`
发布时间：`2026-11-06T13:07:55Z`

独立使用公开下载 URL 获取 checksums.txt 与两个 Linux 压缩包。所有下载 SHA256 同时匹配公开 checksums.txt 和 GitHub Release asset digest；未运行 Linux 二进制。

| 架构 | ELF e_machine | 压缩包 SHA256 |
|---|---:|---|
| linux/amd64 | 62 | `4620e850f74eb09b53250aec1b829dbbc8a81d8544ed807cf9bf75a213751841` |
| linux/arm64 | 183 | `2fc41cbb26967d0c4051cdda2e0ca1fcd021caaee9206e90756650d4ba816bbf` |

两份 ELF 均为 64-bit little-endian。Go buildinfo 均确认 `go1.27.3`、`GOOS=linux`、对应 `GOARCH`、`CGO_ENABLED=0`、`-tags=embed`、`vcs.modified=false`，`vcs.revision` 与上列源码一致，ldflags 中 `main.Commit` 相同且 `main.BuildType=release`。二进制包含嵌入版本 `0.4.13\n`。

| 依赖 | amd64 | arm64 |
|---|---|---|
| `github.com/andybalholm/brotli` | `v1.2.7` | `v1.2.7` |
| `github.com/klauspost/compress` | `v1.20.2` | `v1.20.2` |
| `golang.org/x/net` | `v0.62.0` | `v0.62.0` |
| `golang.org/x/crypto` | `v0.58.0` | `v0.58.0` |
| `google.golang.org/grpc` | `v1.83.2` | `v1.83.2` |
| `google.golang.org/protobuf` | `v1.36.11` | `v1.36.11` |

完整 buildinfo：`buildinfo-amd64.txt`、`buildinfo-arm64.txt`。
结构化证据：`release-artifacts.json`。

这项验收证明公开附件的完整性、架构及构建来源，不代表生产部署或供应商调用验收。

## 发布工作流和 GHCR 镜像

发布工作流：https://github.com/Zluo003/donebot-api/actions/runs/42221993083，`headSha=0eb2f07f8b246043be21719d8a581b820152995f`，全部 job 成功。两架构构建、镜像发布和附件发布均成功。

只读查询 `ghcr.io/zluo003/donebot-api:0.4.13`，公开 OCI image index 为 `sha256:231593c6a4b21c48e30294c48a64e807cd14058c614ff1c052ab9b168640dca4`。

| 平台 | Manifest digest |
|---|---|
| `linux/amd64` | `sha256:3378ff725e8350613b4954386e67715ea5a05a1830070bac21c694afd069e793` |
| `linux/arm64` | `sha256:dd4d5e013473f7a1a3cc56c29c2da2a51d8ac77c414dd8374bb28c74f99b2d72` |

另外两条 unknown/unknown 为 Docker attestation manifest。两架构 config 标签均为 version `0.4.13`、revision `0eb2f07f8b246043be21719d8a581b820152995f`、source `https://github.com/Zluo003/donebot-api`。未下载镜像层或运行容器。

证据：`/tmp/donebot-v0.4.13-release-workflow.json`、`/tmp/donebot-v0.4.13-release-job.log`、`/tmp/donebot-v0.4.13-ghcr-index.json`、`/tmp/donebot-v0.4.13-ghcr-inspect.txt`、`/tmp/donebot-v0.4.13-ghcr-configs.json`、`ghcr-verification.json`。
