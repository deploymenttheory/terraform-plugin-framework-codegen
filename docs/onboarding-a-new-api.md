# Onboarding a new API, end to end

The numbered runbook for taking an API from "there is an OpenAPI document and an SDK" to
"a generated provider with live-proven behaviour". First written from two walked paths —
the tag resource (draft-assisted, fully probed) and the credential pair (hand-authored,
probed, plus an ephemeral) — and since walked roughly twenty times across five recorded
batches (tests, alerts, dashboards, account management, endpoint labels), so every step
below has been done repeatedly, and the sharp edges each one found are recorded where
they cut.

The canonical pipeline order is
`openapi fetch -> blueprint draft -> [probe record -> blueprint merge] -> provider generate`,
and this runbook deliberately authors before recording. Throughout: the order is
**author first, record second**. A resource lands in the blueprint before it has
evidence; replay and verify note the gap and continue, and the record run is what
closes it. Nothing here requires doing steps out of order.

## 0. What you need before starting

- **An SDK — or none.** The toolkit generates the provider layer and binds it to an
  SDK in one of two dialects. `restyService` binds a hand-written SDK that already
  exists (service structs off a client, methods returning
  `(*Result, *resty.Response, error)`); the provider's `go.mod` pins it. With
  `kiotaFluent` there is nothing to write first: `tfpfgen sdk generate` derives the
  SDK from the same pinned OpenAPI snapshot with Microsoft Kiota (embedded in the
  provider tree by default), and `blueprint draft -sdk-dialect kiotaFluent` infers
  fluent request-builder bindings against it. Either way, every symbol a blueprint
  names is type-checked against the real SDK by `bindings check`.
- **A disposable tenant.** Mutating probes and acceptance tests create and destroy real
  objects. The sandbox guard demands a profile that *says so* and proves it at runtime;
  do not point any of this at an account anybody depends on.
- **A bearer token**, reachable from the environment only. The house pattern on macOS:

  ```sh
  security add-generic-password -U -s tfpfgen-probe -a bearer -w   # prompts silently
  TFPFGEN_PROBE_BEARER_TOKEN=$(security find-generic-password -s tfpfgen-probe -w) ...
  ```

  Never a flag (process table, shell history), never a file (the profile loader scans
  for credential-shaped values and refuses the file rather than reading it).

## 1. Pin the OpenAPI document

```sh
tfpfgen openapi fetch -url https://…/api.yaml -out openapi/PROVIDER
```

The first pin needs `-url`; every later refresh is just
`openapi fetch -out openapi/PROVIDER`, because the snapshot records its own source.
Snapshots are immutable and checksum-verified on every load; an unchanged upstream pins
nothing. Adopting a newer snapshot is a deliberate curation step — diff the metadata,
re-draft, re-record where behaviour moved — never a side effect of fetching one.

## 2. See what the document offers

```sh
tfpfgen blueprint draft -tag THING -dry-run
```

Classification is editorial evidence, not a decision: a candidate is a collection path,
an item path, and whichever CRUD operations exist. Skipped kinds are named out loud —
data source and action inference is not implemented, so those are hand-authored (step 4).

## 3. Infer resource blueprints

```sh
tfpfgen blueprint draft -tag THING -out blueprints/PROVIDER \
  -sdk-service-root github.com/…/PROVIDER_api -sdk-accessor r.client.API -api-version-dir v7
```

Attributes come from request ∪ response schemas (request = writable, response-only =
computed); wire bindings and operations are synthesised against the SDK flags. The
output is a **starting point for curation**: read every attribute, every operation, and
expect to edit. What inference cannot know:

- **`policy.updateStyle`** is guessed from the HTTP verb. `putFull` is the safe guess
  (sending the whole object is correct under both semantics); the probe will observe
  the truth and disagree visibly if it differs.
- **Named SDK enum types.** A field the document calls `string` may be a named type in
  the SDK (`tags.ObjectType`). `bindings check` (step 5) catches the mismatch; fix the
  wire with the enum converters (`convert.FrameworkToEnum` with `typeArgs`,
  `convert.EnumToFramework`).
- **`notFoundIsSuccess`** is defaulted `true` with no evidence; `read.not-found-shape`
  will settle it.

## 4. Hand-author what inference does not reach

Data sources, actions, ephemerals, the provider block, identity, list facets, hooks —
all hand-authored, shaped exactly like the pilot's committed examples
(`blueprints/thousandeyes/…`). The parts that repay attention:

- **Acceptance seeds.** A read-only block's generated test needs a real object, and
  which resource's fixture creates one is judgement: declare it (`accTest.seedResourceKey`
  + attribute mappings; `envArgs` + `cleanup` for an action whose subject Terraform
  cannot create). No seed means no generated test — a stated refusal, not a silent one.
- **A scenario per resource** (`PROVIDER/RESOURCE.scenario.json` by convention):
  fixtures (complete valid payloads — one per *branch* of the API's dispatch, see step
  6), candidates for fields whose valid values the document does not describe, and
  `defaultInfluencers` naming every field whose value decides another field's behaviour.
  **A dispatch field nobody declares is a branch nobody measures.** Don't start from a
  blank file: `blueprint draft … -scenario-drafts blueprints/PROVIDER` scaffolds a
  `RESOURCE.scenario.draft.json` worksheet per resource — required fields filled
  where the document can say, `CURATE_ME` everywhere it cannot, documented enum
  alternatives prefilled as candidates. The `.draft` suffix keeps it invisible to
  every loader; curate the placeholders, think about influencers (the scaffold
  deliberately guesses none), and promotion is the rename — a diff a reviewer sees.
- Validation runs on every load and reports every problem at once. So does `LoadDir`'s
  cross-resource checking (duplicate type names, import aliases).

## 5. Type-check the bindings

```sh
tfpfgen bindings check -blueprint blueprints/PROVIDER -module pilot/PROVIDER
```

Real `go/types` against the SDK version the provider pins — accessor chains, method
signatures, return arities, response models, every attribute's `sdkField`, for all five
block kinds, counted per kind. Run it before ever generating: it turns a pile of
identical compile errors in generated code into one message naming the blueprint field
to edit.

Then derive the **static facts** — the behaviour written into the SDK's own struct tags
(a value-typed `omitempty` field cannot send its zero value) — and fold them in:

```sh
tfpfgen bindings facts -blueprint blueprints/PROVIDER -module pilot/PROVIDER \
  -out blueprints/PROVIDER/static.facts.json
tfpfgen blueprint merge -blueprint blueprints/PROVIDER \
  -facts blueprints/PROVIDER/static.facts.json
```

Commit the document; CI re-derives it with `bindings facts -check`, so an SDK version
bump that changes a struct tag shows up as drift instead of as a silently wrong fixture.
Repeat both commands whenever the SDK pin moves.

## 6. Generate, build, and wire the hand-written shell

```sh
tfpfgen provider generate -blueprint blueprints/PROVIDER -out pilot/PROVIDER -dry-run
tfpfgen provider generate -blueprint blueprints/PROVIDER -out pilot/PROVIDER
cd pilot/PROVIDER && go test ./...
```

`provider generate` finishes with the postcheck battery — compile, tfplugindocs
regeneration, `terraform fmt` over the fixtures — so a tree that would fail the CI
checks fails on your machine at generation time. `go test` is still yours to run; the
battery proves the tree is well-formed, not that it is correct.

The hand-written boundary (once per provider, then stable): `main.go`,
`internal/provider/{provider,interfaces}.go`, `internal/client/`,
`internal/acceptance/**`, `internal/services/common/**`. Two lessons the first live runs
taught, both now load-bearing:

- **`Configure` must hand the client to every kind it serves** — `ResourceData`,
  `DataSourceData`, `ActionData`, `EphemeralResourceData`, `ListResourceData`. The pilot
  funnels them through one `configureAllKinds` so a kind added later is added there or
  visibly nowhere. (The generated code guards a nil client with a diagnostic, but the
  diagnostic names a bug you still have to fix.)
- **The echo provider** joins the acceptance factories for ephemeral tests only — an
  ephemeral value never reaches state, so the echo is the one place a check can read it.

## 7. Probe the live API

```sh
TFPFGEN_PROBE_BEARER_TOKEN=$(security find-generic-password -s tfpfgen-probe -w) \
TFPFGEN_PROBE_API_URL=https://api.PROVIDER.com/v7 \
tfpfgen probe record -blueprint blueprints/PROVIDER -resource THING \
  -allow-mutations \
  -profile .tfpfgen/sandbox/PROVIDER.json
```

Scenarios resolve per resource by convention — `blueprints/PROVIDER/THING.scenario.json`
beside the blueprint (`-scenario-dir` overrides the directory, `-scenario` a single
file, and `-scenario` demands `-resource`: a scenario speaks one schema's wire
vocabulary). Scope the run with `-resource`, or omit it to record a whole batch: each
resource runs against its own scenario with its own ledger and budget, and one with no
scenario file is skipped with a stated note rather than blocking the rest. Check the
budget first with `probe list`, which also costs each resource against its own
scenario; every guard condition is required and every refusal names all unmet
conditions at once. What the fixtures decide:

- **One fixture per branch.** The dynamic-tag findings exist because a second fixture
  reached a branch the first could not. A field only one fixture declares is probed
  inside that fixture's payload (never an arbitrary one), and its facts are scoped to
  the dispatch fields every attempt shared — measured branches get facts, unmeasured
  ones get nothing.
- **Declared dispatch fields keep their fixture's values.** Everything else is
  synthesised from a pool that prefers observed-accepted values and never sends
  observed-refused ones.
- **Refused enum values escalate** into every other declared fixture before being called
  rejected; a disagreement one dispatch field cleanly partitions becomes a fact per
  branch.

A mutating record ends with the **rehearsal fixpoint**: the exact lifecycles the
generated acceptance tests will run, with the fixture values the generator will render,
re-derived and re-run until they converge (see
[fixtures-and-rehearsal.md](fixtures-and-rehearsal.md)). Its refusal notes are the
pre-generation signal — a payload the API refuses in rehearsal is an acceptance failure
you get to fix *before* any provider code exists, usually with an `accFixture` hint or
omission. `-skip-rehearsal` skips it for a cheap targeted re-record.

Then prove purity offline, exactly as CI will:

```sh
tfpfgen probe verify -blueprint blueprints/PROVIDER   # no network
```

## 8. Merge the evidence

```sh
tfpfgen blueprint merge -blueprint blueprints/PROVIDER \
  -facts recordings/PROVIDER/THING/RECORDING/facts.json
```

Merge widens and annotates; it never narrows and it never resolves a disagreement
silently. Read all three sections of the output:

- **Changes** — behaviour written, including conditional variants
  (`behaviour.conditional[field=value].…`) that generation acts on: `requiredWhen`
  validators, branch-aware assertions, fixture membership.
- **Conflicts** — the curated blueprint and the evidence disagree. Each names the fix.
  A stale unconditional fact contradicted by a branch observation is resolved by
  *removing the stale fact* and re-merging, so the branch truth applies.
- **Recommendations** — decisions merge refuses to make (RequiresReplace via committing
  `behaviour.immutable`, type changes from integrality, defaults). Committing the
  blueprint diff **is** the opt-in; review it as the decision it is.

Reconcile presence changes by hand where the evidence demands them (the findings doc
pattern: a field the API discards becomes `computed`; a branch-dependent field becomes writable
with its enum converter), then re-run merge until it reports nothing.

Where the fixture generator refuses to derive a value the scenario already knows
(`-adopt-scenarios` names the attributes in its output), adopt the scenario's values
into `accFixture` wire hints instead of retyping them:

```sh
tfpfgen blueprint merge -blueprint blueprints/PROVIDER \
  -facts recordings/PROVIDER/THING/RECORDING/facts.json \
  -adopt-scenarios blueprints/PROVIDER
```

Adoption only fills gaps — refused attributes, first fixture, hand-written hints always
win — so re-running it is safe.

## 9. Acceptance, live

```sh
cd pilot/PROVIDER && TF_ACC=1 go test -count=1 -p 1 -run TestAcc ./...
```

Generated per kind: the resource lifecycle (create, import-verify, update, and a forced
replacement when an immutable attribute has a second usable value), seeded datasource
cross-checks, the list query, the action invoke with its re-asserted reversal, the
ephemeral echo. `-p 1` matters: packages otherwise run concurrently against one tenant.
Seed values are salted per consumer for the same reason.

Then let CI own it: the acceptance workflow is dispatch + weekly schedule, sits behind
a GitHub environment, and is the only job that creates real objects.

## 10. The checks that keep it honest

Every PR runs five jobs in `codegen-verify.yml` — 🔁 Regenerate and diff, 🔗 Verify SDK
bindings (including the static-facts drift check), 🔀 Round-trip through
tfplugingen-framework, 🔬 Re-derive probe facts offline (replay-verify with egress
blocked, then `blueprint merge -check` over every facts file), and 🌍 Terraform
validates the examples — each with a local reproduction listed in [checks.md](checks.md).
A resource with no recording yet is a stated note; a verify that proved nothing at all
fails. When any of these disagrees with you, the committed artefact is the arbiter —
regenerate, re-derive, or re-record; never edit a generated file or a cassette by hand.
