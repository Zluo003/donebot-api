#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"
python3 - <<'PY'
import json
import os
from pathlib import Path
import subprocess

expected = {
    "DONEBOT_ENABLED": "true",
    "DONEBOT_GROUP_NAME": "DoneBot",
    "DONEBOT_AUTO_CREATE_GROUP": "true",
    "DONEBOT_FEATURES_VIDEO": "false",
    "DONEBOT_FEATURES_AUDIO": "false",
    "ASSET_STORAGE_DRIVER": "local",
    "ASSET_STORAGE_PUBLIC_BASE_URL": "",
    "IMAGE_STORAGE_ENABLED": "true",
    "IMAGE_STORAGE_DRIVER": "local",
    "IMAGE_TASK_TTL_HOURS": "24",
}
sample = dict(line.split("=", 1) for line in Path("deploy/.env.example").read_text().splitlines()
              if line and not line.startswith("#") and "=" in line)
for key, value in expected.items():
    assert sample.get(key) == value, f"sample is missing DoneBot default {key}={value}"
for filename in ("docker-compose.yml", "docker-compose.local.yml", "docker-compose.standalone.yml", "docker-compose.dev.yml"):
    path = Path("deploy") / filename
    for overrides in ({}, {"DONEBOT_ENABLED": "false", "DONEBOT_FEATURES_VIDEO": "true",
                           "ASSET_STORAGE_PUBLIC_BASE_URL": "https://gateway.example.com"}):
        env = dict(os.environ, POSTGRES_PASSWORD="test-password", DATABASE_HOST="postgres",
                   DATABASE_PASSWORD="test-password", REDIS_HOST="redis")
        for key in expected:
            env.pop(key, None)
        env.update(overrides)
        result = subprocess.run(["docker", "compose", "--env-file", "/dev/null", "-f", str(path),
                                 "config", "--format", "json"], env=env, check=True, capture_output=True, text=True)
        app = json.loads(result.stdout)["services"]["sub2api"]
        if "dev" not in filename:
            assert app["image"] == "ghcr.io/zluo003/donebot-api:latest", path
        for key, value in expected.items():
            assert app["environment"].get(key) == overrides.get(key, value), f"{path}: {key}"
for path in (Path("deploy/sub2api.service"), Path("deploy/install.sh")):
    text = path.read_text()
    assert "Environment=DONEBOT_ENABLED=true" in text, path
    assert "Environment=IMAGE_STORAGE_DRIVER=local" in text, path
    assert "Environment=IMAGE_STORAGE_ENABLED=true" in text, path
    assert "EnvironmentFile=-/etc/sub2api/environment" in text, path
print("docker compose DoneBot defaults and overrides test passed")
PY
