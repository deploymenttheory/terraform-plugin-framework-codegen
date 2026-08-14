# Glossary

Every domain term in this toolkit was individually approved by the
repository owner. A term not in this table may not be introduced — present
options to the owner first, record the decision here, then use it. The v1
vocabulary (probe, cassette, recording, scenario, blueprint, draft, merge,
sweep, doctor, facts, rehearsal, curate) is retired and may not reappear.

| Term | Meaning |
|---|---|
| **audit** | The credentialed stage that exercises a live API to learn its true behaviour — minimum and maximum valid configuration, field dependencies, value-conditional rules. `tfpfgen audit run`. The only stage that touches a network. |
| **observation** | One recorded finding of an audit: what the live API actually accepted or rejected, with a redacted request/response excerpt as proof. Committed per entity in `audit/observations/<entity>.observations.json`, stamped with the spec hash it was observed against. Deliberately not replayable. |
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
| **corpus** | Third-party OpenAPI documents pinned by SHA-256 and fetched at test time, never vendored. |
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
  omitted `pagination` reads as `none`.
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
  `TF_<PROVIDER>_*` — `TF_` then the uppercased provider name, e.g.
  `TF_THOUSANDEYES_API_TOKEN`. Distinct from the pipeline's
  `TFPFGEN_AUTH_*` secrets, which only the toolkit reads.
- Provider block attributes: `endpoint`, `api_token`, `username`,
  `password`, `client_id`, `client_secret`, `token_url`.
- Conversion catalog function families: `APIToFramework*` and
  `FrameworkToAPI*`.
- Go-idiomatic acronym casing in generated names: known acronyms uppercase
  whole in Pascal/camel spellings (`HTTPServer`, `APIKey`), and a leading
  acronym lowers whole in camel (`id`, `apiKey`). The acronym table lives in
  `internal/intermediate_representation/naming.go`; additions go through the
  repository owner.
