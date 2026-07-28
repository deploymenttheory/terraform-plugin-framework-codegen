# Probing a live API

The prober exercises a real API and writes down what it observed, so that a blueprint records
what an API *does* rather than what its specification claims. Everything it concludes carries its
own evidence and a confidence level, and everything it does to somebody's tenant is bounded,
recorded and swept.

> Status: the read-only tier and the whole safety apparatus are built. The nine mutating probes
> are registered, budgeted and gated but not yet implemented, so `--allow-mutations` currently
> authorises a run that creates nothing. Phase 4.7 fills them in.

## The two tiers

| Tier | What it does | What it needs |
|---|---|---|
| Read-only | six probes: list shape, read shape, error envelope, volatility, pagination, unknown-parameter tolerance | credentials |
| Mutating | nine probes: writability, update style, read-your-writes, requiredness, server defaults, immutability, enum boundaries, normalisation, write side effects | credentials, `--allow-mutations`, a sandbox profile, and every gate condition |

The two are separate Go interfaces with no overlap, so a read-only probe cannot write. That is a
property of the type system rather than a convention: `ReadProbe` is handed a session whose only
method is `Get`.

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
the store would be unreproducible.

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
