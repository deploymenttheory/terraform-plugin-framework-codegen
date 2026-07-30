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

## Action

The simplest block kind, because an action has nothing to reconcile: it reads configuration,
calls the API once and reports what happened. `InvokeRequest` carries only a config and
`InvokeResponse` has no field for a result.

```jsonc
{
  "key": "disable_endpoint_agent",
  "name": "disable_endpoint_agent",
  "schema":  { "attributes": [ { "name": "agent_id", ... } ] },
  "binding": { "service": {...}, "invoke": { "method": "DisableEndpointAgent", ... } }
}
```

Three files, against a resource's five: `action.go`, `model.go`, `invoke.go`. No
`construct.go`, because an action sends its arguments as **call parameters** rather than a
request body; no `state.go`, because it writes nothing back.

That absence drives the per-kind rules, and correcting them was what generating the first
real action forced:

- **An action does not expand.** `Expands()` returned true for it on the reasoning that an
  action sends values to the API — it does, but as arguments, and there is no construct
  function for an expand to build into. It is now resource-only.
- **An action does not flatten.** Requiring a flatten of every kind — "every kind reads" —
  made the first action unrepresentable. A flatten *on* an action attribute is now refused,
  because it would convert into somewhere that does not exist.
- **No `computed`, no `sensitive`, no plan modifiers, no default.** There is no state for a
  computed value to live in and no stored value to mark sensitive. `BlockAction` has encoded
  this since format 3; the action is simply the first kind to exercise it.

The deadline is a generated constant with no timeouts block to override it, because the
framework's action schema has no home for one.

## Identity, and the list facet

Both are **facets of a resource**, not sibling blocks, and that is not a modelling
preference — the framework makes them so.

```jsonc
"identity": {
  "goTypeName": "TagResourceIdentity",
  "attributes": [
    { "name": "id", "goField": "ID", "kind": "string",
      "requiredForImport": true, "fromAttribute": "id" }
  ]
},
"list": {
  "goTypeName": "TagListResource",
  "read": { "method": "GetTags", ... },
  "collectionField": "Tags",
  "identityFrom": [ { "goField": "ID", "fromSdkField": "ID", "isPointer": true } ],
  "displayNameFrom": "Key"
}
```

**Identity** renders an `IdentitySchema` method, an identity model of plain Go scalars, and
the copy that fills it from the resource model. From the model rather than the API: the
values are already in state, so a request to learn what Terraform is holding is one that can
fail for no reason.

Create, Read *and* Update all set it. That is not decoration — `fwserver` refuses a resource
that declares an identity schema and returns none (*"Missing Resource Identity After
Create"*), and Read and Update additionally refuse an identity that differs from the one
Terraform holds. A version that declared the schema without setting it compiled perfectly
and would have failed on every apply.

`IdentityAttribute` is deliberately **not** `Attribute`: an identity attribute has no
presence, no validators, no plan modifiers and no default, and `identityschema` gives it
`requiredForImport`/`optionalForImport` instead. Exactly one of those two must be set —
neither means the attribute can never be supplied, both is a contradiction the framework
rejects at runtime.

**The list facet** backs `terraform query`, and its purpose is discovery and import rather
than data retrieval: the results feed `terraform plan -generate-config-out`. So only
`Identity` and `DisplayName` are populated, never `Resource` — reading whole objects would
cost a request per element for data the query does not use.

Three properties the framework imposes, and which being a facet makes unrepresentable-if-
violated rather than validated afterwards:

- **The type name must equal the resource's.** That string is the entire linkage between the
  two — there is no import from one package to the other — and the framework returns an
  error diagnostic from `GetMetadata` if they differ. Both read the same `ResourceName`.
- **The resource must declare an identity.** `ListResult.Identity` is mandatory, so a list
  facet on a resource without one is refused, naming both halves.
- **`Metadata` and `Configure` take `resource.*` request types**, not list-specific ones. The
  framework declares them that way so one implementation can satisfy both interfaces, and
  reaching for `list.MetadataRequest` is the mistake the shape invites. A test asserts the
  generated signatures.

A list config schema is **filter-only**: a list attribute cannot be `Computed`, because there
is nowhere in a query to put a result. Validation also refuses a facet that leaves any
identity field unfilled — a partly-filled identity records an address that does not resolve.

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

### Values, declared and observed

An attribute's value set appears twice, and the pair is the same shape the IR already uses
for `computedOptionalRequired` against `behaviour.requiredByApi`, and `default` against
`behaviour.serverDefault`:

| field | meaning |
|---|---|
| `type.allowedValues` | what the **specification documents** |
| `behaviour.acceptedValues` | the documented values this API **took** |
| `behaviour.rejectedValues` | the documented values this API **refused** |
| `behaviour.valuesClosed` | whether values from *outside* the documented set were refused |

**A generated `OneOf` comes from `allowedValues`**, the declared set — which reads backwards
until you consider which way each errs. The documented set is a superset of what any single
tenant accepts, so a validator built from it errs toward *permitting*: a stale specification
surfaces as a real API error carrying the API's own message. Built from `acceptedValues` it
would err toward *blocking*, and the pilot is the case in point — `access_type` documents
`system`, this sandbox refused it, and another licence may well allow it.

So a documented value the API refused stays permitted, with the refusal named in a comment
beside the validator. The reader of the schema meets the staleness; the practitioner is not
blocked by it.

### Declared bounds

A type's `constraints` become the framework validator its kind provides:

| declared | on | generated |
|---|---|---|
| `pattern` | string | `stringvalidator.RegexMatches` + a package-level `regexp.MustCompile` var |
| `minLength` / `maxLength` | string | `LengthAtLeast` / `AtMost` / `Between` |
| `minItems` / `maxItems` | list, set, map, nested collection | `SizeAtLeast` / `AtMost` / `Between` |
| `minimum` / `maximum` | int32, int64, float32, float64 | `AtLeast` / `AtMost` / `Between` in that kind's package |

Both bounds present becomes `Between` rather than two validators, so one mistake reports one
diagnostic. Constraints live on `AttrType` rather than `Attribute` because they are properties
of the type: a collection's **element type carries its own**, and those are lifted onto the
collection as `ValueStringsAre(...)` — a bound on a set's elements is not a bound on the set.

Three refusals, each because the framework has no validator to generate:

- A bound on a kind that has none — a `pattern` on a number, a length on a collection.
- **`minimum`/`maximum` on `number`.** `numbervalidator` exists but carries only
  `AtLeastOneOf`, so an arbitrary-precision number has no range validator at all. The message
  says to narrow the attribute to an int or float kind.
- A range nothing can satisfy, such as `minLength` above `maxLength`.

Two things worth knowing about the numbers. JSON Schema states every bound as a number, so an
`int64` attribute's minimum arrives as a `float64` and is **narrowed** — emitting it as a float
would not compile against `int64validator`. And a whole-number float bound keeps its decimal
point, so the literal's type is unambiguous where it reaches a float validator.

Unlike allowed values there is **no observed counterpart**: the prober has no protocol for
discovering a length limit or a numeric range, so there is no evidence that could suppress a
constraint validator. Only the computed rule below applies.

`ingest` reads these from the specification, and refuses a `pattern` Go's `regexp` cannot
compile — reported by name rather than passed on, because the generated code calls
`regexp.MustCompile` and an expression RE2 rejects would panic when the provider starts. JSON
Schema permits ECMA constructs, lookahead most often, that RE2 does not.

Three things suppress the validator:

- **`valuesClosed` is `false`.** The only case with direct evidence of harm — the API
  accepted a value from outside the documented set, so a `OneOf` would reject configurations
  it demonstrably takes. `valuesClosed` being *absent* is not the same thing: an unprobed
  attribute keeps its validator, or ingesting a specification and emitting from it straight
  away would produce none at all.
- **A purely `computed` attribute.** A validator runs against configuration, and a computed
  attribute is never configured, so one there could never run. `computed_optional` still
  gets it.
- **A collection.** Constraining elements needs the element-level wrapper, which is a
  different shape rather than a string validator applied to a set.

## Attributes, continued

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

**Nesting is generated to whatever depth the blueprint declares.** Four levels is routine
in the reference provider. Each level gets its own model struct, `attr.Type` map, object
type var and conversion helper pair, and an enclosing level refers to the one below it
through that object type var rather than restating its shape.

Two things are refused rather than half-emitted:

- **Two nested objects that would declare the same Go identifier.** Every nested object
  contributes a package-level model, `attr.Type` map, object type var and helper pair, so
  a repeat is a redeclaration error in the generated package. The refusal names both
  attributes; the compiler error would name neither.
- **Nesting past ten levels.** That is a runaway guard rather than a design limit — above
  any fixed schema in the reference provider. What exceeds it is a schema whose depth is
  decided at runtime from the practitioner's own configuration, which this IR cannot
  express at all, so the message says to write that resource by hand.

### Where an inferred nested object's names come from

`ingest` infers nested objects, so the five generated identifiers are derived rather than
authored. The rule, for a schema `Assignment` inside resource `tag`:

| field | value | rule |
|---|---|---|
| `goTypeName` | `TagAssignmentModel` | resource stem + schema name + `Model` |
| `sdkType` | `tags.Assignment` | SDK package + schema name |
| `attrTypesVar` | `tagAssignmentAttrTypes` | the same stem, lowerCamel |
| `objectTypeVar` | `tagAssignmentObjectType` | ditto |
| `expandFunc` / `flattenFunc` | `expandTagAssignments` | pluralised for a collection |

A schema already carrying the resource's name keeps it rather than doubling it: `TagFilter`
becomes `TagFilterModel`, not `TagTagFilterModel`. Collisions are resolved with a numeric
suffix at the naming layer, before anything is rendered, because render refuses two nested
objects that would declare the same identifier.

These names are a starting point. They happen to match the pilot's hand-curated blueprint
exactly, which is the evidence that the rule is the one a person reaches for — but renaming
them is expected, and is the same thing curation does to every other inferred name.

**A schema that contains itself is refused whole**, naming the schema. Not just at the
cycle point: refusing only the recursive field would leave the enclosing object in place
minus its recursive dimension — a `tree` attribute offering a label and no children, which
looks usable and cannot express the shape it is named for.

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
