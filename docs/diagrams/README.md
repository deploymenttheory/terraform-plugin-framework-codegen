# Diagrams

Five views of the toolkit, rendered from typed JSON by
[archify](https://github.com/tt-a1i/archify) (MIT).

| Open | Shows |
|---|---|
| [`system.architecture.html`](system.architecture.html) | **Start here.** The whole system on one page: the vendor's document, this repository, a generated provider repo, the live API, the reviewer, the registry, the practitioner. |
| [`pipeline.dataflow.html`](pipeline.dataflow.html) | How a document becomes a provider, and what each stage writes to disk. |
| [`refused-body.sequence.html`](refused-body.sequence.html) | What the audit does when the API refuses a create: read the refusal, correct the body, retry, record. |
| [`correction.lifecycle.html`](correction.lifecycle.html) | A correction's states, and why a withdrawal is not a rejection. |
| [`ci.workflow.html`](ci.workflow.html) | The generation pipeline and the human decision gate that blocks it. |

Each `.html` opens in a browser with no server. Dark and light follow the
system theme; the view selector isolates one path at a time. The diagram itself
is inline SVG — the only external request is to Google Fonts for the label
typeface, which falls back cleanly offline.

## The `.json` is the source; the `.html` is derived

Hand-editing an `.html` is a defect, not a change — the same rule
`docs/config.md` carries. Edit the `.json` and re-render.

## Regenerating

archify is an agent skill, installed into this repository from the version
recorded in `skills-lock.json`:

```bash
npx skills add tt-a1i/archify     # installs into .agents/skills/archify
```

The installer needs Node 22.20 or later; archify itself renders on 18. The
installed tree is git-ignored — `skills-lock.json` is what is tracked, so every
machine and CI run resolves the same version.

Then, from this repository:

```bash
make diagrams                        # re-render all five, and check they fit
make diagrams ARCHIFY=/some/path     # against a clone instead
```

## Authoring a change

```bash
A=.agents/skills/archify

# 1. edit the .json, then validate. A showcase pass reports all 9 artifact
#    checks with 0 composition errors and 0 warnings. A receipt showing only
#    4 checks is basic validation, not acceptance.
node $A/bin/archify.mjs validate <kind> <source.json> --quality showcase

# 2. render, atomically replacing the artifact
node $A/bin/archify.mjs deliver <kind> <source.json> <output.html> --quality showcase

# 3. check it fits on screen. `validate` cannot see this: it reads the source,
#    while this opens the real artifact in headless Chrome and measures it at
#    1440x900, 1600x1000, 1920x1080 and 2048x1320 in both themes. A diagram can
#    pass all 9 artifact checks and still overflow every one of them.
node $A/bin/archify.mjs visual-check <output.html>

# live-reload while authoring
node $A/bin/archify.mjs preview <kind> <source.json> /tmp/preview.html
```

`<kind>` is one of `architecture`, `workflow`, `sequence`, `dataflow`,
`lifecycle` — it must match the file's own `diagram_type`.

**When `validate` reports `internal/unclassified`, it has swallowed the real
diagnostics.** Run the renderer directly to see them; they name the subject and
suggest a fix:

```bash
node $A/renderers/<kind>/render-<kind>.mjs <source.json> /tmp/out.html
```

## Constraints worth knowing before you edit

Learned by hitting them; the schemas in `$A/schemas/` are authoritative.

- **dataflow** — `meta.viewBox` is required (architecture computes one, dataflow
  does not). `stages` is capped at **5**. A flow must step exactly one stage
  forward: no backwards flows, and same-stage flows need explicit routing.
- **lifecycle** — the outcome band has columns **0–2** only; the main rail has
  0–4. Lanes other than `main` and `terminal` share one band.
- **workflow** — `col` is capped at **5**. Lane count changes the column
  geometry, so two nodes in one lane at adjacent columns will collide.
- **all kinds** — `views[].note` is capped at 140 characters, and a node label
  wider than its box is an error. Shorten copy before reaching for geometry
  controls; add at most one diagnosed control per repair.

## CI proposes updates; you approve them

`.github/workflows/diagrams.yml` runs when a push to `main` touches code one of
the diagrams describes. It reads the change, asks Claude which diagrams it
affects, re-renders, holds both gates, and opens a pull request with before and
after screenshots of every diagram attached to the run.

Claude edits `.json` sources only. The rendering, the gates and the pull request
belong to the workflow, so a diagram cannot reach a branch without passing the
same checks `make diagrams` runs here. What no check covers is whether the new
wording is *true* — that is what the pull request is for.

### Authentication

It authenticates by workload identity federation: the run exchanges its GitHub
OIDC token for a short-lived Anthropic token, so no key is stored here. That
needs `id-token: write`, which the job grants itself.

Configure it once in the Claude Console under **Settings → Workload identity →
Connect workload**, choosing the GitHub Actions provider. The wizard registers
the issuer (`https://token.actions.githubusercontent.com`), creates a service
account and a federation rule, and then waits 15 minutes for a real exchange to
confirm the setup — so run this workflow by hand while that window is open.

Match the rule to this repository rather than to the whole account. A GitHub
Actions JWT carries `repo:<owner>/<name>:ref:refs/heads/main` as its subject,
and a `subject_prefix` on that value stops any other repository federating
against the same rule.

The wizard then hands you three identifiers. They are identifiers, not
credentials, so they go in **repository variables**, not secrets:

    ANTHROPIC_FEDERATION_RULE_ID    fdrl_...      required
    ANTHROPIC_ORGANIZATION_ID       a UUID        required
    ANTHROPIC_SERVICE_ACCOUNT_ID    svac_...      required
    ANTHROPIC_WORKSPACE_ID          wrkspc_...    only if the rule spans
                                                  more than one workspace

The run stops at its first step and names whichever of the three required ones
is missing.

One trap worth knowing: `ANTHROPIC_API_KEY` sits *above* federation in the
SDK's credential order. If a key ever reaches this job's environment it wins
silently, and the workflow keeps working while none of the above applies.

Leave the rule's `token_lifetime_seconds` at its 3600 default rather than the
wizard's prefilled 600. A GitHub Actions JWT is single-use — it carries a `jti`,
and re-presenting one is refused as `jti_reused` — so a token that expires
mid-run has to be refreshed against a JWT that may no longer be exchangeable.
The job is capped at 30 minutes, which one hour comfortably covers.

## One diagram is allowed to scroll

`ci.workflow` is exempt from the fit-on-screen check, via `DIAGRAMS_MAY_SCROLL`
in the makefile. The workflow renderer fixes a lane at 104px and a lane row at
640px wide, so its six lanes only fit a 900px-tall viewport at a `viewBox` wide
enough to push node text under the 6px floor `validate` enforces. The two
checks cannot both hold for that renderer.

Six lanes stay because each one names the workflow file that runs that stage.
Three lanes would fit, and would file a stage under a workflow that does not
run it.
