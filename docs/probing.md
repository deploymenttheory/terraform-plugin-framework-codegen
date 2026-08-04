# Probing a live API

The prober exercises a real API and writes down what it observed, so that a blueprint records
what an API *does* rather than what its specification claims. Everything it concludes carries its
own evidence and a confidence level, and everything it does to somebody's tenant is bounded,
recorded and swept.

> Status: complete. All sixteen probes are implemented, and the pilot's committed evidence is
> twenty live mutating runs against a real sandbox, recorded across five waves — every one of
> them re-derivable offline from its transcript, and every one gated in CI by replay-verify.

## What the first live run found

Worth reading before the reference material, because it is the argument for the whole approach.
Every probe passed against a fixture designed to misbehave in twenty-six specific ways. Pointed at
a real API for the first time, the catalogue produced **four facts at `Observed` confidence that
were wrong**, and lost the observations of six probes entirely. None of it was visible offline.

| What went wrong | Why no test caught it |
|---|---|
| Every create reported "no identifier", and the sweeper deleted nothing | The identifier lookup matched the *first* attribute named `id`, and the pilot has `assignments[].id` sorting before the resource's own. No fixture had a nested object with an `id` in it. |
| The enum probe reported a closed set and four rejected documented values, all wrong | It attributed any 4xx to the enum. Every create in its sweep was refused over a *different* required field. `write.required` had always had the "the error must name the field" guard; this probe did not. |
| Six probes observed nothing at all | Synthesised sentinel values violated constraints the API enforces — a documented enum, an undocumented icon set, a hex-colour regex — so the whole body was refused and every field in it went unobserved. |
| A field required by the API was reported as unprobed | The plan's fixtures omitted `accessType` so a default could be observed. There is no default: the API requires it. |
| Half the immutability and requiredness attempts collided | Stamped names restarted from 1 per field, and the API's uniqueness key includes the name. A 409 duplicate is not a fact about the field being probed. |

Each of those is now a guard with a regression test. The one that matters most is the identifier
lookup: it failed *silently*, in the direction that leaves objects in somebody's tenant.

Later waves kept finding the same class of thing, which is the evidence the approach generalises
rather than having been lucky once: an API version bump started refusing a synthesised HTTP
header the previous version tolerated (caught by the recorded rehearsal, fixed with one plan
hint); a permissionless role create turned out to be a genuine server-side 500; user emails are
globally unique and deletion is what frees them, which reshaped the whole fixture strategy for
that resource. None of those facts is in any specification.

**The evidence the run produced**, all of it re-derivable offline:

- `accessType` and `objectType` are **required by the API**, which its own request schema does not
  declare. Two of the pilot's five open guesses, settled — and one of them was `computed_optional`.
- `objectType` is **immutable**, corroborated by two distinct values both refused after a control
  update proved the request shape was sound. Merge recommends `RequiresReplace` and does not add it.
- `color` defaults to `#A7EB10` and `icon` to `LABEL`, both corroborated across three creates.
- The specification is **stale in two places**: it documents `accessType: system` and
  `objectType: endpoint-agent`, and the API rejects both. This is the fact a spec-derived validator
  would have turned into a broken provider.
- An update carrying only the name field is refused outright, so a generated update must send the
  whole object.
- No coupling between fields, no server-side normalisation of free text, and no read-after-write
  delay — three negatives, each recorded as a note rather than as a fact.

## The two tiers

| Tier | What it does | What it needs |
|---|---|---|
| Read-only | six probes: list shape, read shape, error envelope, volatility, pagination, unknown-parameter tolerance | credentials |
| Mutating | ten probes: writability, update style, read-your-writes, requiredness, server defaults, immutability, enum boundaries, normalisation, write side effects, and the rehearsal | credentials, `--allow-mutations`, a sandbox profile, and every gate condition |

The two are separate Go interfaces with no overlap, so a read-only probe cannot write. That is a
property of the type system rather than a convention: `ReadProbe` is handed a session whose only
method is `Get`.

### The rehearsal

`write.rehearsal` is the tenth mutating probe and the last to run, and it is different in kind
from the other nine: they each isolate one behaviour, while the rehearsal walks the **exact
lifecycles the generated acceptance tests will run** — create minimal → update to maximal →
downgrade → delete, and the reverse — with the same derived fixture values the generator will
render into HCL. Every hop's response is compared field-by-field against what was sent, updates
are contrasted against sibling values to tell "suppressed by interaction" from "never returned",
and a jointly-refused body is bisected one sibling at a time to name the culprit. Its facts are
the ones that used to be discovered by a failing acceptance test.

After the standard probes, `probe -mode record` re-derives the fixtures from the merged evidence
and re-runs the rehearsal until the derived bodies stop changing, then freezes the converged
bodies as `rehearsal.json` beside the cassette. The full contract — derivation, the fact kinds,
and why the fixpoint exists — is in
[fixtures-and-rehearsal.md](fixtures-and-rehearsal.md).

## Modes

```
tfpluginframeworkgen probe -mode record|replay|verify|sweep -blueprint DIR [-resource KEY]
```

| Mode | Network | Purpose |
|---|---|---|
| `replay` *(default)* | none | re-derive facts from a committed cassette |
| `record` | live | issue real requests and write a snapshot |
| `verify` | none | assert the committed facts are exactly what replaying the committed transcript produces |
| `sweep` | live | remove objects a previous run left behind |

`replay` is the default deliberately: the safe mode is what you get by typing less, and the mode
that can change somebody's tenant has to be spelled out.

`verify` is the purity gate, and it runs in CI with egress blocked and no credentials. If
derivation ever depended on anything outside the transcript — a clock, an environment variable,
map iteration order — the committed facts and the replayed facts would differ, and every fact in
the store would be unreproducible. Two refinements keep that gate honest rather than brittle:
a replay that reproduces an error the recording itself ended on still has its facts compared
(a faithful reproduction of a recorded failure is a *pass*, not a crash), and `replay -rederive`
rewrites `facts.json` from the committed cassette when the derivation logic itself improves —
so better inference never requires re-probing a live API.

## What a snapshot freezes

A committed evidence snapshot is not just the cassette. It freezes everything replay needs to be
a pure function of the directory:

| File | What it pins |
|---|---|
| `cassette.json` | the ordered transcript, redacted |
| `facts.json` | the derived facts, which `verify` re-derives and compares |
| `plan.json`, `subject.json` | the plan and blueprint-derived subject *as probed*, so later curation cannot silently change what a replay sends |
| `rehearsal.json` | the converged rehearsal bodies, so the fixpoint's outcome replays instead of re-deriving from a blueprint that has since moved |

Two version marks make old evidence honest rather than wrong. The subject carries an
`evidenceRev`: observations added to the prober after a snapshot was recorded are only asserted
against evidence recorded at a revision that knew to capture them, so upgrading the toolkit never
turns green history red. And a snapshot recorded with `-only` records that filter in its
metadata, so a filtered recording replays filtered rather than failing over probes it never ran.

## Credentials

The token and the endpoint come from the environment and nowhere else:

```bash
export TFPFGEN_PROBE_ENDPOINT=https://api.example.com/v7
export TFPFGEN_PROBE_TOKEN=…
```

Not a flag: a flag puts the credential in shell history and in the process table. Not the
profile: a profile is a file that gets written down, and the gate refuses one containing the
token's value — checked verbatim, because shape heuristics miss a great many real credentials.

## The sandbox profile

A mutating run needs one. Default location `.tfpluginframeworkgen/sandbox/<provider>.json`,
which is gitignored — a profile carries `assertions.accountGroupId`, and a tenant identifier in a
committed file turns a vulnerability into a targeted one. Start from
[`examples/probe-profile.example.json`](examples/probe-profile.example.json).

```json
{
  "endpoint": "https://api.example.com/v7",
  "tokenEnv": "TFPFGEN_PROBE_TOKEN",
  "sandbox": true,
  "sandboxEvidence": "A disposable tenant created for provider development; it holds nothing anybody depends on.",
  "namePrefix": "tfpfgen-probe",
  "assertions": {
    "endpointHostSuffix": "example.com",
    "maxExistingObjects": 25,
    "accountGroupParam": "aid",
    "accountGroupJsonPath": "aid"
  }
}
```

`sandbox: true` is a **claim**. The assertions are **evidence**, and the distinction is the whole
design of the gate. `maxExistingObjects` is the cheapest and most empirical of them: a tenant
holding four objects is a sandbox, one holding nine hundred is production, and no amount of
configuration can misrepresent that.

`sandboxEvidence` is bounded by length and word count. Writing the sentence *is* the check — it
makes an operator state a reason rather than tick a box.

The profile is decoded strictly. A mistyped key in a safety file must not be silently ignored: a
profile with `"sandBox": true` would otherwise gate nothing while looking like it gated
everything.

## The gate

Every condition below must hold. A refusal lists **all** of them at once, so a profile gets fixed
in one pass rather than one condition per attempt.

**Static** — checked without issuing a single request:

| Condition | Why |
|---|---|
| `mode` | mutating probes need `-mode record` |
| `allowMutations` | the flag is required every time; it is a request, not an authorisation |
| `sandbox` | the profile must declare it |
| `sandboxEvidence` | a human has to have written a reason |
| `namePrefix` | ≥ 8 characters and containing `tfpfgen`, so anybody who finds an object in a UI can tell what made it |
| `tokenEnv` | named, and set in the environment |
| `noCredentialInProfile` | the token's value, a credential-shaped value, or a key named like one |
| `https` | a bearer token over plain HTTP is a credential on the wire in clear |
| `endpointHostSuffix` | stops a profile pointed at the wrong host |
| `canMutate` | the resource must have a create, a delete, a name field and an identifier field |
| `plan` | valid, and declaring at least one fixture |
| `noSnapshotOverwrite` | evidence already committed is not replaced without `-force` |

**Runtime** — read-only requests, attempted only once the static tier is clean:

| Condition | Why |
|---|---|
| `maxExistingObjects` | the empirical sandbox test |
| `accountGroupId` | the credential really is scoped where the profile says |

The two tiers are separate because "report every unmet condition" and "read the tenant before
writing to it" pull against each other: enumerating runtime failures against a tenant already
refused on static grounds spends somebody's rate-limit allowance to learn nothing.

An **unchecked** assertion is a refusal, not a pass. And where evidence is weaker than the claim,
the weaker outcome is recorded verbatim — an empty tenant yields
`accountGroupId (scoped read succeeded; the tenant is empty, so no object confirmed it)` rather
than a confirmation nothing demonstrated.

## Nothing is created that cannot be removed

### The ledger

`.tfpluginframeworkgen/probe/<provider>/<resource>/ledger.jsonl`, gitignored.

Every create writes an intent line and **`fsync`s it before the request is issued**. That
ordering is the whole design. The failure that strands an object is not a create that fails — it
is a create that *succeeds* and whose response is never seen: a timeout, a dropped connection, a
SIGKILL between sending and reading. The object exists and nothing in the process knows its
identifier. An intent with no resolution is the signature of exactly that, and it is what tells
the sweeper to hunt by name rather than by identifier.

If the intent cannot be written, the request is not issued. A create that cannot be recorded is a
create that cannot be cleaned up.

Status classification follows from the same reasoning: a 4xx **resolves** the intent, because it
is reliable evidence that nothing was created; anything else — a 5xx, a transport error, a 2xx
with no identifier in it — leaves the intent outstanding.

A ledger is clean when it *reconciles*, not when it is empty: forty creates and forty deletes is
a clean run.

One more resolution exists, because the conservative classification above manufactures phantoms:
a create refused with a 5xx leaves its intent outstanding — correctly, since a 5xx proves
nothing — but if the server genuinely created nothing, that intent blocks every later record run
over an object that does not exist. The sweeper resolves it by **proven absence**: when a
collection read is *complete* (not truncated, not suspiciously round) and the intent's stamped
name is not in it, the object demonstrably does not exist and the intent is marked rejected.
Absence from a partial read proves nothing and resolves nothing.

### The sweeper's two passes

1. **By identifier**, from the ledger. Cannot be the whole story: an intent that never learned an
   identifier cannot be deleted by one.
2. **By name prefix**, from a collection read. This is the pass that catches the stranded case,
   and it also catches what a *previous* crashed run left behind. Bounded by the prefix, so it can
   never touch an object the prober did not create.

Both passes are idempotent, so `-mode sweep` is safe to run again after one fails halfway.

A 404 on a delete is **not** taken at face value: on an eventually-consistent API it may mean
"not visible yet", and calling that gone is how an orphan gets reported as cleaned up. It is
confirmed against the collection first.

Paging is not generically implementable, and the sweeper does not pretend. A suspiciously round
item count emits a note and reports `complete: false` rather than claiming a sweep it cannot
vouch for. `maxExistingObjects` is what makes a single-page sweep sound in the first place.

### Cleanup happens per probe

Not at the end of the run — a deadline or a `^C` would leave everything behind, and peak live
objects would be the run's total rather than one probe's. Not per object — the immutability
protocol needs its first object alive while it creates the second. And not by the probe itself: a
probe that abandons its protocol returns early and skips its own cleanup, and the abandon paths
are the majority of the branches in every mutating protocol.

### When something is left behind

The run fails with exit **5** even if every fact was gathered, and prints a table with a runnable
`curl` per object — carrying `"$TFPFGEN_PROBE_TOKEN"` and never a value, because that table goes
into a committed report and a CI step summary. In GitHub Actions it is also appended to
`$GITHUB_STEP_SUMMARY`.

Then:

```bash
tfpluginframeworkgen probe -mode sweep -resource tag -blueprint blueprints/example
```

A sweep is gated more weakly than a record run, deliberately. It does not require
`--allow-mutations` — demanding the mutation flag in order to clean up after yourself is perverse
— and it does not check `maxExistingObjects`, because a tenant that now fails it may be failing
it precisely because it is holding your orphans.

A ledger with outstanding entries **refuses** the next `record` run. Not only for tidiness: a run
against a tenant holding your own orphans makes `maxExistingObjects` measure your own rubbish.
`replay` and `verify` get a note instead — refusing an offline CI gate over a stale local file
buys nothing.

## Budgets

Set per resource in the probe plan; every one has a default, because a plan that forgets a cap
must not thereby become unlimited.

| Cap | Default | Notes |
|---|---|---|
| `maxRequests` | 200 | |
| `maxCreates` | 25 | the cap that matters: requests are cheap and objects are not |
| `maxWallClockSeconds` | 600 | enforced as a context deadline |
| `maxSweepSeconds` | 120 | separate from the run's, because the commonest reason to be sweeping is that the run's deadline expired |
| `maxDeleteFailures` | 0 | not defaulted — zero is the intended value, and treating zero as "unset" would make the safest setting the one you cannot express |

The pilot's plan sets `maxCreates: 60` rather than the default 25. That is a deliberate choice for
a resource with sixteen writable fields and three documented enums, not a workaround: the whole
catalogue costs 45 creates, every one of them swept, and per-probe release keeps the number alive at
any moment far lower. The default was chosen with no data.

The sweeper spends from its **own** reserve of `4 × maxCreates + 8` requests, not from the run's
budget. Without that, exceeding the budget would refuse the sweeper's own deletes, and the cap
meant to bound the blast radius would manufacture exactly the orphans it exists to prevent.

`probe -list` reports the catalogue's worst-case cost with no credentials, no cassettes and no
network:

```bash
tfpluginframeworkgen probe -blueprint blueprints/example -resource tag -list
```

## Exit codes

Precedence is **7 > 5 > 3 > 4 > 6 > 1**, and it is a table rather than a walk over the error
tree: these conditions genuinely co-occur — a run can exceed its budget, sweep, and still leave
an orphan — and which code CI sees must not depend on the order the errors were joined in.

| Code | Meaning | Why it ranks there |
|---|---|---|
| `7` | redaction failed | nothing was written, and a secret nearly was |
| `5` | objects left behind | something is still live in somebody's tenant |
| `3` | gating refused | nothing ran at all |
| `4` | budget exceeded | explains why a replay might not match |
| `6` | replay mismatch | last of the specific codes |
| `1` | anything else | |

Exit `2` means either a usage error or a bug: a panic in a probe is captured, reported with its
stack, swept after, and then re-raised — and the Go runtime exits `2` for a panic. A stack in the
output means the second; no stack means the first.

## The plan is where the API's own constraints live

A probe synthesises values for the fields it sends, and a synthesised value is refused by any API
that constrains the field — which loses the observation for **every** field in that request, not
just the constrained one. Three shapes of constraint turned up in one resource:

| Constraint | Where the probe gets a usable value |
|---|---|
| a documented enum | the specification, via `AttrType.Enum` — carried through `ingest` for exactly this |
| an undocumented value set (`icon`) | `candidates` in the plan; nothing else can know |
| a documented regex (`color`) | `candidates` in the plan; the IR does not carry patterns |

So `candidates` does more than feed the immutability protocol: it is the general answer to "what
value will this API accept here". Declaring two gives the writability protocol its two distinct
values as well.

A field whose only acceptable value cannot be discovered belongs on `deny`, with the consequence
understood: every probe that would have sent it emits a note, and whatever the blueprint claims
about it stays unprobed.

`deny` gates **experiments, not sends**. A denied field is never probed — never omitted to test
requiredness, never substituted to test immutability — but if a fixture declares a value for it,
that value still goes into every body that needs it. The distinction matters because the two
readings diverge exactly when a denied field is also required: strip it from the rehearsal's
bodies and every create fails, and the run learns nothing about all the *other* fields.

## Static facts

Not every fact needs a network. Some behaviour is written into the SDK's own source: a
value-typed request field tagged `omitempty` is a field whose zero value the SDK is
**structurally unable to send** — `false`, `0` and `""` serialise as an omission, whatever the
API would have said about them. Probing for that would be probing the wrong system.

`bindings -facts-out` derives these `zeroValueUnsendable` facts by scanning the pinned SDK's
struct tags, and writes them as an ordinary facts document — the pilot commits
`blueprints/thousandeyes/static.facts.json` — that `merge` folds in exactly like probe facts.
They live outside the evidence snapshots because their evidence is a source location, not an
interaction, and they get their own drift gate: `bindings -facts-check` re-derives and fails on
any difference, so an SDK version bump that changes a struct tag cannot leave the committed
facts stale.

The description marker blocks that `merge` writes keep the two provenances apart: live evidence
writes under its snapshot id, static facts under the id `static`, and within each channel the
newest evidence wins. A fact learned from the SDK's source can never be overwritten by — or
masquerade as — one observed on the wire.

## What a probe may not do

Enforced structurally, not by convention. A `go/ast` test asserts each of these:

- only `transport.go` and `session.go` may reference `net/http`, so every request passes the
  choke point where the budget, the pacer, the concurrency guard and the recorder live;
- `MutatingSession` cannot be constructed without a `Grant`, and `Grant` has one unexported
  constructor and a blank field, so it cannot be built from outside the package even as a zero
  value;
- every probe's doc comment must contain a "How it can be wrong" section.

Requests are also strictly serial. That is a correctness requirement rather than politeness: a
cassette is an ordered transcript, so two requests in flight would make the recorded order a
property of the server's latency. A second concurrent request is **refused** rather than
serialised, because a mutex would hide the problem and the recording would then replay only by
luck.
