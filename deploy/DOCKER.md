# DoneBot API Container Image

The image for this repository is `ghcr.io/zluo003/donebot-api`. It supports `linux/amd64` and `linux/arm64`. Existing container paths, executable names and Compose service names remain `sub2api` for compatibility.

## Deploy with Compose

```bash
mkdir -p donebot-api-deploy && cd donebot-api-deploy
curl -fsSL https://raw.githubusercontent.com/Zluo003/donebot-api/main/deploy/docker-deploy.sh -o docker-deploy.sh
bash docker-deploy.sh
# Review secrets and set ASSET_STORAGE_PUBLIC_BASE_URL before using async images.
nano .env
docker compose up -d
docker compose logs -f sub2api
```

The preparation script downloads this repository's Compose and environment templates and generates local credentials. It writes `COMPOSE_FILE=docker-compose.local.yml` into `.env`, so plain `docker compose` commands keep PostgreSQL, Redis and application data in `./postgres_data`, `./data` and `./redis_data`. The templates enable DoneBot mode, PostgreSQL and Redis persistence, and local asset storage. Configure an upstream-reachable public gateway URL and proxy `/assets/*` before using asynchronous images. Configure media accounts, prices and enabled models in DoneBot; video and audio have no separate runtime switches.

When using the named-volume `docker-compose.yml`, pass `-f docker-compose.yml` explicitly. For an external PostgreSQL/Redis deployment, use `docker-compose.standalone.yml` and supply `DATABASE_HOST`, `DATABASE_PASSWORD` and `REDIS_HOST`. The complete environment reference and operations guide are in [deploy/README.md](README.md).

DoneBot asynchronous media requires Redis AOF and a persistent Redis data directory. The bundled Redis uses `appendfsync everysec`; review its crash-loss window or use `always` when required, and test recovery. See [Redis persistence and retries](README.md#redis-persistence-and-retries). Upload and generated output storage are independent policies in DoneBot → Storage; they can use different local/S3 backends.

## Images and Upgrades

This image contains the gateway only. Mobile relay forwarding runs in the separate `DoneBot-Relay` project and its own container; gateway upgrades do not start a listener on 18080. Deploy and upgrade those nodes independently using [the mobile relay deployment guide](MOBILE_RELAY.md).

```bash
docker pull ghcr.io/zluo003/donebot-api:latest
# Pin a published version for repeatable deployments:
docker pull ghcr.io/zluo003/donebot-api:0.3.0
```

Use a published version tag or digest when repeatability is required. Preserve the application, PostgreSQL and Redis volumes when recreating containers. PostgreSQL readiness checks and the application's bounded database startup retries handle transient recovery; invalid credentials and migration errors still require correction.

## Build from This Checkout

```bash
git clone https://github.com/Zluo003/donebot-api.git
cd donebot-api
docker build -t ghcr.io/zluo003/donebot-api:local .
```

## Links

- [Repository](https://github.com/Zluo003/donebot-api)
- [Releases](https://github.com/Zluo003/donebot-api/releases)
- [Deployment guide](https://github.com/Zluo003/donebot-api/blob/main/deploy/README.md)
