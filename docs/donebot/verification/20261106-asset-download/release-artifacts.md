# DoneBot API v0.4.12 公开附件验收

公开发布：https://github.com/Zluo003/donebot-api/releases/tag/v0.4.12
源码提交：`eb056dfda5934f19ebe29cd50a6099c1c94e5582`
发布时间：`2026-11-06T04:15:56Z`
临时证据目录：`/var/folders/wf/gs6jc_rd4qd_6_1jmqjkg1jh0000gn/T/donebot-v0.4.12-artifacts-nagwa2me`

独立使用公开下载 URL 获取 checksums.txt 与两个 Linux 压缩包。所有下载 SHA256 同时匹配公开 checksums.txt 和 GitHub Release asset digest；未运行 Linux 二进制。

| 架构 | ELF e_machine | 压缩包 SHA256 |
|---|---:|---|
| linux/amd64 | 62 | `0044bf4985ec7401f6107164767b5b2eef2758481e4ef6ac4f0eea9db7e24db8` |
| linux/arm64 | 183 | `9605ca602e2f03aabf24b8c226a972e16450e666878fe51d6234e6bfb222093d` |

两份 ELF 均为 64-bit little-endian。Go buildinfo 均确认 `go1.27.3`、`GOOS=linux`、对应 `GOARCH`、`CGO_ENABLED=0`、`-tags=embed`、`vcs.modified=false`，`vcs.revision` 与上列源码一致，ldflags 中 `main.Commit` 相同且 `main.BuildType=release`。二进制包含嵌入版本 `0.4.12\n`。

| 依赖 | amd64 | arm64 |
|---|---|---|
| `github.com/andybalholm/brotli` | `v1.2.7` | `v1.2.7` |
| `github.com/klauspost/compress` | `v1.20.2` | `v1.20.2` |
| `golang.org/x/net` | `v0.62.0` | `v0.62.0` |
| `golang.org/x/crypto` | `v0.58.0` | `v0.58.0` |
| `google.golang.org/grpc` | `v1.83.2` | `v1.83.2` |
| `google.golang.org/protobuf` | `v1.36.11` | `v1.36.11` |

完整 buildinfo：`/var/folders/wf/gs6jc_rd4qd_6_1jmqjkg1jh0000gn/T/donebot-v0.4.12-artifacts-nagwa2me/buildinfo-amd64.txt`、`/var/folders/wf/gs6jc_rd4qd_6_1jmqjkg1jh0000gn/T/donebot-v0.4.12-artifacts-nagwa2me/buildinfo-arm64.txt`。
结构化证据：`/var/folders/wf/gs6jc_rd4qd_6_1jmqjkg1jh0000gn/T/donebot-v0.4.12-artifacts-nagwa2me/verification.json`。

这项验收证明公开附件的完整性、架构及构建来源，不代表生产部署或供应商调用验收。
