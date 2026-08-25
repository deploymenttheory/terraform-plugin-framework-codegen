# terraform-plugin-framework-codegen

A community CLI tool `tfpfgen` that turns an OpenAPI 3 document into a complete
[terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework)
provider, zero touch. An operator supplies a spec URL and API credentials;
everything else cascades through a GitHub Actions pipeline:

```
config validate → spec import → audit run → spec revise → sdk generate → provider generate → verify → PR
```

An OpenAPI document tells you what an API's fields are *called*. It does not
tell you what makes a Terraform provider actually work. So the pipeline
**audits** the live API, minimum and maximum valid configuration, field
dependencies, value-conditional rules and records **observations**. Those
observations become proposed **corrections** to the spec (RFC-6902 operations
with a justification and a pointer to the observation that proves them). The
**revised spec** is the single source of truth from which both the SDK and the
provider are generated. Every generated file is exactly that, generated;
human judgment enters only as data: `tfpfgen.yaml`, accepted corrections, and
audit inputs.

Generation happens twice, and the second half is what makes the output real.
An SDK is generated first, by the configured backend. The provider is then
generated against that SDK and resolved onto it with `go/types`: every call
expression, accessor and model type is checked against the SDK that was
actually produced, and whatever it cannot carry is deleted with the reason
recorded. Nothing is emitted on the strength of what the document promised.

## Install

Go 1.26 or newer.

```
go install github.com/deploymenttheory/terraform-plugin-framework-codegen/cmd/tfpfgen@v0.7.0
```

Release archives for linux, darwin and windows on amd64 and arm64 are published
on the [releases page](https://github.com/deploymenttheory/terraform-plugin-framework-codegen/releases),
each with a SHA-256 checksums file. Nothing is signed.

A binary built from a source checkout reports its version as `dev`, which a
pinned pipeline treats as a refusal — install a tag to run the workflows.

## CLI

Noun-verb grammar. Every verb is a pure function of committed inputs except
`audit run` and `audit cleanup` - the only two that touch a network with
credentials.

```
tfpfgen config validate
tfpfgen spec import | spec revise | spec verify
tfpfgen audit run | audit cleanup
tfpfgen sdk generate | sdk verify
tfpfgen provider generate | provider verify
tfpfgen version
```

Exit codes are `0` success, `1` a verb that ran and refused, `2` an invocation
that was misspelt.

## Backends

The SDK is produced by one of two external generators, named in `tfpfgen.yaml`
as `sdk.backend`: `kiota` or `openapi-generator`. Exactly one per provider repo.
Each is a binary on `PATH` at the exact version `sdk.backend_version` pins —
the toolkit checks the version and refuses on a mismatch, and never downloads a
tool itself. `.github/actions/setup-backend` is what installs one in CI.

## Consuming it

Provider repos reference this repo's behavior; they never copy it.

- `generator.version` in `tfpfgen.yaml` pins the CLI to an exact tag.
  `.github/actions/setup-tfpfgen` fetches that release and verifies its
  checksum before extracting it.
- The six thin caller workflows pin the moving major tag, `@v0`, until the
  1.0.0 contract freeze.

## The repos

| Repo | Role |
|---|---|
| `terraform-plugin-framework-codegen` (this one) | CLI, reusable workflows, templates, config schema — all behavior |
| `tfpfgen-provider-template` | GitHub template stamping a provider repo's identity and its six thin workflow callers |
| `terraform-provider-thousandeyes` | Proof provider — kiota backend, pipeline wired end to end |

A second proof provider on the `openapi-generator` backend is not yet stood up.
The backend itself is exercised by the curated fixture under `testdata/` and by
the GitHub document below.

## Docs

| Doc | What it covers |
|---|---|
| `docs/contract.md` | The cross-repo contract: pipeline stages, artifact names, exit codes, secrets, the correction decision gate |
| `docs/config.md` | Every `tfpfgen.yaml` key, its default and its allowed values — generated from `internal/config`, not hand-written |
| `docs/glossary.md` | The vocabulary; every term in it is deliberate |
| `docs/mapping.md` | The API behaviour each generated schema shape answers, and the operation set behind each generated resource, datasource, list resource and action |
| `docs/emittance_tracker.md` | What each pilot document currently emits and refuses — the only place those counts are kept |
| `docs/rehearsal.md` | The first full local run of the chain against the quirkserver, stage by stage |
| `docs/releasing.md` | How a release is cut and how the moving major tag moves |
| `docs/comment-style.md` | What a comment in this repo may and may not say |

## Status

The chain runs end to end. Three documents — Jamf Pro, ThousandEyes and
GitHub — each generate a provider tree that builds. What each currently emits
and what it refuses is measured in `docs/emittance_tracker.md`, which is the
only place those counts are kept: a count is a fact about one toolkit commit
against one pinned document, and anywhere else it cannot be re-measured.

What a build does *not* yet mean: the generated schemas have not been
exercised against a live API or a `terraform plan`. Building says the emitted
Go is well-formed against the SDK; it says nothing about whether an attribute
is optional where it should be computed, sensitive where it should be plain, or
a set where it should be a list. Making the schema right for the API's real
behaviour is the work in progress, and `docs/mapping.md` specifies it: thirteen
API behaviours and the terraform-plugin-framework shape each one demands, then
the operation sets an entity can carry and which call each generated function
makes.

## Licence

MIT. See `LICENSE`.
