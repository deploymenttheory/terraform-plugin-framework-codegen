# Documentation

Toolkit documentation. Start with the [repository README](../README.md) for what
this project is and why it exists.

Documents are added as the phase that needs them lands, rather than up front as
empty stubs — a stub describing an unbuilt design is worse than no document,
because it reads as settled.

| Document | Contents |
|---|---|
| [`architecture.md`](architecture.md) | the pipeline stages, and where logic is allowed to live |
| [`onboarding-a-new-api.md`](onboarding-a-new-api.md) | the numbered runbook for taking on a new API end to end |
| [`cli.md`](cli.md) | command reference, flags and exit codes |
| [`blueprint.md`](blueprint.md) | the IR: every field, and what it exists to express |
| [`probing.md`](probing.md) | the probe catalogue, confidence levels, safety model and cleanup guarantees |
| [`fixtures-and-rehearsal.md`](fixtures-and-rehearsal.md) | one fixture derivation, the rehearsal probe, and the fixpoint between them |
| [`generated-boundary.md`](generated-boundary.md) | how generated and hand-written code stay apart, and the escape hatch |
| [`gates.md`](gates.md) | every CI gate, what it proves, and its local reproduction |
| [`interop.md`](interop.md) | reading and writing Provider Code Specification v0.1, and what it cannot carry |
| [`findings/`](findings/) | investigation write-ups whose evidence shaped the toolkit — kept as history |
| [`examples/`](examples/) | starting points, e.g. the sandbox profile |

The pilot provider documents itself: [`pilot/thousandeyes/README.md`](../pilot/thousandeyes/README.md)
covers what it proves and how to run it, and its `docs/` directory is
tfplugindocs output for the Terraform registry.

## Decisions already taken

Recorded here because they shape everything else and are the questions most
likely to be asked again.

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
5. **Acceptance is confirmation, not discovery.** Everything an acceptance test
   would discover is discovered by the probe first — the rehearsal runs the same
   lifecycles with the same values before any provider code exists. A red
   acceptance run means the evidence is incomplete, and the fix starts with
   `probe`.
6. **Generation finishes with the tools that gate it.** `emit` runs the same
   compile, docs and formatting checks CI runs, at generation time. A gate that
   fires after the commit is just a slower postcheck.
7. **`deny` gates experiments, not sends.** A denied field is never probed, but a
   fixture value declared for it still goes into every body that needs it —
   otherwise a denied-but-required field would sink every create.

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
