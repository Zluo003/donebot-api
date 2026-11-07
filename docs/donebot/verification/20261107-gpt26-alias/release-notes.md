GPT Image 2.6 GIF requests now preserve their model capability through configured supplier aliases and Composite routes.

- Keep the public model identity when Composite routing rewrites the request. A known 2.6 public or resolved model may use an administrator-configured GPT Image supplier alias; explicit legacy models and mappings with no known GIF-capable identity remain rejected before prepayment.
- Preserve default complexity=high and GIF frame settings in JSON and multipart generation/edit requests. API Key routing, DoneBot SVG/mask restrictions, account permissions, per-file prepayment and failure refunds remain enforced.
- Add redacted structured service logs for asynchronous image failures and pre-dispatch model-option rejections, including task/request correlation and available model/account context. These are service logs; admin Ops-list persistence is outside this change.

Validation: 62 isolated routing/forwarding subcases cover account/channel aliases, Composite routing, JSON/multipart, generation/edit, preserved reference inputs and debit-before-dispatch. Related service/handler tests and diagnostic redaction checks pass. Synthetic upstreams were used; this release does not claim a successful live animated GIF from the production deployment.

Deploy ghcr.io/zluo003/donebot-api:0.4.17. If a GIF still fails after deployment, the new service logs identify the actual routing values; no automatic paid retry is introduced.
