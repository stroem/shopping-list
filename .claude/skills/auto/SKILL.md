---
name: auto
description: Autonomous issue→PR orchestrator for stroem/shopping-list. Takes a GitHub issue number (or recommends one), runs an interactive spec phase via brainstorming, then executes hands-off on a worktree branch — plan, TDD via subagents, two-stage review, verify against tests AND spec — publishes a PR, and asks before squash-merging to main. Use when the user types /auto <issue-number>, /auto (to pick an issue), or asks to autonomously implement an issue.
argument-hint: "[issue-number]"
---

# /auto — autonomous issue → PR orchestrator

`/auto` is **glue around the superpowers chain**. It does **not** reimplement
spec-writing, the approval gate, planning, or the review loop — those skills
already own those transitions, and duplicating them causes double-commits and
double-prompts. `/auto`'s own contribution is the **GitHub bookends** (issue
selection + assumptions comment up front; review → verify → publish → summary →
merge-prompt → cleanup at the end) wrapped around an interactive spec phase and
a hands-off execution phase.

The shape of a run:

```
/auto <N> → [worktree] → [interactive spec] → [hands-off execution] → [publish PR] → [merge?] → [cleanup]
```

There is **one up-front gate** (spec approval, owned by `brainstorming`) and
**one end prompt** (merge to `main`). Between them the run does not pause for the
user. Two unplanned outcomes can interrupt: **escalate-and-pause** (stuck, with a
bounded retry budget) and **blocker halt** (something destructive/irreversible).

Read `AGENTS.md` for repo context before dispatching subagents — it is the doc
implementers are pointed at.

---

## Phase 0 — Launch hint (first message only)

Print once, at the very start of the run:

> "For an unattended run, Shift+Tab into `acceptEdits` (or `auto` mode if
> available) — I can't set that myself."

The skill **cannot** change the permission mode; it only suggests. The user owns
the mode. When prompts are suppressed, stay *more* conservative, not less: the
blocker-halt list and every behavioral gate below bind in **every** mode,
independent of the permission layer. A silenced prompt never authorizes anything
destructive, irreversible, or a merge to `main` without the merge prompt.

## Phase 1 — Entry / issue selection

- `/auto <N>` → use issue #N.
- `/auto` (no arg) → **milestones are the build order — check them first.** List
  open milestones in order and prefer an issue from the **earliest milestone that
  still has open issues** before considering anything else:

  ```bash
  gh api repos/stroem/shopping-list/milestones --jq \
    'sort_by(.number)[] | select(.open_issues>0) | "\(.number)\t\(.title)\t\(.open_issues) open"'
  # then list issues in that milestone (earliest-first), e.g. milestone "M0 — Foundation & infra":
  gh issue list --repo stroem/shopping-list --state open --milestone "M0 — Foundation & infra"
  ```

  Recommend one from that milestone (the foundation milestones gate the later
  ones — don't pull an M3 app issue while M0 infra is unbuilt), ranking within it
  by readiness, dependencies, labels, and age. Only fall back to
  `gh issue list --repo stroem/shopping-list --state open` (no milestone) when no
  milestone has open issues. **Confirm with the user before taking it.**

Then read and claim the issue:

```bash
gh issue view <N> --repo stroem/shopping-list
gh issue edit <N> --repo stroem/shopping-list --add-assignee @me
```

`--add-assignee @me` is autonomous (Phase 1).

## Phase 1.5 — Prior-art check (is it already done?)

**Before** spinning up a worktree or brainstorming, verify the work isn't
already built — never implement something twice. Read the issue's acceptance
criteria, then investigate the actual repo state:

- Search the code for the behavior the issue asks for (`rg`/`Grep` on the
  relevant packages, the catalog, the API/CLI surface, config flags).
- Check git history for prior work (`git log --oneline --grep "<keywords>"`),
  and the design history under `docs/superpowers/specs|plans/`.
- Check for a related or duplicate issue (`gh issue list --repo stroem/shopping-list
  --state all --search "<keywords>"`), including ones closed as implemented.

Then branch:

- **Already fully implemented** → do **not** build. Report the evidence
  (files/commits/specs) and ask the user whether to just **close the issue**
  (with a comment pointing at where it lives) instead.
- **Partially implemented** → narrow the scope to the **remaining** part, note
  what already exists (a comment on the issue), and carry that narrowed scope
  into the spec phase so brainstorming designs only the gap.
- **Not started** → proceed normally.

This is a read-only investigation in the main checkout; only continue to Phase 2
once you know what (if anything) is left to build.

## Phase 2 — Worktree first

Invoke `superpowers:using-git-worktrees`. Create the worktree at
`.claude/worktrees/<type>+issue-<N>-<slug>` on branch `feat/issue-<N>-<slug>`
(or `fix/…`), from `origin/main`, using Conventional Branch naming.

The worktree is created **before** the spec is written, so that every later
commit — spec, plan, code — lands on the feature branch. **`/auto` never commits
to `main`.** All later phases run inside the worktree.

## Phase 3 — Spec phase (interactive), delegated to `brainstorming`

Invoke `superpowers:brainstorming` and **let it run end to end**. It:

- asks the questions one at a time and refines;
- if scope is too large, **proposes** child issues (`Part of #N`) for the extra
  parts and narrows the current issue to one shippable unit — child-issue
  *creation* is **confirmed with the user** (it's the user's backlog);
- writes the spec to `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md` **on
  the branch**;
- runs its own approval gate (the run waits here until the user approves);
- transitions to `writing-plans` as its documented terminal step.

Do **not** duplicate any of these steps. After the spec is approved, `/auto`
posts the **assumptions comment** to the issue (`gh issue comment <N> --repo
stroem/shopping-list …`) — autonomous.

## Phase 4 — Plan

Reached as `brainstorming`'s terminal step (`writing-plans`). The plan doc is
written to `docs/superpowers/plans/YYYY-MM-DD-<topic>.md`, **on the branch**.

## Phase 5 — Execution (hands-off)

Invoke `superpowers:subagent-driven-development` + `superpowers:test-driven-development`.
Dispatch implementer subagents, each handed pointers to:

- `AGENTS.md` (read it first),
- the spec path,
- the plan path,
- the worktree path.

Rules for execution:

- **Tests fail before implementation** (TDD red → green).
- The **green bar** is `go test ./...` (backend) plus `flutter test` (app),
  whichever the change touches. DB-backed tests **skip cleanly** when
  `DATABASE_URL`/Postgres is absent and are not required to pass.
- Green after each logical unit; commit with Conventional Commits, **no AI
  attribution**.
- A unit that cannot be made green within a **retry budget of 3 attempts**
  triggers **escalate-and-pause** — stop and report "stuck on X, here are the
  options", leaving the worktree intact. Never an infinite loop.

## Phase 6 — Review

Invoke `superpowers:requesting-code-review` **and** dispatch the `spec-reviewer`
agent, passing it the spec path and the base ref `main` to diff against. The two
reviews compose: `requesting-code-review` answers *is the code correct?*;
`spec-reviewer` answers *does it fulfill the spec's intent and acceptance
criteria?*

Fix critical/warning findings. If review reveals the **spec itself** is wrong,
**escalate-and-pause** rather than silently redesigning.

## Phase 7 — Verify

Invoke `superpowers:verification-before-completion`. Run **real commands**;
confirm behavior matches the **spec** and that the suite is green. Evidence
before claims.

## Phase 8 — Publish PR (autonomous) + print summary

```bash
git push -u origin <branch>
gh pr create --repo stroem/shopping-list --title "…" --body "…"
```

The PR body is formatted: summary, assumptions, test plan, and `Closes #N` (if
fully covered) or `Part of #N` (if partial). Post the
**implementation-summary comment** to the issue (autonomous).

Then **print to the session** a concise report of what was done, **including the
PR URL**, the proposed follow-up issues (findings), and any proposed `AGENTS.md`
update. **No gate here** — the PR is reversible (close it, delete the branch).

## Phase 9 — Merge prompt + finish + cleanup

Ask the user: **"squash-merge this PR to `main`?"**

- **Yes** → `gh pr merge <PR> --repo stroem/shopping-list --squash`, then invoke
  `superpowers:finishing-a-development-branch` and clean up:
  ```bash
  git worktree remove <path>
  git branch -d <branch>
  git push origin --delete <branch>
  ```
  (Merge with `gh pr merge <PR> --repo stroem/shopping-list --squash`.)
  Confirm `main` is clean.
- **No** → leave the PR open; report the **exact cleanup commands** for the user
  to run after they merge it themselves. Worktrees are never left lying around
  once merged.

If the user also approved any proposed follow-up issues, **create them now**
(`Part of #parent` where relevant) — confirmed at the summary, created here.

---

## Rules

- **never commit to `main`** — worktree-first; every commit lands on the feature
  branch.
- **Tests fail before implementation** (TDD); `go test ./...` + `flutter test`
  are green after every commit.
- **No AI/Claude attribution** in commit messages (Conventional Commits 1.0.0).
- **Publishing a PR is autonomous** (it is reversible), but **merging to `main`
  happens only on user confirmation** (the merge prompt).
- **Bound every retry loop** — escalate-and-pause (3-attempt budget) on
  exhaustion; never grind forever and never ship something broken.
- **Halt only on a genuine blocker** — destructive/irreversible action, breaking
  the local-dev baseline (`go run ./cmd/api` + `flutter run -d chrome`),
  committing `data/`, or scope explosion.
  Everything else that is *not* stuck: assume, document in the issue, proceed.
- **Always clean up the worktree after merge** (remove worktree, delete branch).
- **Confirmed GitHub writes:** child-issue creation, follow-up issue creation,
  and the `main` merge. **Autonomous GitHub writes:** `--add-assignee @me`,
  comments (assumptions + implementation summary), and `git push` + `gh pr
  create`.
