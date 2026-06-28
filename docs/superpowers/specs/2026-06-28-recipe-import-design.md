# Recipe import & cook mode — epic design

Status: approved-for-decomposition · Date: 2026-06-28

Paste a link to a recipe (website, YouTube, TikTok), have it parsed into a
standardized recipe, pick which ingredients you actually need to buy (some you
already have at home), push those onto a shopping list, and save the recipe to a
household/personal recipe list with a distraction-free **cook mode** (numbered
steps, image, keep-screen-on).

This is an **epic**: it decomposes into six issues, each of which gets its own
deeper spec → plan → implementation via `/auto`. This document is the shared
reference they all point back to.

## Locked decisions

- **AI parsing:** the backend calls **Claude** (this is an Anthropic-model
  project — default to the latest capable Claude model) to turn fetched source
  text into a structured recipe. A **small metered per-import cost is accepted**;
  it is the only deviation from strict scale-to-zero and is bounded because
  imports are occasional and low-volume. No always-on compute is added — parsing
  runs inside the on-demand request Lambda.
- **Sources:** **websites + YouTube** are first-class; **TikTok/Instagram Reels
  are best-effort** (caption/description text only, **no audio transcription**)
  and must **fail soft** — when extraction is weak, fall back to a manual
  paste/edit flow rather than erroring.
- **Ingredient handling:** Claude returns normalized `{name, amount, unit, raw}`
  per ingredient; the backend matches `name` against `food_catalog` (with
  raw-text fallback) so checked ingredients land in the correct **aisle**, with
  the amount preserved.
- **Recipe ownership:** every recipe has an `owner_device_id` and a `visibility`
  of `household` or `private`. **Default is `private`**, with an opt-in toggle to
  **share with the household**. Identity is device-based (`X-Device-Id`), so
  "per-user" means per-device.
- **Image:** store the source `og:image` / video thumbnail **URL** (no
  AI-generated images in v1).

## Architecture & data flow

```
App: paste/share link ─► POST /recipes/import { url }
  backend Lambda:
    1. classify URL → source_type (website | youtube | tiktok)
    2. fetch content:
         website → HTML; prefer schema.org/Recipe JSON-LD, else page text + og:image
         youtube → captions (timedtext) + title + description + thumbnail
         tiktok  → oEmbed/page caption text (+ thumbnail) — best-effort
    3. Claude → structured Recipe JSON:
         { title, image_url, servings?, ingredients:[{name,amount,unit,raw}], steps:[...] }
    4. match each ingredient.name → food_catalog (aisle); keep amount/unit
    5. return DRAFT (not persisted) to the app
  App: review screen — edit fields, checkbox each ingredient ("need to buy")
    ─► POST /recipes            (persist; household-scoped; visibility per toggle)
    ─► checked ingredients added to chosen list (existing list-items write path)
```

- **Import is online-only** (requires backend + Claude).
- **Saved recipes are offline-first:** stored in Drift/SQLite, replicated through
  the **existing pull-sync + outbox**, viewable with no network.

## Data model (new)

Both tables carry `updated_at` + `deleted_at` (soft delete) for pull sync, like
every other table.

`recipes`
- `id` (uuid, client-generatable for idempotency)
- `household_id`
- `owner_device_id`
- `visibility` — `household` | `private` (default `private`)
- `title`, `source_url`, `source_type` (`website`|`youtube`|`tiktok`)
- `image_url` (nullable)
- `servings` (nullable)
- `steps` — ordered list (jsonb or text[])
- `created_at`, `updated_at`, `deleted_at`

`recipe_ingredients`
- `id`, `recipe_id`, `position`
- `raw_text` — the original parsed line
- `name`, `amount` (nullable), `unit` (nullable)
- `catalog_id` (nullable FK → food_catalog), `aisle` (nullable)
- `updated_at`, `deleted_at`

## Sync & visibility

- The recipe pull-sync query returns rows where
  `visibility = 'household' OR owner_device_id = <requesting device>`.
- Accessing another device's `private` recipe returns **404** (no existence
  leak — same rule the app already applies cross-household).

## App surfaces

- **Import flow:** paste/share a link → spinner → **editable draft** → ingredient
  checklist (each defaults to "buy"; uncheck what you already have) → **"Add N to
  list"** (choose target list) + **"Save recipe"** with a **Household / Just me**
  toggle (default Just me).
- **Recipe list:** the device's visible recipes (household-shared + own private),
  shown as image cards with title + source icon; **private recipes show a lock
  badge**.
- **Recipe detail / cook mode:** standardized numbered steps + image + a
  **"Keep screen on"** toggle (Flutter `wakelock_plus`) — the always-on display.
  Fully usable offline.

## Decomposition into issues

1. **backend** — recipe schema + migrations (`recipes`, `recipe_ingredients`,
   incl. `owner_device_id` + `visibility`).
2. **backend** — `POST /recipes/import`: URL classification, source fetchers
   (website / YouTube / TikTok best-effort), Claude extraction → draft. **The
   metered AI cost lives here.** Fail-soft on weak extraction.
3. **backend** — ingredient → `food_catalog` matching (reuse/extend the
   autocomplete matcher) + persist recipe + add-checked-ingredients-to-list.
4. **backend** — recipe CRUD + pull-sync wiring with the visibility filter.
5. **app** — import flow UI (paste → review/edit → ingredient checklist → add to
   list + save with visibility toggle).
6. **app** — recipe list + recipe detail / cook mode (keep-screen-on, lock badge,
   offline).

## Out of scope (v1, YAGNI)

Servings/quantity scaling, audio transcription for TikTok, AI-generated images,
recipe ratings/favorites, sharing recipes across households, nutrition info,
re-import/duplicate detection.

## Open risks

- **Swedish catalog vs English recipes:** `food_catalog` is Livsmedelsverket
  (Swedish); many English ingredient names won't match and will fall back to raw
  text (still addable to a list, just no aisle). **Acceptable for v1**; a future
  issue can add multilingual/fuzzy matching.
- **TikTok extraction is fragile:** no clean transcript; caption-only and may be
  empty. Must degrade to manual paste/edit, never hard-fail the import.
- **Untrusted source fetching:** the import endpoint fetches arbitrary URLs —
  apply SSRF guards (block internal addresses), size/time limits, and treat
  fetched HTML as untrusted input to Claude.
