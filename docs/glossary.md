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
| **correction** | One committed correction to the imported OpenAPI document: RFC 6902 operations plus a required justification and an optional evidence pointer to an observation. Lives in `spec/corrections/`; proposed ones await a human in `spec/corrections/proposed/`; rejected ones leave a marker in `spec/corrections/rejected/`. |
| **revise** | To fold observations into proposed corrections and apply accepted ones — `tfpfgen spec revise`. The spec is revised based on audit observations; the output is the revised spec (`spec/revised.yaml`), the single source of truth for all generation. |
| **import** | To pin the upstream OpenAPI document by hash — `tfpfgen spec import`. The imported document is immutable evidence of what the vendor published. |
| **validate** | The offline preflight — `tfpfgen config validate`: tfpfgen.yaml is well-formed, the auth method's secrets are present, tool pins match. Dies in seconds, before anything credentialed runs. |
| **generate** | Code generation — `tfpfgen sdk generate`, `tfpfgen provider generate`. Every generated file carries a DO-NOT-EDIT header and a manifest entry. |
| **verify** | The drift gate — `tfpfgen sdk verify`, `tfpfgen provider verify`: regenerate into a temporary tree, byte-compare, fail on any difference. |
| **cleanup** | Deleting the live test objects an audit created, matched by name prefix — `tfpfgen audit cleanup`. Runs automatically at the start and end of every audit, and standalone on demand. |
| **inputs** | The small optional committed file of operator-supplied values the audit cannot synthesize (a valid value for an example-less field, an existing parent object's id): `audit/inputs.json`. Its absence degrades gracefully — the audit covers what it can. |
| **authored** | A committed data path generation may never write: tfpfgen.yaml, corrections, inputs. Enforced by the manifest, not by convention. There are no authored *code* files — provider repos are 100% generated code. |
| **manifest** | The ledger of every derived file (path, digest, source, origin) and every authored path. `manifest.json` at the provider-repo root. |
| **quirkserver** | The deliberately-misbehaving stub API that serves as offline ground truth for audit logic and as the fake live API in pipeline rehearsals. |
| **corpus** | Third-party OpenAPI documents pinned by SHA-256 and fetched at test time, never vendored. |
| **backend** | An SDK generator behind the common interface: `kiota` or `openapi-generator`. Exactly one per provider repo. |

## Fixed spellings

- Config file: `tfpfgen.yaml` (schema owned by `internal/config`).
- Secret roles: `TFPFGEN_AUTH_TOKEN`, `TFPFGEN_AUTH_CLIENT_ID`,
  `TFPFGEN_AUTH_CLIENT_SECRET`, `TFPFGEN_AUTH_USERNAME`,
  `TFPFGEN_AUTH_PASSWORD`, `TFPFGEN_AUTH_APP_ID`,
  `TFPFGEN_AUTH_APP_PRIVATE_KEY`.
- OpenAPI extensions: `x-tfpfgen-*`.
- Shared workflows, stage-numbered: `10-generate.yml`, `20-ci.yml`,
  `30-acceptance.yml`, `40-docs.yml`, `50-release.yml`.
- Generation branch in provider repos: `tfpfgen/run-<id>`.
