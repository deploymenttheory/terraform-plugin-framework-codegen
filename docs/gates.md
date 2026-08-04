# The gates

Every artefact in the pipeline is committed, and every arrow between artefacts is
drift-gated in CI. This page lists each gate, what it proves, and the exact local
command that reproduces it — because the correct response to any red gate is to
run the pipeline stage that owns the artefact, never to hand-edit the artefact
into passing.

The gates live in five workflows. The job names below are verbatim, so a failing
check on a PR can be matched to its section here.

## `go | Verify generated code` (`codegen-verify.yml`)

Five jobs, one per arrow.

### 🔁 Regenerate and diff

Re-emits the pilot from the committed blueprints and fails on any `git diff`.
Then builds and tests the toolkit *and* the emitted provider, and asserts the
emitted Go is a gofumpt fixed point. Proves: committed provider ⟵ committed
blueprints, with no hand edits in between.

```bash
go run ./cmd/tfpluginframeworkgen emit -blueprint blueprints/thousandeyes -out pilot/thousandeyes
git diff --exit-code
go build ./... && go test ./...
(cd pilot/thousandeyes && go build ./... && go test ./...)
```

Red here means a blueprint changed without re-running `emit`, or a generated file
was edited by hand. Regenerate and commit; the hand edit belongs in the blueprint
or in a hook (see [generated-boundary.md](generated-boundary.md)).

### 🔗 Verify SDK bindings

Type-checks every binding in the blueprints against the SDK version the pilot's
`go.mod` pins, and re-derives the static facts document against it.

```bash
go run ./cmd/tfpluginframeworkgen bindings \
  -blueprint blueprints/thousandeyes \
  -module pilot/thousandeyes \
  -facts-check blueprints/thousandeyes/static.facts.json
```

Red here usually means an SDK version bump: a method, model or struct tag moved
underneath the blueprints. Fix the bindings, then regenerate the static facts
(`-facts-out`) and re-merge them.

### 🔀 Round-trip through tfplugingen-framework

Exports the blueprints as codegen-spec v0.1, diffs the committed export, and
feeds it to HashiCorp's `tfplugingen-framework` to prove the export is one their
tooling accepts. See [interop.md](interop.md).

```bash
go run ./cmd/tfpluginframeworkgen interop export \
  -blueprint blueprints/thousandeyes \
  -out interop-specs/thousandeyes/provider-code-spec.json
git diff --exit-code -- interop-specs/
```

### 🔬 Re-derive probe facts offline

Replays every committed cassette with no network and no credentials, asserts the
re-derived facts equal the committed `facts.json`, then re-merges every facts file
under `-check` to assert the blueprints already reflect the evidence.

```bash
go run ./cmd/tfpluginframeworkgen probe -blueprint blueprints/thousandeyes -mode verify
for facts in probe-evidence/*/*/*/facts.json; do
  go run ./cmd/tfpluginframeworkgen merge \
    -blueprint blueprints/thousandeyes -facts "$facts" -check -accept-conflicts
done
```

Red on the first half means fact derivation is no longer a pure function of the
transcript (or evidence was edited); red on the second means evidence was
recorded but never merged. See [probing.md](probing.md).

### 🌍 Terraform validates the examples

The only job that runs Terraform itself. Regenerates the registry docs and fails
if they drift, asserts committed HCL is a `terraform fmt` fixed point, then
builds the provider, points Terraform at it with `dev_overrides`, and validates
the committed examples against the real schema. This is the job that catches an
example the generated validators reject.

```bash
(cd pilot/thousandeyes && go generate . && git diff --exit-code -- docs/)
terraform fmt -check -recursive -diff pilot/
```

The `emit` postcheck battery runs the first two of these at generation time (see
[cli.md](cli.md#the-postcheck-battery)), so this gate should only go red when
someone skipped the battery.

## The other workflows

| Workflow | Job | What it proves |
|---|---|---|
| `go \| Unit Tests` | 🧪 Run Unit Tests | the toolkit's own suite, with coverage |
| `go \| Linter` | ✨ Run golangci-lint | toolkit lint |
| `go \| Acceptance tests` | 🌍 Acceptance (live tenant) | the generated provider's full lifecycle against the real API |
| `Lint Codebase` (`linter.yml`) | super-linter | markdown, YAML, and everything else non-Go |
| `dependancy-review.yml` | dependency review | no known-vulnerable dependency lands via PR |
| `release-please.yml` | release-please | versioning and CHANGELOG from conventional commits |
| `pr-title-validation.yml` | PR title | conventional-commit PR titles, safely via `env` |
| `auto-merge-dependabot.yml` | auto-merge | patch-level dependabot PRs merge themselves once green |

## Acceptance

Acceptance is deliberately **not** a per-PR gate. It runs weekly and on manual
dispatch, gated on a GitHub environment, with a concurrency group so two live
runs can never fight over the same tenant. It creates and destroys real objects,
so it needs `TF_ACC=1` and the provider's live credentials; admin-scoped
resources additionally gate on `TFPFGEN_ACC_ADMIN`.

Locally, the equivalent is:

```bash
cd pilot/thousandeyes
TF_ACC=1 go test ./internal/services/... -run 'TestAcc' -timeout 60m -v
```

Acceptance is **confirmation, not discovery**: the probe has already rehearsed
the exact lifecycles these tests run (see
[fixtures-and-rehearsal.md](fixtures-and-rehearsal.md)). A red acceptance run
therefore means the evidence is incomplete or the API changed — the fix starts
with `probe`, not with the generated code.

## Exit codes

Every command's exit codes are a contract, listed in [cli.md](cli.md#exit-codes).
The one worth repeating here: `probe`'s precedence is **7 > 5 > 3 > 4 > 6 > 1**,
so a run that both exceeded its budget and left an orphan reports the orphan.
