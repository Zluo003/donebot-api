# DoneBot API 网关

中文 · [English](README.md) · [日本語](README_JA.md)

DoneBot 专用模型网关，实际维护仓库为 **[Zluo003/donebot-api](https://github.com/Zluo003/donebot-api)**。项目基于 sub2api，保留 Anthropic、OpenAI、Gemini 的原生请求与响应，复用现有账号调度、鉴权、余额和用量计费。

## 已有能力

- 自动创建 DoneBot composite 聚合分组，复用模型白名单、渠道价格和分组倍率。
- 登录用户按安装领取凭据，绑定本人、设备和 DoneBot 分组；DoneBot 模式关闭普通 API Key 管理。
- LLM 原生接口、图片同步/异步生成，以及本地或 S3 兼容资产存储（R2、OSS、MinIO）。
- 通过 NewToken 接入 Seedance 2.0 / Fast / Mini / 2.5，支持首帧、首尾帧、参考生视频，以及 2.5 编辑和延长；视频账号有专用面板和分辨率多选。
- 复用用户余额、限流和用量日志。LLM 与 Seedance 按实际 token 计费，图片按张/尺寸计费。

## Linux 安装

需要 Linux `amd64` 或 `arm64`、systemd、Bash 4+，并准备 PostgreSQL 和 Redis。安装脚本从**本仓库**下载 Release，校验 `checksums.txt`，兼容保留 `sub2api` 二进制、系统用户和服务名。

```bash
curl -fsSL https://raw.githubusercontent.com/Zluo003/donebot-api/main/deploy/install.sh | sudo bash
```

安装后启动服务，浏览器访问 `http://服务器地址:8080` 完成设置向导：

```bash
sudo systemctl enable --now sub2api
sudo journalctl -u sub2api -f
```

下载入口：**[GitHub Releases](https://github.com/Zluo003/donebot-api/releases)**。Linux 包名为 `sub2api_<版本>_linux_amd64.tar.gz`、`sub2api_<版本>_linux_arm64.tar.gz`，内含已嵌入管理页面的 `sub2api` 二进制。二进制安装不会代替你部署 PostgreSQL 和 Redis。

配置、升级、手工安装和备份见[部署说明](deploy/README.md)。

## Docker 部署

```bash
mkdir -p donebot-api-deploy && cd donebot-api-deploy
curl -fsSL https://raw.githubusercontent.com/Zluo003/donebot-api/main/deploy/docker-deploy.sh | bash
# 启动前检查生成的 .env 和部署配置。
docker compose up -d
docker compose logs -f sub2api
```

本仓库镜像为 `ghcr.io/zluo003/donebot-api`，已发布标签以[仓库 Packages](https://github.com/Zluo003?tab=packages&repo_name=donebot-api)为准；Linux 二进制包另在 Releases 中提供。Compose 服务名、数据目录和 systemd 服务名保留 `sub2api`，避免破坏已有部署兼容性。

## 启用 DoneBot 模式

本仓库部署模板默认启用 DoneBot 模式。已有部署可将以下配置合并进运行实例的 `config.yaml`，也可使用对应环境变量 `DONEBOT_ENABLED`、`DONEBOT_GROUP_NAME`、`DONEBOT_AUTO_CREATE_GROUP`、`DONEBOT_FEATURES_VIDEO`、`DONEBOT_FEATURES_AUDIO`：

```yaml
donebot:
  enabled: true
  group_name: DoneBot
  auto_create_group: true
  features:
    video: false
    audio: false
```

Docker 修改生成的 `.env`，二进制安装修改 `/etc/sub2api/environment`。使用本地资产与异步图片前，填写 `ASSET_STORAGE_PUBLIC_BASE_URL=https://网关公网域名`，并将 `/assets/*` 反代至网关；修改后重启服务。Docker 也可将完整配置挂载至 `/app/data/config.yaml`。资产存储、图片异步任务配置见[完整配置示例](deploy/config.example.yaml)和[部署/验收记录](docs/donebot/验收记录.md)。

启动后配置上游账号和渠道价格，在 DoneBot 管理页同步并开启模型。登录用户通过 `POST /api/v1/donebot/installations/claim` 领取当前安装的凭据；凭据绑定本人，仅首次领取返回明文。模型调用使用安装凭据，管理员可以吊销安装，不展示明文凭据。

使用 Seedance 时开启 `donebot.features.video` / `DONEBOT_FEATURES_VIDEO=true`，在账号管理点击“添加视频账号”，上游地址使用 `https://api.newtoken.club`，配置允许模型与分辨率。原生端点、`asset://` / 公网 URL 素材、官方分辨率和 token 定价见 [Seedance 接入说明](docs/donebot/Seedance-接入说明.md)。音频没有新增独立生成流程。

## 源码构建

使用 [`backend/go.mod`](backend/go.mod) 声明的 Go 版本、Node.js 24 和 pnpm 9。

```bash
git clone https://github.com/Zluo003/donebot-api.git
cd donebot-api
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend build
cd backend
GOTOOLCHAIN=auto CGO_ENABLED=0 go build -tags=embed -trimpath -o bin/sub2api ./cmd/server
```

`embed` 构建会嵌入 `pnpm build` 生成的前端。准备好数据库、Redis 和配置后运行 `bin/sub2api`。

```bash
# 在 backend/ 执行
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go test -tags=unit -p 1 ./...

# 在 frontend/ 执行
pnpm lint:check
pnpm typecheck
pnpm test:run
```

## 文档与反馈

- [部署说明](deploy/README.md)
- [DoneBot 网关文档](docs/donebot/README.md)
- [Seedance 接入说明](docs/donebot/Seedance-接入说明.md)
- [问题反馈](https://github.com/Zluo003/donebot-api/issues) · [版本发布](https://github.com/Zluo003/donebot-api/releases)

本项目由 **[Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api)** 派生，感谢上游作者及贡献者。保留原有署名与许可，遵循 [GNU LGPL v3](LICENSE)。
