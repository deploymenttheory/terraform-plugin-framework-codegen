# Emittance tracker

What the toolkit currently emits from each pilot document, and what it
refuses. One measurement per row, taken by running the generator rather than
estimated.

A count here is a fact about a particular toolkit commit against a particular
pinned document, and it goes stale the moment either moves. That is the reason
this file exists: counts stated anywhere else — a readme, a comment, a commit
message — cannot be re-measured, and drift silently. Here they carry what they
were measured against, so a stale row is visible as a stale row.

## What is measured

- **Provider tree files** is what `tfpfgen provider generate` reports writing
  into the provider repo root. It is not the manifest's file count, which also
  covers the generated SDK.
- **Resources, data sources, list resources and actions** are the entities that
  survived derivation, binding and emission — what the generated provider
  actually registers, not what the document might have supported.
- **Refusals** is the `unsupported.json` total, split by the stage that refused:
  `derivation`, `binding`, `emission`. A refusal is one entity or one attribute
  the toolkit declined to emit, each carrying the reason.
- **Builds** means the generate verb's postcheck passed in that tree: `go mod
  tidy`, `go build`, `go vet`.

## Measurement of 2026-08-15

Toolkit at `list-identity-from-item-key`. Every tree regenerates byte-identical
under `tfpfgen provider verify`.

| Document | Provider tree files | Resources | Data sources | List resources | Actions | Builds |
|---|---|---|---|---|---|---|
| Jamf Pro | 3915 | 76 | 211 | 39 | 101 | yes |
| GitHub | 4442 | 59 | 323 | 29 | 77 | yes |
| ThousandEyes | 1912 | 35 | 95 | 30 | 51 | yes |
| Total | 10269 | 170 | 629 | 98 | 229 | |

Refusals, by the stage that refused:

| Document | Total | Derivation | Binding | Emission |
|---|---|---|---|---|
| Jamf Pro | 255 | 102 | 136 | 17 |
| GitHub | 839 | 330 | 494 | 15 |
| ThousandEyes | 342 | 87 | 251 | 4 |
| Total | 1436 | 519 | 881 | 36 |

Binding refuses most of what is refused, and that is the expected shape: it is
the only stage that resolves a drafted mapping against the SDK that was
actually generated, so it is where a document's ambition meets what the
backend could carry.

Eleven of the emission refusals are one shape: a list element whose key the
document spells its own way, where no rule derives that spelling from the path
— `/roles/{id}` beside an element carrying `roleId`, `/users/{id}` beside
`uid`. Each names its candidates in its reason. They need the field named as
data before they can publish an identity.

## The documents

Each is pinned by SHA-256 in its own provider repo's `spec/upstream.lock.json`.
The pin, not the URL, is what a measurement is against — the sources below all
track a moving branch upstream.

| Document | Version | OpenAPI | Source |
|---|---|---|---|
| Jamf Pro | production, 11.30.2 | 3.0.1 | `go-sdk-jamfpro-v2`, `openapi-specs/` |
| GitHub | 1.1.4 | 3.0.3 | `github/rest-api-description`, `descriptions/api.github.com/` |
| ThousandEyes | 7.0.99 | 3.0.1 | Cisco DevNet, unified OAS |

## What a build does not mean

Building says the emitted Go is well-formed against the SDK. It says nothing
about whether an attribute is optional where it should be computed, sensitive
where it should be plain, or a set where it should be a list. No count in this
file has been exercised against a live API or a `terraform plan`.
`docs/mapping.md` specifies the behaviour the schemas still have to answer.

## Reproducing a row

In a provider repo with a committed revised spec and a generated SDK:

```
tfpfgen provider generate
```

The verb prints the entity counts and the refusal split as its last two lines,
and runs the postcheck. `tfpfgen provider verify` regenerates into a temporary
tree and byte-compares, which is what proves a committed tree still matches the
toolkit that claims to produce it.

Take every row of a measurement at one toolkit commit. A table mixing commits
describes no version of anything.
