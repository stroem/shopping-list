---
name: auto
description: Use when the user types /auto <issue-number>, /auto with no argument (recommend an issue to build), or otherwise asks to autonomously take a GitHub issue through to an open PR in stroem/shopping-list.
argument-hint: "[issue-number]"
---

# /auto — autonomous issue → PR orchestrator

`/auto` is a **thin trigger** around the self-contained **`auto` agent**
(`.claude/agents/auto.md`). It does **not** depend on the `superpowers` plugin —
the agent owns the full lifecycle (assumptions → worktree → spec → plan → TDD loop
→ review → verify → PR), inlining every discipline it used to borrow and
delegating small tasks to repo-local expert subagents (`tester`, `implementer`,
`code-reviewer`, `spec-reviewer`).

The skill keeps only the **two points that need the user** in the foreground —
**issue selection** (when no number is given) up front, and the **merge-to-`main`
prompt** at the end. Everything between is hands-off inside the agent.

```
/auto <N> → [resolve issue] → launch `auto` agent (hands-off) → open PR → [merge? prompt] → cleanup
```

Read `AGENTS.md` for repo context; it is the doc every subagent is pointed at.

---

## Step 1 — Resolve the issue (foreground)

- **`/auto <N>`** → use issue #N. Proceed straight to Step 2.
- **`/auto` (no arg)** → **milestones are the build order.** List open milestones
  in order and prefer an issue from the **earliest milestone that still has open
  issues**:

  ```bash
  gh api repos/stroem/shopping-list/milestones --jq \
    'sort_by(.number)[] | select(.open_issues>0) | "\(.number)\t\(.title)\t\(.open_issues) open"'
  # then list issues in that milestone, e.g.:
  gh issue list --repo stroem/shopping-list --state open --milestone "M0 — Foundation & infra"
  ```

  Recommend one from that milestone (foundation milestones gate later ones — don't
  pull an M3 app issue while M0 infra is unbuilt), ranking `ready-for-agent` issues
  first, then by dependencies, labels, and age. Fall back to
  `gh issue list --repo stroem/shopping-list --state open` only when no milestone
  has open issues. **Confirm the choice with the user before launching.**
- **Invalid arg** (not an integer, and not the no-arg case) → print the usage
  (`/auto <issue-number>` or `/auto` to pick one) and stop.

### Readiness check (before launching)

`/auto` runs the agent **hands-off and asks the user nothing**, so only launch on
an issue that has been vetted. After resolving the issue number, check its labels:

- **`ready-for-agent`** → proceed to Step 2.
- **`needs-info`, or no readiness label** → **warn the user and confirm before
  launching.** The issue may still hold open decisions the agent will silently turn
  into assumptions. Offer to grill it first (the `/create-issue` alignment gate or
  the `grilling` skill) and flip it to `ready-for-agent`, or to launch anyway
  accepting that the ambiguity becomes documented assumptions. Launch only on
  explicit confirmation.

## Step 2 — Launch the `auto` agent (hands-off)

Dispatch the **`auto`** agent (Agent tool, `subagent_type: auto`) **in the
background**, so the user keeps an interactive prompt while it runs. Prompt:

```
Issue: #<N> (repo stroem/shopping-list)

Run the full autonomous lifecycle for this issue per your agent instructions:
fetch context & claim, prior-art check, assumptions (assume & document — never
ask the user), worktree from origin/main, spec + plan on the branch, the
subagent-driven TDD loop (per task: `tester` writes the failing test, then
`implementer` makes it green; run independent tasks in parallel; YOU own git and
commit each task), review (`code-reviewer` + `spec-reviewer`), verify with real
command output, then push and open the PR. Use AGENTS.md as the source of truth
for the repo slug and build/test commands. Terminal state is an OPEN PR plus a
report — do NOT merge to main and do NOT prompt; the merge decision returns to me.

Halt only on a genuine blocker (destructive migration, data loss, breaking the
local-dev baseline, committing data/, scope explosion, anything touching
production / bypassing CI/CD). Otherwise assume, document, and proceed.
```

Tell the user the agent has started for issue #N and that you'll surface the
result (PR URL, a blocker report, or an already-done finding) when it returns. Do
**not** do the implementation work yourself — the agent owns it end to end.

## Step 3 — On return: relay, then the merge prompt (foreground)

When the agent reports back, relay its summary to the user — **the PR URL**, the
assumptions it made, any proposed follow-up issues, and the exact cleanup commands.

If the agent **escalated-and-paused**, **halted on a blocker**, or found the work
**already done**, relay that and stop — there is nothing to merge.

Otherwise ask the user: **"squash-merge this PR to `main`?"**

- **Yes** →
  ```bash
  gh pr merge <PR> --repo stroem/shopping-list --squash
  ```
  then clean up (from the repo root, not inside the worktree):
  ```bash
  git worktree remove <worktree-path>
  git branch -d <branch>
  git push origin --delete <branch>
  ```
  Confirm `main` is clean. If the user approved any proposed follow-up issues,
  create them now (`Part of #<parent>` where relevant).
- **No** → leave the PR open and hand the user the exact cleanup commands to run
  after they merge it themselves. Worktrees are never left lying around once merged.

---

## Rules

- **Never commit to `main`** — the agent is worktree-first; every commit lands on
  the feature branch.
- **Fully autonomous execution** — the agent never asks the user questions; it
  assumes and documents. The only user gates are issue selection (Step 1) and the
  merge prompt (Step 3), both owned by this skill.
- **Publishing a PR is autonomous** (reversible); **merging to `main` happens only
  on the user's confirmation.**
- **Bound retries / blocker halt** — the agent escalates-and-pauses after a
  3-attempt budget and halts on genuine blockers; never grinds forever, never
  ships broken.
- **Always clean up the worktree after merge** (remove worktree, delete branch).
- **No AI/Claude attribution** in commit messages (Conventional Commits 1.0.0).
