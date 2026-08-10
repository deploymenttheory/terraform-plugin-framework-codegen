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
- **Corpus documents are pinned by hash and fetched, not vendored.**
- **No file over 800 lines.** v1's 3,556-line probe file is the cautionary
  tale; decompose by protocol instead.

## Pull requests

Create pull requests; do not merge them. The repository owner merges. Every
change goes on its own branch cut fresh from `main`.

## Verifying claims

Before stating that something regenerates unchanged, run the command that would
show it changing. Claims about generated output are verified at the layer they
are about. `main` must be actually measured: CI runs on push as well as PR.
