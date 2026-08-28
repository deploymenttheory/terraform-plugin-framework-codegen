# Naming standard, and the inventory it is being applied to

**Part I** is the standard: ten rules that decide what a name in this
repository may be. **Part II** is the inventory of every name that currently
breaks them, with the rule that decides each one.

The standard is a proposal until the repository owner approves it. Once
approved it belongs in `docs/naming-standard.md`, referenced from `CLAUDE.md`
and `docs/glossary.md`, and it governs every future change.

---
# Part 0 — What the toolkit does, and where the disputed names live

Written because the rest of this document was unreadable without it. Every
disputed name below is placed on one concrete example rather than defined in
terms of other disputed names.

## The pipeline, six steps

| Step | Verb | What actually happens |
|---|---|---|
| 1 | `spec import` | Download the vendor's OpenAPI document, record its SHA-256 so nobody can swap it silently. |
| 2 | *(classification)* | Read the document's URL paths and decide what each group of them is: something Terraform can create and destroy, something it can only read, something it can only invoke, or nothing at all. |
| 3 | `audit run` | **Send real requests to the real API** to find out what it actually does, as opposed to what the document claims. This is the only step that touches a network. |
| 4 | `spec revise` | Turn what step 3 learned into patches against the document, get a human to approve them, apply them. |
| 5 | `sdk generate` | Run kiota or openapi-generator over the patched document to get a Go SDK. |
| 6 | `provider generate` | Turn the patched document into Terraform provider code, checked against the SDK from step 5. |

**Almost every disputed name is in step 3.** That step is the reason the toolkit
exists — an OpenAPI document tells you what an API's fields are *called*, not
which ones you may actually send together — and it is the part with no
established vocabulary to borrow.

## Step 3 on one real example

The example is the one the fake API was built around: a **monitor**.

The document says a monitor has a field `kind`, which is one of `ping`, `web`
or `dns`. What the document does *not* say, and what only the live API knows:

```
kind=ping   needs target_host
kind=web    needs web
kind=dns    needs domain, and dnssec is only legal once domain is set
interval    the document marks it optional; the API rejects a create without it
```

Finding that out is the whole job. Here is how the code does it, with the name
each thing currently carries.

### 3a. Work out what to send — the `strategy` package

**Find the field whose value changes the rules.** Scan the create body for a
field that could plausibly select between different request bodies: an enum or
a boolean. Here it
is `kind`. Candidates are ranked — a required enum is the likeliest, then an
optional enum, then a boolean — and the top-ranked one is used.

> currently: **`Gate`**, `GateKind`, `primaryGate`
> *the field whose value decides which other fields are allowed*

**Work out one request body per value of that field.** Four in total: a
baseline that pins nothing, plus one each for `kind=ping`, `kind=web`,
`kind=dns`.

> currently: **`Variant`**
> *one version of the request body, for one value of the selector field*

**For each of those, list the fields to send.** Two lists: the smallest body that
should work (required fields only), and the widest body that should work
(everything plausibly allowed). Each list also carries, per field, what is
needed to invent a value for it — its type, format, pattern, enum members,
declared example.

> currently: **`Skeleton`** (the list) and **`SynthHint`** (the per-field material)
> *the field list for one request body, and how to make up each value*

**Write down the guesses.** From the document's structure and from prose in
field descriptions, note things worth testing: *"dnssec is probably only valid
when kind=dns"*, *"dnssec probably requires domain"*. These are guesses, not
findings — the live API decides.

> currently: **`Hypothesis`**, `HypothesisKind`
> *a guess about the API's rules, to be confirmed or refuted by a real request*

**Say which request would settle each guess.** For "dnssec requires domain":
send a create with `kind=dns` and `dnssec` set but `domain` absent, and see
whether it is refused.

> currently: **`Check`**
> *the one request that would prove or disprove one guess*

**Put the requests in order, and budget them.** The full ordered list for this
entity, plus a cap on how many requests it may spend.

> currently: **`Program`**, `Budget`, and the whole thing is a **`Strategy`**
> *the ordered list of requests to send for one entity, and its request cap*

### 3b. Send them — the `audit/run` package

**Send the first create.** The API answers:

```
400  field interval is required
```

The document said `interval` was optional. It is not.

**Read the refusal and fix the body.** The fake API and the executor share a
fixed sentence form so the field name can be pulled out mechanically:

```
field X is required              -> add X and retry
field X is not valid when G=V    -> remove X and retry
field X requires field Y         -> add Y and retry
field X must reference an ...    -> borrow a real id and retry
anything else                    -> give up on this request
```

> currently: the sentence form is the **grammar**; fixing the body is **`heal`**;
> the four fixes are the approved **requestAdjustment** actions
> *add / remove / requires / borrow*

**Record what was learned.** `interval` is required by the API though the
document says otherwise. That is a finding, with the request and response kept
as proof, and a verdict on how confident it is.

> **`observation`** and **`outcome`** — both already approved and settled

**Clean up.** Everything the run created gets deleted, because these are real
objects in somebody's real tenant. Anything that could not be deleted is
reported so a human can go and look.

> currently: **`cleanupDebris`**, **`Orphans`** — already ruled: → `cleanupLeftoverObjects`, `UndeletedObjects`

### 3c. What it produces

Four findings on this one entity:

```
interval  required by the API          (the document said optional)
dnssec    valid only when kind=dns     (the document said nothing)
dnssec    requires domain              (the document said nothing)
kind      selects three valid bodies   (the document said nothing)
```

Those become patches to the document in step 4, and then Terraform schema
rules — a required attribute, and validators that reject an invalid `terraform
plan` before it ever reaches the API.

## The vocabulary, on one page

Reading order is the order a thing appears in a run.

| Now called | In plain terms | Status |
|---|---|---|
| entity | one thing the provider will manage — a monitor, a tag | settled |
| **Gate** | the field whose value decides which other fields are allowed | ruled: kept |
| **Variant** | one version of the request body, for one value of that field | ruled: kept |
| **Skeleton** | the list of fields to send in one request body | ruled: → `RequestFields` |
| **SynthHint** | per field, what is needed to invent a value for it | ruled: → `SyntheticValueRules` |
| **Hypothesis** | a guess about the API's rules, to be tested live | ruled: → `Claim` |
| **Check** | the one request that would settle one guess | ruled: → `Probe` |
| **Program** | the ordered list of requests for one entity | ruled: kept |
| **Strategy** | all of the above, for one entity | ruled: kept |
| **Budget** | how many requests this entity may spend | ruled: kept |
| **grammar** | the fixed sentence form a refusal takes | ruled: kept |
| **heal** | reading a refusal and fixing the body to retry | ruled: → `correctBody` |
| requestAdjustment | the four fixes: add, remove, requires, borrow | settled |
| observation | one thing learned, with proof | settled |
| outcome | how confident: confirmed, inconclusive, blocked, exhausted | settled |
| correction | one patch to the document, with a justification | settled |

---

# Part I — The standard

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

## R4 — No abbreviation, anywhere

Every type, field, function, parameter, local and template-consumed field in
`internal/**` and `cmd/**` is fully worded. Production code and test code
alike. The glossary already required this inside
`internal/intermediate_representation`; it now applies everywhere.

Two exceptions, and no others:

1. **Acronyms in the acronym table** (`internal/intermediate_representation/naming.go`)
   — `API`, `HTTP`, `SDK`, `ID`, `URL`, `JSON`, `YAML`, `RPS`, `SHA`. Cased
   Go-idiomatically: uppercase whole in Pascal (`APIKey`), lowered whole when
   leading in camel (`apiKey`, `id`). Additions to the table go to the owner.
2. **Names an external contract fixes** — kiota's `GetPasswordEscaped`, cobra's
   `Short`/`Long`/`Example`, `go/types`, httpmock's `Responder`,
   terraform-plugin-framework's method names. Use theirs exactly; do not
   expand them.

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
currently writes `x-tfpfgen-eventual-consistency`, which R6 forbids: one fact
must not have two names because it crossed a serialisation boundary.

## R7 — Emitted provider code speaks HashiCorp, and the toolkit follows it

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

How the rules resolve the loudest cases. **These are proposals**; the owner's
ruling column in Part II is still the decision.

| Now | Fails | Reasoning | Proposed |
|---|---|---|---|
| `heal` / `healed` / `unhealable` | R3 | Metaphor. R2: **requestAdjustment** is the approved noun for the change made, and `adjust.go` already carries the verb. | `adjust` / `adjusted` / `unadjustable` |
| `cleanupDebris` | R3 | Metaphor. R2: **cleanup** is approved; the objects are what a previous run left live. | `cleanupLeftoverObjects` |
| `maximalCulprit` / `suspects` | R3, R1 | Metaphor, and neither says what it holds. It is the optional field a refusal was attributed to. | `refusedOptionalField` / `candidateFields` |
| `entityRecipe` | R3, R1 | Metaphor. It holds how an entity creates, addresses and deletes its objects. | `entityLifecycle` |
| `Skeleton` | R3, R2 | Metaphor. No approved word fits — **request bodies** names the *accepted* ones. New root word needed → owner. | `DraftRequestBody` (owner decision) |
| `Hypothesis` | R2, R1 | Self-declared provisional. The glossary already says "how far the audit got with one **claim**" and describes each kind as *claiming* something. But `run.claim` already means something adjacent (R5). | `Claim` + rename `run.claim` (owner decision) |
| `Check` | R2 | The glossary already uses **probe** for exactly this ("the made-up-field probe"), and a file is named for it. Its comment claiming *probe* is retired is simply wrong. | `Probe` |
| `refineReserve` and the `*Reserve` family | R3, retired stem | *refinement* is retired; "reserve" is a coined unit of account. It is a per-step request budget. | `createStepRequestBudget`, `maximalStepRequestBudget`, … |
| `MapRemoteStateToTerraform` | R7 | Collides with Terraform's own "remote state". R7.3: the emitted side picks, and the toolkit's approved `APIToFramework*` catalog already names this direction. | `APIToFrameworkModel` |
| mocks `Registry` / `Register` / `NewRegistry` | R5, R7 | Four approved emit words re-used for an unrelated system, in emitted code. | `MockResponderSet` / `AddResponders` (owner decision) |
| emitted `Operation` type | R5 | A second `Operation`. It names which lifecycle method an error interrupted. | `LifecycleMethod` |
| `orphan` ×3 | R5, R3 | Metaphor, and three meanings. | live object → `undeletedObject`; manifest → `unproducedFile`; fixture entity → renamed under R9 |
| `x-tfpfgen-eventual-consistency` | R6 | Its kind is `readAfterWrite`. | `x-tfpfgen-read-after-write` |
| `json:"element_kind"` | retired | `ElementKind` is retired; the field was renamed and the tag was not. | `json:"element_type"` |
| `"list-resource"` / `"list_resource"` / `"list-resources"` | R5, R6 | Four spellings of one approved term. R6 fixes casing per surface. | kind `list_resource`; slot `list_resources`; directory `list-resources`; one constant each, declared once |
| `oag*` | R4 | Abbreviation of the approved backend name. | `openAPIGenerator*` |
| `HasEC` / `ECDuration` | R4 | Abbreviation in a template contract. | `HasReadAfterWriteLag` / `ReadAfterWriteLag` |
| `Materialize` | R5, R8 | A second name for the approved verb **revise**, spelled two ways. | fold into `Revise` |
| `"strip-schema-defaults"` | R2, R6 | A fifth JSON-Patch operation, coined against an external contract. Needs an owner decision on whether the *concept* survives at all. | owner decision |

---

# Part II — The inventory

Every name in the tree that breaks Part I, audited at commit `7de76bb`.

> **The `Ruling: ______` blanks below are superseded.** Every one has been
> decided; the decisions are recorded in **Part V**, which is authoritative.
> Part II is kept as the evidence behind them.

## How to rule on it

Each row carries a **Ruling** column. Fill it with one of:

| Ruling | Meaning |
|---|---|
| `→ <name>` | Rename to this. |
| `keep` | The name passes; add it to `docs/glossary.md`. |
| `delete` | The concept should not exist under any name. |
| `defer` | Leave it; decide later. |

Most rows do not need an individual ruling. Once Part I is approved, R4
(abbreviations), R5 (collisions), R6 (wire spelling) and the retired-term list
decide their own outcomes mechanically — those rows are marked with the rule
that settles them. What is left for you is roughly forty terms where the rules
narrow the answer but do not pick it.

Sections are ordered by blast radius, not by volume. **Section 1** is a
published contract — visible in provider repos and operator output, and a
semver event to change. **Section 2** becomes the permanent vocabulary of every
generated provider. Sections 3 onward are internal and cheap.

Within each table, rows run most-invented first. `(weak)` marks a row where the
word is close to ordinary English; `keep` is a reasonable default for those.

# Section 1 — Contract surfaces

These are visible outside this repository: in `spec/revised.yaml`, in committed
audit and correction artifacts, in `tfpfgen.yaml`, in CLI output, on the wire,
and in CI configuration.

## 1.1 `x-tfpfgen-*` extension keys

The glossary's approved extension list is: `-update-style`,
`-list-response-shape`, `-identifier-property`, `-valid-configuration`,
`-valid-when`, `-depends-on`, `-mutually-exclusive`. Everything below is in use
and absent from it. All nine are written into `spec/revised.yaml` in provider
repos.

| Key | Uses | Declared at | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `x-tfpfgen-required-when` | 26 | `specmodel/extensions.go:28` | A property is required only when a sibling field holds a given value. Generates a `<prop>RequiredWhenValidator` into provider code (`emit/render_validators.go:68,182`). | Absent from the approved list, and it is the single most-used unapproved key. | |
| `x-tfpfgen-eventual-consistency` | 19 | `specmodel/extensions.go:30` | How long a read may lag a write for this entity. | Absent; **and its name does not match its own observation kind**, which is `readAfterWrite`. Two names for one fact. | |
| `x-tfpfgen-create-only` | 19 | `specmodel/extensions.go:25` | The API refuses this property on update. | Absent from the approved list. Its observation kind is `immutable` — again, two names. | |
| `x-tfpfgen-server-default` | 17 | `specmodel/extensions.go:49` | The value the server stores when the request omits the property. | Absent from the approved list. | |
| `x-tfpfgen-values-open` | 12 | `specmodel/extensions.go:37` | The API accepts values beyond the declared enum. | Absent; its observation kind is `values`, so the key and the kind disagree. | |
| `x-tfpfgen-volatile` | 10 | `specmodel/extensions.go:39` | The property differs between two identical reads. | Absent from the approved list. | |
| `x-tfpfgen-delete-not-found-ok` | 10 | `specmodel/extensions.go:35` | A 404 on delete means "already gone". | Absent from the approved list. | |
| `x-tfpfgen-server-forced` | 9 | `specmodel/extensions.go:42` | The server overwrites whatever was sent. | Absent from the approved list. | |
| `x-tfpfgen-silently-ignored-on-update` | 8 | `specmodel/extensions.go:52` | Updates accept the property and discard it. | Absent; its kind is `ignoredOnUpdate`, so the key is longer and editorialising ("silently") where the kind is not. | |

## 1.2 An invented fifth JSON-Patch operation

The glossary defines a **correction** as "RFC 6902 operations plus a required
justification". RFC 6902 has `add`, `remove`, `replace`, `move`, `copy`, `test`.

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `"strip-schema-defaults"` | `op` value written into committed correction files | `spec/correction/correction.go:248`, error text `:254`, impl `:372`; walk at `spec/yamlwalk/yamlwalk.go:38` | A whole-document operation removing every schema's `default` key. | A coined extension to an external contract, serialised to disk in provider repos. The code comment at `:249` admits "Not expressible in RFC 6902". | |
| `StripSchemaDefaults` | exported func | `spec/yamlwalk/yamlwalk.go:38` | Implements the above. | Same coinage, exported across packages. | |

## 1.3 Committed file and directory names

The glossary fixes `spec/revised.yaml`, `spec/corrections/`,
`spec/corrections/proposed/`, `spec/corrections/rejected/`,
`audit/observations/<entity>.observations.json`,
`audit/request_bodies/<entity>.request_bodies.json`, `audit/summary.json`,
`audit/inputs.json`, `audit/runs/<runid>.activity.jsonl`, `manifest.json` and
`unsupported.json`. These are not on that list.

| Path | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|
| `spec/upstream.yaml` (`DocumentName`) | `spec/store/store.go:33` | The pinned vendor document, stored byte-for-byte. | The glossary names the *act* (**import**, "pinned by hash") and the revised output, but never this file. **"upstream"** is used ~15 times as a domain noun. | |
| `spec/upstream.lock.json` (`LockName`) | `spec/store/store.go:35` | Where the document came from, its SHA-256, and when. | Unapproved path segment. **"lock"** as the noun for the pin record is also unapproved — the glossary says "pin", never "lock". | |
| `spec/corrections/proposed/proposals.json` (`ReportName`) | `spec/revise/report.go:31`, written `:407` | A JSON report of pending proposals, read by the pull-request job. | The glossary fixes the directory but names no file inside it beyond the correction files themselves. | |
| `spec/revised.prenormalized.yaml` | `sdkgen/generate.go` | The pre-normalized copy handed to the SDK generator. | Carries the unapproved stage name **prenormalize** into a path. | |
| `.tfpfgen-previous`, `.tfpfgen-sdk-staging-*`, `.tfpfgen-provider-staging-*`, `tfpfgen-sdk-verify-*`, `tfpfgen-sdkgen-*` | `sdkgen/generate.go:60,172`, `sdkgen/verify.go`, `providergen/providergen.go:95` | The parked old tree and the temporary directories each stage builds into. | **"staging"** and **"parked"** name a deliberate mechanism; neither is approved. | |
| `internal/services/list-resources` | emitted directory; `emit/services.go:196` (`kindListResources`) | Where a generated provider's list-resource service packages live. | A **fourth** spelling of one approved term — see §4.2. | |
| `tests/terraform/unit`, `tests/terraform/acceptance`, `tests/responses` | emitted directories; `emit/render_fixtures.go:150-158` | Where generated HCL and wire-JSON fixtures land in every provider repo. | Directory vocabulary in every provider repo; not on the approved list. (weak) | |

## 1.4 Committed JSON field names and their closed string sets

### `audit/summary.json`

| Field / value | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|
| `orphans` (`Orphans`), and the error text "see the orphan list" | `audit/run/cleanup.go:20,41,71`; `orphanLine` `:242` | Objects cleanup could not delete. | **"orphan"** is an invented domain noun on a committed artifact and in operator-facing error text. It is also used for a *different* thing in `manifest` (§3.12) and as a fixture entity key (§3.11). Three meanings, one word. | |
| `"audited"` (`StatusAudited`) | `audit/run/run.go:140` | Per-entity status meaning the entity finished. | A status vocabulary parallel to the approved closed **outcome** set (`confirmed`, `inconclusive`, `blocked`, `timeoutExhausted`), which contains no such value. | |
| `StatusBlocked`, `StatusTimeoutExhausted` | `audit/run/run.go:141,142` | The other two per-entity statuses. | Values match approved outcomes, but they form a second parallel status type rather than reusing `observe.Outcome`. (weak) | |
| `ledgerDeletes`, `prefixDeletes` | `audit/run/cleanup.go:18,19` | Counts of objects removed by id versus by name prefix. | **"the prefix pass"** (`cleanup.go:26,46,63,122,149`) is a named mechanism; neither the mechanism nor these counted categories are approved. | |
| `edgesConfirmed`, `edgesInconclusive` | `audit/run/run.go:210,211` | Counts of inferred conditional-edge observations. | **"edge"** appears in glossary prose ("the core conditional edge") but is never a listed term, and here it is a committed countable noun. | |
| `skippedEntities` (and `plan.Skipped`) | `audit/run/run.go:178`; `audit/plan/plan.go:171` | Entities the plan left out, and why. | "skipped entity" as a committed category. (weak) | |
| `byKind`, `byOutcome` | `audit/run/run.go:180,181` | Observation counts bucketed by kind and by outcome. | Built on approved nouns. (weak) | |
| `adjustments`, `rateLimited`, `slowdowns`, `rateLimitRps`, `rejectsUnknownFields` | `audit/run/run.go:218-220` | — | **Approved.** Listed to confirm they were checked. | — |

### `audit/observations/<entity>.observations.json`

| Field | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|
| `pathTemplate` | `audit/observe/redact.go:22` | The operation path with parameters left as `{…}` rather than a concrete URL. | **"path template"** is a term of art here — the deliberate non-replayability of an excerpt depends on it — and it is unapproved. | |
| `requestFragment`, `responseFragment` (+ `MaxFragmentBytes`) | `audit/observe/redact.go:26,27,14` | The truncation-checked body snippets on an excerpt. | **"fragment"** is a coined unit distinct from the approved **excerpt**. | |
| `"[redacted]"` | `audit/observe/redact.go:54` | The placeholder a removed value reads as. | On-disk string literal; spelling unapproved. (weak) | |

### The audit plan artifact

| Field | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|
| `bisectionAllowance` (`BisectionAllowance`) | `audit/plan/plan.go:132`; sized at `synth.go:76` | Extra `createMaximal` attempts allowed for narrowing which optional field a refusal is about. | **"bisection"** and **"allowance"** are both coined; this one is serialised. | |
| `role` = `"resource"` / `"lookup"` / `"datasource"` | `audit/plan/plan.go:154`; `strategy/strategy.go:258,302-336` | Which of three shapes an entity's audit takes. | **"role"** as a term of art, and `"lookup"` as a status label — the approved phrase is *lookup-by-key datasource*. Declared twice, in two packages. | |
| `declaredProperties` | `audit/plan/plan.go:164` | Union of property names the entity's schemas declare, used to spot undocumented fields. | Coined artifact field. | |
| `parentRefs` | `audit/plan/inputs.go:34` | Path-parameter values naming an existing parent object, inside the **authored** `audit/inputs.json`. | The *inputs* glossary entry describes the idea ("an existing parent object's id") but fixes no key spelling. This is an authored file an operator writes by hand. | |
| `values`, `skip` (`entityInputKeys`) | `audit/plan/inputs.go:29,37,41` | The other two keys an entity object in `audit/inputs.json` may carry. | Same — operator-facing schema of an authored file, unapproved. | |

### `spec/corrections/proposed/proposals.json`

The whole schema of this artifact is coined. It is read by the pull-request job
and its contents reach PR bodies humans review.

| Field | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|
| `findings` (`Finding`) | `spec/revise/report.go:75,80` | One observation's entry in the report. | **"finding"** used as a structural noun — a type and a JSON array. | |
| `groups` (`Group`, `GroupBranch`) | `spec/revise/report.go:41,48`; `buildGroup` `:155` | All proposals of one kind against one entity — one review decision. | The *branch scheme* `tfpfgen/correction-<entity>-<kind>` is approved; **"group"** as the noun for the unit of decision is not. | |
| `means` (`Means`) | `spec/revise/report.go:102`; `explain.go:38` | What the finding costs a Terraform practitioner. | Coined field name. | |
| `merging`, `closing` | `spec/revise/report.go:73,74`; `explain.go:43,45` | What accepting or refusing the correction does. | Coined fields that turn git verbs into domain concepts. | |
| `expected`, `observed` | `spec/revise/report.go:100,101` | What the document led the audit to expect, versus what the API did. | Coined artifact fields. | |
| `valueSpelling` | `spec/revise/report.go:88` | The finding's value rendered for a human. | **"spelling"** as a domain noun for a rendered value. | |
| `kindTitle`, `kindPlural` (and `Title`, `Plural`) | `spec/revise/report.go:55,56`; `explain.go:30,32` | Human-readable singular and plural names for an observation kind. | Introduce a parallel human vocabulary beside the approved kind names. | |
| `stale` | `spec/revise/report.go:91`; `propose.go:62,102` | The observation was taken against a superseded document pin. | **"stale"** as a committed category. | |

### `spec/upstream.lock.json`

| Field / type | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|
| `Lock` (type), `fetchedAt`, `documentVersion`, `format`, `source`, `sha256`, `openapi` | `spec/store/store.go:42-55` | The pin record's on-disk shape. | The type name **`Lock`** and the fields `fetchedAt` / `documentVersion` are coined; the glossary's verb is *import* and its noun is *pin*. | |
| `Outcome` with `Pinned` / `Unchanged` / `Repinned` | `spec/store/store.go:62,66,69,72` | What one `spec import` did. | **A second, unrelated `Outcome` type**, colliding with the approved observation `Outcome` whose four values are a closed set. **`Repinned`** is a coined verb form. | |

### `manifest.json`

| Value | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|
| `"origin": "postcheck"` (`OriginPostcheck`) | `manifest/manifest.go:51` | Marks manifest entries the Go toolchain finalised. | **"postcheck"** is an invented stage name in a committed closed value set. The approved stage vocabulary is `derivation \| binding \| emission` plus the named verbs. | |
| `"source": "go mod tidy"` | `providergen/providergen.go:196` | What produced `go.mod` / `go.sum`. | A command string where every other entry carries a template path or an entity key — an ad-hoc value in the `source` set. (weak) | |
| `format_version`, `unsupported`, `path`, `stage`, `reason`, `authored`, `derivation\|binding\|emission`, `entity "x"` | `emit/unsupported.go:37-63`; `manifest/manifest.go:71` | — | **Approved.** Confirmed clean. | — |

## 1.5 Sentinel values sent to live APIs

| Value | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|
| `"tfpfgen-undocumented"` | `audit/plan/steps.go:12`, **duplicated** `audit/run/strategize.go:36` | The made-up enum value the `undocumentedEnumValue` probe sends to a live API. | The *step kind* is approved; this wire spelling is not — and it is declared twice, so the two can drift. | |
| `"tfpfgen_unknown_field"` | `audit/plan/steps.go:16`, **duplicated** `audit/run/strategize.go:37` | The made-up field name the `undeclaredSpecField` probe sends. | Same: unapproved wire spelling, duplicated. Note the two sentinels disagree on hyphen versus underscore. | |
| `tfpfgen-test-` (`NamePrefix`) | `internal/fixtures` | — | **Approved.** | — |

## 1.6 `tfpfgen.yaml` config keys

The glossary fixes the file name and delegates the schema to `internal/config`,
and `docs/config.md` is generated from the structs — so these are self-documenting
and mild. Only `audit.auto_accept` and `audit.rate_limit_rps` are named in the
glossary. Listed for completeness; `keep` is a reasonable blanket ruling.

| Key | Location | What it is | Ruling |
|---|---|---|---|
| `audit.base_url_override` | `config/config.go:70` | Base URL the audit calls instead of the document's server. | |
| `audit.max_objects` | `config/config.go:72` | Ceiling on simultaneously live created objects. | |
| `audit.name_prefix` | `config/config.go:71` | The prefix every created object carries. | |
| `audit.enabled` | `config/config.go:69` | Whether the pipeline runs the audit at all. | |
| `services.exclude` (and the section name `services`) | `config/config.go:78-79` | Spec entities that become no provider code. **"services"** as the noun for entities-that-become-code is a different sense from the approved `internal/templates/services/`; the toolkit's own word is *entity*. | |
| `sdk.client_type_name`, `sdk.include_paths`, `sdk.exclude_paths`, `sdk.backend_version` | `config/config.go:52-56` | SDK generation knobs. `backend` itself is approved. | |
| `provider.name`, `provider.registry_namespace` | `config/config.go:36,37` | The provider's name and registry namespace. | |
| `generator.version`, `spec.document_url`, `version` | `config/config.go:42,47,24` | The pinned toolkit tag, the upstream document URL, the config schema version. | |
| `auth.method`, `auth.api_key_header`, `auth.token_url` | `config/config.go:62-64` | Auth selection and its two parameters. | |
| `auth.method` values `bearer_token`, `basic`, `oauth2_client_credentials`, `github_app` | `config/config.go:90-94` | The closed auth-method set. Mostly external HTTP vocabulary. | |

## 1.7 CLI verbs, flags and operator-facing output

The approved verb set is `audit run`, `audit cleanup`, `spec import`,
`spec revise`, `spec verify`, `sdk generate`, `sdk verify`,
`provider generate`, `provider verify`, `config validate`. The only approved
flag is `--force-api-audit`.

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `materialize` / `materialise` | domain verb, in cobra `Short`, flag help, `docs/contract.md` and an exported func | `cli/spec_revise.go:29,74`; `spec/revise/revise.go:58` (`Materialize`), package doc `:1,5,9,41`; `docs/contract.md:29`; `cli/audit.go:268` | Writing `spec/revised.yaml` from the pin plus accepted corrections. | **A second name for the approved verb `revise`**, on the exported entry point and in operator-facing help text — and spelled both ways: `materialize` in `spec_revise.go`, `materialised` in `audit.go:268`. | |
| `"vetoed %s: %s"` (`Vetoed`, `vetoSet`, `catVetoed`) | printed output + type + const | `cli/spec_revise.go:114`; `spec/revise/propose.go:95,370`; `compile.go:32` | A `derivedDefault` observation blocking a `serverDefault` correction on the same attribute. | **"veto"** is an invented mechanism name that an operator reads. | |
| `"unplaceable %s: %s"` (`Unplaceable`, `catUnplaceable`) | printed output + const | `cli/spec_revise.go:117`; `spec/revise/compile.go:29,48` | A proposal whose target could not be located in the document. | Invented adjective naming a refusal category, printed to operators. | |
| `"no correction form exists yet"` (`NoForm`, `catNoForm`) | printed output + const | `cli/spec_revise.go:110`; `spec/revise/compile.go:25,29,115` | A finding for which no correction shape has been written. | **"form"** as a domain noun; the glossary has no such term. | |
| `"suppressed %s"`, `"stale %s"`, `"already stated"`, `"not confirmed"`, `"auto-accepted"` | printed output + the `Proposals` bucket fields | `cli/spec_revise.go:99,108,161,173`; `spec/revise/propose.go:80-102` | The closed set of reasons an observation did not become a correction. | A closed set of coined reason categories an operator reads. None are in the glossary. | |
| `--postcheck` and `"postcheck passed:"` / `"postcheck skipped:"` | flag + printed labels | `cli/provider.go:125,116,118`; also a job step name in `.github/workflows/10-generate.yml:718` | Running `go mod tidy`, `go build` and `go vet` in the generated tree. | A named pipeline stage with its own flag, two output labels, a Go type, a file and a manifest origin. | |
| `orphan` / `"  orphan: %s\n"` | printed output | `cli/audit.go:188-189,383-384` | A live object cleanup could not delete. | See §1.4. | |
| `--print-ir` (`printIR`) | flag | `cli/provider.go:124,66,82` | Prints the derived intermediate representation as JSON and generates nothing. | Unapproved flag, and it abbreviates the approved *intermediate representation* to `ir`. | |
| `__serve-quirkserver` | hidden CLI subcommand | `cli/serve.go:25,28`; registered `cli/cli.go:88` | Runs the quirk server as a real HTTP process for rehearsals. | A verb outside the approved set. Hidden, but present in the command tree. | |
| `--secrets` | flag | `cli/config.go:33,51` | Also require the auth method's secrets in the environment. | Unapproved flag naming a domain concept (the *secret roles*). | |
| `TFPFGEN_LOG_LEVEL` | environment variable | `cli/audit.go:292,295` | Sets the audit run's zerolog level. | The only approved `TFPFGEN_*` spellings are the seven `TFPFGEN_AUTH_*` secret roles. This is a new variable in that namespace. | |
| `--base-url`, `--prefix`, `--out`, `--dir`, `--config`, `--file`, `--sdk`, `--propose-only`, `--addr`, `--spec` | flags | `cli/audit.go:126-129,172-175`; `sdk.go:60-62`; `provider.go:34-37`; `spec.go:57,79`; `spec_revise.go:72-74`; `serve.go:33-34` | Path, source and behaviour switches. | Not in the approved flag list. Most are generic English; `--propose-only`, `--prefix` and `--spec` carry domain words. (weak) | |
| `"the plan skipped N entities"` / `printSkipped` | printed output | `cli/audit.go:302,308,328` | Groups skipped entities by reason. | "skipped" as an outcome category is not in the approved closed **outcome** set. (weak) | |

## 1.8 CI: workflow, job and secret names

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `TFPFGEN_APP_ID`, `TFPFGEN_APP_PRIVATE_KEY` | CI secret names | `.github/workflows/10-generate.yml:43,46,321,331-332`; `20-corrections.yml:31,34` | The **pipeline's own** GitHub App, deliberately distinct from the audited API's App credentials. The workflow comment at `10-generate.yml:37` says so explicitly. | The distinction is intentional and correct, but the spelling is not approved and sits one word away from `TFPFGEN_AUTH_APP_ID` / `TFPFGEN_AUTH_APP_PRIVATE_KEY` in the same namespace — exactly the confusion the `TFPFGEN_AUTH_*` convention exists to prevent. | |
| `withdraw-corrections` / "withdraws" / "withdrawn" | job name + step text, ~18 uses | `10-generate.yml:593,574,583,588,616-645`; `20-corrections.yml:60,66,232` | Closing the correction pull requests a run no longer proposes. | **"withdraw"** is a coined lifecycle transition. `docs/contract.md:95` even states "A proposal can be withdrawn, which is not a rejection" — a whole state the glossary does not record beside proposed / accepted / rejected. | |
| `continue-pipeline` | job name | `20-corrections.yml:228` | Resumes generation once nothing is left to decide. | Coined stage name. | |
| `.github/actions/stage`, with input `verb` | composite action directory + input | `.github/actions/stage/action.yml:1,11`; used `10-generate.yml:202` and throughout | One pipeline stage: restore artifacts, run the verb, upload the result. | **"stage"** as a named pipeline unit and **"verb"** as an input name, promoted to a directory name. | |
| `preflight` | job name | `60-release.yml:3,39,70` | The release gate checking `go mod tidy` and `go build`. | The glossary reserves **validate** / "the offline preflight" for `config validate`. This is a second, unapproved sense of the word — and a third exists in `audit/run` (§3.2). | |
| "debounce" | mechanism named in comments | `10-generate.yml:391`; `20-corrections.yml:227` | What stops repeated continuation runs. | Coined mechanism name in the pipeline's vocabulary. | |
| "narrative" | noun in comments and PR body text | `10-generate.yml:426,539` | The proposal report a correction PR body carries. | Coined noun for a generated artefact that reaches PR bodies operators read. | |
| `record` | job name | `20-corrections.yml:52,96` | Writes the rejection marker for a closed correction PR. | Coined job name. (weak) | |
| `major-tag` | job name | `release.yml:54` | Moves the major tag to this release. | Coined job name. (weak) | |
| `ci.yml`, `release.yml` | workflow file names | `.github/workflows/` | This repo's own CI and release workflows. | The glossary fixes the six shared workflow names `10-`…`60-`; these two unnumbered files sit outside that scheme. (weak) | |
| `validate-report`, `spec-imported`, `spec-revised`, `sdk-tree`, `provider-tree`, `corrections-proposed`, `observations` | inter-job artifact names | `10-generate.yml:98,137,667,690,719,288,206` | The named artifacts passed between jobs. | Mostly derived from approved vocabulary. (weak) | |
| `AUDIT`, `REUSE_RUN_ID`, `HAS_APP`, `HAS_TOKEN`, `PROPOSED`, `GENERATE_WORKFLOW`, `ALL_SECRETS`, `TEST_TIMEOUT`, `TFPLUGINDOCS_VERSION`, `OPENAPI_URL` | CI env vars | `10-generate.yml:52,76,168-169,245,264-266,353`; `40-acceptance.yml:52,86`; `50-docs.yml:49,119` | Step-scoped plumbing values. | Unrecorded CI names, mostly generic. (weak) | |

## 1.9 Scripts

The approved script list contains only `scripts/repo_hygiene_gate.sh`.

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `scripts/coverage_gate.sh`, and its `coverage_gate:` output prefix | script name + printed prefix | `scripts/coverage_gate.sh`, `:12,30,37` | Fails the build when total or per-package coverage falls below a floor. | A second gate script, not on the approved list, though `CLAUDE.md` describes the rule it enforces. | |
| `TOTAL_MIN`, `PKG_MIN` | env vars | `coverage_gate.sh:6,7,26,32,38,39` | The total and per-package coverage floors. | Outside any approved namespace; `PKG` is also an abbreviation. | |
| "pilot leakage" | rule name in comment + output | `repo_hygiene_gate.sh:5,41` | Pilot vendor names appearing in non-test source. | The script is approved; this rule's *name* is a coinage. | |
| "the 800-line ceiling", "the N% floor", "the N% gate" | printed threshold names | `repo_hygiene_gate.sh:23,41,51`; `coverage_gate.sh:27,38` | The file-size and coverage rules. | **"ceiling"** and **"floor"** as threshold nouns. (weak) | |

---

# Section 2 — Emitted provider vocabulary

These identifiers are written by `internal/templates/**` into **every generated
provider repo**. They become the permanent vocabulary of downstream code that
nobody can hand-edit. Highest blast radius after Section 1.

## 2.1 The provider-core layer

| Term | Kind | Template location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `MapRemoteStateToTerraform` | generated exported func | `services/resource/state.go.tmpl:7,13`; called `crud.go.tmpl:49,103,168` | Copies the SDK's read model onto the framework model struct. | **"remote state" collides head-on with Terraform's own unrelated "remote state"** — the worst collision in the emitted surface. The approved name for this direction is the `APIToFramework*` catalog. | |
| `MapRemoteStateToDatasource` | generated exported func | `services/datasource/state.go.tmpl:8,10` | The same, for a datasource. | Same collision. | |
| `Registry`, `NewRegistry`, `GlobalRegistry`, `Register`, `MockRegistrar` | generated exported type, funcs, var, interface | `provider-core/internal/mocks/mocks.go.tmpl:70,72,81,86,91,144` | The process-wide table of per-entity mock responder registrars. | **Deliberate overload of the approved emit `Registry` / `Register` / `Registrations` vocabulary for a completely unrelated system** — and this copy is the one emitted into every provider. Two concepts, four shared words. `Registrar` is itself invented. | |
| `Operation`, with `OperationCreate/Read/Update/Delete/Invoke` | generated exported named string type + closed constant set | `provider-core/internal/services/common/errors/errors.go.tmpl:27,30-36` | Which lifecycle verb an error interrupted, for the 404 policy and the message. | **A second `Operation` type.** `Operation` is approved *intermediate-representation* vocabulary meaning an API call; this is a different thing with its own value set, emitted into every provider. | |
| `CoManagementNote` | generated exported func | `provider-core/internal/services/common/schema/description.go.tmpl:12`; consumed at `emit/render_resource.go:269`, `render_datasource.go:168,309`, `render_listresource.go:88`, `render_action.go:81` | Builds the sentence a schema description carries when several generated entities write to one API collection. | **"co-management" / "co-managed entity"** names a whole derivation concept and appears nowhere in the glossary. | |
| `StateContainer`, `CreateResponseContainer`, `UpdateResponseContainer` | generated exported interface + types | `provider-core/internal/services/common/crud/read_with_retry.go.tmpl:18,24,35` | The get/set pair over a write response's state that the read-after-write loop rewrites. | **"state container"** names a concept in the generated crud layer; unapproved. | |
| `ConsistencyPredicate` / `consistencyPredicate` | generated exported field + per-resource func | `read_with_retry.go.tmpl:56`; `services/resource/crud.go.tmpl:16` | A callback that keeps a successful read retrying until the state looks settled. | **"consistency predicate"** names a concept; unapproved. | |
| `eventualConsistency` | generated const | `services/resource/crud.go.tmpl:11` | How long a read may lag a write for this entity. | A named derivation output with its own const, template flag, IR field and extension key. See also `HasEC`/`ECDuration` in §3.9. | |
| `ReadWithRetry`, `DeleteWithRetry` (+ their `*Options` types, and the file names `read_with_retry.go` / `delete_with_retry.go`) | generated exported funcs, types, file names | `read_with_retry.go.tmpl:46,68`; `delete_with_retry.go.tmpl:14,29` | The two retry loops. | `readWithRetry` is an approved **audit step kind**, not an approved generated symbol — the same word now names two things in two systems. `DeleteWithRetry` has no counterpart at all; the approved step kind is `deleteWithConfirmation`. | |
| `Info` | generated exported type | `errors.go.tmpl:39` | The backend-neutral `{Status, Message}` description of one API error. | A bare, unapproved noun naming the central concept of the emitted errors package. | |
| `IsFatalRead`, `IsRetryableDelete` | generated exported funcs | `errors.go.tmpl:136,149` | The HTTP-status policies the retry loops consult. | **"fatal read"** and **"retryable delete"** are coined category names for status sets. | |
| `kiotaSilence` | generated const | `errors/extract_kiota.go.tmpl:26` | The fixed string kiota's `ApiError.Error()` returns when nothing set a message. | **"silence"** as a named category of API error is a coined metaphor. | |
| `kiotaSaid` | generated func | `extract_kiota.go.tmpl:72` | Reads whatever prose the API's error body carried. | **"said"** as a domain verb for extracted error prose. | |
| `kiotaDetailed`, `kiotaTitled`, `kiotaMessaged`, `kiotaErrored`, `kiotaUndeclared` | generated interface types | `extract_kiota.go.tmpl:32-36` | One-method interfaces asserting which getter a kiota error type carries. | An invented adjective family naming error-body shapes. | |
| `undeclaredText`, `listedText`, `appendText` | generated funcs | `extract_kiota.go.tmpl:100,119,135` | Mine a message out of kiota's `AdditionalData` bag. | **"undeclared text"** / **"listed text"** are coined category names. (weak) | |
| `wire` | generated package-level var | `extract_kiota.go.tmpl:44`; `extract_openapigenerator.go.tmpl:52` | The single compiled-in error extractor instance. | **Overloads the approved "wire name" / "wire JSON" vocabulary** with an unrelated meaning inside generated code. | |
| `extractor` / `extract` | generated interface + method | `errors.go.tmpl:49` | Pulls `Info` out of the backend SDK's own error type. | Unapproved name for the backend seam. (weak) | |
| `openAPIError`, `statusError`, `openAPIGeneratorExtractor`, `FromResponse` | generated types + exported func | `extract_openapigenerator.go.tmpl:12,19,40,48` | The openapi-generator error seam. | Mostly mundane. (weak) | |
| `Describe`, `StatusFromDiagnostics`, `permissionDetail` | generated exported funcs | `errors.go.tmpl:55,166,205` | Render any error as `Info`; recover a status by regex from a diagnostics message; render the "you may lack these scopes" sentence. | (weak) | |
| `HandleCreateError`, `HandleReadError`, `HandleUpdateError`, `HandleDeleteError`, `HandleDatasourceReadError`, `HandleActionError` | generated exported funcs | `errors.go.tmpl:67-126` | Per-verb diagnostic emitters. | A closed named set in the emitted vocabulary. (weak) | |
| `HandleTimeout`, `ResourceTimeouts`, `DatasourceTimeouts` | generated exported funcs | `crud/timeout.go.tmpl:26`; `schema/timeouts.go.tmpl:19,30` | The bounded context every retrying helper needs; the shared timeouts attribute. | (weak) | |
| `headerTransport`, `orEnv`, `secondsOrEnv` | generated types/funcs | `client/client.go.tmpl:82`; `provider/provider.go.tmpl:269,283` | The RoundTripper stamping user agent and credential; config-then-environment resolution. | (weak) | |

## 2.2 Generated provider block attributes

The glossary's approved provider-block attribute list is closed: `endpoint`,
`api_token`, `username`, `password`, `client_id`, `client_secret`, `token_url`,
`request_timeout`, and the `client_options` block.

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `app_id`, `app_private_key` | generated HCL attributes (schema keys + `tfsdk` tags) | `provider/provider.go.tmpl:109,113`; `model.go.tmpl:38,40`; `client.go.tmpl:63-64` | The GitHub App credential fields of the generated provider block. | **Not on the approved attribute list.** These are practitioner-facing HCL, so the spelling is a published contract. | |
| `AppID`, `AppPrivateKey` (+ `AuthGitHubApp` at `emit/provider_core.go:91`) | generated exported struct fields | `client.go.tmpl:63-64`; `model.go.tmpl:38,40` | The Go spellings of the above. | Same. | |

## 2.3 Per-entity service files

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `constructResource`, `constructUpdate`, `constructInvocation`, the generated file `construct.go`, and `ConstructBody` / `ConstructImports` / `ConstructReturnType` / `WriteConstructor` | generated funcs, generated file name, template-consumed fields | `services/resource/construct.go.tmpl:10,21`; `services/action/invoke.go.tmpl:9` | Build the SDK request body from the plan. | **"construct"** is used as a domain noun here (a construct body, a construct file); absent from the approved list. | |
| `conditional_validators.go` | generated file name + template name | `services/resource/conditional_validators.go.tmpl` | Carries `ConfigValidators` and the generated custom validator types. | **"conditional validators"** names the whole value-conditional-edge output; the approved terms are the observation kinds (`validWhen`, `validConfiguration`, …). | |
| `<prop>RequiredWhenValidator`, `emitRequiredWhen` | generated type name suffix + emitter | `emit/render_validators.go:68,182,184` | The validator generated from `x-tfpfgen-required-when`. | Carries the unapproved extension key of §1.1 into every provider's source. | |
| `seeded` | generated func | `services/resource/responders.go.tmpl:82` | Parses the committed minimal response fixture the stateful mock store starts from. | **"seed"** is a metaphor coinage; not in the approved fixtures vocabulary. | |
| `mockState` | generated package var | `services/resource/responders.go.tmpl:13` | The in-memory object store the fake API keeps. | Names a concept ("the mock store"). (weak) | |
| `ActivateMocks`, `ActivateErrorMocks`, `DeactivateAndReset` | generated exported methods | `mocks/mocks.go.tmpl:97,109,121` | Turn the httpmock transport on with happy-path or refusing responders, and off again. | A coined verb pair naming a test-harness stage. | |
| `UnitEndpoint` / `unitEndpoint` | generated exported const, **duplicated in Go** | `mocks/mocks.go.tmpl:22`; mirrored `emit/render_resource.go:572` | The unreachable base URL every unit-test responder registers under. | **"unit endpoint"** names a fixed toolkit concept, and the spelling is load-bearing in two places that can drift. | |
| `TestResource`, `CheckExists`, `CheckDestroyed` | generated exported interface + funcs | `acceptance/acceptance.go.tmpl:72,80,99` | The per-resource "does this object exist in the live API" check. | **`TestResource` reads as "a resource under test", which it is not** — it is the existence checker. Coined and misleading. | |
| `SetupUnitTestEnvironment`, `registerAuthMocks`, `requestID`, `object()` | generated funcs | `mocks.go.tmpl:35,63`; `responders.go.tmpl:72`; `datasource/responders.go.tmpl:33` | Env-var fakes; the OAuth2 token responder; the id segment of an item URL; the decoded maximal response fixture. | (weak) | |
| `createResponder`, `readResponder`, `updateResponder`, `deleteResponder`, `listResponder`, and the generated file `mocks/responders.go` | generated funcs + file name | `responders.go.tmpl:90,109,122,146,162` | The per-verb httpmock handlers. | `Responder` is httpmock's own type (external), but the generated **file name** is a toolkit choice not on the approved list. (weak) | |
| `identityModel`, `identityModelForTest`, `listConfigModel` | generated types | `list-resource/model.go.tmpl:12`; `list_resource_test.go.tmpl:122`; from `emit/render_listresource.go:60` | The structs a list result's identity and the list block's own config decode through. | The approved concept is **resource identity schema**; these are unapproved generated symbols for it. (weak) | |
| `listedresource` | generated import alias, written into every `list_resource_test.go` | `emit/render_listresource.go:65` | The alias the list-resource test imports the managed resource package under. | A coined single-word noun written into generated source. | |
| `<Pascal>Mock`, `<Pascal>DatasourceMock`, `RegistryName` | generated types + template field | `resource/responders.go.tmpl:31`; `datasource/responders.go.tmpl:24`; `emit/render_datasource.go:65` | The per-entity registrar struct and the key it registers under. | Part of the mock-registrar coinage above. (weak) | |
| `tfpfgen_run` / `random_string.tfpfgen_run` | generated HCL resource name in every acceptance fixture | `internal/fixtures/replay.go:26-27` (`RunSuffixExpr`, `RunSuffixBlock`) | The `random_string` resource whose value suffixes synthesised names so two runs do not collide. | **"run suffix"** is a coined concept, and this label lands in committed generated `.tf` files. | |
| `deprecationMessage` | const, emitted verbatim into every provider | `emit/render_schema.go:61` | The fixed sentence a deprecated attribute's schema carries. | A toolkit-owned fixed wording in every provider; unapproved as a wording. (weak) | |
| `modify_plan.go`, `PreCheck`, `NewRequestAdapter` | generated file name / funcs | `resource/modify_plan.go.tmpl`; `acceptance.go.tmpl:60`; `client_kiota.go.tmpl:16` | Derived from framework, terraform-plugin-testing and kiota names. | **Not violations** — external contract. Listed to confirm they were checked. | — |

---

# Section 3 — Go identifiers, by package

Internal to this repository. Cheaper to change than Sections 1 and 2, but this
is where most of the volume is.

## 3.1 `internal/audit/strategy` — a package that declares its own vocabulary provisional

**Read this one first.** The package comment at `strategy.go:23-27` says:

> *Deferred naming: the hypothesis-kind values (variant, requiredWhen,
> requiresField, mutuallyExclusive, validWhen) are working identifiers, and the
> final observation-kind names are an owner decision settled in Wave 3. The
> exported type names (Strategy, Variant, Skeleton, Hypothesis, Check, Step)
> are likewise provisional.*

That decision was never made. Meanwhile `internal/audit/run`,
`internal/audit/infer` and the committed plan JSON all now depend on these
spellings.

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `Hypothesis`, `HypothesisKind` (+ JSON `hypotheses`, `kind`) | exported types | `strategy.go:212,62`; `variants.go:144` | A candidate conditional rule read out of the document before anything touches the network — "field X may only be valid when sibling Y equals Z". Carries a provenance; the live audit confirms or refutes it, and a confirmed one becomes an **observation**. | Self-declared provisional. The glossary mentions "the strategy's hypotheses" only in passing inside the *triangulating inference* entry. | |
| `HypothesisVariant` = `"variant"` | const, written to strategy JSON | `strategy.go:66` | A gate value selects a distinct set of valid fields. | Coined kind value; *variant attribute* is approved but means something else. | |
| `HypothesisRequiresField` = `"requiresField"` | const | `strategy.go:72` | A field is settable only when a sibling is present. | The approved observation kind for exactly this is `dependsOn`. Two names, one fact. | |
| `HypothesisRequiredWhen` / `HypothesisValidWhen` / `HypothesisMutuallyExclusive` | consts | `strategy.go:69,77,74` | Hypotheses mirroring three observation kinds. | Reuse observation-kind spellings for a second, pre-live concept — so a reader cannot tell from the word whether a thing is a guess or a finding. | |
| `Skeleton` (+ the file `skeleton.go`, `skeleton()`) | exported type + func | `strategy.go:163`; `skeleton.go:109` | The synthesised request body for one variant: field names plus per-field material for building values. Two per variant, minimal and maximal. | Self-declared provisional. A metaphor. The glossary's **request bodies** is the *accepted* counterpart; this is the not-yet-sent one and has no approved name. | |
| `Check` (+ JSON `check`) | exported type | `strategy.go:194,224` | The description of the single live request that would confirm or refute one hypothesis: which step kind, which field, which gate value, what the API is expected to do. | Self-declared provisional — its own doc comment says *"(Working name…)"*. **That comment also claims the term "probe" is retired. It is not** — see §7.2. | |
| `Check.Expect` = `"accept"` / `"reject"` / `"conditional"` | JSON field + string literals | `strategy.go:206`; values `prose.go:137,151,163`, `variants.go:188,227` | What the API is expected to do. | Coined closed set. | |
| `Gate`, `GateKind` (+ `GateRequiredEnum` `"requiredEnum"`, `GateOptionalEnum` `"optionalEnum"`, `GateBool` `"bool"`), and the JSON fields `gates` / `gateField` / `gateValue` | exported types + consts + JSON | `strategy.go:82-90,109,178-179,202-203,219-220,238-239`; `infer/evidence.go:66-67,85-86` | The field whose value decides which shape a request body must take, and how strongly a candidate ranks. | **"gate" appears in the glossary only parenthetically** — "the discriminator (gate) field" — yet here it is a type, a ranking system and a JSON field family spanning three packages. | |
| `SynthHint` (+ JSON `hints`) | exported type | `strategy.go:127`; `skeleton.go:76` | The schema facts a value is built from at run time (type, format, pattern, required, enum values). | A coined compound, **and an abbreviation** — "synth". Crosses into `run` and `cycle.go`. | |
| `Strategy`, `Variant`, `Step` | exported types | `strategy.go` | The compiled per-entity audit plan, one gate value's shape, one step of the program. | Self-declared provisional. | |
| `Program` / `buildProgram` / "step program" | JSON field + func | `strategy.go:268`; `program.go:126` | The ordered list of steps one entity runs. | **step kind** is approved; **"program"** as the name for the sequence is not. | |
| `Strategy.Role` = `"resource"` / `"lookup"` / `"datasource"` | JSON field + literals | `strategy.go:258,302,304,336` | Which of three shapes an entity's audit takes. | See §1.4 — declared twice, and `"lookup"` shortens the approved *lookup-by-key datasource*. | |
| `Budget.Formula` (JSON `formula`) | JSON field | `strategy.go:246,316`; `program.go:260` | A human-readable string recording the arithmetic behind a budget. | Coined artifact field. | |
| `refineReserve` | const | `program.go:63,68,101` | A create-family step's request reserve. | **RETIRED STEM.** The glossary says the Wave 2 name *refinement* is retired and "that spelling no longer appears". It does. See §4.1. | |
| `maximalReserve`, `updateReserve`, `pollReserve`, `deleteConfirmReserve`, `cleanupReserve`, `negativeReserve`, `preflightReserve` | consts | `program.go:72,75,78,82,85,88,91` | Per-step-kind request weights summed into an entity budget. | **"reserve"** is an invented unit of account. The whole family is unapproved. | |
| `perObjectCost`, `readOnlyBudget` | consts | `program.go:36,39` | Requests one live object is "worth"; the two-request budget for a read-only entity. | Coined units and categories. | |
| `Provenance` + `ProvenanceStructural` / `ProvenanceProse` / `ProvenanceDerived` | exported type + consts | `strategy.go:44,49,52,55` | How strongly a variant or hypothesis is grounded. | **Duplicate declaration** — `observe.Provenance` at `observe/observe.go:232` has the identical three values. The *concept* is approved; two copies of it are not. See §4.4. | |
| `proseCategory` + `catRequired` / `catValid` / `catExclusive` | unexported type + consts | `prose.go:19,23,26,28` | Which sort of rule a mined description sentence signals. | Coined type of art and closed set. | |
| `prosePhrase`, `prosePhrases`, `proseHypotheses`, `extractProse`, `proseRequiresField` | type, package var, funcs | `prose.go:32,40,61,89,144` | The fixed English phrase list mined from property descriptions, and the mining functions. | **prose** is approved only as a *provenance value*, not as the name of a mining subsystem. | |
| `detectGates`, `primaryGate` | funcs | `gates.go:16,47` | Find and rank the selector-field candidates. | Built on the unapproved `gate`. | |
| `deriveVariants`, `buildVariant`, `variantHypotheses`, `gatherBranches`, `matchBranch`, `branchAdmitsValue` | funcs | `variants.go:21,56,157,84,106,127` | Compose per-gate-value body shapes from `oneOf` / `anyOf` branches. | **"branch"** and **"variant"** used as terms of art beyond the approved *variant attribute*. | |
| `requiresFieldHypothesis`, `dependentHypotheses`, `mutuallyExclusiveHypothesis`, `conditionalHypothesis`, `sortHypotheses`, `dedupHypotheses` | funcs | `variants.go:222,199,265,287`; `prose.go:156,124` | Builders and ordering helpers for each hypothesis shape. | Compounds of unapproved vocabulary. | |
| `Subjects` (JSON `subjects`), `joinSubjects` | JSON field + func | `strategy.go:216`; `variants.go:302` | The fields an edge is about. | **"subjects"** is a coined JSON field name. | |
| `maxVariantValues`, `hintsFor`, `synthHint`, `indexFields` | const + funcs | `variants.go:12`; `skeleton.go:97,76,116` | Caps and field-list plumbing. | Built on the unapproved nouns. (weak) | |

## 3.2 `internal/audit/run`

The largest package (39 files) and the densest concentration of metaphor.

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `heal` / `healed` / `healing` / `unhealable` | verb, ~20 uses, including **operator-facing error text** | `adjust.go:124,266,293,297,301`; `cycle.go:31,36`; `steps_create.go:18,55,73,106`; `entity.go:273`; `infer/infer.go:187`; `infer/evidence.go:82,110`; `run/evidence.go:75`; `quirkserver/quirks.go:124`; `spec/store/store_test.go:166` | Making a refused create body acceptable: read the refusal, add/remove/borrow a field or cycle an enum value, retry. | A medical metaphor as the verb for a core mechanism. The glossary approves **requestAdjustment** for the *change made* but has no word for the *act*. `steps_create.go:106` prints "adding %s did not heal it" to operators. | |
| `cleanupDebris` / "audit debris" | method + doc prose | `cleanup.go:38,46,50`; `cli/audit.go:140` | The shared pass removing leftover objects from the tenant. | **"debris"** is a colourful coinage for "live objects a previous audit left behind". | |
| `maximalCulprit` / "culprit" / "suspects" | method + locals + doc prose | `steps_create.go:114,136,146-147,151-158,519` | The optional field blamed for a refused maximal create. | **Detective metaphor** naming a real concept — the field a refusal is attributed to. | |
| `entityRecipe` / `recipeOf` / `ent.recipe` | unexported type + func + field | `run.go:346`; `entity.go:19,205` | How an entity creates, addresses and deletes its objects. | **"recipe"** is a coined metaphor for a concept the glossary never names. | |
| `cycleConditional` / "value-cycling" (+ the file `cycle.go`, `maxCycleAttempts`, `cyclableSiblings`) | method + named subsystem | `cycle.go:3-13,29,42,128,179`; referenced `adjust.go:28,297` | Retrying a create with different enum values for a sibling field until one is accepted. | A named subsystem with its own file, doc header, cap constant and evidence type — pure coinage. **"cyclable"** is invented. | |
| `claim` / `stepClaims` / `emitHaltedClaims` / `halt` | unexported type + funcs + method | `entity.go:58,61,104,392,401,425` | The observation a step would have produced had it run, and why the entity stopped. | **"claim"** appears in glossary prose ("how far the audit got with one claim") but is never listed; here it is a type, a mapper and an emitter. **"halt"** is a coined verb. | |
| `searchMinimal` / `searchAllowance` / `searchCandidates` / "the additive search" | methods + func + named mechanism | `steps_create.go:86,96-101,362,369,377,386,390,431` | Adding one field at a time until the API accepts a create. | **"additive search"**, **"allowance"** and **"candidates"** name a mechanism the glossary does not have. | |
| `reduceMaximal` / `bisectMaximal` / `bisectionAllowance` | methods + func | `steps_create.go:149,475`; `synth.go:76`; `strategize.go:589` | Dropping fields, then halving the optional-field set, to find the one field a refusal is about. | **"bisection"** is coined and, unlike the rest, **serialised into plan JSON** (§1.4). | |
| "refusal grammar" / `classifyRefusal` / `refusalAction` / `actKind` / `actStop` / `actAdd` / `actRemove` / `actRequires` / `actBorrow` | named subsystem + types + consts | `adjust.go:12,85,88-92,96,129,133,179,201` | Parsing a 4xx message into an instruction. | **"grammar"** is named as a subsystem. `actStop` has no approved counterpart; the other four match the approved `requestAdjustment` actions but abbreviate them to `act*`. | |
| `strategize` (+ the file `strategize.go`) | func + file name | `strategize.go:46`; called `run.go:236` | Replacing an entity's plan steps with its compiled strategy's program. | A coined verb formed from an unapproved noun, promoted to a file name. | |
| `translateProgram`, `typedGate`, `collectHints`, `variantValue`, `numericVariant`, `findVariant` | funcs | `strategize.go:168,281,296,438,482,539` | Turning strategy steps into executable plan steps, and the value synthesis around them. | Built on `program`, `gate`, `SynthHint` and `variant`. | |
| `addressing` / `addressingOf` | unexported type + func | `strategize.go:87,102` | The paths and path-parameter values an entity's requests use. | **addressing attribute** is approved as a *generated attribute*. A struct named `addressing` meaning "paths + path values" is a different, unapproved concept sharing the word. | |
| `synthSkeletonBody`, `synthValue`, `synthField` | funcs | `strategize.go:339,363,381` | Building a request body or one value from schema material. | **"synth"** as a domain noun-prefix, and an abbreviation. | |
| `nameToken` / `NameBearing` | func + **exported** func | `strategize.go:583`; `plan/synth.go:278,284` | The invented value stamped into a name field, and the test for which fields get one. | **"name-bearing"** and **"name token"** are coined terms of art, one of them exported across packages. Only `NamePrefix` is approved. | |
| `preflight` / `preflighted` / "foreign-object pre-flight" / "foreign" | method + field + named concept | `run.go:4-6,65,105-106`; `entity.go:35,164-166,185,192,195` | Reading a collection before the first create to check the tenant looks like a sandbox, and counting objects the audit did not create. | **"pre-flight"** collides with the glossary's *"the offline preflight"* for `config validate`, and with the release job name (§1.8) — three senses. **"foreign"** is a coined counted category. `--force-api-audit` is approved but the check it bypasses is unnamed. | |
| `guardMutation` / "host allowlist" | method + named mechanism | `client.go:76,155,260,263,265`; `run.go:3`; `cleanup.go:29` | Refusing a mutating request to any host other than the base URL's. | A named safety mechanism; unapproved, though arguably generic security vocabulary. (weak) | |
| `registry` (runner field) | field | `run.go:307` | Map of entity → its current live object, resolving `$created:<entity>`. | **Clashes with the approved `emit.Registry`** — a second unrelated sense of the same domain word, and a third exists in the emitted mocks (§2.1). | |
| `borrowed` (cache) / `collectionPaths(noun …)` | field + func | `run.go:328`; `borrow.go:52` | The per-collection id cache; guessing a collection's path from a noun in a refusal. | `borrow` is approved as a `requestAdjustment` action; the **cache** and the **noun→path guess** are unnamed extensions of it. (weak) | |
| `valuesProof`, `undeclaredProof`, `updProof`, `createProof`, `readProof`, `appendProof` | struct fields + func | `evidence.go:43,50,59,61,62`; `steps_create.go:355` | Excerpts kept as backing for a finding. | **"proof"** used as a structural noun-suffix; the glossary uses it only in prose. `updProof` is also an abbreviation. | |
| `combinedRefusals` / `FieldPair` (JSON `a`/`b`) | field + exported type | `evidence.go:73`; `infer/evidence.go:72` | Field pairs a create was refused for carrying together. | **"combined refusal"** is a coined signal category. | |
| `conditionalValues` / `ConditionalValue` | field + exported type + JSON | `evidence.go:78`; `infer/evidence.go:84` | One value-cycling outcome. | Coined evidence type; serialised. | |
| `adjustResult.gaveUp` / `.tried` / `.conditional`, `isConditionalRefusal` | struct fields + method | `adjust.go:70,74,81`; `cycle.go:179` | Whether the loop ended without success; the fields it tried; a refusal that named a declared field but fit no grammar rule. | **"gave up"** is colloquial for a state the summary reasons about; **"conditional refusal"** is a coined category. | |
| `namedKnownFields` / "generalized field extraction" | func + named mechanism | `cycle.go:196`; `adjust.go:26` | Scanning a refusal for any field the entity declares. | Coined subsystem name. | |
| `condCoords` | func | `cycle.go:112` | The three-part key a value-cycling result is recorded under. | **"coordinates"** metaphor for an evidence key. | |
| `recordConditional`, `recordConditionalInconclusive`, `primaryGate` | methods | `cycle.go:145,158,168` | Append a value-cycling outcome or an untested edge; the field ranked likeliest to be the selector. | Built on unapproved evidence and gate vocabulary. | |
| `learnID`, `idFromSelfLink`, `envelopeKeys` | funcs | `id.go:30,104,145` | Extracting a created object's identifier; reading an id out of a self-link; listing likely wrapper keys. | **"learn"**, **"self link"** and **"envelope key"** used as toolkit terms; `envelope` is approved only as a `listResponseShape` value. (weak) | |
| `unresolved()` / `resolve()` / "settled" | ledger methods + doc term | `ledger.go`; used `cleanup.go:66,70,107,139,223-225` | An intent line not yet matched by a created/rejected/deleted line. | **"unresolved" / "settle"** as ledger-state vocabulary. (weak) | |
| `requiredWhenPair`, `undeclaredUnstable`, `maxAdjustIters`, `cleanupAllowance`, `minPrefixChars`, `requiredPrefixToken` | types, fields, consts | `evidence.go:84,53`; `adjust.go:51`; `run.go:123,127,135` | The two halves of a required-when test; a field whose observed JSON type differed between reads; assorted caps. | Coined, mostly minor; `maxAdjustIters` and `updProof` are also abbreviations. (weak) | |
| `createdObject`, `entityState`, `blockedError`, `budgetError`, `reqSpec`, `httpResult`, `refused()`, `mentions()` | unexported types + methods | `run.go:338`; `entity.go:17`; `client.go:22,28,34,43,57,72` | One live object; one entity's in-flight state; the two errors that classify a stop reason; request/response records. | Mundane, but `reqSpec` abbreviates twice over and `mentions` names a real inference step. (weak) | |
| `activityKind` values `intent` / `created` / `rejected` / `deleted` | consts written to `<runid>.activity.jsonl` | `ledger.go:32,34,38,40` | The four line kinds in the activity ledger. | **Approved** — the glossary's *activity ledger* entry lists exactly these four. Confirmed clean. | — |

## 3.3 `internal/audit/observe`

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `Kind` values not in the glossary: `writable`, `immutable`, `requiredByAPI`, `requiredWhen`, `serverDefault`, `derivedDefault`, `normalisation`, `ignoredOnUpdate`, `serverForced`, `volatile`, `values`, `updateStyle`, `deleteNotFoundOK`, `readAfterWrite` | consts written to committed observations | `observe.go:40-131` | The fourteen scalar observation kinds. | The glossary names only 7 of the 21 kinds in code, and describes `undocumentedFieldInSpec` as "the fifteenth" — implying these fourteen were once settled but never recorded. **This is most likely a glossary gap rather than a naming defect** (see §7.3), but the owner should confirm each spelling. | |
| `Provenance` (second declaration) | exported type + consts | `observe.go:232-234` | How strongly an inferred edge is grounded. | The *concept* is approved. **Two identical declarations** — see §4.4. | |
| `Excerpt.PathTemplate`, `RequestFragment`, `ResponseFragment`, `MaxFragmentBytes`, `redacted` | JSON fields + consts | `redact.go:14,22,26,27,54` | See §1.4 — these reach committed observation files. | | |
| `sensitiveKeyParts`, `containsSecret` sweep | package var + doc phrase | `redact.go:60,147` | Substrings marking a JSON key whose value is stripped; the post-substitution check that no secret survived. | **"sweep"** is a metaphor. Generic security vocabulary otherwise. (weak) | |
| `entityFile`, `canonicalValue`, `ComputeID`'s `"tfpfgen-observation"` salt | unexported type + func + string | `file.go:22,40`; `observe.go:365` | The shape of one `<entity>.observations.json`; JSON round-tripping so typed and decoded forms encode identically; the hash-domain salt. | Mundane. (weak) | |
| `updateStyles`, `jsonTypes`, `listEnvelopes`, `listPaginations` | package vars | `observe.go:254,255,303,310` | Validation sets for the approved value vocabularies. | **Not violations** — every member is approved. Confirmed clean. | — |

## 3.4 `internal/audit/infer`

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `EdgeKinds`, `edge`, `edgeAttr` | exported package var + methods | `infer.go:14`; `edges.go:19,25` | The four observation kinds counted as "edges", and the builders for one. | **"edge"** is glossary prose only ("the core conditional edge"); here it is an exported closed set and a constructor family, and it reaches `audit/summary.json` (§1.4). | |
| `hypothesisGaps`, `hypothesisObservations` | methods | `infer.go:378,399` | Turning unconfirmed hypotheses into inconclusive observations. | **"gap"** is a coined category ("tested-and-unconfirmed"); both are built on the provisional `hypothesis`. | |
| `valueConditionalConfig` | method | `infer.go:198` | A `validConfiguration` learned from value-cycling rather than from field sets. | **"value-conditional"** as a named second rule. | |
| "variant diffing" | named mechanism in doc comments | `infer.go` header; `evidence.go:101,111`; `run/evidence.go:66`; `observe/observe.go:169` | Comparing accepted field sets across gate values — the way `validWhen` is learned. | A named mechanism the glossary's `validWhen` entry describes but does not name. | |
| `bothDirections` / "both-direction corroboration" | local + doctrine in doc comments | `infer.go:219`; `evidence.go:81,112`; `cycle.go:12` | The rule that an edge needs one positive and one negative signal. | Named discipline; central to the package and unapproved. | |
| `validWhenEdges`, `requiredEdges`, `candidateFields` | methods | `infer.go:141,300,116` | The per-rule assertion functions. | Built on `edge`; **`candidateFields`** names a concept ("fields variant diffing has something to say about"). | |
| `gate`, `gateOf`, `acceptedUnder`, `removedUnder`, `acceptedAlone` | type + funcs | `evidence.go:134,143,167,192`; `infer.go:285` | The selector field and value-bucketed field sets. | Built on the unapproved `gate`. | |
| `rank(Outcome)`, `dedup`, `keyOf` | funcs | `edges.go:13,44,65` | Ordering outcomes by informativeness; collapsing duplicates. | **`rank`** encodes an unapproved strength-ordering over the four approved outcomes. (weak) | |
| `provenanceFor`, `provenanceForSet`, `variantProvenance`, `requiresProvenance` | methods | `infer.go:159,178,260,290` | Provenance lookups per rule. | Built on approved `provenance` with coined qualifiers. (weak) | |
| `AdjustAction` + `AdjustAdd` / `AdjustRemove` / `AdjustRequires` / `AdjustBorrow` | exported type + consts | `evidence.go:40-49` | The four changes the adaptive executor made. | **Approved** — these are the `requestAdjustment` actions. Confirmed clean. | — |
| `soleArrayKey`, `paginationOf`, `RejectsUnknownFields`, `model`, `newModel` | funcs, field, type | `evidence.go:128,240,261`; `infer.go:84,93` | Finding the wrapping key; classifying pagination; the caution flag; the precomputed per-entity picture. | Values and the flag spelling are approved; the rest is generic. (weak) | |

## 3.5 `internal/audit/plan`

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `synth` (type) + `minimalBody`, `maximalBody`, `objectValue`, `formatValue`, `typeValue`, `variant`, `formatVariant` | unexported type + methods | `synth.go:21,33,53,127,150,187,208,253` | Value synthesis from schema material. | **"synth"** as a domain noun-prefix, and an abbreviation. **`variant` here means "a second, different value for an update"** — a *third* unrelated sense of the word in this repo. | |
| `NameBearing`, `nameToken` | exported func + method | `synth.go:278,284` | See §3.2. | | |
| `bisectionAllowance`, `BisectionAllowance` | func + exported JSON field | `synth.go:76`; `plan.go:132` | See §1.4. | | |
| `adminSkip` | method | `derive.go:149` | The exclusions the operator decided (config exclude plus inputs skip). | **"admin"** as a category of skip reason. | |
| `EntityPlan.Role`, `DeclaredProperties`, `ParentRefs`, `entityInputKeys` | exported fields | `plan.go:154,164`; `inputs.go:29-41` | See §1.4. | | |
| `undocumentedEnumValue` = `"tfpfgen-undocumented"`, `undeclaredSpecFieldName` = `"tfpfgen_unknown_field"` | consts | `steps.go:12,16` | See §1.5. | | |
| `requiredWhenHint`, `pollSpec`, `conditionalSteps`, `negativeSteps` | funcs | `steps.go:189,231,296,306` | Step-derivation rules. | **"negative step"** and **"conditional step"** are coined step categories beside the approved closed step-kind set. (weak) | |
| `Skipped`, `deriver`, `parentFor`, `pathValues`, `successSchemaOf`, `declaredProperties`, `cloneBody` | types + funcs | `plan.go:171`; `derive.go:136,178,213`; `steps.go:121,131,322` | Plan plumbing. | Mundane. (weak) | |
| `RunIDToken` (`<runid>`), `CreatedRef` (`$created:`), `envRefOpen`/`envRefClose` (`${VAR}`) | consts + funcs | `plan.go:29,36-37,42,48,242` | The three plan tokens. | **Approved** — all three spellings are explicitly in the glossary. Confirmed clean. | — |

## 3.6 `internal/spec/revise`, `internal/spec/correction`, `internal/spec/store`

Most of this package's findings are in §1.2, §1.3, §1.4 and §1.7, because they
reach disk or operator output. What remains:

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `Explanation` / `explanations` / `Explain` | exported type + package var + func | `revise/explain.go:27,52`; used `report.go:160` | The per-kind five-sentence account of a finding (title, means, expected, observed, merging, closing). | A coined term of art with a bidirectional-coverage test behind it. | |
| `Proposals` and its buckets `Suppressed`, `NotConfirmed`, `AlreadyStated`, `NoForm`, `Vetoed`, `Unplaceable`, `Stale`, `AutoAccepted`, `Proposed` | exported type + fields | `revise/propose.go:75,80-102` | The reason-categories an observation falls into when it does not become a correction. | A closed set of coined reason categories an operator reads. See §1.7. | |
| `category` + `catCompiled` / `catAlreadyStated` / `catNoForm` / `catUnplaceable` / `catVetoed` | unexported type + consts | `revise/compile.go:19-35` | The same categories inside the compiler. | Coined closed set; `cat` is also an abbreviation. | |
| `compilableKinds` / `CompilableKinds()` | package var + exported func | `revise/propose.go:334,352` | The observation kinds `audit.auto_accept` may name. | **"compilable"** is a coined adjective forming a closed set an operator's config is validated against (`config/validate.go:126`). | |
| `Finding`, `Group`, `GroupBranch`, `buildGroup`, `buildReport` | exported types + funcs | `revise/report.go:41,48,80,123,155,235` | See §1.4. | | |
| `Materialize` | exported func | `revise/revise.go:58` | See §1.7. | | |
| `refusePending` / "pending decision" | func + named gate state | `revise/revise.go:96-100`; `report.go:29` | Failing while any proposed correction awaits a human. | **"pending decision"** as a named gate state. (weak) | |
| `propSite`, `schemaSite`, `propertiesSite`, `declarationPointer` | unexported type + funcs + field | `revise/locate.go:215`; `compile.go:448`; `compile_edges.go:100,187` | The place in the YAML tree a correction targets. | **"site"** as a domain noun; also `prop` is an abbreviation. | |
| `literalSpelling` | func | `revise/compile.go:690` | Renders a decoded scalar the way the document spells it. | **"spelling"** as a domain verb-noun, matching the coined `valueSpelling` JSON field. (weak) | |
| `variantSets`, `highestOrdinal`, `ordinalName`, `autoName`, `compiledName`, `revisedState` | funcs | `revise/propose.go:116,294,297,302,384,400` | Per-gate-value field sets; the `NNN-` / `auto-NNN-` file numbering; the compiler's input. | The `auto-NNN-` prefix is approved; **"ordinal"** as the toolkit's word for the number is not. (weak) | |
| `compiler`, `compiled`, `stated()`, `unplaceable()`, `locator`, `nodeAt`, `followSchemaRefs`, `contentSchema`, `extensionNode`, `kebab`, `sanitiseRef`, `codeList`, `plural`, `describeValue`, `describeValues`, `describeListShape` | unexported types + funcs | `revise/compile.go:40,47,48,52`; `locate.go:22,34,85,104`; `report.go:244,268,289,315,345,369,386` | Compiler internals and report rendering. | Prose; the kebab branch spelling itself is approved. (weak) | |
| `dependencyOrder`, `flatOp`, `flatten`, `properPrefix` | func + unexported type + funcs | `correction/correction.go:134,152,160,195` | Reordering operations so a container-creating `add` runs before operations addressing its descendants. | **"dependency order"** is a named mechanism with load-bearing semantics. **`flatOp` carries the retired `Op`** — see §4.1. | |
| `"stale"` refusal | error category | `correction/correction.go:16` | An `add` whose value is already present, meaning the vendor fixed the document. | The same coined "stale" category as in `revise`. | |
| `Lock`, `Outcome`/`Pinned`/`Unchanged`/`Repinned`, `ShortSHA`, `Retrieve`, `retrieveTimeout` | exported types + funcs | `store/store.go:42-72`; `retrieve.go:16,21` | See §1.3 and §1.4. `Retrieve` and `ShortSHA` are mundane. | | |
| `Marker`, `Written`, `Note`, `Justification`, `Evidence`, `Operation`/`Op`/`Path`/`Value`, `Suffix`, `DirName`, `OutputName`, `ProposedDirName`, `RejectedDirName`, `ForceBlockStyle`, `Verify` | exported types, fields, consts, funcs | across `revise` / `correction` / `store` | The rejection marker and its JSON shape; the correction's required justification and evidence pointer; the RFC 6902 operation shape; the approved directory and file names. | **Not violations** — all approved or external contract. Confirmed clean. | — |

## 3.7 `internal/specmodel`

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `Op` | exported type | `classify.go:36`; leaks to `classify.go:59-63,71,363,370`, `plan/derive.go:224`, `strategy/strategy.go:362`, `revise/locate.go:77,104,124` | A method + path + operationId triple identifying one HTTP operation. | **RETIRED.** The glossary retires `Op`/`Ops` in favour of `Operation`/`Operations`. Retired next door to the package that was cleaned. See §4.1. | |
| `Classification.Extra` (type `[]Op`) + `extraRefs` | exported field + func | `classify.go:71,370` | Operations on an entity's paths that fit no role slot. | **"extra"** as a named category of operation. | |
| `Exclusion`, `exclusionReason`, `Classifications.Excluded` | exported type + method + field | `classify.go:86,98,309` | An entity that classifies as nothing, and the sentence saying why. | **"exclusion"** as a domain noun, distinct from the config's `services.exclude`. The approved noun for a refusal is *unsupported* / the `unsupported.json` report. Also declared in the IR (`ir/model.go:47`). | |
| `Classification.MissingUpdate` | exported field | `classify.go:74` | The API declares no update operation for this entity. | Coined flag for a concept the glossary does not carry. | |
| `entity.collectionWrite` | unexported field | `classify.go:151` | A PUT or PATCH on the collection path itself. | Coined operation-position name; load-bearing for the singleton rule. | |
| `loneWrite` | local driving the `action` branch | `classify.go:229` | A single write with nothing to read, list or delete it. | Coined — and it is the only name the rule that produces an **action** has. | |
| `presentRoles` / "role slots" | method + named mechanism | `classify.go:56-58,336` | Which of create / read / update / delete / list an entity has. | **"role slot"** is a named mechanism used in `docs/mapping.md` and in the glossary's *action* entry, but never listed. (weak) | |
| `ValidVariant` (fields `Value`, `Fields`) | exported type | `extensions.go:137` | One discriminator value and the properties valid under it. | The approved vocabulary is `validConfiguration` "carrying the discriminator and the per-value valid field sets". `ValidVariant` is a new noun for that half. | |
| `keyForPath`, `splitPath`, `singularize`, `trailingParam`, `pendingRef`, `resolve`, `componentName`, `Describe`, `SuccessSchema`, `Extensions`, `extensionShapes`, `parseExtensions`, `suggestExtension`, `ValidListPagination`, `Schema.AdditionalPropertiesDeclared` | funcs, types, fields | `classify.go:117,389,392`; `refs.go:10,19,36`; `describe.go:9`; `extensions.go:16,96,161,183,208`; `model.go:159` | Path-to-key derivation, `$ref` resolution, extension parsing. | Mechanical. **`trailingParam` carries the retired `Param`** (§4.1); the rest is prose. (weak) | |
| `KindResource`/`KindDatasource`/`KindListResource`/`KindAction`, `ListEnvelopeWrapped`/`ListEnvelopeBare`, `RequiredWhen`/`ValidWhen`/`DependsOn`/`ValidConfiguration`/`ListResponseShape` types, `DependentRequired`/`DependentSchema`/`Discriminator` | consts + types | `classify.go:16-27`; `extensions.go:81,83,100-144`; `model.go:187,198,205` | Entity kinds, envelope values, typed extension readings, JSON-Schema constructs. | **Not violations**, except that `KindListResource` = `"list-resource"` is one of four spellings — see §4.2. | — |

## 3.8 `internal/intermediate_representation`

Abbreviations in this package are a separate rule and are gathered in
**Section 5**. This table is coined *domain vocabulary* only.

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `flat` / `flatten` | unexported type + func | `attributes.go:12,66` | A schema with `$ref`s resolved and `allOf` folded into a single view of one property. | A coined noun for a first-class derivation concept — **and it reads as an abbreviation of "flattened"**, which the package's fully-worded rule forbids. Spreads to `flatPrimary`, `flatCreate`, `flatRead`, `flatUpdate`, `flatValue`, `flatItems`. | |
| `site` | unexported type | `attributes.go:404` | One property seen from both the create and the read side of the fold. | A metaphor for a core derivation concept. Also used in `spec/revise` (§3.6) for something else. | |
| `attributeEdges` | unexported type | `attributes.go:423,563` | The cross-attribute rules read off one property for the tree to aggregate. | **"edge"** is glossary prose for the inference only; as an IR type it is a second coinage. | |
| `CoManagementNote` / `coManagementNote` | exported struct field + func | `model.go:56,100,127,144,161` | The prose appended to a schema description when sibling entities derive from one API collection. | See §2.1 — this reaches every generated provider. | |
| `AdvisoryValues` | exported struct field | `model.go:367`; `attributes.go:557` | An open enum's known values, documented but never validated. | **"advisory"** is invented jargon for a value category. | |
| `ConditionalRequirement(s)`, `ConditionalValidity(s)`, `Dependency(ies)`, `MutuallyExclusiveGroups`, `ValidConfiguration`, `ConfigVariant` | exported types + fields | `model.go:249-295` | The aggregated `required-when` / `valid-when` / `depends-on` / `mutually-exclusive` rules. | The **observation kinds** are approved; these are a parallel, unapproved IR spelling of the same four facts. `ConfigVariant` is a fresh coinage beside the approved *variant attribute*. | |
| `Exclusion` / `Excluded` / `configExcludedReason` = `"excluded by configuration"` | type, field, const + string | `model.go:36,47`; `derive.go:18` | One entity that produced nothing, and the reason stamped on it. | See §3.7 — declared in two packages, and the reason string is a domain label. | |
| `EventualConsistency` / `maxEventualConsistency` | exported field + func | `model.go:91`; `derive.go:607` | The largest declared read-after-write lag across an entity's lifecycle. | Named concept driving generated waits; see §2.1 and §1.1. | |
| `SilentlyIgnoredOnUpdate`, `DeleteNotFoundOK`, `MissingUpdate`, `UpdateStyle`, `ListEnvelopeKey` | exported struct fields | `model.go:79,88,93,105,130,147,370`; `attributes.go:446`; `derive.go:333` | Behaviour flags read from the extension keys. | Each carries an unapproved extension key's wording into the IR. **"envelope"** appears in the glossary only inside `listResponseShape`'s description, never as a field name. | |
| `Timeouts` / `defaultTimeouts` | exported type + func | `model.go:378,387` | The generated per-operation timeout defaults. | Named generated-artefact concept. | |
| `kept` / `keep` / `family` / "collision family" / `disambiguateKey` | unexported type, locals, func | `derive.go:49,53,59,84,147` | The entities surviving exclusion; the set of entities whose collection paths derive one key; and the rule that renames the later one. | **"collision family"** and **"disambiguate"** name an algorithm; unapproved. | |
| `deriver` | unexported type | `derive.go:202` | Holds the operation index every entity builder reads. | Agent-noun coinage; the glossary approves the verb `Derive` only. Also declared in `audit/plan`. | |
| `companionTree` / "companion datasource" | local + named concept | `derive.go:432,437`; and `emit/render_datasource.go:241,243,247` (in user-facing error strings) | The datasource derived beside a resource. | **"companion"** is glossary prose only, yet it names a datasource kind beside the approved *lookup-by-key datasource* and *filter attribute*, and it reaches operator-facing error text. | |
| `resolveUnion` / `unionBranches` / `hasUnion` | method + fields | `attributes.go:54,55,191` | Collapse an all-scalar `oneOf` / `anyOf` to one declared type. | `OneOf` is approved; **"union" / "union branch"** is a separate coinage. (weak) | |
| `refuse`, `refuseReservedRootNames`, `reservedRootNames`, `foldBound`, `ensureID`, `ensureParentParameters`, `ensureFilterAttributes`, `requireKey`, `enclosingEntity`, `sortedConditionals`, `sortedValidities`, `sortedDependencies` | funcs + var | `attribute_types.go:171,181,197`; `attributes.go:169,326,346,366`; `attribute_addressing.go:11,48,104,168`; `derive.go:509` | Marking attributes unsupported; the terraform-reserved name set; the fold's bound rule; synthesising the id, addressing attributes and filters; finding an action's parent. | Approved concepts, unapproved function vocabulary. (weak) | |
| `identifierWord`, `leadWithALetter`, `boundaryBefore`, `Names.Pascal`, `Names.Camel` | funcs + fields | `naming.go:17,19,146,237,248` | Reduce a name to Go identifier characters; the exported and unexported spellings. | Mundane; `Pascal` / `Camel` are truncations of PascalCase / camelCase used as term-of-art field names. (weak) | |
| `Model`, `Resource`, `Datasource`, `ListResource`, `Action`, `AttributeTree`, `ComputedOptionalRequired`, `Operation(s)`, `Names`, `AttributeType`, `Parameter`, `PathParameters`, `APIVersionDirectory`, `OneOf`, `ElementType`, `GoName`, `TerraformName`, `Classification.LookupByKey` | exported vocabulary | throughout | — | **Approved.** Confirmed clean. | — |

## 3.9 `internal/emit`

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `unrenderableError` / `unrenderable` / `excludes` | type + funcs | `services.go:48,53,59` | The error marking an entity whose shape emission cannot serve, so the rest still generate. | **"unrenderable"** is a coined adjective naming a refusal category. | |
| `KeptUnbound` / `keptUnboundKey` / `joinTreeKeeping` / `recordKept` | exported field + funcs | `services.go:35,209,227,230,235,381` | Attributes the join kept although the SDK carries no field for them — the id and the addressing attributes. | **"kept unbound"** is a coined two-word concept with its own key format and its own reporting rule. | |
| `companionDatasource` / `companionItemTree` / `companionAddressing` / `companionFilters` / "// Companion fields." | methods + template-context grouping | `render_datasource.go:38,241,424,590,636` | The filters-and-items datasource that accompanies a resource. | See §3.8. Two of the uses are inside **user-facing error strings** (`:243`, `:247`). | |
| `callPlan` (+ `ParameterDeclarations`, `Assign`, `Payload`, `ClosureBody`, `Imports`) | type + template-consumed fields | `render_mapping.go:337-353`; documented as a concept at `templates/templates.go:31` | One SDK invocation rendered for a template. | **"call plan"** is a named toolkit concept; the approved binding vocabulary is `Bindings`, `Call`, `FieldAccess`, `Segment`. It also collides with terraform's "plan". | |
| `writePlan` | type | `render_convert.go:86` | One resolved write-direction conversion. | Same "plan" coinage and the same terraform collision. | |
| `RequiredWhen` / `emitRequiredWhen` | generated type-name suffix + emitter | `render_validators.go:68,182,184` | See §1.1 and §2.3. | | |
| `stockValidator` / "stock" | func + the word throughout | `render_constraints.go:132,137`; `render_validators.go:552`; `render_resource.go:353` | A validator taken from a HashiCorp validators package rather than generated here. | **"stock validator"** is a coined category distinguishing two kinds of emitted validator. | |
| `schemaKind` + `schemaResource` / `schemaDatasource` / `schemaAction` / `schemaListResource` | named int type + closed constant set | `render_schema.go:16-23` | Which framework schema package a declaration is rendered against. | A coined closed kind-set beside the approved registry slots and service-template directories. | |
| `schemaType` | type | `schema_type.go:22` | Every framework name that follows from one attribute type. | Coined type name (its *fields* are the spec's and are fine). | |
| `modelNamer` / `newModelNamer` / `modelDeclaration` / `validatorNamer`, and inside them the locals `survey` and `claimants` | types + funcs + locals | `render_schema.go:331,351,358,360,362`; `render_validators.go:37` | Assign collision-free Go struct and validator-type names to nested objects. | **"namer"** names a concept; **"survey"** and **"claimant"** are colourful coinages for the pre-pass that finds contested names. | |
| `audience` | concept in doc comments | `render_resource.go:667,680` | A leftover name for what is now `fixtures.Form`. | **Two spellings for one approved concept** — the comment describes `Form` as an "audience". | |
| "the ListWrap defect" | named defect in doc comments | `render_validators.go:315`; `services_test.go:281`; `docs/rehearsal.md:137` | The old behaviour where every generated list mock assumed a `"value"` envelope. | An invented proper-noun codename for a past bug. Also a comment-style violation — see §6.4. | |
| `paramFailure`, `paramField`, `paramNode`, `paramValue`, `paramSegmentIndex`, `actionParamNodes`, `integerParsedParams`, `UpdateParamCopies` | types, funcs, template field | `render_mapping.go:359,462,472,552,670`; `render_resource.go:450,610`; `render_action.go:192` | Path-parameter plumbing. | **RETIRED `Param`/`Params` spelling** — see §4.1. `UpdateParamCopies` is in the template contract. | |
| `op` as a parameter name | func parameter | `render_resource.go:563-565`; `render_listresource.go:99` | An `*ir.Operation`. | **RETIRED `Op` abbreviation** — see §4.1. | |
| `kindListResources` = `"list-resources"`, `bindingKindListResource` = `"list_resource"` | consts | `services.go:196,223` | The emitted directory name and the binding-kind match string. | Two of the four spellings of one approved term — see §4.2. | |
| `deriveFixtures`, `supportedTree`, `serviceRenderer`, `identityAttribute`, `schemaBuilder`, `respDiagnostics`, `streamDiagnostics`, `invocable`, `pinBySDKType`, `resultLineDepth`, `deprecationMessage`, `unitEndpoint` | types + funcs + consts | `services.go:201,453,493,503`; `render_identity.go:14`; `render_schema.go:61,65`; `render_mapping.go:371,384`; `render_action.go:333`; `render_listresource.go:339`; `render_resource.go:572` | Assorted rendering plumbing. | **"supported tree"**, **"invocable"** and **"pin"** are mild coinages; `unitEndpoint` and `deprecationMessage` reach generated code (§2.3). (weak) | |
| `HasEC` / `ECDuration` | template-consumed fields | `render_resource.go:93-94`; consumed `crud.go.tmpl:6,11,72,182` | Whether the entity declares eventual consistency, and how long. | The concept is unapproved (§2.1) **and `EC` is an abbreviation** in a template contract. | |
| `ResourceCtor` | template-consumed field | `render_listresource.go:41` | The expression building the resource a list resource lists. | **`Ctor`** is an abbreviation in a template contract. | |
| `RegistryName`, `ExpectedFirstID`, `UnitChecks`, `MinimalChecks`, `MaximalChecks`, `FilterChecks`, `SingletonID` | template-consumed fields | `render_datasource.go:46,65,71,461`; `render_listresource.go:50`; `render_resource.go:76,99-100` | The mock registry key, and blocks of generated test assertions. | `RegistryName` overloads the approved `Registry`; the rest are mundane. (weak) | |
| `RenderServices`, `ServiceFiles`, `Registry`, `Register`, `Registrations`, `RegistrySlots`, the `// tfpfgen:<slot>:imports|registrations` sentinels, and `unsupported.json`'s whole shape | exported vocabulary | `registry.go`, `services.go`, `unsupported.go` | — | **Approved.** Confirmed clean. | — |

## 3.10 `internal/sdkbind`

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `dialect` (interface) + "dialect-neutral" | exported interface + the phrase throughout | `binder.go:10,12,13,33,133,143`; `model.go` package doc; `ir/model.go:186,228`; `ir/derive.go:228`; `providergen/curated_test.go` (17 uses) | What a backend contributes to the shared binding walk. | **The glossary's word for this is `backend`.** A second vocabulary for one concept, spanning four packages. (The JSON-Schema-dialect uses in `sdkgen/prenormalize.go:410` and `spec/revise/compile_edges.go:250` are legitimate external vocabulary and are *not* findings.) | |
| `json:"element_kind"` | **JSON tag persisted to disk** | `model.go:288` | The wire key the `ElementType` field serialises under. | **RETIRED.** `ElementKind` is explicitly retired in favour of `ElementType`; the field was renamed but the tag was not. See §4.1. | |
| `CallParam`, `Params` (+ `json:"params"`), `callParams`, `paramName`, `"param_"`, `settleParamTypes` | type, field, funcs, string literal | `model.go:212,224,226,230`; `binder.go` (`localFor`, `callParams`); `prune_calls.go:242` | The locals a call expression references. | **RETIRED `Param`/`Params` spelling**, including on a JSON tag and in a string emitted into generated code. See §4.1. | |
| `settle` family: `settleCall`, `settleScalar`, `settleCreateID`, `settleUpdateBody`, `settleEachDirection`, `settleParamTypes` | funcs | `prune.go:180,229`; `prune_calls.go:138,242`; `prune_fields.go:104,284` | Resolve a drafted spelling against the loaded SDK. | **"settle"** is a coined verb for the prune step; the glossary's words are *resolve* and *repair*. | |
| `hop` / `resolveFieldHop` | concept + func | `model.go:218` ("one hop per field selection"); `prune_calls.go:75` | One step of a call chain. | **`Segment` is the approved word for exactly this.** Two names, one concept, in one package. | |
| `Removal` (+ `Removed`, and `Kind` = `"resource"`/`"datasource"`/`"list_resource"`/`"action"`) | exported type + closed string set | `model.go:340`; `verify.go:13`; `prune.go:101,272,310,329`; `verify.go:119,132,149,159` | One thing pruning deleted, with the SDK's reason. | The approved sdkbind vocabulary is `Bindings`, `Call`, `FieldAccess`, `Segment`, `ElementType`, `prune`, `binder`. `Removal` is not among them, and its `Kind` set is a closed on-disk label set the glossary does not fix (it fixes only `unsupported.json`'s `stage`). | |
| `Problem` / `problem` / `Dropped` / `DropProblems` | exported types + methods | `verify.go:12,112,452,488` | One binding that does not match the SDK, and one entity verification removed. | Coined finding and refusal categories parallel to `Removal`. | |
| `unbuildableReason` + its two reason literals | func + strings that land in `unsupported.json` | `prune.go:376,387,389` | Names the missing direction after pruning, or empty. | **"unbuildable"** is invented jargon, and the two reason strings reach a committed artifact in provider repos. | |
| `accessMode` + `accessReadWrite` / `accessReadOnly` / `accessWriteOnly` | named int kind + constants | `binder.go:31,34-38` | Which directions a kind's fields travel. | Coined closed kind set. | |
| `EnvelopeKey`, `CollectionAccess`, `CreateIDAccess`, `NestedNilable` / `nilableType` | exported struct fields + func | `model.go:160,245,250,267,268,330`; `prune_fields.go:223` | The observed wire key wrapping a list's items; the access from a list result to its element slice; the accessor a create answers the id through; whether a nested accessor can return nil. | **"envelope"** as a binding-model field, and **"nilable"** as invented jargon. | |
| `FieldBinding.Attr` (+ `json:"attr"`) | field + persisted label | `model.go:278` | The terraform attribute name. | **`attr` abbreviation used as a persisted JSON label.** | |
| `listElementCandidate`, `pickByName`, `sliceGetters`, `sliceFields`, `indexerPrefix` = `"By"`, `repairIndexer`, `flipEscaped`, `enumParse`, `checkParseFunc`, `ParseFunc`, `didYouMean`, `SDKInfo`, `InfoFor`, `OperationPackages`, `recordPackage`, `recordTypePackage` | types + funcs + fields | `prune_list.go:84,94,113,132`; `prune_calls.go:261,279,311`; `prune_fields.go:456,497`; `verify.go:349`; `loader.go:225`; `model.go:40,52,130,135,325` | Element-accessor selection, kiota indexer repair, the `Escaped` spelling flip, enum parse companions, import bookkeeping, and the nearest-name suggestion. | Coined selection and repair vocabulary; `indexer` and `Escaped` are close to kiota's own naming. **`didYouMean`** is a colloquial coinage as a function name. (weak) | |

## 3.11 `internal/sdkgen`

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `Prenormalize` (+ the file `prenormalize.go`, ~29 uses) | exported func + file name + named stage | `prenormalize.go:54`; referenced `backend.go:13,42`, `generate.go:44,160`, `cli/sdk.go:23` | The five document rewrites every SDK generation performs before the generator runs. | **A named pipeline stage**, exported and given its own file. The glossary names `import`, `revise`, `generate`, `verify` — not this one. It also reaches a committed path (§1.3). | |
| `collapseAnonymousAllOfs`, `widenByteArrayCollections`, `reduceUnions`, `dropUnacceptableErrorContent`, and "pass" as the unit | funcs + named unit | `prenormalize.go:20,51,67-69,80` | The four named pre-normalization rewrites. | Each names an unapproved rewrite; **"pass"** is itself a coined unit. | |
| `Drift` + `DriftChanged` / `DriftMissing` / `DriftExtra` / `DriftHandEdited`, values `"changed"` / `"missing"` / `"extra"` / `"hand-edited"` | exported type + consts + printed strings | `verify.go:24,26,29,33,36,39`; printed `cli/sdk_verify.go:19`, `cli/provider.go:135` | The four kinds of difference `sdk verify` and `provider verify` report. | **"drift"** is glossary prose for the gate itself ("The drift gate"), but this closed four-value *kind set* is an unrecorded taxonomy printed to operators. `"hand-edited"` is the most coined. | |
| `scrubDatedHeaders`, `scrubKiotaLock`, "scrub" (~12 uses) | funcs + verb | `backend.go:14,42,120`; `kiota.go:204`; `generate.go:44` | Remove the nondeterminism a generator's output carries. | **"scrub"** is a coined verb for a named normalisation step. | |
| `staging`, `swap`, `parked`, `inventory` | locals + funcs | `generate.go:57,60,79,90,205,215,250` | The intermediate tree, the two renames that install it, and the walk that builds its manifest entries. | **"staging" / "swap" / "parked"** name a deliberate mechanism (§1.3); **"inventory"** is a noun-as-verb where the glossary's word is **manifest**. | |
| `gate` | func | `backend.go:95` | Refuses a mismatched tool version, naming both. | **"gate"** is glossary prose ("the drift gate", "the coverage gate"); as a function name for version pinning it is a third sense, beside `strategy.Gate`. (weak) | |
| `kiotaStub`, `openAPIGeneratorStub`, `installStub`, `stubArgs`, the file `stub_test.go` | consts + funcs + file name | `stub_test.go:18,52,80,101` | Fake generator binaries used to test invocation. | **"stub"** is glossary vocabulary for `quirkserver` only; here it names a different fixture category. (weak) | |
| `dropUnreferencedImports`, `withoutUnreferencedImports`, `sortLinesInFile`, `headerLines`, `datedComment`, `timestampValue`, `semverRE`, `prenormalizeSample`, `Options`, `Result`, `Report` | funcs, consts, vars, types | `kiota.go:129,150`; `openapigenerator.go:149`; `backend.go:68,100,105,110`; `prenormalize_test.go:9`; `generate.go:19,33`; `verify.go:52` | Normalisation helpers, header-scrub patterns, and the generic option/result types. | Mundane; `semverRE` is an abbreviation. (weak) | |

## 3.12 `internal/providergen`, `internal/manifest`, `internal/fixtures`, `internal/code`

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `PostcheckReport`, `Postcheck` (field, ×2), `postcheck`, `postcheckSteps`, `postcheckStepTimeout`, `postcheckOwned`, `recordPostcheckOwned`, the file `postcheck.go`, the flag `--postcheck`, and `OriginPostcheck` = `"postcheck"` in `manifest.json` | type, fields, funcs, consts, file name, flag, committed JSON value | `providergen/postcheck.go:14,19,26,41,56`; `providergen.go:49,75,164,170`; `manifest/manifest.go:51`; `cli/provider.go:125` | Running `go mod tidy`, `go build` and `go vet` in the generated tree after installing it. | **An invented stage name spread across nine identifiers, a file, a CLI flag, a CI step and a committed artifact's closed value set.** The approved stage vocabulary is `derivation \| binding \| emission` plus the named verbs. | |
| `OrphansOf`, `removeOrphans`, `orphans` | exported method + func + local | `manifest/manifest.go:7,209,215`; `providergen/providergen.go:123,451` | Files a previous run produced that this one no longer does. | **"orphan"** is a term of art here — the manifest's stated reason to exist — and is unapproved. It means a *different* thing in `audit/run` (§1.4) and is a fixture entity key below. Three meanings, one word. | |
| `RunSuffixExpr`, `RunSuffixBlock`, `WithRunSuffix` | exported consts + method | `fixtures/replay.go:26,27,39` | See §2.3 — `tfpfgen_run` reaches every generated acceptance fixture. | | |
| `overlayEntries`, `overlayOne`, `overlaid` | funcs | `fixtures/replay.go:125,183` | Replace derived fixture values with the ones a recorded create carried. | **"overlay"** is a coined verb for a domain operation. | |
| `nameBearing`, `restoredNames`, `restoreFirstSynthesised`, `keepOnePrefixed`, `anyPrefixed`, `synthesised`, and "the prefix guard" | funcs + field + named mechanism | `fixtures/replay.go:95,113`; `fixtures.go:80,81,131,142,156` | Whether an attribute's value names its object; the machinery that puts the invented prefixed name back after an overlay. | **"name-bearing"**, **"synthesised name"** and **"the prefix guard"** are coined concepts. Only `NamePrefix` is approved. | |
| `PinNumeric` / `pinBySDKType` | exported method + func | `fixtures/fixtures.go:215`; `emit/services.go:503` | Replaces a named string fixture value with digits where the SDK's Go type demands it. | **"pin"** as a coined verb, colliding with the document pin of `spec/store`. | |
| `variantModel`, `buildVariantModel`, `applyVariant`, `requiredForVariant`, `ownerOf` | type + funcs + fields | `fixtures/fixtures.go:54,173,240,243,252` | Which discriminator value owns and requires which attributes. | *variant attribute* is approved; **"variant model"** and **"owner of"** as named machinery are not. This is a *fourth* sense of "variant". (weak) | |
| `bindContext`, the file `bindcontext.go`, "bind harness" | func + file name + error string | `providergen/bindcontext.go:31,86` | Where `go/types` loads the SDK from, and the temporary module it may build to do so. | **"bind context"** and **"bind harness"** name concepts; the approved noun is **binding**. (weak) | |
| `offlineSignatures` | package var, **duplicated** | `providergen/postcheck.go:29`; `emit/compile_test.go:91` | Toolchain messages meaning "no network". | **"signature"** as a category name is coined, and the list is duplicated in two packages so it can drift. (weak) | |
| `RefusesWrites`, `EntriesNotOf`, `generation`, `staging`, `ValueForSDKType`, `FromAcceptedRequestBody`, `UnsupportedSummary`, `DefaultGoVersion`, `AcceptedRequestBodies`, `AuthGitHubApp`, `unifyByWire`, `Aliased` | exported methods, types, funcs, consts | `manifest/manifest.go:124,139`; `providergen/providergen.go:95,229`; `fixtures/fixtures.go:401,498`; `replay.go:72`; `emit/unsupported.go:208`; `emit/provider_core.go:23,34,91`; `code/import.go:45` | Assorted API surface. | Mostly derivative of approved terms. `AuthGitHubApp` brings the unapproved `app_id` / `app_private_key` with it (§2.2). (weak) | |
| `Manifest`, `authored`, `code.Import{Alias,Path}`, `SchemaDefinition`, `CustomValidator`, `CustomPlanModifier`, `Fixture`, `Entries`, `Entry`, `Omissions`, `Omission`, `Form`, `ConfigMinimal`, `ConfigMaximal`, `ResponseMinimal`, `ResponseMaximal`, `NamePrefix` | exported vocabulary | `manifest`, `code`, `fixtures` | — | **Approved.** The whole of `internal/code` is clean. | — |

### Test-only vocabulary in these packages

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| `curated` — the file `curated_test.go`, plus `curatedDialects`, `curatedDir`, `curatedRepo`, and "the curated fixture" in `README.md:104` | file name + package vars + funcs + prose | `providergen/curated_test.go:14,20,26,27,30,40,42` | The committed fictional OpenAPI document plus one hand-written stub SDK per backend, driven through the real verbs. | A coined name for a whole test artifact. The retirement of **corpus** shows this naming space is owner-controlled. The helper also uses **`dialect`** for **backend** 17 times. | |
| The fixture's invented entity nouns: `beacon`, `gizmo`, `blob`, `orphan`, `http_server`, `tag`, `alert_rule`, `audit_event`, `license` | entity keys + generated Go types | `testdata/curated/**`; `providergen/curated_test.go:150-156` | The entities the fictional document declares. | `beacon` and `gizmo` are colourful coinages; **`orphan` re-uses the manifest's own domain term as an entity key**, giving two meanings inside one test suite. | |
| `fictional` — `fictionalModel`, `fictionalBindings`, `fictionalProviderCore`, `renderFictional`, `writeFictionalKiotaModule` | test helpers, and printed in failure messages | `emit/services_fixture_test.go:31,260,368`; `services_test.go:13`; `services_compile_test.go:115`; printed at `render_listresource_test.go:169`, `render_datasource_test.go:21` | The in-memory model, bindings and context the emit tests render from. | "the fictional tree" is used as if it were vocabulary. | |
| `oag` — the file `services_oag_test.go`, plus `oagAccess`, `oagModel`, `oagBindings`, `oagProviderCore`; and `kAccess` | file name + helpers | `emit/services_oag_test.go:17,24,107,237`; `services_fixture_test.go:189` | The openapi-generator variant of the emit fixture set, and the kiota one. | **`oag` is an unapproved abbreviation of the approved backend name `openapi-generator`**, and the two sides are named inconsistently (`services_fixture_test.go` + `k*` versus `services_oag_test.go` + `oag*`). | |
| `installStubSDK`, `installEntityStubSDK`, `scopedCompanion`, `scopedListResource`, `registryFixture` | test helpers | `emit/compile_test.go:71`; `services_compile_test.go:167`; `render_datasource_test.go:15`; `render_listresource_test.go:14`; `registry_test.go:11` | Stub SDKs, parent-scoped fixtures, a sample registry file. | Mild coinages; `scopedCompanion` carries the unapproved "companion". (weak) | |

## 3.13 `internal/cli`, `internal/quirkserver`, `internal/config`, `cmd/tfpfgen`

`internal/cli`'s findings are in §1.7 — its vocabulary is almost entirely
operator-facing. What remains here is internal plumbing.

| Term | Kind | Location | What it is | Why unapproved | Ruling |
|---|---|---|---|---|---|
| "shape resource" / the `shape*` family — the files `shapes.go`, `shape_assignment.go`, `shape_monitor.go`, `shape_stream.go`, plus `initShapes`, `routeShape`, `shapeInvalid`, `shapeCreate`, `shapeList`, `shapeRead`, `shapeUpdate`, `shapeDelete` | named concept + 4 file names + 9 funcs, ~100 uses | `quirkserver/shapes.go:132,146,207,212,225,229,239,262`; `quirkserver.go:91,179`; `standalone.go:43` | Whole small always-on API surfaces (monitor, assignment, agent, stream) the audit is asserted against. | **The largest single unrecorded coinage in the repo by volume.** The glossary uses "shape" only inside `listResponseShape`. | |
| `exhibit` — the files `exhibit_read_test.go`, `exhibit_write_test.go`, `exhibit_shapes_test.go`, plus `readExhibits`, `writeExhibits`, `EachQuirkIsExhibited` | file-name prefix + package vars + a contract named in the package doc | `quirkserver/exhibit_read_test.go:12`; `exhibit_write_test.go:13`; `quirkserver_test.go:82,85-93`; `quirkserver.go:20` | Files holding one demonstration per quirk, and the driver asserting every quirk has one. | A coined noun for a whole category of test artefact, promoted to three on-disk file names and named as a contract in the package doc. | |
| The `Quirks` field taxonomy — 38 exported fields: `SilentlyDiscards`, `ImmutableAfterCreate`, `RequiresExtraFieldOnUpdate`, `ConstantDefaults`, `DerivedDefaults`, `CounterDefault`, `RequiredButUndeclared`, `ConditionallyRequired`, `DiscardsWhen`, `WriteSideEffects`, `NormalisesCase`, `TrimsWhitespace`, `SortsLists`, `ExpansionGated`, `SilentlyDiscardsOnUpdate`, `PutClearsOmitted`, `EventuallyConsistentReads`, `NamesRefusedFieldInProse`, `ErrorEnvelope`, `ClosedEnum`, `RejectsValueUnless`, `RejectsDocumentedValue`, `DeleteFails`, `DeleteFlakyEvery`, `RateLimitHeaders`, `RateLimit`, `VolatileFields`, `IgnoresUnknownQueryParams`, `TypedQueryParams`, `BasePath`, `NotFoundStatus`, `Forces`, `NullsInWriteResponse`, `SuppressWhenSibling`, `UpdateDefaults` | struct fields | `quirkserver/quirks.go:22-117` | 38 named API misbehaviours, one switch each on the fake server. | **An entire coined taxonomy.** Only `quirkserver` itself is approved; not one behaviour name is recorded. Several shadow observation kinds under different words (`ImmutableAfterCreate` vs `immutable`, `EventuallyConsistentReads` vs `readAfterWrite`). | |
| `seededAgents` / `Seed` / "seeds" | package var + **exported method** | `quirkserver/shapes.go:120,129,137`; `quirkserver.go:133` | The fixed agent set the server serves, and the method inserting an object directly. | **"seed" is a metaphor coinage on exported API surface.** Also emitted into providers (§2.3). | |
| "oracle" | prose in a doc comment | `quirkserver/shapes.go:14` | Describes the monitor's spec as "an honest-but-partial oracle". | A metaphor characterising a fixture's role. | |
| "grammar" | named contract in doc comments + a test name | `quirkserver/shapes.go:26`; `shape_monitor.go:44`; `shape_stream.go:7,37`; `exhibit_shapes_test.go:25` (`Monitor_VariantGrammar`) | The fixed sentence form every 400 refusal takes, so the executor's parser can extract the field name. | A named contract between the server and the executor — and the same word names the executor's own subsystem (§3.2). | |
| `Envelope` + `EnvelopeProblem` / `EnvelopeOAuth` / `EnvelopeLegacy` / `EnvelopeEmpty` (`"problem"`, `"oauth"`, `"legacy"`, `"empty"`), and `envelopeKey` = `"things"` | named string kind + constants + const | `quirkserver/envelope.go:15,19,21-29`; `quirkserver.go:73` | Which shape an error body takes. | A named kind with a closed value set. **"envelope" for an error-body form is a different concept from the glossary's list-response envelope** — a second sense in one repo. | |
| `Conditional` (+ `WhenField`, `WhenValue`, `Then`) | type + fields | `quirkserver/quirks.go:8,11,12,14` | A requirement that depends on another field's value. | **`validWhen` is the approved word for exactly this concept.** | |
| `project` | method | `quirkserver/handlers.go:253` | Renders a stored object the way the API would return it. | Coined verb ("projection") for a named rendering step. | |
| `monitorKinds`, `monitorVariantFields`, `monitorRequiredForKind`, `streamFormats`, `streamModes`, `streamModeForFormat`, `StandaloneQuirks` / "profile", `collection` / `newCollection`, `validator` (func type), `specDoc`, `knownQueryParams`, `refusedFieldDetail`, `missingRequired`, `rejectedEnumValue`, `badQueryParam`, `badTypedParam`, `applyDefaults`, `applySideEffects`, `normalise` | vars + funcs + types | `shape_monitor.go:10,16,89`; `shape_stream.go:27,28,34`; `standalone.go:14,63`; `serve.go:41`; `shapes.go:45,51,204`; `behaviour.go:11,38,47,80,87,104,139,145,152` | Fixture vocabulary, refusal renderers and the quirk appliers. | Carry the unapproved taxonomy. **`knownQueryParams` uses the retired `Params`**; **`specDoc`** abbreviates twice and misuses "spec". **"profile"** for a quirk set is unrecorded. (weak) | |
| `usageError`, `usagef`, `exactArgs`, `oneArg`, `providerFlags`, `goOnPath`, `describeVersions`, `describeWritten`, `describeNote`, `skippedListLimit`, `auditRunsDir` | types + funcs + consts | `cli/cli.go:25,30,95`; `spec.go:88,100`; `provider.go:29,178`; `spec_revise.go:178,182`; `audit.go:36,302` | Operator spelling-mistake handling and message rendering. | Mundane; `usageError` names an exit-code category. (weak) | |
| "verdict", "dispatcher" | prose in the package doc | `cmd/tfpfgen/main.go:3` | The exit code `cli.Run` returns, and the `cli` package's command tree. | **"verdict"** is a metaphor naming the exit-code contract; **"dispatcher"** is an unapproved name for the CLI root. | |
| `leaf`, `section`, `collectLeaves`, `sections`, `descriptions`, `allowedValues`, `autoAcceptKinds`, `SchemaVersion`, `RequiredSecrets`, `MissingSecrets`, `problems()`, `describeDecodeError`, `nearestKey`, `knownKeys` | unexported types + funcs | `config/reference.go:21,46,55,62,70,87`; `validate.go:126`; `config.go:19,137,150,171,185`; `secrets.go:20,38` | The reflection machinery generating `docs/config.md`, and validation. | Documentation plumbing, no domain vocabulary. `autoAcceptKinds` inherits the coined "compilable kinds" (§3.6). (weak) | |
| `SecretToken`…`SecretAppPrivateKey`, `BackendKiota`, `BackendOpenAPIGenerator`, `version.value` | consts + var | `config/secrets.go:8-14`; `config.go:84-85`; `version/version.go:9` | The seven `TFPFGEN_AUTH_*` names, the two backends, the stamped version string. | **Not violations.** `internal/version` is clean. | — |

---

# Section 4 — Retired spellings and inconsistencies

These are not open questions about taste. Each one contradicts a decision the
glossary already records, or contradicts itself.

## 4.1 Retired terms still in the tree

| Term | Location | The glossary says | Ruling |
|---|---|---|---|
| `refineReserve` | `audit/strategy/program.go:63,68,101` | *requestAdjustment* is "the successor to the retired Wave 2 name **refinement**; that spelling no longer appears." It does. The doc comments at `audit/run/steps_create.go:15,110` also say "refining the body" and "refined". | |
| `Op` (exported type) | `specmodel/classify.go:36`; referenced `classify.go:59-63,71,363,370`, `audit/plan/derive.go:224`, `audit/strategy/strategy.go:362`, `spec/revise/locate.go:77,104,124` | "`Operation`/`Operations` replace the earlier `Op`/`Ops`." Retired in the IR; still live in `specmodel` and named by four other packages. | |
| `flatOp` | `spec/correction/correction.go:152,160` | Same. | |
| `op` (parameter name) | `emit/render_resource.go:563-565`, `emit/render_listresource.go:99`; and in the **emitted** `errors.go.tmpl:184,218` | Same — and one instance ships into every generated provider. | |
| `json:"element_kind"` | `sdkbind/model.go:288` | "**ElementType** … Replaces `ElementKind`, which is retired." The Go field was renamed; **the JSON tag was not**, so the retired word is still persisted to disk. | |
| `CallParam`, `Params` + `json:"params"`, `callParams`, `paramName`, `"param_"`, `settleParamTypes` | `sdkbind/model.go:212,224,226,230`; `binder.go`; `prune_calls.go:242` | "`Parameter`/`PathParameters` replace `Param`/`PathParams`." Includes a JSON tag and a string emitted into generated code. | |
| `pathParams`, `itemKeyParam`, `templateParam`, `keyParam` | `ir/derive.go:240,270-279,413-428,480,524,529,553`; `ir/attribute_addressing.go:11,19,168,169,182` | Same — inside the very package the rule was written for. | |
| `paramFailure`, `paramField`, `paramNode`, `paramValue`, `paramSegmentIndex`, `actionParamNodes`, `integerParsedParams`, `UpdateParamCopies` | `emit/render_mapping.go:359,462,472,552,670`; `render_resource.go:450,610`; `render_action.go:192` | Same. `UpdateParamCopies` is in the template contract. | |
| `trailingParam`, `knownQueryParams`, `params` (test local) | `specmodel/classify.go:389`; `quirkserver/behaviour.go:139`; `ir/attributes_test.go:597-598` | Same. | |

**Confirmed absent:** `cassette`, `blueprint`, `doctor`, `corpus`,
`TypeKind`, `APIVersionDir`, `Presence*`, `optional-computed`,
`TF_<PROVIDER>_*`. Those retirements held.

## 4.2 One approved term, four spellings

**list resource** is an approved term. In code it is spelled four ways, and one
of them reaches `unsupported.json` in every provider repo.

| Spelling | Location | Used for |
|---|---|---|
| `"list-resource"` | `specmodel/classify.go:24` (`KindListResource`) | The classification kind, and `docs/mapping.md`'s kind name. |
| `"list_resource"` | `sdkbind/prune.go:310`, `sdkbind/verify.go:149`, `sdkbind/model.go:340`, `emit/services.go:223`, `emit/unsupported.go:197` | The binding-removal kind, and the `path` prefix in `unsupported.json`. |
| `"list_resources"` | `emit/registry.go:38,47`; `providergen/providergen.go:399` | The registry slot — **this one is glossary-approved.** |
| `"list-resources"` | `emit/services.go:196` (`kindListResources`) | The emitted directory `internal/services/list-resources/`. |

A fifth, `list-resource`, is the approved service-template directory name.

**Ruling:** ______________

## 4.3 `materialize` / `materialise`

The same coined verb, spelled both ways, for the act the glossary calls
**revise**:

- `materialize` — `cli/spec_revise.go:29` (cobra `Short`), `:74` (flag help), `spec/revise/revise.go:6,9,52,58` (the exported func), `docs/contract.md:29`
- `materialise` — `cli/audit.go:268`, `cli/audit_test.go:175`

**Ruling:** ______________

## 4.4 Duplicated declarations of one concept

| Concept | Declared at | Note | Ruling |
|---|---|---|---|
| `Provenance` (+ its three values) | `audit/observe/observe.go:232-234` **and** `audit/strategy/strategy.go:44-55` | The concept is approved; two identical types are not. | |
| `Outcome` | `audit/observe/observe.go:262-273` (approved: 4 values) **and** `spec/store/store.go:62-72` (`Pinned`/`Unchanged`/`Repinned`) | A second, unrelated type wearing an approved name. | |
| `Registry` / `Register` | `emit/registry.go:12` (approved) **and** the emitted `mocks/mocks.go.tmpl:81,91` | Two unrelated systems, four shared words — and the second ships into every provider. | |
| `Exclusion` | `specmodel/classify.go:86` **and** `ir/model.go:47` | | |
| `deriver` | `ir/derive.go:202` **and** `audit/plan/derive.go:136` | | |
| `primaryGate` | `audit/strategy/gates.go:47` **and** `audit/run/cycle.go:168` | | |
| `"tfpfgen-undocumented"` / `"tfpfgen_unknown_field"` | `audit/plan/steps.go:12,16` **and** `audit/run/strategize.go:36-37` | Wire sentinels declared twice; they can drift apart silently. | |
| `offlineSignatures` | `providergen/postcheck.go:29` **and** `emit/compile_test.go:91` | | |
| `unitEndpoint` | `mocks/mocks.go.tmpl:22` **and** `emit/render_resource.go:572` | | |

## 4.5 One word, several unrelated meanings

| Word | The meanings now in the tree |
|---|---|
| **orphan** | A live object cleanup could not delete (`audit/run`); a generated file no run produces any more (`manifest`); an entity key in the curated fixture. |
| **envelope** | A list response's wrapper (approved, `listResponseShape`); an error-body shape (`quirkserver/envelope.go`); a binding-model field (`sdkbind.EnvelopeKey`); a kiota error interface family. |
| **variant** | A `oneOf`/`anyOf` branch attribute (approved); one gate value's body shape (`strategy.Variant`); a second value for an update (`plan/synth.go:208`); the fixture ownership picture (`fixtures.variantModel`). |
| **registry** | The provider-core registration slots (approved); the audit's entity→live-object map (`run.go:307`); the emitted mock registrar table. |
| **gate** | The discriminator field (`strategy.Gate`); the drift/coverage gates (glossary prose); the SDK tool-version check (`sdkgen/backend.go:95`). |
| **preflight** | `config validate`, the offline preflight (glossary); the audit's foreign-object check (`audit/run`); the release job (`60-release.yml`). |
| **grammar** | The quirkserver's refusal sentence form; the executor's refusal parser. |
| **plan** | Terraform's plan; the audit plan (approved); `emit.callPlan`; `emit.writePlan`. |
| **site** | `ir.site` (a property seen from both sides); `revise.propSite` (a place in the YAML tree). |
| **stub** | `quirkserver` (glossary); `sdkgen`'s fake generator binaries; the curated fixture's hand-written SDKs. |

**Ruling on each:** ______________

---

# Section 5 — `internal/intermediate_representation`: the fully-worded rule

The glossary states: *"Every identifier in the package is fully worded — no
abbreviated type, field, function, parameter or local."* It is broadly
violated. Grouped, because the ruling is likely one decision rather than a
hundred.

## 5.1 Production code

| Identifier | Location | What it is |
|---|---|---|
| `dst` | `attributes.go:169,170,172` | The destination bound pointer. |
| `seenProp` | `attributes.go:73,136,137` | Property names already folded in. |
| `schemaExt` | `attributes.go:303,304,307` | The merged extensions of both schema sides. |
| `out` | `attribute_types.go:216,228`; `attributes.go:316,330,350,370,380,394`; `derive.go:177,534` | The value under construction. Pervasive. |
| `prev` | `naming.go:249,250,253` | The preceding rune. |
| `max` | `derive.go:608,613,614,617` | The largest eventual-consistency duration. Also shadows the builtin. |
| `ds`, `lr` | `derive.go:113-119` | The derived datasource; the derived list resource. |
| `pi`, `oi` | `derive.go:210-212` | Path index and operation index. |
| `s`, `b`, `r`, `a`, `n`, `i`, `j` | `naming.go:148,176,180,208-216,248-253`; `attributes.go:197,320,335-374`; `attribute_types.go:96-101`; `derive.go:92-100,167-198,290,574,596-597` | Strings, builders, runes, acronym hits, counts, loop indices. |
| `wire`, `snake` | `attributes.go:431`; `naming.go:160,167` | A wire property name; a snake_case key. (weak) |
| `flat`, `flatPrimary`, `flatCreate`, `flatRead`, `flatUpdate`, `flatValue`, `flatItems` | `attributes.go:12,238,239,435,436`; `attribute_types.go:15,58,108,119,141,146` | The folded schema views — also a coined domain term (§3.8). |
| `pathParams`, `keyParam`, `itemKeyParam`, `templateParam` | see §4.1 | Retired *and* abbreviated. |

## 5.2 Tests

Roughly 120 further single- and double-letter identifiers, plus:

| Identifier | Location | What it is |
|---|---|---|
| `spec` (as a variable) | `attributes_test.go:245,422,484,543`; `constraints_test.go:166,233,296`; `derive_test.go:149,201`, throughout | The OpenAPI document text under test. **The glossary reserves *spec* for the one document a provider is generated from**, so this is a misuse as well as a shortening. |
| `doc` | `intermediate_representation_test.go:12,14,31,33`; `derive_test.go:15,19,24`; `attributes_test.go:304-309` | The loaded document. |
| `cfg` | `intermediate_representation_test.go:31,33` | The loaded config. |
| `tc` | `naming_test.go:107` | The table-driven test case. |
| `str` | `attributes_test.go:650,736` | A string-schema constructor. |
| `params` | `attributes_test.go:597,598` | Path parameters — retired spelling too. |
| `r`, `m`, `d`, `ds`, `lr`, `e`, `k`, `u`, `v`, `a`, `i`, `s` | `derive_test.go:149-578`; `derive_list_resource_test.go:59,90,117`; `intermediate_representation_test.go:14,33,74`; `attributes_test.go`, `constraints_test.go` throughout | Resources, models, documents, datasources, list resources, entries, keys, loop values. |

**Ruling — production code:** ______________

**Ruling — tests:** ______________

**Ruling — does the fully-worded rule extend to `_test.go` files at all?** ______________

---

# Section 6 — Comment-style violations

Per `docs/comment-style.md`, a comment answers **what** and **why** and stops.
Each row below quotes the offending text and gives a transform that keeps the
fact and drops the story, per that document's *"Transform, don't delete"* rule.
Where the rule is simply "delete", it says so.

## 6.1 Run-specific counts and measurements

`docs/emittance_tracker.md` is the only place a count of what the toolkit emits
or refuses may live. `docs/comment-style.md` additionally forbids "counts and
measurements from a particular run" outright: a measurement belongs in a pull
request body, where it is dated.

| Location | The text | Transform | Ruling |
|---|---|---|---|
| `emit/unsupported_test.go:230` | "Reporting those as refusals is how this **file came to claim 207 losses on one pilot** that were not losses." | Keep the standing property, drop the count and the history: "Reporting those as refusals would be wrong: the attribute reaches the schema regardless." | |
| `cli/audit_test.go:339` | "covers the reporting gap **a live GitHub run exposed: the plan skipped 56 of 61 entities** and the run printed only the number, so nothing said the audit had barely touched the API." | "…covers the case where the plan skips most entities: printing only the number leaves nothing saying the audit barely touched the API." | |
| `audit/strategy/program.go:60` | "the multi-variant monitor's 33-step program **spends ~92 requests live, which the old base(10)+fields×variants=38 could not cover**, so it exhausted before completing." | Drop the count and the superseded formula; keep the standing rule: "A budget too small to let a resource's whole program complete is the exhaustion this weighting exists to prevent." (that sentence is already there at `:57-58`) | |
| `audit/strategy/strategy_test.go:639` | "compiles a **~30-step program; the old base+fields×variants**…" | Same — drop the count and the superseded formula. | |
| `spec/revise/compile_test.go:701` | "is the defect **the first live run surfaced: five pull requests**, each asking a human to approve an eventual-consistency annotation of '0s'." | Keep the rule, drop the run: "A zero lag means the read never lagged the write, so there is nothing to declare and nothing to decide." (already the next sentence) | |
| `emit/services_errors_test.go:665` | "Real documents leave most properties bare — **one pilot annotates 12%**." | "Real documents leave most properties bare." | |
| `audit/run/run.go:215` | "an entity that came back thin **after fifty refusals** was not measuring a quiet API" | "an entity that came back thin under sustained rate-limiting was not measuring a quiet API" | |
| `providergen/curated_test.go:78,93` | "known shape: **three** resources, a companion datasource for each…"; "the **three** resources are all enumerable" | Fixture-shape counts, arguably a property of the committed fixture rather than a run measurement. (weak) | |

## 6.2 Post-mortem narrative and superseded behaviour

`docs/comment-style.md` drops: "used to", "we tried", "turned out", "which is
how X became Y". State only what is true now.

| Location | The text | Transform | Ruling |
|---|---|---|---|
| `emit/services.go:43-46` | "**Aborting the whole run instead was the old behaviour, and one entity took every other entity with it**: an item path carrying a second path parameter no attribute answers is enough, and that single shape emitted nothing at all for the provider." | **This is verbatim the passage `CLAUDE.md` quotes as its own "No" example**, and `CLAUDE.md` supplies the approved rewrite: "Refuses one entity rather than the run: an unrenderable shape is a fact about that entity, and the rest still generate." | |
| `providergen/internals_test.go:201` | "the resource beside it still emits; **it used to fail the whole run**." | Delete the clause. | |
| `sdkbind/prune_edge_test.go:184` | "the ThousandEyes Tagsable shape (GET /gizmos → {"gizmos":[...]}) that **used to be pruned** for carrying 'no single way to reach its elements'." | "…a wrapped-list collection whose envelope key names `GetGizmos` directly, so the element is reached through it." | |
| `sdkbind/prune_edge_test.go:382` | "SDK shapes the catalog can carry but **the pruner used to refuse** settle to…" | Same treatment. | |
| `emit/schema_type_test.go:24` | "These are the names **the six switches this record replaced used to answer**, so a change to any of them is a change to generated code." | "These are the names generated code depends on, so a change to any of them is a change to generated code and must be deliberate." | |
| `ir/derive_list_resource_test.go:54` | "spells that key **after the thing it identifies used to publish no identity**" | Rewrite as the standing property. | |
| `spec/correction/correction_test.go:528` | "the shape that **silently broke a 7 MB document in v1**." | Drop the version, the size and the story; keep the shape's description. | |
| `sdkgen/prenormalize_test.go:184` / `prenormalize.go:205` | "inline on a response (**the shape that broke a real build**)"; "because **the shape that broke the build** was inline" | "…because the shape this pass exists for is declared inline rather than as a component schema." | |

## 6.3 Roadmap and project commentary

`docs/comment-style.md` drops "roadmap and project commentary in API docs —
which tranche a refactor lands in is not a property of the function."

| Location | The text | Ruling |
|---|---|---|
| `audit/strategy/strategy.go:23-27` | The whole "Deferred naming … an owner decision settled in **Wave 3** … likewise provisional" block. **Delete once §3.1 is ruled on** — that is the point of §3.1. | |
| `audit/strategy/strategy.go:20,43` | "the live API, exercised in **a later wave**, is the only thing that confirms"; "derived is reserved for the triangulating inference of **a later wave**" | |
| `audit/strategy/strategy.go:58-60` | "These are **WORKING IDENTIFIERS**: the final observation-kind names are an owner decision **settled in Wave 3**…" | |
| `audit/strategy/strategy.go:191-193` | "(**Working name** — see the package note on deferred naming; the retired term "probe" is deliberately avoided.)" — and the "probe" claim is wrong (§7.2). | |
| `audit/observe/observe.go:162,171,181,189` | "Emitted as `x-tfpfgen-valid-configuration` **by a later wave**." ×4 — the emission now exists, so these are also stale. | |
| `quirkserver/exhibit_shapes_test.go:14` | "a fixture whose phrasing drifted would make the **Wave 2/3** tests pass for the wrong reason" | |
| `providergen/curated_test.go:18` | "The curated fixture is **Phase 1's exit gate**" | |

## 6.4 A named defect from project history

| Location | The text | Ruling |
|---|---|---|
| `emit/render_validators.go:315` | "It closes **the ListWrap defect** — the generated mock no longer assumes every API wraps its collection under…" | |
| `emit/services_test.go:281` | "proves **the ListWrap defect**…" | |
| `docs/rehearsal.md:137` | The section heading "## **ListWrap** closed" | |

`docs/comment-style.md` forbids naming which pull request changed something and
how a bug was found. A codename for a past defect is the same class of thing.
The standing property — "the generated mock reads the observed envelope rather
than assuming one" — is what belongs.

## 6.5 Vendor and pilot names in comments

`CLAUDE.md`: "No provider-specific values in code or defaults… CI greps for
this." `scripts/repo_hygiene_gate.sh` checks non-test source only, so these are
all in tests and pass the gate — but they are still run anecdotes.

| Location | The text | Ruling |
|---|---|---|
| `audit/run/probe_fidelity_test.go:9-14` | "**The ThousandEyes proof run** recorded `interval` as server-forced on three resources. It was not. The audit sent the string "120" into an integer enum…" — a war story; the standing property is the type-coercion rule it pins. | |
| `sdkbind/prune_edge_test.go:183` | "the **ThousandEyes** Tagsable shape" | |
| `spec/correction/correction_test.go:80` | "The exact **ThousandEyes** shape: 001 (sorting first) adds a property's default…" | |
| `spec/revise/ordering_test.go:150` | "`tagInfoSpec` is the **ThousandEyes** tag surface in miniature" | |
| `emit/services_errors_test.go:665` | "one **pilot** annotates 12%" (also §6.1) | |
| `emit/unsupported_test.go:230` | "207 losses on **one pilot**" (also §6.1) | |
| `README.md:122` | "Three documents — **Jamf Pro, ThousandEyes and GitHub**" — prose in a readme, arguably legitimate. (weak) | |

`internal/vendor_openapi_specs/vendor_openapi_specs.go:19` names ThousandEyes
correctly: that package is the one the hygiene gate exempts, and the glossary
requires the version be recorded there. **Not a violation.**

## 6.6 Editorial asides

`docs/comment-style.md` drops "worth having anyway", "which is the point", "the
rest is left alone deliberately".

| Location | The text | Ruling |
|---|---|---|
| `audit/infer/evidence.go:121` | "a fact about the response, not the document, **which is the point**" | |
| `quirkserver/shapes.go:32` | "the stream's value-conditional refusal deliberately does not, **which is the whole**…" | |
| `quirkserver/quirks.go:187` | "**deliberately.**" as a standalone sentence | |
| `audit/strategy/prose.go:10` / `program.go:55` | "It is **deliberately** small"; "The reserves are **deliberately** generous" | |

Note: most of the ~40 other uses of "deliberately" in the repo are legitimate —
they introduce a genuine *why* ("Alias nodes are deliberately not dereferenced:
correction application does…"). Only the ones above are bare editorialising.
No blanket rule is needed.

## 6.7 A systemic gap: no section dividers anywhere

`docs/comment-style.md` specifies section dividers — `// ── `, a noun-phrase
title, then `─` padding to exactly 80 runes — and says to use them "in files
over roughly 250 lines, to group related declarations".

**There is not one section divider in the repository.** There are ~50 non-test
files over 250 lines, including:

| Lines | File |
|---|---|
| 722 | `emit/render_mapping.go` |
| 715 | `spec/revise/compile.go` |
| 704 | `emit/render_resource.go` |
| 696 | `specmodel/load.go` |
| 647 | `emit/render_datasource.go` |
| 618 | `ir/derive.go` |
| 613 | `emit/services.go` |
| 611 | `audit/run/run.go` |
| 606 | `specmodel/extensions.go` |
| 596 | `fixtures/fixtures.go`, `audit/observe/observe.go` |
| 594 | `audit/run/strategize.go` |

(No divider that *does* exist is malformed — the check for the 80-rune rule
returned nothing, because there are none to check.)

**Ruling — add dividers, or drop the rule from `docs/comment-style.md`?** ______________

---

# Section 7 — Glossary gaps and contradictions

Not all of the above is a coding defect. Some of it is the glossary being
behind the code. These are the places where the *document* may be what needs
changing.

## 7.1 Terms the glossary uses in prose but never lists

Each of these appears inside a glossary entry's description, so it reads as
sanctioned — but it is not in the table, and code has built type systems on
several of them.

| Word | Where the glossary uses it | What code built on it | Ruling |
|---|---|---|---|
| **gate** | `validConfiguration`: "the discriminator (gate) field"; `validWhen`: "a sibling gate field equals a specific value" | `strategy.Gate`, `GateKind` + 3 values, `gateField`/`gateValue` JSON across three packages, `detectGates`, `primaryGate` (×2), `typedGate`, `infer.gate` | |
| **claim** | `outcome`: "How far the audit got with one claim"; most observation-kind entries: "The observation kind **claiming** that…" | `run.claim`, `stepClaims`, `emitHaltedClaims` | |
| **edge** | `validWhen`: "the core conditional edge"; `triangulating inference`: "asserts a conditional edge" | `infer.EdgeKinds` (exported), `edge`, `edgeAttr`, `validWhenEdges`, `requiredEdges`, `ir.attributeEdges`, and `edgesConfirmed`/`edgesInconclusive` in `audit/summary.json` | |
| **probe** | `rejectsUnknownFields`: "the made-up-field **probe**" | `audit/run/probe_fidelity_test.go` — see §7.2 | |
| **companion** | not in the glossary at all, but used throughout `docs/mapping.md` | `emit.companionDatasource` and three helpers, plus two user-facing error strings | |
| **role slot** | `action`: "the **role slots** describe HTTP position" | `specmodel.presentRoles`, and `Role` as a JSON field in two packages | |
| **refusal** | used in `unsupported.json`'s entry and throughout | `ir.refuse`, `emit.unrenderableError`, `run.refusalAction`, `classifyRefusal`, `combinedRefusals` | |
| **excerpt** | `observation`: "a redacted request/response excerpt as proof" | `observe.Excerpt` (fine) — but also `RequestFragment`/`ResponseFragment`, a second unit inside it (§1.4) | |

## 7.2 A comment that contradicts the glossary

`audit/strategy/strategy.go:191-193` says:

> *(Working name — see the package note on deferred naming; **the retired term
> "probe" is deliberately avoided**.)*

`probe` is **not** in the glossary's retired list. The retired terms are
`cassette`, `blueprint`, `doctor`, plus `corpus`, `refinement`, `Op`/`Ops`,
`TypeKind`, `Param`/`PathParams`, `APIVersionDir`, `Presence*`,
`optional-computed`, `ElementKind`, and the two retired spellings for
correction branches and provider env vars. The glossary *uses* "probe" in
`rejectsUnknownFields`: "the made-up-field probe (`undeclaredSpecField`)". And
`internal/audit/run/probe_fidelity_test.go` is named for it.

Either the comment is wrong, or the glossary and a file name are. **Ruling:**
______________

## 7.3 21 observation kinds in code, 7 in the glossary

`audit/observe/observe.go` declares 21 `Kind` values. The glossary's table
names seven: `undocumentedFieldInSpec`, `validConfiguration`, `validWhen`,
`dependsOn`, `mutuallyExclusive`, `listResponseShape`, `identifierProperty`.
It also describes `undocumentedFieldInSpec` as "the fifteenth", which implies
fourteen prior kinds were settled at some point.

The fourteen unrecorded: `writable`, `immutable`, `requiredByAPI`,
`requiredWhen`, `serverDefault`, `derivedDefault`, `normalisation`,
`ignoredOnUpdate`, `serverForced`, `volatile`, `values`, `updateStyle`,
`deleteNotFoundOK`, `readAfterWrite`.

This is most likely a **glossary gap**, not a naming defect. But three of them
disagree with their own extension keys (§1.1), which is a real defect either
way:

| Observation kind | Extension key it writes |
|---|---|
| `readAfterWrite` | `x-tfpfgen-eventual-consistency` |
| `values` | `x-tfpfgen-values-open` |
| `ignoredOnUpdate` | `x-tfpfgen-silently-ignored-on-update` |
| `immutable` | `x-tfpfgen-create-only` |

**Ruling — record the fourteen as-is, or rename?** ______________

**Ruling — reconcile the four kind/key mismatches?** ______________

## 7.4 A whole state the glossary does not record

`docs/contract.md:95` states: *"A proposal can be withdrawn, which is not a
rejection."* The glossary's **correction** entry records `proposed` /
accepted / `rejected` only. The withdrawal state has a CI job
(`withdraw-corrections`), ~18 uses across two workflows, and a documented
contract — but no glossary entry.

**Ruling:** ______________

## 7.5 Names the glossary delegates

The glossary says *"Config file: `tfpfgen.yaml` (schema owned by
`internal/config`)"*, and `docs/config.md` is generated from the structs with a
test that fails if a key lacks a description or a description lacks a key. So
the config keys in §1.6 are arguably already governed, just not by the
glossary.

**Ruling — does that delegation stand, or should the key names be listed?**
______________

---

# Part III — The decision list

Part I decides most of Part II mechanically. This is what that comes to, and
what is left for you.

## III.0 — An amendment R4 needs before it can be applied

R4 as drafted has two exceptions: the acronym table, and names an external
contract fixes. Applying it to the tree surfaces a third that it must have:

**Established Go idiom where expansion fights the language.**

| Name | Uses (non-test) | Why it cannot be expanded |
|---|---|---|
| `ctx` | 207 | `context context.Context` shadows the package name in the body. Every Go codebase, including the standard library, writes `ctx`. |
| `err` | pervasive | Same problem against the `errors` package, and `if err != nil` is the most-read line in Go. |
| `ok` | pervasive | The comma-ok idiom is a language construct, not a name. |

I propose adding exactly these three to R4 and nothing else. In particular
`req`, `resp`, `cfg`, `attr`, `op`, `spec`, `doc`, `ent`, `sum`, `pkg`, `ptr`,
`val`, `idx`, `src`, `dst`, `ext`, `param`, `props`, `disc`, `hyps`, `desc`,
`elem`, `str`, `buf`, `cur`, `prev` are **not** idiom — they are shortenings,
and they are exactly what makes the tree hard to read.

**Ruling — add these three exceptions to R4?** ______________

## III.1 — Decided by R4 (abbreviations)

**Scale.** ~2,200 abbreviation occurrences in non-test Go source, plus
single-letter locals, plus roughly 120 more in
`internal/intermediate_representation`'s tests alone. This is the largest
tranche and the one a reader feels most.

Proposed expansions, applied uniformly. Approve the table once and the whole
tranche is mechanical:

| Now | Uses | Expands to |
|---|---|---|
| `ent`, `ents` | 331 | `entity`, `entities` |
| `spec` (as a shortening) | 220 | `document` — **and `spec` is then reserved** for the glossary's sense: the one document a provider is generated from |
| `attr`, `attrs` | 181 | `attribute`, `attributes` |
| `op`, `ops` | 166 | `operation`, `operations` — also closes the retired `Op`/`Ops` spelling |
| `sum` | 114 | `summary` |
| `cfg` | 112 | `config` (the type is `config.Config`; the local is `config`) |
| `pkg`, `pkgs` | 93 | `package` is reserved, so `goPackage` / `goPackages` |
| `doc`, `docs` | 91 | `document`, `documents` |
| `param`, `params` | 73 | `parameter`, `parameters` — also closes the retired `Param`/`Params` spelling |
| `ptr` | 71 | `pointer` |
| `props`, `prop` | 50 | `properties`, `property` |
| `ext`, `exts` | 56 | `extension`, `extensions` |
| `req` | 53 | `request` |
| `dst`, `src` | 76 | `destination`, `source` |
| `val`, `vals` | 70 | `value`, `values` |
| `idx` | 43 | `index` |
| `prev`, `cur` | 69 | `previous`, `current` |
| `buf` | 33 | `buffer` |
| `resp` | 23 | `response` |
| `hyps` | 23 | follows whatever `Hypothesis` becomes (§III.5) |
| `disc` | 22 | `discriminator` |
| `desc` | 20 | `description` |
| `msg` | 16 | `message` |
| `elem`, `elems` | 15 | `element`, `elements` |
| `str` | 8 | `text` (`string` is reserved) |
| `oag*` | 4 helpers + a file name | `openAPIGenerator*`, and `services_openapigenerator_test.go` |
| `synth`, `SynthHint` | ~30 | `synthesise` / `synthesisedValue*` — R8 spelling |
| `EC`, `HasEC`, `ECDuration` | template contract | `ReadAfterWriteLag`, `HasReadAfterWriteLag` |
| `Ctor`, `ResourceCtor` | template contract | `ResourceConstructor` |
| `Pascal`, `Camel` (`ir.Names`) | field names | `PascalCase`, `CamelCase` |
| `maxAdjustIters` | const | `maxAdjustmentAttempts` |
| `semverRE`, `templateParam` | vars | `semverPattern`, `templateParameterPattern` |
| `specDoc` | var | `embeddedDocument` |
| `updProof` | field | `updateEvidence` |
| `PKG_MIN`, `TOTAL_MIN` | shell env | `PACKAGE_MINIMUM`, `TOTAL_MINIMUM` |
| single-letter locals (`s`, `b`, `r`, `a`, `n`, `i`, `j`, `k`, `m`, `d`, `e`, `u`, `v`, `pi`, `oi`, `ds`, `lr`, `tc`) | several hundred | named for what they hold: `index`, `builder`, `rune`, `entry`, `resource`, `datasource`, `listResource`, `testCase`, … |

**Ruling — approve the expansion table as a single tranche?** ______________

## III.2 — Decided by R5 (one word, one concept)

Each collision resolved. Where the glossary already fixes one sense, that sense
keeps the word and the other gives way.

| Word | Sense that keeps it | Sense that gives way | Proposed |
|---|---|---|---|
| **orphan** | none — R3 kills it outright | all three | live object → `undeletedObject`; manifest → `unproducedFile`; fixture entity → `widget` (R9) |
| **envelope** | `listResponseShape`'s wrapper (glossary) | quirkserver error-body shape | → `errorBodyShape`; `EnvelopeProblem` → `ErrorBodyProblem`, etc. |
| | | `sdkbind.EnvelopeKey` | → `ListWrapperKey` |
| | | kiota error interface family | folded into R7 rename |
| **variant** | *variant attribute* (glossary) | `strategy.Variant` | → `GateValueShape` |
| | | `plan/synth.go` `variant` (a second value for an update) | → `alternateValue` |
| | | `fixtures.variantModel` | → `discriminatorOwnership` |
| **registry** | provider-core registration slots (glossary) | `run.registry` (entity → live object) | → `createdObjects` |
| | | emitted mocks `Registry`/`Register` | → `MockResponderSet` / `AddResponders` (R7, §III.5) |
| **gate** | `strategy.Gate` — the discriminator field | `sdkgen.gate()` (tool-version check) | → `refuseVersionMismatch` |
| | | "the drift gate" / "the coverage gate" (prose) | prose, unchanged |
| **preflight** | `config validate` (glossary: "the offline preflight") | `audit/run` foreign-object check | → `checkTenantIsEmpty` |
| | | `60-release.yml` job | → `release-checks` |
| **grammar** | neither | quirkserver refusal sentence form | → `refusalSentenceForm` |
| | | executor refusal parser | → `refusalParser` |
| **plan** | terraform's plan, and the audit plan (glossary) | `emit.callPlan` | → `renderedCall` |
| | | `emit.writePlan` | → `writeConversion` |
| **site** | neither | `ir.site` | → `foldedProperty` |
| | | `revise.propSite` | → `propertyLocation` |
| **stub** | `quirkserver` (glossary) | `sdkgen` fake generator binaries | → `fakeGenerator*` |
| | | curated fixture's hand-written SDKs | → `handWrittenSDK` |
| **claim** | see §III.5 — depends on the `Hypothesis` ruling | | |

**Duplicate declarations**, all resolved by declaring once and importing:

| Concept | Currently | Proposed |
|---|---|---|
| `Provenance` | `observe` + `strategy` | declare in `observe`; `strategy` imports it |
| `Outcome` | `observe` (approved) + `store` | `store.Outcome` → `store.ImportResult`; `Repinned` → `Repinned` kept as a value name only |
| wire sentinels | `plan/steps.go` + `run/strategize.go` | declare in `plan`; `run` imports |
| `offlineSignatures` | `providergen` + `emit` test | declare once in `providergen`; export it |
| `unitEndpoint` | template + `emit/render_resource.go` | template consumes it from the render context |
| `Exclusion` | `specmodel` + `ir` | declare in `specmodel`; `ir` imports |
| `deriver` | `ir` + `audit/plan` | rename per package: `modelBuilder` / `planBuilder` |
| `primaryGate` | `strategy` + `run/cycle.go` | `run` calls `strategy`'s |

**Ruling — approve §III.2 as a tranche?** ______________

## III.3 — Decided by R6 (the wire spelling is the Go spelling)

The four kind/key mismatches, plus the retired tag:

| Go name | Writes today | R6 requires | Ruling |
|---|---|---|---|
| `readAfterWrite` | `x-tfpfgen-eventual-consistency` | `x-tfpfgen-read-after-write` | |
| `values` | `x-tfpfgen-values-open` | `x-tfpfgen-values` — **or** rename the kind to `valuesOpen`, which reads better; owner picks | |
| `ignoredOnUpdate` | `x-tfpfgen-silently-ignored-on-update` | `x-tfpfgen-ignored-on-update` ("silently" is editorialising) | |
| `immutable` | `x-tfpfgen-create-only` | `x-tfpfgen-immutable` — **or** rename the kind to `createOnly`; owner picks | |
| `ElementType` | `json:"element_kind"` | `json:"element_type"` | |
| `FieldBinding.Attr` | `json:"attr"` | `json:"attribute"` (R4 too) | |
| `sdkbind.Params` | `json:"params"` | `json:"parameters"` (R4 too) | |

The five keys with no mismatch — `-required-when`, `-server-default`,
`-volatile`, `-delete-not-found-ok`, `-server-forced` — pass R6 already. They
still need approval as *terms* (§III.5), but not renaming.

**The four spellings of `list resource`**, resolved by R6's casing table:

| Surface | Spelling | Declared once at |
|---|---|---|
| Classification kind, binding kind, `unsupported.json` path | `list_resource` | one exported const in `specmodel` |
| Registry slot | `list_resources` | `emit.RegistrySlots` (unchanged, approved) |
| Emitted directory, service-template directory | `list-resources` / `list-resource` | one const in `emit` |

Today `specmodel` says `"list-resource"` where `sdkbind` and `emit` say
`"list_resource"` — that one is a straight defect, not a convention question.

**Ruling — approve §III.3?** ______________

## III.4 — Decided by the retired list

No judgment needed; the glossary already ruled. Nine sites:

| Term | Sites | Becomes |
|---|---|---|
| `refineReserve`, "refining"/"refined" | `strategy/program.go:63,68,101`; `run/steps_create.go:15,110` | `createStepRequestBudget`; "adjusting"/"adjusted" |
| `Op` (type), `flatOp`, `op` (param) | `specmodel/classify.go:36` + 4 packages; `correction.go:152`; `emit`, emitted `errors.go.tmpl` | `Operation`, `flatOperation`, `operation` |
| `json:"element_kind"` | `sdkbind/model.go:288` | `json:"element_type"` |
| `CallParam`, `Params`, `callParams`, `paramName`, `"param_"`, `settleParamTypes` | `sdkbind` | `CallParameter`, `Parameters`, `callParameters`, `parameterName`, `"parameter_"`, `resolveParameterTypes` |
| `pathParams`, `itemKeyParam`, `templateParam`, `keyParam` | `ir` | `pathParameters`, `itemKeyParameter`, `templateParameterPattern`, `keyParameter` |
| `paramFailure`, `paramField`, `paramNode`, `paramValue`, `paramSegmentIndex`, `actionParamNodes`, `integerParsedParams`, `UpdateParamCopies` | `emit` | `parameter*` throughout |
| `trailingParam`, `knownQueryParams`, `params` | `specmodel`, `quirkserver`, `ir` tests | `trailingParameter`, `knownQueryParameters`, `parameters` |

**Ruling — approve §III.4?** ______________

## III.5 — Needs your ruling

The rules narrow these but do not pick. Roughly forty decisions. A proposal is
given for each so you can approve rather than compose.

### A. The `internal/audit/strategy` vocabulary

The package's own comment says these await you. Nothing downstream can settle
until they do.

| Now | What it is | Proposed | Ruling |
|---|---|---|---|
| `Hypothesis`, `HypothesisKind` | A candidate conditional rule read out of the document before anything touches the network. The live audit confirms or refutes it; a confirmed one becomes an **observation**. | `Claim` / `ClaimKind` — the glossary already says "how far the audit got with one **claim**" and describes each kind as *claiming* something. **Requires renaming `run.claim`** (the observation a step would have produced) to `pendingObservation`. | |
| the 5 `HypothesisKind` values (`variant`, `requiredWhen`, `requiresField`, `mutuallyExclusive`, `validWhen`) | The shapes a candidate rule can take. | Make them **the same words as the observation kinds they become** — `validConfiguration`, `requiredWhen`, `dependsOn`, `mutuallyExclusive`, `validWhen` — so a claim and the observation it becomes share a name and only the type differs. (`requiresField` → `dependsOn` is the one real change.) | |
| `Skeleton` | The synthesised request body for one gate value: field names plus per-field material. Not yet sent. | `DraftRequestBody` — pairs with the approved **request bodies**, which names the accepted ones. New root word `draft`, so it needs you. | |
| `Check` | The single live request that would confirm or refute one claim. | `Probe` — the glossary already uses it ("the made-up-field probe") and `probe_fidelity_test.go` is named for it. Its comment calling *probe* retired is simply wrong (§7.2). | |
| `Gate`, `GateKind`, `gateField`, `gateValue` | The field whose value selects which shape a body must take. | `keep` and add to the glossary — it is already glossary prose ("the discriminator (gate) field"), it is not a metaphor, and it passes R1. Alternative if you want it fully literal: `Discriminator`, but that collides with OpenAPI's own `discriminator` keyword for a narrower thing. | |
| `SynthHint` | The schema facts a value is built from at run time. | `ValueSynthesisRule` (R4 expands `synth`, R8 spells it `-is-`). | |
| `Strategy`, `Program`, `Step` | The compiled per-entity audit plan and its ordered steps. | `EntityAuditPlan` / `Steps` / `Step` — but this collides with the existing `audit/plan` package. Alternative: keep `Strategy`, add to glossary. **Recommend `keep`** — it passes R1 and R3, and the collision cost of renaming is higher. | |
| `Check.Expect` = `accept`/`reject`/`conditional` | What the API is expected to do. | `ExpectedAnswer` with the same three values; `keep` the values. | |
| `Role` = `resource`/`lookup`/`datasource` | Which of three shapes an entity's audit takes. | `AuditShape`, and `lookup` → `lookupByKey` to match the approved *lookup-by-key datasource*. | |
| `Budget.Formula` | A human-readable string recording a budget's arithmetic. | `BudgetDerivation`, or `delete` — it is a debugging aid on a committed artifact. | |
| the `*Reserve` family | Per-step-kind request weights. | `*StepRequestBudget` (R3 kills "reserve" as a coined unit). | |
| `proseCategory`, `prosePhrase`, `proseHypotheses` | The English phrases mined from property descriptions. | `keep` the `prose` stem — it is an approved **provenance** value and not a metaphor — expanded per R4: `proseRuleCategory`, `prosePhrase`, `proseClaims`. | |

### B. `internal/audit/run` metaphors

| Now | What it is | Proposed | Ruling |
|---|---|---|---|
| `heal` / `healed` / `unhealable` (~20 uses, one in operator-facing text) | Making a refused create body acceptable. | `adjust` / `adjusted` / `unadjustable` — reuses the approved **requestAdjustment**, and `adjust.go` already carries it. | |
| `cleanupDebris` | The pass removing objects a previous run left live. | `cleanupLeftoverObjects` | |
| `maximalCulprit`, `suspects` | The optional field a refusal was attributed to. | `refusedOptionalField`, `candidateFields` | |
| `entityRecipe`, `recipeOf` | How an entity creates, addresses and deletes its objects. | `entityLifecycle`, `lifecycleOf` | |
| `cycleConditional`, "value-cycling", `cyclableSiblings`, `maxCycleAttempts` | Retrying a create with different enum values until one is accepted. | `retryAcrossGateValues`, "gate-value retry", `retryableSiblings`, `maxGateValueAttempts` | |
| "refusal grammar", `classifyRefusal`, `refusalAction`, `actKind`, `act*` consts | Parsing a 4xx message into an instruction. | `refusalParser`, `classifyRefusal` (keep), `refusalInstruction`, `instructionKind`, `instruction*` — and align the four values with the approved `requestAdjustment` actions, keeping `actStop` as `instructionStop`. | |
| "the additive search", `searchMinimal`, `searchAllowance`, `searchCandidates` | Adding one field at a time until a create is accepted. | `addFieldsUntilAccepted`, `fieldAdditionBudget`, `fieldsToTry` | |
| `bisectMaximal`, `bisectionAllowance` (**in plan JSON**) | Halving the optional-field set to find the field a refusal is about. | `narrowRefusedField`, `narrowingBudget` — contract surface, so it lands in the §III.3 tranche | |
| `strategize` (func + file) | Replacing plan steps with the compiled strategy's program. | `applyStrategy` / `apply_strategy.go` — follows whatever `Strategy` becomes | |
| `claim`, `stepClaims`, `emitHaltedClaims`, `halt` | The observation a step would have produced, and why the entity stopped. | `pendingObservation`, `stepObservations`, `emitStoppedObservations`, `stopReason` — depends on the `Hypothesis` ruling above | |
| `preflight`, "foreign objects" | Checking the tenant looks like a sandbox before the first create. | `checkTenantIsEmpty`, `objectsNotCreatedByThisRun` | |
| `*Proof` fields, `condCoords`, `gaveUp`, `combinedRefusals` | Evidence carriers. | `*Evidence`, `conditionalKey`, `exhausted`, `refusedTogether` | |
| `StatusAudited` (**on `audit/summary.json`**) | Per-entity status meaning the entity finished. | Fold into the approved **outcome** set — an entity that finished is `confirmed`; drop the parallel status type entirely. Contract surface. | |

### C. Emitted provider vocabulary (R7: HashiCorp picks, the toolkit follows)

| Now | What it is | Proposed | Ruling |
|---|---|---|---|
| `MapRemoteStateToTerraform` / `MapRemoteStateToDatasource` | Copies the SDK's read model onto the framework model. | `APIToFrameworkModel` / `APIToFrameworkDatasourceModel` — the approved `APIToFramework*` catalog already names this direction, and it does not collide with Terraform's "remote state". | |
| mocks `Registry`, `Register`, `NewRegistry`, `GlobalRegistry`, `MockRegistrar` | The table of per-entity mock responders. | `MockResponderSet`, `AddResponders`, `NewMockResponderSet`, `AllMockResponders`, `MockResponders` | |
| emitted `Operation` + `OperationCreate/Read/Update/Delete/Invoke` | Which lifecycle method an error interrupted. | `LifecycleMethod` + `LifecycleCreate/Read/Update/Delete/Invoke` | |
| `CoManagementNote` | The sentence added when several entities write to one API collection. | `SharedCollectionNote` | |
| `StateContainer`, `CreateResponseContainer`, `UpdateResponseContainer` | The get/set pair the read-after-write loop rewrites. | `StateAccessor`, `CreateResponseState`, `UpdateResponseState` | |
| `ConsistencyPredicate` | The callback that keeps a read retrying until state settles. | `ReadSettledFunc` | |
| `kiotaSilence`, `kiotaSaid`, `kiotaDetailed`/`Titled`/`Messaged`/`Errored`/`Undeclared` | kiota error-body handling. | `kiotaEmptyMessage`, `kiotaMessage`, and the interface family → `kiotaWith<Getter>` named for the getter each asserts | |
| `Info` (the errors package's central type) | The `{Status, Message}` description of one API error. | `APIError` | |
| `IsFatalRead`, `IsRetryableDelete` | The HTTP-status policies. | `ReadStatusIsFatal`, `DeleteStatusIsRetryable` | |
| `constructResource`, `constructUpdate`, `constructInvocation`, `construct.go` | Build the SDK request body from the plan. | `buildCreateRequest`, `buildUpdateRequest`, `buildInvokeRequest`, `build_request.go` | |
| `seeded`, `mockState` | The stateful mock store and its starting fixture. | `initialObjects`, `mockObjects` | |
| `TestResource`, `CheckExists`, `CheckDestroyed` | The per-resource live-existence check. | `ExistenceChecker`, `CheckExists` (keep), `CheckDestroyed` (keep) | |
| `wire` (the compiled-in extractor) | The single error extractor instance. | `errorExtractor` | |
| `app_id`, `app_private_key` (**HCL attributes**) | GitHub App credentials in the generated provider block. | Add to the approved provider-block list as-is — they match `client_id`/`client_secret`'s shape and every other provider spells them this way. Contract surface. | |
| `tfpfgen_run` (**in every generated `.tf`**) | The `random_string` whose value suffixes synthesised names. | `keep` — it is namespaced, literal, and identifies the toolkit. Add to the glossary. | |
| `conditional_validators.go` | The generated file carrying `ConfigValidators`. | `config_validators.go` — matches the framework's own `ConfigValidators` | |
| `UnitEndpoint` | The unreachable base URL unit-test responders register under. | `MockBaseURL` | |
| `listedresource` (import alias) | The alias a list-resource test imports its resource under. | `managedresource` | |

### D. Contract terms that need approving as terms

These pass R1, R3 and R6 — they are literal and their spellings are
consistent. What they lack is your approval and a glossary entry.

| Group | Items | Proposed | Ruling |
|---|---|---|---|
| `x-tfpfgen-*` keys with no mismatch | `-required-when`, `-server-default`, `-volatile`, `-delete-not-found-ok`, `-server-forced` | `keep`, add all five to the glossary's approved extension list | |
| The 14 unrecorded observation kinds | `writable`, `immutable`, `requiredByAPI`, `requiredWhen`, `serverDefault`, `derivedDefault`, `normalisation`, `ignoredOnUpdate`, `serverForced`, `volatile`, `values`, `updateStyle`, `deleteNotFoundOK`, `readAfterWrite` | `keep` all fourteen, add to the glossary table (§7.3) | |
| `tfpfgen.yaml` config keys | the 18 in §1.6 | `keep` — the glossary already delegates the schema to `internal/config`, and `docs/config.md` is generated with a test that forbids drift (§7.5) | |
| `audit/summary.json` fields | `ledgerDeletes`, `prefixDeletes`, `edgesConfirmed`, `edgesInconclusive`, `skippedEntities`, `byKind`, `byOutcome` | `keep`; add **edge** and **the prefix pass** to the glossary as terms | |
| Wire sentinels | `"tfpfgen-undocumented"`, `"tfpfgen_unknown_field"` | `keep` the values, declare each **once** (§III.2), and make them consistent: both hyphenated or both underscored — they currently disagree | |
| `Drift` + `changed`/`missing`/`extra`/`hand-edited` | `sdk verify` / `provider verify` output | `keep`; add **drift** and its four kinds to the glossary | |
| `proposals.json` schema | `findings`, `groups`, `means`, `merging`, `closing`, `expected`, `observed`, `valueSpelling`, `kindTitle`, `kindPlural` | `keep` most; `valueSpelling` → `renderedValue` (R1: "spelling" does not say what it holds) | |
| `spec/upstream.yaml`, `spec/upstream.lock.json`, `Lock` | The pinned document and its pin record | The glossary's verb is **import** and its noun is **pin**, so: `spec/imported.yaml`, `spec/imported.pin.json`, type `Pin`. Contract surface. | |

### E. Coined stage names

| Now | What it is | Proposed | Ruling |
|---|---|---|---|
| `Materialize` / `materialise` | Writing `spec/revised.yaml` from the pin plus accepted corrections. | Fold into the approved verb **revise**: `Revise`. Delete both spellings. | |
| `Prenormalize` (+ its file, ~29 uses, and `revised.prenormalized.yaml`) | The five document rewrites before the SDK generator runs. | `PrepareForGenerator` / `prepare_for_generator.go` / `spec/revised.prepared.yaml` — or `keep` and add **prenormalise** to the glossary (R8 respells it). | |
| `Postcheck` (9 identifiers, a file, a CLI flag, a CI step, a **manifest origin**) | `go mod tidy` + `go build` + `go vet` in the generated tree. | `VerifyGeneratedTree` / `--verify-tree` / manifest origin `"toolchain"`. The manifest origin is a contract surface. | |
| "the prefix pass" | The cleanup sweep deleting by name prefix. | `keep`, add to the glossary | |
| "withdraw" (a CI job, ~18 uses, **documented in `docs/contract.md:95`**) | Closing correction PRs a run no longer proposes. | `keep` — it is documented behaviour with no other name. Add **withdrawn** to the glossary's correction lifecycle beside proposed / accepted / rejected (§7.4). | |
| quirkserver `shape*` (4 files, 9 funcs, ~100 uses), `exhibit*` (3 files) | Always-on API surfaces the audit is asserted against; one demonstration per quirk. | `fixtureAPI*` / `fixture_api_*.go`; `demonstration*` / `demonstration_*_test.go` | |
| the 38 `Quirks` fields | Named API misbehaviours. | `keep` as a set — they are literal and pass R1 — but align the ones that shadow observation kinds under different words: `ImmutableAfterCreate` → `Immutable`, `EventuallyConsistentReads` → `ReadAfterWrite` | |

### F. Concepts that may not survive under any name

| Now | The question | Ruling |
|---|---|---|
| `"strip-schema-defaults"` | A fifth JSON-Patch operation invented against RFC 6902 and written into committed correction files. The glossary defines a correction as "RFC 6902 operations". Does the concept survive at all — expressed instead as generated `remove` operations, one per schema — or is the extension kept and the glossary's definition widened? | |
| `Budget.Formula` | A human-readable arithmetic string on a committed artifact. Keep, or delete? | |
| `curated` / `fictional` / the `beacon`/`gizmo`/`blob`/`orphan` fixture entities | R9 says fixture nouns are literal. Rename the fixture vocabulary, or exempt `testdata/` from R9 as produced-not-maintained? | |
| The `strategy` package's `Provenance` duplicate | Delete and import `observe`'s, or is the duplication deliberate to keep `strategy` free of an `observe` dependency? | |

---

# Part IV — Applying it

Once Part I is approved and Part III is ruled on, in tranches:

| # | Tranche | Contract impact | Verified by |
|---|---|---|---|
| 1 | **Comment-style** (§6) — counts, narrative, roadmap, the ListWrap codename, vendor anecdotes; and section dividers if §6.7 says add them | none | `make check` |
| 2 | **Retired spellings** (§III.4) | none | `make check` |
| 3 | **Collisions and duplicate declarations** (§III.2) | none | `make check` |
| 4 | **Abbreviations** (§III.1) — largest volume, mechanical | none | `make check` |
| 5 | **Internal coined terms** (§III.5 A, B, E) | none | `make check` |
| 6 | **Emitted provider vocabulary** (§III.5 C) | breaks generated trees | `make check`, plus regenerating a provider tree and byte-comparing |
| 7 | **Contract surfaces** (§III.3, §III.5 D, and the contract rows of A/B/E) | **semver event** | `make check`, `UPDATE_DOCS=1 go test ./internal/config -run TestUnit_Config_ReferenceMatchesDocs`, migration note in the PR body |
| 8 | **Glossary and `docs/naming-standard.md`** — Part I lands as the standard; every approved term from Part III lands in the glossary; `CLAUDE.md` references both | none | `docs/config.md` regenerates unchanged |

Tranches 1–5 can land in any order. 6 must precede 7 only if a contract name is
derived from an emitted one. 8 lands with each tranche that introduces a term,
not once at the end — `docs/glossary.md` must never be behind the tree again.

---

# Appendix — how this audit was run

Against commit `7de76bb`. Four sweeps: three package-scoped identifier sweeps
(`audit`+`spec`+`specmodel`+`config`; `emit`+`templates`+`fixtures`+
`providergen`+`manifest`+`code`; `intermediate_representation`+`sdkbind`+
`sdkgen`+`cli`+`quirkserver`+`cmd`+workflows+scripts), plus a repo-wide
comment-style sweep. Every `file:line` in Sections 1, 2, 4 and 6 was read
directly. Section 3's rows come from the package sweeps; the loudest twenty
were re-verified by hand.

Not covered, by scope agreement: `testdata/**` contents beyond the fixture
entity nouns; the generated trees in provider repos; `docs/*.md` prose beyond
the specific lines cited.


---

# Part V — Rulings, as given

Recorded in the order decided. These supersede the proposals in Part III.

## Standard

| # | Ruling |
|---|---|
| R4 exceptions | **Three exceptions plus a shadowing rule.** `ctx`, `err`, `ok` keep their short form — no expansion exists that does not fight the language. Everything else expands fully; where the natural expansion would shadow an imported package or a predeclared identifier, pick the next fullest word that does not: `cfg` → `configuration` (not `config`), `pkg` → `goPackage`, `str` → `text`, `len` → `length`. |
| Acronym casing | **The acronym table wins.** `openAPISpec`, not `openApiSpec` — consistent with `HTTPServer`, `APIKey`, `openAPIGenerator`, and it governs generated provider names too. |

## §III.1 — abbreviations

| # | Ruling |
|---|---|
| Scope | **Approved as one tranche**: production and tests together, named abbreviations and single-letter locals. |
| `spec` | **→ `openAPISpec`** where it names the OpenAPI document. Lazy locals are **not** blanket-renamed — each is named for the purpose it serves at that site (`createBody`, `listResponse`, `tagDocument`, …). `spec` survives only in the glossary's sense: `internal/spec`, `spec/revised.yaml`, `spec.document_url`. |

## §III.2 — collisions

| Word | Ruling |
|---|---|
| **orphan** | **All three give way.** audit → `UndeletedObjects`, operator line `undeleted: <id>`, error "N object(s) could not be removed; see the undeleted list"; manifest → `UnproducedFilesOf` / `removeUnproducedFiles`; fixture entity → `widget`. *Contract: `audit/summary.json` `orphans` → `undeletedObjects` — tranche 7.* |
| **envelope** | **Killed outright, both senses.** A JSON response is not an envelope. List sense → **wrapper**: `x-tfpfgen-list-response-shape: {wrapper: wrapped \| bare, key: <key>}`; `sdkbind.EnvelopeKey` → `ListWrapperKey`; `ListEnvelopeWrapped`/`Bare` → `ListWrapperPresent`/`Absent`; `run.envelopeKeys` → `listWrapperKeys`; `quirkserver.envelopeKey` → `listWrapperKey`. Error sense → `ErrorBodyShape` with `ErrorBodyProblem`/`OAuth`/`Legacy`/`Empty`. *Contract: the approved extension value changes — tranche 7, migration note.* |
| **variant** | **Kept for the three discriminator senses**, which are one idea at three stages: the glossary's *variant attribute*, `strategy.Variant`, `fixtures.variantModel`. Only the unrelated sense gives way: `plan/synth.go`'s `variant()` (a second value that differs from the first) → `alternateValue()`. |
| **registry** | **Both real registries keep it; the map gives way.** `emit.Registry` (approved) and the emitted `mocks.Registry` are both tables things register themselves into, so one word for one concept is correct. `run.registry` registers nothing — it is a lookup — so → `createdObjects`. |
| **gate** | **Both keep it — different parts of speech.** The noun (`strategy.Gate`, the field whose value selects a body shape) and the verb sense (a check that refuses passage: "the drift gate", "the coverage gate") are one English word doing two grammatical jobs. Both go in the glossary explicitly. *Separately, `sdkgen.gate(tool, have, want)` still fails **R1** as a bare name — carried as an R1 item, → `refuseVersionMismatch`.* |
| **preflight** | **All three keep it — one idea at three stages**, on the same reasoning as `variant`. `config validate` before anything credentialed, the audit's tenant check before the first create, the release job before publishing. Added to the glossary as a term in its own right. |
| **site** | **Both rename — they fail R1 independently of the collision.** `ir.site` (one property seen from both sides of the create/read fold) → `foldedProperty`; `revise.propSite` (a property's node plus the object schema declaring it) → `propertyLocation`. |
| **stub** | **Kept for all three — one idea, three uses**, on the same reasoning as `variant`, `preflight` and `grammar`: a hand-written stand-in for a real component, used as test input. Covers `quirkserver`, `sdkgen`'s fake generator binaries, and the curated fixture's hand-written SDKs. Glossary entry generalised from quirkserver's to cover all three. |
| **plan** | Terraform's `req.Plan` keeps it (R7, external). The remaining senses are being decided **individually**, not as a bundle. |
| **plan** — audit plan | **Keep.** `internal/audit/plan` is a plan in the ordinary sense — worked out before the run, then executed — and it is toolkit-internal, never Terraform-facing. |
| **plan** — `emit.callPlan` | **→ `finalisedAPIRequest`.** *Note: `audit/run`'s `reqSpec` (→ `requestSpec` under R4) describes an actual HTTP request, while this describes the rendered Go that makes one. Two "API request" things in the tree — flagged under R5, no action unless you want one.* |
| **plan** — `emit.writePlan` | **→ `writeConversion`.** Pairs with the approved `FrameworkToAPI*` catalog, which is exactly this direction. |
| **grammar** | **Kept — one contract, two ends.** The quirkserver emits refusal sentences in a fixed form; the audit executor parses it. Both sides need the word. No identifiers carry it — doc comments and one test name only. Glossary entry added. |
| **quirkserver** | **→ `testapiserver`** (package), `TestAPIServer` (identifiers) — casing per the acronym-table ruling. `Quirks` stays as the switch set: each switch encodes one behaviour observed in a real API. Hidden verb → `__serve-test-api-server`. *Reopens an approved glossary term.* |

**All ten collisions ruled.** Remaining in §III.2: the eight duplicate declarations.

### Duplicate declarations

| Concept | Ruling |
|---|---|
| **`Provenance`** | **Keep both — the duplication is deliberate.** `strategy` does not import `observe`, and unifying would make the document-reading compiler depend on the observation writer. Glossary records the mirror as intentional; a test asserts the two value sets stay identical. |
| **`Outcome`** (`spec/store`) | **→ `ImportAction`.** `observe.Outcome` (approved, four values) keeps the name. |
| **`Exclusion`** | **Both rename — "exclusion" is the wrong word.** It conflated an operator's choice (`services.exclude`) with the toolkit's own refusal. `specmodel.Exclusion` → `UnclassifiedEntity`; `ir.Exclusion` → `UnsupportedEntity`, aligning with the approved `unsupported.json`. |

## `shape` — banned as an identifier

| Scope | Ruling |
|---|---|
| **The word** | **Banned as an identifier or contract value; allowed in prose.** Same carve-out R3 makes for `gate`, `binder`, `loader`: a comment saying "the shape the generated SDK already has" is ordinary English and stays. A type, function, variable or wire value named `shape` does not, because a reader must guess which of four things is meant. ~90 identifier uses change; ~250 prose uses stay. |

| Use | Ruling |
|---|---|
| **`listResponseShape`** | **Split — it records two unrelated facts.** Wrapping and pagination have nothing to do with each other, which is why no single word fitted. Two extension keys, two observation kinds: `x-tfpfgen-list-wrapper: {wrapped: true, key: tags}` / `{wrapped: false}`, and `x-tfpfgen-list-pagination: cursor \| offset \| page \| none`. Observation kinds `listResponseShape` → `listWrapper` + `listPagination`. This also absorbs the earlier **envelope** ruling: `ListEnvelopeWrapped`/`Bare` disappear into the boolean, and `sdkbind.EnvelopeKey` → `ListWrapperKey`. *Two contract changes — tranche 7, migration note.* |
| **quirkserver `shape*`** | **→ `handle*`** — they are HTTP handlers over a `collection`, and the package already has `handlers.go`. `shapeCreate/List/Read/Update/Delete/Invalid` → `handleCreate/List/Read/Update/Delete/Invalid`; `routeShape` → `routeCollection`; `initShapes` → `initCollections`. Files: `shapes.go` → `collections.go`, `shape_monitor.go` → `collection_monitor.go`, `shape_assignment.go` → `collection_assignment.go`, `shape_stream.go` → `collection_stream.go`. |
| **`classify.go` `*Shape` booleans** | **→ `has*Operations`.** `resourceShape` → `hasResourceOperations`, `dsShape` → `hasDatasourceOperations`, `lookupShape` → `hasLookupOperations`, `singletonShape` → `hasSingletonOperations`, `listOnlyShape` → `hasListOnlyOperations`. The paired `*OK` locals stay. |

## §III.5 A — the `internal/audit/strategy` vocabulary

| Now | Ruling |
|---|---|
| **`Hypothesis`, `HypothesisKind`** | **→ `Claim` / `ClaimKind`.** The glossary already says "how far the audit got with one **claim**" and describes every observation kind as *claiming* something, so one word covers the fact before and after it is tested. *Knock-on: `run.claim` — an unrelated thing, the observation a step would have produced had it run — → `pendingObservation`.* |
| **`Skeleton`** | **→ `RequestFields`.** It is the list of field names one request body will carry, with the per-field material for inventing each value. Reads as `Minimal RequestFields` / `Maximal RequestFields`. |
| **`Check`** | **→ `Probe`.** The glossary already uses the word for exactly this ("the made-up-field probe"), and `probe_fidelity_test.go` is named for it. The comment at `strategy.go:191` claiming *probe* is retired is simply wrong — nothing retired it — and is deleted. |
| **`heal` / `healed` / `unhealable`** | **→ `correct` / `corrected` / `uncorrectable`.** *See the disambiguation note below.* |
| **`heal`** (applied) | **→ `correctBody` / `bodyCorrected` / `uncorrectableRefusal`.** Qualified so it can never be misread as producing a document **correction**. The approved `requestAdjustment` (add \| remove \| requires \| borrow) is untouched. `adjusted` → `bodyCorrected`, `adjustCreate` → `correctCreateBody`, `maxAdjustIters` → `maxBodyCorrectionAttempts`. |
| **`SynthHint`** | **→ `SyntheticValueRules`.** The rules a made-up value for one field must satisfy: type, format, pattern, enum members, example, default, numeric bounds. *Small follow-up at implementation time: the carrying field reads `Rules []SyntheticValueRules`.* |
| **`Strategy`, `Program`, `Budget`** | **All three kept.** None is a metaphor; renaming `Strategy` would collide with `audit/plan`, which keeps its name. All three added to the glossary. |
| **the `*Reserve` family** | **→ `*StepRequests`.** `refineReserve` → `createStepRequests`, then `maximalStepRequests`, `updateStepRequests`, `pollStepRequests`, `deleteStepRequests`, `cleanupStepRequests`, `negativeStepRequests`, `preflightRequests`. Kills the **retired** `refine` stem and the coined "reserve" unit in one move; `stepRequests()` already exists with that name. |

## §III.5 B — the `internal/audit/run` metaphors

| Now | Ruling |
|---|---|
| **`cleanupDebris`** | **→ `cleanupLeftoverObjects`.** Keeps the approved verb **cleanup** as the head; the doc comment already reached for "leftovers". Pairs with the orphan ruling: `CleanupSummary.Orphans` → `UndeletedObjects`. |
| **`maximalCulprit` / `suspects`** | **→ `refusedOptionalField` / `optionalFields`.** The doc comment already contained the right sentence — "narrows a refused maximal create to the optional field the API objected to". |
| **`entityRecipe`** | **→ `entityLifecycle`.** It holds what is needed to create and destroy one object, which is the lifecycle — Terraform vocabulary already. `recipeOf` → `lifecycleOf`, `ent.recipe` → `ent.lifecycle`, `r.recipes` → `r.lifecycles`. |
| **`cycleConditional` / "value-cycling"** | **→ `retryAcrossValues`.** `cyclableSiblings` → `retryableSiblings`, `maxCycleAttempts` → `maxValueRetries`, `recordConditional` → `recordValueOutcome`, `condCoords` → `valueOutcomeKey`, `cycle.go` → `retry_across_values.go`. |
| **`actKind` / `refusalAction`** | **→ the approved `requestAdjustment` vocabulary.** `actKind` → `adjustmentKind`; `actStop/Add/Remove/Requires/Borrow` → `adjustmentNone/Add/Remove/Requires/Borrow`; `refusalAction` → `parsedRefusal`. Four of the five values already *were* the approved actions under a private duplicate spelling. |
| **the additive search** | **→ `addFieldsUntilAccepted` + `*AttemptLimit`.** `searchCandidates` → `fieldsToTry`, `searchAllowance` → `fieldAdditionAttemptLimit`. Sibling follows: `bisectMaximal` → `narrowRefusedField`, `bisectionAllowance` → `fieldNarrowingAttemptLimit` *(in the committed plan JSON — tranche 7)*. `cleanupAllowance` → `cleanupTimeLimit`, since it is a time budget and was the third thing "allowance" meant. |
| **`strategize`** | **→ `applyStrategies`**, file `strategize.go` → `apply_strategies.go`. `Strategy` is kept, so the verb is the plain one for using it. |
| **`StatusAudited`** and the status set | **Deleted — folded into the approved `outcome` set.** The parallel three-value status type goes; an entity that finished is `confirmed`. `audit/summary.json` `"status": "audited"` → `"outcome": "confirmed"`. *Contract — tranche 7.* |
| **the `*Proof` fields** | **Kept — only the abbreviation fixed.** "Proof" says why the excerpt is kept, which is what a reader needs. `updProof` → `updateProof` (R4). |
| **`gaveUp` / `tried` / `conditional`** | **All three renamed.** `gaveUp` → `unresolved`; `tried` → `addedFields`; `conditional` → `freeFormConditional`, keeping the phrase the rest of the package already uses. `adjusted` → `bodyCorrected` (ruled earlier). |
| **the evidence signal types** | **Renamed for the observation kind each feeds**, so the mapping is unmissable: `combinedRefusals` → `mutuallyExclusiveSignal`; `infer.ConditionalValue` → `validConfigurationSignal`; `listBodies` → `listPaginationSignal`. `acceptedRequestBodies` keeps its name — approved term. |
| **`namedKnownFields`, `learnID`, `idFromSelfLink`, `guardMutation`** | **All four renamed.** → `declaredFieldsNamedIn`, `extractID`, `idFromResourceURL`, `refuseForeignHostWrite`. Removes `learn` and `guard` as metaphors and drops "self link", which is not a specified term. |

*§III.5 B complete. Remaining `audit/run` names are weak/mundane and default to `keep` unless raised.*

## §III.5 C — emitted provider vocabulary

| Now | Ruling |
|---|---|
| **`MapRemoteStateToTerraform`** | **→ `MapRemoteStateToResource`.** `MapRemoteStateToDatasource` **kept**. The `MapRemoteState*` family stays; it is made symmetric on the service kind instead of naming one side "Terraform". *I raised that "remote state" is also a Terraform term of art for backend-stored state; the owner has kept the family regardless. Only `resource` and `datasource` have one — `list-resource` and `action` emit no state mapper. The datasource's unexported `mapItem` follows the family.* |
| **`CoManagementNote`** | **Kept.** "Co-managed" is ordinary English and precise. It is the *concept* the glossary never caught up with, not the name — add **co-managed entity** as a term. |
| **`StateContainer`, `CreateResponseContainer`, `UpdateResponseContainer`, `ConsistencyPredicate`** | **All kept.** `Container` for a getter/setter pair and `Predicate` for a boolean function are standard programming vocabulary, and the types wrap framework types in framework spellings. Added to the glossary. |
| **emitted `Operation`** | **Kept.** Generated providers never import the toolkit, so `errors.Operation` and `ir.Operation` can never appear to one reader. Both senses recorded in the glossary, scoped. |
| **`wire`** (emitted errors pkg) | **→ `sdkErrors`.** Reads at the call site as `sdkErrors.extract(err)`. The doc comment's "exactly one dialect file is compiled into this package" also becomes "backend", per the `dialect` ruling. |
| **`kiotaSilence` / `kiotaSaid`** | **→ `kiotaEmptyMessage` / `kiotaErrorText`.** Both were figures of speech in code a provider maintainer cannot edit; the facts survive in the doc comments unchanged. |
| **`TestResource`** | **→ `RemoteObjectCheck`.** The name said the opposite of what the type is — it is not a resource under test, it is the out-of-band check that the object exists in the live API. Also fixes the `tr TestResource` parameter abbreviation. |
| **`Info`** (emitted errors pkg) | **→ `APIError`.** `errors.Info` said nothing — information about what. `extractor` **kept**: a fine Go name for a one-method interface, and its instance is now `sdkErrors`. |
| **the `construct*` family** | **Kept.** "Construct" is ordinary English for building a value and `WriteConstructor` matches Go's constructor convention. It is the write-direction counterpart to `MapRemoteStateToResource`; both go in the glossary as a pair. |
| **`IsFatalRead` / `IsRetryableDelete`** | **Kept.** Standard HTTP-client vocabulary, and the doc comments state the status sets exactly. |
| **`seeded` / `mockState`** | **→ `serverFilledFields()` / `mockObjects`.** `mockState` is the fake API's object store, not Terraform state, and the overlap cost something in a file full of real `tfsdk.State` handling. |
| **`app_id` / `app_private_key`** | **Kept — added to the approved provider-block attribute list.** They match `client_id`/`client_secret` in shape and their `<PROVIDER>_APP_ID` env fallbacks already follow the approved convention. The gap was the glossary, not the names. |
| **`listedresource`** | **Kept.** It names the relationship from the importing file's point of view: the resource this list resource lists. |

## §III.5 E — coined stage names

| Now | Ruling |
|---|---|
| **`Materialize` / `materialise`** | **→ `WriteRevision`.** Names the mechanical act, leaving the approved **revise** to mean the whole two-half stage, and avoids `revise.Revise` stuttering. Both spellings go; `docs/contract.md:29`, the cobra `Short`, the `--propose-only` help and `audit.go:268` all follow. |
| **`Prenormalize`** | **Kept, respelled `Prenormalise` (R8).** A real named stage the glossary never recorded — the same gap that left 14 observation kinds unlisted. `prenormalise.go`, `spec/revised.prenormalised.yaml`. Glossary entry added. |
| **`Postcheck`** | **→ `VerifyGeneratedTree`.** `PostcheckReport` → `TreeVerificationReport`, `postcheckSteps` → `verificationCommands`, `postcheckOwned` → `toolchainWritten`, `postcheck.go` → `verify_generated_tree.go`, `--postcheck` → `--verify-tree`. Manifest origin `"postcheck"` → `"toolchain"`, which is what every other origin value does — name who wrote the file. *Contract — tranche 7.* |

## §III.5 D and F

| Item | Ruling |
|---|---|
| **`"strip-schema-defaults"`** | **Correction op deleted.** `prenormalise` already runs the identical walk on every SDK generation, unconditionally — which is the right place, since the reason is a standing generator behaviour rather than one document's mistake. Nothing in `spec/revise` ever emitted the correction; only two tests constructed one. Removing it makes corrections RFC 6902 again, so the glossary definition needs no widening. `yamlwalk.StripSchemaDefaults` is untouched. |
| **`spec/upstream.yaml`, `spec/upstream.lock.json`, `Lock`** | **→ `spec/imported.yaml`, `spec/imported.pin.json`, type `Pin`.** Uses the approved verb (**import**) and noun (**pin**), so the pair reads as one idea. Fields unchanged. *Contract — tranche 7.* |
| **Terms approved as-is** | **All four groups approved**, to be recorded in the tranche-8 glossary update: (1) the five clean `x-tfpfgen-*` keys — `-required-when`, `-server-default`, `-volatile`, `-server-forced`, `-delete-not-found-ok`; (2) the fourteen unrecorded observation kinds; (3) the eighteen `tfpfgen.yaml` config keys, already governed by the generated `docs/config.md` and its no-drift test; (4) the `audit/summary.json` fields, plus **edge** and **the prefix pass** as terms. |

## §III.3 and §III.4

| Item | Ruling |
|---|---|
| **The four key/kind mismatches** | **Key follows kind in all four.** The kind is the primary spelling — what an observation carries and what the inference reasons about — and the extension key derives from it by R6's casing rule. `x-tfpfgen-eventual-consistency` → `-read-after-write`; `-values-open` → `-values`; `-silently-ignored-on-update` → `-ignored-on-update`; `-create-only` → `-immutable`. *Contract — tranche 7.* |
| **`list resource` spellings** | **Apply R6's casing rule; declare each once.** Three of the four spellings are correct for their surface. The defect is `specmodel/classify.go:24` — `"list-resource"` → `"list_resource"`, matching `sdkbind` and `emit` and the value that reaches `unsupported.json`. The kind value becomes one exported const in `specmodel` (today: five separate literals); the directory names one const each in `emit`. |
| **§III.4 — retired spellings** | **Approved as a tranche.** All nine groups, ~40 sites. `json:"element_kind"` → `json:"element_type"` is the one contract surface among them and rides tranche 7; the rest are Go-only. |

## Fixtures, quirks, comments

| Item | Ruling |
|---|---|
| **fixture vocabulary** | **`oag` fixed; `curated` and `fictional` kept.** `oag` abbreviates an approved backend name and its kiota counterpart is spelled out, so the pair was inconsistent as well as short: `services_oag_test.go` → `services_openapigenerator_test.go`, `oag*` → `openAPIGenerator*`, `kAccess` → `kiotaAccess`. `curated` and `fictional` are ordinary English describing exactly what those fixtures are; both go in the glossary. |
| **the 38 `Quirks` fields** | **Kept unchanged.** A quirk is a server behaviour; an observation kind is what the audit concluded. Forcing the names to match would imply a one-to-one mapping that does not exist. Recorded in the glossary as a set. **⚠ Conflict: `ErrorEnvelope` is one of the 38, and the *envelope* ruling killed that word — see the open item below.** |
| **§6.1–6.6 comment-style** | **Prescribed transforms applied as a tranche.** ~40 comments; `docs/comment-style.md` already decides each. Includes deleting the whole "Deferred naming … Wave 3 … provisional" block at `strategy.go:23-27`, now that §III.5 A has settled every name in it, and the four stale "by a later wave" comments in `observe.go`. Tranche 1, no contract impact. |
| **§6.7 section dividers** | **Added to the ~50 non-test files over 250 lines.** The rule exists because those files are hard to navigate, and they are. Slower than the renames — placing the groups takes judgement — so it lands as its own pass. |

## Conflict resolution, duplicates, emitted leftovers

| Item | Ruling |
|---|---|
| **`Quirks.ErrorEnvelope`** | **Renamed too** — the envelope ruling was about the word, not one declaration site. Field → `ErrorBody`, type → `ErrorBodyShape`, so the pair reads `ErrorBody ErrorBodyShape` rather than field and type being identical. It is the only one of the 38 carrying a killed word, so "keep all 38" holds everywhere else. |
| **the five duplicate declarations** | **Obvious fix applied to each.** `ir.deriver` → `operationIndex`, `audit/plan.deriver` → `planBuilder`. Wire sentinels declared once in `audit/plan`, `run` imports; spellings unchanged. `offlineSignatures` exported from `providergen` as `OfflineToolchainMessages`, `emit`'s test imports it. `unitEndpoint`/`UnitEndpoint` mirror kept (templates must carry finished values) but renamed `mockBaseURL`/`MockBaseURL` with a test asserting they match. `primaryGate` are **not** duplicates: `strategy.primaryGate` keeps the name (it picks); `run.primaryGate` → `gateFieldFor` (it recalls). |
| **emitted leftovers** | **`conditional_validators.go` → `config_validators.go`** (matches the framework's own `ConfigValidators`, which is what the file holds) and **`UnitEndpoint` → `MockBaseURL`**. `ActivateMocks`/`ActivateErrorMocks`/`DeactivateAndReset` **kept** — they wrap `httpmock.Activate` deliberately. `identityModel`, `listConfigModel`, `tfpfgen_run` **kept**. |

## §III.5 A — the four strategy leftovers

| Now | Ruling |
|---|---|
| **`Check.Expect`** | **→ `Probe.ExpectedAnswer`.** `Expect` was a verb standing in for a noun — the field holds what is expected. Values unchanged: `accept` \| `reject` \| `conditional`. *Contract — the strategy artifact, tranche 7.* |
| **`Strategy.Role`** | **→ `Strategy.AuditShape`**, and the value `lookup` → `lookupByKey`, matching the approved *lookup-by-key datasource*. **⚠ This reintroduces `shape`, which was banned as an identifier.** The ban therefore carries an explicit exception: `AuditShape` is the one approved identifier use, recorded in the glossary alongside the ban so it reads as a decision rather than an oversight. |
| **`Budget.Formula`** | **Kept unchanged.** The string genuinely is the formula that produced the number, and it is already committed. *(Its text updates only where the `*Reserve` → `*StepRequests` rename touches it.)* |
| **the `prose*` family** | **Renamed for what it mines → `description*`.** `proseCategory` → `descriptionRuleCategory`; `prosePhrase(s)` → `descriptionPhrase(s)`; `proseHypotheses` → `descriptionClaims`; `extractProse` → `extractDescriptionClaims`; `proseRequiresField` → `descriptionDependsOn`. Names the OpenAPI keyword the text comes from. *The **prose** provenance value is untouched — it stays an approved observation-provenance spelling.* |
| **`withdraw`** | **Kept — the state added to the glossary.** The distinction is real and load-bearing: a *rejection* writes a marker that suppresses re-proposal permanently; a *withdrawal* records nothing, so the observation can be proposed again. The **correction** entry gains a fourth state: proposed / accepted / rejected / withdrawn. The job, the `tfpfgen-withdrawn` label and `docs/contract.md:95` are unchanged. |

---

**Every item in Parts II and III is now ruled.** Part V is authoritative.
