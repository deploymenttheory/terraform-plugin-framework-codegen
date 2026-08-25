# Working in this repository

This repo is **tfpfgen**: the toolkit that turns an OpenAPI document into a
terraform-plugin-framework provider. It owns every executable part of the
system — the CLI, the reusable GitHub Actions workflows, the templates, the
config schema. Provider repos and the template repo reference this repo's
behavior by pinned tag; they never copy it.

An earlier attempt at the same problem exists. Mine it for lessons, never for
vocabulary or defaults.

## Naming is user-owned

Every domain term in this repo was individually approved by the repository
owner and lives in `docs/glossary.md`. Before coining any new term — a verb, a
noun, a directory, a file name, a workflow name, an `x-tfpfgen-*` key — present
options to the owner and record the decision in the glossary. The retired terms
`cassette`, `blueprint` and `doctor` may not reappear.

Retirement covers the domain noun, not the English word. Code that records a
reason, a binder that drafts a call, a loader that merges `allOf` — that is
prose describing what the code does, and it stays.

## Library mandates

Set by the repository owner; not open to per-PR relitigating:

- **The CLI is driven by cobra and viper.** New verbs are cobra commands;
  tfpfgen.yaml is read through viper. Strict decoding stays: unknown keys
  error with a did-you-mean suggestion, and the semantic validation plus the
  fixed `TFPFGEN_AUTH_*` secrets contract live in `internal/config`.
- **All logging uses zerolog** (`github.com/rs/zerolog`) — never a
  hand-rolled logger, never zap.
- **Four absences are already decided, and each is a choice rather than a
  gap.** There is no OpenAPI library: `internal/specmodel` is a hand-written
  reader over `yaml.Node`, because document order is load-bearing for the SDK
  generators and a decoded map loses it. There is no test framework — stdlib
  `testing`. There is no HTTP client — stdlib `net/http`. There is no mocking
  library: `internal/quirkserver` is a real server that misbehaves on purpose.
- Any other cross-cutting library choice goes to the repository owner before
  it lands.

A test is named `TestUnit_<Area>_<Behaviour>` or
`TestIntegration_<Area>_<Behaviour>`.

## Hard rules

- **Providers consume tags only, never `main`.** Every behavior change to a
  reusable workflow (`.github/workflows/NN-*.yml`), a template, or the config
  schema is a contract change and follows semver. Until the 1.0.0 freeze a
  compatible release fast-forwards the moving `v0` tag: provider repos pin an
  exact tag in `generator.version`, and caller workflows pin `@v0`.
- **No provider-specific values in code or defaults.** No vendor name, path,
  header, or endpoint from any pilot API may appear as a default. CI greps for
  this; treat a hit as a build failure, not a style nit. The grep cannot name
  `github`, which is the module host, so that one pilot rests on the author.
- **Templates carry shape, Go carries logic.** Templates branch on presence,
  never on meaning. Recursion is handled in Go. Every value a template consumes
  is a finished expression, declaration, or boolean.
- **The coverage gate is not advisory.** CI fails below 90% total, or below
  80% for any single package under `internal/` — which is the whole of what
  the profile covers. A PR that lowers coverage does not merge. Packages ship
  with their tests in the same PR — no "tests to follow". The gate a generated
  provider repo runs is the total alone.
- **The hygiene gate enforces three rules on every push**
  (`scripts/repo_hygiene_gate.sh`): no hand-written Go file over 800 lines, no
  pilot vendor name in non-test source, and no tracked file over 1 MiB.
- **Never commit generated pilot output or binaries.** Generated provider trees
  live in provider repos; this repo holds only the machinery and its fixtures.
- **Exit codes are 0, 1 and 2** — success, a verb that ran and refused, and an
  invocation that was misspelt. The table in `docs/contract.md` is the
  contract, and a new code is appended rather than renumbering one.
- **Vendor OpenAPI specs are committed and embedded.** The third-party
  documents the tests parse live in `internal/vendor_openapi_specs`, taken as
  the vendor published them. They are test input and nothing else: never
  imported, corrected, revised or generated from. Replacing one is deliberate
  — download over the file, then update the version and count its consumers
  assert, which exist so a replacement is noticed rather than absorbed.
- **No hand-written file over 800 lines.** Decompose by protocol instead.
  Generated and fixture code under `testdata/` is exempt — it is produced,
  not maintained, and reproducing it is cheap.
- **Comments say what and why. Nothing else.** A comment is read by someone
  changing this code in two years. It must tell them what the code does and
  why it does it that way, and nothing that was only true on the day it was
  written.

  Forbidden: what the code used to do, which pull request changed it, how a
  bug was found, counts or measurements from a particular run ("42
  attributes across the three pilots", "the first live run opened
  fifty-seven"), war stories about a previous version, and any sentence
  whose subject is the author or the project rather than the code.

  ```go
  // No — history, and an anecdote that expires.
  // Aborting the whole run instead was the old behaviour, and one entity
  // took every other entity with it; that single shape emitted nothing at
  // all for the provider.

  // Yes — what, then why.
  // Refuses one entity rather than the run: an unrenderable shape is a
  // fact about that entity, and the rest still generate.
  ```

  Rationale stays when it constrains future change. "Matched on the package
  path, because another SDK's type of the same name would not parse through
  this one" is why, and belongs. The same fact told as a story does not.
  Git history is where change is recorded; a pull request body is where a
  measurement belongs.

## Generated and measured files

Some files here are outputs. Editing one by hand is a defect rather than a
change:

- `docs/config.md` is rendered from the `internal/config` structs. Regenerate
  it with `UPDATE_DOCS=1 go test ./internal/config -run
  TestUnit_Config_ReferenceMatchesDocs`. A key with no description and a
  description with no key both fail that test, so the schema and its reference
  cannot drift apart.
- `docs/emittance_tracker.md` is the only place counts of what the toolkit
  emits and refuses may live. A count is a fact about one toolkit commit
  against one pinned document; stating one in a readme, a comment or another
  doc puts it somewhere it cannot be re-measured, where it goes stale
  invisibly.
- `spec/revised.yaml`, the intermediate representation, and every file of a
  generated provider tree except the three authored paths — `tfpfgen.yaml`,
  `spec/corrections/**` and `audit/inputs.json`.

## Pull requests

Create pull requests; do not merge them. The repository owner merges. Every
change goes on its own branch cut fresh from `main`.

## Verifying claims

Before stating that something regenerates unchanged, run the command that would
show it changing. `make check` mirrors CI step for step, except that it leaves
golangci-lint to CI.

Claims about generated output are verified at the layer they are about: against
a generated tree, never by grepping this repo for the symbol. An emitter builds
most of what it emits — `internal/emit/render_constraints.go` spells a pair of
bounds as `fmt.Sprintf("%sBetween(%v, %v)", …)` — so a grep here for
`int64validator.Between` finds nothing while the generator emits it.

`main` must be actually measured: CI runs on push as well as PR.
