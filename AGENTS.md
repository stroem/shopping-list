# AGENTS.md — agent orientation for shopping-list ("Handla")

This is the cross-tool **`AGENTS.md`** standard file: an agent-facing orientation
read by tools that arrive with no context, and a stable pointer target for the
workflow skills below (`/create-issue`, `/auto`, the `spec-reviewer` agent). The
design history under [`docs/superpowers/`](./docs/superpowers/) is the deep
reference; the **GitHub issue + PR thread** is durable shared memory. Where this
file and a spec disagree, the **latest approved spec** wins.

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

**Backend (Go), from `backend/`:**

- `go build ./...` · `go test ./...`
- `go run ./cmd/api` — local API on `:8080`, reads `DATABASE_URL` (local or Neon).
- `go run ./cmd/seed` — import `data/food/*` into the catalog + EAN tables (one-shot, local).
- Migrations via `golang-migrate` (the `migrations/` dir).

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

## Sync & sharing

- **Local-first:** the app reads from Drift/SQLite; writes go local first and into
  an **outbox** that replays to the backend (on launch, on reconnect, with backoff).
- **Pull sync:** `GET …?since=<updated_at cursor>`. **No realtime push in v1**
  (cost). Push is a future ticket.
- **Idempotency:** client-generated UUIDs + `Idempotency-Key` so double-taps and
  outbox replays never duplicate.
- **Sharing / auth:** a household is a shared-secret UUID (join code) + per-device
  `X-Device-Id`. **No OAuth in v1.** Everything is household-scoped; cross-household
  access returns **404** (no existence leak).

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
  attribution in commit messages** (hard rule). Standard Go project layout.
- **Workflow:** new work goes brainstorm → spec (`docs/superpowers/specs/`) →
  plan (`docs/superpowers/plans/`) → subagent-driven execution → finish in a
  worktree. Don't skip the design step.

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

Three named entry points turn this repo's superpowers practice into stable,
GitHub-anchored commands, all against **`stroem/shopping-list`**:

- **`/create-issue <description>`** — draft and file a GitHub issue (structured
  title + body + suggested labels), then offer `/auto`.
- **`/auto [issue-number]`** — autonomous issue→PR orchestrator: interactive spec
  phase, then hands-off plan → TDD via subagents → two-stage review → verify →
  PR, asking before squash-merging to `main`.
- **`spec-reviewer` agent** — goal-conformance review (does the implementation
  fulfil the spec's intent and the issue's acceptance criteria?), distinct from
  code-correctness review.

**GitHub is shared memory.** Because these agents are stateless, durable
knowledge lives where they can always read it back: **this file**, the **issue
thread**, and the **PR body**. Write decisions and assumptions there, not in
ephemeral chat.
