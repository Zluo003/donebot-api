# GPT Image 2.6 supplier aliases and async diagnostics

Baseline: e29e2bb (v0.4.16).

The user reported no gateway request record for two failed GIF tasks. Yingzo API accepts configured GPT Image supplier aliases based on the known public 2.6 capability; DoneBot required an exact supplier ID before forwarding. An isolated baseline reproduced the same error in 16 alias cases. Exact self mappings passed the existing eight-case probe, so the production request's actual resolved values remain unconfirmed and no user mapping error is asserted.

The fix preserves the original public model identity through Composite context, validates legacy public/routing/supplier models, admits a GPT Image supplier alias only when public or resolved identity proves 2.6 capability, and injects complexity for that identity. It retains all existing GIF option checks, API Key routing and DoneBot product limits.

62 routing subcases pass after the fix: account/channel aliases, JSON/multipart generation/edit; known public 2.6 through Composite alias chains; unknown public resolved to 2.6; known legacy public or supplier; non-GPT supplier; unknown public and supplier. The fake HTTPUpstream asserts exact model, complexity, frames, references and synchronous supplier path. Atomic precharge is observed before HTTP dispatch; no supplier was contacted or charged.

Async workers bypass the ordinary HTTP Ops-error middleware: the original POST returns 202 and later GETs return 200. New structured service warnings include task/request correlation and available routing identity, with diagnostic redaction. Admin Ops-list persistence is not changed. The old persistent task error remains authoritative; no dispatch, refund or retry is inferred from a missing log field.

Production static tests already passed (four PNGs), but the two production GIF attempts remain failed. They are not automatically resubmitted; live GIF acceptance remains pending deployment and verification.
