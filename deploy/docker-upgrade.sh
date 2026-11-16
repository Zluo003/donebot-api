#!/usr/bin/env bash

set -euo pipefail

DONEBOT_COMPOSE_ARGS=()
while (($#)); do
    case "$1" in
        -f|--file)
            if (($# < 2)); then
                printf 'Missing Compose file after %s\n' "$1" >&2
                exit 2
            fi
            DONEBOT_COMPOSE_ARGS+=("$1" "$2")
            shift 2
            ;;
        -h|--help)
            printf 'Usage: %s [-f COMPOSE_FILE]\n' "$0"
            printf 'Pull and recreate the sub2api gateway service from the current directory.\n'
            exit 0
            ;;
        *)
            printf 'Unknown argument: %s\n' "$1" >&2
            printf 'Usage: %s [-f COMPOSE_FILE]\n' "$0" >&2
            exit 2
            ;;
    esac
done

if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
    printf 'Docker Compose v2 is required.\n' >&2
    exit 1
fi

if [[ ! -f .env ]]; then
    printf 'Run this script from the DoneBot API deployment directory containing .env.\n' >&2
    exit 1
fi

if ! DONEBOT_COMPOSE_SERVICES="$(docker compose "${DONEBOT_COMPOSE_ARGS[@]}" config --services 2>/dev/null)"; then
    printf 'Could not load the Compose configuration from the current directory.\n' >&2
    exit 1
fi

if ! grep -Fxq 'sub2api' <<<"$DONEBOT_COMPOSE_SERVICES"; then
    printf 'The selected Compose configuration does not define the sub2api service.\n' >&2
    exit 1
fi

printf 'Pulling the DoneBot API gateway image...\n'
docker compose "${DONEBOT_COMPOSE_ARGS[@]}" pull sub2api

printf 'Recreating the gateway service...\n'
docker compose "${DONEBOT_COMPOSE_ARGS[@]}" up -d --no-deps sub2api

docker compose "${DONEBOT_COMPOSE_ARGS[@]}" ps sub2api
printf 'DoneBot API gateway upgrade completed. Data volumes and dependency services were not changed.\n'
