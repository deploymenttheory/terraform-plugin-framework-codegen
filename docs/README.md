# Documentation

Toolkit documentation. Start with the [repository README](../README.md) for what
this project is and why it exists.

Documents are added as the phase that needs them lands, rather than up front as
empty stubs — a stub describing an unbuilt design is worse than no document,
because it reads as settled.

| Document | Contents | Lands in |
|---|---|---|
| `architecture.md` | the pipeline stages, and where logic is allowed to live | Phase 1 |
| `cli.md` | full CLI reference, generated from the subcommand usage strings | Phase 1 |
| `blueprint.md` | the IR: every field, and what it exists to express | Phase 1 |
| `generated-boundary.md` | how generated and hand-written code stay apart, and the escape hatch | Phase 1 |
| `onboarding-a-new-api.md` | numbered runbook for taking on a new API end to end | Phase 2 |
| `interop.md` | reading and writing Provider Code Specification v0.1, and what it cannot carry | Phase 3 |
| `probing.md` | the probe catalogue, confidence levels, safety model and cleanup guarantees | Phase 4 |
| `pilot-thousandeyes.md` | the pilot: what was generated, and the before/after against the existing provider | Phase 6 |
| `adr/` | decision records, so settled questions are not re-litigated in six months | ongoing |

## Decisions already taken

Recorded here until `adr/` exists, because they shape everything else and are the
questions most likely to be asked again.

1. **Provider layer only.** The toolkit generates the Terraform provider. SDKs
   already exist and are generated elsewhere; this project binds to them.
2. **Own IR, HashiCorp's specification as an interop format.** The Provider Code
   Specification cannot express CRUD wiring, SDK binding, observed behaviour or
   test scaffolding, so it is a format this project reads and writes — not its
   model.
3. **Full-lifecycle probing, gated and recorded.** Mutating probes require an
   explicit flag *and* a profile that proves at runtime that it is a sandbox.
   Every transcript is committed, and facts are re-derived from it offline in CI.
4. **The pilot is a new provider, not a migration.** Generating onto a clean
   slate keeps state-upgrade work off the toolkit's critical path.

## Conventions worth knowing before reading the code

- **Templates carry no logic.** Everything a template consumes is a finished
  string or a boolean, precomputed in `internal/render`. Templates branch on
  presence, never on meaning. This is why the emitted shape can be reviewed as
  ordinary text without reading the generator.
- **Generated output is deterministic.** No timestamps, no tool version, no
  absolute paths, no map-iteration order. Two runs over the same inputs produce
  byte-identical trees, which is the only thing that makes the drift check
  meaningful.
- **Nothing is deleted silently.** A merge layer may add to a blueprint but only
  the hand-authored override layer may remove from it. A probe run against a
  tenant that can see nothing therefore cannot quietly delete half a schema.
