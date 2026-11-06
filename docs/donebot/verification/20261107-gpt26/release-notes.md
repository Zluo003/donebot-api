GPT Image 2.6 Flare and Sunburst can now be configured as native image models for DoneBot, including persistent asynchronous GIF generation and reference-image GIF edits through API Key upstream accounts.

- Add both official model IDs to the backend default catalog and admin model choices, with explicit official token-cost fallback rates.
- Preserve complexity and GIF frame controls in JSON and multipart requests; DoneBot fixes complexity to high and rejects SVG before paid generation.
- Keep GIF routing on compatible API Key accounts, retain GIF availability in mixed account pools, and preserve original animated bytes and image/gif MIME.
- Reject ambiguous duplicate request fields before task creation or prepayment. Keep existing per-file prepayment, failure refund, receipt recovery and account permissions.

After deployment, configure the two models' upstream mappings, account availability and per-image prices. Adding the default model IDs does not enable upstream access or create sellable pricing. GIF uses the configured per-output-file image tariff and requires the supplier's GIF preview access. OAuth GIF is not exposed until its protocol is verified.

Validation uses isolated services and synthetic GIF fixtures; this release does not claim a paid live GPT Image 2.6 generation has been verified.
