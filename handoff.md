# Handoff

Where the work stands, what it actually achieved, and what is left.

Every number here is reproducible — see [Verifying these numbers](#verifying-these-numbers).
Counts of what the toolkit emits and refuses are not repeated here;
`docs/emittance_tracker.md` is the one place they live.

---

## Read this first

The distinction this document turns on: **coverage** is how much of a document
generates at all. **Correctness** is whether what generates matches how the API
really behaves — what `docs/mapping.md` specifies and what `README.md` calls
"the work in progress". They are not the same thing.

**Correctness work has now started, and the offline half is largely done.**
Constraint keywords parse, bounds and patterns become validators, `format:
password` and `writeOnly` become `Sensitive`, `deprecated` becomes a
`DeprecationMessage`, and a documented default fills the response. Every one of
those was zero when this document was first written.

**The half that needs a live API has not started, and cannot yet.** No audit has
ever run against any of the three pilots: all three revised specs contain zero
`x-tfpfgen-*` extensions, and no scratch tree has an `audit/` or
`spec/corrections/` directory. Every shape that depends on an observation —
server defaults, immutability, normalisation, conditional validity — is
unexercised, and the generated config validators along with them.

### The presence census

The measure of the remaining gap. `docs/contract.md` states the intent:

> `Optional` alone is the rare one. Most APIs answer with a value for every
> field they accept … emitting it as `Optional` alone gives the practitioner a
> perpetual diff.

Counting `computed_optional_required` across every attribute tree of all three
pilots, at `30b23ac` on 2026-08-25:

| | optional | computed_optional | ratio |
|---|---|---|---|
| when first measured | 2,221 | 64 | 35× |
| after the coverage work | 3,216 | 65 | 49× |
| now | 10,574 | 670 | **15.8×** |

The absolute figures are not comparable across rows — union variants brought
each branch's own fields into the tree, so there are far more attributes to
count than there were. The ratio is the figure that means something, and it
moved the right way for the first time: `x-tfpfgen-server-default` is still the
thing that would close it, and only an audit produces one.

---

## What merged

`#72`–`#86` are the coverage work, described in the git history. Since then:

| PR | What |
|---|---|
| #87 | `docs/mapping.md` committed — the specification for all correctness work |
| #88 | Integer path parameters parsed with a diagnostic |
| #89 | List-resource test content type |
| #90 | List-resource addressing schema |
| #91 | Schema constraint keywords parse (`maxLength`, `uniqueItems`, `minimum`, …) |
| #92 | Update the ThousandEyes test document |
| #93 | `Sensitive` and `DeprecationMessage` emitted |
| #94 | Constraint validators emitted from declared bounds |
| #95 | Point the GitHub test document at an immutable ref |
| #96 | A documented default fills the response |
| #97 | `prune.go` decomposed |
| #98 | Comment sweep |
| #99 | A list resource requires a resource to match |
| #100 | Resource identity schema |
| #101 | Fixtures respect `format` |
| #102 | Generated tests assert what the provider actually produces |
| #103 | `docs/mapping.md` gains the entity operation sets |
| #104 | A lone write is an invocation, whichever method spells it |
| #105 | A datasource filters on the fields of the objects it lists |
| #106 | A filter survives only where the field it selects on does |
| #107 | `docs/emittance_tracker.md` — counts live in one file |
| #108 | A list result names the object by the key the resource is addressed by |
| #109 | A union becomes one attribute per variant where nothing writes it |
| #110 | `CLAUDE.md` and `README.md` restated against the tree they describe |
| #112 | Update the ThousandEyes test document again |
| #113 | The test documents named `vendor_openapi_specs` rather than *corpus* |
| #114 | Those documents committed and embedded, retiring the fetch-and-pin scheme |

---

## Where correctness stands

`docs/mapping.md` lists thirteen API behaviours and the shape each demands.
**Detection** is whether the audit can observe the behaviour; **expression** is
whether the generator can emit the shape. Measured in the generated trees, not
in this repo's source — an emitter builds most of what it emits.

| # | Behaviour | Detect | Express |
|---|---|---|---|
| 1 | Accepted on write, never returned | ✗ no observation kind | ✗ `WriteOnly` never emitted |
| 2 | Never accepted, always returned | ~ `writable=false`, `serverForced`, `volatile` | ~ Computed yes; `UseStateForUnknown` still only on `id` |
| 3 | Optional in, always returned | ✓ `serverDefault` | ~ a *documented* default now fills the response; an observed one needs the audit |
| 4 | Returned obfuscated (`****`) | ✗ misreads as `serverForced` | ✓ `Sensitive` emitted from `format: password` / `writeOnly` |
| 5 | Valid only when a sibling equals a value | ✓ `validWhen` | ~ emitter exists; **no tree contains one**, because no audit has run |
| 6 | Settable at create, refused after | ✓ `immutable` | ~ `RequiresReplace` yes; `…IfConfigured` no |
| 7 | Rejected at create, settable on update | ✗ | ✗ `construct.go` still discards `isCreate` |
| 8 | Echoed back semantically equivalent | ✓ `normalisation` | ✗ no custom type, no `SemanticEquals` |
| 9 | Silently clamped or truncated | ~ enums only | ✓ bounds, lengths and patterns become validators |
| 10 | Omitted → returns `""`/`[]`/`0` | ~ lands as `serverDefault` | ✗ no policy either way |
| 11 | Collection returned in arbitrary order | ✗ | ✗ `uniqueItems` now parses, but `SetAttribute` is never emitted |
| 12 | Collection returns server-injected members | ✗ | ✗ |
| 13 | Field carries one of several object shapes | n/a structural | ~ one attribute per variant where nothing writes it; a writable union is refused |

Emitted symbol counts across the three trees at `30b23ac`, 2026-08-25, every
one of which was zero when this document was first written:
`Sensitive` 53, `DeprecationMessage` 171, `int64validator.Between` 180,
`UTF8LengthBetween` 42, `LengthAtLeast`/`LengthAtMost` 138, `RegexMatches` 21,
`Default` 18.

Still zero, and each is a row above: `WriteOnly`, `SetAttribute`,
`SetNestedAttribute`, `SemanticEquals`, `RequiresReplaceIfConfigured`, and any
config validator at all.

`specmodel.Schema` now parses `writeOnly`, `deprecated`, `uniqueItems`,
`maxLength`, `minLength`, `maxItems` and `minItems`. Only `nullable` remains
unparsed.

---

## Outstanding coverage work

Refusals grouped by what the reason says, across all three pilots. The stage
split and the totals are in `docs/emittance_tracker.md`.

| Family | Share | Note |
|---|---|---|
| SDK model lacks the accessor | 729 | The biggest by far, and the one to characterise next. Mostly fields the generated model genuinely lacks rather than a naming bug. |
| Object with no declared shape | 139 | `additionalProperties: true`, or neither properties nor `additionalProperties`. The vendor documented nothing — arguably a vendor-facing report rather than codegen work. |
| Singleton at a fixed path with no operation set | 125 | One object at a fixed path that fits no operation set. |
| Collection shape unsupported | 105 | Arrays of arrays, maps of objects. |
| Read/write type mismatch | 89 | What survives after #85. |
| Nothing survives to read back or send | 88 | Every field of the entity was refused, so the entity goes too. |
| Terraform reserved name at a schema root | 17 | |

Two families from the previous handoff have all but closed: the path-parameter
type mismatch is down from 250 refusals to 1 (#88), and union refusals from 90
to 13 (#109) — eleven of those thirteen a branch referencing no component.

### Gap 2b — file transfer

`multipart/form-data`, untouched. Agreed shape is `source` and
`content_base64`, mutually exclusive via `resourcevalidator.Conflicting`, with a
computed `content_base64` for downloads. Needs five things, which is why it was
deferred: `multipart/form-data` parsing in `specmodel`; an IR flag for "this
operation takes a file"; a construct idiom that is `AddOrReplacePart(name,
contentType, content)` rather than field setters; the two attributes and their
validator; and **a request adapter reachable from the resource** —
`MultipartBody.SetRequestAdapter` needs one and generated services receive
`*sdk.APIClient`, not the adapter.

---

## Owed by the repository owner before work can start

`CLAUDE.md` makes every domain term owner-approved. These block their gaps:

- **The undeclared-response-schema observation** — its kind, its `x-tfpfgen-*`
  key, and whether it is eligible for `audit.auto_accept`. The vendor declares a
  200 with no schema, so there is nothing to map into state; the agreed approach
  is an observation recording the shape the API actually returned. Also needs
  quirkserver ground truth: a shape whose document declares no response schema
  while the server returns one.
- **`mapping.md` row 1** — `WriteOnly` plus an `..._version` Int64 trigger would
  be the first generated attribute with no wire counterpart. Name and suffix.
- **`mapping.md` row 8** — `x-tfpfgen-normalisation` and its value set.
  `internal/spec/revise/compile.go:116` still refuses for want of it.

Settled since this list was written: variant sub-attribute naming (#109, now in
the glossary as **variant attribute**), and `docs/mapping.md` is committed.

---

## Lessons that change how to work on this

**Check what the SDK already decided before designing from the document.**
Three premise failures in one session, all the same shape:

- *Typed maps* — the plan assumed the SDK carries a Go map. kiota emits no
  `map[string]string` at all; it generates a model whose only field is
  `additionalData map[string]any`.
- *Discriminated unions* — the plan assumed documents declare discriminators.
  GitHub, which owned almost every union refusal, declares none.
- *Merging object unions* — argued for on the grounds that branches need naming
  and reads are ambiguous. Both false: kiota names every branch and exactly one
  accessor is non-nil. Building it made GitHub's refusals sharply worse.

In each case a five-minute `grep` of the generated SDK would have prevented
hours. Measure the pilots before designing, not after.

**Measure at the layer the claim is about.** An earlier revision of this file
reported ten framework symbols as never emitted. Four of them were being emitted
at the time. The census had been taken by grepping this repo for the symbol, and
`internal/emit/render_constraints.go` spells a pair of bounds as
`fmt.Sprintf("%sBetween(%v, %v)", …)` — so the string never appears here and
always appears in the output. Grep the generated tree.

**`postcheck` catches what unit tests do not.** Two bugs in #85 produced
generated Go that did not compile, and neither would have surfaced in the
toolkit's own suite: construction typed a slice from the *getter* while filling
it with the *constructor's* values; and a nested block with no writable children
rendered a loop declaring an index nothing read.

**Refusal counts going *up* can be correct.** One misleading entity-level
refusal becoming several accurate field-level ones raises the total and improves
the toolkit. Read the reasons, not just the total.

**Rebase rather than merge `main` into these branches.** #83 and #85 were merged
the other way and both broke `main`.

---

## Verifying these numbers

Five local gates, all of which CI also runs. `make check` is the first four:

```sh
make check                       # fmt, build, vet, coverage, hygiene
golangci-lint run                # make check leaves this to CI
```

Then the loop that matters, into `/Users/dafyddwatkins/GitHub/terraform/scratch/gen`:

```sh
go build -o …/scratch/bin/tfpfgen ./cmd/tfpfgen
cd …/scratch/gen/<pilot>
tfpfgen provider generate     # postcheck: go mod tidy, go build, go vet
tfpfgen provider verify       # must report no drift
```

The emitted-symbol census, which is a fact about the generated tree:

```sh
grep -rc 'Sensitive:' <pilot>/internal/services | grep -v ':0$'
```

The presence census:

```sh
tfpfgen provider generate --print-ir > ir.json
# count computed_optional_required across every attribute tree
```

**The report is the acceptance test.** Each piece of work should name the count
it expects to move, and the before/after `unsupported.json` totals should show
it moved by that much and nothing else changed. That is a stronger gate than any
unit test here, because it measures three real documents.
