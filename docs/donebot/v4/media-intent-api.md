# DoneBot Media Intent API

This is the intended DoneBot v4 contract for media generation. It is a DoneBot-only
adapter over the gateway's existing native image, video, and music routes; it does
not replace their provider execution mechanisms. The gateway resolves a request
from its capability catalog and available accounts. Clients submit an intent and
do not choose a provider model, protocol, credential, or native route.

`audio` in this API means asynchronous music generation. Text-to-speech remains
the existing synchronous streaming endpoint and has no media prepayment or refund.

## Submit

`POST /v1/donebot/media/intent`

Required header: `Idempotency-Key` (1-256 visible ASCII characters). The key is
scoped to the authenticated user and installation. Repeating the same key with
the same request returns the same task; reusing it with different content returns
`409 Conflict`.

Request body:

```json
{
  "intent": { "kind": "image", "operation": "generate" },
  "parameters": {
    "prompt": "A studio product photograph of a red ceramic mug",
    "options": { "aspect_ratio": "4:3", "quality": "high" },
    "references": [{ "asset_id": "ast_0123456789abcdef0123456789abcdef", "type": "image" }]
  }
}
```

`kind` is `image`, `video`, or `audio`. Supported operations are image
`generate`/`edit`, video `generate`/`edit`/`extend`, and audio `generate` only.
The body has no provider model, adapter, credential, native protocol field, or
arbitrary URL. `references` contains only asset IDs previously uploaded under the
same authenticated installation. Ownership and existence are checked again at
dispatch time. Options are translated by the gateway and must be registered for
the selected operation. Unsupported values fail before an asynchronous task is
queued or prepayment is taken.

Successful submission returns `202 Accepted`:

```json
{
  "task_id": "0e18472d-f50b-44fa-bff8-c0340d50ea5b",
  "receipt_id": "cb018f0a-8854-49ef-81c9-48b08d756747",
  "kind": "image",
  "operation": "generate",
  "status": "queued",
  "retry_count": 0,
  "payment": {
    "state": "prepaid",
    "currency": "CNY",
    "prepaid_amount": "2.50",
    "actual_amount": null,
    "refunded_amount": "0",
    "refund_complete": false
  },
  "artifacts": []
}
```

Each accepted intent has one stable gateway `task_id` and one stable billing
`receipt_id`. The same IDs remain attached to every internal model or account
attempt. Internal retries do not create another client task, require another
idempotency key, expose per-attempt receipt IDs, or charge the user again. The
`retry_count` is informational and counts gateway-managed dispatch attempts; it
does not grant the client permission to resubmit.

The gateway may try another eligible model/account within the same task after a
definitive retryable failure. It must not issue another paid submission when an
upstream task may already have been accepted or when the outcome is uncertain;
it polls or reconciles a known upstream task instead. Invalid requests and other
non-retryable failures terminate without model fallback. The gateway owns retry
eligibility and attempt ordering.

## Billing

Image, video, and music tasks accepted by this asynchronous API use one prepayment
and one logical billing receipt for the task. The gateway reserves a conservative
amount that covers every candidate it may select. If any selectable candidate
cannot be priced, the request is rejected before acceptance. Changing accounts
or models internally does not create another prepayment or receipt.

After success, the gateway settles the task against measured usage and returns
any unused amount through the same receipt. After all eligible attempts end in a
definitive failure, it refunds the task prepayment once through that receipt. An
accepted, running, or uncertain outcome is not refunded or automatically
resubmitted until reconciliation establishes the result. Append-only balance
ledger movements may record the original charge and a later settlement/refund,
but they are all associated with this one task receipt, never with individual
attempts.

`payment.state` is `prepaid`, `settled`, `refunded`, or `uncertain`. Amounts are
decimal CNY values. `actual_amount` remains `null` until actual usage is known.
The receipt exposes the gateway user's charge only; it does not represent a
provider balance or provider refund.

## Read Task

`GET /v1/donebot/media/tasks/{task_id}` returns the same task object, refreshed
from durable dispatch state, the single billing receipt, and persisted results.
Status is one of `queued`, `dispatching`, `retrying`, `running`, `completed`,
`failed`, or `uncertain`. Internal retries update this task's status and
`retry_count`; they do not create public child tasks. `uncertain` means acceptance
or outcome is not confirmed and must not trigger another paid submission.

`GET /v1/donebot/media/tasks/by-idempotency/{key}` resolves the same
owner-scoped task after a client timeout. The path value is the original opaque
key and is never logged or stored in plaintext; the server looks it up by its
scoped hash. `404` means no task was accepted with that key. Any other failure
means the client should continue querying and must not submit the same paid
operation again with a new key.

## Artifacts

The gateway fetches each completed result and saves it to gateway-controlled
storage before marking the task complete. Generated outputs use the retention
period configured for generated storage in the admin panel. The client should
download completed artifacts promptly and persist them locally. This output
retention does not change the existing upload and ownership flow for input
references.

Each artifact has exactly these stable fields:

```json
{
  "id": "artifact_0e18472df50b44fa",
  "kind": "image",
  "mime": "image/png",
  "name": "generated-image.png",
  "download_url": "/v1/donebot/media/tasks/0e18472d-f50b-44fa-bff8-c0340d50ea5b/artifacts/4f4e2c6f-caca-4a13-a42a-4f5f8cc9f882"
}
```

`download_url` is a fixed relative capability path composed from the random task
UUID and random artifact UUID. Download requires no API key and carries no
signature or expiring query token. The server verifies that the artifact belongs
to the task in the path and that its retention period, configured for generated
storage in the admin panel, has not expired, then streams the stored bytes with
the recorded content type. It never redirects to a provider or object-store URL.
Invalid bindings and expired artifacts return the not-found response. The two
random UUIDs make the path unguessable; the API must never log or expose provider
or object-store URLs in its place.

`GET /v1/donebot/media/tasks/{task_id}/artifacts/{artifact_id}` returns the
stored bytes and exact `Content-Type`, without API-key authentication. Unknown
task/artifact bindings and expired outputs return not found.

## Text-to-Speech

`POST /v1/audio/speech` remains the existing synchronous streaming endpoint; it
does not create an asynchronous media task and does not use media prepayment or
refund. The gateway streams native audio bytes and records actual usage through
the existing usage-billing path after the stream completes. `X-DoneBot-Receipt-ID`
identifies that stream's usage receipt; clients can query
`GET /api/v1/donebot/receipts/{receipt_id}` after local audio EOF to confirm
settlement. An interrupted or uncertain stream is not proof of a successful
charge or a safe reason to resend a paid request.

## Errors

Malformed intent/options/references return `400` or `422` before billing.
Authentication and ownership checks retain the existing API-key authentication
and not-found behavior. Idempotency-key reuse with different content returns
`409`. A task that is accepted or has an uncertain upstream outcome is never
restarted as a new paid task by an idempotency retry.
