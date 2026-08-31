# Glossary

Every domain term in this toolkit was individually approved by the
repository owner. `docs/naming-standard.md` is the rule set a name has to
pass before it is proposed for this table. A term not in this table may not be introduced — present
options to the owner first, record the decision here, then use it. The
retired terms may not reappear: *cassette*, *blueprint*, *doctor*, *corpus*, *authority* (the computed-optional route; now **SchemaAttributeTypeDetermination**), *refuse* as the code verb for setting an entity or attribute aside (now **exclude**), *shapelessObject* (now `objectWithoutPropertiesOrAdditionalProperties`),
*refinement*, `Op`/`Ops`, `Param`/`Params`/`PathParams`, `TypeKind`,
`ElementKind`, `APIVersionDir`, `Presence*`, `optional-computed`,
`TF_<PROVIDER>_*`, and the correction-branch spelling
`tfpfgen/correction-<observationID>`.

Retirement covers the domain noun and not the English word: code that records
a reason, a binder that drafts a call or a loader that merges `allOf` is
prose. **`shape` is the one word banned as an *identifier* while allowed in
prose** — it was naming four unrelated things. `Strategy.AuditShape` is the
single recorded exception.

| Term | Meaning |
|---|---|
| **audit** | The credentialed stage that exercises a live API to learn its true behaviour — minimum and maximum valid configuration, field dependencies, value-conditional rules. `tfpfgen audit run`. The only stage that touches a network. |
| **observation** | One recorded finding of an audit: what the live API actually accepted or rejected, with a redacted request/response excerpt as proof. Committed per entity in `audit/observations/<entity>.observations.json`, stamped with the spec hash it was observed against. Deliberately not replayable. |
| **audit summary** | How far a run got with each entity: its status, the reason it stopped, and the redacted refusal behind that reason, committed at `audit/summary.json` beside the observations and the request bodies. An observation says what a run learned about one property and a request body what a create looked like when it worked; neither says why an entity produced nothing, and an entity blocked by a refusal nobody watched for otherwise leaves no trace to act on. Written whatever the run did, so a run where every entity blocked still records why. The aggregate counts it also carries are a fact about one run against one document — `docs/emittance_tracker.md` remains the only place a count of what the toolkit emits or excludes may live. |
| **request bodies** | The create request bodies a run got the API to accept, committed per entity in `audit/request_bodies/<entity>.request_bodies.json` with the status each was answered and the response it was answered with. An observation says something about one property; these say what a whole create looked like when it worked, which is the one thing a generated acceptance test cannot derive — the document describes what should be accepted, and only a run knows what was. Acceptance fixtures replay these values rather than deriving them again. Named for the request half deliberately: the response is carried as evidence of what the API echoed, and it is the request that a configuration has to reproduce. |
| **correction** | One committed correction to the imported OpenAPI document: RFC 6902 operations plus a required justification and an optional evidence pointer to an observation. Lives in `spec/corrections/`; proposed ones await a human in `spec/corrections/proposed/`; rejected ones leave a marker in `spec/corrections/rejected/`. Kinds listed in config `audit.auto_accept` skip `proposed/` and land accepted directly, named with an `auto-NNN-` prefix. A correction is **proposed**, then **accepted** (merged), **rejected** (closed, leaving a marker that suppresses re-proposal permanently) or **withdrawn** (closed by a later run that no longer proposes it, recording nothing, so the observation can be proposed again). |
| **revise** | To fold observations into proposed corrections and apply accepted ones — `tfpfgen spec revise`. Its two halves are `Propose` and `WriteRevision`. The spec is revised based on audit observations; the output is the revised spec (`spec/revised.yaml`), the single source of truth for all generation. |
| **import** | To pin the upstream OpenAPI document by hash — `tfpfgen spec import`. The imported document is immutable evidence of what the vendor published, committed at `spec/imported.yaml` beside its **pin**. |
| **pin** | The record of where an imported document came from and what it was: source, SHA-256, fetch time, format and version, committed at `spec/imported.pin.json`. Carried as the `Pin` type. |
| **validate** | The offline preflight — `tfpfgen config validate`: tfpfgen.yaml is well-formed, the auth method's secrets are present, tool pins match. Dies in seconds, before anything credentialed runs. |
| **verify generated tree** | The toolchain gate `provider generate` runs after installing the tree: `go mod tidy`, `go build ./...`, `go vet ./...`. Enabled by `--verify-tree`, on by default where a Go toolchain is on PATH. The files it finalises — `go.mod`, `go.sum` — are recorded in the manifest under the `toolchain` origin, so the drift gate covers what no template emitted. |
| **generate** | Code generation — `tfpfgen sdk generate`, `tfpfgen provider generate`. Every generated file carries a DO-NOT-EDIT header and a manifest entry. |
| **verify** | The drift gate — `tfpfgen spec verify`, `tfpfgen sdk verify`, `tfpfgen provider verify`: regenerate into a temporary tree, byte-compare, fail on any difference. |
| **cleanup** | Deleting the live test objects an audit created, matched by name prefix — `tfpfgen audit cleanup`. Runs automatically at the start and end of every audit, and standalone on demand. |
| **activity ledger** | The audit's durable record of every object a run brings into existence, written and fsynced before each create request is sent: `audit/runs/<runid>.activity.jsonl`, one line per event (intent, created, rejected, deleted). Never committed — it records live objects in somebody's tenant. Cleanup replays it to delete by id after a crash. |
| **inputs** | The small optional committed file of operator-supplied values the audit cannot synthesize (a valid value for an example-less field, an existing parent object's id): `audit/inputs.json`. Its absence degrades gracefully — the audit covers what it can. |
| **authored** | A committed data path generation may never write: tfpfgen.yaml, corrections, inputs. Enforced by the manifest, not by convention. There are no authored *code* files — provider repos are 100% generated code. |
| **manifest** | The ledger of every derived file (path, digest, source, origin) and every authored path. `manifest.json` at the provider-repo root. |
| **test API server** | The deliberately-misbehaving stub API that serves as offline ground truth for audit logic and as the fake live API in pipeline rehearsals. |
| **vendor OpenAPI specs** | The third-party OpenAPI documents this toolkit's own tests parse and derive — a vendor's document exactly as published, committed under `internal/vendor_openapi_specs/testdata` and embedded, so every machine reads the same bytes and no test needs a network. Distinct from **spec**, which is the one document a provider is generated from: these are only ever read, never imported, corrected or revised. The earlier name *corpus*, and the hash-pinned fetch-at-test-time scheme it named, are both retired — the scheme put the build at the mercy of a vendor's publishing schedule, for test input that could simply be committed. |
| **backend** | An SDK generator behind the common interface: `kiota` or `openapi-generator`. Exactly one per provider repo. |
| **intermediate representation** | The ephemeral, never-committed derivation (`internal/intermediate_representation`) recomputed from the revised spec and config on every generation run; its model vocabulary (Model, Resource, Datasource, ListResource, Action, AttributeTree, ComputedOptionalRequired, Operation, Names) is approved. Every identifier in the package is fully worded — no abbreviated type, field, function, parameter or local. `Operation`/`Operations` replace the earlier `Op`/`Ops`, `AttributeType` replaces `TypeKind`, `Parameter`/`PathParameters` replace `Param`/`PathParams`, and `APIVersionDirectory` replaces `APIVersionDir`. `OneOf` is the one deliberate exception: it is named for the `stringvalidator.OneOf` it generates, and generated things are spelt the way HashiCorp spells them. `Datasource` likewise stays one word here, per the fixed spelling below. |
| **ComputedOptionalRequired** | How an attribute participates in a plan, and the four values it takes: `required`, `optional`, `computed`, `computed_optional`. The name and the values are [terraform-plugin-codegen-spec](https://github.com/hashicorp/terraform-plugin-codegen-spec)'s (`schema.ComputedOptionalRequired`), adopted so the toolkit and the specification that could describe its output call the same fact by the same name. It replaces the earlier `Presence` / `PresenceRequired` / `PresenceOptional` / `PresenceComputed` / `PresenceOptionalComputed`, and the value `optional-computed`; those spellings are retired. |
| **ElementType** | The type within a list — the spec's `schema.ElementType`. Replaces `ElementKind`, which is retired. For a collection of collections it is the leaf, and `NestedCollectionElementTypes` spells every level beneath the outer collection, outermost first and ending in that leaf; `CollectionNestingDepth()` counts them. Distinct from `sdkbind`'s own `ElementType`, which is a finished Go type expression for an SDK collection element rather than a terraform attribute type. |
| **apiMechanics** | The catalogue of API mechanics: response properties that describe the API rather than the resource, which `spec revise` leaves out of the revised document — so the SDK, the derivation and the audit's proposals all stop seeing them together. Lives in `internal/spec/revise/api_mechanics.go`; each entry matches exact wire spellings from a published convention. Grows one mechanic at a time as emittance surfaces one in a generated schema; every removal is named in the revision's result. |
| **reservedSpellings** | The wire spellings the ecosystem writes as one run, which letter-boundary snake casing would split: two owner-owned lists in `internal/intermediate_representation/naming.go` — `technology` (oauth, oauth2, idp, ios, ipados, macos, tvos, watchos, visionos) and `brands` (itunes, github). A member is keyed by the exact casing a wire uses and maps to its one-word snake form; its Go spelling joins the acronym table in the vendor's own casing (OAuth, IdP, MacOS, ITunes…). Matching is exact-case between word boundaries, so `RadiOS` and `idPage` never match. Additions go through the repository owner. |
| **navigationLinks** | The first API mechanic: the API's own links to itself and its neighbours, spelled `_links` as HAL (JSON Hypertext Application Language) reserves it. `apiMechanics.navigationLinks` in prose and the revise output. |
| **NestedAttributes** | The child attribute tree an object attribute carries, or the object at the bottom of a list or map of objects: `Attribute.NestedAttributes`. Replaces the bare adjective `Nested`, which is retired as a field name. |
| **Attributes** | The `AttributeTree` a resource, datasource, list resource or action carries: `Resource.Attributes`, `Datasource.Attributes`, `ListResource.Attributes` and `.AddressingAttributes`, `Action.RequestAttributes`. The earlier field name `Schema` is retired for these: `schema` means the OpenAPI schema (`specmodel.Schema`) and nothing else. |
| **Type** | The terraform attribute type of an attribute or a path parameter: `Attribute.Type`, `URLPathParameter.Type`, `QueryParameter.Type`, all `AttributeType`. `Attribute.Kind` is retired; `Kind` is left to `OperationKind` (an operation's lifecycle role) and to `specmodel`'s entity kinds. |
| **URLPathParameter** | One parameter of a URL path template, with its name and terraform type: `Operation.PathParameters []URLPathParameter`. Replaces the bare `Parameter`, which said neither which parameter nor of what. |
| **SchemaAttributeTypeDetermination** | Which declaration decided that an attribute is computed-optional, recorded so an operator can find the route and correct it: `serverDefault` (the audit measured it), `responseRequired` (the response schema requires it), `requestDefault` (the request property declares a default), `responseProperty` (the response schema merely describes it). `Attribute.SchemaAttributeTypeDetermination`; the constants are `SchemaAttributeTypeDetermination<Route>`. Replaces the unrecorded root word `Authority`, which is retired. |
| **ReadAfterWriteDelay** | The longest read-after-write lag the document declares across an entity's lifecycle, as a duration: `Resource.ReadAfterWriteDelay`, the template field `HasReadAfterWriteDelay`, and the emitted constant `readAfterWriteDelay`. One spelling for the fact the observation kind `readAfterWrite` records; the earlier `EventualConsistency` / `HasEC` / `ECDuration` are retired. |
| **TerraformBlockType** | Which kind of terraform block an entity was, or would have become — `resource`, `datasource`, `list_resource` or `action` — on an `UnsupportedEntity` and in `unsupported.json` as `terraformBlockType`. Replaces the bare `Kind` there. |
| **exclude** | What the toolkit does when it sets an entity or an attribute aside rather than generating it, at any stage — configuration, classification, derivation, binding or emission. The code verb is `exclude` (`exclude()`, `excludeReservedRootNames()`, `excludedAttributes()`), the model fields are `ExcludedBy*`, the cause is `excludedByConfiguration`, and the committed record is `unsupported.json`. `refuse` is retired for this sense; it remains the audit's word for an API answering a request with a 4xx (`refusal`, `uncorrectableRefusal`, the refusal **grammar**), which is a different fact. |
| **IsDatasourceFilterArgument** | The bool marking a datasource argument that selects which listed objects come back rather than describing one (`Attribute.IsDatasourceFilterArgument`); it binds to no SDK field. Replaces the bare `Filter`. |
| **ValidConfigurationVariant** | One discriminator value of a **validConfiguration** with the attribute names valid under it (`Value`, `AttributesValidWhenEqual`). Replaces the abbreviated `ConfigVariant`. |
| **WhenPropertyEquals** / **AttributesValidWhenEqual** | On a `ConditionalRequirement` or `ConditionalValidity`: the value the gate property must equal, and the attribute names that are required or valid when it does. Replace the bare `Equals` and `Valid`. |
| **code.Import** | One source-code import an emitted expression needs: `{Alias, Path}`, the spec's `code.Import` mirrored in `internal/code` rather than depended upon, because a mirrored struct costs nothing and a module costs a dependency. |
| **SchemaDefinition** | A finished code expression, carried together with the `code.Import`s it needs — the spec's spelling for the same thing `CLAUDE.md` already demands of every value a template consumes. `code.CustomValidator` and `code.CustomPlanModifier` are the two carriers: an expression declares the packages it references on the value it returns, so a validator or plan modifier can never be rendered into a file whose import block forgot it. |
| **binding** | The dialect-neutral mapping from one intermediate-representation entity onto the generated SDK's surface (`internal/sdkbind`): finished call expressions, accessor names, model types. Drafted by a per-backend binder, resolved against the real SDK with go/types. Its vocabulary (Bindings, Call, FieldAccess, Segment) is approved. |
| **prune** | To resolve drafted bindings against the generated SDK and delete whatever the SDK cannot carry, recording the SDK's reason for each deletion. A spelling is repaired only where the SDK admits exactly one answer — never invented, never widened. |
| **provider-core** | The shared plumbing the toolkit emits into every generated provider: the client, crud retry, error semantics, the conversion catalog, schema helpers, and the test harness. An emitted copy, not a shared library — every file is manifest-covered and regenerated wholesale. Templates live in `internal/templates/provider-core`. |
| **emit** | The render-and-write layer (`internal/emit`): it turns the provider-core and service templates plus finished context values into provider files and reports what it wrote as manifest entries. Its emission vocabulary is approved: `RenderServices` renders every entity's service files and answers a `ServiceFiles` (the files plus a `Registry` of registration lines); `Register` writes one slot's `Registrations` into a registry file at its sentinels; `RegistrySlots` is the fixed slot order. |
| **emittance tracker** | `docs/emittance_tracker.md`, the one place counts of what the toolkit emits are kept: provider tree files, entities by kind, and exclusions by the stage that excluded, per pilot document. A count is a fact about one toolkit commit against one pinned document, so every row records both. Stating such a count anywhere else — a readme, a comment, a doc — is what this file exists to stop: elsewhere it cannot be re-measured and goes stale invisibly. |
| **fixtures** | The single derivation of one entity's test fixture values (`internal/fixtures`), rendered twice — HCL and wire JSON — from one result so the two can never disagree. Its vocabulary is approved: a `Fixture` carries `Entries` (one `Entry` per supported attribute) and `Omissions` (one `Omission` per excluded attribute, with its reason); a `Form` (`ConfigMinimal`, `ConfigMaximal`, `ResponseMinimal`, `ResponseMaximal`) selects which entries a rendering carries; `NamePrefix` (`tfpfgen-test-`) marks every synthesised name-bearing string. |
| **step kind** | One audit derivation rule's output, named for what the step does. The set is closed, twelve strong, spelled identically in Go (`Step*`) and in plan JSON: `createMinimal`, `readWithRetry`, `readConsecutive`, `updateField`, `deleteWithConfirmation`, `createMaximal`, `omitRequired`, `undocumentedEnumValue`, `undeclaredSpecField`, `createPerEnumValue`, `read`, `cleanupDelete`. |
| **outcome** | How far the audit got with one claim. The set is closed, four strong, spelled identically in Go (`Outcome*`) and in observation JSON: `confirmed`, `inconclusive`, `blocked`, `timeoutExhausted`. Despite its name's emphasis on time, `timeoutExhausted` covers every exhausted run budget alike — request, live-object and time. |
| **claim** | One thing the audit believes about an API before the live run settles it: read off the document's structure or mined from a field description, carried with its **provenance**. `strategy.Claim`; its kinds are spelled exactly as the **observation** kinds they become, so a claim and the finding it turns into share a name. A confirmed claim is an observation; an unconfirmed one is `inconclusive`. |
| **probe** | The single live request that would confirm or refute one claim: which **step kind** exercises it, the field it targets, the gate value it pins, and the answer that would count as confirmation. `strategy.Probe`. |
| **request fields** | The list of field names one create body will carry, with the **synthetic value rules** for inventing each value. Two per **variant** — the smallest body that should work and the widest. Values are invented at send time, never here. |
| **synthetic value rules** | Per field, what a made-up value must satisfy: type, format, pattern, enum members, declared example, declared default, numeric bounds. Read off the document; the executor builds the value live. |
| **correct** | What the adaptive executor does to a request body the API refused: read the refusal, make one **requestAdjustment**, retry. Spelled `correctBody` in code, qualified deliberately — a **correction** is a patch to the document, a different act at a different stage. A refusal it cannot act on is `uncorrectableRefusal`. |
| **gate** | The field whose value decides which other fields an API accepts — a required enum, an optional enum or a boolean, ranked in that order. Also, separately, the English verb: a check that refuses passage (the drift gate, the coverage gate). Both senses are approved; they are different parts of speech and no reader confuses them. |
| **edge** | One conditional rule between fields, of the kinds `validWhen`, `validConfiguration`, `dependsOn` and `mutuallyExclusive`. Counted on the audit summary as `edgesConfirmed` and `edgesInconclusive`. |
| **the prefix pass** | The half of **cleanup** that sweeps every reachable collection deleting objects whose names carry the run's prefix, as opposed to the half that deletes by id from an **activity ledger**. Counted separately on the cleanup summary because something getting past a ledger is worth distinguishing. |
| **preflight** | A cheap check run before an expensive thing, which stops early. One idea at three stages: `config validate` before anything credentialed runs, the audit's tenant check before its first create, and the release job before publishing. |
| **grammar** | The fixed sentence form a refusal takes, so a parser can pull the offending field out of it with one expression. One contract described from both ends: the **stub** API emits it, the executor parses it. |
| **stub** | A hand-written stand-in for a real component, used as test input. Covers the **test API server**, `sdkgen`'s fake generator binaries, and the curated fixture's hand-written SDKs. |
| **prenormalise** | The six rewrites every SDK generation applies to a copy of the revised document before the backend runs: strip schema defaults, collapse single-member anonymous `allOf`, widen byte-array collections, reduce unions, drop unacceptable error content, extract request-body enums to named components. Each answers a standing generator behaviour rather than one document's mistake, so they are built in rather than committed as corrections. Output at `spec/revised.prenormalised.yaml`, never committed. |
| **co-managed entity** | One of several generated entities that write to the same underlying API collection. Each one's schema description carries a fixed note saying so, because managing one remote object through more than one of them causes drift. |
| **curated fixture** | The committed fictional OpenAPI document under `testdata/curated`, plus one hand-written **stub** SDK per **backend**, driven through the real verbs. The chain's offline end-to-end gate. |
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
| **listWrapper** | The observation kind recording whether a collection response wraps its items under a key of an object, and which key. Entity-level; read from the live response body, never from the document. Extension key `x-tfpfgen-list-wrapper`, compiled onto the entity's list operation; derivation reads it in preference to the list response schema, which is exactly what the observation exists to contradict. |
| **listPagination** | The observation kind recording the pagination style a collection response advertises: `cursor`, `offset`, `page` or `none`. Entity-level; read from the live response body. Extension key `x-tfpfgen-list-pagination`. Separate from **listWrapper** because wrapping and pagination are unrelated facts about one response — an API can change how it pages without changing how it wraps. |

## Fixed spellings

- Config file: `tfpfgen.yaml` (schema owned by `internal/config`).
- Secret roles: `TFPFGEN_AUTH_TOKEN`, `TFPFGEN_AUTH_CLIENT_ID`,
  `TFPFGEN_AUTH_CLIENT_SECRET`, `TFPFGEN_AUTH_USERNAME`,
  `TFPFGEN_AUTH_PASSWORD`, `TFPFGEN_AUTH_APP_ID`,
  `TFPFGEN_AUTH_APP_PRIVATE_KEY`.
- OpenAPI extensions: `x-tfpfgen-*`.
- Approved extension values:
  `x-tfpfgen-update-style: patch-merge | put-full | replace-only`;
  `x-tfpfgen-list-wrapper: {wrapped: true | false, key: <wrapping key,
  wrapped only>}`;
  `x-tfpfgen-list-pagination: cursor | offset | page | none`;
  `x-tfpfgen-identifier-property: <property name>`, on a read operation.
- The full approved `x-tfpfgen-*` set. Every key is spelled as the
  **observation kind** that writes it, in kebab case, so one fact never has
  two names because it crossed a serialisation boundary:
  `-immutable`, `-required-when`, `-read-after-write`, `-update-style`,
  `-delete-not-found-ok`, `-values`, `-volatile`, `-server-forced`,
  `-server-default`, `-ignored-on-update`, `-valid-when`, `-depends-on`,
  `-mutually-exclusive`, `-valid-configuration`, `-list-wrapper`,
  `-list-pagination`, `-identifier-property`.
- The observation kinds, closed and spelled identically in Go (`Kind*`) and
  in observation JSON: `writable`, `immutable`, `requiredByAPI`,
  `requiredWhen`, `serverDefault`, `derivedDefault`, `normalisation`,
  `ignoredOnUpdate`, `serverForced`, `volatile`, `values`, `updateStyle`,
  `deleteNotFoundOK`, `readAfterWrite`, `undocumentedFieldInSpec`,
  `validConfiguration`, `validWhen`, `dependsOn`, `mutuallyExclusive`,
  `listWrapper`, `listPagination`, `identifierProperty`.
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
  resource the bindings or emission exclude takes its list resource with
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
  the only object there is. Its path names no item, so every parameter on
  it addresses a parent and becomes an addressing attribute.
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
  referencing no component excludes the whole union rather than half of it.
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
- **normalisation kind** — the relation between a value sent and the
  spelling the API stores it in, recorded on the property as
  `x-tfpfgen-normalisation` from a confirmed `normalisation` observation:
  `case-folded`, `trimmed`, `extended` (the answer carries the value inside
  a longer spelling — a scheme, a port, a unit), `same-instant` (a
  timestamp respelt) or `reordered` (a list in another order). Generated
  state keeps the configured spelling when the answer is that form of it,
  so a host the API answers with its port is not drift; a `date-time`
  property answered outside RFC 3339 also loses its format, because the SDK
  cannot read the answer through it.
- **addressing attribute** — a generated attribute that exists to fill an
  operation's path parameter rather than to carry a field of the object.
  Every path parameter above the item key becomes one: required, spelled
  from its wire name, in path order ahead of the id, and forcing
  replacement, because an object does not move to another parent in place.
  A parent the request or response body already declares is left as the
  body declares it. A parent the document spells `id` cannot take that
  name — `id` is the resource's own identity — and is named after the
  entity it addresses (`template_id`), keeping the parameter's spelling as
  its wire name. Addressing attributes and the `id` survive binding with
  no SDK field behind them — they address the object rather than describe
  it, so no model carries them. A fixture for a resource under a parent
  the provider emits carries the parent's minimal block beside its own and
  takes the identifier from it, in both suites.
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
- Audit entity flag: `--entity <key>` on `tfpfgen audit run`, repeatable,
  narrows the run to the named entities and the parents their paths embed;
  every other entity is listed as skipped with the flag as the reason.
- Audit force flag: `--force-api-audit` on `tfpfgen audit run` proceeds
  despite foreign objects beyond the object budget in the tenant. There is
  no consent environment variable: the audit creates and deletes real
  objects, running it only against sandbox/non-production tenants is the
  operator's responsibility, and the toolkit does not police it.
- Audit plan tokens: `<runid>` is the run-id placeholder execution
  substitutes into synthesised names; `${VAR}` marks an operator input
  read from the named environment variable at execution time;
  `$created:<entity>` is the id of an object the audit itself created;
  `$borrow:<collection path>` is the id of an object the API already
  serves at that path, read once per run — the value synthesis binds to a
  field whose name says it references another object.
- Operator environment variables a generated provider reads:
  `<PROVIDER>_*` — the uppercased provider name, bare, e.g.
  `THOUSANDEYES_API_TOKEN`. The earlier `TF_<PROVIDER>_*` spelling is
  retired: published providers spell these `AWS_`, `GOOGLE_`,
  `CLOUDFLARE_`, and an operator reaching for `THOUSANDEYES_API_TOKEN` is
  reaching for the name every other provider taught them. Still distinct
  from the pipeline's `TFPFGEN_AUTH_*` secrets, which only the toolkit
  reads.
- Provider block attributes: `endpoint`, `api_token`, `username`,
  `password`, `client_id`, `client_secret`, `token_url`, `app_id`,
  `app_private_key`, `request_timeout`, and the `client_options` block.
  `app_id` and `app_private_key` are the GitHub App credentials; they match
  `client_id`/`client_secret` in shape, and their `<PROVIDER>_APP_ID` and
  `<PROVIDER>_APP_PRIVATE_KEY` environment fallbacks follow the operator
  convention above.
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
  `FrameworkToAPI*`. In emitted service code the two directions are
  `MapRemoteStateToResource` / `MapRemoteStateToDatasource` (the API's answer
  onto the framework model) and the `construct*` family (the plan onto the
  SDK request body).
- Emitted provider-core vocabulary: `APIError` and `Describe` in the errors
  package; `LifecycleMethod`; `StateContainer`, `CreateResponseContainer`,
  `UpdateResponseContainer` and `ConsistencyPredicate` in the read-after-write
  loop; `MockBaseURL` and the mock `Registry`; `RemoteObjectCheck`,
  `CheckExists` and `CheckDestroyed` in the acceptance harness;
  `SharedCollectionNote` for a **co-managed entity**; `tfpfgen_run`, the
  `random_string` whose value suffixes synthesised names in every generated
  acceptance fixture.
- Go-idiomatic acronym casing in generated names: known acronyms uppercase
  whole in Pascal/camel spellings (`HTTPServer`, `APIKey`), and a leading
  acronym lowers whole in camel (`id`, `apiKey`). The acronym table lives in
  `internal/intermediate_representation/naming.go`; additions go through the
  repository owner.
- **emittance report** — the committed page saying how the pinned document
  became this provider, at the provider repo root as
  `generated_provider_<name>.html`. It carries the run's causal chain, not a
  history across runs: what the document was, what prenormalising changed
  before the backend read it, and then, under the document's own tags, each
  entity's journey from a path to what shipped — with every loss shown once
  as the fact behind it and everything that fact cost. Derived like any
  other generated file: manifest-covered, byte-compared by `provider
  verify`, and carrying no timestamp and no count of anything outside the
  run, because either would fail that gate on every run. It is a view of the
  same records `unsupported.json` holds, never a second place an exclusion is
  recorded.
- **unsupported.json** — the committed record of everything generation
  excluded, at the provider repo root: `{formatVersion, unsupported: [{terraformBlockType,
  entity, attribute, service, tag, stage, reason}]}`. The subject is fields
  rather than one rendered sentence, so a reader grouping exclusions never
  parses prose to do it. `entity` is the entity key and `attribute` its
  dotted path beneath it, empty when the whole entity was excluded. `terraformBlockType` is
  `resource | datasource | list_resource | action`, empty for an entity
  excluded before it became any of them. `service` and `tag` are where it
  belongs — the service area derived from its path, and the group the
  document places it in — carried so an entity that became nothing can still
  be grouped with the ones that did. `cause` is the fact behind
  the exclusion — a code from a closed set per stage, and the subject it is
  about, such as the SDK type that carries none of an entity's fields. Two
  exclusions belong to one cause when both match, so consequences of one fact
  group by an exact comparison rather than by a guess at which prose reasons
  mean the same thing. `stage` is the closed set `configuration |
  classification | derivation | binding | emission`, naming which decision
  excluded it. The first three are separate because their remedies are: an
  entry the operator wrote, a shape the document does not offer, and an
  attribute the derivation will not guess at. Derived
  content like any other: manifest-covered and byte-compared by `provider
  verify`. Generation never fails on it — one entity must not take the whole
  provider with it — the point is that an exclusion appearing or disappearing
  is a line in a generation pull request rather than a line in a CI log
  nobody reads.
