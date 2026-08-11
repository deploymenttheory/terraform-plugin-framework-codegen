# Pipeline rehearsal: the v2 audit engine, end to end

This is the record of the first full local run of the v2 audit engine —
strategy compiler, adaptive executor, triangulating inference, and the
corrections-plus-validators layer of Waves 1–4 — driven against the
quirkserver's shape resources with a `tfpfgen` built from `main` after
`#42` (stock-idiom conditional validators). The goal was the project's
central claim, now for a *multi-variant* API: supply the spec, and
everything cascades — import, audit, revision, SDK, provider, verification —
with no hand-written provider code and the one human step the design demands
(accepting or rejecting proposed corrections).

**Verdict: the v2 chain works end to end, and is deterministic.** Every
verb ran against a real HTTP server (the quirkserver's `monitor` /
`assignment` / `agent` shapes), a real kiota 1.34.1, and a real Go
toolchain; the adaptive executor self-healed the optional-but-required
field and borrowed a live reference, the inference emitted the conditional
edges, revision folded them into the spec as `x-tfpfgen-*` extensions, and
the generated provider — including a genuinely multi-variant resource —
built, vetted, tested green, and passed every drift gate. Two independent
full runs produced a byte-identical `revised.yaml` and provider tree.

Three toolkit defects stood between a rebuilt `main` and that green board.
Each was invisible to the stubbed suite and caught only by generating and
running a real multi-variant provider; each is fixed in **PR #43**
(`fix/fixtures-variant-config`), and the rehearsal was re-run green on the
rebased branch.

## Setup

| Piece | Value |
|---|---|
| Toolkit | built from `main` after `#41`, `#42`, plus the three fixes in `#43` |
| Live API stand-in | `tfpfgen __serve-quirkserver --addr 127.0.0.1:PORT --spec ./monitor.openapi.yaml` (hidden dev verb) |
| Shapes served | `monitor` (multi-variant, `kind` ∈ {ping, web, dns} gating which siblings are valid), `assignment` (its `agent_id` must reference a live `/agents` object), `agent` (a fixed read-only pool). The legacy `/things` quirk surface is also served. |
| Provider under rehearsal | `orbit` (`registry_namespace: deploymenttheory`), `auth.method: bearer_token`, `sdk.backend: kiota@1.34.1` |
| Credential | a synthetic `TFPFGEN_AUTH_TOKEN`, distinctive so redaction could be grepped for |

The quirkserver writes the OpenAPI document it implements at boot; that
file is what `spec import` pinned (sha256 `d47ccb0bcc54`). The document is
honest-but-partial: it declares `monitor`'s fields flat, marks only `kind`
required, and says nothing of the variant structure, the real requirement of
`interval`, or that `agent_id` must reference a live object. The audit
discovers all of it.

## The run, stage by stage

Every stage is one verb of the built binary, in order, in one scratch
provider repo (a `git init` repo, so Go's `-buildvcs` stamping succeeds).

| # | Verb | Exit | What happened |
|---|---|---|---|
| 1 | `config validate --secrets` (secret unset) | 1 | Refused, naming `TFPFGEN_AUTH_TOKEN` — the preflight dies in milliseconds. |
| 2 | `config validate --secrets` | 0 | `tfpfgen.yaml is valid: provider orbit, backend kiota@1.34.1, auth bearer_token`. |
| 3 | `spec import ./monitor.openapi.yaml` | 0 | Pinned `spec/upstream.yaml`, sha256 `d47ccb0bcc54`, openapi 3.0.3. |
| 4 | `spec revise` (no observations) | 0 | `spec/revised.yaml` = the upstream document, 0 corrections. |
| 5 | `audit run --base-url …` | 0 | 54 observations; `monitor` and `assignment` exercised, `thing` blocked; 4 confirmed edges; cleanup left the tenant empty; token redacted. |
| 6 | `spec revise` (observations present) | 1 | Proposed 10 corrections, then **hard-failed by design**, naming all 10 files. No ignore flag exists. |
| 7 | accept: `mv proposed/*.correction.json corrections/` | — | The one human step the design demands. |
| 8 | `spec revise` (rerun) | 0 | Converged: 0 new proposals; `revised.yaml` written with all 10 corrections and their `x-tfpfgen-*` edge extensions. |
| 9 | `sdk generate` | 0 | 20 files with kiota 1.34.1 from `spec/revised.yaml`. |
| 10 | `provider generate` | 0 | 115 files: 3 resources, 4 datasources; postcheck passed (`go mod tidy`, `go build`, `go vet`). |
| 11 | `go build && go vet && go test ./...` (independent) | 0 | The generated provider compiles, vets, and its unit tests pass — including the multi-variant `monitor` resource and datasource. |
| 12 | `provider verify` | 0 | `. matches regeneration from spec/revised.yaml: 115 files, no drift`. |
| 13 | `sdk verify` | 0 | `internal/sdk matches regeneration with kiota 1.34.1: 20 files, no drift`. |
| 14 | `spec verify` | 0 | The pin matches its lock. |

## What the audit observed

54 observations across the three shapes. The findings the v2 engine exists
to earn all landed:

**The adaptive executor self-healed and borrowed.** Its request
adjustments, reported on the summary:

- `add monitor.interval` — the document marks `interval` optional; the
  server enforces it. The executor read the `field interval is required`
  400, added the field, and retried.
- `add monitor.target_host` (gate `kind=ping`), `add monitor.domain` (gate
  `kind=dns`), `add monitor.web` (gate `kind=web`), and the matching
  `remove` of each variant's fields under the wrong `kind` — the variant
  grammar, learned by diffing what each `kind` accepts.
- `borrow assignment.agent_id` — the executor `GET /agents`, took a live
  id, and the create it had refused as `agent_id must reference an existing
  agent` succeeded. `assignment`'s create, read and list all confirmed.

**The triangulating inference emitted the conditional edges** — 4
confirmed, 0 inconclusive:

| Edge kind | Subject | Condition | Extension |
|---|---|---|---|
| `validConfiguration` | `kind` | variants `dns:[dnssec,domain]`, `ping:[target_host]` | `x-tfpfgen-valid-configuration` |
| `validWhen` | `dnssec` | `kind = dns` | `x-tfpfgen-valid-when` |
| `validWhen` | `domain` | `kind = dns` | `x-tfpfgen-valid-when` |
| `validWhen` | `target_host` | `kind = ping` | `x-tfpfgen-valid-when` |
| `requiredWhen` | `domain` / `target_host` / `web` | `kind = dns` / `ping` / `web` | `x-tfpfgen-required-when` |

The `dnssec`-requires-`domain` co-requirement surfaced as
`validWhen(dnssec, kind=dns)` rather than a separate `dependsOn`: on a `dns`
monitor `domain` is itself required, so `dnssec` never lacks it, and the
inference recorded the edge the evidence actually supported. No
`mutuallyExclusive` edge arose on this surface.

**Redaction held.** The distinctive bearer token appears in **zero** files
under `audit/` or `spec/` (grepped for the exact value, including the
observation JSON and the activity ledger). Observation excerpts carry
method, path template, status, and response fragments only.

**Cleanup left the tenant empty.** The run created 8 objects and removed
every one — ledger-tracked ids plus a name-prefix sweep for a monitor whose
id was never learned. `GET /monitors` and `GET /assignments` answered empty
after the run.

## Corrections → the revised spec

10 proposals compiled from the 54 observations; all accepted for the
rehearsal. Applied to the pinned document they produced a `revised.yaml`
carrying, on `MonitorCreate`, `x-tfpfgen-valid-configuration` (the
discriminator and its per-variant valid field sets),
`x-tfpfgen-valid-when` on `dnssec` / `domain` / `target_host`,
`x-tfpfgen-required-when` on `domain` / `target_host` / `web`, and
`x-tfpfgen-eventual-consistency` on the read operations. The rerun after
acceptance proposed nothing — convergence held.

## Validators generated from the edges

`provider generate` emitted `internal/services/resources/monitors/v1/monitor/conditional_validators.go`,
realizing every edge as a stock-idiom config validator (the `#42` form):
`kindRequiredWhenValidator` (×3), `kindValidWhenValidator` (×2) and
`kindValidConfigurationValidator`, each a `resource.ConfigValidator`, plus
`stringvalidator.OneOf("ping","web","dns")` on the `kind` attribute. These
are the value-conditional equivalent of `AlsoRequires` / `ConflictsWith`:
the config is refused when a variant's required field is missing or when a
field is set under the wrong `kind`. The generated resource unit tests
apply variant-consistent configurations and pass against them.

## ListWrap closed

The document declares every collection response a bare array; the server
answers a wrapped envelope (`{"monitors":[…]}`). The v1 rehearsal left a
defect here — the emitted mock responders hardcoded `{"value":[…]}`,
disagreeing with the SDK the bare-array document produced, and the
datasource unit test failed to parse it. In v2 the emit layer derives the
list shape from the declared envelope: the generated mock now serves a bare
array (`SuccessResponse(200, []map[string]any{object()})`), the datasource
`Read` iterates the SDK's returned slice directly, and **all four datasource
unit tests pass**. The defect is closed.

The audit *does* record the server's true wrapped shape as a
`listResponseShape` observation, but that kind still compiles to *no
correction* (`no correction form exists yet`), so the generated provider
follows the document's declared bare-array shape rather than the observed
wrapper. Reconciling the two — and the acceptance test that would exercise
it against the live wrapped server — remains deferred.

## Determinism

Two independent full chains, each against a fresh quirkserver process, were
run end to end. Their `spec/revised.yaml` and their entire generated
provider tree (`internal/**`, `go.mod`, `manifest.json`, `spec/corrections/**`)
were **byte-identical** (aggregate tree hash matched). The only per-run
variation lived where the design puts it: the audit's non-committed
evidence — each observation's `runId`, `observedAt`, and the run-id embedded
in synthesised object names, plus the activity ledger — and the import
`fetchedAt` timestamp. None of it reaches the generated code.

Determinism was not free: closing it required the third fix below.

## Defects found — each caught only by this run

1. **Multi-variant fixtures failed the entity's own validators** (`#43`,
   blocking). The generated `conditional_validators.go` enforces the variant
   grammar, but the fixture generator ignored the edges: the minimal
   `monitor` config omitted `target_host` (required when `kind=ping`) and
   the maximal config set every variant's fields at once. `go test ./...`
   failed at the pre-apply plan. `emit.supportedTree` was also dropping
   every tree-level edge but `ConditionalRequirements` before the fixtures
   saw them. Fixed: the derivation pins one variant, prunes the attributes
   other variants own, and forces the chosen variant's requirement into the
   minimal renderings — HCL and wire gate identically.

2. **Nil dereference in the generated nested-object state mapping** (`#43`,
   blocking). Once fixtures correctly omitted the `web` block from a `ping`
   monitor, `MapRemoteStateToTerraform` panicked on `remote.GetWeb()`. The
   nil-guard was decided by comparing SDK-type spellings, which are equal
   for a kiota interface getter (`GetWeb() MonitorCreate_webable`) even
   though the interface is nil-comparable. Fixed: nilability is decided in
   the binder from the accessor's real `types.Type` and recorded on
   `FieldAccess`, so the read is guarded whenever the API can omit the
   object.

3. **Non-deterministic read-after-write lag** (`#43`). `readAfterWrite`
   recorded `time.Since(create)` — wall-clock, ~340 ms one run and ~350 ms
   the next — which folded into `x-tfpfgen-eventual-consistency` and made
   `revised.yaml` non-reproducible across audits. Fixed: the lag is now
   `failedPolls × interval`, zero on an immediate read and a clean multiple
   of the deterministic interval under real eventual consistency.

None of the three changed a CLI verb, a config key, a workflow, or the
template contract, so the provider-template repo needs no mirror.

## Deferred / inconclusive — honestly

- **The `web` variant is absent from `validConfiguration`.** The confirmed
  variants are `dns` and `ping`; `web` was under-explored. `web` is a nested
  block whose `web.url` the executor could not synthesise a value for inside
  the monitor's complexity-scaled request budget (base 10 + writable ×
  variants = 38), so the `validWhen(web, kind=web)` edge was **withheld**
  rather than asserted on thin evidence — a nested-block `validWhen` held
  back exactly because a value could not be synthesised. `requiredWhen(web,
  kind=web)` was still learned, so the generated validator does require the
  block; only its per-variant *validity* is uncaptured.
- **`monitor` and `assignment` finished `timeoutExhausted`.** Their edge
  findings are confirmed, but the request budget was spent on variant
  discovery and reference-borrowing before the lifecycle probes completed,
  so `immutable` / `values` / `updateStyle` / `deleteNotFoundOK` came out
  `timeoutExhausted` rather than `confirmed`. The chain is unaffected — the
  budget under-provisions a full multi-variant / reference lifecycle, a
  tuning question, not a correctness one.
- **`thing` is blocked.** The legacy `/things` surface answers a different
  400 grammar (`title: missing required field`, `detail: query`) than the
  shape validators' `field <name> …` sentences the adaptive executor parses,
  so it cannot self-heal the `mode=advanced` → `query` requirement. `thing`
  is not a v2 shape; its block is an artefact of mixing the old surface with
  the new executor.
- **`listResponseShape` compiles to no correction** (above), so acceptance
  against the live wrapped server is not yet possible for the datasources.
- **`dependsOn` and `mutuallyExclusive`** are not exercised by this surface;
  their emission is proven only by unit tests, not by this rehearsal.
- **The CI drill** (running this chain as a workflow) remains deferred; the
  runner would need kiota 1.34.1 at the pin, and the local run is the
  authoritative proof.

## The evidence trail

- `#41` — audit corrections + validators (Wave 4)
- `#42` — conditional-edge validators realized with stock framework idioms
- `#43` — the three fixes above: multi-variant fixtures, nilable nested
  state read, deterministic read-after-write lag
