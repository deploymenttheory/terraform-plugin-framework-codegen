# The blueprint

A blueprint is the intermediate representation the generator emits from. It is
JSON, committed, and meant to be read like a code review — which is why it is not
a compact binary format or an in-memory structure.

## Why not HashiCorp's Provider Code Specification

The Provider Code Specification describes a schema. That is genuinely useful and
this project reads and writes it (Phase 3), but it is a fraction of what a working
provider needs. It has no representation for:

- which SDK method a create calls, or what that method returns
- how a field crosses the boundary between Terraform's types and the SDK's
- what the API actually does, as opposed to what its document claims
- timeouts, import semantics, permissions, test fixtures

Those are most of a provider. So the blueprint is a superset, and the official
format is something the toolkit interoperates with rather than its model.

## Three conventions

**Absence is representable everywhere.** Optional scalars are pointers and
optional collections are nil-able, so a layer can say "I have no opinion about
this" distinctly from "this is false". Without that distinction the layered merge
in Phase 4 is not decidable — a probe that observed nothing would be
indistinguishable from a probe that observed `false`.

**JSON keys are camelCase.** The house `.golangci.yml` enables `tagliatelle`,
which defaults to camelCase. A consequence worth knowing: `internal/interop` must
use HashiCorp's Go types *verbatim* rather than redeclaring them, because their
JSON is snake_case and would trip the linter on every field.

**Nothing carries a timestamp, an absolute path or a tool version.** A blueprint
is committed and diffed by CI, so any value that changes without an input changing
would make the drift check useless.

## Layout

One provider block and one file per resource:

```
blueprints/thousandeyes/
  provider.blueprint.json        the provider block: name, module, SDK, conventions
  resources/tag.blueprint.json   one resource
```

`LoadDir` merges them and validates the result **as a whole**. That matters:
cross-resource collisions — two resources sharing a Terraform type, or an import
alias — are invisible to a per-file check and are exactly what stops a provider
from starting.

Exactly one file may carry a provider block. Two would mean the emitter silently
picks one, and which one would depend on filename ordering.

## Provider

| Field | Purpose |
|---|---|
| `name`, `typePrefix` | registry name; prefixes every resource type |
| `goModule` | the generated provider's module path |
| `sdk.dialect` | `restyService` (built) or `kiotaFluent` (reserved, refused) |
| `sdk.clientType`, `sdk.clientImport` | the client field on every resource struct |
| `conventions.*` | output directories and default timeouts |
| `support.*` | the hand-written provider packages generated code calls into |

`support` is data rather than constants because those packages belong to the
provider, not the generator.

## Block kinds

The top level is the **Terraform block kind**, not the Terraform type, and everything
cascades down from it — the same organising principle the framework itself uses. Each
kind is a `Name`, a `Schema`, and then the operations that kind supports.

`resource` and `datasource` are built. `list`, `ephemeral` and `action` have their
kinds declared in `internal/blueprint/blockkind.go` and arrive in later phases.

**An attribute's legal fields depend on the kind rendering it.** Every kind has its own
schema package — `resource/schema`, `datasource/schema` and so on — and those packages
are structurally similar but deliberately not identical:

| field | resource | datasource | ephemeral | action | list |
|---|---|---|---|---|---|
| `computedOptionalRequired` (computed), `sensitive` | ✓ | ✓ | ✓ | ✗ | ✗ |
| `planModifiers`, `default` | ✓ | ✗ | ✗ | ✗ | ✗ |
| `writeOnly` | ✓ | ✗ | ✗ | ✓ | ✗ |

`Validate` refuses a field the target kind has no home for, naming both the attribute
and the kind. Without that refusal the generator emits
`datasourceschema.StringAttribute{Default: ...}` and the failure surfaces as a compiler
error in generated output, which names neither.

An action or list attribute having no `computed` is why a list resource's config schema
is filter-only: there is nowhere to put a result.

## Data source

The same skeleton as a resource, minus everything a data source has no operation for.

```jsonc
{
  "key": "tag",
  "name": "tag",                  // registers as thousandeyes_tag
  "schema":  { "attributes": [ ... ] },
  "binding": { "service": {...}, "read": {...}, "response": {...} },
  "timeouts": { "readSeconds": 180 }
}
```

`binding` is a **`DataSourceBinding`, not a `ResourceBinding`**: it holds a service
reference, one read operation and a response model, and there is no create, update, delete
or request body on the type at all. Modelling it as a resource binding with most fields
refused would put the refusal in a rule somebody has to remember to keep.

Two consequences follow from a data source sending nothing to the API:

- **Its attributes carry only the flatten direction.** An `expand` on a data source
  attribute is refused, because there is no request body for the value to reach. A
  *required* attribute here is a lookup argument or a filter, and it reaches the API as a
  call argument — which is what the `configField` argument kind is for.
- **No `planModifiers` and no `default`.** Neither field exists on
  `datasource/schema`'s attribute types. `Validate` refuses a declared one, and the
  generator does not synthesise the `UseStateForUnknown` it adds for a resource's computed
  strings.

Emitted as four files — `datasource.go`, `model.go`, `read.go`, `state.go` — against a
resource's five. There is no `construct.go` because there is nothing to construct.

## Resource

`key` is the stable merge key. Probe facts and hand-authored overrides join on it,
so it is the one field never to rename casually.

`name` is the type name **without** the provider prefix — `tag`, not
`thousandeyes_tag`. The registry-visible type is composed at render time from
`provider.typePrefix`, exactly as the framework composes it: `Metadata` sets
`req.ProviderTypeName + "_" + Name`. Storing the composed string as well would
denormalise it, and a denormalised field is one that can disagree with itself.

The naming fields — `goPackage`, `goPackageAlias`, `goTypeName`, `modelTypeName` — are
explicit rather than derived. Deriving them would make a rename of the Go type an
invisible consequence of a rename of the Terraform type.

`serviceGroup` and `apiVersionDir` place the package on disk:
`<resourceRoot>/<serviceGroup>/<apiVersionDir>/<goPackage>`.

## Schema

Attributes hang off a `schema` object rather than off the block directly, because the
framework's schema is itself a thing with a `Version`, a description and a deprecation
message — and because that is where an attribute's legal field set is decided.

```jsonc
"schema": {
  "attributes": [ ... ],
  "version": 0,                 // resource schema version, for state upgrades
  "markdownDescription": "..."
}
```

There is no `blocks`. See the note below.

## Attributes

```jsonc
{
  "name": "colour",             // tfsdk name, snake_case
  "goField": "Colour",          // model struct field
  "type": { "kind": "string" },
  "computedOptionalRequired": "computed_optional",
  "wire": { ... }
}
```

`computedOptionalRequired` is spelled exactly as the official specification spells it —
`required`, `optional`, `computed`, `computed_optional` — so interop needs no
mapping table.

**Type kinds.** Scalars: `bool`, `string`, `int32`, `int64`, `float32`, `float64`,
`number`. Collections of scalars: `list`, `set`, `map`, each with `elem`. Nested
objects: `list_nested`, `set_nested`, `single_nested`, each with `nested`.

Nested attributes, not blocks. The choice is permanent for a published provider, so it
is recorded here to stay reviewable — and the evidence is one-sided. In
`deploymenttheory/terraform-provider-microsoft365`, a 167-resource provider,
`schema.ListNestedBlock` appears three times across two files, of which one use is live
and one is dead code; `SetNestedBlock` and `SingleNestedBlock` appear not at all. Against
366 `SingleNestedAttribute`, 178 `ListNestedAttribute` and 110 `SetNestedAttribute`. The
single live block has an empty validator slice and enforces its cardinality by hand in
`modify_plan.go`, which is what a nested attribute would have done for it.

Nesting is supported **one level deep** and refused beyond it, naming the
offending attribute. Each level needs its own model, `attr.Type` map and helper
pair, and a partially-correct nested mapping is the class of bug that surfaces as
a diff a practitioner cannot resolve.

## Wire

How one attribute crosses the boundary.

| Field | Purpose |
|---|---|
| `jsonPath` | the API's own name. The join key for probe facts, so the prober never needs to know Terraform naming. |
| `sdkField`, `sdkGoType` | the SDK model's field and its exact declared type |
| `expand` | Terraform → SDK |
| `flatten` | SDK → Terraform |
| `skipExpand` | a computed field is read, never sent |
| `skipFlatten` | a write-only secret must not be flattened, or state blanks on every read |

A conversion with `returnsError` makes its caller fallible, which propagates:
the helper returns diagnostics, so `constructResource` does, so its CRUD call site
changes. That propagation is computed in `render`, so a resource with only
infallible scalars still gets the simpler signatures.

## Binding

The part the official specification has nothing for. The design goal is that a
call is **data**, never a dialect the emitter branches on — adding an SDK whose
methods look different should be a blueprint change, not an emitter change.

```jsonc
"read": {
  "style": "method",
  "method": "GetTag",
  "args": [{ "kind": "ctx" }, { "kind": "stateField", "field": "ID" }],
  "return": "resultTransportError",
  "resultType": "tags.Tag",
  "httpMethod": "GET",           // not used to build the call; the SDK owns the wire
  "pathTemplate": "/tags/{id}"   // carried for mocks, fixtures and doc comments
}
```

`return` is recorded rather than inferred because it decides the arity of every
error return in the generated body. Getting it wrong produces code that does not
compile — `tfpluginframeworkgen bindings` checks it against the method's real signature.

## Policy

`updateStyle` is **required** when a resource has an update operation, and
validation refuses to let it be absent.

`putFull` clears fields the request omits; `patchMerge` leaves them alone. That is
not a detail: guessing wrong silently erases attributes the practitioner never
mentioned. The ThousandEyes tag endpoint is `PUT`.

## Behaviour

What the API actually does, as opposed to what its document claims: `writable`,
`immutable`, `serverDefault`, `returnedOnRead`, `volatile`, `requiredByApi`.

Populated by the prober in Phase 4. Every field is a pointer, for the reason in
*Absence is representable* above.

The fields exist now and are unused, so that a blueprint written today does not
need reshaping when probing lands.

## Validation

`Validate` reports **every** problem, not the first. Fixing a blueprint one error
per run is miserable and collecting them costs nothing.

Parsing rejects unknown fields. A silently ignored key is the worst failure mode
for a hand-authored document: write `updateStyle` where the schema says
`policy.updateStyle`, see no complaint, and get a provider that clears attributes
it should preserve.

Checks worth knowing about:

- duplicate Terraform types, import aliases and resource keys, across all files
- duplicate model fields — the subtle one, where the schema is fine and the model
  silently loses an attribute
- an ID or import binding naming an attribute that does not exist
- a writable attribute with `skipExpand`, whose value could never reach the API
- a default on a non-computed attribute, which is dead configuration
- a resource with no read operation, which cannot refresh state

## Terminology: whose word for what

The blueprint is a superset of HashiCorp's Provider Code Specification, so it uses **their** word
for anything they have one for. A reviewer who knows `terraform-plugin-codegen-spec` should not have
to learn a second vocabulary to read this repo.

| Concept | Their term | Ours |
|---|---|---|
| whether an attribute is computed, optional, required | `ComputedOptionalRequired` | same |
| the element type of a collection | `ElementType` | same |
| the object shape a nested attribute holds | field `NestedObject`, type `NestedAttributeObject` | same |
| the attribute itself | `Attribute`, `Name`, `Sensitive`, `MarkdownDescription`, `DeprecationMessage`, `Validators`, `PlanModifiers`, `Default` | same |
| the type kind | per-type attributes (`StringAttribute`, `SetNestedAttribute`) | one `AttrType` with a `Kind`, whose values spell theirs (`single_nested`, `set_nested`) |

Spelling is British English, which is this project's own. It is a separate question from
terminology: `ComputedOptionalRequired` is adopted because it is HashiCorp's *word* for the concept,
and `Behaviour` keeps its spelling because the concept is ours and so is the voice.

What stays American is only what an identifier outside this repo fixes, and the list is short enough
to state: the HTTP `Authorization` header and `http.StatusUnauthorized`; the API's own field names
and response headers, including `color` and `x-organization-rate-limit-limit`; and
terraform-plugin-framework's exported types, `schema.NestedAttributeObject` among them. Our verb is
`Authorise`; the header it sets is `Authorization`. Both are correct, for different reasons.

### What is deliberately ours

Four names have no counterpart in their spec, because the concepts do not exist in it. Each is kept,
and the reason belongs next to the name rather than in a reviewer's head.

- **Blueprint**, not `Specification`. Their `Specification` is a schema description; a blueprint is
  that plus everything CRUD generation needs — SDK bindings, wire mappings, observed behaviour. The
  two are not the same document, and calling ours theirs would promise interoperability we do not
  have. `interop export` produces a real `Specification` from a blueprint, and that command is where
  the equivalence is claimed.
- **Behavior**, on an attribute. What a probe observed about the API: writability, immutability,
  requiredness, server defaults. Their spec has nowhere to put an empirical finding.
- **Wire**, on an attribute. How a field is spelled on the wire and in the SDK. Their spec stops at
  the Terraform schema.
- **Fact**, **cassette**, **probe**. The evidence pipeline is entirely ours.

`AttrType` differs structurally as well as in name: their spec models the type by which pointer is
non-nil, and ours by a `Kind` discriminant. That is a deliberate difference — a discriminant is what
lets one code path walk every attribute — and it is the reason `interop` exists as a mapping layer
rather than a cast.
