# DoneBot 网关文档

本目录描述 [Zluo003/donebot-api](https://github.com/Zluo003/donebot-api) 的网关实现、配置和验收，只涉及本仓库。网关与客户端通过 HTTP 契约交互。

## 安装与部署

- [项目说明](../../README_CN.md)：能力、Linux 安装、Docker 和源码构建。
- [部署说明](../../deploy/README.md)：运行配置、数据库、Redis、升级和备份。
- [Linux Releases](https://github.com/Zluo003/donebot-api/releases)：`amd64` / `arm64` 二进制及校验和。
- [完整配置示例](../../deploy/config.example.yaml)。

Linux 安装脚本：

```bash
curl -fsSL https://raw.githubusercontent.com/Zluo003/donebot-api/main/deploy/install.sh | sudo bash
```

服务名和二进制名保留 `sub2api` 以兼容部署脚本，仓库、下载和更新来源均为 `Zluo003/donebot-api`。

## 架构与验收

- [DoneBot 网关改造方案](DoneBot-网关改造方案.md)：最初的分阶段施工方案与接口契约；视频、音频范围以之后的 Seedance、APIMart 接入说明为准。
- [P2–P5 验收记录](验收记录.md)：安装凭据、资产存储、异步图片、部署约束和测试证据。
- [Seedance 接入说明](Seedance-接入说明.md)：六种视频模式、NewToken 账号配置、官方分辨率、素材引用、按实际输出秒数计费和验收。
- [APIMart 接入说明](APIMart-接入说明.md)：Seedream Flash、Suno 歌曲/音乐、TTS、账号配置、计费与后台升级。

- [存储管理说明](存储管理说明.md)：上传与生成产物两种独立策略、本地/S3、容量及到期清理。
- [模型定价说明](模型定价说明.md)：LLM、图片、音频、视频的模型最终售价、单位与覆盖规则。

## 当前范围

LLM 使用原生接口，按 token 计费；图片支持同步/异步及按张/尺寸计价；上传资产支持本地或 S3 兼容存储；用户凭据按安装领取，复用现有 API Key 鉴权与余额链路，不提供普通 Key 管理。

DoneBot 聚合分组及管理页已实现，按模型编辑最终售价，复用现有 `Group.ModelPricing`，优先于渠道默认价。视频按实际输出秒数计费，Suno 按生成次数、TTS 按 Unicode 字符计费。音频和视频标签随功能开关显示。Seedance 视频通过账号管理中的专用面板配置，需开启 `DONEBOT_FEATURES_VIDEO=true`；APIMart 图片与音频已有专用账号面板，Suno / TTS 需开启 `DONEBOT_FEATURES_AUDIO=true`。不引入报价单、预授权单或另一套账本。
