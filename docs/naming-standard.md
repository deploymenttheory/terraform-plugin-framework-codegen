# Naming standard

Every domain term in this toolkit is owner-approved and recorded in
`docs/glossary.md`. This document is the rule set that decides what a name
may be before it gets there. `CLAUDE.md` states the naming rule in short
form; this is the long form, and the two do not disagree.

The audit that produced it, and the ruling on every name it found, are in
`naming_conventions.md` at the repository root.

## The test a name has to pass

> **A reader who has never seen this code must be able to tell, from the
> identifier alone, what the thing holds or what it does.**

Everything below is that test made specific. Where two rules pull apart,
the one with the lower number wins.

---

## R1 — A name states what the thing is

Not what it resembles, not what it was for, not how it was arrived at. If the
name needs the doc comment to be understood, the name is wrong and the doc
comment is carrying it.

The check: read the identifier out of context. If the honest answer is "I would
have to go and look", rename it.

```go
// Fails R1 — you have to go and look.
func (r *runner) maximalCulprit(...)      // ?
type entityRecipe struct{ … }             // ?
const refineReserve = 5                   // ?

// Passes R1.
func (r *runner) refusedOptionalField(...)
type entityLifecycle struct{ … }
const createStepRequestBudget = 5
```

Longer and duller beats shorter and cleverer. A name is read far more often
than it is typed.

## R2 — Compose approved words before coining new ones

Where a term in `docs/glossary.md` fits, use it. Where several fit, compose
them. Coin a new **root** word only when no approved word means the thing, and
then it goes to the repository owner and lands in the glossary in the same pull
request that introduces it.

Composition is not the same as coining: `acceptedRequestBody` composes two
approved terms and needs no approval. `Skeleton` is a new root word and does.

**R1 outranks R2.** Where the approved word is vaguer than a plain new one,
legibility wins — and the plain new word goes to the owner rather than being
used quietly.

## R3 — No metaphor

A name is not allowed to be a figure of speech. This is the rule that catches
most of what is wrong with the tree today, and it is deliberately absolute
rather than a matter of taste — "apt metaphor" is exactly the judgment call
that produced the current vocabulary.

Currently in the tree and failing R3:

> `heal` · `unhealable` · `debris` · `culprit` · `suspect` · `recipe` ·
> `oracle` · `seed` / `seeded` · `veto` / `vetoed` · `skeleton` · `scrub` ·
> `survey` · `claimant` · `coordinates` · `harvest`-shaped verbs ·
> `gave up` · `orphan` · `parked` · `sweep` · `verdict` · `silence` ·
> `said` · `debounce` · `narrative` · `witness`-shaped nouns

Ordinary English that happens to have a metaphorical origin is fine — a
*binder* binds, a *loader* loads, a *gate* refuses passage. R3 bans names whose
meaning **only** arrives by analogy: an error body is not silent, a field is
not a culprit, a request body is not a skeleton.

**`shape` is banned as an identifier, allowed in prose.** A comment saying
"the shape the generated SDK already has" is ordinary English. A type,
function, variable or wire value named `shape` is not, because it was doing
four unrelated jobs and a reader had to guess which. One identifier exception
is on the record: `Strategy.AuditShape`, approved deliberately.

## R4 — No abbreviation, anywhere

Every type, field, function, parameter, local and template-consumed field in
`internal/**` and `cmd/**` is fully worded. Production code and test code
alike. The glossary already required this inside
`internal/intermediate_representation`; it now applies everywhere.

Three exceptions, and no others:

1. **Acronyms in the acronym table** (`internal/intermediate_representation/naming.go`)
   — `API`, `HTTP`, `SDK`, `ID`, `URL`, `JSON`, `YAML`, `RPS`, `SHA`. Cased
   Go-idiomatically: uppercase whole in Pascal (`APIKey`), lowered whole when
   leading in camel (`apiKey`, `id`). Additions to the table go to the owner.
2. **Names an external contract fixes** — kiota's `GetPasswordEscaped`, cobra's
   `Short`/`Long`/`Example`, `go/types`, httpmock's `Responder`,
   terraform-plugin-framework's method names. Use theirs exactly; do not
   expand them.
3. **Established Go idiom where expansion fights the language** — `ctx`, `err`
   and `ok`, and nothing else. `context context.Context` shadows the package
   in the body, `errors err` does the same, and the comma-ok idiom is a
   language construct rather than a name.

**The shadowing rule.** Everything else expands fully. Where the natural
expansion would shadow an imported package or a predeclared identifier, take
the next fullest word that does not:

| Short | Not | Because | Use |
|---|---|---|---|
| `cfg` | `config` | shadows `internal/config` in 22 files | `configuration` |
| `pkg` | `package` | reserved word | `goPackage` |
| `str` | `string` | predeclared | `text` |
| `len` | `len` | predeclared | `length` |

`req`, `resp`, `attr`, `op`, `doc`, `ent`, `sum`, `ptr`, `val`, `idx`, `src`,
`dst`, `ext`, `param`, `props`, `disc`, `desc`, `elem`, `buf`, `cur` and
`prev` are not idiom. They are shortenings, and they are what makes a tree
hard to read.

A single-letter loop index is an abbreviation. `for index := range` costs four
characters and one habit.

## R5 — One concept, one word. One word, one concept

Both directions bind.

- **One concept, one word.** The same fact is spelled the same way in every
  package, in Go, in JSON, on disk and in prose. No concept is declared twice
  (`Provenance` currently is; so are the wire sentinels).
- **One word, one concept.** No word means two things. Where a collision
  exists, *both* sides get examined — the one that is further from the
  glossary's sense gives way.

The tree currently has ten collisions; §4.5 lists them. `orphan` means three
different things, `envelope` four, `variant` four.

## R6 — The wire spelling is the Go spelling

A JSON tag, an `x-tfpfgen-*` key, a config key, a committed path segment and
its Go identifier are the same word in different casing conventions — and
nothing else.

| Surface | Convention |
|---|---|
| Go identifier | `PascalCase` exported, `camelCase` unexported |
| JSON field, observation kind, step kind, outcome | `camelCase` |
| `x-tfpfgen-*` key, config key, file name, directory | `kebab-case` for keys and directories, `snake_case` for config keys |
| Terraform attribute | `snake_case` |

`readAfterWrite` may therefore only write `x-tfpfgen-read-after-write`. It
currently writes `x-tfpfgen-read-after-write`, which R6 forbids: one fact
must not have two names because it crossed a serialisation boundary.

## R7 — What is emitted speaks the reader's language, not the toolkit's

`internal/templates/**` is read by provider maintainers who know
terraform-plugin-framework and have never seen this toolkit. So:

1. **Where HashiCorp already has a word, use theirs**, spelled their way
   (`DataSourceName`, not `DatasourceName`).
2. **Never re-use one of their words for something else.** This is what
   `MapRemoteStateToTerraform` breaks — Terraform's "remote state" is a
   different thing entirely.
3. **Then the toolkit renames its internal term to match.** One vocabulary,
   chosen for the downstream reader rather than for us. Where the toolkit and
   the emitted code name one concept, the emitted side picks the word.

R7 outranks R2: an approved glossary term gives way to a HashiCorp one in
emitted code, and then the glossary entry changes.

**The same rule governs anything else generation emits for a person to
read**, and prose has no compiler to keep it honest. A generated report is
opened by somebody who knows Terraform and their own API, so it says what
happened in those terms: what the provider was built from, which step set
something aside, what that cost them, and whether anything can be done. The
glossary governs the code, the JSON and the paths on disk; it does not
govern the page. A stage name, a cause code or an internal noun reaching a
reader is this rule broken, not a wording preference — and where the
machinery is genuinely wanted it goes behind a disclosure, under the prose
rather than instead of it.

The mapping from an internal term to the sentence a reader gets is itself
prose the owner approves, held to its vocabulary by a test the way
`internal/spec/revise` holds its correction narration.

## R8 — British English

The repository is predominantly British already, and the committed observation
kind `normalisation` fixes it on a contract surface. So: `-ise`, `-isation`,
`behaviour`, `colour`.

Two exceptions:

1. **External contracts keep their own spelling** — a HashiCorp symbol, an
   OpenAPI keyword, a Go standard-library name, a vendor's wire field.
2. **Nothing already correct gets churned.** R8 is a tie-breaker for new names
   and for names being changed anyway, not a licence for a spelling sweep.

Current outliers: `normaliz*` (100 uses) against `normalis*` (58), and
`materializ*` (61) — the latter is being removed under R5 regardless.

## R9 — Test and fixture names are literal

Test helpers, fixture entities and stub data are named for what they contain,
not given personality. A fictional entity is `widget` or `thing`, not `beacon`
or `gizmo`. A fixture set is named for the backend or shape it exercises, not
for a quality it has.

R9 exists because fixture vocabulary leaks: `orphan` became an entity key in
the curated fixture while already meaning two other things.

## R10 — How a name gets approved

Unchanged from `CLAUDE.md`, restated so it is in one place:

1. A change that needs a new **root** word stops and presents options to the
   repository owner, with what the thing is in one plain sentence.
2. The approved word lands in `docs/glossary.md` **in the same pull request**
   that first uses it.
3. Composition of approved words (R2) needs no approval and no glossary entry.
4. A rename of a contract surface (`x-tfpfgen-*`, committed JSON, config keys,
   `unsupported.json`, manifest origins, emitted provider symbols) is a semver
   event and carries a migration note in the pull request body.
5. Retired words are listed in the glossary and never return.

---

## Worked examples

How the rules resolved the loudest cases in the audit. Every row is a ruling,
not a proposal.

| Was | Failed | Now |
|---|---|---|
| `heal` / `healed` / `unhealable` | R3 | `correctBody` / `bodyCorrected` / `uncorrectableRefusal` |
| `cleanupDebris` | R3 | `cleanupLeftoverObjects` |
| `maximalCulprit` / `suspects` | R3, R1 | `refusedOptionalField` / `optionalFields` |
| `entityRecipe` | R3, R1 | `entityLifecycle` |
| `Skeleton` | R3 | `RequestFields` |
| `SynthHint` | R3, R4 | `SyntheticValueRules` |
| `Hypothesis` | R2 | `Claim` — the glossary already said "how far the audit got with one claim" |
| `Check` | R2 | `Probe` — the glossary already used the word; the comment calling it retired was wrong |
| `refineReserve` and the `*Reserve` family | retired stem, R3 | `createStepRequests` and the `*StepRequests` family |
| `cycleConditional` / "value-cycling" | R3 | `retryAcrossValues` |
| `strategize` | R1 | `applyStrategies` |
| `Materialize` / `materialise` | R5, R8 | `WriteRevision` |
| `Postcheck` | R1 | `VerifyGeneratedTree`; manifest origin `"toolchain"` |
| `envelope` (both senses) | R3 | `x-tfpfgen-list-wrapper` + `x-tfpfgen-list-pagination`; `ErrorBodyShape` |
| `orphan` (three senses) | R3, R5 | `UndeletedObjects`, `UnproducedFilesOf`, `widget` |
| `shape` as an identifier | R1, R5 | `handle*`, `has*Operations`, `listResponseFormat` split in two |
| `json:"element_kind"` | retired | `json:"element_type"` |
| `x-tfpfgen-read-after-write` | R6 | `x-tfpfgen-read-after-write` — the kind is `readAfterWrite` |
| `oag*` | R4 | `openAPIGenerator*` |
| `MapRemoteStateToTerraform` | asymmetric | `MapRemoteStateToResource`, pairing with `MapRemoteStateToDatasource` |
| `"strip-schema-defaults"` | R2 | deleted — `prenormalise` already did the same walk on every run |

Cases where a word was **kept** matter as much. Four apparent collisions turned
out to be one idea seen at several stages, and renaming them would have made
the tree harder to read, not easier: **variant** (a document branch, an audit
request body, a fixture's ownership picture), **preflight** (before a
credentialed run, before the first create, before a release), **grammar** (the
fake API emits it, the executor parses it) and **stub** (a fake generator, a
hand-written SDK, a misbehaving API).
