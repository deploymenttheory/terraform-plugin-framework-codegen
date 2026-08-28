# The cross-repo contract

This document names the parts of the toolkit other repositories depend on.
Changing anything here is a contract change: it follows semver, and a
breaking change cuts a new major tag rather than moving `v0`.

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
| 4 | spec-propose | `tfpfgen spec revise --propose-only` | `corrections-proposed` | Compiles observations into `spec/corrections/proposed/` and stops, exiting 0 whatever it proposed. Publishes the proposals, and the report describing them, off the runner. |
| 5 | open-correction-prs | — | one PR per (entity, kind) group | Runs beside [6], never before it. Write permissions; skipped when nothing was proposed. |
| 6 | spec-revise | `tfpfgen spec revise` | `spec-revised` | Hard-fails while `spec/corrections/proposed/` is non-empty, naming each file a human must accept or reject. No ignore flag exists. |
| 7 | sdk-generate | `tfpfgen sdk generate` | `sdk-tree` | The configured backend, at its exact version pin. |
| 8 | provider-generate | `tfpfgen provider generate` | `provider-tree` | Includes the manifest and the derived go.mod. |
| 9 | verify | `tfpfgen provider verify` + build/vet/lint/test | `verify-report` | Coverage gate ≥90%; docs and fmt must be fixed points. |
| 10 | open-pr | — | the PR | Assembles every artifact. Branch `tfpfgen/run-<id>`. |

## The decision gate

Job [6] refuses to write while any correction awaits a decision. That
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

**The proposal report.** Beside the proposals, `--propose-only` writes
`spec/corrections/proposed/proposals.json`: the same proposals grouped by
(entity, kind), each finding carrying its observation ID, its observed value,
the redacted request/response excerpts it was read from, and the plain-English
account of what the API did and what each decision costs. It exists because a
correction file cannot narrate — it is a justification, some RFC 6902 and a
pointer — and a reviewer needs the exchange, not the mechanism. It is
deterministic, it is never applied, and it is not a correction: everything
that scans that directory keys on the `.correction.json` suffix, so the gate
does not count it as a pending decision.

**One PR per entity per kind.** Job [5] opens each on branch
`tfpfgen/correction-<entity>-<kind>`, the kind in kebab case, sanitised to
what a git ref may hold. The branch is stable across runs, so a re-run updates
the pending decision instead of opening a second one beside it: an undecided PR
has its title, body and branch rewritten to the current evidence, because the
render it opened with belongs to whichever toolkit version happened to be
current that day. The branch is only pushed when the correction files actually
differ — commit timestamps alone move the sha — and the body is only edited
when it differs, so a retried job is silent. It was one PR
per *correction*, which is one per attribute: the first live run opened
fifty-seven, twenty-five of them recording a single field's default. Each PR
carries every correction file of its group, moved from
`spec/corrections/proposed/` to `spec/corrections/`:

- **Title** — `tfpfgen: <entity> — <N> <human kind title>`, e.g.
  `tfpfgen: tag — 3 server-assigned defaults`.
- **Label** — `tfpfgen-correction`, created if the repository lacks it.
- **Body** — a lead sentence, then one section per finding narrating what was
  asked of the API, what the document led us to expect, what actually came
  back (the real status and the redacted response fragment) and what it means
  for a practitioner; then what merging and closing each do; then the RFC 6902
  operations in a collapsed `<details>`. Three machine-readable lines close
  it: `tfpfgen-run-id: <id>`, `tfpfgen-observations: <ids>` — this group's
  observations — and `tfpfgen-groups: <entity>/<kind>=<id>+<id>;…`, the whole
  run's manifest.

**Merging accepts; closing rejects — the whole group.** A merge puts the
corrections on the default branch, which is what accepting means. A close
without a merge is recorded by `20-corrections.yml` as one
`spec/corrections/rejected/<observationID>.json` per observation in the group
— `observationID`, `reason` (the PR's closing comment, else `closed without
merging`) and `rejectedAt`, those three keys and no others, because the
decoder is strict. The IDs are read from the body's `tfpfgen-observations`
line rather than from the branch name, which a group has no way to encode; a
PR from before grouping still falls back to the observation ID in its branch.
Each marker suppresses re-proposal until someone deletes it.

Job [5] filters each group down to what is still undecided — dropping
observations already accepted on the default branch or already carrying a
marker — skips a group with nothing left, and skips a group whose branch
already has an open PR.

**A proposal can be withdrawn, which is not a rejection.** An observation is
not permanent: fix a defect in the audit and a finding it used to report can
stop existing. A probe that sent the string `"120"` into an integer enum had
`interval` recorded as server-forced on three resources; the corrected run does
not observe it at all. Job [5a] closes every open correction PR outside the set
this run proposes, labels it `tfpfgen-withdrawn`, and records nothing — and
`20-corrections` skips both its jobs on that label, so no marker is written and
no continuation is dispatched into a run still in flight.

The distinction is load-bearing. Closing a correction PR any other way writes
one rejection marker per finding, and a marker suppresses its observation until
someone deletes the file; applied to a finding that was wrong about the probe
rather than the API, it buries the corrected proposal too. Job [5a] therefore
runs even when job [5] is skipped, which is precisely the run that proposes
nothing and leaves every open PR unanswerable.

**Recording a decision is never cancelled.** The `record` job's concurrency
group is keyed on the pull request number, so no two closes contend. It used
to be repository-wide, and GitHub keeps only one *pending* run per group: a
bulk close of fifty-nine correction PRs produced four runs and zero markers,
because each new close cancelled the pending run before it wrote anything.
The marker commit rebuilds on the branch tip and retries a losing push up to
eight times — many closes push to the same branch at once — then asserts every
marker is present on the default branch and fails the job if it is not.

**The continuation waits for every decision, not for an empty queue.** A run
opens a dozen PRs; if the operator starts merging while job [5] is still
creating the rest, the open count passes through zero, and continuing there
would generate a provider from a half-decided spec. So the `continue` job
resolves the `tfpfgen-groups` manifest instead: a group counts as decided only
when every observation in it is either accepted on the default branch or
carries a marker. It dispatches only when all of them are, and no
`tfpfgen-correction` PR is open; it warns by name when a group was never
opened, telling the operator to re-dispatch; and it continues at once for a
run whose manifest is empty. The manifest lives in the PR body because that is
the one place readable from the close event itself — no artifact to expire, no
extra push to race the marker commits, and every sibling PR of the run carries
it, so a group whose own PR was never opened is still accounted for. The
dispatch passes `reuse_audit_run_id` from the same body, so the pipeline
resumes at revise against the observations it already paid for. That job's
concurrency group is repository-wide, which is the debounce: GitHub keeps the
newest queued run, and the newest is the last close.

**Verifying the concurrent close.** The race is not reproducible in CI, so it
is rehearsed: clone the default branch into N working copies, run the `record`
step's script in each against a different set of observation IDs
simultaneously, and assert `spec/corrections/rejected/` holds one marker per
ID afterwards. Twelve grouped closes of five observations each must yield
sixty markers and no worker failures.

**The App, and life without it.** A `workflow_dispatch` made with
`GITHUB_TOKEN` starts no run, and a merge it authored raises no event, so
auto-continuation needs a GitHub App: set `TFPFGEN_APP_ID` and
`TFPFGEN_APP_PRIVATE_KEY` and the loop closes itself. Both secrets are
optional. Without them both workflows fall back to `github.token`, open and
record everything identically, and print a warning and a `::notice::` naming
the run ID to dispatch by hand. Note these are the pipeline's own App, not
the `TFPFGEN_AUTH_APP_*` role that authenticates an audited API.

## What an observation is worth

Every attribute the generator emits lands in exactly one of five outcomes, and
nothing else exists. A correction is only worth compiling if it moves an
attribute between them, or changes a plan modifier or a validator.

`docs/mapping.md` is the wider specification these five outcomes serve: twelve
API behaviours and the terraform-plugin-framework shape each demands, and above
them the operation sets an entity can carry. The table below decides presence;
mapping.md also fixes which plan modifier, validator, custom type or collection
kind that behaviour calls for, and which API call each generated function
makes.

| Outcome | When | Decided by |
|---|---|---|
| Omitted entirely | The type cannot be represented | `deriveType` marks it unsupported |
| `Required` | Writable and required on create | the create body's `required` |
| `Optional` + `Computed` | Writable, and the response carries a value whether or not the request supplied one | `x-tfpfgen-server-default`, the response schema's `required`, or a `default` on the request property |
| `Computed` | The practitioner cannot set it | absent from the create body, `readOnly`, `x-tfpfgen-server-forced`, `x-tfpfgen-volatile` |
| `Optional` | Writable, and the server leaves it absent when omitted | none of the above |

`Optional` alone is the rare one. Most APIs answer with a value for every field
they accept, so a writable attribute usually belongs in `Optional` + `Computed`;
emitting it as `Optional` alone gives the practitioner a perpetual diff, because
Terraform holds null in config against a value in state.

Three declarations reach `Optional` + `Computed`, of decreasing authority.

`x-tfpfgen-server-default` is the audit's own measurement, taken by omitting the
attribute and reading what comes back. It is the only one that does not depend
on the document being diligent, which is why it exists.

The response schema's `required` list is the document asserting the same fact.
It is too weak on its own: a document that declares nothing required in its
responses — as real ones do — sends every writable attribute to plain
`Optional`.

OpenAPI's `default` is the document stating what the server substitutes for an
omitted value, which is that same fact in different words. It is read from the
**request** side only: a default on a response schema says nothing about what
happens when a request omits the field.

That third route carries a known risk, accepted deliberately. A `default` on a
`$ref`'d property is written onto a schema every other use of that type shares,
so one declaration can move attributes that were never meant to move together. A
correction is the remedy where it is wrong. The alternative was leaving thousands
of attributes on plain `Optional`, which gives the practitioner a perpetual diff
on every one of them the server fills — a defect in every plan, against a risk
in some.

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
    uses: deploymenttheory/terraform-plugin-framework-codegen/.github/workflows/10-generate.yml@v0
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
    uses: deploymenttheory/terraform-plugin-framework-codegen/.github/workflows/20-corrections.yml@v0
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
- Caller workflows reference `deploymenttheory/terraform-plugin-framework-codegen/.github/workflows/<NN-name>.yml`
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
