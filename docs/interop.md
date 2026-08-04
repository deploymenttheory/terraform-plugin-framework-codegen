# Provider Code Specification interop

`internal/interop` reads and writes HashiCorp's [Provider Code Specification][spec]
v0.1. `tfpluginframeworkgen interop export` projects a blueprint onto that format;
`interop import` reads one back as a draft.

[spec]: https://developer.hashicorp.com/terraform/plugin/code-generation/specification

## Why this exists

**Nothing in this toolkit's pipeline consumes the exported document.** The pipeline
runs blueprint → Go. The official format sits outside it, and no stage reads it.

The value is as a **conformance oracle**. Exporting the pilot blueprint and validating
it against HashiCorp's own embedded JSON schema — then feeding it to their real
`tfplugingen-framework` — is the only check that the blueprint's schema slice
describes a schema Terraform can actually have, performed by a party that does not
share this repository's assumptions about what a valid schema is. Every other test
here is us agreeing with ourselves.

That is a real but modest return, and it is worth stating plainly rather than dressing
up as interoperability with a consumer that does not exist. If the committed export
and its CI job were deleted in a year because nobody had looked at either, that would
be a correct decision. The code is arranged so that removal is one directory and one
workflow job.

The half that has not earned its keep is **import**, not export. Export is cheap — the
blueprint is a superset, so it is a projection plus a report — and it buys the oracle.
Import needs naming synthesis, the draft mechanism, block conversion and four
refusals, and what it buys is a *worse* starting point than `ingest` already produces
from the same OpenAPI document, for any API where somebody has published a
codegen-spec. That set is currently empty. It is built narrowly for that reason:
resources only, no data sources, no provider configuration schema.

## The version string is `"0.1"`, not `"0.1.0"`

Upstream's validator switches on the document's version **exactly**:

```go
switch versionedDocument.Version {
case Version0_1: // "0.1"
    schemaVersion = JSONSchemaVersion0_1
default:
    return fmt.Errorf("version: %q is unsupported", versionedDocument.Version)
}
```

The published documentation shows `0.1.0`, which upstream rejects. Anybody writing the
version by hand from the docs produces a document no tool will read, and the failure
arrives as a bare "version is unsupported" a long way from the cause.

`interop.SpecVersion` is taken from `spec.Version0_1` rather than written out, and
`TestUnit_Interop_Version` asserts all four halves of this: the constant's value, that
our output declares it, that upstream parses our output, and that upstream **rejects**
the same document with `0.1.0` substituted.

## Upstream types are used verbatim

`internal/interop` never redeclares HashiCorp's structs. The official JSON is
snake_case and the house `.golangci.yml` enables `tagliatelle` with a camelCase
default, so a local redeclaration would need a linter exclusion on every field — and
would give two definitions to drift apart. Using their types directly means there are
no local tags to lint.

The cost is that the mapping code is verbose: the format declares its attributes three
times over, once each in the `resource`, `datasource` and `provider` packages, as a fat
struct with one pointer field per type. `prepared.go` absorbs that — it holds every
converted sub-value from the shared `schema` package, and the per-package assemblers do
nothing but choose which pointer field to set. It is the same division the toolkit
draws between `internal/render` and `internal/templates`.

## What the format cannot carry

Everything below is reported, never dropped in silence. Severities are assigned by a
table in `note.go` rather than by judgement at each call site, so the whole loss
vocabulary reads in one screen and `TestUnit_Interop_Severities` can assert it is
total — a new IR field with no entry fails that test.

| Severity | Meaning |
|---|---|
| `info` | No counterpart, but nothing is at risk: provenance only, mechanically re-derivable, or the content crossed verbatim into a field with a different declared contract. |
| `lossy` | Crossed in a coarsened form a consumer cannot distinguish from the original. |
| `dropped` | Carried nowhere. This is the level that says the export cannot be turned back into something emittable. |

**Dropped:** CRUD bindings (`binding`), wire bindings (`wire`), observed behaviour
(`behaviour`), update style, read-back, delete semantics, import policy, timeouts, the
SDK module.

**Lossy:** `int32` → `int64` and `float32` → `float64`; a block converted to a nested
attribute on import; plan modifiers and defaults on a data-source attribute, which the
format has no field for.

**Info:** generated Go identifiers, the model field name, the type prefix, layout
conventions, specification provenance, and attribute descriptions — see below.

Uniform losses are aggregated per resource with a count; selective ones stay addressed
per attribute. Wire bindings and model field names exist on *every* attribute, so
reporting them individually made even the single-resource pilot's report sixty-four
lines of which forty-five were identical — and the pilot now exports twenty-three
resources. Aggregating gives one line per distinct loss.

### Attribute descriptions

The format has **no attribute-level markdown description** — only `description`,
documented as plain text. So a blueprint's `markdownDescription` is written there
verbatim.

Nothing is lost: the text crosses byte-for-byte and a round trip recovers it exactly,
which is why this is `info` rather than `lossy`. What differs is the field's declared
contract, and that is worth saying once per resource. Resource *schemas* do carry both
`description` and `markdown_description`; only `markdown_description` is written, since
every consumer that renders one falls back to the other.

### Coarsening is right, refusing would not be

`int32` widens to `int64` rather than failing. An `int64` schema accepts everything an
`int32` schema does, so the exported document is weaker but not wrong, and refusing
would make a perfectly legal blueprint inexportable — which would defeat the point of
having an export at all.

The pilot uses neither `int32` nor `float32`, so **a lossy note on the pilot means a
mapping regressed**. `TestUnit_CLI_Interop_PilotHasNoLossyNotes` pins that; the failure
is otherwise invisible, because the export would still be valid and still round-trip.

## Static defaults

The blueprint stores a default as the Go literal the emitter will write (`"devices"`,
`0`, `false`); the official format wants a typed JSON value. Export parses, import
renders back.

Two refusals live here, both returning `ErrUnrepresentable`:

- **A kind whose upstream default type has no `Static` field.** Only `bool`, `string`,
  `int64` and `float64` carry one. `NumberDefault` and every collection and nested
  default carry a custom definition only.
- **A `Raw` value that is not a Go literal** — a bare identifier, or a qualified
  constant like `pkg.Devices`.

Neither is silently rerouted into `Custom`. `Custom` expects a full expression such as
`stringdefault.StaticString("x")`, so putting a bare literal there would generate Go
that does not compile.

One asymmetry is accepted: a raw string literal (`` `devices` ``) comes back as an
interpreted one (`"devices"`), because the format carries the value and not the syntax.

## Importing produces a draft

An imported blueprint has a schema and no bindings, so it cannot be emitted. Running
one through `blueprint.Validate` produces a correct message per missing field — for the
pilot, **fifty-three problems** that collectively say "this is broken" rather than
"this came from a schema-only source and needs its bindings authored".

So `interop import` writes `*.blueprint.draft.json`:

```go
const DraftExt = ".blueprint.draft.json"
```

`blueprint.findBlueprints` matches names ending in `blueprint.Ext`
(`.blueprint.json`), and `.blueprint.draft.json` does not have that suffix. **So
`LoadDir`, `emit` and `verify` cannot see a draft at all.** That is the whole
mechanism: an incomplete blueprint is not something the pipeline tolerates, it is
something the pipeline cannot open. Promoting one is a rename — a git-visible,
reviewable act.

```
$ tfpluginframeworkgen emit -blueprint drafts/ -out provider/
no blueprint found under drafts/ (expected files named *.blueprint.json)
```

Instead of the fifty-three messages, `interop import` prints what to author,
collapsed:

```
2 draft(s) written. 7 field group(s) must be authored before emission:

  provider.sdk.{dialect,modulePath,clientType}
  resources[tag].binding.service.{importPath,typeName,accessor}
  resources[tag].binding.{create,read,update,delete}
  resources[tag].binding.body.{requestType,responseType}
  resources[tag].binding.id.fromCreate
  resources[tag].policy.updateStyle
  resources[tag].attributes[*].wire.{sdkField,sdkGoType,expand,flatten}   (23)
```

### Rejected alternatives

- **A `Draft bool` on the blueprint, with `Validate` softening binding errors.** One
  field, but it creates a first-class "incomplete blueprint" state, and within two
  releases somebody makes `emit` tolerate it.
- **Writing a normal `.blueprint.json` and letting `emit` fail.** The file is then
  loadable by nothing — not even `blueprint validate` — so it is a trap that reports as
  fifty-three unrelated errors.
- **Setting `drop: true`.** The worst of the three: `Validate` *skips* dropped
  resources, so the blueprint would pass CI cleanly while emitting nothing at all.

### Names are derived, and not singularised

`ToBlueprint` synthesises the Go identifiers the document does not carry, using
`internal/naming`. Deliberately without singularisation: the pilot's hand-authored name
for the object inside `assignments` is `TagAssignmentModel`, and the derivation
produces `AssignmentsModel`. It says so in a note for a human to shorten. English
pluralisation in a code generator is a bug factory — "statuses", "analyses", "children"
— and the note costs one line where a wrong guess costs a rename nobody notices is
needed.

`associated_external_type` is the one upstream field carrying part of the blueprint's
binding, so a nested object's SDK struct **does** survive a round trip rather than
needing to be retyped. It is deliberately not extended to scalar attributes'
`wire.sdkGoType`: valid but semantically empty, and it would break other tools'
round-trip expectations for a field that is re-derivable anyway.

## Refusals on import

| Input | Why |
|---|---|
| `map_nested` | The blueprint has no map-nested kind. Converting to a list would change the configuration a practitioner writes, which is not an import's decision. |
| `dynamic` | No blueprint counterpart. `tfplugingen-framework` v0.4.1 never implemented it either. |
| `object` attribute | An object is a single value with typed fields and no per-field presence, documentation or validators. `single_nested` is close but not equivalent, and upgrading silently would invent a schema the document did not describe. |
| An object element type | The blueprint's nested kinds live on the attribute, not in an element type. |
| A `custom_type` | Reported as `dropped`; the blueprint has no field for it. |

Blocks are **not** refused. A block and a nested attribute are the same data with
different HCL syntax, so refusing would make hand-authored specifications
un-ingestible; they are converted and reported `lossy`, because the choice is permanent
once a provider is published. HashiCorp's own OpenAPI generator emits no blocks, so this
only fires on hand-written input.

## Verification

```bash
# Round-trip and conformance, hermetic.
go test ./internal/interop/... -cover

# The committed export must match the blueprints.
go run ./cmd/tfpluginframeworkgen interop export \
  -blueprint blueprints/thousandeyes \
  -out interop-specs/thousandeyes/provider-code-spec.json
git diff --quiet -- interop-specs/

# Strict fails on the pilot, because its CRUD bindings cannot cross.
go run ./cmd/tfpluginframeworkgen interop export -blueprint blueprints/thousandeyes -strict; echo $?   # 1

# The third-party parse: the reason this package exists.
go install github.com/hashicorp/terraform-plugin-codegen-framework/cmd/tfplugingen-framework@v0.4.1
tfplugingen-framework generate resources \
  --input interop-specs/thousandeyes/provider-code-spec.json --output /tmp/gen

# Import, and confirm the drafts are invisible to the pipeline.
go run ./cmd/tfpluginframeworkgen interop import \
  -spec interop-specs/thousandeyes/provider-code-spec.json \
  -provider thousandeyes -api-version-dir v7 -service-group tags -out /tmp/drafts
go run ./cmd/tfpluginframeworkgen emit -blueprint /tmp/drafts -out /tmp/out   # expect: no blueprint found
```

The `tfplugingen-framework` job in `codegen-verify.yml` **blocks** rather than warns. A
check that cannot fail the build goes red within a month and is ignored within two, at
which point it is a standing lie about the state of the export. Blocking is safe because
the input is fixed — the committed document, not a freshly inferred one — and the tool
is pinned, so the only thing that can break it is a change to our own mapping, which is
precisely what wants guarding.

## Exit codes

No new numbers; 3–7 stay reserved for `probe`.

| Situation | Code |
|---|---|
| Success, including a heavily downgraded export | 0 |
| `-strict` with any `lossy` or `dropped` note; `ErrUnrepresentable`; a document with no resources | 1 |
| A missing or malformed flag; input that fails `spec.Validate` or declares an unknown version | 2 |
