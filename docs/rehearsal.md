# Pipeline rehearsal: the offline zero-touch chain, end to end

This is the record of the first full local run of the pipeline against the
quirkserver — the stand-in live API — using a `tfpfgen` built from `main`
(post `v0.2.0`, with #30–#33 and #35 merged). The goal was the project's central
claim: supply the spec, and everything cascades — import, audit, revision,
SDK, provider, verification — with no hand-written provider code and no
human step except the one the design demands (accepting or rejecting
proposed corrections).

**Verdict: the chain works end to end.** Every verb ran against a real
HTTP server, a real kiota 1.34.1, and a real Go toolchain; the generated
provider compiled and every drift gate answered clean. Four toolkit
defects stood between `v0.2.0` and a green board — each invisible to the
stubbed test suite and caught only by this run — and one defect remains
open in the emitted unit-test layer, documented below.

## Setup

| Piece | Value |
|---|---|
| Toolkit | built from `main` after #30, #31, #32, #33 |
| Live API stand-in | `tfpfgen __serve-quirkserver --addr 127.0.0.1:8179 --spec quirkserver.openapi.yaml` (hidden dev verb, #30) |
| Quirks profile | `quirkserver.StandaloneQuirks()`: discarded `notes`, immutable `color`, served default 45 vs documented 30, volatile undeclared `etag`, normalised `code`, unenforced documented requirements, closed enum on `mode`, conditional `query`, update-ignored `label` |
| Provider under rehearsal | `orbit` (`registry_namespace: deploymenttheory`), `auth.method: bearer_token`, `sdk.backend: kiota@1.34.1` |
| Credential | a synthetic `TFPFGEN_AUTH_TOKEN`, distinctive so redaction could be grepped for |

The quirkserver writes the OpenAPI document it implements at boot; that
file is what `spec import` pinned. The document is deliberately imperfect
in exactly the ways the serving profile misbehaves, so the audit has real
findings to earn.

## The run, stage by stage

Every stage below is one verb of the installed binary, in order, in one
scratch provider repo. Timings are wall clock on a warm module cache.

| # | Verb | Exit | Time | What happened |
|---|---|---|---|---|
| 1 | `config validate --secrets` (secret unset) | 1 | 7 ms | Refused, naming `TFPFGEN_AUTH_TOKEN` — the preflight dies in milliseconds, before anything credentialed. |
| 2 | `config validate --secrets` | 0 | 6 ms | `tfpfgen.yaml is valid: provider orbit, backend kiota@1.34.1, auth bearer_token`. |
| 3 | `spec import quirkserver.openapi.yaml` | 0 | 11 ms | Pinned as `spec/upstream.yaml`, sha256 `d0782979b059`, openapi 3.0.3. |
| 4 | `spec revise` (no observations) | 0 | 7 ms | `spec/revised.yaml` = the upstream document, 0 corrections. |
| 5 | `audit run` | 0 | 812 ms | 1 entity audited, 24 observations, 43 of 60 budgeted requests, 8 objects created (ceiling 25), cleanup left the tenant empty. |
| 6 | `spec revise` (observations present) | 1 | 17 ms | Proposed 11 corrections, then **hard-failed by design**, naming all 11 files and refusing to continue while `spec/corrections/proposed/` is non-empty. No ignore flag exists. |
| 7 | accept: `mv proposed/*.correction.json corrections/` | — | — | The one human step the design demands. |
| 8 | `spec revise` (rerun) | 0 | 10 ms | Converged: 0 new proposals; `spec/revised.yaml` written with all 11 corrections applied. |
| 9 | `sdk generate` | 0 | 0.65 s | 7 files with kiota 1.34.1 from `spec/revised.yaml`. |
| 10 | `provider generate` | 0 | 3.5 s | 53 files: 1 resource, 1 datasource; postcheck passed (`go mod tidy`, `go build`, `go vet`). |
| 11 | `go build ./...` (independent) | 0 | 0.6 s | The generated provider compiles on its own. |
| 12 | `provider verify` | 0 | 0.8 s | `. matches regeneration from spec/revised.yaml: 53 files, no drift`. |
| 13 | `sdk verify` | 0 | 1.0 s | `internal/sdk matches regeneration with kiota 1.34.1: 7 files, no drift`. |
| 14 | `spec verify` | 0 | 7 ms | The pin matches its lock. |

## What the audit observed

24 observations, all `confirmed`, across 13 kinds, in 43 requests and
803 ms:

| Kind | Count | The finding |
|---|---|---|
| `writable` | 8 | Seven fields round-trip; `notes` is accepted and never stored (`writable: false`) — the silent-discard trap caught. |
| `requiredByAPI` | 4 | `name` genuinely enforced; `mode` documented required but **not** enforced (`false`) — a documented requirement the API ignores. |
| `ignoredOnUpdate` | 2 | `label` (and `notes`) accepted on update with a 2xx and not applied — distinguished from immutability, which refuses. |
| `immutable` | 1 | `color` refused on update, named in the error. |
| `serverDefault` | 1 | Omitted `retention` comes back 45; the document claims 30. |
| `normalisation` | 1 | `MiXeD` in, `mixed` back on `code`. |
| `volatile` | 1 | `etag` differs between two identical reads. |
| `undocumentedFieldInSpec` | 1 | `etag` is real, stable-typed `string`, and absent from the document. |
| `values` | 1 | `mode` accepted `basic`, rejected `advanced` (refused because the probe body omitted `query`), closed enum. |
| `requiredWhen` | 1 | `query` required exactly when `mode=advanced`, matching the document's `x-tfpfgen-required-when`. |
| `updateStyle` | 1 | `patch-merge`: PUT preserves omitted fields. |
| `deleteNotFoundOK` | 1 | Second delete answers 404. |
| `readAfterWrite` | 1 | No read lag (0s). |

The summary also reported `rejectsUnknownFields: thing false` — the API
accepts and ignores an undeclared body field, so refusal-based findings
needed no caution flag.

**Redaction held**: the bearer token appears nowhere under `audit/` or
`spec/` (grepped for the exact value). Observation excerpts carry method,
path template, status, and response fragments only. The activity ledger
was consumed by the run's own cleanup; the tenant ended empty.

## Corrections: proposed, accepted, applied

11 proposals compiled from the 24 observations; all were accepted for the
rehearsal. Applied to the pinned document they produced a revised spec in
which:

- `notes` is `readOnly` with `x-tfpfgen-silently-ignored-on-update` — the
  provider no longer offers a field the API discards;
- `color` carries `x-tfpfgen-create-only` — emitted as a
  `RequiresReplace` plan modifier in the generated resource;
- `retention`'s default reads 45, the value actually served;
- `etag` exists, typed `string`, marked `x-tfpfgen-volatile`;
- `mode`'s enum shrank to `[basic]` (see the epistemics note below);
- the operation level gained `x-tfpfgen-update-style: patch-merge`,
  `x-tfpfgen-delete-not-found-ok: true`, and
  `x-tfpfgen-eventual-consistency: 0s`.

The rerun after acceptance proposed nothing — the convergence property
(accept, re-revise, settle) held in practice.

One observation compiled into no correction: `normalisation` on `code`
reported `no correction form exists yet` — an honest, designed gap in the
correction vocabulary, not a failure.

An epistemics note worth keeping: the `values` correction removed
`advanced` from `mode`'s enum because the probe's candidate body omitted
`query`, and `advanced` requires it. The audit itself recorded the vetoed
combination correctly, but the compiled correction states more than the
evidence supports. A human reviewing proposal 008 with the observation
excerpt in hand could reasonably have rejected it; the rehearsal accepted
everything deliberately to exercise the apply path.

## Defects found — each caught only by this run

The toolkit's own suite (92.5% coverage, all green) stubs the SDK
backends and uses a stdlib-only curated SDK fixture. The first three
shipping defects lived exactly in the seams the stubs cover; the fourth
lived in what the whole suite shares.

1. **kiota generated an unimportable tree** (#31, merged). No
   `--namespace-name` was passed, so kiota defaulted to `ApiSdk`:
   `package ApiSdk` importing `"ApiSdk/things"`, which type-checks in no
   provider repo. First `provider generate` failed with
   `could not import ApiSdk/things`. Fixed by deriving the namespace from
   `emit.FromConfig` — the module path plus `internal/sdk`, one
   definition site.

2. **The first-run bind harness could not resolve the SDK's own
   dependencies** (#32, merged). A repo's first generate has no committed
   `go.mod`, so binding copies the SDK into a temporary module harness —
   whose `go.mod` declared no requirements. A real kiota tree imports the
   kiota runtime everywhere; the bind failed with `undefined: …Parsable`.
   The harness now runs `go mod tidy` before type-checking.

3. **`provider verify` could never pass after a postchecked generate**
   (#33, merged). Postcheck's `go mod tidy` rewrites `go.mod` and writes
   `go.sum` *after* the manifest recorded the emitted bytes, so verify
   flagged `go.mod` as `changed` and `hand-edited` on every run — pipeline
   jobs 6 and 7 were mutually exclusive for a kiota provider. The
   toolchain-finalised files are now re-recorded after postcheck (go.sum
   under the new `postcheck` manifest origin) and verify holds them to
   those digests; a hand edit is still caught.

4. **A suite-wide shared-connection-pool flake** (#35, merged). CI failed
   this very report's PR on a docs-only diff:
   `transport connection broken: http: CloseIdleConnections called`. Every
   client built as `&http.Client{}` (the audit runner, the oauth2 token
   client, corpus fetch, spec store retrieve) rode
   `http.DefaultTransport`, whose idle connections
   `httptest.Server.Close` closes — and every parallel test closes one.
   Each client now owns a cloned transport. The failure had landed
   wherever the scheduler put it, which is what made it look like three
   different bugs before it was one.

## Known defect, left open deliberately

**The emitted unit-test mocks assume a `value`-enveloped list response.**
`render_datasource.go`, `render_resource.go` and `render_listresource.go`
hardcode `ListWrap = "value"`, so generated mock responders serve
`{"value": [...]}` regardless of what the document declares. This
document (and the curated fixture) declare the list response as a bare
array, so the generated kiota SDK expects an array, fails to parse the
mock (`value is not a collection`), and the generated
`TestUnitThingDatasource_Read` fails. Everything up to and including
build, vet and the drift gates passes; the emitted test layer is
self-inconsistent for any API whose list response is not `value`-wrapped.

The fix needs the intermediate representation to carry the declared list
envelope (bare array vs. named wrapper) from `specmodel` through to the
emitters — a new IR field, and IR vocabulary is owner-approved, so the
naming decision is deferred to the repository owner rather than coined
here.

Related, smaller: the quirkserver actually answers `{"things": [...]}`
for list while its own document declares a bare array, and the audit
never notices — the twelve step kinds exercise the item lifecycle, never
the list shape. A list-envelope observation kind would close that hole;
it is also a vocabulary decision.

## Deferred

- **The CI drill** (running this chain as a workflow) — deferred; the
  runner would need kiota 1.34.1 installed at the pin, and the local run
  is the authoritative proof this rehearsal was after.
- **Acceptance against the running quirkserver** (`terraform plan/apply`
  with the generated provider pointed at the live stand-in) — blocked on
  the list-envelope defect above for the datasource; not attempted.
- **A `normalisation` correction form**, reported by revise itself.
- Nothing here changed the cross-repo contract: the serve verb is hidden
  dev tooling, and #31–#33 changed no CLI surface, config key, or
  workflow, so the provider template repo needs no mirror for any of it.

## The evidence trail

- #30 — `feat(quirkserver): the fixture can run as a real HTTP server`
- #31 — `fix(sdkgen): kiota generates at the provider module's SDK import path`
- #32 — `fix(providergen): the bind harness resolves the SDK's own dependencies`
- #33 — `fix(providergen): verify tolerates the toolchain-finalised go.mod and go.sum`
- #35 — `fix(audit): the runner's HTTP clients own their connection pools` (and corpus/store)

The full transcript (every verb, exit code, and timing quoted above) was
captured from the clean-room run in a fresh scratch repo against a fresh
quirkserver process, with the toolkit built from `main` after all four
merges.
