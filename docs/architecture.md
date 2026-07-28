# Architecture

The toolkit turns a **blueprint** into a terraform-plugin-framework provider. This
describes the stages, and — more usefully — where logic is allowed to live and why
those boundaries are drawn where they are.

## The pipeline

```
OpenAPI snapshot ──ingest──┐
                            ├──merge──► blueprint.json ──emit──► provider Go tree
live API ────────probe──────┤                                    + tests, mocks,
                            │                                      fixtures, docs
human overrides ────────────┘
```

Every arrow's output is a committed, reviewable artefact. That is deliberate: a
pipeline whose intermediate state lives only in memory can only be reviewed by
reading its output, and its output is thousands of lines of generated Go.

Stages marked *planned* are designed for but not built.

| Stage | Package | State |
|---|---|---|
| `specs` — fetch and pin an OpenAPI snapshot | `internal/specstore` | planned (Phase 2) |
| `ingest` — OpenAPI → blueprint | `internal/ingest/openapi` | planned (Phase 2) |
| `probe` — live API → behaviour facts | `internal/probe` | planned (Phase 4) |
| `merge` — fold facts into a blueprint | `internal/blueprint/merge` | planned (Phase 4) |
| `emit` — blueprint → provider | `internal/emit`, `internal/render`, `internal/templates` | **built** |
| `verify` — fail on drift | `cmd/tfpluginframeworkgen/verify.go`, `internal/manifest` | **built** |
| `bindings` — type-check bindings against the SDK | `internal/sdkbind` | **built** |
| `interop` — Provider Code Specification v0.1 | `internal/interop` | **built** (export; import writes drafts) |

## Where logic is allowed to live

This is the load-bearing rule, and most of the structure follows from it.

**All logic lives in `internal/render`.** Every value a template consumes is a
finished Go expression, a finished declaration, or a boolean. Templates branch on
*presence* — "is there a description?" — never on *meaning* — "what kind of
attribute is this?".

The reason is reviewability. `internal/templates` is the emitted shape written as
ordinary text, so you can read what the generator produces without reading the
generator. A template containing type dispatch destroys that: the shape becomes
something you have to simulate mentally rather than read.

The convention is inherited from the sibling SDK generator, whose
`service.go.tmpl` consumes only precomputed strings.

If a template needs to decide something, the decision belongs in `render`.

**Recursion is handled in Go, not in templates.** A Terraform schema is recursive,
and the obvious response — one recursive template — would put the shape back
inside logic. Instead `render` walks the attribute tree and produces finished
declarations, which the file template merely lists.

## Package map

```
cmd/tfpluginframeworkgen/     CLI. stdlib flag, one FlagSet per subcommand, no cobra.
internal/
  blueprint/           the IR: types, validation, canonical JSON, layered load
  render/              ALL logic. Blueprint -> finished strings.
  templates/           embedded .tmpl. The emitted shape, as reviewable text.
  emit/                plan, format, write. Owns gofumpt and overwrite refusal.
  manifest/            what the last run produced, so orphans can be found
  naming/              identifiers. One word-splitter, several joins.
  sdkbind/             type-checks bindings against the SDK actually pinned
  version/             the tool version, in exactly one place
```

`naming` and `blueprint` depend on nothing else in the repo. `render` depends on
`blueprint` and `naming`. `emit` depends on `render` and `templates`. Nothing
depends on `cmd`.

## Determinism

Generated output must be byte-identical for identical inputs. The drift gate
regenerates and fails on any diff, so a single unstable value would make CI fail
on runs that changed nothing — and a CI failure that everyone learns to ignore is
worse than no CI at all.

What that forbids, in emitted files and in the manifest:

- no timestamps
- no tool version (it lives in `.tfpluginframeworkgen/manifest.json`, and nowhere else,
  so a release is a one-line diff rather than a rewrite of the tree)
- no absolute paths
- no reliance on Go map iteration order; every map is rendered through a sorted
  key slice, and every IR slice is sorted by a declared key before rendering

`TestUnit_Emit_IsDeterministic` builds the pilot twenty-five times and compares.
Twenty-five rather than two, because map iteration order varies per iteration and
two runs can agree by luck.

## Formatting

Emitted code is formatted with **gofumpt**, not `go/format`.

This is not a style preference. The target repositories run gofumpt, gci and
golines with autofix enabled. Output that is merely gofmt-clean gets silently
rewritten the first time a developer opens the file, and then shows up as drift
with no source change — which reads as a generator bug and is not one.

Import blocks are emitted pre-grouped into the three sections the house `gci`
configuration expects: standard library, third party, then `deploymenttheory`.

Each emitted file gets its **own** import set. A shared block would fail to
compile in most of them, since Go rejects unused imports.

## Failure behaviour

The generator fails loudly rather than emitting something approximate.

- An attribute whose type has no framework mapping is a hard error, not a skipped
  attribute. Silently omitting one produces a provider that looks complete and
  cannot express part of the API.
- An unresolvable conversion is a hard error listing every unresolved pair, not
  just the first.
- Nesting deeper than the emitter supports is refused, naming the offending
  attribute.
- A template that renders unparseable Go returns the numbered source with the
  error, because a formatter reports a line and column and matching that against
  unnumbered output is needless work.
- `emit` refuses to overwrite a file that is not marked generated.

## Further reading

- [`blueprint.md`](blueprint.md) — the IR, field by field
- [`generated-boundary.md`](generated-boundary.md) — what is generated, what is
  yours, and how the two are kept apart
- [`cli.md`](cli.md) — command reference
