# The cross-repo contract

This document names the parts of the toolkit other repositories depend on.
Changing anything here is a contract change: it follows semver, and a
breaking change cuts a new major tag rather than moving `v1`.

## The pipeline

One `workflow_dispatch` on a provider repo's thin caller runs the reusable
`10-generate.yml` here. Eight jobs, each exactly one verb, each arrow a
named artifact. GitHub's "Re-run failed jobs" resumes from any point because
succeeded jobs' artifacts persist within the run.

| # | Job | Verb | Artifact | Notes |
|---|---|---|---|---|
| 1 | config-validate | `tfpfgen config validate --secrets` | `validate-report` | Fails in under a minute on an unknown key, a missing role secret, a non-tag generator pin, or caller drift. |
| 2 | spec-import | `tfpfgen spec import` | `spec-imported` | Pins the upstream document by SHA-256. |
| 3 | audit | `tfpfgen audit run` | `observations` | The only job that receives secrets. Runs when asked (`audit=true`) or when no committed observations exist. |
| 4 | spec-revise | `tfpfgen spec revise` | `spec-revised` | Hard-fails while `spec/corrections/proposed/` is non-empty, naming each file a human must accept or reject. No ignore flag exists. |
| 5 | sdk-generate | `tfpfgen sdk generate` | `sdk-tree` | The configured backend, at its exact version pin. |
| 6 | provider-generate | `tfpfgen provider generate` | `provider-tree` | Includes the manifest and the derived go.mod. |
| 7 | verify | `tfpfgen provider verify` + build/vet/lint/test | `verify-report` | Coverage gate ≥90%; docs and fmt must be fixed points. |
| 8 | open-pr | — | the PR | The only job with write permissions. Branch `tfpfgen/run-<id>`. |

## Calling the workflows

A provider repo runs the pipeline through a thin caller — triggers,
inputs and an inherited secrets context, nothing else. Everything the
pipeline does lives here and reaches the caller by pinned tag:

```yaml
name: generate
on:
  workflow_dispatch:
    inputs:
      audit:
        type: boolean
        default: false
      reuse_audit_run_id:
        type: string
        default: ""
      openapi_url:
        type: string
        default: ""
permissions:
  contents: write
  pull-requests: write
jobs:
  generate:
    uses: deploymenttheory/terraform-plugin-framework-codegen-1/.github/workflows/10-generate.yml@v0
    with:
      audit: ${{ inputs.audit }}
      reuse_audit_run_id: ${{ inputs.reuse_audit_run_id }}
      openapi_url: ${{ inputs.openapi_url }}
    secrets: inherit
```

The caller's `permissions` are the ceiling the called jobs downscope from:
only `open-pr` keeps write. `secrets: inherit` hands the repo's
`TFPFGEN_AUTH_*` secrets across; only the audit job reads their values.

The same shape calls `20-corrections.yml` (a `pull_request: closed`
trigger, plus the optional App secrets), `30-ci.yml` (push/PR triggers,
no inputs, no secrets), `40-acceptance.yml` (schedule/dispatch only, an
`environment` input whose protection rules gate the run), `50-docs.yml`
(schedule/dispatch) and `60-release.yml` (tag push, plus the GPG secrets).

**The `@v0` pin rule:** until the 1.0.0 contract freeze, callers pin
`@v0`, which fast-forwards with every compatible pre-1.0 release. The
freeze cuts `v1`; callers move to `@v1` deliberately, and from then on a
breaking change cuts the next major rather than moving the tag callers
follow.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. |
| 1 | Failure: the operation ran and refused, or broke. |
| 2 | Usage: the invocation itself was misspelt. |

New codes are appended, never renumbered.

## Secrets

Secret names are fixed by auth role and resolved in exactly one place,
`internal/config`. A provider repo sets only the roles its `auth.method`
needs:

| `auth.method` | Required secrets |
|---|---|
| `bearer_token`, `api_key_header` | `TFPFGEN_AUTH_TOKEN` |
| `basic` | `TFPFGEN_AUTH_USERNAME`, `TFPFGEN_AUTH_PASSWORD` |
| `oauth2_client_credentials` | `TFPFGEN_AUTH_CLIENT_ID`, `TFPFGEN_AUTH_CLIENT_SECRET` |
| `github_app` | `TFPFGEN_AUTH_APP_ID`, `TFPFGEN_AUTH_APP_PRIVATE_KEY` |

Only the audit job receives values; validation checks presence and never
prints them.

## Versioning

- Provider repos pin `generator.version` to an exact release tag; branches
  are refused by validation.
- Caller workflows reference `deploymenttheory/terraform-plugin-framework-codegen-1/.github/workflows/<NN-name>.yml`
  at the moving major tag — `@v0` until the 1.0.0 contract freeze, `@v1`
  after it. Compatible releases fast-forward the tag; breaking changes cut
  the next major and leave existing callers untouched.
- Third-party actions are SHA-pinned; first-party workflows are tag-pinned.
- Observations record the spec hash they were taken against; revision flags
  staleness when the pinned document moves.

## The authored/derived split

Everything in a provider repo is derived — regenerated wholesale, digest-
tracked in `manifest.json` — except the authored data files: `tfpfgen.yaml`,
`spec/corrections/**`, `audit/inputs.json`. Generation refuses to write an
authored path; CI refuses a hand edit to a derived one. There are no
hand-owned code files.
