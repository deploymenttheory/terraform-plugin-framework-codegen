# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This file is maintained by [release-please](.github/workflows/release-please.yml)
from conventional commit messages. Add entries by writing good commit messages,
not by editing this file by hand.

## Unreleased — Kiota SDK generation and the kiotaFluent dialect

`tfpfgen sdk generate` derives a Go SDK from a pinned OpenAPI snapshot with
Microsoft Kiota (PATH tool, hard version gate on the committed
kiota-lock.json; `-mode embed` into the provider module by default,
`-mode external` with its own go.mod; `-check` regenerates and
byte-compares). The reserved `kiotaFluent` dialect is implemented end to
end: blueprints record fluent chains as data (`Operation.chain`), the
emitter renders them with method access, nil-result guards and enum parse
companions, `bindings check` walks chains and Get/Set pairs against the
real SDK with did-you-mean, `blueprint draft -sdk-dialect kiotaFluent`
infers the whole shape from the snapshot, and `provider.sdk` gains
`mode`/`generator` with a go.mod assertion in the postcheck. A second
pilot, `pilot/thousandeyes-kiota`, binds the `tag` resource against an
embedded kiota SDK and re-derives identical facts from the shared
`recordings/thousandeyes` — the probe layer is wire-level, so switching
dialect is a binding change, not an evidence change.

## Unreleased — provider push

`tfpfgen provider push -out DIR -repo URL` publishes the generated provider
tree to its own repository: shallow-clone, sync, manifest-based pruning of
files the generator owned and no longer produces, a commit with stated
provenance on the generator-owned `tfpfgen/generate-<digest>` branch, and a
pull request against the default branch. No difference means exit 0 and
nothing pushed. The token comes from `TFPFGEN_GITHUB_TOKEN` (or
`GITHUB_TOKEN`) — env only, never a flag.

## Unreleased — Naming standard hard cut

Everything below was renamed in one release; no aliases were kept.

### CLI

The binary is now `tfpfgen` (was `tfpluginframeworkgen`), and the grammar is
noun-group throughout: `tfpfgen <noun> <verb>`. Flags are single-dash everywhere.

| Old | New |
|---|---|
| `tfpluginframeworkgen` (binary, `cmd/tfpluginframeworkgen`) | `tfpfgen` (`cmd/tfpfgen`) |
| `specs` | `openapi fetch` |
| `specs -output-dir` | `openapi fetch -out` |
| `ingest` | `blueprint draft` |
| `ingest -spec-root` | `blueprint draft -openapi-dir` |
| `ingest -spec` | `blueprint draft -openapi` |
| `ingest -only` | `blueprint draft -tag` |
| `ingest -list` | `blueprint draft -dry-run` |
| `ingest -all` | `blueprint draft -include-unusable` |
| `ingest -plan-drafts` | `blueprint draft -scenario-drafts` |
| `probe -mode record\|replay\|verify\|sweep` | `probe record\|replay\|verify\|sweep` (bare `probe` still defaults to replay) |
| `probe -list` | `probe list` |
| `probe -only` | `probe -probe` |
| `probe -evidence` | `probe -recordings` |
| `probe -plan` | `probe -scenario` |
| `probe -plan-dir` | `probe -scenario-dir` |
| `probe -no-rehearse` | `probe -skip-rehearsal` |
| `merge` | `blueprint merge` |
| `merge -accept-conflicts` | `blueprint merge -allow-conflicts` |
| `merge -snapshot-id` | `blueprint merge -recording` |
| `merge -promote-plans` | `blueprint merge -adopt-scenarios` |
| `merge -github-summary` | `blueprint merge -summary` |
| `emit` | `provider generate` |
| `emit -only` | `provider generate -resource` |
| `emit -list` | `provider generate -dry-run` |
| `verify` (command) | `provider generate -check` |
| `scaffold` | `provider scaffold resource\|data-source` (still not implemented) |
| `bindings` | `bindings check` |
| `bindings -facts-out` | `bindings facts -out` |
| `bindings -facts-check` | `bindings facts -check` |
| `interop export` | `spec export` |
| `interop export -only` | `spec export -resource` |
| `interop import` | `spec import` |
| `interop import -list` | `spec import -dry-run` |
| `-v` (global) | deleted — it never worked |
| `-config` (global) | deleted — it never worked |

`blueprint validate|diff|list`, `version`, `-q` and `-chdir`/`-C` are unchanged;
`spec import -spec` keeps its name and means the Provider Code Specification JSON.

### Repository paths

| Old | New |
|---|---|
| `openapi-specs/` | `openapi/` |
| `probe-evidence/` | `recordings/` |
| `interop-specs/` | `specs/` |
| `<key>.probe.plan.json` | `<key>.scenario.json` (drafts: `<key>.scenario.draft.json`) |
| `plan.json` (inside a recording) | `scenario.json` |
| `.tfpluginframeworkgen/` | `.tfpfgen/` |
| `docs/gates.md` | `docs/checks.md` |
| `docs/examples/probe-profile.example.json` | `docs/examples/sandbox-profile.example.json` |
| `.github/workflows/dependancy-review.yml` | `.github/workflows/dependency-review.yml` |

`blueprints/` and `pilot/` are unchanged, as are the `TFPFGEN_` environment prefix
and the `tfpfgen-probe` object prefix.

### Vocabulary and Go API

| Old | New |
|---|---|
| plan (the probe worksheet) | scenario — "plan" is banned outside `terraform plan` and `planModifiers` |
| snapshot / evidence (probe output) | recording — "snapshot" is the pinned OpenAPI document only |
| gate (CI job) | check |
| gate (sandbox admission) | guard |
| gate (API dispatch field) | dispatch field |
| emit | generate — render is internal template execution only |
| protocol (as a synonym for probe) | probe |
| archetype | scaffold template |
| archetype provider | reference provider |
| probe profile | sandbox profile |
| wave | batch |
| `internal/render` + `internal/emit` | `internal/generate` |
| `internal/ingest/openapi` | `internal/openapi` |
| `internal/specstore` | `internal/snapshot` |
| `internal/interop` | `internal/spec` |
| `probe.Plan` | `probe.Scenario` |
| `probe.Findings` | `probe.FactSet` |
| `cassette.Finding` | `cassette.Leak` |
| `cassette.Write` | `cassette.Record` |
| `specstore.Pin` | `snapshot.Pin` |
| `emit.Plan` | `generate.Fileset` |
| `GateOptions` | `GuardOptions` |
| `interop.Note` | `spec.Loss` |
| unprefixed enum constants | type-prefixed: `ConfidenceObserved`, `BlockKindResource`, `EntryKindIntent`, `ProbeKindRead`, … |
| `ResourceName` / `DataSourceName` / `EphemeralName` / `ActionName` (generated registration constant) | `TypeName` |
| blueprint keys `alerts_rule`, `dashboards_filter`, `TestsDnssec` | `alert_rule`, `dashboard_filter`, `TestsDNSSEC` (SDK-side names keep the SDK's spelling) |
