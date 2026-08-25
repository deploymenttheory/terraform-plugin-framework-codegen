# Comment style

Comments in this repo state **what** and **why**. Nothing else.

They are not a record of how the code was arrived at. The investigation belongs
in the pull request body and in commit messages, which are where this project
keeps measured evidence and where a measurement carries a date. A comment that
retells the debugging story buries the one or two sentences a reader needs, and
it ages badly — the narrative stays after the finding it describes has been
superseded.

`CLAUDE.md` states the same rule in short form; this document is the long form,
and the two do not disagree.

## The rule

A comment answers two questions and stops.

1. **What** — what the thing does or is. Present tense, one sentence, starting
   with the identifier so `go doc` reads correctly: `// TryLock attempts the lock
   without blocking.`
2. **Why** — only the reasons a reader cannot deduce from the code: a constraint
   imposed by an external system, an obvious alternative that does not work, or a
   contract other code depends on.

Length follows the why. If there is no non-obvious why, the comment is one line.
If the why is a real constraint, spend the lines it takes to state it — but state
it, do not narrate it. Prose is fine; story is not.

Put the rationale for what a function does in its **doc comment**, not inline in
the body. `go doc` only shows the doc comment. Inline comments are for a single
statement whose reason is local to that statement.

## Keep

These are facts, and they are the point of the comment.

- **Generator and API behaviour, as a standing property.** "kiota models an
  object union as a composed type with one named accessor per branch." "A
  document that declares nothing required in its responses sends every writable
  attribute to plain `Optional`."
- **Accepted and rejected value sets**, and the backend or version qualifiers
  that scope a claim: "`list/schema` attribute types declare no `Sensitive`
  field", "measured against terraform-plugin-framework v1.19.0".
- **Raw evidence quoted inline** — an SDK accessor spelling
  (`GetPasswordEscaped`), a refusal reason as the report prints it, an HTTP
  status, a framework type name. They are what makes the surrounding claim
  checkable.
- **Indented tabular blocks.** They render as godoc code blocks and usually carry
  more than the prose around them. See `internal/vendor_openapi_specs/cache.go` (the cache
  layout) and `internal/sdkbind/binder_kiota.go` (document shape → builder
  chain). Keep the table, rewrite the prose framing it.
- **Cross-references.** Prefer paths and symbols over URLs: `docs/contract.md`,
  `docs/glossary.md`, `docs/mapping.md`, `x-tfpfgen-*` keys, `TFPFGEN_*`
  environment variable names, the exit-code contract. Keep a mapping.md row
  number next to its reference so it stays resolvable. This is a preference
  about *form*, not a licence to delete citations — a pointer into upstream
  source that lets a reader check a claim
  (`terraform-plugin-framework-validators stringvalidator/utf8_length_between.go`)
  is evidence and stays. Drop a bare URL only when the sentence still stands
  without it.
- **Contracts** — locking requirements, `nil, nil` returns, ordering guarantees,
  what a caller must do first, which stage owns a refusal.
- **Operator-visible risk in credentialed tests** — why a test is opt-in, what it
  does to somebody's tenant. State it as a standing risk, not as an anecdote.

## Drop

- **Discovery narrative and post-mortems.** "used to", "we tried", "turned out",
  "it was tried and does NOT work", "that happened and nothing noticed",
  "which is how X became Y", "had already drifted into a duplicate".
- **Counts and measurements from a particular run.** "207 losses on one pilot",
  "the first live run opened fifty-seven", "42 attributes across the three
  pilots". A measurement belongs in a pull request body, where it is dated.
- **Retracted conclusions.** State only what is true now. A superseded finding
  kept "because it was load-bearing for a while" is a trap for the next reader.
- **Roadmap and project commentary in API docs.** Which tranche a refactor lands
  in is not a property of the function.
- **Editorial asides.** "worth having anyway", "the rest is left alone
  deliberately", "which is the point".
- **Restatements of the signature.** `// Prune prunes the bindings.`,
  `// Load loads a document.`, `// IDs returns the ids.` Either say something the
  signature does not, or say nothing.
- **Which pull request changed it**, and how a bug was found. Git history
  records change; a comment states what is true.

## Transform, don't delete

Almost every narrative sentence contains a fact. Convert the story into the
standing property it implies; do not throw the fact out with it.

Before — 11 lines:

> joinTreeKeeping is joinTree, and also answers the attributes it kept with no
> SDK field behind them — the id and the addressing attributes.
>
> Pruning removes their bindings, correctly: no model carries them, because they
> address the object rather than describe it. But the attribute still reaches the
> schema, so reporting that removal as something the operator lost is wrong, and
> it was wrong 207 times on one pilot. This is the only place that knows, because
> this is the place that decides.

After — 7 lines, same information, no run-specific count and no editorial:

```go
// joinTreeKeeping is joinTree, and also answers the attributes it kept with
// no SDK field behind them — the id and the addressing attributes.
//
// Pruning removes their bindings correctly: no model carries them, because
// they address the object rather than describe it. The attribute still
// reaches the schema, so reporting that removal as a loss would be wrong,
// and this is the only place that knows which attributes those are.
```

## Section dividers

Files long enough to need navigation carry section dividers:

```go
// ── Host clipboard (pinned thread) ───────────────────────────────────────────
```

- `// ── `, a noun-phrase title, a space, then U+2500 (`─`) padding to **exactly
  80 runes** total.
- Blank line above and below. Never inside a function body.
- Use them in files over roughly 250 lines, to group related declarations. A short
  file with three functions gains nothing from them.
- A divider is a label, not a comment: it says what the group is, and the
  what/why prose stays on the declarations beneath it.

## Package comments

The package comment must sit **directly above the `package` clause**, below any
`//go:build` line. A header block placed above the build constraint is not a
package comment and never reaches `go doc`.

```go
//go:build windows

// Package hcs drives the Windows Host Compute Service …
package hcs
```

## Where the doc goes for commands

- **`internal/cli`** — user-facing text lives in the cobra `Short`, `Long` and
  `Example` fields, which is the right home for it. Each `newXxxCommand`
  constructor still carries a one-line Go doc so `go doc` is not blank. A verb's
  exit-code behaviour belongs here, next to `docs/contract.md`'s table.
- **`cmd/tfpfgen`** — three lines of substance over `cli.Run`. It needs a package
  comment and nothing else; everything a reader wants is in `internal/cli`.
- **`internal/templates`** — a template's comments are emitted into somebody
  else's repository, where nobody can edit them. They say what the generated
  code does, never how this toolkit decided to generate it.