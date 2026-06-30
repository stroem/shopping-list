# Whole-segment aisle matching for `AisleForCategories` — design

**Issue:** [#31](https://github.com/stroem/shopping-list/issues/31) — Refine AisleForCategories: whole-segment matching to kill short-keyword false positives
**Milestone:** M1 — Food catalog & data pipeline
**Date:** 2026-06-28
**Status:** approved

## Problem

`AisleForCategories` (`internal/catalog/aisle_categories.go`) maps Open Food Facts
`categories_tags` to the v1 9-aisle taxonomy by `strings.Contains` over short
keywords. Substring matching over short keywords misfires:

- `egg` matches `en:eggplants` → aisle 2 (dairy) instead of 1 (produce);
- `cream` matches `en:ice-creams` → dairy (2) wins over frozen (7) because dairy
  is checked first;
- `oil` matches `en:boiled-*`, `tea` matches unrelated tags.

## Goal

Improve aisle accuracy for OFF products by matching **whole tag segments** instead
of raw substrings, without changing the `AisleForCategories` signature or its
callers.

## Approach: token-aware, most-specific-match-wins

Replace the substring matcher with token-aware matching that prefers the most
specific keyword. The keyword data (`categoryGroups`, in taxonomy-priority order)
is unchanged in shape; only the matching logic and the produce keyword list change.

### 1. Normalize a tag into match units

For each tag (e.g. `en:ice-creams`):

- lowercase it;
- drop a leading language prefix — the segment before the **first** `:` when that
  segment is a short alpha code (e.g. `en:`, `fr:`); keep the remainder
  (`ice-creams`);
- split the remainder on `-` into **words** (`["ice", "creams"]`);
- the **match units** are those words **plus** the full hyphenated compound:
  `{"ice", "creams", "ice-creams"}`.

`en:eggplants` → `{"eggplants"}`. `en:fresh-vegetables` →
`{"fresh", "vegetables", "fresh-vegetables"}`.

### 2. Match a keyword against a unit (plural-insensitive equality)

A keyword matches a unit when `singular(keyword) == singular(unit)` — **equality on
normalized forms**, never substring. Keywords may themselves be hyphenated
compounds (`"ice-cream"`), compared against compound units.

```
singular(w):
  if strings.HasSuffix(w, "ies") -> w[:len(w)-3] + "y"   // dairies -> dairy, berries -> berry
  else                           -> strings.TrimSuffix(w, "s")  // creams -> cream, eggs -> egg
```

Applied to a hyphenated compound, `singular` only normalizes the trailing word
(`ice-creams` → `ice-cream`), which is the desired behavior for the compound unit.

### 3. Pick the winner: most tokens, then group priority

Scan every (group, keyword, tag-unit) combination. A keyword's **specificity** is
its token count (number of `-`-separated words: `"ice-cream"` = 2, `"cream"` = 1).
Among all matches:

1. the highest token count wins (most specific);
2. ties break by **group priority** — the earlier group in `categoryGroups` wins
   (preserving the existing taxonomy order: proteins first, pantry last).

Return the winning group's aisle as `*int`, or `nil` when nothing matches.

### Why this resolves the cases

- `en:eggplants` → unit `eggplants`; `singular("eggplants") = "eggplant" ≠ "egg"`,
  so dairy's `egg` no longer matches. With `"eggplant"` added to the produce
  group, it maps to produce (1).
- `en:ice-creams` → dairy's `cream` (1 token) and frozen's `ice-cream` (2 tokens)
  both match; the 2-token compound wins → frozen (7).
- `en:creams` → only dairy's `cream` matches (1 token) → dairy (2): real cream is
  unaffected.
- `oil`, `tea` only match whole words, so `en:boiled-*` and unrelated tags no
  longer false-positive.
- `seafood beats dairy` (`en:dairies`, `en:seafood`): both 1-token matches, tie
  broken by group priority — seafood's group precedes dairy's → seafood (4).

## Keyword-list change

Add `"eggplant"` to the produce group (aisle 1) so `en:eggplants` maps to produce —
this is required by the acceptance criteria. No other keyword changes are made
unless a previously-passing case regresses; the goal is a minimal, behavior-driven
edit, not a taxonomy overhaul (that breadth belongs to #28).

## Scope

- **In scope:** `internal/catalog/aisle_categories.go` — rewrite `AisleForCategories`
  to the algorithm above, add unexported `normalizeTag`/`singular` helpers, add
  `"eggplant"` to produce.
- **Out of scope:** `AisleFor` (the Swedish food *name* matcher in `aisle.go`) — the
  issue targets `categories_tags`; `AisleFor`'s `gris`/`ris`, `filé`/`fil` false
  positives are a separate concern. Also out of scope: the `huvudgrupp` food-group
  enrichment (#28), which this issue is "related to" but distinct from.
- The `AisleForCategories(tags []string) *int` signature is unchanged, so
  `ParseOFFLine` and the seed path are untouched (no behavior change beyond more
  accurate aisle assignment).

## Testing

`internal/catalog/aisle_categories_test.go` (table-driven, already present):

- **All existing cases keep passing** (dairy, seafood-beats-dairy, meat, produce,
  bread, frozen, drink-incl-alcohol, candy, pantry catch-all, no-match, empty).
- **New regression cases:**
  - `en:ice-creams` → 7 (frozen) — the compound beats dairy's `cream`;
  - `en:eggplants` → 1 (produce) — `egg` no longer hijacks it;
  - `en:creams` → 2 (dairy) — real cream still maps to dairy (guards against
    over-correction);
  - `en:boiled-vegetables` → 1 (produce), proving `oil` no longer matches `boiled`
    (whole-word matching) and the produce word still wins;
  - a multi-token specificity case, e.g. `["en:ice-creams"]` already covers
    compound-beats-word; add `["en:dairies","en:ice-creams"]` → 7 to prove the
    compound wins even when a competing dairy tag is also present.

Green bar = `go test ./internal/catalog/` (no DB needed for these). Full
`go test ./...` stays green.

## Files touched

- `backend/internal/catalog/aisle_categories.go` — new matcher + helpers; add
  `"eggplant"` to produce keywords.
- `backend/internal/catalog/aisle_categories_test.go` — add the regression cases.
