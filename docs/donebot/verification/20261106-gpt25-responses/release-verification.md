# DoneBot API v0.4.13 release verification

Source SHA: `0eb2f07f8b246043be21719d8a581b820152995f`.

| Workflow | Ref | Result | URL |
| --- | --- | --- | --- |
| CI | main | Success | https://github.com/Zluo003/donebot-api/actions/runs/42220421046 |
| Security Scan | main | Success | https://github.com/Zluo003/donebot-api/actions/runs/42220420892 |
| CI | v0.4.13 | Success | https://github.com/Zluo003/donebot-api/actions/runs/42221993094 |
| Security Scan | v0.4.13 | Success | https://github.com/Zluo003/donebot-api/actions/runs/42221993079 |
| Release | v0.4.13 | Success | https://github.com/Zluo003/donebot-api/actions/runs/42221993083 |

Every run's head SHA matches the source above. Both CI runs completed backend
unit/integration tests, frontend typecheck and critical Vitest, golangci-lint,
deployment shell checks and release-helper checks. Security scans passed Go
reachable-vulnerability checks and frontend audit/exception validation. This
is not a claim that every dependency has no published advisory.

The release built frontend assets, Linux amd64/arm64 binaries, public archives,
checksums and the dual-platform GHCR image. Independent public-artifact checks
passed; details and hashes are in `release-artifacts.md`,
`release-artifacts.json` and `ghcr-verification.json`.

Published release: https://github.com/Zluo003/donebot-api/releases/tag/v0.4.13

Deployment image: `ghcr.io/zluo003/donebot-api:0.4.13`.

No Linux binary or container was executed during artifact verification. The
production gateway was not deployed or restarted, and no additional paid image
generation was submitted. Transparency and actual pixel dimensions still need
real-output verification after deployment.
