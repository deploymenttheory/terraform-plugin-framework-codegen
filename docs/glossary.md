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
| **inputs** | The small optional committed file of operator-supplied values the audit cannot synthesize (a valid value for an example-less field, an existing parent object's id): `audit/inputs.json`. Its absence degrades gracefully — the audit covers what it can. |
| **authored** | A committed data path generation may never write: tfpfgen.yaml, corrections, inputs. Enforced by the manifest, not by convention. There are no authored *code* files — provider repos are 100% generated code. |
| **manifest** | The ledger of every derived file (path, digest, source, origin) and every authored path. `manifest.json` at the provider-repo root. |
| **quirkserver** | The deliberately-misbehaving stub API that serves as offline ground truth for audit logic and as the fake live API in pipeline rehearsals. |
| **corpus** | Third-party OpenAPI documents pinned by SHA-256 and fetched at test time, never vendored. |
| **backend** | An SDK generator behind the common interface: `kiota` or `openapi-generator`. Exactly one per provider repo. |
| **intermediate representation** | The ephemeral, never-committed derivation (`internal/intermediate_representation`) recomputed from the revised spec and config on every generation run; its model vocabulary (Model, Resource, Datasource, ListResource, Action, AttributeTree, Presence, Op, Names) is approved. |
| **binding** | The dialect-neutral mapping from one intermediate-representation entity onto the generated SDK's surface (`internal/sdkbind`): finished call expressions, accessor names, model types. Drafted by a per-backend binder, resolved against the real SDK with go/types. Its vocabulary (Bindings, Call, FieldAccess, Segment) is approved. |
| **prune** | To resolve drafted bindings against the generated SDK and delete whatever the SDK cannot carry, recording the SDK's reason for each deletion. A spelling is repaired only where the SDK admits exactly one answer — never invented, never widened. |
| **provider-core** | The shared plumbing the toolkit emits into every generated provider: the client, crud retry, error semantics, the conversion catalog, schema helpers, and the test harness. An emitted copy, not a shared library — every file is manifest-covered and regenerated wholesale. Templates live in `internal/templates/provider-core`. |
| **emit** | The render-and-write layer (`internal/emit`): it turns the provider-core and service templates plus finished context values into provider files and reports what it wrote as manifest entries. Its emission vocabulary is approved: `RenderServices` renders every entity's service files and answers a `ServiceFiles` (the files plus a `Registry` of registration lines); `Register` writes one slot's `Registrations` into a registry file at its sentinels; `RegistrySlots` is the fixed slot order. |
| **fixtures** | The single derivation of one entity's test fixture values (`internal/fixtures`), rendered twice — HCL and wire JSON — from one result so the two can never disagree. Its vocabulary is approved: a `Fixture` carries `Entries` (one `Entry` per supported attribute) and `Omissions` (one `Omission` per refused attribute, with its reason); a `Form` (`ConfigMinimal`, `ConfigMaximal`, `ResponseMinimal`, `ResponseMaximal`) selects which entries a rendering carries; `NamePrefix` (`tfpfgen-test-`) marks every synthesised name-bearing string. |
| **step kind** | One audit derivation rule's output, named for what the step does. The set is closed, twelve strong, spelled identically in Go (`Step*`) and in plan JSON: `createMinimal`, `readWithRetry`, `readConsecutive`, `updateField`, `deleteWithConfirmation`, `createMaximal`, `omitRequired`, `undocumentedEnumValue`, `undeclaredSpecField`, `createPerEnumValue`, `read`, `cleanupDelete`. |
| **outcome** | How far the audit got with one claim. The set is closed, four strong, spelled identically in Go (`Outcome*`) and in observation JSON: `confirmed`, `inconclusive`, `blocked`, `timeoutExhausted`. Despite its name's emphasis on time, `timeoutExhausted` covers every exhausted run budget alike — request, live-object and time. |

## Fixed spellings

- Config file: `tfpfgen.yaml` (schema owned by `internal/config`).
- Secret roles: `TFPFGEN_AUTH_TOKEN`, `TFPFGEN_AUTH_CLIENT_ID`,
  `TFPFGEN_AUTH_CLIENT_SECRET`, `TFPFGEN_AUTH_USERNAME`,
  `TFPFGEN_AUTH_PASSWORD`, `TFPFGEN_AUTH_APP_ID`,
  `TFPFGEN_AUTH_APP_PRIVATE_KEY`.
- OpenAPI extensions: `x-tfpfgen-*`.
- Approved extension values:
  `x-tfpfgen-update-style: patch-merge | put-full | replace-only`.
- Shared workflows, stage-numbered: `10-generate.yml`, `20-ci.yml`,
  `30-acceptance.yml`, `40-docs.yml`, `50-release.yml`.
- Generation branch in provider repos: `tfpfgen/run-<id>`.
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
- Naming helpers the intermediate representation exports for every
  emitter: `GoName` (the Pascal Go spelling, acronym-aware) and
  `TerraformName` (the snake_case terraform attribute spelling).
- Rejected-proposal marker: one JSON file per rejected proposal in
  `spec/corrections/rejected/`, shaped
  `{"observationID": "…", "reason": "…", "rejectedAt": "…"}`. A marker
  suppresses re-proposal of that observation permanently; deleting the
  marker is the only way back.
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
