# terraform-plugin-framework-codegen-1

`tfpfgen` turns an OpenAPI 3 document into a complete, tested
[terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework)
provider — zero touch. An operator supplies a spec URL and API credentials;
everything else cascades through a GitHub Actions pipeline:

```
spec import → audit run → spec revise → sdk generate → provider generate → verify → PR
```

An OpenAPI document tells you what an API's fields are *called*. It does not
tell you what makes a Terraform provider actually work. So the pipeline
**audits** the live API — minimum and maximum valid configuration, field
dependencies, value-conditional rules — and records **observations**. Those
observations become proposed **corrections** to the spec (RFC-6902 operations
with a justification and a pointer to the observation that proves them). The
**revised spec** is the single source of truth from which both the SDK and the
provider are generated. Every generated file is exactly that — generated;
human judgment enters only as data: `tfpfgen.yaml`, accepted corrections, and
audit inputs.

## The four repos

| Repo | Role |
|---|---|
| `terraform-plugin-framework-codegen-1` (this one) | CLI, reusable workflows, templates, config schema — all behavior |
| `tfpfgen-provider-template-1` | GitHub template stamping a provider repo's identity + thin workflow callers |
| `terraform-provider-thousandeyes-1` | Proof provider #1 — kiota SDK backend |
| `terraform-provider-github-1` | Proof provider #2 — openapi-generator SDK backend |

## CLI

Noun-verb grammar. Every verb is a pure function of committed inputs except
`audit run` and `audit cleanup` — the only two that touch a network with
credentials.

```
tfpfgen config validate | config init
tfpfgen spec import | spec revise | spec verify
tfpfgen audit run | audit cleanup
tfpfgen sdk generate | sdk verify
tfpfgen provider generate | provider verify
tfpfgen callers sync
tfpfgen version
```

See `docs/contract.md` for pipeline stages, artifact names, and exit codes,
and `docs/glossary.md` for the vocabulary — every term in it is deliberate.

## Status

Phase 0: contract and toolkit skeleton. Not yet usable.
