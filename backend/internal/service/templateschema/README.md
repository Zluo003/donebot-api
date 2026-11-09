# Template Package Schema

`package-v2.schema.json` is generated from the Apache-2.0 OpenMausBot package
schema retained in DoneBot (`shared/package-format.ts`, base v0.1.146).
Attribution: OpenMausBot contributors, including Milind Soni. The upstream
Apache-2.0 LICENSE and NOTICE remain in the DoneBot repository.

Regenerate from the adjacent DoneBot checkout:

```sh
node --experimental-strip-types scripts/export-template-schema.ts ../donebot-api/backend/internal/service/templateschema
```

The two testdata files are generated from DoneBot's self-contained ecommerce
templates by that same command. They contain no store accounts or credentials.

The gateway checks the schema, size, references, SOUL and SKILL.md declarations.
The client additionally applies the original package parser and file trust
rules. No organization publisher identity or permissions are granted by upload.
