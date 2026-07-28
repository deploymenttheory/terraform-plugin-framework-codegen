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

## Resource

`key` is the stable merge key. Probe facts and hand-authored overrides join on it,
so it is the one field never to rename casually.

The naming fields — `terraformType`, `goPackage`, `goPackageAlias`, `goTypeName`,
`modelTypeName` — are explicit rather than derived. Deriving them would make a
rename of the Go type an invisible consequence of a rename of the Terraform type.

`serviceGroup` and `apiVersionDir` place the package on disk:
`<resourceRoot>/<serviceGroup>/<apiVersionDir>/<goPackage>`.

## Attributes

```jsonc
{
  "name": "color",              // tfsdk name, snake_case
  "goField": "Color",           // model struct field
  "type": { "kind": "string" },
  "presence": "computed_optional",
  "wire": { ... }
}
```

`presence` is spelled exactly as the official specification spells it —
`required`, `optional`, `computed`, `computed_optional` — so interop needs no
mapping table.

**Type kinds.** Scalars: `bool`, `string`, `int32`, `int64`, `float32`, `float64`,
`number`. Collections of scalars: `list`, `set`, `map`, each with `elem`. Nested
objects: `list_nested`, `set_nested`, `single_nested`, each with `nested`.

Nested attributes, not blocks. The choice is permanent for a published provider,
and HashiCorp's OpenAPI generator emits no blocks, so there is no upstream signal
to follow. It is recorded here so it stays reviewable.

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
