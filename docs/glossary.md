# Glossary

Every domain noun this project uses has exactly one meaning, and this table is where
those meanings live. A term not in this table may not be introduced in docs or code
without adding it here first.

| Term | Meaning |
|---|---|
| OpenAPI document | The upstream API description as its vendor publishes it; never called a spec or a specification. |
| snapshot | A pinned, committed copy of an OpenAPI document, fetched by `openapi fetch` and stored under `openapi/` so every derivation is reproducible. |
| blueprint | The committed JSON intermediate representation the generator builds a provider from — schema plus bindings, wire mappings and observed behaviour. |
| draft | A blueprint or scenario not yet fit for the pipeline, held under a `.draft.json` name the pipeline's loaders cannot open. |
| promote | To rename a draft to its canonical filename, and nothing else — a git-visible act with no content change. |
| adopt | To fold values from a probe scenario into blueprint hints (`blueprint merge -adopt-scenarios`); adopted hints refresh on re-merge, hand-written ones are never touched. |
| scenario | The per-resource probe worksheet (`<key>.scenario.json`, `probe.Scenario`) saying what a probe run sends and in what order. |
| payload | A request body a scenario or probe sends to the API; distinct from a fixture, which is test input. |
| fixture | A test input owned by the test suite — `accFixture` hints, the generated `minimal.tf`/`maximal.tf`, Go `testdata`. |
| probe | The subsystem — and one unit within it — that exercises a live API's lifecycle to observe what it actually does. |
| recording | The committed evidence tree one probe run leaves under `recordings/`: scenario, facts, subject, metadata, report, rehearsal and interactions. |
| cassette | The replayable HTTP capture inside a recording: `metadata.json` plus the `interactions/` directory. |
| interaction | One captured HTTP request/response pair in a cassette. |
| fact | One observation about one field or operation, with a confidence and the probe that produced it. |
| static facts | Facts derived from the pinned SDK's own types with no HTTP (`bindings facts`, committed as `static.facts.json`). |
| note | One reported line in a loss or draft report — the unit of "reported, never silent"; the spec bridge's note type is `spec.Loss`. |
| confidence | How well-evidenced a fact is: `observed` (seen directly), `corroborated` (seen more than once or from two angles), `inferred` (follows from responses plus an assumption), `suspected` (one ambiguous observation; reported, never written). |
| rehearsal | The probe pass that sends the derived acceptance bodies before acceptance does, so a wrong derivation fails in the probe rather than in a test run. |
| rehearsal fixpoint | The loop that re-derives payloads from each round's new facts until another round would change nothing about what is sent. |
| subject | The wire-level description of the resource under probe — key, path templates, JSONPaths — with no Terraform naming in it. |
| guard | The sandbox-admission table that authorises mutation: every static condition reported together, runtime assertions only after those pass. |
| grant | The unforgeable token the guard issues; a mutating session cannot be constructed without one. |
| ledger | The durable, append-only record of every object a probe run created, so the sweeper can find them even when a create never resolved an ID. |
| sweep | The probe verb that finds and deletes objects a previous run stranded, built from the blueprint alone. |
| orphan | A generated file the blueprints no longer produce; reported by default, deleted only with `-clean`. |
| batch | One group of resources taken through the pipeline together. |
| generate | The user-facing operation that turns blueprints into provider code (`provider generate`); *render* is internal template execution only, and *emit* is not a word this project uses. |
| fileset | Everything one generation run produces, as a value (`generate.Fileset`) — what gets written, checked and recorded in the manifest. |
| postcheck | The battery `provider generate` runs after writing: `go build`, tfplugindocs, `terraform fmt`. |
| manifest | `.tfpfgen/manifest.json`, the record of what the last generation run produced; how orphans are found. |
| drift | A generated file on disk differing from what the blueprints produce. |
| check | A verification that can fail a run: a CI job, or a `-check` mode such as `provider generate -check`. Never "gate". |
| scaffold template | The template for a file written once and then owned by a person — `modify_plan.go`, `predicate.go`, `state_upgrade.go`. |
| reference provider | `deploymenttheory/terraform-provider-microsoft365`, the ~168-resource hand-written provider this toolkit's conventions are calibrated against. |
| sandbox profile | The operator-authored file that tells the guard what this tenant permits; never "probe profile". |
| spec | HashiCorp's Provider Code Specification (codegen-spec v0.1), always and only; read and written by `spec export` / `spec import` via `internal/spec`. |
| binding | The blueprint section that names which SDK service and methods a block's operations call, as data the generator never branches on. |
| dialect | The SDK calling convention a binding assumes — `restyService` today, `kiotaFluent` reserved. |
| wire | How one attribute crosses the boundary: its JSON path, SDK field and type, and the expand/flatten conversions. |
| behaviour | What the API actually does about a field, as observed — writability, immutability, server defaults, returned-on-read — as opposed to what its document claims. |
| hooks | The blueprint field naming which hand-written files to scaffold for a resource. |
| facet | A capability that is part of a resource rather than a sibling block — identity, and the list facet behind `terraform query`. |
| candidate | A synthetic value from outside an attribute's documented set, sent to learn whether that set is closed. |
| TypeName | The generated constant every resource, data source, ephemeral and action registers under — the full Terraform type name. |

## Whose word wins

HashiCorp's words for HashiCorp's concepts: `planModifiers`, `ComputedOptionalRequired`
and *spec* are theirs and are spelled their way. The SDK's words for SDK symbols: the
`dashboards_filters` package and its service names keep the SDK's spelling even where
our blueprint keys differ. GitHub's words for GitHub things: *artifact* upload and
status *checks* keep GitHub's spelling. Everything that is this project's own prose is
British English — behaviour, artefact, authorise.
