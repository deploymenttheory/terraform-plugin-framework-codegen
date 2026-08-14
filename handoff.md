# Handoff

Where the work stands, what it actually achieved, and what is left.

Every number here is reproducible — see [Verifying these numbers](#verifying-these-numbers).

---

## Read this first

**All fifteen merged PRs improved *coverage*, not *correctness*.** Coverage is
how much of a document generates at all. Correctness is whether what generates
matches how the API really behaves — which is what `mapping.md` asks for and
what `README.md` calls "the work in progress".

They are not the same thing, and the second has not been started.

**Coverage moved:**

| | start | now |
|---|---|---|
| thousandeyes | 505 | 344 |
| github | 1200 | 937 |
| jamfpro | 470 | 319 |
| **total refusals** | **2175** | **1600 (−26%)** |

**Correctness did not, and is now numerically worse:**

| | start | now |
|---|---|---|
| plain `optional` attributes | 2,221 | **3,216** |
| `computed_optional` attributes | 64 | **65** |

`docs/contract.md:168` states the intent plainly:

> `Optional` alone is the rare one. Most APIs answer with a value for every
> field they accept … emitting it as `Optional` alone gives the practitioner a
> perpetual diff.

It is currently **49× more common** than `computed_optional`. Every attribute
the coverage work recovered is another attribute with the wrong presence, so
the coverage work made this number grow. That is not an argument against the
coverage work — those attributes had to exist before they could be correct —
but it is the reason the headline figure moved the wrong way.

---

## What merged

| PR | What | Measured effect |
|---|---|---|
| #72 | Realign vocabulary to terraform-plugin-codegen-spec; `internal/code` | byte-identical output |
| #73 | One `schemaType` record replacing nine per-type switches | byte-identical; −114 lines |
| #74 | `client_options` block; env prefix `TF_<P>_` → `<P>_` | +2 files/provider |
| #75 | `unsupported.json` — the refusal report | made 2,175 refusals visible |
| #76 | `RequiresReplace` from the create ∖ update body difference | +20 emitted |
| #77 | Stop counting a pruned binding as a loss when the attribute survives | −350 |
| #78 | Bridge `DateOnly`, `[]time.Time`, `[]byte` | −64 |
| #79 | `CLAUDE.md`: comments say what and why | rule only |
| #80 | Resolve SDK runtime types; interfaces that construct themselves | reasons corrected |
| #81 | Bridge `uuid.UUID`, path params parsed with a diagnostic | −42, +8 entities |
| #82 | `additionalProperties` as a typed map, via kiota's `additionalData` bag | −48 |
| #83 | Collapse a union whose branches are all scalars | −17 |
| #84 | 800-line ceiling covers hand-written code only | rule only |
| #85 | Bridge a field the SDK reads and writes as different types | −55, +3 entities |
| #86 | Repair `main` (mangled test file, one over the ceiling) | CI green |

`unsupported.json` (#75) is the instrument the rest depends on. It is
manifest-covered and drift-gated, so a refusal appearing or disappearing shows
up as a line in a generation pull request rather than in a CI log nobody reads.

---

## The objective that has not been started

`mapping.md` lists twelve API behaviours and the terraform-plugin-framework
shape each demands. **Detection** is whether the audit can observe the
behaviour; **expression** is whether the generator can emit the shape.

| # | Behaviour | Detect | Express |
|---|---|---|---|
| 1 | Accepted on write, never returned | ✗ no observation kind | ✗ `WriteOnly` never emitted |
| 2 | Never accepted, always returned | ~ `writable=false`, `serverForced`, `volatile` | ~ Computed yes; `UseStateForUnknown` still only on `id` |
| 3 | Optional in, always returned | ✓ `serverDefault` | ~ presence yes; modifier no |
| 4 | Returned obfuscated (`****`) | ✗ misreads as `serverForced` | ✗ **`Sensitive` never emitted on an entity attribute** |
| 5 | Valid only when a sibling equals a value | ✓ `validWhen` | ~ `ValidateConfig` yes; plan modifier no |
| 6 | Settable at create, refused after | ✓ `immutable` | ~ `RequiresReplace` yes; `…IfConfigured` no |
| 7 | Rejected at create, settable on update | ✗ | ✗ `construct.go` still discards `isCreate` |
| 8 | Echoed back semantically equivalent | ✓ `normalisation` | ✗ no custom type, no `SemanticEquals` |
| 9 | Silently clamped or truncated | ~ enums only | ✗ no constraint validators at all |
| 10 | Omitted → returns `""`/`[]`/`0` | ~ lands as `serverDefault` | ✗ no policy either way |
| 11 | Collection returned in arbitrary order | ✗ | ✗ **`SetAttribute` appears nowhere** |
| 12 | Collection returns server-injected members | ✗ | ✗ |

Verified against current `main`: `Sensitive` 0, `SetAttribute` 0,
`SetNestedAttribute` 0, `SemanticEquals` 0, `RequiresReplaceIfConfigured` 0,
`Default` 0, `DeprecationMessage` 0, `LengthBetween` 0, `RegexMatches` 0,
`int64validator.Between` 0 — counting non-test, non-provider-block occurrences.

`specmodel.Schema` still does not parse `writeOnly`, `nullable`, `deprecated`,
`uniqueItems`, `maxLength`, `minLength`, `maxItems`, `minItems`. Several rows
are blocked on that parse alone.

### The cheapest correctness work, and it needs no credentials

Rows 4, 9 and 11 are partly answerable from the document, offline:

- **46 Jamf Pro properties declare `format: password` or `writeOnly: true`** and
  every one generates as a plain, visible attribute today. That is a
  security-visible defect the spec already tells us about.
- `uniqueItems: true` → `SetAttribute` (row 11). ThousandEyes declares it 28
  times.
- `maximum` / `minimum` / `maxLength` / `minLength` / `pattern` → plan-time
  validators (row 9). ~370 declarations across the three.

### The expensive part, and it needs a live API

Rows 1, 2, 3, 10 and 12 need the audit to have run. **It never has, against any
of the three pilots** — there is no `audit/` directory and no
`spec/corrections/` in any scratch tree. The 3,216 vs 65 figure is waiting on
`x-tfpfgen-server-default`, which only an audit produces.

---

## Outstanding coverage work

Ordered by measured value per unit of effort, which is **not** the original plan
order.

### 1. Path parameter type mismatch — 250 refusals

```
path parameter "hook_id" is string in the schema but int32 in the generated
SDK, and no conversion between them is safe without a parse that can fail
```

The mechanism already exists. #81 added `paramDeclaration`
(`internal/emit/render_mapping.go`), which emits a fallible parse guarded by an
attribute diagnostic:

```go
providerId, providerIdErr := uuid.Parse(data.ProviderID.ValueString())
if providerIdErr != nil {
    resp.Diagnostics.AddAttributeError(path.Root("provider_id"),
        "Invalid providerId", providerIdErr.Error())
    return
}
```

This is the same shape with `strconv.ParseInt`. Every method a declaration lands
in — Create, Read, Update, Delete, Invoke — carries a `resp` with `Diagnostics`
and returns nothing, so it compiles in all of them. **Largest single win left,
and the cheapest.**

### 2. Gap 8 — SDK model lacks the getter, 442 refusals

The biggest family. It could not be characterised honestly until #77 removed the
benign removals from the signal; it can be now. Of an earlier sample, only 24 of
752 had a did-you-mean near match, so these are mostly fields the model
genuinely lacks rather than a naming bug. Needs measuring before designing.

One known defect to fold in: a synthesised `id`'s removal reason is sometimes
taken from a neighbouring field — `attribute "id"` carrying `carries no
GetActorIdEscaped to read "actor_id"`.

### 3. Gap 7 — document declares no response schema, 225 refusals

Not a toolkit defect: the vendor declares a 200 with no schema, so there is
nothing to map into state. Agreed approach is a new audit observation recording
the observed response shape, compiled into a correction that adds the schema.

**Blocked on naming** — see below. Also needs quirkserver ground truth: a shape
whose document declares no response schema while the server returns one.

### 4. Gap 9 — object unions as one attribute per variant, 84 refusals

kiota models an object union as a composed type with **one named accessor per
branch**:

```go
type Commit_Commit_author struct {
    emptyObject EmptyObjectable
    simpleUser  SimpleUserable
}
func (m *Commit_Commit_author) GetSimpleUser() SimpleUserable
```

So the shape that fits is a nested object with one mutually-exclusive
sub-attribute per variant, named from the SDK. This **inverts the usual
derive-then-bind order** — the names come from the SDK, not the document — which
is why it is its own piece of work.

Do not merge the branches into one flat object. That was tried and measured:
GitHub's refusals went 967 → 2172, because the merged fields do not exist on the
composed wrapper.

Keep in reserve: a union at an *entity root* should become two resources. None
of the 84 is at a root — all are nested attributes, some five levels deep — so
splitting today would multiply entities combinatorially (one GitHub datasource
would become 64).

### 5. Gap 2b — file transfer, 13 refusals

#80 fixed the lookups; the modelling is untouched. Agreed shape is `source` and
`content_base64`, mutually exclusive via the existing
`resourcevalidator.Conflicting` machinery, with a computed `content_base64` for
downloads.

Needs five things, which is why it was deferred: `multipart/form-data` parsing
in `specmodel`; an IR flag for "this operation takes a file"; a construct idiom
that is `AddOrReplacePart(name, contentType, content)` rather than field
setters; the two attributes and their validator; and **a request adapter
reachable from the resource** — `MultipartBody.SetRequestAdapter` needs one and
generated services receive `*sdk.APIClient`, not the adapter.

### 6. Smaller families

`object with no declared shape` 140 (the vendor documented nothing — arguably a
vendor-facing report, not codegen work), `SDK lacks the setter` 105,
`read/write mismatch` 89 left after #85, `call shape unsupported` 58, `array
shape` 57, `list element has no id` 48, `fits no kind` 47.

---

## Owed by the repository owner before work can start

`CLAUDE.md` makes every domain term owner-approved. These block their gaps:

- **Gap 7** — the observation kind, its `x-tfpfgen-*` extension key, and whether
  it is eligible for `audit.auto_accept`.
- **Gap 9** — how a variant sub-attribute is named. The SDK supplies a name
  (`GetSimpleUser` → `simple_user`); whether the IR takes it from there or from
  the document's `$ref` is the decision.
- **mapping.md row 1** — `WriteOnly` plus an `..._version` Int64 trigger would be
  the first generated attribute with no wire counterpart. Name and suffix.
- **mapping.md row 8** — `x-tfpfgen-normalisation` and its value set.
  `compile.go:114` already says this is an owner decision.

`mapping.md` itself lives at `~/Desktop/mapping.md` and is **not in the repo**.
It is the specification for all of the correctness work and should be committed.

---

## Still queued

**The comment sweep.** `CLAUDE.md` now requires comments to say what and why and
nothing else. The existing tree has not been swept, and that includes comments
written during #72–#86 — several carry counts from a particular run, which is
exactly what the rule forbids.

**`internal/sdkbind/prune.go` is at 799 lines**, one under the ceiling it is
still held to. The next change to it fails the gate.

---

## Lessons that change how to work on this

**Check what the SDK already decided before designing from the document.**
Three premise failures in one session, all the same shape:

- *Typed maps* — the plan assumed the SDK carries a Go map. kiota emits **zero**
  `map[string]string` across all three SDKs; it generates a model whose only
  field is `additionalData map[string]any`. 2,397 such models exist.
- *Discriminated unions* — the plan assumed documents declare discriminators.
  Twelve sites across three documents, and GitHub, which owns 102 of 103 union
  refusals, has **none**.
- *Merging object unions* — argued for on the grounds that branches need naming
  and reads are ambiguous. Both false: kiota names every branch and exactly one
  accessor is non-nil. Building it made GitHub's refusals worse by 1,205.

In each case a five-minute `grep` of the generated SDK would have prevented
hours. Measure the pilots before designing, not after.

**`postcheck` catches what unit tests do not.** Two bugs in #85 produced
generated Go that did not compile, and neither would have surfaced in the
toolkit's own suite: construction typed a slice from the *getter* while filling
it with the *constructor's* values; and a nested block with no writable children
rendered a loop declaring an index nothing read. The second predates #85 —
entities carrying it were refused whole, so it had never reached a compiler.

**Refusal counts going *up* can be correct.** #80 raised Jamf Pro by 5 and that
was the point: one misleading entity-level refusal became several accurate
field-level ones. Read the reasons, not just the total.

**Rebase rather than merge `main` into these branches.** #83 and #85 were merged
the other way and both broke `main` — a test file joined mid-function so
`internal/emit` would not compile, and a file left 12 lines over the ceiling.
#86 repaired both.

---

## Verifying these numbers

Five local gates, all of which CI also runs. Run them **before** opening a PR —
the hygiene gate and the linter each caught something CI would have:

```sh
gofmt -l internal cmd            # must be empty
bash scripts/repo_hygiene_gate.sh
golangci-lint run
go build ./... && go vet ./...
go test ./...                    # coverage ≥90% total, ≥80% per core package
```

Then the loop that matters, into `/Users/dafyddwatkins/GitHub/terraform/scratch/gen`:

```sh
go build -o …/scratch/bin/tfpfgen ./cmd/tfpfgen
cd …/scratch/gen/<pilot>
tfpfgen provider generate     # postcheck: go mod tidy, go build, go vet
tfpfgen provider verify       # must report no drift
```

`provider generate` prints the refusal total. For the presence census behind the
3,216 / 65 figure:

```sh
tfpfgen provider generate --print-ir > ir.json
# then count computed_optional_required across every attribute tree
```

**The report is the acceptance test.** Each piece of work should name the count
it expects to move, and the before/after `unsupported.json` totals should show
it moved by that much and nothing else changed. That is a stronger gate than any
unit test here, because it measures three real documents.
