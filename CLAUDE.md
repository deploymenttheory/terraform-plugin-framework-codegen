# Working in this repository

This repo is **tfpfgen**: the generator that turns an OpenAPI document into a
terraform-plugin-framework provider. It is a toolkit, not a provider.

## Changes here must be reflected in the provider template

A change to this repository must be reflected in
[`deploymenttheory/tfpfgen-provider-template`](https://github.com/deploymenttheory/tfpfgen-provider-template).

That template is the contract every generated provider repo is stamped from —
`terraform-provider-thousandeyes`, `terraform-provider-github`, and every one
after them. A toolkit change that lands without the template moving means each
new provider is born against a stale contract, and nothing in either repo's CI
compares the two, so the divergence is silent until someone's pipeline fails.

Treat the template edit as **part of** the toolkit change, not a follow-up.

### What counts

Anything a generated provider repo consumes:

- CLI verbs, flags, and their **defaults** — the template's `pipeline.yml`
  invokes this CLI directly
- `config.json` keys, including ones only some auth methods use
- required secrets and their exact names
- generator behaviour that changes emitted output
- `CONFIGURING.md` prose describing any of the above

### The check worth running every time

Does the template's `pipeline.yml` read a `config.json` key that the template's
own `config.json` does not declare?

```bash
grep -oE '\.(provider|openapi|sdk|generator|probe|auth)\.[a-zA-Z]+' \
  .github/workflows/pipeline.yml | sort -u
```

`jq` returns `null` for a missing key rather than failing, so a gap here
surfaces as a confusing runtime error in someone else's repository rather than
as a broken template.

## Pull requests

Create pull requests; do not merge them. The repository owner merges.

Every change goes on its own branch cut fresh from `main` — never commit to
`main` directly, and never push follow-up commits to a branch whose PR has
already been merged.

## Verifying claims about generated output

Check the layer the claim is about. Regenerating a provider tree proves nothing
about the SDK beneath it, and a `-check` run with a flag omitted can report
drift that is purely an artefact of the invocation. Before stating that
something regenerates unchanged, run the command that would show it changing.

`codegen-verify.yml` runs on pull requests and manual dispatch only — there is
no push trigger — so `main` being green is never actually measured. To check it:

```bash
gh workflow run codegen-verify.yml --ref main
```

Note also that `gh pr checks` on a **merged** PR reports jobs cancelled at merge
time as `fail`, which makes it useless as a baseline. Query job conclusions
directly with `gh api repos/{owner}/{repo}/actions/jobs/{id}` instead.
