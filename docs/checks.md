# The checks

Every artefact in the pipeline is committed, and every arrow between artefacts is
drift-checked in CI. This page lists each check, what it proves, and the exact local
command that reproduces it — because the correct response to any red check is to
run the pipeline stage that owns the artefact, never to hand-edit the artefact
into passing.

The checks live in the workflows under `.github/workflows/`. The job names below
are verbatim, so a failing check on a PR can be matched to its section here.

## `go | Verify generated code` (`codegen-verify.yml`)

Five jobs, one per arrow.

### 🔁 Regenerate and diff

Regenerates the pilot from the committed blueprints and fails on any `git diff`.
Then builds and tests the toolkit *and* the generated provider, and asserts the
generated Go is a gofumpt fixed point. Proves: committed provider ⟵ committed
blueprints, with no hand edits in between.

```bash
go run ./cmd/tfpfgen provider generate -blueprint blueprints/thousandeyes -out pilot/thousandeyes
git diff --exit-code
go build ./... && go test ./...
(cd pilot/thousandeyes && go build ./... && go test ./...)
```

Red here means a blueprint changed without re-running `provider generate`, or a
generated file was edited by hand. Regenerate and commit; the hand edit belongs in
the blueprint or in a hook (see [generated-boundary.md](generated-boundary.md)).

### 🔗 Verify SDK bindings

Type-checks every binding in the blueprints against the SDK version the pilot's
`go.mod` pins, and re-derives the static facts document against it.

```bash
go run ./cmd/tfpfgen bindings check \
  -blueprint blueprints/thousandeyes \
  -module pilot/thousandeyes
go run ./cmd/tfpfgen bindings facts \
  -blueprint blueprints/thousandeyes \
  -module pilot/thousandeyes \
  -out blueprints/thousandeyes/static.facts.json -check
```

Red here usually means an SDK version bump: a method, model or struct tag moved
underneath the blueprints. Fix the bindings, then regenerate the static facts
(`bindings facts -out`, without `-check`) and re-merge them.

### 🔀 Round-trip through tfplugingen-framework

Exports the blueprints as the Provider Code Specification (codegen-spec v0.1),
diffs the committed export, and feeds it to HashiCorp's `tfplugingen-framework`
to prove the export is one their tooling accepts. See [interop.md](interop.md).

```bash
go run ./cmd/tfpfgen spec export \
  -blueprint blueprints/thousandeyes \
  -out specs/thousandeyes/provider-code-spec.json
git diff --exit-code -- specs/
```

### 🔬 Re-derive probe facts offline

Replays every committed recording with no network and no credentials, asserts the
re-derived facts equal the committed `facts.json`, then re-merges every facts file
under `-check` to assert the blueprints already reflect the evidence.

```bash
go run ./cmd/tfpfgen probe verify -blueprint blueprints/thousandeyes
for facts in recordings/*/*/*/facts.json; do
  go run ./cmd/tfpfgen blueprint merge \
    -blueprint blueprints/thousandeyes -facts "$facts" -check -allow-conflicts
done
```

Red on the first half means fact derivation is no longer a pure function of the
transcript (or a recording was edited); red on the second means a recording was
made but never merged. See [probing.md](probing.md).

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

The `provider generate` postcheck battery runs the first two of these at
generation time (see [cli.md](cli.md#the-postcheck-battery)), so this check
should only go red when someone skipped the battery.

## The other workflows

| Workflow | Job | What it proves |
|---|---|---|
| `go \| Unit Tests` (`unit-tests.yml`) | 🧪 Run Unit Tests | the toolkit's own suite, with coverage |
| `go \| Linter` (`go-lint.yml`) | ✨ Run golangci-lint | toolkit lint |
| `go \| Acceptance tests` (`acceptance.yml`) | 🌍 Acceptance (live tenant) | the generated provider's full lifecycle against the real API |
| `Linter` (`linter.yml`) | ✨ Linter | super-linter over markdown, YAML, and everything else non-Go |
| `Dependency Review` (`dependency-review.yml`) | 🔎 Dependency Review | no known-vulnerable dependency lands via PR |
| `release-please` (`release-please.yml`) | 🔖 Release Please | versioning and CHANGELOG from conventional commits |
| `PR Title Validation` (`pr-title-validation.yml`) | ✅ Validate PR Title | conventional-commit PR titles, safely via `env` |
| `Auto-Merge Dependabot` (`auto-merge-dependabot.yml`) | 🤖 Auto-Merge Dependabot | patch-level dependabot PRs merge themselves once green |

## Acceptance

Acceptance is deliberately **not** a per-PR check. It runs weekly and on manual
dispatch, behind a GitHub environment, with a concurrency group so two live
runs can never fight over the same tenant. It creates and destroys real objects,
so it needs `TF_ACC=1` and the provider's live credentials; admin-scoped
resources additionally require `TFPFGEN_ACC_ADMIN`.

Locally, the equivalent is:

```bash
cd pilot/thousandeyes
TF_ACC=1 go test -v -count=1 -p 1 -run 'TestAcc' -timeout 90m ./...
```

Acceptance is **confirmation, not discovery**: the probe has already rehearsed
the exact lifecycles these tests run (see
[fixtures-and-rehearsal.md](fixtures-and-rehearsal.md)). A red acceptance run
therefore means the evidence is incomplete or the API changed — the fix starts
with `probe record`, not with the generated code.

## Exit codes

Every command's exit codes are a contract, listed in [cli.md](cli.md#exit-codes).
The one worth repeating here: `probe`'s precedence is **7 > 5 > 3 > 4 > 6 > 1**,
so a run that both exceeded its budget and left an orphan reports the orphan.
