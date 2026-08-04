# CLI reference

`tfpluginframeworkgen` is one binary with subcommands, dispatched with the standard
library's `flag` package. Each subcommand owns its own `FlagSet`, so there is no
global flag namespace for two subcommands to collide in, and `tfpluginframeworkgen emit
-v` reads naturally rather than requiring flags before the command.

> This page is maintained by hand and checked against `tfpluginframeworkgen <cmd> -h`.
> The authoritative text is the binary's own help output; if the two disagree,
> the binary is right and this page is stale.

## Commands

Listed in pipeline order — the order an author walks them, which is also the order
`help` prints them.

| Command | Purpose | State |
|---|---|---|
| `specs` | fetch and snapshot an upstream OpenAPI document | **built** |
| `ingest` | infer a provider blueprint from an OpenAPI snapshot | **built** *(resources only; data sources, actions and the provider block are hand-authored)* |
| `blueprint` | validate, diff or list blueprints | planned *(validation itself is built and runs on every load)* |
| `probe` | exercise a resource's lifecycle; record, replay, verify or sweep | **built** *(both tiers plus the rehearsal; a mutating run needs `--allow-mutations` and a sandbox profile)* |
| `merge` | fold probe facts into a blueprint | **built** |
| `emit` | render a provider from blueprints, then postcheck it | **built** |
| `verify` | fail if the committed provider has drifted | **built** |
| `scaffold` | write a blank resource from the archetype | planned *(the archetype exists; `emit` scaffolds it via `hooks`)* |
| `bindings` | check blueprint SDK bindings against the pinned SDK; derive static facts | **built** |
| `interop` | export or import codegen-spec v0.1 JSON | **built** *(import is resources-only and writes drafts)* |
| `version` | print the version | **built** |

A planned command is registered and documented but not implemented: running one
says so and exits non-zero. Registering the full surface up front means `help`
describes the intended pipeline from the first commit, and reaching for a missing
stage gets a straight answer rather than "unknown command".

Two pieces of pipeline behaviour live *inside* commands rather than as commands of
their own, and it is worth saying so because the source layout suggests otherwise:
the **rehearsal fixpoint** (`cmd/tfpluginframeworkgen/rehearse.go`) is a behaviour
of `probe -mode record`, and **plan promotion** (`cmd/tfpluginframeworkgen/promote.go`)
is a behaviour of `merge -promote-plans`. There is no `rehearse` or `promote`
subcommand.

## Global flags

Accepted by every subcommand.

| Flag | Purpose |
|---|---|
| `-v` | verbose output |
| `-q` | suppress progress output |
| `-C <dir>` | change to this directory before running |
| `-config <path>` | provider generation config (default `.tfpluginframeworkgen/config.yaml`) |

## Exit codes

Scripts branch on these, so they are a contract rather than an implementation
detail.

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | failure — including drift, and a mismatched binding |
| `2` | invalid input: a usage error, or an unusable plan or config |
| `3` | gating refused (`probe`) |
| `4` | budget exceeded (`probe`) |
| `5` | cleanup left orphaned objects (`probe`) |
| `6` | replay mismatch (`probe`) |
| `7` | redaction check failed (`probe`) |

A malformed flag exits `2`, not `1`: a caller mistake must not be reported the
same way as the tool failing.

Exit `2` has a second meaning, and it is worth stating rather than hiding: a panic
is captured, reported with its stack, swept after, and then re-raised — and the Go
runtime exits `2` for a panic. So exit `2` means a usage error (no stack in the
output) or a bug (a stack). Re-raising is still right: a panic is a bug and the
stack is worth more than a tidier code.

Where several of `3`–`7` apply at once — a run can exceed its budget, sweep, and
still leave an orphan — the precedence is **7 > 5 > 3 > 4 > 6 > 1**. It is a table
rather than a walk over the error tree, because the first match in a tree walk is
not the most serious condition, and which code CI sees must not depend on the order
the errors happened to be joined in.

---

## `specs`

Fetches the upstream OpenAPI document and pins it as a new snapshot.

```
tfpluginframeworkgen specs [-url URL] [-output-dir DIR] [-dry-run]
```

| Flag | Default | Purpose |
|---|---|---|
| `-url` | the latest snapshot's recorded source | the document to fetch |
| `-output-dir` | — | snapshot root, e.g. `openapi-specs/thousandeyes` (required) |
| `-dry-run` | `false` | fetch and report, but pin nothing |

The refresh loop is the point: with `-url` omitted, the source comes from the
latest snapshot's own metadata, so `specs -output-dir openapi-specs/thousandeyes`
re-fetches from wherever the last pin came from. No one has to remember the URL,
because the snapshot that would go stale is the thing that records it. `-url` is
only required for the very first snapshot.

A snapshot is a directory named `<version>-t<millis>` holding the document
(`api.yaml`) and `metadata.json` (source URL, digest, fetch time). An upstream
document identical to the latest snapshot pins nothing and exits `0` — the weekly
refresh should be quiet when there is nothing to review.

The fetch is bounded at two minutes: a specification is a couple of megabytes, and
anything slower is a network problem worth hearing about.

## `ingest`

Infers draft blueprints (and optionally probe-plan worksheets) from a pinned
OpenAPI snapshot.

```
tfpluginframeworkgen ingest [-spec-root DIR] [-snapshot NAME] [-only TAG] [-list]
```

| Flag | Default | Purpose |
|---|---|---|
| `-spec-root` | `openapi-specs/thousandeyes` | directory holding pinned snapshots |
| `-snapshot` | the newest | snapshot to read |
| `-spec` | — | read this document directly, bypassing the snapshot store |
| `-list` | `false` | list what the document offers and exit |
| `-only` | — | restrict to candidates whose tag or key contains this; comma-separate several |
| `-all` | `false` | include candidates that cannot become resources or data sources |
| `-out` | — | write inferred blueprints under this directory |
| `-plan-drafts` | — | also scaffold a `KEY.probe.plan.draft.json` worksheet per resource under this directory |
| `-provider` | `thousandeyes` | provider name, which prefixes every resource type |
| `-api-version-dir` | `v7` | version directory generated packages live under |
| `-sdk-service-root` | the ThousandEyes SDK's | import prefix the SDK's service packages live under |
| `-sdk-accessor` | `r.client.API` | expression reaching a service from the resource receiver |

`ingest -list` is the survey: it reports every candidate the document offers and
why the ineligible ones are ineligible. The write path produces **drafts** — a
schema and a best-guess binding, which a human then curates (presence, hints,
denials, sweep configuration) before the file earns its non-draft name. The
`-plan-drafts` worksheets serve the same role for probe plans: every field listed
with a place to put fixture values and candidates, nothing invented.

## `probe`

Exercises a live API and writes down what it observed.

```
tfpluginframeworkgen probe [-mode record|replay|verify|sweep] -blueprint DIR
    [-resource KEY] [-only PROBE] [-list] [-plan FILE] [-plan-dir DIR]
    [--allow-mutations] [-profile FILE] [-force] [-no-rehearse] [-rederive]
    [-evidence DIR] [-provider NAME]
```

| Flag | Purpose |
|---|---|
| `-mode` | `replay` (default), `record`, `verify` or `sweep` |
| `-blueprint` | blueprint file or directory (required) |
| `-resource` | probe one resource, by blueprint key |
| `-only` | run one probe, by name |
| `-list` | print the catalogue with its worst-case cost, and exit |
| `-plan` | probe plan: the fixtures and candidate values a probe cannot discover |
| `-plan-dir` | directory of per-resource plans, `KEY.probe.plan.json`; defaults to the blueprint directory |
| `--allow-mutations` | permit probes that create, update and delete |
| `-profile` | sandbox profile; defaults to `.tfpluginframeworkgen/sandbox/<provider>.json` |
| `-force` | record over evidence that is already committed |
| `-no-rehearse` | skip the rehearsal fixpoint after the standard mutating probes |
| `-rederive` | with `-mode replay`: rewrite `facts.json` from the committed cassette, no network |
| `-evidence` | root of the committed evidence (default `probe-evidence`) |
| `-provider` | provider name for the evidence path; defaults to the blueprint's |

The four modes:

- **`record`** talks to the live API and freezes everything it saw — cassette,
  facts, the plan and subject as probed, and the rehearsal's derived bodies.
- **`replay`** re-runs the probes against the committed cassette. No network, no
  credentials. This is the default because the safe mode should be what you get by
  typing less.
- **`verify`** replays and then compares the derived facts against the committed
  `facts.json`, failing on any difference — the CI drift gate for evidence. It
  compares facts even when the replay reproduces an error the recording ended on,
  because a reproduced failure with identical facts is a faithful replay.
- **`sweep`** deletes every leftover object the ledger still holds an intent for,
  and nothing else.

`-resource` and `-only` are separate axes and deliberately not one flag: probing one
resource with the whole catalogue and probing every resource with one protocol are
both things an operator wants.

A mutating record ends with the **rehearsal**: `write.rehearsal` walks both
lifecycle directions (create minimal → update to maximal → downgrade → delete, and
the reverse), and the command then re-derives fixtures from the merged evidence and
re-runs it until the derived bodies stop changing. The converged bodies are frozen
as `rehearsal.json` beside the cassette. `-no-rehearse` skips this for a cheap
targeted re-record; evidence recorded that way carries no rehearsal facts. See
[fixtures-and-rehearsal.md](fixtures-and-rehearsal.md).

`-rederive` exists for toolkit upgrades: when fact derivation itself changes, the
committed cassettes are re-read and `facts.json` files are rewritten without
touching the network, so better inference never requires re-probing a live API.

Credentials come from the environment and nowhere else — `TFPFGEN_PROBE_ENDPOINT` and
`TFPFGEN_PROBE_TOKEN`. A flag would put the token in shell history and in the process
table; the profile is a file that gets written down, and the gate refuses one that
contains the token's value.

`-list` needs no credentials, no cassettes and no network:

```
$ tfpluginframeworkgen probe -blueprint blueprints/thousandeyes -resource tag -list
```

A mutating run needs `-mode record`, `--allow-mutations`, and a sandbox profile that
passes every gate condition. A refusal lists all of them at once and exits `3`:

```
$ tfpluginframeworkgen probe -blueprint blueprints/example -resource tag \
    -mode record --allow-mutations
tfpluginframeworkgen: mutating probes refused: 5 condition(s) were not met:
  - sandbox: the profile does not declare sandbox: true
  - sandboxEvidence: sandboxEvidence is 2 characters, and at least 24 are required; …
  - namePrefix: namePrefix "tf" is shorter than 8 characters; …
  - plan: the plan declares no fixtures; …
  - noSnapshotOverwrite: evidence for this plan is already committed; …
```

See [probing.md](probing.md) for the gate, the ledger, the sweeper and the budgets.

## `merge`

Folds a probe run's facts into the blueprint they describe.

```
tfpluginframeworkgen merge -blueprint DIR -facts FILE [-strategy annotate|apply]
    [-check] [-accept-conflicts] [-promote-plans DIR] [-snapshot-id ID]
    [-github-summary PATH]
```

| Flag | Default | Purpose |
|---|---|---|
| `-blueprint` | — | blueprint file or directory (required) |
| `-facts` | — | facts JSON to fold in (required) |
| `-strategy` | `annotate` | `annotate` writes behaviour and descriptions; `apply` may also widen presence |
| `-check` | `false` | write nothing and exit 1 if merging would change anything |
| `-accept-conflicts` | `false` | suppress the conflict exit code; conflicts are still reported and still not applied |
| `-promote-plans` | — | directory of `KEY.probe.plan.json` files whose fixture values are promoted into `accFixture` wire hints, for attributes the generator refuses to derive |
| `-snapshot-id` | the facts file's directory | identifies the evidence in the description marker |
| `-github-summary` | — | append a summary here |

`annotate` is the default because most of what a probe learns lands in `behaviour`
and in the description marker block, neither of which changes the schema. `apply`
is for the facts that do — a field observed as server-populated widening to
`computed_optional`, for instance — and a fact whose application would *conflict*
with a curated declaration is reported and left alone under either strategy.

`-check` is the drift gate: CI re-merges every committed facts file and fails if
any blueprint would change, so evidence and blueprint cannot silently diverge. A
facts file with no facts (a rehearsal-only or read-only snapshot) is trivially
reflected and passes.

`-promote-plans` copies fixture values from the *plan* into `accFixture` wire
hints — but only for attributes the fixture generator refuses to derive itself,
only from the plan's first fixture (later fixtures probe variants, not the
canonical shape), and never over a hand-written hint. Static facts documents merge
through the same command with `-snapshot-id` naming the `static` channel; see
[probing.md](probing.md#static-facts).

## `emit`

Renders a provider from blueprints, then runs the postcheck battery over what it
wrote.

```
tfpluginframeworkgen emit -blueprint DIR -out DIR [-only NAME] [-dry-run]
```

| Flag | Default | Purpose |
|---|---|---|
| `-blueprint` | — | blueprint file or directory (required) |
| `-out` | — | provider root to write into (required unless `-list` or `-dry-run`) |
| `-only` | — | generate a single resource by key, for inspecting output |
| `-dry-run` | `false` | print the write plan and touch nothing |
| `-list` | `false` | list the files that would be written and exit |
| `-clean` | `false` | delete files the blueprints no longer produce |
| `-force` | `false` | overwrite files that are not marked as generated |
| `-skip-postcheck` | `false` | skip the post-emit battery; for tight inner loops only |

Building the plan touches nothing on disk, so `-dry-run` exercises the same code
path as a real run rather than approximating it.

`-force` exists for adopting an existing tree and is deliberately not the default:
a mistyped `-out` would otherwise destroy work with no way to recover it.

`-only` skips the provider-wide registration files, because a registration listing
a subset would not compile against a tree containing the rest. It also leaves the
manifest unchanged, since a partial inventory would make `verify` report every
unlisted file as an orphan.

Files whose content is already identical are reported as unchanged rather than
rewritten, so regenerating does not churn modification times.

### The postcheck battery

After writing, `emit` finishes the job with the same tools that gate the result in
CI, in order:

1. **`go build ./...`** in the output module — the tree must compile.
2. **`go generate .`** — regenerates `docs/` via tfplugindocs, but only when the
   module root carries a `go:generate` directive for it. Schema changes and their
   registry docs can never drift apart.
3. **`terraform fmt -recursive`** — formats the generated `.tf` fixtures, and then
   *fails if it rewrote anything*, because a rewrite means the generator produced
   unformatted HCL and that is a generator bug to fix, not output to keep patching.

The battery only runs when the output root is a Go module (`go.mod` present), so
emitting into a scratch directory for inspection stays cheap. `-skip-postcheck`
exists for tight inner loops; CI and any run before a commit should keep the
battery on — a skipped battery just moves the same failures to the gates.

## `verify`

Fails if the committed provider no longer matches its blueprints.

```
tfpluginframeworkgen verify -blueprint DIR -out DIR
```

| Flag | Default | Purpose |
|---|---|---|
| `-blueprint` | — | blueprint file or directory (required) |
| `-out` | — | provider root to check (required) |
| `-github-summary` | `$GITHUB_STEP_SUMMARY` | write a markdown summary here |

Three failure classes, reported separately because they have different causes and
different fixes:

- **drift** — a generated file differs from what the blueprints produce
- **missing** — a file the blueprints produce is not on disk
- **orphaned** — a file the blueprints no longer produce is still on disk

Orphans need `.tfpluginframeworkgen/manifest.json`. A tree without one says so rather
than silently reporting none, so "no orphans" is distinguishable from "could not
check".

CI uses `emit` plus `git diff` instead, which is strictly stronger: `verify` can
only compare what the blueprints produce against disk, so a manifest-less tree
hides orphans from it. `verify` is for local use, where a clean git tree cannot be
assumed.

## `bindings`

Type-checks a blueprint's SDK bindings against the SDK the provider will compile
against, and derives the static facts that live in the SDK's own source.

```
tfpluginframeworkgen bindings -blueprint DIR -module DIR [-facts-out FILE | -facts-check FILE]
```

| Flag | Default | Purpose |
|---|---|---|
| `-blueprint` | — | blueprint file or directory (required) |
| `-module` | — | a module whose `go.mod` pins the SDK (required) |
| `-facts-out` | — | write statically derived facts (zero-value unsendable) to FILE |
| `-facts-check` | — | re-derive static facts and fail when FILE differs — the drift gate for a committed static facts document |

`-module` is normally the generated provider's own directory. The toolkit's module
deliberately depends on no provider SDK, so it cannot be used — and resolving the
SDK from a module that genuinely depends on it is the point: it guarantees the
version checked is the version that will be compiled.

Checks the accessor field chain, service type, each operation's method, the
declared return arity against the method's real arity, result types, request and
response models, and every attribute's SDK field against the model it is read from
or written to.

```
tag: binding.service.accessor: "r.client.Tags" does not resolve:
  thousandeyes.Client has no field "Tags" (available: API, Transport)
```

`-facts-out` scans the request models' struct tags for value-typed fields with
`omitempty` — fields whose zero value the SDK is structurally unable to send — and
writes them as `zeroValueUnsendable` facts for `merge` to fold in. `-facts-check`
is the same derivation as a drift gate: bump the SDK pin and CI tells you if the
facts document is stale. The scan also *warns* about value-typed request structs
that no attribute sets, because such a struct always serialises as `{}` on the
wire; a required struct stays a value by design, so this is a warning and not an
error. See [probing.md](probing.md#static-facts).

## `interop`

Reads and writes HashiCorp's Provider Code Specification v0.1. Two verbs; see
[`interop.md`](interop.md) for what the format cannot carry, and why this exists at
all.

```
tfpluginframeworkgen interop export -blueprint DIR [-out FILE] [-only KEY] [-strict] [-report FILE]
tfpluginframeworkgen interop import -spec FILE -out DIR -provider NAME [flags]
```

### `interop export`

| Flag | Default | Purpose |
|---|---|---|
| `-blueprint` | — | blueprint file or directory (required) |
| `-out` | stdout | file to write |
| `-only` | — | restrict to one resource, by blueprint key |
| `-strict` | `false` | exit 1 if anything was coarsened or dropped |
| `-report` | — | write the downgrade notes as JSON |

Writes to stdout by default, so `interop export -blueprint … | jq` is the natural way
to look at one. The document is validated against upstream's embedded JSON schema
*before* it is written, so a mapping bug cannot leave a bad artefact on disk for CI to
diff against.

A heavily downgraded export is a success. The format has no way to express a CRUD
binding, and a tool that failed for that reason would never export anything; `-strict`
is for the caller who wants to assert that a *particular* blueprint is fully
expressible.

The downgrade report goes to stderr with `fmt`, not through the log package, so `-q`
cannot hide it. Uniform losses are aggregated per resource with a count.

```
1 resource(s), 0 data source(s), 17 attribute(s). 20 note(s): 6 dropped, 0 lossy, 14 info

dropped:
  resources[tag].binding: SDK call wiring has no counterpart, so a document exported
    from this blueprint cannot be emitted from
  resources[tag].attributes[*].wire: expand and flatten bindings have no counterpart,
    so the exported attributes carry no mapping to the SDK (23 affected)
```

### `interop import`

| Flag | Default | Purpose |
|---|---|---|
| `-spec` | — | codegen-spec v0.1 JSON to read (required) |
| `-out` | — | directory to write draft blueprints under (required) |
| `-provider` | — | registry provider name, which prefixes every Terraform type (required) |
| `-type-prefix` | `-provider` | type prefix, when it differs from the provider name |
| `-go-module` | — | the generated provider's Go module path |
| `-api-version-dir` | — | version directory generated packages live under, e.g. `v7` |
| `-service-group` | — | service grouping for the on-disk layout |
| `-list` | `false` | report what the document offers and exit |

Resources only. Data sources and the provider configuration schema are reported and
skipped.

Writes `*.blueprint.draft.json`, which `LoadDir` cannot see — so `emit` and `verify`
are structurally unable to open a blueprint that has a schema and no bindings.
Promoting a draft is a rename. The command prints the fields that must be authored
first, collapsed:

```
2 draft(s) written. 7 field group(s) must be authored before emission:

  provider.sdk.{dialect,modulePath,clientType}
  resources[tag].binding.service.{importPath,typeName,accessor}
  resources[tag].binding.{create,read,update,delete}
  resources[tag].attributes[*].wire.{sdkField,sdkGoType,expand,flatten}   (23)
```

## `version`

```
tfpluginframeworkgen version [-short]
```

---

For the full pipeline walked in order with these commands — including the record →
merge → emit loop and the CI gates each stage answers to — see
[onboarding-a-new-api.md](onboarding-a-new-api.md) and [gates.md](gates.md).
