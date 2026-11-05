# DoneBot API 0.4.8 发布核查

源码修复提交：`59b3a34002eab55019fa3e1a6fa9b85890b9de58`；发布标签 `v0.4.8`。

[发布页](https://github.com/Zluo003/donebot-api/releases/tag/v0.4.8)；[发布构建](https://github.com/Zluo003/donebot-api/actions/runs/41957704137)。安装器兼容的 Linux amd64、arm64 压缩包与双架构 GHCR 镜像均由现有完整发布流程生成。

主 agent 使用 `gh run download 41957704137 --pattern 'release-binary-*'` 下载两套构建及来源 manifest，独立计算 archive SHA256，并核对版本 `0.4.8`、完整修复提交、ELF64 machine（amd64=62、arm64=183）和二进制内的提交标识。再下载公开的 `checksums.txt`，确认两个哈希与独立计算完全一致；GitHub Release asset digest 亦相同。记录见 [artifacts.json](artifacts.json)。没有在 macOS 上运行 Linux 二进制，未把静态核查写作服务启动验收。

客户端本轮未修改或重打包。现有客户端 session 目录同步测试 9 项、账户模型分组/目录解析/契约 45 项全部通过，涵盖新旧 ID 替换、304、scope 缩减及五类展示。仅按现有目录展示，没有加入本地可售型号。

## 全量 CI

标签对应的 [CI 41957704131](https://github.com/Zluo003/donebot-api/actions/runs/41957704131) 全部成功：完整 Go unit、integration、golangci-lint、前端类型检查及关键 Vitest、部署脚本与发布工具检查。安全扫描亦成功。结构化结果见 [ci-run.json](ci-run.json)、[security-run.json](security-run.json)，完整 CI 日志见 [ci-full.log](ci-full.log)。[release-run.json](release-run.json) 记录发布全流程成功；[published-release.json](published-release.json) 记录公开附件摘要。

## 更新与回滚

在网关服务器，使用原 sh 安装器显式升级：

```sh
sudo bash install.sh upgrade -v v0.4.8
```

如果需要重新下载安装器：

```sh
curl -fsSL https://raw.githubusercontent.com/Zluo003/donebot-api/main/deploy/install.sh -o install.sh
sudo bash install.sh upgrade -v v0.4.8
```

升级完成后，在客户端点击“刷新账户与模型”；应在原有 15 个之外出现四个视频型号（总数 19，以当前后台配置为准）。账号和价格保持原设置，无新增开关。生产部署及升级后真实目录本轮未执行。

回滚可使用 `sudo bash install.sh rollback -v v0.4.6`；账号、价格、任务、回执和附件保留。回滚将恢复互斥分辨率配置下视频目录缺失的旧行为。

## 验证边界

本轮没有新 staging 生成请求 ID 或费用回执，不调用收费视频接口，不证明供应商生成和结算结果。生产只通过既有已登录后台只读核查映射、官方规格、价格、绑定和两账号互斥分辨率，未保存或改动配置。
