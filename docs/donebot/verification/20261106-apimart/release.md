# DoneBot API v0.4.10 发布验收

源码提交：`05b942ee63d880040d9afb48e74885971c98f930`。

[GitHub Release](https://github.com/Zluo003/donebot-api/releases/tag/v0.4.10) 已公开，
[发布构建](https://github.com/Zluo003/donebot-api/actions/runs/42105512692) 全流程成功。
公开元数据见 [published-release.json](published-release.json)，构建记录见
[release-run.json](release-run.json)。

独立下载 Linux amd64、arm64 公开压缩包及工作流产物，核对公开校验文件、GitHub
asset digest、来源 manifest 和独立 SHA256 计算，全部一致，两个来源的压缩包逐字节
相同。ELF 架构为 amd64=62、arm64=183；版本为 0.4.10，Go buildinfo 中提交与上述
源码一致、`vcs.modified=false`。未在 macOS 运行 Linux 二进制。证据见
[artifacts.json](artifacts.json) 与 [verification.json](verification.json)。

通过 `docker buildx imagetools inspect` 只读核对公开镜像
`ghcr.io/zluo003/donebot-api:0.4.10`，包含 linux/amd64 和 linux/arm64，证据见
[image-manifest.json](image-manifest.json)。没有启动镜像或修改生产部署。

## 完整 CI

对应源码提交的 [CI](https://github.com/Zluo003/donebot-api/actions/runs/42105512447)
全部成功：完整 Go unit、integration、golangci-lint、前端类型检查和关键 Vitest、
部署脚本与发布工具检查。[安全扫描](https://github.com/Zluo003/donebot-api/actions/runs/42105512445)
亦成功。结构化结果见 [ci-run.json](ci-run.json) 与 [security-run.json](security-run.json)。

## 部署

使用现有 sh 安装器：

```sh
sudo bash install.sh upgrade -v v0.4.10
```

部署并重启后，初始快照重建会刷新媒体能力，无需清空 Redis 或重新创建账号。
后台显示三个独立 APIMart 账号新增入口；已有多能力账号不自动迁移。

生产部署、收费供应商生成和历史产物实际下载本轮未执行；完整边界见
[修复与验证说明](README.md)。历史未确认任务不根据缺失证据自动退款或重新生成。
