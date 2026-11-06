# DoneBot API v0.4.15 发布核验

核验日期：2026-11-07（Asia/Shanghai）。用户已部署本版本；文生图、单参考图图生图及单图图层拆分的真实产物与收费结果，由主任务另行记录，本页不宣称这些验收已经通过。

## 源码与检查

| 项目 | 结果 |
| --- | --- |
| 发布源码 / 标签 `v0.4.15` | `e8b03704d52ad7e1f65d1258400e7323b5d950c4` |
| [源码 CI 42266815343](https://github.com/Zluo003/donebot-api/actions/runs/42266815343) | 成功；golangci-lint、frontend、shell、test（含集成测试）、release-helpers 全通过 |
| [安全扫描 42266815018](https://github.com/Zluo003/donebot-api/actions/runs/42266815018) | 成功 |
| [发布工作流 42268971398](https://github.com/Zluo003/donebot-api/actions/runs/42268971398) | completed / success |
| main 的 VERSION 同步 | `6f52d4b`，`VERSION` 同步为 `0.4.15`；不改变上述发布源码归属 |

[GitHub Release](https://github.com/Zluo003/donebot-api/releases/tag/v0.4.15)；[发布说明正文](release-notes.md)。

## 下载产物

| 文件 | 架构 | SHA-256 |
| --- | --- | --- |
| `sub2api_0.4.15_linux_amd64.tar.gz` | Linux amd64，ELF machine 62 | `0e492bc5c1da6f38d49f08f594a13a73aa9295f9cc708129afdf65606965c390` |
| `sub2api_0.4.15_linux_arm64.tar.gz` | Linux arm64，ELF machine 183 | `adc6a1c15deaf8b2453f3cf8e4a46ae0c48607ec2c4807d7a1d685ef77c06d60` |

两个压缩包的 SHA-256、GitHub 资产 digest、Actions 产物 manifest 和二进制内嵌源码提交一致。构建信息均为 `BuildType=release`、Linux、CGO disabled，并内嵌发布提交。`vcs.modified=true` 按发布二进制的原始构建元数据保留，源码关联同时由内嵌 Commit、产物 manifest 与镜像 revision 交叉核实。

## 容器镜像

- 镜像：`ghcr.io/zluo003/donebot-api:0.4.15`
- OCI 索引：`sha256:416deeca2de36ca538e1cb82789abab5c9cbf305f64e50e71ee7eca303ab9070`
- Linux amd64 manifest：`sha256:3fe3554fb2389e19651f8b0074cce77b6ccf5c7b391afacf34ae793e22049c68`
- Linux arm64 manifest：`sha256:c5b62735a85bfe0d0bb555891d93e881e20b8e066b8cbde35f58c85a34d5761b`

两个平台镜像标签均记录版本 `0.4.15`、源码仓库及发布提交。索引中的 `unknown/unknown` 条目是 attestation manifest，原始字段保留。

## 证据范围与脱敏

- [汇总核验](verification.json)
- [原始发布工作流元数据](release-run.json)
- [原始 GitHub Release 元数据快照](release.json)
- [原始 OCI 镜像索引](image-manifest.json)
- [amd64 构建信息](buildinfo-amd64.txt) / [arm64 构建信息](buildinfo-arm64.txt)

四份 JSON 按原始字节归档，未修改远端元数据字段。两份构建信息仅将第一行的临时本机文件路径替换为二进制文件名，其余构建字段保持原值。归档前扫描未发现临时凭据、token 或 JSON 内的本机绝对路径；未归档二进制与压缩包。Release 元数据是核验时快照，发布说明正文单独保存在 `release-notes.md`。
