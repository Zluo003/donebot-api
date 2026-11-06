# DoneBot API v0.4.14 发布验收

源码与 annotated tag 固定为 `818e6417d941b73e54c9dc8901cb849bb0efbc7b`。
[GitHub Release](https://github.com/Zluo003/donebot-api/releases/tag/v0.4.14)
已公开，[发布工作流](https://github.com/Zluo003/donebot-api/actions/runs/42256208083)
完整成功，包含前端、Linux amd64/arm64 二进制、镜像发布和 VERSION 同步。
main 的 VERSION 同步提交为 `14a4f74`，没有移动发布标签。

[源码 CI](https://github.com/Zluo003/donebot-api/actions/runs/42253886430)
通过全部 unit、integration、golangci-lint、前端类型与关键 Vitest、脚本和发布工具检查；
[安全扫描](https://github.com/Zluo003/donebot-api/actions/runs/42253886364) 通过。
数据库集成测试包括 GIF 预扣费、失败退款两条独立账务记录及退款幂等。
标签再次触发的 [CI](https://github.com/Zluo003/donebot-api/actions/runs/42256208289)
与 [安全扫描](https://github.com/Zluo003/donebot-api/actions/runs/42256208084) 也全部通过。

独立下载公开压缩包、checksums.txt、Actions 原始压缩包与来源 manifest。
两种来源的压缩包逐字节一致，独立 SHA256 同时匹配公开校验文件、GitHub asset digest
和构建 manifest。ELF 架构分别为 amd64=62、arm64=183，Go buildinfo 的
`vcs.revision` 和 `main.Commit` 均为上述来源 SHA，构建类型 release，Go 1.27.3。
buildinfo 的 `vcs.modified=true` 对应发布工作流写入 VERSION 和生成嵌入前端资源；
公开包直接复用了已经独立核对的 Actions 构建包，没有在发布步骤重新编译。
未在 macOS 执行 Linux 程序。

| 压缩包 | SHA256 |
| --- | --- |
| `sub2api_0.4.14_linux_amd64.tar.gz` | `b0e990110f6ef2a1fbac64b66331b58d986158b1fdd2f139e766f67c39f0455c` |
| `sub2api_0.4.14_linux_arm64.tar.gz` | `8af01baaa4dc6ba40255c5122b68dc522472b76fff8558919864dfdd80e1972f` |

只读查询 GHCR Registry API 并独立核对内容 digest：

- 镜像：`ghcr.io/zluo003/donebot-api:0.4.14`
- 多架构 index：`sha256:15d088296a18b03ab81cfb0ae581a2b30be7726a4504d0ccb6c82ec9cac966c6`
- index 包含 linux/amd64 和 linux/arm64，两者分别与架构标签指向相同 image manifest。
- 两种镜像 config 的 version=0.4.14、revision=上述源码 SHA，source=本仓库。

结构化证据见 [verification.json](verification.json)、[workflows.json](workflows.json)、
[image-manifest.json](image-manifest.json)、[amd64 buildinfo](buildinfo-amd64.txt) 和
[arm64 buildinfo](buildinfo-arm64.txt)。完整发布说明见 [release-notes.md](release-notes.md)。

部署后需要为两个模型配置实际上游映射、账号授权和每个输出文件价格。
GIF 保留既有按文件预扣费/失败退款，未引入按帧额外计费；供应商需要支持 GIF 预览。
当前 GIF 只调度原生 Images API Key 账号，OAuth 静态图能力保留。
本次未部署生产、修改生产目录或价格、调用付费上游，也未生成桌面安装包；
离线与数据库测试不能代替真实供应商生成验收。
