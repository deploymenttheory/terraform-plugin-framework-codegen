# Comment style

Comments in this repo state **what** and **why**. Nothing else.

They are not a record of how the code was arrived at. The investigation belongs in
`docs/backends.md` and in commit messages, which are where this project already
keeps measured evidence. A comment that retells the debugging story buries the one
or two sentences a reader needs, and it ages badly — the narrative stays after the
finding it describes has been superseded.

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

- **External-system behaviour, as a standing property.** "HNS reads
  `Ipams[].Subnets[]` back as a flat `Subnets[]`." "`query user` answers
  *No User exists for \** on a guest that plainly has a console session."
- **Accepted and rejected value sets**, and the hardware or build-number
  qualifiers that scope a claim: "SecureNestedPaging requires AMD SEV-SNP",
  "measured on build 26200".
- **Raw evidence quoted inline** — HRESULTs (`0x8007139F`), SDDL strings
  (`D:P(A;;FA;;;BA)(A;;FA;;;SY)`), Winsock error names. They are what makes the
  surrounding claim checkable.
- **Indented tabular blocks.** They render as godoc code blocks and usually carry
  more than the prose around them. See `internal/hcs/netmodes.go` (mode → HCN
  network) and `internal/hcs/reconcile.go` (network type → how it reports
  `SwitchGuid`). Keep the table, rewrite the prose framing it.
- **Cross-references.** Prefer paths and symbols over URLs: `docs/backends.md`,
  gate ids (G1, G2, G4), milestone ids (M3, M4), env var names. Keep a gate or
  milestone id next to its `docs/backends.md` reference so it stays resolvable.
  This is a preference about *form*, not a licence to delete citations — a
  pointer into upstream source that lets a reader check a claim
  (`hcsshim internal/uvm/create_wcow.go`) is evidence and stays. Drop a bare URL
  only when the sentence still stands without it.
- **Contracts** — locking requirements, `nil, nil` returns, ordering guarantees,
  what a caller must do first.
- **Operator-visible risk in acceptance tests** — why a test is opt-in, what it
  does to the host. State it as a standing risk, not as an anecdote.

## Drop

- **Discovery narrative and post-mortems.** "used to", "we tried", "turned out",
  "it was tried and does NOT work", "that happened and nothing noticed",
  "for as long as weave asked HNS to…", "which is how X became Y".
- **Incident statistics and blame.** "one exec in fifteen hundred bound the module
  bus's port", "it cost an acceptance run", "this gate has already produced two
  'we tested that' claims that turned out to be testing something else".
- **Retracted conclusions.** State only what is true now. A superseded finding
  kept "because it was load-bearing for a while" is a trap for the next reader.
- **Roadmap and project commentary in API docs.** Which milestone a refactor lands
  in is not a property of the function.
- **Editorial asides.** "the crux of the Windows guest story", "worth having
  anyway", "the rest is left alone deliberately".
- **Restatements of the signature.** `// New returns an HCS engine.`,
  `// Resume resumes a paused VM.`, `// Profiles returns the registry.` Either say
  something the signature does not, or say nothing.
- **Porting history**, beyond a single attribution line in a file header.

## Transform, don't delete

Almost every narrative sentence contains a fact. Convert the story into the
standing property it implies; do not throw the fact out with it.

Before — 14 lines:

> installedMatches reports whether the guest already holds exactly this binary,
> comparing content hashes.
>
> The obvious check — ask the deployed agent its version and compare — is not
> enough, and the way it fails is quiet. The version identifies the protocol, so
> it does not move when the binary changes for any other reason: a bug fix, a
> dependency bump, a different link flag. Rebuilding the agent for the GUI
> subsystem changed nothing about the protocol, so a version check reported the
> guest up to date and left the old binary running, console window and all.

After — 6 lines, same information, no story:

```go
// installedMatches reports whether the guest already holds exactly this binary,
// comparing content hashes.
//
// A version comparison is not sufficient: the version identifies the protocol,
// so it does not move when the binary changes for any other reason — a bug fix,
// a dependency bump, or a link flag such as -H windowsgui.
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

- **`internal/command`** — the `XxxCommand` struct carries the prose that explains
  the feature. Its `Validate` and `Run` methods each carry one to three lines
  saying what they do and any state precondition they enforce.
- **`internal/cli`** — user-facing text lives in the cobra `Short`, `Long` and
  `Example` fields, which is the right home for it. Each `newXxxCommand`
  constructor still carries a one-line Go doc so `go doc` is not blank.
- **`cmd/hcsspike`** — a lab notebook rather than a library. Its measured verdict
  tables and pass/fail matrices are the deliverable and stay verbatim. The
  self-narration around them does not.