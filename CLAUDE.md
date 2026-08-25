# Working in this repository

This repo is **tfpfgen v2**: the toolkit that turns an OpenAPI document into a
terraform-plugin-framework provider. It owns every executable part of the
system — the CLI, the reusable GitHub Actions workflows, the templates, the
config schema. Provider repos and the template repo reference this repo's
behavior by pinned tag; they never copy it.

This is the ground-up rewrite. The v1 repo
(`terraform-plugin-framework-codegen`, no suffix) is the failed attempt it
replaces — mine it for lessons, never for vocabulary or defaults.

## Naming is user-owned

Every domain term in this repo was individually approved by the repository
owner and lives in `docs/glossary.md`. Before coining any new term — a verb, a
noun, a directory, a file name, a workflow name, an `x-tfpfgen-*` key — present
options to the owner and record the decision in the glossary. Never reintroduce
v1 vocabulary (probe, cassette, recording, scenario, blueprint, draft, merge,
sweep, doctor, facts, rehearsal, curate).

## Library mandates

Set by the repository owner; not open to per-PR relitigating:

- **The CLI is driven by cobra and viper.** New verbs are cobra commands;
  tfpfgen.yaml is read through viper. Strict decoding stays: unknown keys
  error with a did-you-mean suggestion, and the semantic validation plus the
  fixed `TFPFGEN_AUTH_*` secrets contract live in `internal/config`.
- **All logging uses zerolog** (`github.com/rs/zerolog`) — never a
  hand-rolled logger, never zap.
- Any other cross-cutting library choice (HTTP client, test framework, …)
  goes to the repository owner before it lands.

## Hard rules

- **Providers consume tags only, never `main`.** Every behavior change to a
  reusable workflow (`.github/workflows/NN-*.yml`), a template, or the config
  schema is a contract change and follows semver. Compatible releases
  fast-forward the moving `v1` tag; breaking changes cut `v2`.
- **No provider-specific values in code or defaults.** No vendor name, path,
  header, or endpoint from any pilot API may appear as a default. CI greps for
  this; treat a hit as a build failure, not a style nit.
- **Templates carry shape, Go carries logic.** Templates branch on presence,
  never on meaning. Recursion is handled in Go. Every value a template consumes
  is a finished expression, declaration, or boolean.
- **The coverage gate is not advisory.** CI fails below 90% total (80% per core
  package). A PR that lowers coverage does not merge. Packages ship with their
  tests in the same PR — no "tests to follow".
- **Never commit generated pilot output or binaries.** Generated provider trees
  live in provider repos; this repo holds only the machinery and its fixtures.
- **Every verb keeps its exit-code contract** documented in `docs/contract.md`.
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

## Pull requests

Create pull requests; do not merge them. The repository owner merges. Every
change goes on its own branch cut fresh from `main`.

## Verifying claims

Before stating that something regenerates unchanged, run the command that would
show it changing. Claims about generated output are verified at the layer they
are about. `main` must be actually measured: CI runs on push as well as PR.
