# What the tag API's conditional structure actually is

Established by hand against the live sandbox on 2026-07-30, because the prober cannot see any of
it. Ten POSTs, each varying one thing; every object swept afterwards.

This exists to specify a probe, not to document ThousandEyes. The pattern it describes — **one
field's value deciding whether another is required, writable, or returned** — is not specific to
tags, and the fact set has no way to express it.

## The findings

### `objectType` decides `type`, and `type` must still be sent

| sent | result |
|---|---|
| `objectType: test`, no `type` | 201, `type: "static"` |
| `objectType: test`, `type: dynamic` | **400** `type: Dynamic tags are not supported for the provided object type` |
| `objectType: endpoint-agent`, no `type` | **400** `type: Static tags are not supported for the provided object type` |
| `objectType: endpoint-agent`, `type: dynamic` | 201, `type: "dynamic"` |

So `type` is not a free choice and not inferred either. `endpoint-agent` requires
`type: dynamic`; every other object type requires `static`, which is also the default. Omitting
it on an `endpoint-agent` tag fails.

This is why the committed blueprint marking `type` computed is wrong, and why simply making it
writable is not right either: its legal value is a function of `objectType`.

### A dynamic tag requires `matchType` **and** `filters`

`objectType: endpoint-agent`, `type: dynamic`, nothing else:

```
400  filters: must not be empty
     matchType: must not be null
```

Both are optional in the document and both are mandatory here — conditionally on `type`.

### `matchType` is silently discarded on a static tag

Sent `matchType: "and"` with `objectType: test`. Result: **201, `matchType: null`.**

The same field on a dynamic tag comes back as `"and"`.

This explains a fact the prober already recorded and got half right.
`match_type.returnedOnRead` is `false` in the committed blueprint, measured against a static tag
where the field is meaningless. On a dynamic tag it is returned. **The fact is true, conditionally,
and stored unconditionally** — which is how it came to suppress the attribute's assertion and its
import verification for every tag rather than for static ones.

### `filters.scope` decides what `filters.key` may be, and the key is normalised

| sent | result |
|---|---|
| `scope: custom`, `key: k` | 201, `key: "k"` — any user-defined key |
| `scope: default`, `key: agent-id` | 201, `key: "agent-id"` |
| `scope: default`, `key: machineId` | 201, **`key: "agent-id"`** |
| `scope: default`, `key: tfacc-k` | **400** `Invalid filter key 'tfacc-k'. Valid keys are: machineId, publicNetworkCidr, ..., host, userName, ...` |

Two things here. The set of legal keys depends on `scope`, and it is documented only in prose.
And the API accepts *both* its internal names and the document's names, normalising the internal
one to the documented one — `machineId` in, `agent-id` out.

That last part corrects an earlier reading of mine: the error message's key list is not evidence
that the document is stale. Both spellings work and the documented spelling is canonical.

### `assignments` is read-only

Sent `assignments: [{id: "00000000-...", type: "test"}]`. Result: **201, `assignments: null`.**

The document agrees — `readOnly: true` — and a fresh inference marks it `computed`. The committed
blueprint has it `computed_optional`, which is wrong: nothing a practitioner writes there ever
reaches the API. A tag is assigned to an object from the object's side.

So the earlier plan to put `assignments` in a fixture was never going to work, and not because the
identifier is unresolvable. It is not writable at all.

## What the prober is missing

Every fact in the set is unconditional: a field is writable, or required, or returned. Each of the
findings above is a fact *plus a precondition*, and there is nowhere to put the precondition. The
consequences already visible in the pilot:

- `match_type.returnedOnRead = false` is recorded from a static-tag observation and applied to
  every tag, so the generated code suppresses its read-back and its import check unconditionally.
- `object_type`'s `rejectedValues` includes `endpoint-agent`, observed while creating static tags.
  It is a perfectly good object type — for a dynamic one. A generated `OneOf` built from the
  observed-accepted set would have excluded a valid value.
- Nothing records that `type`, `matchType` and `filters` are required together, so no generated
  validator or fixture can honour it.

### What a probe would have to do

1. **Vary a candidate gate field across its allowed values**, and re-run the existing per-field
   probes under each — writability, requiredness, returned-on-read — rather than once against one
   arbitrary fixture.
2. **Record the precondition with the fact.** A fact needs somewhere to say "when `objectType` is
   `endpoint-agent`", or the observation is a half-truth stored as a whole one.
3. **Read the error body for co-requirements.** `filters: must not be empty` and
   `matchType: must not be null` arriving together, only under `type: dynamic`, is the API stating
   a conditional requirement outright. It is already in a response the prober receives.
4. **Distinguish accepted-and-discarded from accepted-and-stored**, per gate value. `matchType` on
   a static tag is the clearest case: 201, and the value is gone.

Until that exists, re-recording would only re-measure the same unconditional half-truths against a
larger enum set. Which is why the re-record waits for the probe rather than the other way round.

## The dynamic-branch fixture

Added to `blueprints/thousandeyes/probe.plan.json` as `endpoint-agent-tag`. Every field in it is
load-bearing, which is the point: this is the smallest body that reaches the dynamic branch at all,
and each of the findings above is one of the reasons.

```json
{ "objectType": "endpoint-agent", "type": "dynamic", "matchType": "and",
  "filters": [{ "key": "...", "mode": "in", "scope": "custom", "values": ["..."] }] }
```

Cost, from `probe -list` before and after: 147 → 174 requests, 47 → 56 creates. Inside the plan's
existing budget of 200 and 60, so no cap change. `write.required` is where the increase lands —
16 → 30 requests — because it now omits fields from three fixtures instead of two, and that is
exactly the probe whose disagreements become conditional facts.

Verified by sending the fixture's own body: 201, and the response returns `matchType: "and"` where a
static tag returned `null`. That is the conditional `returnedOnRead` finding confirmed from the
other side, and it is what the re-record will now be able to observe.

**One coverage consequence, stated rather than discovered.** `Scope.fixtureKeys` is deliberately
cross-fixture — "a field one fixture sets is a field the operator has told us a valid value for, so
it is not a candidate for what does the server do when this is omitted". Setting `matchType` and
`filters` here therefore removes them from the omitted set, so the server-default protocol stops
probing them. That is the design working: a valid dynamic body cannot omit either, so claiming to
know a valid value for them is simply true. Their omission behaviour is still covered by
`write.required`, which is where it belongs.

## Consequences for the blueprint, not yet applied

Each of these is a schema change whose evidence is above, and each changes the probe plan, so they
belong with the re-record rather than before it.

- `type`: must become writable, with its legal value gated on `object_type`.
- `assignments`: must become `computed`. It is read-only in the document and discarded in practice.
- `match_type`: `returnedOnRead` is conditional and currently applied unconditionally.
- `object_type`: `endpoint-agent` is in `rejectedValues` and should not be.
