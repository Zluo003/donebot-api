# GPT Image 2.5 Responses routing verification

## Observed problem and comparison

Six previously authorized DoneBot client live tests completed, but both transparent
PNG edits returned RGB images without alpha. All six outputs were downloaded; no
new production request or paid retry was made during this gateway investigation.

Compared DoneBot API baseline `5e05a26812a6d87a0327d11faf1bf61361334426`
with the user's working reference, Yingzo API
`6c4bf565daa40f4ec298dbb9f346a9fc9573082d`.

The JSON parser, defaults, Responses request builder, image-tool normalization,
and asynchronous request-context cloning match. Both paths retain
`background`, `output_format`, `size` and `quality`. Generated-asset storage
writes received image bytes without reencoding.

The routing differs: DoneBot's custom Direct Images adapter was introduced in
`5e325d6` and selected `/backend-api/codex/images/generations` or `/images/edits`
for GPT Image 2.5 OAuth/setup-token requests. Yingzo selects
`/backend-api/codex/responses`, forces the image-generation tool and puts image
parameters in that tool. DoneBot's direct fallback only handles HTTP 404/405;
a successful response containing an opaque image cannot trigger that fallback.
Source comparison identifies a routing discrepancy, not proof of the production
supplier's exact request or the sole cause of the previous output defects.

## Change

`gpt-image-2.5-flare`, `gpt-image-2.5-sunburst` and their `2026-09-08` snapshots
now use Responses on the first OAuth/setup-token request, including the admin
account preview. The adapter preserves the mapped image model and passes
`background=transparent`, `output_format=png`, dimensions, quality and edit
reference URLs to the image-generation tool. Image edits retain `action=edit`.

GPT Image 1.5/2 Direct Images support, API-key passthrough, client async endpoints,
prepaid billing/refunds, task recovery and generated-file storage are unchanged.
There is no retry after an HTTP 200 opaque result. The Responses driver-model
selection is unchanged; deployments can use the existing
`SUB2API_IMAGES_MAIN_MODEL` setting if required by their upstream account.

## Isolated verification

All upstream calls in these tests use in-memory stubs and synthetic PNGs, with
no real credentials, production database or user data.

- Red proof before the production change: all 40 forwarding scenarios failed
  on the first endpoint assertion because Direct Images was selected.
- Green: 50 scenarios across forwarding and admin account preview pass. They
  cover both model aliases and snapshots, OAuth/setup-token accounts,
  generation/edit, sync/stream responses, and channel/account model mappings.
- Forwarding assertions inspect the first outbound Responses request, forced
  tool choice, exact mapped image model, background/format/dimensions/quality,
  edit action and reference URL. Exactly one upstream call is made.
- Returned synthetic PNG bytes are identical and retain transparent/opaque
  pixels. Admin preview SSE also retains the returned image bytes.
- Existing Direct Images tests still exercise GPT Image 1.5/2.

Commands (working directory `backend`):

```sh
go test ./internal/service -run '^TestGPTImage25' -count=1 -json
go test ./internal/service ./internal/handler ./internal/repository ./internal/server/middleware -run '(?i)(image|gptimage25)' -count=1 -json
go test -tags unit ./internal/service -run 'TestGPTImage25|TestCodexDirectImages|TestCodexImagesLuna|TestOpenAI.*Image|TestImagesOAuth|TestForwardImages|TestImageTask|TestDoneBotMedia' -count=1
```

All passed. The four-package image regression passed 351 top-level tests and
638 leaf cases. `gofmt` and `git diff --check` passed. Full CI, security and
release evidence is in `release-verification.md`; public archive and container
verification is in `release-artifacts.md`.

## Deployment boundary

Release [v0.4.13](https://github.com/Zluo003/donebot-api/releases/tag/v0.4.13)
is published and its public artifacts are verified. It must be deployed and the
gateway restarted before new tasks use this route. This change does not regenerate historical results. No live request
has yet verified transparency or dimensions on the new deployed route; real
outputs must be decoded to check alpha and actual size before those tests can
be considered passed. Reference Yingzo API was read-only throughout.
