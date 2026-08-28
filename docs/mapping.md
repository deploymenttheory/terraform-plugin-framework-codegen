# API Behaviour → Terraform Plugin Framework Schema Mapping

Two questions, one per section. What shape a field's observed behaviour
demands, and what an entity's operation set makes it.

Scope: `terraform-plugin-framework` only. No `helper/schema` (SDKv2) patterns.

## Field behaviour

One row per scenario. Where behaviour differs by HTTP method, the method is
called out inline within the Request and Response cells.

| # | Scenario | HTTP Method | Request | Response | Provider Schema Type | Notes |
|---|---|---|---|---|---|---|
| 1 | Field is accepted on write but never returned by any response, including the create response, so its value can never be verified. | POST, PATCH | Accepted | Never returned | `Optional + WriteOnly` plus an `..._version` Int64 trigger | Read from `req.Config`, never `req.Plan`. Cannot be `Computed` or nested in a `Set`. Gate resend on the version bump. |
| 2 | Field is never accepted in a request and is always populated by the server in every response. | POST, GET | Not accepted; ignored or 400 | Always populated | `Computed`, plus `UseStateForUnknown()` only if stable post-create | Stable (`id`, `created_at`) → pin it. Volatile (counts, timestamps) → leave unpinned, or move to a data source. |
| 3 | Field is optional in the request; the response always returns a value, either the caller's or a server-assigned default. | POST, PATCH | Optional | Always populated | `Optional + Computed` + `UseStateForUnknown()` | Omitted config plans as unknown until create fills it. Removal from config is sticky, not a revert to default. |
| 4 | Field accepts a real value on write but every response returns it obfuscated rather than omitted. | POST, PATCH, GET | Real value accepted | Masked (`********`, `****1234`) | `Optional + Sensitive`, not `Computed` | Persist the config value on write; skip the attribute entirely in `Read`. Import forces one spurious update. |
| 5 | Field is only valid when a sibling field holds a specific value; otherwise it is rejected or silently dropped. | POST, PATCH | Valid only when Y = Z | Populated when Y = Z, else null | `Optional + Computed` + custom `PlanModifier` + `ValidateConfig` | Modifier reads Y from `req.Plan`, not state — Y may change in the same apply. `ValidateConfig` catches hard rejects at plan time. |
| 6 | Field is settable at create but the API refuses to change it afterwards. | POST, PATCH | Accepted on create; rejected or ignored on update | Unchanged after create | `RequiresReplace()`, or `RequiresReplaceIfConfigured()` when a server default also applies | Where replace is too destructive, drop the modifier and raise `AddAttributeError` inside `Update()` instead. |
| 7 | Field cannot be set at create but becomes settable via a later update once the resource exists. | POST, PATCH | Rejected or discarded on create; accepted on update | Null until set by update | `Optional` — asymmetry is invisible at schema level | Omit from the create payload, then issue a follow-up update. Persist the ID before that call or a failure orphans the resource. |
| 8 | Field is echoed back in a textually different but semantically equivalent form — case-folded, trimmed, or reserialised. | POST, PATCH, GET | Accepted as authored | Returned normalised | Custom type implementing `StringSemanticEquals` | Semantic equality, not a plan modifier. Common in plist payloads and URL fields. |
| 9 | Field is accepted without error but silently stored differently — clamped to a maximum, truncated, or coerced to canonical casing. | POST, PATCH | Out-of-range value accepted silently | Returns the clamped value | `Optional`/`Required` + validators mirroring the API's cap | Encode the documented limit (`int64validator.AtMost`, `stringvalidator.OneOf`) so it fails at plan time rather than diverging silently. |
| 10 | Field omitted from the request returns as an empty value rather than absent, so null in config never matches the response. | POST, GET | Omitted entirely | `""`, `[]`, or `0` | `Optional + Computed`, or normalise empty → null in the read mapper | Apply one approach provider-wide. Watch Go `omitempty` dropping deliberately-empty values from the body. |
| 11 | Collection is returned with members in an order unrelated to submission order. | POST, PATCH, GET | Accepted in authored order | Same members, arbitrary order | `SetAttribute` / `SetNestedAttribute` | Costs index addressing and duplicates; one unknown member marks the whole set unknown. Use `List` only if order is genuinely meaningful. |
| 12 | Collection is returned containing server-injected members the caller never submitted. | POST, PATCH, GET | Caller's members accepted | Superset of what was written | `Optional + Computed` on the collection, or filter injected members in the read mapper | Removal semantics differ sharply. If injected members are indistinguishable from the caller's, `Optional + Computed` is the only safe option. |
| 13 | Field carries one of several object shapes — a `oneOf`/`anyOf` — and the response holds whichever the server chose. | POST, PATCH, GET | One shape accepted | Exactly one shape returned | An object attribute carrying one nested attribute per variant | Computed throughout where nothing writes it. Where it is writable the variants are `Optional` and never `Computed`, with `resourcevalidator.Conflicting` over them — see the note below the table. |

**Row 13, on why a variant is never `Optional + Computed`.** Terraform Core
builds the proposed new state by taking a value from configuration where it is
non-null and falling back to prior state otherwise. A variant that is
`Computed` therefore survives its own removal from configuration, so switching
from one variant to another leaves both set in the plan and the provider sends
both. No config validator catches it: `Conflicting` and `ExactlyOneOf` read
configuration, never the plan, and the configuration holds only the new
variant. Row 3 records the same behaviour in general — removal from config is
sticky — and a union is where it does real damage.

A variant is named for the component its branch references, which is also what
the SDK names its accessor after, so the two agree without either being told
about the other. A branch that references no component names nothing on either
side, and one of those refuses the whole union: half a union is a schema that
cannot hold what the API returns.

## Entity operation sets

One named shape per operation set: which kind the entity yields, and which API
call each generated function makes. `internal/specmodel/classify.go` decides
it.

**Entities come from paths, not from schemas.** A collection path and its item
sibling — `/tags` and `/tags/{tagId}` — are one entity, keyed off the
collection. An operation's role is its HTTP position and nothing else:

| Position | Role |
|---|---|
| `POST` on the collection | create |
| `GET` on the collection | list |
| `GET` on the item | read |
| `PUT` or `PATCH` on the item | update |
| `DELETE` on the item | delete |
| `PUT` or `PATCH` on the collection | the update of a `create_with_ru_api`, and nothing on any other shape |

A second claimant for a role, or a method in no role's position, is recorded as
surplus rather than dropped, so the audit planner sees the whole surface.

**A kind is what an entity becomes**: `resource`, `datasource`, `list-resource`
or `action`. An entity commonly yields several, and the kinds are decided
independently — a resource is always readable by id, so it yields a datasource
too, and one kind never excludes another.

**Each operation set has a name**: the terraform function it serves, then the
API operations behind it. `create_with_crd_api` is the create function working
against a create, read and delete API. New operation sets are named the same
way.

| Name | Kind | Operation set |
|---|---|---|
| `create_with_crud_api` | resource | `POST` + item `GET` + item `PUT`/`PATCH` + item `DELETE` |
| `create_with_crd_api` | resource | `POST` + item `GET` + item `DELETE`, no update |
| `create_with_ru_api` | resource | `GET` + `PUT`/`PATCH` on one id-free path |
| `invoke_with_write_api` | action | exactly one operation, and it writes |
| `list_with_list_api` | list resource | collection `GET`, beside a resource |
| `read_with_list_and_get_api` | datasource | collection `GET` + item `GET` |
| `read_with_get_api` | datasource | item `GET`, no collection `GET` |
| `read_with_list_api` | datasource | collection `GET`, no item `GET` |

A shape is only selected where the operations it needs carry schemas: the
create a request body, the read and the list a success response. An operation
present but undeclared classifies as nothing, and says so. An action is the
exception — a write that takes no body is still a write.

## Resources

Three operation sets yield a resource. They differ in what the API lets
terraform do to a live object, and the difference reaches the schema in two of
the three.

| Terraform function | `create_with_crud_api` | `create_with_crd_api` | `create_with_ru_api` |
|---|---|---|---|
| `Create` | `POST` the collection | `POST` the collection | `PUT`/`PATCH` the path |
| `Read` | `GET` the item | `GET` the item | `GET` the path |
| `Update` | `PUT`/`PATCH` the item | refuses | `PUT`/`PATCH` the path |
| `Delete` | `DELETE` the item | `DELETE` the item | removes from state |
| Schema consequence | none | `RequiresReplace()` on every writable attribute | `id` is synthesised |

**`create_with_crud_api`** is the ordinary shape and needs no accommodation.
The four functions each call their own operation.

**`create_with_crd_api`** has no operation that changes a live object, so no
change to one can be applied in place. Every writable attribute carries
`RequiresReplace()`, which makes terraform destroy and recreate rather than
update. `Update` is generated because the interface demands it, and errors: a
plan that reached it would already have decided to replace.

**`create_with_ru_api`** is a singleton — one object the API owns outright,
typically the settings behind a screen of a product's own console. It has no
create and no destroy, and its write returns no id. Terraform still needs one,
so a constant is synthesised from the terraform type name. `Create` writes
through the update call, which is also where the practitioner-settable
attributes are read from, since there is no create body to read them from.
`Delete` calls nothing: terraform stops managing the object and the API keeps
it as it is. `PUT` and `PATCH` are the same scenario here, not two. A
singleton's path names no item, so each of its path parameters addresses a
parent and becomes a required addressing attribute — `/templates/{id}/sharing-settings`
takes `template_id`, named after the parent because `id` is the resource's own.

The classifier tells it from a collection by the response, not by the path: a
`GET` with no item sibling whose body carries no array is one object rather
than a list of them. Read as a collection it would have no elements to reach
and would fail for saying so.

A `create_with_ru_api` yields no datasource. There is nothing to look up: the
object is one fixed thing, and the resource already reads it.

**A query parameter an operation requires** belongs to the operation rather
than to the object, so no attribute carries it. The generated call sends it as
a constant — the parameter's own example first, then its schema's example,
then its default — through the SDK's request configuration for that verb; the
audit sends the same value on every delete, so an object whose delete confirms
itself through one is not left in the tenant. A required parameter the
document states no value for is left out: the document has not said what to
send.

## Actions

**`invoke_with_write_api`** is an entity whose whole surface is a single write —
one `POST`, `PUT` or `PATCH`, with no read, no list and no delete beside it.
Issuing a certificate, rotating a key, triggering a sync. It has no lifecycle,
so it is an invocation rather than a thing terraform owns: its arguments are
its request body and its path parameters, and it holds no state.

The operation sits in the classification's create slot, because the slots
describe HTTP position and the kind says what that position amounts to.

Gap: `internal/specmodel/classify.go` recognises only the `POST` form. A lone
`PUT` or `PATCH` yields nothing and the entity is excluded.

## List resources

**`list_with_list_api`** is the list capability of a resource, not a kind of
entity in its own right: the same terraform type, streaming the identities of
the objects that exist right now. It exists exactly where an entity is both a
resource and enumerable. See the `list resource` entry in `docs/glossary.md`
for why an enumerable entity with no resource behind it cannot be one.

That confines it to `create_with_crud_api` and `create_with_crd_api`. A
`create_with_ru_api` is one object at one path with nothing to enumerate.

Gap: the `List` emitted from
`internal/templates/services/list-resource/list.go.tmpl` makes one call and
streams what it returns, so a paged API yields only its first page. The audit
records the pagination style in `x-tfpfgen-list-pagination`, but nothing
downstream reads it, and the style alone is not enough to build a loop — the
request parameter carrying the page and the response field carrying the next
one are recorded nowhere.

## Datasources

Three operation sets yield a datasource, and none of them needs a resource.

| Name | Argument | Calls | Answers |
|---|---|---|---|
| `read_with_list_and_get_api` | one optional filter per root scalar | item `GET` where `id` is set, collection `GET` otherwise | `items` |
| `read_with_get_api` | the item path key, required | item `GET` | the object it identifies |
| `read_with_list_api` | one optional filter per root scalar | collection `GET` | `items` |

**Filters are what make a datasource usable.** A datasource that only lists
hands back the collection whole, and HCL then has to address a result by its
position in it — `items[2]` — which no API promises to keep stable. So every
scalar field at the root of a listed object is offered as an optional argument
of that field's own type, and `items` carries the objects that matched every
argument the configuration set. Nested fields and collections are not offered:
HCL would have to describe a whole object to match one leaf of it, and a
collection has no single value to compare. They are read off the items the
filters selected.

```hcl
data "example_computer" "mine" {
  name    = "abcd"
  managed = true
}
```

Matching is exact. A filter the configuration leaves out narrows nothing,
which is what lets several combine and none be mandatory.

**`read_with_list_and_get_api`** is the common shape, and the one every
resource also yields. Setting `id` is answered by the item `GET` rather than
by listing the collection and discarding all but one of it; every other filter
lists and matches.

**`read_with_get_api`** is a lookup by key. With no list operation there is
nothing to filter, so the item path parameter becomes the required argument —
often a name, in the APIs that address by one — and the answer carries the id.
It is the one datasource shape that answers a single object.

**`read_with_list_api`** is a collection the API enumerates but cannot address
one member of. It generates the same filters and `items`, with no operation
behind an `id`, and takes the schema of one object from the collection's own
element — the only account of it the document offers. It yields neither a
resource nor a list resource, because neither can exist without a way to reach
a single object.

A filter is named for the field it selects on, so its spelling is the API's.
`items` is the toolkit's own, and carries no wire name beyond itself.

## What yields nothing

An entity that fits no kind is excluded with a reason, never guessed at. The
reasons are for people, and they surface in audit-planning output so a
surprising omission traces to the document rather than to a hunch. Five name a
shape that matched but lacked a schema:

- create, read and delete are present but the create request or read success
  response declares no schema
- list and read are present but a success response schema is missing
- one object at a fixed path with no operation that writes it, so terraform
  would own nothing
- list is present but its success response declares no schema
- readable by id but the read success response declares no schema

Where no shape matched at all, the reason names the roles the entity does have,
or reports that it has none in a classifiable position.
