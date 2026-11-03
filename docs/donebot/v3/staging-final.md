# Staging acceptance, 2026-11-04

Environment: https://test.donebot.cn. Deployed source: `7670599`; image: `donebot-api:v3-7670599`. The gateway container was healthy after the restart acceptance. This evidence update does not change the deployed binary.

`staging-media-native.json` contains the full safe request/receipt records, including failed attempts. It stores no credential or signed media URL. Native Responses and Gemini SSE, Chat tool calls, CNY receipt settlement, requested high / effective low, and a real image generation succeeded. The image cost was 25 CNY cents (¥0.25). Replaying its exact operation after restarting the gateway returned `Idempotency-Replayed: true` and the original receipt `d9f62422-d526-4a42-878b-460aec576f92`; the replay request ID was `a82085c0-62ac-428d-9607-c3b0f5ae8b0d`.

A real macOS credential-service and desktop session/bridge check succeeded against Chat: request `eecadeeb-dfb6-4ea0-85b0-088464076d1d`, receipt `ecfd6a7a-a5d6-4e67-9116-fa172267c421`, 0.0084 CNY cents. The isolated installation and native keychain test entries were removed. The final active acceptance installation was revoked with request `af687d53-792a-4a60-bf72-7d68d5ad15d3`, HTTP 204.

Operator metadata was added only for verified capabilities: `deepseek-flash.tool_calling=true`; `gpt-6.1-terra.tool_calling=true` and reasoning control `effort`, levels `low` and `high`, native field `reasoning.effort`. `donebot.model_capabilities.v3` previously had no setting. `api_base_url` was corrected from ` https://donebot.cn` to `https://test.donebot.cn`, because installation origin matching correctly rejects a different claim origin. A temporary group 2 reasoning maximum of `low` was restored to its original empty string; the existing downgrade policy was retained. No pricing, balance or FX setting was changed.

Live limitations: no sellable Anthropic model or currently configured video/music/speech capability was available for this acceptance. No video request was submitted. Future paid video acceptance is limited to 720p and 5 seconds. Four protocols, media adapters, failures and recovery have isolated fixture coverage; those results must not be represented as four-protocol or all-media live provider success. Claude/Gemini CLI compatibility limits and native receipt attribution are documented in the client acceptance report.

Deployment compose working directory is `/home/donebot-api/deploy`, with base `docker-compose.local.yml` and overlay `/home/donebot-api-v3-deploy/7670599/compose.v3.yml`. Rollback image `donebot-api:pre-v3-7c920be` and overlay `/home/donebot-api-v3-deploy/5d0045b/compose.rollback.yml` were retained. To roll back only the gateway, run from that working directory:

```sh
docker compose -f docker-compose.local.yml -f /home/donebot-api-v3-deploy/5d0045b/compose.rollback.yml up -d --no-deps sub2api
```

Preserve PostgreSQL, Redis/AOF, assets and migration 253. Disable new DoneBot media creation before using an old binary that lacks durable operation handling. `DONEBOT_FEATURES_VIDEO=false` and `DONEBOT_FEATURES_AUDIO=false` disable the corresponding capability advertisement. `DONEBOT_ENABLED=false` disables DoneBot management/catalog, but does not revoke previously issued keys or all native routes. Revoke installations through the supported API when required. Keep the corrected staging origin; do not restore the malformed setting.

## Official Codex native acceptance

A real Responses function-tool check succeeded through a disposable native keychain session and bridge: request header IDs `154d45b8-7cc8-42c6-9300-54d3cdcbc00b` and `fcb62a9b-d7e9-4b94-b418-57302b59a4e6`, receipt `6c78a565-cfae-42e9-9574-193ff8aad733`, actual 0.158585 CNY cents. Only after this evidence was `gpt-6.1-terra.tool_calling` set to true in the operator capability metadata.

The official Codex CLI 0.162.1 then completed a turn through the real client driver, a fresh native credential service session, the scoped catalog and loopback bridge: request `fccf271d-434d-4e87-a39c-fd8fb7f98707`, receipt `f607c6b0-cf0a-4121-8829-a883163d1d29`, actual 0.203952 CNY cents, requested/effective `low`. This verifies a native Codex text turn; it does not establish automatic receipt attribution by the CLI integration or a live Codex tool-result round trip. The key was not present in any temporary profile file, both installations were revoked, all disposable keychain entries were deleted and profiles removed. Safe records include the revoke request IDs.
