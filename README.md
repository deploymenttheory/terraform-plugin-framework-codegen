# terraform-plugin-framework-codegen

A toolkit that programmatically generates [terraform-plugin-framework][framework]
providers from an API specification **plus recorded API behaviour**, so that
state mapping is compile-checked generated code instead of runtime reflection.

> **Status: Phase 1b of 7.** Generation works end to end: a blueprint produces a
> provider that compiles, tests and plans. The stages that *infer* a blueprint —
> OpenAPI ingestion and live API probing — are registered and documented but not
> yet built, so blueprints are hand-authored today. See [Roadmap](#roadmap).

## Why

An OpenAPI document tells you what an API's fields are *called*. It does not
tell you the things that decide whether a Terraform provider actually works:

- which fields are genuinely writable, versus accepted and silently discarded
- which are immutable, and so need `RequiresReplace`
- what the server rewrites on the way in — case, whitespace, URL form, list
  order, timestamp format — every one of which is a perpetual diff
- what it defaults when you omit a field, and whether that default is a real
  constant or a derived value that must simply be `Computed`
- whether `PATCH` merges or replaces, whether create returns the object, and how
  long after create the thing is actually readable

A generator that trusts the specification produces providers with perpetual
diffs, spurious replacements, and `provider produced inconsistent result after
apply`. The alternative, historically, is discovering each fact one production
bug at a time and encoding it as a hand-maintained special case.

So this toolkit **pokes the live API and records what it does**. The probe
transcripts are committed as evidence, facts are re-derived from them offline in
CI, and they feed the generator alongside the specification.

## Pipeline

```
OpenAPI snapshot ──ingest──┐
                            ├──merge──► blueprint.json ──emit──► provider Go tree
live API ────────probe──────┤                                    + tests, mocks,
                            │                                      fixtures, docs
human overrides ────────────┘
```

Each arrow writes a **committed, reviewable artefact**. CI regenerates every one
of them and fails on drift, then builds and tests the result — because a
generator change can produce a clean diff and broken code.

A blueprint names SDK symbols as strings, so `tfpluginframeworkgen bindings` type-checks
them against the SDK the provider actually pins. That turns a wrong symbol from a
pile of identical compile errors in generated code into one message naming the
blueprint field to edit.

## Quick start

Steps 0 to 2 are not built yet, so a blueprint is hand-authored today. Everything
from step 5 onward works — that is the pilot's actual build.

```bash
# 0. pin an upstream spec snapshot
tfpluginframeworkgen specs -output-dir openapi-specs/thousandeyes

# 1. see what the spec offers before committing to anything
tfpluginframeworkgen ingest -only Tags -list

# 2. infer a blueprint, bound against SDK methods that provably exist
tfpluginframeworkgen ingest -only Tags -out blueprints/thousandeyes

# 3. probe a sandbox, recording evidence. Every guard is required.
#    The token comes from TFPFGEN_PROBE_TOKEN and never from a flag or the profile.
tfpluginframeworkgen probe -blueprint blueprints/thousandeyes -resource tag \
  -mode record --allow-mutations -plan blueprints/thousandeyes/probe.plan.json \
  -profile .tfpluginframeworkgen/sandbox/thousandeyes.json

# 3b. re-derive the same facts from the committed transcript, with no network at all
tfpluginframeworkgen probe -blueprint blueprints/thousandeyes -mode verify

# 4. fold the evidence in; conflicts with the spec are surfaced, never resolved silently
tfpluginframeworkgen merge -blueprint ... -facts ... -strategy annotate

# 4b. check every SDK symbol the blueprint names actually exists
tfpluginframeworkgen bindings -blueprint blueprints/thousandeyes -module pilot/thousandeyes

# 5. emit. Dry run first, always.
tfpluginframeworkgen emit -blueprint blueprints/thousandeyes -out pilot/thousandeyes -dry-run
tfpluginframeworkgen emit -blueprint blueprints/thousandeyes -out pilot/thousandeyes

# 6. the actual proof
cd pilot/thousandeyes && go build ./... && go test ./... && terraform plan

# 7. the CI gate
tfpluginframeworkgen verify -blueprint blueprints/thousandeyes -out pilot/thousandeyes
tfpluginframeworkgen probe  -blueprint blueprints/thousandeyes -mode verify   # no network
```

## What is generated and what is yours

The boundary is the most important thing to understand about a generated
provider. It is enforced four ways: a per-file header, the emission manifest,
`.gitattributes`, and `tfpluginframeworkgen verify`.

| Path | Owner | Change it by |
|---|---|---|
| `internal/services/**/{resource,model,construct,state,crud}.go` | toolkit | editing the blueprint, then `emit` |
| `internal/services/**/{modify_plan,validate}.go` | **you** | editing them; `emit` never touches them again |
| `internal/services/**/mocks/`, `tests/` | toolkit | re-probing, then `emit` |
| `internal/provider/{resources,datasources,provider}.go` marked regions | toolkit | `emit -register` |
| `internal/provider/configure_clients.go`, `internal/client/` | **you** | editing them — auth is always bespoke |
| `internal/services/common/convert/` | toolkit | editing the templates |
| `internal/services/common/{crud,errors,schema}/` | **you** | editing them |

Generated files carry exactly this header, and nothing else may:

```go
// Code generated by tfpluginframeworkgen from blueprints/<path> (sha256:…). DO NOT EDIT.
```

There is deliberately **no** preserved-region mechanism inside a generated file:
ownership is all-or-nothing per file. A file that genuinely cannot be generated is
scaffolded once and then yours, and `emit` never touches it again.

[`docs/generated-boundary.md`](docs/generated-boundary.md) covers how the boundary
is enforced, orphan detection, and the escape hatch.

## Repository layout

| Path | Contents |
|---|---|
| `cmd/tfpluginframeworkgen/` | the one installable binary; stdlib `flag` subcommand dispatch |
| `internal/blueprint/` | the IR, its validation, and the layered-merge engine |
| `internal/ingest/` | OpenAPI → blueprint |
| `internal/interop/` | reads and writes `terraform-plugin-codegen-spec` v0.1 |
| `internal/probe/` | the API behaviour prober |
| `internal/cassette/` | HTTP record/replay, redaction, deterministic canonicalisation |
| `internal/emit/`, `internal/render/` | blueprint → Go; all logic lives in `render` |
| `internal/sdkbind/` | type-checks blueprint bindings against the pinned SDK |
| `internal/manifest/` | what the last run produced, so orphaned files can be found |
| `internal/templates/` | embedded `.tmpl` files — the emitted shape, as reviewable text |
| `blueprints/` | committed blueprints, one directory per provider |
| `probe-evidence/` | committed probe cassettes and derived facts |
| `openapi-specs/` | pinned, immutable specification snapshots |
| `pilot/thousandeyes/` | a nested module: a fully generated provider, built and plan-tested in CI |
| `docs/` | architecture, CLI reference, and the new-API onboarding runbook |

## Relationship to HashiCorp's code generation tooling

This toolkit is **not** a wrapper around `tfplugingen-openapi` or
`tfplugingen-framework`. Those generate a schema and a model struct and stop:
they emit no CRUD logic at all, and they cannot express `dynamic` attributes,
`int32`/`float32`, blocks, resource identity, or write-only attributes. Their
renderers live under `internal/`, so there is nothing to import.

What this project does adopt is the **Provider Code Specification** as an
interop format: `tfpluginframeworkgen interop` reads and writes v0.1 JSON, so
`tfplugingen-openapi` output can be ingested and the schema slice can be handed
to other tools. Everything that format cannot carry — CRUD wiring, SDK binding,
observed behaviour, test scaffolding — lives in this project's own richer IR.

## Roadmap

| Phase | Delivers | State |
|---|---|---|
| 0 | module, CLI skeleton, CI gates | **done** |
| 1 | walking skeleton: one resource, hand-authored blueprint → `terraform plan` | **done** |
| 1b | nested attributes | **done** |
| 2 | `ingest`: OpenAPI → the same blueprint, byte-identical | **done** |
| 3 | `terraform-plugin-codegen-spec` v0.1 interop | **done** |
| 4 | the prober: record, replay, gating, cleanup | next |
| 5 | tests, mocks and fixtures derived from probe evidence | |
| 6 | breadth — ~20 resources, docs, weekly spec refresh | |
| 7 | a second API, proving nothing is pilot-shaped | |

Deferred beyond v0.1.0: ephemeral resources, actions, list resources,
provider-defined functions, state upgraders.

## Limitations

- **Probing needs a sandbox tenant and consumes its quota.** Mutating probes
  refuse to run unless the profile asserts, at runtime, that it really is a
  sandbox — `sandbox: true` is a claim, and the assertions are the evidence.
  Read-only probing is safe anywhere.
- **A mutating run can still leave something behind, and says so.** The ledger
  records every create before it is issued, so an object whose response was never
  seen is still findable; the sweeper removes by identifier and then by name
  prefix. Anything left is reported with a runnable `curl` and exits 5 even if
  every fact was gathered. See [docs/probing.md](docs/probing.md).
- **A wrong fact is worse than no fact.** Inferred field interdependencies and
  scraped enum values are emitted as documentation and commented-out validators,
  never as active constraints — an over-tight validator rejects configurations
  the API would have accepted, and the user cannot work around it.
- **The prober cannot learn everything.** Licence-gated behaviour, cross-object
  constraints, RBAC, production latency, and whether a field is *semantically* a
  secret all need a human. The probe plan's deny list is where that boundary is
  drawn honestly.
- **`ingest` refuses partial resources by default.** A resource whose CRUD set is
  incomplete is a curation decision, not something to guess at.
- **Nesting is generated to any depth** the blueprint declares. Two things are refused,
  naming the offending attribute: two nested objects that would declare the same Go
  identifier, and nesting past ten levels — a runaway guard, since a schema deeper than
  that is usually one whose depth is decided at runtime and so is not expressible here.
- **Two attribute decisions in the pilot are unprobed guesses**, recorded as such
  in the blueprint's own descriptions: whether `color`, `access_type` and
  `match_type` really carry server defaults, and whether `legacy_id` is
  integral despite the specification typing it as a number. Phase 4 settles both.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). The one rule specific to this
repository: **generated artefacts are regenerated, never hand-edited.** Change
the blueprint, the templates, or the generator, and re-run. CI enforces this.

## License

[MIT](LICENSE).

[framework]: https://developer.hashicorp.com/terraform/plugin/framework
