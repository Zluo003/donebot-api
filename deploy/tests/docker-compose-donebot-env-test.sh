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
    "ASSET_STORAGE_DRIVER": "local",
    "ASSET_STORAGE_PUBLIC_BASE_URL": "",
    "IMAGE_STORAGE_ENABLED": "true",
    "IMAGE_STORAGE_DRIVER": "local",
    "IMAGE_TASK_TTL_HOURS": "24",
}
obsolete = ("DONEBOT_FEATURES_VIDEO", "DONEBOT_FEATURES_AUDIO")
sample = dict(line.split("=", 1) for line in Path("deploy/.env.example").read_text().splitlines()
              if line and not line.startswith("#") and "=" in line)
for key, value in expected.items():
    assert sample.get(key) == value, f"sample is missing DoneBot default {key}={value}"
for key in obsolete:
    assert key not in sample, f"sample still declares obsolete media switch {key}"
for filename in ("docker-compose.yml", "docker-compose.local.yml", "docker-compose.standalone.yml", "docker-compose.dev.yml"):
    path = Path("deploy") / filename
    for overrides in ({}, {"DONEBOT_ENABLED": "false", "DONEBOT_FEATURES_VIDEO": "false", "DONEBOT_FEATURES_AUDIO": "false",
                           "ASSET_STORAGE_PUBLIC_BASE_URL": "https://gateway.example.com"}):
        env = dict(os.environ, POSTGRES_PASSWORD="test-password", DATABASE_HOST="postgres",
                   DATABASE_PASSWORD="test-password", REDIS_HOST="redis")
        for key in (*expected, *obsolete):
            env.pop(key, None)
        env.update(overrides)
        result = subprocess.run(["docker", "compose", "--env-file", "/dev/null", "-f", str(path),
                                 "config", "--format", "json"], env=env, check=True, capture_output=True, text=True)
        app = json.loads(result.stdout)["services"]["sub2api"]
        if "dev" not in filename:
            assert app["image"] == "ghcr.io/zluo003/donebot-api:latest", path
        for key, value in expected.items():
            assert app["environment"].get(key) == overrides.get(key, value), f"{path}: {key}"
        for key in obsolete:
            assert key not in app["environment"], f"{path}: still forwards obsolete media switch {key}"
for path in (Path("deploy/sub2api.service"), Path("deploy/install.sh")):
    text = path.read_text()
    assert "Environment=DONEBOT_ENABLED=true" in text, path
    assert "Environment=IMAGE_STORAGE_DRIVER=local" in text, path
    assert "Environment=IMAGE_STORAGE_ENABLED=true" in text, path
    assert "EnvironmentFile=-/etc/sub2api/environment" in text, path
    for key in obsolete:
        assert key not in text, f"{path}: still installs obsolete media switch {key}"
donebot_config = Path("deploy/config.example.yaml").read_text().split("donebot:\n", 1)[1].split("\nserver:", 1)[0]
assert "features:" not in donebot_config, "DoneBot YAML still declares media runtime switches"
print("DoneBot Compose defaults, legacy env isolation, sh/systemd and YAML deployment checks passed")
PY
