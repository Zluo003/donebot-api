# DoneBot API Gateway

[中文](README_CN.md) · English · [日本語](README_JA.md)

DoneBot's model gateway, maintained at **[Zluo003/donebot-api](https://github.com/Zluo003/donebot-api)**. Built on sub2api, it keeps native Anthropic, OpenAI and Gemini request/response formats while reusing account scheduling, authentication, balances and usage billing.

## What is included

- A DoneBot composite group with account-enabled model catalogs. LLMs use channel prices × a per-model multiplier; images, audio and video use per-model sale prices through the existing group pricing chain.
- Installation credentials bound to the signed-in user and device; ordinary API key management is hidden in DoneBot mode.
- Native LLM APIs, synchronous/asynchronous images, and assets backed by local or S3-compatible storage (R2, OSS, MinIO).
- Seedance 2.0 / Fast / Mini / 2.5 native video tasks through NewToken, including first/last frames, references, and 2.5 editing/extension. Video accounts have a dedicated panel and resolution selection.
- APIMart Seedream 5.0 Flash, Suno song/instrumental generation and TTS, with a dedicated account panel and audio model management.
- Existing user balances, rate limits and usage logs. LLMs use tokens, images use actual image count and size, Seedance uses actual output seconds, Suno uses generations, and TTS uses Unicode characters.

## Linux installation

Requires Linux `amd64` or `arm64`, systemd, Bash 4+, PostgreSQL and Redis. The installer downloads releases from **this repository**, verifies `checksums.txt`, and installs the binary and service under their compatible `sub2api` names.

```bash
curl -fsSL https://raw.githubusercontent.com/Zluo003/donebot-api/main/deploy/install.sh | sudo bash
```

After installation, start the service and complete the web setup wizard at `http://YOUR_SERVER:8080`:

```bash
sudo systemctl enable --now sub2api
sudo journalctl -u sub2api -f
```

Downloads: **[GitHub Releases](https://github.com/Zluo003/donebot-api/releases)**. Linux archives are named `sub2api_<version>_linux_amd64.tar.gz` and `sub2api_<version>_linux_arm64.tar.gz`; each contains the `sub2api` binary with the web UI embedded. Database and Redis services are deployed separately for binary installation.

See [the deployment guide](deploy/README.md) for configuration, upgrades, manual installation and backups.

## Docker deployment

```bash
mkdir -p donebot-api-deploy && cd donebot-api-deploy
curl -fsSL https://raw.githubusercontent.com/Zluo003/donebot-api/main/deploy/docker-deploy.sh | bash
# Review the generated .env and deployment configuration before starting.
docker compose up -d
docker compose logs -f sub2api
```

The container image for this repository is `ghcr.io/zluo003/donebot-api`. Image tags and availability are listed in [the repository's packages](https://github.com/Zluo003?tab=packages&repo_name=donebot-api); Linux binary assets are listed separately in Releases. Compose service names and persistent data paths retain `sub2api` for compatibility.

## Enable DoneBot mode

This repository's deployment templates enable DoneBot mode by default. For existing installations, merge this block into the running instance's `config.yaml`, or set the corresponding environment variables (`DONEBOT_ENABLED`, `DONEBOT_GROUP_NAME`, `DONEBOT_AUTO_CREATE_GROUP`):

```yaml
donebot:
  enabled: true
  group_name: DoneBot
  auto_create_group: true
```

For Docker, edit the generated `.env`; for binary installation, edit `/etc/sub2api/environment`. Set `ASSET_STORAGE_PUBLIC_BASE_URL=https://your-gateway-domain` before using local assets and async images, and proxy `/assets/*` to the gateway. Restart the service after changes. You can also mount a full Docker configuration at `/app/data/config.yaml`. See [the configuration example](deploy/config.example.yaml) and [deployment/acceptance notes](docs/donebot/验收记录.md) for storage and async image settings.

Manage upload and shared generated output storage in DoneBot → Storage; see [storage settings](docs/donebot/存储管理说明.md) for capacity, retention and local/S3 policies.

After startup, configure and enable upstream accounts, bind them to the DoneBot group and enable the permitted models. Synchronize the DoneBot catalog, set each LLM multiplier or media sale price, then save model switches and prices together. Images, video, music and speech use the configured account capabilities and individual model permissions without separate video/audio runtime switches; see [model pricing](docs/donebot/模型定价说明.md). A signed-in user claims an installation through `POST /api/v1/donebot/installations/claim`; the returned credential belongs to that user and installation. It is returned only on first claim. Model requests use this installation credential, and administrators can revoke installations without exposing their credentials.

For Seedance, add a video account with upstream URL `https://api.newtoken.club`, and configure the permitted models and resolutions. Follow [the Seedance integration guide](docs/donebot/Seedance-接入说明.md) for native endpoints, `asset://`/public URL materials, official resolution limits and per-second sale prices. For APIMart, use the dedicated account panel with `https://api.apimart.ai` and enable the Suno/TTS models after configuring prices. Native endpoints and local pricing are documented in [the APIMart guide](docs/donebot/APIMart-接入说明.md). Administrators can use the sidebar version badge to update official binary releases online.

## Build from source

Use the Go version in [`backend/go.mod`](backend/go.mod), Node.js 24 and pnpm 9.

```bash
git clone https://github.com/Zluo003/donebot-api.git
cd donebot-api
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend build
cd backend
GOTOOLCHAIN=auto CGO_ENABLED=0 go build -tags=embed -trimpath -o bin/sub2api ./cmd/server
```

The embedded build includes the frontend output generated by `pnpm build`. Run `bin/sub2api` with PostgreSQL, Redis and your configuration available.

```bash
# From backend/
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go test -tags=unit -p 1 ./...

# From frontend/
pnpm lint:check
pnpm typecheck
pnpm test:run
```

## Documentation and support

- [Deployment guide](deploy/README.md)
- [Storage management](docs/donebot/存储管理说明.md)
- [DoneBot gateway documentation](docs/donebot/README.md)
- [Model pricing](docs/donebot/模型定价说明.md)
- [Seedance integration](docs/donebot/Seedance-接入说明.md)
- [APIMart images and audio](docs/donebot/APIMart-接入说明.md)
- [Issues](https://github.com/Zluo003/donebot-api/issues) · [Releases](https://github.com/Zluo003/donebot-api/releases)

This is a dedicated fork of **[Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api)**. Thanks to the upstream authors and contributors. Original attribution is retained, and this repository is distributed under the [GNU LGPL v3 license](LICENSE).
