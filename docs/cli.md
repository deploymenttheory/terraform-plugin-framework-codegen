# CLI reference

`tfpluginframeworkgen` is one binary with subcommands, dispatched with the standard
library's `flag` package. Each subcommand owns its own `FlagSet`, so there is no
global flag namespace for two subcommands to collide in, and `tfpluginframeworkgen emit
-v` reads naturally rather than requiring flags before the command.

> This page is maintained by hand and checked against `tfpluginframeworkgen <cmd> -h`.
> The authoritative text is the binary's own help output; if the two disagree,
> the binary is right and this page is stale.

## Commands

| Command | Purpose | State |
|---|---|---|
| `specs` | fetch and snapshot an upstream OpenAPI document | planned |
| `ingest` | infer a provider blueprint from an OpenAPI snapshot | planned |
| `blueprint` | validate, diff or list blueprints | planned |
| `probe` | exercise a resource's lifecycle; record or replay cassettes | planned |
| `merge` | fold probe facts into a blueprint | planned |
| `emit` | render a provider from blueprints | **built** |
| `verify` | fail if the committed provider has drifted | **built** |
| `bindings` | check blueprint SDK bindings against the pinned SDK | **built** |
| `scaffold` | write a blank resource from the archetype | planned |
| `interop` | export or import codegen-spec v0.1 JSON | **built** |
| `version` | print the version | **built** |

A planned command is registered and documented but not implemented: running one
says so and exits non-zero. Registering the full surface up front means `help`
describes the intended pipeline from the first commit, and reaching for a missing
stage gets a straight answer rather than "unknown command".

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
| `3` | gating refused *(reserved for `probe`)* |
| `4` | budget exceeded *(reserved for `probe`)* |
| `5` | cleanup left orphaned objects *(reserved for `probe`)* |
| `6` | replay mismatch *(reserved for `probe`)* |
| `7` | redaction check failed *(reserved for `probe`)* |

A malformed flag exits `2`, not `1`: a caller mistake must not be reported the
same way as the tool failing.

---

## `emit`

Renders a provider from blueprints.

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
against.

```
tfpluginframeworkgen bindings -blueprint DIR -module DIR
```

| Flag | Default | Purpose |
|---|---|---|
| `-blueprint` | — | blueprint file or directory (required) |
| `-module` | — | a module whose `go.mod` pins the SDK (required) |

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

## Worked example

The pilot, end to end:

```bash
# what would be written, without writing it
tfpluginframeworkgen emit -blueprint blueprints/thousandeyes -dry-run

# check every SDK symbol the blueprint names actually exists
tfpluginframeworkgen bindings -blueprint blueprints/thousandeyes -module pilot/thousandeyes

# generate
tfpluginframeworkgen emit -blueprint blueprints/thousandeyes -out pilot/thousandeyes

# the actual proof
cd pilot/thousandeyes && go build ./... && go test ./... && terraform plan

# what CI runs
tfpluginframeworkgen verify -blueprint blueprints/thousandeyes -out pilot/thousandeyes
```
