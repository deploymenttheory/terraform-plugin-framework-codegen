# Fixtures and the rehearsal

How the toolkit decides what values an acceptance test uses, and how it proves —
before any provider code exists — that the lifecycle those values describe is one
the live API will actually accept.

## The problem this solves

The pipeline originally treated acceptance testing as a discovery mechanism: emit a
provider, run its tests against the live API, and fix whatever failed — a
server-populated field here, a refused zero value there, an update that silently
reset a sibling. Every fix was real, but each one was discovered *after*
generation, one failure at a time, against the most expensive oracle available.
That is playing whack-a-mole with a live tenant.

The correction is sequencing. Everything an acceptance test would discover is now
discovered by the probe, **before emit**, and recorded as facts with evidence.
Acceptance's job is confirmation: a red acceptance run means the evidence is
incomplete or stale, and the fix is better probing — not a hand-edit to generated
code.

Three pieces make that hold together:

1. **One derivation** of fixture values, shared by the probe and the generator, so
   what was rehearsed is what gets rendered.
2. **The rehearsal probe**, which walks the exact lifecycles the generated tests
   will run.
3. **A fixpoint** between the two: derive → rehearse → merge what was learned →
   re-derive, until the bodies stop changing.

## One derivation: `internal/fixturespec`

`internal/fixturespec` answers, for every attribute of a resource, the question
"what value does the acceptance fixture use?" — exactly once. The probe renders
its answer as wire JSON to send; the generator renders the *same* answer as HCL in
`testdata/minimal.tf` and `testdata/maximal.tf`. Two renderings, one derivation.
Before this package existed those were two independent guesses, and every
disagreement between them was invisible until a live test failed.

The preference ladder, most trusted first:

| Source | Where it comes from |
|---|---|
| forced value | `behaviour.forcedValue` — the server demonstrably imposes this value, so sending anything else is rehearsing a lie |
| curated hint | `accFixture` in the blueprint — a human wrote it down, usually because the API's constraint is undiscoverable |
| documented enum | the first enum value, from the schema |
| server default | `behaviour.serverDefault`, when the field must be sent anyway |
| format synthesis | the OpenAPI `format` — see below |
| plain synthesis | a type-appropriate sentinel carrying the run's name stamp |

Format-aware synthesis exists because a bare sentinel string is refused by any API
that validates the field, and one refused field loses the observation for the whole
body:

| `format` | Synthesised value |
|---|---|
| `date-time` | `2027-06-01T00:00:00Z` |
| `date` | `2027-06-01` |
| `uuid` | a fixed, obviously-synthetic UUID |
| `email` | a `tfacc`-tagged address |
| `uri` / `hostname` | RFC 2606 reserved names |
| `ipv4` | `192.0.2.1` (TEST-NET-1) |
| `ipv6` | `2001:db8::1` (documentation prefix) |

And refusal is an answer too. The derivation declines to invent values for
credential-shaped fields (a generated fixture must never contain something that
looks like a secret), for `_id`-suffixed references (an invented identifier points
at nothing), and for pattern-constrained strings it cannot satisfy. A refused
attribute is carried as an explicit `Skipped` entry with the reason, in both
renderings — the generated `maximal.tf` says *why* an attribute is absent, and the
probe knows not to send it.

## Curated hints: `accFixture`

When the derivation cannot know the right value, a human writes it into the
blueprint (see [blueprint.md](blueprint.md)):

- `hcl` — the value as the fixture should render it;
- `wire` — the value as the probe should send it, when the two differ;
- `omit` — the *curated omission*: this attribute must not appear in any fixture,
  recorded as a decision rather than left as an accident (the live case: a pair of
  fields the API refuses jointly, where the fixture keeps one);
- `source` — provenance, so a promoted hint is distinguishable from a hand-written
  one.

Hints can also be **promoted from probe plans**: `merge -promote-plans DIR` copies
a plan's fixture values into wire hints, but only for attributes the derivation
refuses to derive itself, only from the plan's *first* fixture (later fixtures
exist to probe variants, and promoting one broke a real acceptance test), and never
over a hand-written hint. The plan stays the place where probe-only knowledge
lives; the blueprint accumulates only what generation actually needs.

## The rehearsal: `write.rehearsal`

The rehearsal is a mutating probe (see [probing.md](probing.md)) that runs the
derived fixtures through the exact shapes a generated acceptance test uses:

- **Direction A**: create minimal → update to maximal → downgrade to minimal →
  delete.
- **Direction B**: create maximal → downgrade to minimal → delete.

Both directions, because the failure modes differ: a field with a server default
misbehaves when it is *added* by an update; a field the server force-sets
misbehaves when a *create* tries to set it; a downgrade is where update-resets
show themselves.

At every hop the response is compared against what was sent, field by field. Three
refinements keep the comparison honest:

- **Contrast**: before concluding a field is ignored on update, the probe
  substitutes a *different* in-bounds value (numeric contrast respects the
  declared `minimum`/`maximum` — discovered live when contrasting an HTTP version
  of 2 up to an out-of-range 3) and retries an update refused with the contrast
  uncontrasted, so a rejected substitute does not masquerade as suppression.
- **Bisection**: a body refused as a whole is re-tried dropping one sibling at a
  time (budgeted), so the *culprit* is named instead of the whole body being
  written off. Single-culprit by design; two interacting culprits exceed the
  budget and are recorded as a refusal note.
- **Read-back with expansions**: every read uses the plan's declared query
  expansions, because some APIs omit nested collections from a bare item read —
  concluding "not returned" from an unexpanded read was a real false fact.

## What the rehearsal learns

Its facts are precisely the ones acceptance tests used to discover the hard way:

| Fact | Meaning | Merge writes |
|---|---|---|
| `returnedOnCreate` / `returnedOnUpdate` (false) | sent, accepted, absent from that hop's response | description note; state-handling guidance |
| `serverForced` | the server replaces the sent value with its own, consistently | `behaviour.forcedValue` when corroborated — which then feeds the next derivation |
| `updateDefault` / `updateResets` | an update omitting the field resets it to a default | `behaviour.updateDefault`; description note |
| `interactionSuppressed` | returned normally except in the presence of a named sibling | description note naming the sibling |
| `zeroValueUnsendable` | the SDK cannot send the zero value at all | presence recommendation (static; see [probing.md](probing.md#static-facts)) |

As with every probe, a fact carries its evidence (request indices into the
cassette) and a confidence level, and `merge` only *acts* on corroborated facts —
a single observation annotates, it does not rewrite presence.

## The fixpoint

Facts change fixtures: learn that the server forces a value and the right fixture
value *is* that value; learn a zero value is unsendable and the fixture must omit
it. But the rehearsal that produced those facts ran with the old fixtures — so
`probe -mode record` re-derives the bodies from the merged evidence and reruns the
rehearsal until **derivation converges** (bounded rounds; convergence is the
normal case after one).

The loop lives in the command layer (`cmd/tfpluginframeworkgen/rehearse.go`), not
in the probe, because it needs `merge` — and the probe package must never depend
on the package that interprets its output. The converged bodies are frozen as
`rehearsal.json` in the evidence snapshot, so replay replays the fixpoint's
*outcome* rather than re-deriving from a blueprint that has since moved.

## `maximal.tf` is generated and policed

The last piece is ownership. Acceptance fixtures (`testdata/minimal.tf`,
`testdata/maximal.tf`) are **generated** from the same derivation, headered as
generated, and drift-gated like every other emitted file. A hand-maintained
maximal fixture was the original design, and it desynchronised from the rehearsed
bodies within one wave — the whole point of a shared derivation is defeated the
moment one of its renderings is edited by hand. What used to be hand-tuning a
fixture is now a curated `accFixture` hint, which both renderings honour. See
[generated-boundary.md](generated-boundary.md).
