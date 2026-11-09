# DoneBot Online Templates

The dedicated admin page is `/admin/donebot/templates` (visible when DoneBot is
enabled). Upload an existing `openmaus.package` v2 JSON file, review the detected
Bot or department, change name/version/summary as needed, then publish. Skills,
SOUL, members and room definitions travel in the same JSON document; separate
ZIP files or arbitrary scripts are not supported by this version.

The current ecommerce directory contains individual role packages and department
packages. Catalog bundles with multiple presets are not single Bot templates.

Published content is immutable by `(template_id, release)`. Use **查看 → 发布新版本**
to reuse a package with a new version, or upload an edited package. A repeated
version returns HTTP 409. Unpublish hides a release from downloads and new online
catalogs; republish restores it. History stays in the dedicated release table.
The user catalog selects the highest published semantic version per template.
Unpublishing the highest version exposes the highest remaining published version.
It is not a remote revocation or deletion of a user's existing local copies.

Admin routes (existing admin authentication; publish/visibility use step-up):

- `GET /api/v1/admin/donebot/templates`
- `POST /api/v1/admin/donebot/templates/preview` with `{ document }`
- `POST /api/v1/admin/donebot/templates` with `{ document }`
- `GET /api/v1/admin/donebot/templates/:template_id/:release`
- `PATCH /api/v1/admin/donebot/templates/:template_id/:release` with `{ published }`

Authenticated user routes:

- `GET /api/v1/donebot/templates` returns `{ templates }`.
- `GET /api/v1/donebot/templates/:template_id/:release` returns `{ template, content }`.

The release's SHA-256 and byte length describe the exact UTF-8 `content` string,
not a JSON reserialization. Client requests use the existing DoneBot session and
fixed gateway origin, independently of paid model traffic. Downloaded packages
are validated again by the original client package parser with file trust.

Migration `256_donebot_templates.sql` creates a dedicated table with immutable
release keys. It runs through the existing migration runner on normal startup.
There is no seed publication or automatic conversion of built-in templates.
The separate manual rollback is `backend/migrations/down/256_donebot_templates.sql`.
Stop all gateway instances before running it. It refuses to remove existing
releases; an empty table can be rolled back and upgraded again on startup.

Verification on 2026-11-09:

```sh
cd backend
go test -tags=unit ./internal/service ./internal/handler ./internal/handler/admin ./internal/server/routes -run DoneBot -count=1
go test -tags=template_integration ./internal/templateintegration -count=1 -v
go build ./cmd/server
cd ../frontend
pnpm exec vitest run src/views/admin/__tests__/DoneBotTemplatesView.spec.ts
pnpm build
```

The integration fixture starts an entirely new native PostgreSQL cluster in a
temporary directory, uses the real table/repository/service/HTTP handlers, tests
concurrent publication, version ordering, history, hash, unpublish and republish,
then shuts it down. It requires `initdb` and `pg_ctl`; it never uses the configured
deployment database. Authentication/step-up inheritance has separate route tests.

For a local UI preview with in-memory demonstration data only:

```sh
cd frontend
node scripts/preview-templates.mjs
```

This mounts the real template page/dialogs in an isolated layout, intercepts all
API calls and prints a loopback URL. Restarting resets its sample publications.
The UI was driven at 1440x1000 and 390x844 with upload, preview and publish; the
mobile document width was 390 pixels. This is not a deployed production service.
