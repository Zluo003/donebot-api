# DoneBot API v0.4.12 CI evidence

Source SHA: `eb056dfda5934f19ebe29cd50a6099c1c94e5582`.

| Workflow | Ref | Result | URL |
| --- | --- | --- | --- |
| CI | main | Success | https://github.com/Zluo003/donebot-api/actions/runs/42160877365 |
| CI | v0.4.12 | Success | https://github.com/Zluo003/donebot-api/actions/runs/42160877688 |
| Security Scan | main | Success | https://github.com/Zluo003/donebot-api/actions/runs/42160877397 |
| Security Scan | v0.4.12 | Success | https://github.com/Zluo003/donebot-api/actions/runs/42160877696 |

Both CI runs completed all five jobs successfully: backend unit and integration tests, golangci-lint, frontend typecheck and critical Vitest, deployment shell checks, and release helpers. Both security runs passed backend govulncheck and frontend audit/exception checks.

All four run head SHAs were checked against the source SHA above. No CI or Security failures occurred for this source. This report covers CI and Security only; release artifact verification and production deployment are separate.

The previous v0.4.11 source `838363051f635db3e1ea7d49a73de2d940dbf1f2` passed both CI runs but failed both Security runs for reachable brotli/compress vulnerabilities. Its separate report is `/tmp/donebot-v0.4.11-ci-report.md`; it is not treated as a successful security release.

发布流水线：[42160877651](https://github.com/Zluo003/donebot-api/actions/runs/42160877651)，success；公开版本 [v0.4.12](https://github.com/Zluo003/donebot-api/releases/tag/v0.4.12)。

未部署生产网关，未宣称真实任务已恢复。公开附件验收见 release-artifacts.md，客户端验收见 client-verification.md。
