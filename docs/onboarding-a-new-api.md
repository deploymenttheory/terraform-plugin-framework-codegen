# Onboarding a new API, end to end

The numbered runbook for taking an API from "there is a specification and an SDK" to "a
generated provider with live-proven behaviour". Written from two walked paths — the tag
resource (ingest-assisted, fully probed) and the credential pair (hand-authored, probed,
plus an ephemeral) — so every step below has been done at least twice, and the sharp
edges each one found are recorded where they cut.

Throughout: the pipeline's order is **author first, record second**. A resource lands in
the blueprint before it has evidence; replay and verify note the gap and continue, and
the record run is what closes it. Nothing here requires doing steps out of order.

## 0. What you need before starting

- **An SDK.** The toolkit generates the provider layer only; it binds to an SDK that
  already exists (`restyService` dialect: service structs off a client, methods
  returning `(*Result, *resty.Response, error)`). The generated provider's `go.mod`
  pins it, and every symbol a blueprint names is type-checked against that pin.
- **A disposable tenant.** Mutating probes and acceptance tests create and destroy real
  objects. The sandbox gate demands a profile that *says so* and proves it at runtime;
  do not point any of this at an account anybody depends on.
- **A bearer token**, reachable from the environment only. The house pattern on macOS:

  ```sh
  security add-generic-password -U -s tfpfgen-probe -a bearer -w   # prompts silently
  TFPFGEN_PROBE_TOKEN=$(security find-generic-password -s tfpfgen-probe -w) ...
  ```

  Never a flag (process table, shell history), never a file (the profile loader scans
  for credential-shaped values and refuses the file rather than reading it).

## 1. Pin the specification

```sh
tfpluginframeworkgen specs -url https://…/api.yaml -output-dir openapi-specs/PROVIDER
```

The first pin needs `-url`; every later refresh is just
`specs -output-dir openapi-specs/PROVIDER`, because the snapshot records its own source.
Snapshots are immutable and checksum-verified on every load; an unchanged upstream pins
nothing. Adopting a newer snapshot is a deliberate curation step — diff the metadata,
re-ingest, re-record where behaviour moved — never a side effect of fetching one.

## 2. See what the specification offers

```sh
tfpluginframeworkgen ingest -only THING -list
```

Classification is editorial evidence, not a decision: a candidate is a collection path,
an item path, and whichever CRUD operations exist. Skipped kinds are named out loud —
data source and action inference is not implemented, so those are hand-authored (step 4).

## 3. Infer resource blueprints

```sh
tfpluginframeworkgen ingest -only THING -out blueprints/PROVIDER \
  -sdk-service-root github.com/…/PROVIDER_api -sdk-accessor r.client.API -api-version-dir v7
```

Attributes come from request ∪ response schemas (request = writable, response-only =
computed); wire bindings and operations are synthesised against the SDK flags. The
output is a **starting point for curation**: read every attribute, every operation, and
expect to edit. What ingest cannot know:

- **`policy.updateStyle`** is guessed from the HTTP verb. `putFull` is the safe guess
  (sending the whole object is correct under both semantics); the probe will observe
  the truth and disagree visibly if it differs.
- **Named SDK enum types.** A field the spec calls `string` may be a named type in the
  SDK (`tags.ObjectType`). `bindings` (step 5) catches the mismatch; fix the wire with
  the enum converters (`convert.FrameworkToEnum` with `typeArgs`, `convert.EnumToFramework`).
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
- **A probe plan per resource** (`PROVIDER/RESOURCE.probe.plan.json` by convention):
  fixtures (complete valid bodies — one per *branch* of the API's dispatch, see step 6),
  candidates for fields whose valid values the spec does not describe, and
  `defaultInfluencers` naming every field whose value decides another field's behaviour.
  **A gate nobody declares is a branch nobody measures.** Don't start from a blank
  file: `ingest … -plan-drafts blueprints/PROVIDER` scaffolds a
  `RESOURCE.probe.plan.draft.json` worksheet per resource — required fields filled
  where the specification can say, `CURATE_ME` everywhere it cannot, documented enum
  alternatives prefilled as candidates. The `.draft` suffix keeps it invisible to
  every loader; curate the placeholders, think about influencers (the scaffold
  deliberately guesses none), and promotion is the rename — a diff a reviewer sees.
- Validation runs on every load and reports every problem at once. So does `LoadDir`'s
  cross-resource checking (duplicate type names, import aliases).

## 5. Type-check the bindings

```sh
tfpluginframeworkgen bindings -blueprint blueprints/PROVIDER -module pilot/PROVIDER
```

Real `go/types` against the SDK version the provider pins — accessor chains, method
signatures, return arities, response models, every attribute's `sdkField`, for all five
block kinds, counted per kind. Run it before ever emitting: it turns a pile of identical
compile errors in generated code into one message naming the blueprint field to edit.

## 6. Emit, build, and wire the hand-written shell

```sh
tfpluginframeworkgen emit -blueprint blueprints/PROVIDER -out pilot/PROVIDER -dry-run
tfpluginframeworkgen emit -blueprint blueprints/PROVIDER -out pilot/PROVIDER
cd pilot/PROVIDER && go build ./... && go test ./...
```

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
TFPFGEN_PROBE_TOKEN=$(security find-generic-password -s tfpfgen-probe -w) \
TFPFGEN_PROBE_ENDPOINT=https://api.PROVIDER.com/v7 \
tfpluginframeworkgen probe -blueprint blueprints/PROVIDER -resource THING \
  -mode record --allow-mutations \
  -plan blueprints/PROVIDER/THING.probe.plan.json \
  -profile .tfpluginframeworkgen/sandbox/PROVIDER.json
```

One resource per invocation — a plan speaks one schema's wire vocabulary. Check the
budget first with `-list`; every guard is required and every refusal names all unmet
conditions at once. What the fixtures decide:

- **One fixture per branch.** The dynamic-tag findings exist because a second fixture
  reached a branch the first could not. A field only one fixture declares is probed
  inside that fixture's body (never an arbitrary one), and its facts are scoped to the
  gates every attempt shared — measured branches get facts, unmeasured ones get nothing.
- **Declared gates keep their fixture's values.** Everything else is synthesised from a
  pool that prefers observed-accepted values and never sends observed-refused ones.
- **Refused enum values escalate** into every other declared fixture before being called
  rejected; a disagreement one gate cleanly partitions becomes a fact per branch.

Then prove purity offline, exactly as CI will:

```sh
tfpluginframeworkgen probe -blueprint blueprints/PROVIDER -mode verify   # no network
```

## 8. Merge the evidence

```sh
tfpluginframeworkgen merge -blueprint blueprints/PROVIDER \
  -facts probe-evidence/PROVIDER/THING/SNAPSHOT/facts.json
```

Merge widens and annotates; it never narrows and it never resolves a disagreement
silently. Read all three sections of the output:

- **Changes** — behaviour written, including conditional variants
  (`behaviour.conditional[gate=value].…`) that emission acts on: `requiredWhen`
  validators, branch-aware assertions, fixture membership.
- **Conflicts** — the curated blueprint and the evidence disagree. Each names the fix.
  A stale unconditional fact contradicted by a branch observation is resolved by
  *removing the stale fact* and re-merging, so the branch truth applies.
- **Recommendations** — decisions merge refuses to make (RequiresReplace via committing
  `behaviour.immutable`, type changes from integrality, defaults). Committing the
  blueprint diff **is** the opt-in; review it as the decision it is.

Reconcile presence changes by hand where the evidence demands them (the findings doc
pattern: a field the API discards becomes `computed`; a gated field becomes writable
with its enum converter), then re-run merge until it reports nothing.

## 9. Acceptance, live

```sh
cd pilot/PROVIDER && TF_ACC=1 go test -count=1 -p 1 -run TestAcc ./...
```

Generated per kind: the resource lifecycle (create, import-verify, update, and a forced
replacement when an immutable attribute has a second usable value), seeded datasource
cross-checks, the list query, the action invoke with its re-asserted reversal, the
ephemeral echo. `-p 1` matters: packages otherwise run concurrently against one tenant.
Seed values are salted per consumer for the same reason.

Then let CI own it: the acceptance workflow is dispatch + weekly schedule, environment-
gated, and the only job that creates real objects.

## 10. The gates that keep it honest

Every PR: regenerate-and-diff, build and test toolkit + pilot, bindings, interop
round-trip, offline fact re-derivation with egress blocked, `merge -check`, terraform
validation of examples and fixtures. A resource with no evidence yet is a stated note;
a verify that proved nothing at all fails. When any of these disagrees with you, the
committed artefact is the arbiter — regenerate, re-derive, or re-record; never edit a
generated file or a cassette by hand.
