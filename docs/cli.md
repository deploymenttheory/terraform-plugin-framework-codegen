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
| `interop` | export or import codegen-spec v0.1 JSON | planned |
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
