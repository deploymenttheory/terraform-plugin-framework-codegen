# The cross-repo contract

This document names the parts of the toolkit other repositories depend on.
Changing anything here is a contract change: it follows semver, and a
breaking change cuts a new major tag rather than moving `v1`.

## The pipeline

One `workflow_dispatch` on a provider repo's thin caller runs the reusable
`10-generate.yml` here. Ten jobs, each exactly one verb, each arrow a
named artifact. GitHub's "Re-run failed jobs" resumes from any point because
succeeded jobs' artifacts persist within the run.

| # | Job | Verb | Artifact | Notes |
|---|---|---|---|---|
| 1 | config-validate | `tfpfgen config validate --secrets` | `validate-report` | Fails in under a minute on an unknown key, a missing role secret, a non-tag generator pin, or caller drift. |
| 2 | spec-import | `tfpfgen spec import` | `spec-imported` | Pins the upstream document by SHA-256. |
| 3 | audit | `tfpfgen audit run` | `observations` | The only job that receives the `TFPFGEN_AUTH_*` values. Runs when asked (`audit=true`) or when no committed observations exist. |
| 4 | spec-propose | `tfpfgen spec revise --propose-only` | `corrections-proposed` | Compiles observations into `spec/corrections/proposed/` and stops, exiting 0 whatever it proposed. Publishes the proposals off the runner. |
| 5 | open-correction-prs | — | one PR per proposal | Runs beside [6], never before it. Write permissions; skipped when nothing was proposed. |
| 6 | spec-revise | `tfpfgen spec revise` | `spec-revised` | Hard-fails while `spec/corrections/proposed/` is non-empty, naming each file a human must accept or reject. No ignore flag exists. |
| 7 | sdk-generate | `tfpfgen sdk generate` | `sdk-tree` | The configured backend, at its exact version pin. |
| 8 | provider-generate | `tfpfgen provider generate` | `provider-tree` | Includes the manifest and the derived go.mod. |
| 9 | verify | `tfpfgen provider verify` + build/vet/lint/test | `verify-report` | Coverage gate ≥90%; docs and fmt must be fixed points. |
| 10 | open-pr | — | the PR | Assembles every artifact. Branch `tfpfgen/run-<id>`. |

## The decision gate

Job [6] refuses to materialize while any correction awaits a decision. That
refusal is the point, but a refusal alone destroys what it refuses: proposals
written into the runner's workspace by a job that then exits 1 leave with the
runner, and the operator has nothing to review. Jobs [4] and [5] and the
`20-corrections.yml` workflow close that loop.

**`corrections-proposed`** is the whole `spec/corrections/` tree — proposals,
auto-accepted `auto-NNN-` files, previously accepted corrections and
rejection markers alike. Job [6] restores it, so the gate sees exactly what
[4] compiled. A run that proposes nothing against a tree that has no
corrections committed produces no artifact at all; the restore pattern
tolerates a missing artifact, and job [5] is skipped rather than failed.

**One PR per proposal.** Job [5] opens each on branch
`tfpfgen/correction-<observationID>` — the observation ID, 16 hex characters,
because it is stable across runs where the `NNN-` ordinal is not, so a re-run
updates the same PR instead of opening a second one for a decision already
pending. Each PR carries exactly one file, moved from
`spec/corrections/proposed/` to `spec/corrections/`:

- **Title** — `tfpfgen: <entity>.<attribute> (<kind>)`, read from the
  correction's justification prose.
- **Label** — `tfpfgen-correction`, created if the repository lacks it.
- **Body** — the justification, the RFC-6902 operations as fenced JSON, the
  `audit/observations/<entity>.observations.json#<observationID>` evidence
  pointer, and a machine-readable `tfpfgen-run-id: <id>` line naming the run
  that proposed it.

**Merging accepts; closing rejects.** A merge puts the correction on the
default branch, which is what accepting means. A close without a merge is
recorded by `20-corrections.yml` as
`spec/corrections/rejected/<observationID>.json` — `observationID`, `reason`
(the PR's closing comment, else `closed without merging`) and `rejectedAt`,
those three keys and no others, because the decoder is strict. The marker
suppresses re-proposal until someone deletes it.

Job [5] opens nothing for a proposal already accepted on the default branch,
already carrying a rejection marker, or already the subject of an open PR.

**The continuation is debounced.** Corrections arrive in batches and are
answered one at a time, so `20-corrections.yml` dispatches the caller's
generate workflow only on the close that leaves no open `tfpfgen-correction`
PR behind — passing `reuse_audit_run_id` read back from the PR body, so the
pipeline resumes at revise against the observations it already paid for. A
repository-wide concurrency group keeps two simultaneous closes from both
dispatching.

**The App, and life without it.** A `workflow_dispatch` made with
`GITHUB_TOKEN` starts no run, and a merge it authored raises no event, so
auto-continuation needs a GitHub App: set `TFPFGEN_APP_ID` and
`TFPFGEN_APP_PRIVATE_KEY` and the loop closes itself. Both secrets are
optional. Without them both workflows fall back to `github.token`, open and
record everything identically, and print a warning and a `::notice::` naming
the run ID to dispatch by hand. Note these are the pipeline's own App, not
the `TFPFGEN_AUTH_APP_*` role that authenticates an audited API.

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
only `open-correction-prs` and `open-pr` keep write. `secrets: inherit`
hands the repo's secrets across by name — the `TFPFGEN_AUTH_*` roles, read
only by the audit job, and `TFPFGEN_APP_ID` / `TFPFGEN_APP_PRIVATE_KEY` if
the repo has them. Every secret is declared `required: false`; validation,
not the workflow, is what refuses a missing role.

`20-corrections.yml` is the one caller whose trigger is not a dispatch,
because it answers a decision rather than starting work:

```yaml
name: corrections
on:
  pull_request:
    types: [closed]
permissions:
  contents: write
  pull-requests: read
  actions: write
jobs:
  corrections:
    uses: deploymenttheory/terraform-plugin-framework-codegen-1/.github/workflows/20-corrections.yml@v0
    with:
      generate_workflow: generate.yml
    secrets: inherit
```

`generate_workflow` names the caller's own generate workflow file — the one
above, `generate.yml` — so the debounced continuation knows what to
dispatch. The called workflow ignores every PR that is not labelled
`tfpfgen-correction`.

The same thin shape calls `30-ci.yml` (push/PR triggers, no inputs, no
secrets), `40-acceptance.yml` (schedule/dispatch only, an `environment`
input whose protection rules gate the run), `50-docs.yml`
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

Two further secrets belong to the pipeline itself rather than to any
`auth.method`, and neither is required:

| Secret | Used by | Absent |
|---|---|---|
| `TFPFGEN_APP_ID` | `open-correction-prs`, `20-corrections.yml` | Falls back to `github.token`; a warning says merges will not auto-continue. |
| `TFPFGEN_APP_PRIVATE_KEY` | the same | The same. |

The App must be installed on the provider repo with **contents: write**,
**pull requests: write**, **issues: write** (labels) and **actions: write**
(the continuation dispatch).

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
