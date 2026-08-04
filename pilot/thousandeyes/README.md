# The ThousandEyes pilot

A fully generated [terraform-plugin-framework](https://developer.hashicorp.com/terraform/plugin/framework)
provider for the ThousandEyes v7 API — the proving ground for the
[toolkit at the repository root](../../README.md). Everything the generator
claims to do has to work here first, against a real API, in CI.

This module is **not published to the Terraform registry**. It exists to be
generated, built, unit-tested and live-tested; publishing is out of scope.

## What is in here

| Path | What it is | Owner |
|---|---|---|
| `internal/services/resources/` | 23 resource packages — CRUD, models, schemas, acceptance tests, fixtures | generated |
| `internal/services/datasources/`, `.../actions/`, `.../ephemeral/` | data sources, the action, the ephemeral credential | generated |
| `internal/provider/{resources,datasources,list_resources,actions}.go` | registration | generated |
| `internal/provider/{provider,interfaces}.go`, `internal/client/` | authentication and wiring | hand-written |
| `internal/services/common/{convert,crud,errors,schema}/` | shared helpers the generated code calls | hand-written |
| `internal/acceptance/` | the acceptance harness (exists/destroy checks, logging) | hand-written |
| `docs/` | registry documentation, rendered by tfplugindocs — **do not hand-edit**; `go generate .` rewrites it |
| `examples/` | scaffolded example configurations a human may enrich |

The full ownership story, including the per-file header that marks generated
code, is in [docs/generated-boundary.md](../../docs/generated-boundary.md).
The one rule: **fix the blueprint, not the Go** — every generated file here is
overwritten by the next `provider generate`.

## The surface

Every resource is backed by a committed blueprint in
[`blueprints/thousandeyes/`](../../blueprints/thousandeyes/), and every probed
resource by a recording in [`recordings/thousandeyes/`](../../recordings/thousandeyes/):
the test lifecycles were rehearsed against the live API before this code was
generated (see [docs/fixtures-and-rehearsal.md](../../docs/fixtures-and-rehearsal.md)).

Some acceptance tests are guarded by environment variables rather than running
unconditionally, because a disposable tenant cannot hold every prerequisite:

| Variable | Covers |
|---|---|
| `TFPFGEN_ACC_ADMIN` | account groups, roles, users — need an admin-scoped token |
| `TFPFGEN_ACC_ENTERPRISE` | agent-to-agent and voice tests — need enterprise agents (lab hardware) |
| `TFPFGEN_ACC_SIP` | SIP server tests — need a reachable SIP target |

The hardware-dependent variables stay unset in CI, so those tests skip with a stated
reason rather than failing. The dashboard `layout` attribute is dropped pending a
widgets model.

## Running it

Unit tests (no credentials, no network — mocks and committed fixtures):

```bash
go test ./...
```

Acceptance (creates and destroys real objects in a live tenant — use a
disposable one):

```bash
export THOUSANDEYES_BEARER_TOKEN=…   # the provider's own auth variable
TF_ACC=1 go test -count=1 -p 1 -run TestAcc ./...
```

`-p 1` matters: packages otherwise run concurrently against one tenant. In CI,
acceptance is a weekly and on-dispatch workflow, admitted through a GitHub
environment — see [docs/checks.md](../../docs/checks.md).

To try the provider against local Terraform configuration, build it and point
Terraform at the binary with `dev_overrides` (no `terraform init`):

```bash
go build -o /tmp/terraform-provider-thousandeyes .
cat > /tmp/dev.tfrc <<'EOF'
provider_installation {
  dev_overrides { "registry.terraform.io/deploymenttheory/thousandeyes" = "/tmp" }
  direct {}
}
EOF
TF_CLI_CONFIG_FILE=/tmp/dev.tfrc terraform plan
```

## Regenerating

From the repository root:

```bash
go run ./cmd/tfpfgen provider generate -blueprint blueprints/thousandeyes -out pilot/thousandeyes
```

`provider generate` finishes with the postcheck battery (compile, tfplugindocs,
`terraform fmt`), so the tree it leaves is the tree CI accepts. The SDK version
everything is checked against is pinned in this module's `go.mod` and named in
the provider blueprint; bump both together and re-run `bindings check` and
`bindings facts -check` (see the
[onboarding runbook](../../docs/onboarding-a-new-api.md)).
