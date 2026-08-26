# Glossary

Every domain term in this toolkit was individually approved by the
repository owner. A term not in this table may not be introduced — present
options to the owner first, record the decision here, then use it. The
retired terms cassette, blueprint and doctor may not reappear. Retirement
covers the domain noun and not the English word: code that records a reason,
a binder that drafts a call or a loader that merges `allOf` is prose.

| Term | Meaning |
|---|---|
| **audit** | The credentialed stage that exercises a live API to learn its true behaviour — minimum and maximum valid configuration, field dependencies, value-conditional rules. `tfpfgen audit run`. The only stage that touches a network. |
| **observation** | One recorded finding of an audit: what the live API actually accepted or rejected, with a redacted request/response excerpt as proof. Committed per entity in `audit/observations/<entity>.observations.json`, stamped with the spec hash it was observed against. Deliberately not replayable. |
| **audit summary** | How far a run got with each entity: its status, the reason it stopped, and the redacted refusal behind that reason, committed at `audit/summary.json` beside the observations and the request bodies. An observation says what a run learned about one property and a request body what a create looked like when it worked; neither says why an entity produced nothing, and an entity blocked by a refusal nobody watched for otherwise leaves no trace to act on. Written whatever the run did, so a run where every entity blocked still records why. The aggregate counts it also carries are a fact about one run against one document — `docs/emittance_tracker.md` remains the only place a count of what the toolkit emits or refuses may live. |
| **request bodies** | The create request bodies a run got the API to accept, committed per entity in `audit/request_bodies/<entity>.request_bodies.json` with the status each was answered and the response it was answered with. An observation says something about one property; these say what a whole create looked like when it worked, which is the one thing a generated acceptance test cannot derive — the document describes what should be accepted, and only a run knows what was. Acceptance fixtures replay these values rather than deriving them again. Named for the request half deliberately: the response is carried as evidence of what the API echoed, and it is the request that a configuration has to reproduce. |
| **correction** | One committed correction to the imported OpenAPI document: RFC 6902 operations plus a required justification and an optional evidence pointer to an observation. Lives in `spec/corrections/`; proposed ones await a human in `spec/corrections/proposed/`; rejected ones leave a marker in `spec/corrections/rejected/`. Kinds listed in config `audit.auto_accept` skip `proposed/` and land accepted directly, named with an `auto-NNN-` prefix. |
| **revise** | To fold observations into proposed corrections and apply accepted ones — `tfpfgen spec revise`. The spec is revised based on audit observations; the output is the revised spec (`spec/revised.yaml`), the single source of truth for all generation. |
| **import** | To pin the upstream OpenAPI document by hash — `tfpfgen spec import`. The imported document is immutable evidence of what the vendor published. |
| **validate** | The offline preflight — `tfpfgen config validate`: tfpfgen.yaml is well-formed, the auth method's secrets are present, tool pins match. Dies in seconds, before anything credentialed runs. |
| **generate** | Code generation — `tfpfgen sdk generate`, `tfpfgen provider generate`. Every generated file carries a DO-NOT-EDIT header and a manifest entry. |
| **verify** | The drift gate — `tfpfgen spec verify`, `tfpfgen sdk verify`, `tfpfgen provider verify`: regenerate into a temporary tree, byte-compare, fail on any difference. |
| **cleanup** | Deleting the live test objects an audit created, matched by name prefix — `tfpfgen audit cleanup`. Runs automatically at the start and end of every audit, and standalone on demand. |
| **activity ledger** | The audit's durable record of every object a run brings into existence, written and fsynced before each create request is sent: `audit/runs/<runid>.activity.jsonl`, one line per event (intent, created, rejected, deleted). Never committed — it records live objects in somebody's tenant. Cleanup replays it to delete by id after a crash. |
| **inputs** | The small optional committed file of operator-supplied values the audit cannot synthesize (a valid value for an example-less field, an existing parent object's id): `audit/inputs.json`. Its absence degrades gracefully — the audit covers what it can. |
| **authored** | A committed data path generation may never write: tfpfgen.yaml, corrections, inputs. Enforced by the manifest, not by convention. There are no authored *code* files — provider repos are 100% generated code. |
| **manifest** | The ledger of every derived file (path, digest, source, origin) and every authored path. `manifest.json` at the provider-repo root. |
| **quirkserver** | The deliberately-misbehaving stub API that serves as offline ground truth for audit logic and as the fake live API in pipeline rehearsals. |
| **vendor OpenAPI specs** | The third-party OpenAPI documents this toolkit's own tests parse and derive — a vendor's document exactly as published, committed under `internal/vendor_openapi_specs/testdata` and embedded, so every machine reads the same bytes and no test needs a network. Distinct from **spec**, which is the one document a provider is generated from: these are only ever read, never imported, corrected or revised. The earlier name *corpus*, and the hash-pinned fetch-at-test-time scheme it named, are both retired — the scheme put the build at the mercy of a vendor's publishing schedule, for test input that could simply be committed. |
| **backend** | An SDK generator behind the common interface: `kiota` or `openapi-generator`. Exactly one per provider repo. |
| **intermediate representation** | The ephemeral, never-committed derivation (`internal/intermediate_representation`) recomputed from the revised spec and config on every generation run; its model vocabulary (Model, Resource, Datasource, ListResource, Action, AttributeTree, ComputedOptionalRequired, Operation, Names) is approved. Every identifier in the package is fully worded — no abbreviated type, field, function, parameter or local. `Operation`/`Operations` replace the earlier `Op`/`Ops`, `AttributeType` replaces `TypeKind`, `Parameter`/`PathParameters` replace `Param`/`PathParams`, and `APIVersionDirectory` replaces `APIVersionDir`. `OneOf` is the one deliberate exception: it is named for the `stringvalidator.OneOf` it generates, and generated things are spelt the way HashiCorp spells them. `Datasource` likewise stays one word here, per the fixed spelling below. |
| **ComputedOptionalRequired** | How an attribute participates in a plan, and the four values it takes: `required`, `optional`, `computed`, `computed_optional`. The name and the values are [terraform-plugin-codegen-spec](https://github.com/hashicorp/terraform-plugin-codegen-spec)'s (`schema.ComputedOptionalRequired`), adopted so the toolkit and the specification that could describe its output call the same fact by the same name. It replaces the earlier `Presence` / `PresenceRequired` / `PresenceOptional` / `PresenceComputed` / `PresenceOptionalComputed`, and the value `optional-computed`; those spellings are retired. |
| **ElementType** | The type within a list — the spec's `schema.ElementType`. Replaces `ElementKind`, which is retired. Distinct from `sdkbind`'s own `ElementType`, which is a finished Go type expression for an SDK collection element rather than a terraform attribute type. |
| **code.Import** | One source-code import an emitted expression needs: `{Alias, Path}`, the spec's `code.Import` mirrored in `internal/code` rather than depended upon, because a mirrored struct costs nothing and a module costs a dependency. |
| **SchemaDefinition** | A finished code expression, carried together with the `code.Import`s it needs — the spec's spelling for the same thing `CLAUDE.md` already demands of every value a template consumes. `code.CustomValidator` and `code.CustomPlanModifier` are the two carriers: an expression declares the packages it references on the value it returns, so a validator or plan modifier can never be rendered into a file whose import block forgot it. |
| **binding** | The dialect-neutral mapping from one intermediate-representation entity onto the generated SDK's surface (`internal/sdkbind`): finished call expressions, accessor names, model types. Drafted by a per-backend binder, resolved against the real SDK with go/types. Its vocabulary (Bindings, Call, FieldAccess, Segment) is approved. |
| **prune** | To resolve drafted bindings against the generated SDK and delete whatever the SDK cannot carry, recording the SDK's reason for each deletion. A spelling is repaired only where the SDK admits exactly one answer — never invented, never widened. |
| **provider-core** | The shared plumbing the toolkit emits into every generated provider: the client, crud retry, error semantics, the conversion catalog, schema helpers, and the test harness. An emitted copy, not a shared library — every file is manifest-covered and regenerated wholesale. Templates live in `internal/templates/provider-core`. |
| **emit** | The render-and-write layer (`internal/emit`): it turns the provider-core and service templates plus finished context values into provider files and reports what it wrote as manifest entries. Its emission vocabulary is approved: `RenderServices` renders every entity's service files and answers a `ServiceFiles` (the files plus a `Registry` of registration lines); `Register` writes one slot's `Registrations` into a registry file at its sentinels; `RegistrySlots` is the fixed slot order. |
| **emittance tracker** | `docs/emittance_tracker.md`, the one place counts of what the toolkit emits are kept: provider tree files, entities by kind, and refusals by the stage that refused, per pilot document. A count is a fact about one toolkit commit against one pinned document, so every row records both. Stating such a count anywhere else — a readme, a comment, a doc — is what this file exists to stop: elsewhere it cannot be re-measured and goes stale invisibly. |
| **fixtures** | The single derivation of one entity's test fixture values (`internal/fixtures`), rendered twice — HCL and wire JSON — from one result so the two can never disagree. Its vocabulary is approved: a `Fixture` carries `Entries` (one `Entry` per supported attribute) and `Omissions` (one `Omission` per refused attribute, with its reason); a `Form` (`ConfigMinimal`, `ConfigMaximal`, `ResponseMinimal`, `ResponseMaximal`) selects which entries a rendering carries; `NamePrefix` (`tfpfgen-test-`) marks every synthesised name-bearing string. |
| **step kind** | One audit derivation rule's output, named for what the step does. The set is closed, twelve strong, spelled identically in Go (`Step*`) and in plan JSON: `createMinimal`, `readWithRetry`, `readConsecutive`, `updateField`, `deleteWithConfirmation`, `createMaximal`, `omitRequired`, `undocumentedEnumValue`, `undeclaredSpecField`, `createPerEnumValue`, `read`, `cleanupDelete`. |
| **outcome** | How far the audit got with one claim. The set is closed, four strong, spelled identically in Go (`Outcome*`) and in observation JSON: `confirmed`, `inconclusive`, `blocked`, `timeoutExhausted`. Despite its name's emphasis on time, `timeoutExhausted` covers every exhausted run budget alike — request, live-object and time. |
| **undocumentedFieldInSpec** | The observation kind (the fifteenth, `Kind*` in Go) claiming a real field the API demonstrably carries that the spec omits: read-back and consecutive-read responses show it with one stable JSON type, and the value is that type name (`string`, `number`, `boolean`, `object`, `array`). Its correction adds the property, with the observed type, to the entity schema's properties. |
| **rejectsUnknownFields** | The audit summary's per-entity report of the made-up-field probe (`undeclaredSpecField`): `true` when the API rejected a body field no schema declares, `false` when it accepted and ignored it. When true, that entity's refusal-based findings need caution. A summary field, never an observation. |
| **requestAdjustment** | One change the adaptive executor was forced to make to a request body to get it accepted — `add`, `remove`, `requires` or `borrow` — carried on the audit summary as `adjustments` and handed to the inference as its raw signal. The successor to the retired Wave 2 name *refinement*; that spelling no longer appears. |
| **triangulating inference** | The stage (`internal/audit/infer`, `Infer`) that reads all of one entity's evidence at once — every accepted create, every request adjustment, the collection responses, the strategy's hypotheses — and asserts a conditional edge only where the signals converge in both directions. Convergent evidence yields a `confirmed` observation; thin, one-directional or conflicting evidence yields `inconclusive`; a lone ambiguous 4xx yields nothing. It never touches a network. |
| **provenance** | How strongly an inferred edge is grounded, carried on the observation: `structural` (the document's own composition keywords), `prose` (mined description text) or `derived` (concluded from live probing alone). Empty on the scalar kinds an executor reads from one probe. |
| **validConfiguration** | The observation kind claiming an entity has several distinct valid configurations selected by a discriminator value. The attribute is the discriminator (gate) field; the value is the sorted list of gate values each of which produced a valid object. Extension key `x-tfpfgen-valid-configuration`, carrying the discriminator and the per-value valid field sets (assembled from the run's validWhen edges); it generates a config validator. Asserted only by the inference, never one probe. |
| **validWhen** | The observation kind claiming a field or block is valid only when a sibling gate field equals a specific value — the core conditional edge. The attribute is the subject field, the condition names the gate field and value, the value is `true`. Extension key `x-tfpfgen-valid-when`; it generates a config validator. Learned by variant diffing: accepted under exactly one gate value, removed under at least one other. |
| **dependsOn** | The observation kind claiming a field is settable only when a second field is present, whatever that second field's value. The attribute is the dependent field, the value is the name of the field it requires. Extension key `x-tfpfgen-depends-on`. Learned from a `requires` adjustment the API forced and the retry accepted. |
| **mutuallyExclusive** | The observation kind claiming at most one of a set of fields may be set. Entity-level (empty attribute); the value is the sorted list of the mutually-exclusive field names. Extension key `x-tfpfgen-mutually-exclusive`. Learned when each field is accepted alone but the pair is refused together. |
| **backoff** | How the audit answers a rate-limit refusal (HTTP 429): it waits, retries, and permanently slows the rest of the run down. Three parts — jitter on every request so a run's traffic does not march in lock-step into the server's metering window; retry with exponential backoff and full jitter, honouring `Retry-After` when the server sends one; and a halving of the token bucket's rate once refusals recur, never a raising of it. Lives in `internal/audit/run/backoff.go`; the token bucket it slows stays in `ratelimit.go`. Bounds are fixed constants, not configuration — operators size load through `audit.rate_limit_rps`. Reported on the run summary as `rateLimited`, `slowdowns` and `rateLimitRps`, because findings gathered while an API was refusing traffic are thinner than the same findings off a quiet one. |
| **identifierProperty** | The observation kind naming the response property that carries the value an entity's item path addresses the object by. Entity-level (empty attribute); the value is the property name. Extension key `x-tfpfgen-identifier-property`, compiled onto the read operation; derivation gives the id attribute that wire name in place of the path parameter's. Learned by matching the id the run already extracted against the response body's own properties, never by naming rules — a path that says `{id}` and a body that says `aid` name one identifier and the document says so nowhere. Asserted only where the two disagree. |
| **listResponseShape** | The observation kind recording a collection response's structure: a wrapped envelope (with its key) versus a bare array, plus the pagination style (`cursor`, `offset`, `page`, `none`). Entity-level; read from the live response body, never from the document. Extension key `x-tfpfgen-list-response-shape`, compiled onto the entity's list operation; derivation reads it in preference to the list response schema, which is exactly what the observation exists to contradict. |

## Fixed spellings

- Config file: `tfpfgen.yaml` (schema owned by `internal/config`).
- Secret roles: `TFPFGEN_AUTH_TOKEN`, `TFPFGEN_AUTH_CLIENT_ID`,
  `TFPFGEN_AUTH_CLIENT_SECRET`, `TFPFGEN_AUTH_USERNAME`,
  `TFPFGEN_AUTH_PASSWORD`, `TFPFGEN_AUTH_APP_ID`,
  `TFPFGEN_AUTH_APP_PRIVATE_KEY`.
- OpenAPI extensions: `x-tfpfgen-*`.
- Approved extension values:
  `x-tfpfgen-update-style: patch-merge | put-full | replace-only`;
  `x-tfpfgen-list-response-shape: {envelope: wrapped | bare, key: <wrapping
  key, wrapped only>, pagination: cursor | offset | page | none}` — an
  omitted `pagination` reads as `none`;
  `x-tfpfgen-identifier-property: <property name>`, on a read operation.
- Shared workflows, stage-numbered in pipeline order:
  `10-generate.yml`, `20-corrections.yml`, `30-ci.yml`,
  `40-acceptance.yml`, `50-docs.yml`, `60-release.yml`.
- Generation branch in provider repos: `tfpfgen/run-<id>`.
- Correction branch in provider repos:
  `tfpfgen/correction-<entity>-<kind>`, the kind in kebab case and both
  parts sanitised to lower-case letters, digits, underscores and hyphens;
  labelled `tfpfgen-correction`. One branch per entity per observation
  kind, so one pull request answers every finding of that kind on that
  entity at once. The earlier per-observation spelling
  `tfpfgen/correction-<observationID>` is retired: a grouped decision has
  no single observation to name, and the observation IDs a rejection needs
  travel in the pull request body instead.
- Machine-append sentinels in provider-core registry files:
  `// tfpfgen:<slot>:imports` and `// tfpfgen:<slot>:registrations`, where
  `<slot>` is `resources`, `datasources`, `list_resources`, or `actions` —
  the registry slots, in the fixed order `emit.RegistrySlots` declares.
- Per-entity (service) templates live under `internal/templates/services/`,
  one directory per service kind: `resource`, `datasource`,
  `list-resource`, `action`.
- Generated Go identifiers spell data source the HashiCorp way, two words
  in Pascal case: `DataSourceName`, never `DatasourceName`. Prose, CLI
  verbs and the intermediate representation keep the one-word
  `datasource`.
- Generated service package names are the provider name and the entity key
  run together, stripped of the punctuation a Go identifier may not carry:
  provider `jamfpro` and key `computer_group` give package
  `jamfprocomputergroup`. The prefix is not decoration — a key is whatever
  the document's path segments spell, which includes Go's reserved words,
  and an entity keyed `package` produced `package package`. No reserved
  word begins with a provider name, so the prefix removes the class rather
  than escaping one case of it, and it makes a generated package
  unmistakable at its import site.
- **list resource** — the list capability of a managed resource: the same
  terraform type, streaming the identities of the objects that exist right
  now. Terraform matches the two by type name and refuses to load a
  provider whose list resource names no resource, so one is derived
  exactly where an entity is both a resource and enumerable, and a
  resource the bindings or emission refuse takes its list resource with
  it. The earlier meaning — a list-only entity, enumerable but not
  addressable — is retired: no resource can ever match such an entity, so
  it could not be a list resource at all. Those entities are datasources.
- **operation set names** — one name per operation set an entity can carry,
  spelling the terraform function first and the API operations behind it
  second, so a name says what generation had to work with. The set is
  closed until the owner extends it, and a new one is named the same way:
  `create_with_crud_api`, `create_with_crd_api`, `create_with_ru_api`,
  `invoke_with_write_api`, `list_with_list_api`,
  `read_with_list_and_get_api`, `read_with_get_api`, `read_with_list_api`.
  `docs/mapping.md` fixes which calls each one makes. They name shapes in
  prose, not identifiers in Go: the classifier decides kinds, and a kind is
  what the code carries.
- **singleton** — one object the API owns outright at a single path,
  readable and writable but neither created nor destroyed, and returning no
  id when written. Terraform state needs one, so a constant is synthesised
  from the terraform type name. It is the `create_with_ru_api` operation
  set: create writes through the update call, delete stops managing the
  object rather than removing it, and `PUT` and `PATCH` are the same
  scenario. A singleton yields no datasource — the resource already reads
  the only object there is.
- **action** — an entity whose whole surface is a single write with no
  lifecycle around it, generated as a terraform action: an invocation
  rather than a thing terraform owns. Its operation sits in the
  classification's create slot, because the role slots describe HTTP
  position and the kind says what that position amounts to.
- **filter attribute** — an optional argument of a datasource with a list
  operation, one per scalar field at the root of a listed object, named and
  typed for the field it selects on. Objects and collections are not
  offered: HCL would have to describe a whole object to match one leaf of
  it, and a collection has no single value to compare. Matching is exact,
  and a filter the configuration leaves out narrows nothing, so several
  combine and none is mandatory. Filters carry no wire value and bind to no
  SDK field — the match runs over the objects the list already answered
  with. They are what makes such a datasource usable: without them the
  collection comes back whole and HCL must address a result by its position
  in it. `items`, which carries what matched, is the toolkit's own
  vocabulary rather than any API's and takes no wire name beyond its own.
- **lookup-by-key datasource** — a datasource whose only access is the item
  `GET`. With no list operation there is nothing to filter, so the item
  path parameter — often a name, in the APIs that address by one — becomes
  the required argument, and the answer carries the id. Carried as
  `Classification.LookupByKey`, and set only where the entity is not also a
  resource: a resource's by-id datasource is its normal companion, not a
  key lookup.
- **variant attribute** — one branch of a `oneOf`/`anyOf`, emitted as a
  nested attribute under the union's own attribute, which becomes an object
  carrying one variant per branch. It is the shape the generated SDK already
  has: a union arrives there as a composed type with a field and an accessor
  per branch. A variant takes its name from the component the branch
  references and keeps that component's spelling as its wire name, which is
  what makes the drafted accessor land on the one the SDK generated. A branch
  referencing no component refuses the whole union rather than half of it.
  Variants are computed wherever nothing writes the union; a writable union is
  not served yet, because `Optional + Computed` cannot express mutual
  exclusion — see `docs/mapping.md` row 13.
- **resource identity schema** — the separate object terraform stores
  beside a resource's state to name the remote object it stands for
  (`resource.ResourceWithIdentity`). It is the addressing attributes plus
  the `id`, all `RequiredForImport`: the framework requires an identity to
  name at most one remote object per provider, and an `id` alone does not
  where a parent scopes it. A list resource's results are identities in
  this shape, which is why the resource must declare it.
- **addressing attribute** — a generated attribute that exists to fill an
  operation's path parameter rather than to carry a field of the object.
  Every path parameter above the item key becomes one: required, spelled
  from its wire name, in path order ahead of the id, and forcing
  replacement, because an object does not move to another parent in place.
  A parent the request or response body already declares is left as the
  body declares it. Addressing attributes and the `id` survive binding with
  no SDK field behind them — they address the object rather than describe
  it, so no model carries them.
- Naming helpers the intermediate representation exports for every
  emitter: `GoName` (the Pascal Go spelling, acronym-aware) and
  `TerraformName` (the snake_case terraform attribute spelling).
- Rejected-proposal marker: one JSON file per rejected proposal in
  `spec/corrections/rejected/`, shaped
  `{"observationID": "…", "reason": "…", "rejectedAt": "…"}`. A marker
  suppresses re-proposal of that observation permanently; deleting the
  marker is the only way back.
- Audit runs directory: `audit/runs/` holds the activity ledgers, one
  `<runid>.activity.jsonl` per run. Never committed.
- Audit force flag: `--force-api-audit` on `tfpfgen audit run` proceeds
  despite foreign objects beyond the object budget in the tenant. There is
  no consent environment variable: the audit creates and deletes real
  objects, running it only against sandbox/non-production tenants is the
  operator's responsibility, and the toolkit does not police it.
- Audit plan tokens: `<runid>` is the run-id placeholder execution
  substitutes into synthesised names; `${VAR}` marks an operator input
  read from the named environment variable at execution time;
  `$created:<entity>` is the id of an object the audit itself created.
- Operator environment variables a generated provider reads:
  `<PROVIDER>_*` — the uppercased provider name, bare, e.g.
  `THOUSANDEYES_API_TOKEN`. The earlier `TF_<PROVIDER>_*` spelling is
  retired: published providers spell these `AWS_`, `GOOGLE_`,
  `CLOUDFLARE_`, and an operator reaching for `THOUSANDEYES_API_TOKEN` is
  reaching for the name every other provider taught them. Still distinct
  from the pipeline's `TFPFGEN_AUTH_*` secrets, which only the toolkit
  reads.
- Provider block attributes: `endpoint`, `api_token`, `username`,
  `password`, `client_id`, `client_secret`, `token_url`, `request_timeout`,
  and the `client_options` block.
- **client_options** — the provider block that paces the retry loops the
  generated provider runs on the practitioner's behalf:
  `read_retry_delay_seconds` and `delete_retry_delay_seconds`. Named on the
  `terraform-provider-microsoft365` pattern — an HCL attribute first with
  the environment variable as its override, units in the name, durations as
  integer seconds. A null, unparseable or non-positive value leaves the
  compiled default in force, so a typo can neither fail an apply nor turn a
  paced loop into a busy one. Installed once by `Configure` through
  `crud.SetRetryCadence`.
- Conversion catalog function families: `APIToFramework*` and
  `FrameworkToAPI*`.
- Go-idiomatic acronym casing in generated names: known acronyms uppercase
  whole in Pascal/camel spellings (`HTTPServer`, `APIKey`), and a leading
  acronym lowers whole in camel (`id`, `apiKey`). The acronym table lives in
  `internal/intermediate_representation/naming.go`; additions go through the
  repository owner.
- **unsupported.json** — the committed record of everything generation
  refused, at the provider repo root: `{format_version, unsupported: [{path,
  stage, reason}]}`. `path` addresses what was refused in the
  terraform-plugin-codegen-spec idiom — `resource "tag" attribute
  "metadata"`, an attribute's dotted path beneath its entity, and `entity
  "x"` for something that fits no kind and so has no kind to name. `stage`
  is the closed set `derivation | binding | emission`, naming which part of
  the pipeline refused it. Derived content like any other: manifest-covered
  and byte-compared by `provider verify`. Generation never fails on it —
  one entity must not take the whole provider with it — the point is that a
  refusal appearing or disappearing is a line in a generation pull request
  rather than a line in a CI log nobody reads.
