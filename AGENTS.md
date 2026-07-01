# AGENTS.md — agent orientation for shopping-list ("Handla")

This is the cross-tool **`AGENTS.md`** standard file: an agent-facing orientation
read by tools that arrive with no context, and a stable pointer target for the
workflow skills below (`/create-issue`, `/auto`, and the `auto` / `tester` /
`implementer` / `code-reviewer` / `spec-reviewer` agents). The design history
under [`docs/superpowers/`](./docs/superpowers/) is the deep reference; the
**GitHub issue + PR thread** is durable shared memory. Where this file and a spec
disagree, the **latest approved spec** wins.

- **Repository:** `stroem/shopping-list` (GitHub) — the canonical slug every agent
  and `gh` command uses. Defined here so it lives in exactly one place.
- **Canonical commands:** the **Build, run, test** section below is the single
  source of truth for how to build, run, and test this repo. Agents read those
  commands from here — they do **not** hardcode or invent their own.

## What this is

**A shared shopping list for food — only food, done well.** Two people (you + a
partner) share lists in a household; the app learns what you actually buy and
sorts the list by store aisle so shopping is fast. It is a **Flutter app**
(targets **web + Android** first, iOS-ready) talking to a **Go backend deployed
as an AWS Lambda behind API Gateway**, backed by **Neon Postgres**
(scale-to-zero). Offline-first with pull sync.

**Hard principle: keep running cost ≈ 0.** Everything must scale to zero or fit a
free tier — no always-on compute. This is a design invariant, not an afterthought.

**Explicitly NOT in scope:** alcohol / Systembolaget, Home & Gift categories,
push notifications, realtime sync, ratings & favorites. Those are precisely what
made the earlier attempts (`../shopping`, `../shopping_v2`) too big. Scope creep
back into them is rejected on purpose. Full rationale:
[`docs/superpowers/specs/2026-06-28-handla-food-mvp-design.md`](./docs/superpowers/specs/2026-06-28-handla-food-mvp-design.md).

## Repo layout (monorepo)

```
app/        Flutter app (Dart, Riverpod, Drift/SQLite, outbox). Targets: web + Android.
backend/    Go module. Standard Go project layout:
  cmd/api/      local HTTP server for dev (same handlers as Lambda, no AWS needed)
  cmd/lambda/   Lambda entrypoint — wraps the same chi router for API Gateway HTTP API
  cmd/seed/     one-shot importer: loads data/food/* into Postgres (catalog + EAN)
  internal/     domain packages: households, users, lists, listitems, checkoffs,
                catalog, eanmappings, sync, config, db
  migrations/   golang-migrate SQL files
data/       GITIGNORED, never committed. Source data (Livsmedelsverket + Open Food
            Facts) consumed once by cmd/seed. Not code; not shipped.
infra/      IaC for Lambda + API Gateway + Neon wiring (SAM or Terraform), cost-minimal.
docs/superpowers/   specs + plans (design history, deep reference).
```

## Build, run, test

A root **`justfile`** wraps the common tasks (`just` to list): `just run` (API),
`just db` / `just db-stop` (local Postgres, prints the URL), `just migrate`,
`just test` (Go + Flutter), `just app-run`. **`cmd/api` requires `DATABASE_URL`**
— use `just db` to get one locally. Run **`just hooks`** once per clone to install
the git guardrails (block AI attribution, direct commits to `main`, and `data/`).

**Backend (Go), from `backend/`:**

- `go build ./...` · `go test ./...`
- `go run ./cmd/api` — local API on `:8080`, **requires `DATABASE_URL`**; `GET /healthz` pings the DB (200 JSON / 503).
- `go run ./cmd/migrate <up|down|version|force <v>|steps <n>|goto <v>>` — migration CLI (reads `DATABASE_URL`; destructive ops prompt on a TTY, `-y` to skip).
- `go run ./cmd/seed livsmedelsverket|openfoodfacts` — import `data/food/*` into the catalog + EAN tables (one-shot, local). `livsmedelsverket` also reads `livsmedelsverket_klassificeringar.json` (food groups) when present.
- `GET /v1/sync?since=<RFC3339Nano cursor>` — pull-sync: household-scoped rows changed since the cursor (soft-deletes included); returns `{cursor, changes}`. Mutating routes accept `Idempotency-Key` (replayed via the `idempotency` middleware).
- Migrations via `golang-migrate` (embedded from the `migrations/` dir).

**App (Flutter), from `app/`:**

- `flutter run -d chrome` (web) or `-d <android-device>`
- `flutter test` · `flutter build web` / `flutter build apk`
- Codegen (Riverpod + Drift): `dart run build_runner build --delete-conflicting-outputs`

**Green bar = `go test ./...` (backend) + `flutter test` (app).** DB-backed
tests **skip cleanly** when `DATABASE_URL`/Postgres is absent, so CI is
unaffected.

## Architecture

```
Flutter app (web + Android) ─ Riverpod · Drift/SQLite · outbox
        │  HTTPS / JSON
        ▼
API Gateway (HTTP API — cheaper than REST) ─► Go Lambda (one binary, chi adapter)
        │  pgx + Neon pooler
   Neon Postgres (scale-to-zero)
        ▲
  EventBridge-scheduled Lambda ─► weekly food-catalog refresh (optional)
```

`cmd/api` and `cmd/lambda` share the **same router and handlers** — local dev
never requires AWS. AWS specifics are deploy-time only.

## Data model

All tables carry `updated_at` + `deleted_at` (soft delete) for pull sync.
Entities: `households`, `users` (device_id, household_id), `lists`,
`list_items`, `items` (per-household product master driving autocomplete),
`check_off_events` (append-only history → stats), `food_catalog`
(Livsmedelsverket generics), `ean_mappings` (Open Food Facts barcode → product).
See the design spec for the field-level schema.

## Glossary (ubiquitous language)

Use these exact terms in specs, issues, code, and commits — one name per concept
cuts agent verbosity and drift. To refine or add a term, use the `domain-modeling`
skill; this list and the `## Data model` section are its home.

- **Household** — the sharing & access boundary. Everything is household-scoped;
  cross-household access returns **404** (no existence leak). Joined via an **invite code**.
- **List** / **list item** — a named shopping list, and one line on it (both household-scoped).
- **Item** — the per-household product master that drives autocomplete; distinct from a
  *list item* (one occurrence of a product on a list).
- **Check-off event** — append-only record of a list item being ticked off; the history
  that feeds stats and aisle learning.
- **food_catalog** — Livsmedelsverket generic foods, the autocomplete source.
- **ean_mapping** — Open Food Facts barcode → product mapping, the scanning source.
- **Outbox** — the local write queue; writes go local-first and replay to the backend.
- **Pull sync** — `GET /v1/sync?since=<cursor>`: household-scoped rows changed since the
  cursor (soft-deletes included). No realtime push in v1.
- **Idempotency key** — client-generated UUID sent as `Idempotency-Key` so double-taps and
  outbox replays never duplicate.
- **Device id** — `X-Device-Id`; retained for future device/sync use, **not** identity
  (identity is OIDC `(issuer, subject)`).

## Sync & sharing

- **Local-first:** the app reads from Drift/SQLite; writes go local first and into
  an **outbox** that replays to the backend (on launch, on reconnect, with backoff).
- **Pull sync:** `GET …?since=<updated_at cursor>`. **No realtime push in v1**
  (cost). Push is a future ticket.
- **Idempotency:** client-generated UUIDs + `Idempotency-Key` so double-taps and
  outbox replays never duplicate.
- **Sharing / auth:** sign-in is **Google / OIDC** — the backend verifies an ID
  token (`Authorization: Bearer`, configurable issuer via `OIDC_ISSUER` /
  `OIDC_AUDIENCE`) and keys a person by `(issuer, subject)`, auto-creating the
  `users` row on first request. People **share a household via an invite code**
  (`households.invite_code`, carried by a shareable link). Everything is
  household-scoped; cross-household access returns **404** (no existence leak).
  `X-Device-Id` is retained only for future device/sync use, not identity.

## The `data/` directory

`data/` is **gitignored and never committed** — it is downloaded source material
(Livsmedelsverket + Open Food Facts), not code, and is not shipped with the app.
It exists only as input to `backend/cmd/seed`, which imports it once into Postgres:
`livsmedelsverket_products.json` → `food_catalog` (autocomplete);
`swedish_food_products.jsonl` → `ean_mappings` (barcode scanning). Never add
`data/` to a commit; never load it at runtime.

## Invariants & conventions

- **Local dev must never break.** `go run ./cmd/api` + `flutter run -d chrome`
  against a local/Neon Postgres is the baseline. AWS is additive and deploy-time.
- **Keep cost ≈ 0.** Prefer scale-to-zero (Neon), HTTP API over REST API GW,
  scheduled Lambda over always-on cron. No always-on compute.
- **Food only.** Reject scope creep into alcohol or non-food categories — that is
  why the earlier attempts ballooned.
- **`data/` is never committed.**
- **Config via env** (`DATABASE_URL`, etc.); `.env` is dotenv-loaded locally, an
  `.env.example` is committed.
- **Git:** Conventional Commits 1.0.0; Conventional Branches; **no AI/Claude
  attribution in commit messages** (hard rule, hook-enforced via `just hooks`).
  Standard Go project layout.
- **Workflow:** new work goes assumptions → spec (`docs/superpowers/specs/`) →
  plan (`docs/superpowers/plans/`) → subagent-driven TDD execution → review →
  finish in a worktree. Don't skip the design step. The `/auto` orchestrator runs
  this lifecycle autonomously (self-contained, no plugin dependency).

## If you were dispatched as a subagent

- **Read this file first.** You start with **no conversation context** — the
  orchestrator hands you the spec path, plan path, and worktree path. **Use them**;
  do not invent paths or scope.
- **Trust `go test ./...` and `flutter test`** over editor diagnostics.
- The green bar is `go test ./...` + `flutter test`; DB-backed tests skip cleanly
  without `DATABASE_URL` and aren't required.
- **Commit with Conventional Commits and no AI attribution.** Work only inside the
  worktree you were given; never commit to `main`.

## Specialised workflow

Named entry points turn this repo's development practice into stable,
GitHub-anchored commands, all against **`stroem/shopping-list`** — self-contained,
with **no `superpowers` plugin dependency**:

- **`/create-issue <description>`** — draft and file a GitHub issue (structured
  title + body + suggested labels), then offer `/auto`.
- **`/auto [issue-number]`** — a thin trigger that resolves the issue (and the
  end-of-run merge prompt), then launches the self-contained **`auto` agent**: a
  fully autonomous issue→PR orchestrator that runs assumptions → worktree → spec →
  plan → a subagent-driven TDD loop → two-stage review → verify → PR, hands-off,
  asking before squash-merging to `main`.
- **`auto` agent** — the orchestrator; owns git and commits, dispatches the
  `tester` + `implementer` per bite-sized TDD task (independent tasks in
  parallel), and runs the two reviewers below.
- **`tester` agent** — testing expert; writes the **failing** test for one task
  (RED) and proves it fails for the right reason.
- **`implementer` agent** — production-code expert; writes the **minimal** code to
  turn the test green (GREEN).
- **`code-reviewer` agent** — code-correctness review (bugs, edge cases, idioms,
  test quality).
- **`spec-reviewer` agent** — goal-conformance review (does the implementation
  fulfil the spec's intent and the issue's acceptance criteria?), distinct from
  code-correctness review.

**GitHub is shared memory.** Because these agents are stateless, durable
knowledge lives where they can always read it back: **this file**, the **issue
thread**, and the **PR body**. Write decisions and assumptions there, not in
ephemeral chat.
