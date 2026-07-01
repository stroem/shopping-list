---
name: create-issue
description: Use when the user types /create-issue <description>, or asks to file, open, draft, or create a GitHub issue in stroem/shopping-list from a free-text description.
argument-hint: "<description>"
---

# create-issue

Turn a free-text description into a well-structured GitHub issue in
`stroem/shopping-list`: draft it, **check it isn't a duplicate**, confirm with the user,
create it, and offer to hand it straight to `/auto`.

The issue you create is **shared memory** for stateless agents: a future
`/auto` run reads the issue thread to recover intent. Write the body so an agent
with zero conversation context could pick it up.

## Input

`$ARGUMENTS` is the free-text description of the issue. If it is empty, ask once
what the issue is about. Do not proceed without a description.

## Step 1 — Draft (do NOT create yet)

Compose the issue locally first; nothing is sent to GitHub in this step.

**Title:** follow the standard GitHub issue-title convention — **sentence case**
(capitalize the first word), concise and descriptive, **no trailing period**,
and **no `type:` / commit-style prefix** (issue titles are not commit messages).
State the problem or the desired outcome, not a command. e.g.
`Autocomplete ranks rarely-bought items too high` or `Add barcode scanning to
the list screen`, not `fix: autocomplete` and not `Fixes autocomplete.`

**Body:** structured Markdown, including only the sections that apply:

- `## Description` — what and why, 1–3 sentences.
- `## Acceptance criteria` — a checkbox list of concrete, testable outcomes.
- `## Out of scope` — only if worth stating explicitly.
- `## Notes` — technical hints, affected packages, links. These double as agent
  breadcrumbs, since the issue is shared memory for a later `/auto` run.

**Labels:** tag every issue with **one _type_ label** and, where it applies,
**one _area_ label** — plus any relevant meta label. Pick the closest from the
existing repo set only; never invent a label that is not already in the repo
(verify with `gh label list --repo stroem/shopping-list` if unsure).

- **Type** (what kind of work — pick one): `bug`, `feature`, `documentation`,
  `performance`, `chore`.
- **Area** (which part of the system — pick one if it clearly fits): `app`
  (Flutter app — UI, Riverpod, local Drift), `backend` (Go API / Lambda
  handlers), `catalog` (food data, `cmd/seed`, autocomplete, EAN mappings),
  `sync` (offline outbox, pull sync, idempotency), `sharing` (households,
  device identity, auth), `infra` (AWS Lambda / API Gateway / Neon / IaC,
  cost).
- **Meta** (optional): `question`, `good first issue`, `help wanted`.
- **Readiness** (decided after grilling in Step 3 — this label gates `/auto`):
  `ready-for-agent` when the issue is fully specified and unambiguous (a
  no-questions agent could implement it correctly), or `needs-info` when an open
  decision still needs a human. Default to `needs-info` until grilling has removed
  the ambiguity.

Example: autocomplete ranks the wrong items → `bug` + `app`; adding barcode
scanning → `feature` + `catalog`; wiring up the Lambda deploy →
`feature` + `infra`.

## Step 2 — Check for an existing issue (do this BEFORE creating)

Never file a duplicate. Search **open and closed** issues for an existing match
using the draft's key terms (and labels):

```bash
gh issue list --repo stroem/shopping-list --state all --limit 30 --search "{key terms}"
# optionally narrow by area, e.g.:  --label catalog
```

Judge overlap by intent, not just title wording. Then:

- **No real match** → continue to Step 3.
- **A likely match exists** → show the user the matching issue(s) (number, title,
  state) and ask how to proceed — do **not** create silently:
  - **Extend** — add a comment to the existing issue with the new detail
    (`gh issue comment {N} …`); do not open a new one.
  - **Edit** — update the existing issue's body/labels to fold in the new scope
    (`gh issue edit {N} …`).
  - **Create anyway** — it's genuinely distinct; create a new issue and
    cross-link it (`Related to #{N}`).
  - **Skip** — already covered; do nothing.
  - If the match is **closed as implemented**, say so — the work may already be
    done; default to Skip unless the user wants a follow-up.

Carry the user's choice into the next step.

## Step 3 — Grill, then confirm (alignment gate)

This is the **only** interactive gate, and it does double duty: it aligns the
issue *and* decides whether `/auto` can safely take it.

**Grill out ambiguity first.** `/auto` asks the user nothing — it assumes and
documents — so any ambiguity left in the issue becomes an unreviewed assumption.
Before confirming, interview the user to close the gaps a no-questions agent would
otherwise have to guess: the acceptance criteria (make each one testable), what is
in and out of scope, error and edge cases, and any UX / API / schema decision the
description leaves open. Use the `grilling` skill for a sharper interview. Keep
going until nothing material is left to a guess.

**Then set readiness.** Label the issue `ready-for-agent` if grilling left it
fully specified and unambiguous; label it `needs-info` if a decision still
genuinely needs a human — and say which one, so it is clearly not yet for `/auto`.

**Then confirm.** Show the user the full draft: title, rendered body, chosen
labels (including readiness), and the duplicate-check result from Step 2. Refine
on focused questions and repeat until the user is happy.

## Step 4 — Create

Once approved, create the issue. The exact command form:

```bash
gh issue create \
  --repo stroem/shopping-list \
  --title "{title}" \
  --body "$(cat <<'EOF'
{body}
EOF
)" \
  --label "{label}"
```

Use one `--label` flag per label (repeat the flag, do not comma-join). After it
runs, report the created issue's number and URL.

## Step 5 — Offer the autonomous run

After creation, offer the autonomous run **only if the issue is
`ready-for-agent`**:

> Issue #{N} created and ready-for-agent. Want me to run `/auto {N}` now — spec
> through to PR?

- **Yes** → invoke the `auto` skill with the new issue number.
- **No** → stop here; the user can run `/auto {N}` later.

If the issue is `needs-info`, do **not** offer `/auto`. Tell the user which
decision is still open; it should be resolved and the label flipped to
`ready-for-agent` before an autonomous run.
