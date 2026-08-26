# Handoff

## The job, and the only measure of it

Make the 35 generated ThousandEyes resource acceptance tests pass. A test
passes when, and only when, this says so:

```sh
sh '/Users/dafyddwatkins/localtesting/thousandeyes/tf_acceptance_tests.sh'
```

Nothing else counts. Audit observation counts, correction counts, attribute
counts, `postcheck`, `make check` — all of these have moved a great deal while
the benchmark stayed at zero. Run the benchmark, quote its number, and treat a
change that does not move it as a change that did nothing.

**Current score: 1 passed, 34 failed.** It was 0/35 at the start of this work.

---

## The test harness, in full

### Where everything lives

| What | Path |
|---|---|
| Toolkit (this repo) | `/Users/dafyddwatkins/GitHub/terraform/terraform-plugin-framework-codegen` |
| Generated provider under test | `/Users/dafyddwatkins/GitHub/terraform/scratch/gen/thousandeyes` |
| Built CLI | `/Users/dafyddwatkins/GitHub/terraform/scratch/bin/tfpfgen` |
| Test scripts | `/Users/dafyddwatkins/localtesting/thousandeyes/` |

Other pilot trees sit beside the ThousandEyes one: `github`, `jamfpro`,
`credentials`. Only ThousandEyes has a live token and an audit.

### The scripts

`tf_set_env_var.sh` — the one place the lab credential lives. Every other
script sources it. It exports `THOUSANDEYES_API_TOKEN` (what the generated
provider reads), `TFPFGEN_AUTH_TOKEN` (what `tfpfgen audit run` reads — the
toolkit's fixed secret contract) and `TE_PROVIDER_DIR`.

`tf_acceptance_tests.sh [filter]` — **the benchmark**. Discovers every package
under `internal/services` holding a `TestAcc`, runs them with `TF_ACC=1`, and
prints a summary ending `N passed, M failed, K skipped`. The optional argument
is a case-insensitive substring matched against package paths:

```sh
sh tf_acceptance_tests.sh                 # all 35
sh tf_acceptance_tests.sh tags/v1/tag     # one resource, ~7s
sh tf_acceptance_tests.sh tests/v1        # a group
```

Knobs: `TE_PARALLEL` (default 1 — both `-p` and `-parallel`, so nothing runs
concurrently against the live tenant), `TE_TIMEOUT` (default 120m, the whole
run's `go test` timeout, not per test).

Logs land at `/Users/dafyddwatkins/localtesting/thousandeyes/acceptance-<stamp>.log`.

`tf_remediation_loop.sh [filter]` — the full loop: build tfpfgen → probe the
live API → compile and accept corrections → regenerate → run the benchmark,
repeated until the pass count stops moving. Knobs: `TE_LOOP_MAX` (default 5),
`TE_LOOP_AUDIT` (`0` reuses the last probe findings — much faster when only
generator code changed), `TE_LOOP_ACCEPT` (`0` stops and leaves proposals for
review). It ends by ranking the distinct `Error:` reasons from the last run,
which is where the next change comes from. Per-phase logs go to a timestamped
`remediation-<stamp>/` directory.

**`TE_LOOP_ACCEPT=1` crosses the correction gate automatically.** That gate is
deliberate — accepting a correction is a human decision the pipeline exists to
enforce. The loop crosses it on purpose; run with `TE_LOOP_ACCEPT=0` first if
you want to read what the probe wants to change.

### What each generated resource carries

For `<entity>` under `internal/services/resources/<group>/v1/<entity>/`:

| File | Purpose |
|---|---|
| `resource_acceptance_test.go` | the live lifecycle: **step 1** apply minimal, **step 2** import + `ImportStateVerify`, **step 3** apply maximal |
| `tests/terraform/acceptance/resource_{minimal,maximal}.tf` | what the live steps apply — **built from recorded bodies** |
| `tests/terraform/unit/resource_{minimal,maximal}.tf` | what the unit tests apply — **still derived from the document** |
| `tests/responses/resource_{minimal,maximal}.json` | wire JSON the mocks answer with |
| `mocks/responders.go` | httpmock responders built from those |
| `crud.go`, `state.go`, `construct.go`, `model.go`, `resource.go` | the provider itself |

The acceptance and unit fixtures diverge on purpose. Unit configs must keep
matching their mocks, which are built from the same derivation; acceptance
configs replay what the API accepted and carry a per-run random suffix.

### The audit artifacts

Under the provider tree:

| Path | What |
|---|---|
| `audit/observations/<entity>.observations.json` | one fact per property |
| `audit/bodies/<entity>.bodies.json` | **the accepted create bodies** — request, response, status |
| `audit/inputs.json` | operator-supplied values the probe cannot invent (authored) |
| `spec/corrections/*.correction.json` | accepted corrections (authored) |
| `spec/corrections/proposed/` | awaiting a human decision |
| `spec/revised.yaml` | generated — never hand-edit |

---

## The architecture

The document is a hypothesis; the API is the authority.

```
OpenAPI doc ──> specmodel ──> IR ──> sdkbind ──> emit ──> provider tree
                   ^                                          |
                   |                                          v
              corrections <── revise <── observations <── audit (live probe)
                                          + bodies
```

Presence (`required` / `optional` / `computed`) is corrected into the document
and re-derived. **Values are not.** Values come from `audit/bodies/` and are
replayed directly into acceptance fixtures. That split is the point: deriving
values again from the document is what produced a year of one-field-at-a-time
failures (`icon`, `match_type`, `filters` were all the same bug).

### Key code

| Concern | Where |
|---|---|
| Additive minimal search (add a field until 2xx) | `internal/audit/run/steps_create.go: searchMinimal` |
| Subtractive maximal reduction (drop until 2xx) | `internal/audit/run/steps_create.go: reduceMaximal` |
| Refusal grammar (what a 4xx names) | `internal/audit/run/adjust.go: classifyRefusal` |
| Recorded bodies artifact | `internal/audit/observe/bodies.go` |
| Replaying a body into a fixture | `internal/fixtures/fixtures.go: FromAcceptedBody` |
| Per-run unique names | `internal/fixtures/fixtures.go: WithRunSuffix`, `RunSuffixBlock` |
| Acceptance vs unit split | `internal/emit/render_fixtures.go: resourceFixtures` |
| Probe step ordering | `internal/audit/strategy/program.go: buildProgram` |
| Presence rule | `internal/intermediate_representation/attributes.go` (~line 490) |

---

## What has merged

| PR | What |
|---|---|
| #115 | a nested attribute is held as a value that can be unknown |
| #116 | the audit reads the refusals it is given |
| #117 | a created resource answers with what names it (identity, create-response id, examples, safe literals) |
| #118 | an entity names the property that identifies it (`x-tfpfgen-identifier-property`) |
| #119 | an acceptance test replays a request the API took |

**Open PR** — the branch this handoff is on: the maximal create ordering fix,
plus the quirkserver's `NamesRefusedFieldInProse`.

---

## Where the 34 failures actually are

```
33  fail at Step 1/3   (create)
 1  fail at Step 3/3

30  Error: Create failed (HTTP 4xx)
 4  Error: Provider produced inconsistent result after apply
 1  Error: Invalid id
 1  Error: Create failed: the request never completed
```

**The score tracks recorded-body coverage.** Only four entities have a body
recorded — `tag`, `credential`, `dashboard`, `connectors_generic` — because
only four got through the probe. The other 31 still have document-derived
configs, which is why they fail at step 1 with a 4xx.

So the single highest-value work is **getting more entities through the
probe**. Everything else is downstream of that.

### Next steps, ranked

1. **Unblock the probe.** 30 entities blocked in the last run. Read the reasons
   in the audit output — they group into: parent objects that cannot be
   re-created, creates the search could not heal, and a tenant ceiling on
   `/templates`. Each group unblocked is several tests.
2. **Populate `audit/inputs.json`.** Some entities need real tenant values no
   generator can invent: a reachable stream endpoint URL, an agent id
   (`tests_ftp_server`), a dashboard id (`dashboard_snapshot`), a discriminator
   `type` for `connectors_generic` / `operations_webhook`.
3. **The four inconsistent-result failures.** A value sent and echoed back
   differently. `FromAcceptedBody` already drops what is never returned; this is
   the narrower case of a value the API rewrites.
4. **`Invalid id` / `request never completed`** — one each, `templates_sharing_setting`
   (a singleton) and `stream` (`lastSuccess`/`lastFailure` declared `int64`, the
   API answers RFC 3339).

---

## Blockers that are not code

**`account_group` cannot pass.** A leaked object permanently holds the fixture
name and the API refuses to delete it:

```
DELETE /v7/account-groups/281474976717041
400 "Unable to delete accounts outside your organization."
```

It needs org-admin or ThousandEyes support. The per-run random suffix (#119)
means new leaks will not recur, but this one predates it.

**Import is flaky.** `tag` failed once at step 2 with `Read failed (HTTP 403)`
and passed on a re-run with no change — read-after-write lag, with ThousandEyes
answering 403 where 404 is expected. Any full-suite number is unreliable until
the retry predicate treats that as lag. Re-run a single failure before
believing it.

**A full suite run is slow.** Serial by design (`TE_PARALLEL=1`). One earlier
run had a single resource burn 1802s on a create timeout. Prefer filtered runs
while iterating.

---

## Lessons that would have saved hours

**Run the benchmark.** Every proxy metric moved while it sat at zero. If a
change cannot be shown to move the pass count, say so plainly rather than
reporting the proxy.

**Verify a diagnosis before acting on it.** Three claims in this work were
asserted confidently and were wrong: that a live minimal object was racing the
maximal create (it was — but only under the *strategy* program, not the plan
path that was read first); that the recipe carrying a stale body caused the
maximal failures (it did not — two tests written to prove it passed without the
fix); and that a regression was caused by a code change when it was a local DNS
failure. Instrument and look. One `fmt.Fprintf` to stderr settled in one run
what an hour of reading did not.

**Fix a fact in the layer that owns it.** Two workarounds were written into the
emitter for facts that belong to the probe — sending `x-tfpfgen-server-default`
as an input value, and stopping the state mapper nulling any optional
attribute. Both were reverted in #119. If a fact about the API has nowhere to
live, that is a missing observation kind, not a licence to put policy in the
generator.

**Naming is owner-owned.** A new observation kind or `x-tfpfgen-*` key needs
the repository owner to approve the name before it is coined, and the decision
recorded in `docs/glossary.md`. `identifierProperty` went through that; the
"accepted on write, never returned" fact (`docs/mapping.md` row 1) still needs
it.

---

## Verifying the toolkit itself

```sh
make check          # fmt, build, vet, coverage gate, hygiene gate
golangci-lint run   # make check leaves this to CI
```

Coverage gate: 90% total, 80% per package under `internal/`. Currently 90.7%.

Regenerating a pilot tree:

```sh
go build -o ../scratch/bin/tfpfgen ./cmd/tfpfgen
cd ../scratch/gen/thousandeyes
tfpfgen provider generate    # postcheck: go mod tidy, go build, go vet
```

Claims about generated output are verified against a generated tree, never by
grepping this repo for a symbol — an emitter builds most of what it emits.
