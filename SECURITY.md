# Security Policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately via
[GitHub private vulnerability reporting](https://github.com/deploymenttheory/terraform-plugin-framework-codegen/security/advisories/new)
on this repository. Do not open a public issue for anything you believe is a
security problem. You should receive an acknowledgement within a few days; please
allow a reasonable window for a fix before any public disclosure.

## What is security-sensitive in this repository

This project probes live APIs with real credentials and commits the resulting
transcripts, so its threat surface is specific and worth stating:

- **Bearer tokens.** Credentials are read from the environment only
  (`TFPFGEN_PROBE_BEARER_TOKEN`; the generated pilot provider uses
  `THOUSANDEYES_BEARER_TOKEN`). No command accepts a token flag, and the sandbox
  profile loader refuses a profile containing a credential-shaped value. Anything
  that would move a token into a file, a flag, a log line or a committed artefact
  is a vulnerability — report it.
- **Committed cassettes.** Probe transcripts under `recordings/` are public
  by design. Redaction allowlists rather than denylists, and a recording fails
  outright (exit `7`, nothing written) if a credential-shaped value survives.
  A redaction bypass — any way a secret can reach a committed cassette — is the
  highest-severity bug this repository can have.
- **Sandbox profiles.** `.tfpfgen/sandbox/` is gitignored because a
  profile carries tenant identifiers. Committed artefacts must never contain
  tenant names; opaque numeric identifiers are accepted.
- **Generated fixtures.** The fixture derivation refuses to invent values for
  credential-shaped fields, so generated test configurations cannot contain
  anything that looks like a secret. Secret-bearing attributes are generated as
  sensitive or ephemeral and never written to state.

## Scope

The generated pilot provider (`pilot/thousandeyes/`) is not published to the
Terraform registry; issues in it are still welcome here, since its code is this
repository's output. Vulnerabilities in the ThousandEyes API or SDK belong with
their respective owners.
