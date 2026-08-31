# Emittance tracker

What the toolkit currently emits from each pilot document, and what it
excludes. One measurement per row, taken by running the generator rather than
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
- **Exclusions** is the `unsupported.json` total, split by the stage that excluded:
  `derivation`, `binding`, `emission`. An exclusion is one entity or one attribute
  the toolkit declined to emit, each carrying the reason.
- **Builds** means the generate verb's postcheck passed in that tree: `go mod
  tidy`, `go build`, `go vet`.

## Measurement of 2026-08-31

Toolkit at `v0.10.0` (`f6c6c82`). Every tree ran the whole chain — `spec
revise`, `sdk generate`, `provider generate` with tree verification — and
regenerates byte-identical under `tfpfgen provider verify`.

| Document | Provider tree files | Resources | Data sources | List resources | Actions | Builds |
|---|---|---|---|---|---|---|
| Jamf Pro | 3920 | 76 | 211 | 39 | 101 | yes |
| GitHub | 4508 | 62 | 323 | 31 | 77 | yes |
| ThousandEyes | 1927 | 35 | 95 | 30 | 53 | yes |
| Total | 10355 | 173 | 629 | 100 | 231 | |

Exclusions, by the stage that excluded:

| Document | Total | Classification | Derivation | Binding | Emission |
|---|---|---|---|---|---|
| Jamf Pro | 231 | 90 | 13 | 111 | 17 |
| GitHub | 934 | 84 | 172 | 665 | 13 |
| ThousandEyes | 418 | 6 | 83 | 325 | 4 |
| Total | 1583 | 180 | 268 | 1101 | 34 |

API mechanics left the documents. Revision removed `_links` — the API's own
link to itself, `apiMechanics.navigationLinks` — at 167 ThousandEyes sites
and 50 GitHub sites; the Jamf Pro document declares none. Every generated
`links` attribute is gone with it: 102 ThousandEyes and 17 GitHub schema
files carried one at the previous measurement, and zero do now. The pins are
unchanged, so this measurement compares to the previous one directly.

What the removal moved, precisely:

- ThousandEyes lost nine binding exclusions that were all one fact — an SDK
  model answering no accessor for a `_links` field — because the field is no
  longer there to bind. One datasource,
  `endpoint_test_results_real_user_tests_page`, is now excluded whole, and
  its reason says why: once its links went, no attribute survived that a
  response can be read back into. A datasource whose only readable content
  was the API describing itself was never a datasource.
- GitHub lost one such binding exclusion, on `repos_branch`. Its seventeen
  `links` attributes leave silently: the property is no longer in the revised
  document, so there is nothing to exclude and nothing to bind.
- The generated GitHub SDK is 32 files smaller — 3528 files to 3496 —
  because kiota no longer models the link objects.
- `_links` still appears in both revised documents as prose — pagination
  descriptions saying "use `next` from `_links`" — and inside GitHub's
  `x-github-breaking-changes` extension payload, which is the vendor's own
  annotation and not schema content. No schema `required` list demands the
  removed property. GitHub's `download_links` property, a different word
  that merely contains the spelling, is untouched.

Collections of collections — a list of lists, a list of maps, a map of lists
and a map of maps, with a scalar at the bottom — generate as they did at the
previous measurement:

| Document | Attributes | What they are |
|---|---|---|
| Jamf Pro | 1 | a map of lists of strings: `privileges_by_site` on the `user` datasource |
| GitHub | 2 | a list of lists of strings: `sort_by` on the two projects-v2 view actions |
| ThousandEyes | 10 | a map of maps of strings, `custom_headers.domains`, on the http-server, page-load and web-transaction tests — resource, datasource and instant action each — and a list of lists of integers, `packets_by_second`, on the network test-results datasource |

No pilot document declares a collection of collections with an object at the
bottom, which is the one such shape still excluded
(`nestedCollectionElement`): zero in all three.

Binding excludes most of what is excluded, and that is the expected shape: it
is the only stage that resolves a drafted mapping against the SDK that was
actually generated, so it is where a document's ambition meets what the
backend could carry.

Eleven of the emission exclusions are one shape: a list element whose key the
document spells its own way, where no rule derives that spelling from the path
— `/roles/{id}` beside an element carrying `roleId`, `/users/{id}` beside
`uid`. Each names its candidates in its reason. They need the field named as
data before they can publish an identity.

## The documents

Each is pinned by SHA-256 in its own provider repo's `spec/imported.pin.json`.
The pin, not the URL, is what a measurement is against — the sources below all
track a moving branch upstream.

| Document | Version | OpenAPI | Source |
|---|---|---|---|
| Jamf Pro | production, 11.31.1 (`11.31.1-t1787060595569`) | 3.0.1 | `go-sdk-jamfpro-v2`, `openapi-specs/` |
| GitHub | 1.1.4 | 3.0.3 | `github/rest-api-description`, `descriptions/api.github.com/` |
| ThousandEyes | 7.0.102 | 3.0.1 | Cisco DevNet, unified OAS |

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

The verb prints the entity counts and the exclusion split as its last two lines,
and runs the postcheck. `tfpfgen provider verify` regenerates into a temporary
tree and byte-compares, which is what proves a committed tree still matches the
toolkit that claims to produce it.

Take every row of a measurement at one toolkit commit. A table mixing commits
describes no version of anything.
