# Whole-segment aisle matching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `AisleForCategories`' substring matching with token-aware, most-specific-match-wins matching that kills the egg→eggplant and cream→ice-cream false positives.

**Architecture:** Normalize each OFF tag into match units (hyphen words + the full compound, language prefix dropped), match keywords by plural-insensitive equality (not substring), and select the match with the most keyword tokens, breaking ties by taxonomy priority. Single file: `internal/catalog/aisle_categories.go`.

**Tech Stack:** Go 1.26, standard library (`strings`), existing `internal/catalog`.

## Global Constraints

- Only `internal/catalog/aisle_categories.go` changes; `AisleForCategories(tags []string) *int` signature is unchanged (`ParseOFFLine`/seed path untouched). `AisleFor` in `aisle.go` is out of scope.
- Minimal keyword edit: add `"eggplant"` to the produce group; no broader taxonomy overhaul.
- All existing `TestAisleForCategories` cases must keep passing.
- Green bar = `go test ./...` from `backend/`; these tests need no DB.
- No new third-party dependency. Conventional Commits 1.0.0; **no AI/Claude attribution**. Commit only on branch `feat/issue-31-aisle-segment-matching`; never `main`.

---

### Task 1: Token-aware most-specific matcher for `AisleForCategories`

**Files:**
- Modify: `backend/internal/catalog/aisle_categories.go` (rewrite the matcher; add helpers; add `"eggplant"` to produce)
- Test: `backend/internal/catalog/aisle_categories_test.go` (add regression cases)

**Interfaces:**
- Consumes: `aisleGroup` struct (`aisle int`, `keywords []string`) from `aisle.go` (unchanged); test helpers `intp`/`deref` already in `aisle_test.go`.
- Produces: `AisleForCategories(tags []string) *int` (signature unchanged) + unexported `singular`, `tagUnits`, `keywordTokens` helpers.

- [ ] **Step 1: Add the failing regression tests**

In `backend/internal/catalog/aisle_categories_test.go`, add these cases to the existing `cases` slice in `TestAisleForCategories` (place them before the closing `}` of the slice literal):

```go
		{"ice-cream is frozen not dairy", []string{"en:ice-creams"}, intp(7)},
		{"eggplant is produce not dairy", []string{"en:eggplants"}, intp(1)},
		{"real cream stays dairy", []string{"en:creams"}, intp(2)},
		{"oil no longer matches boiled", []string{"en:boiled-vegetables"}, intp(1)},
		{"compound beats competing dairy tag", []string{"en:dairies", "en:ice-creams"}, intp(7)},
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/catalog/ -run TestAisleForCategories -v`
Expected: FAIL — `ice-cream is frozen not dairy` gets 2 (dairy) not 7, and `eggplant is produce not dairy` gets 2 not 1, under the current substring matcher.

- [ ] **Step 3: Rewrite `aisle_categories.go`**

Replace the entire contents of `backend/internal/catalog/aisle_categories.go` with:

```go
package catalog

import "strings"

// categoryGroups maps OFF English category-tag keywords to the v1 aisle
// taxonomy, in priority order (specific proteins first, pantry last as the
// catch-all). Keywords are matched against whole tag segments by
// AisleForCategories, not as raw substrings. Multi-word keywords (e.g.
// "ice-cream") are more specific and win over single-word ones.
var categoryGroups = []aisleGroup{
	{4, []string{"seafood", "fish", "salmon", "tuna", "shellfish", "shrimp", "herring", "mackerel"}},
	{3, []string{"meat", "poultry", "beef", "pork", "chicken", "ham", "sausage", "bacon", "charcuterie", "turkey"}},
	{2, []string{"dairies", "dairy", "milk", "cheese", "yogurt", "yoghurt", "butter", "cream", "egg"}},
	{1, []string{"fruit", "vegetable", "legume", "salad", "potato", "berries", "mushroom", "eggplant"}},
	{5, []string{"bread", "bakery", "viennoiserie", "baguette", "toast", "crackers"}},
	{7, []string{"frozen", "ice-cream", "ice-creams"}},
	{8, []string{"beverage", "water", "juice", "soda", "coffee", "tea", "drink", "wine", "beer", "alcoholic", "spirit"}},
	{9, []string{"chocolate", "candy", "candies", "snack", "biscuit", "chips", "confectioner", "sweet", "dessert"}},
	{6, []string{"pasta", "rice", "cereal", "flour", "condiment", "sauce", "canned", "spice", "oil", "groceries", "breakfast", "legumes-and-their-products"}},
}

// singular maps a word to a crude singular form so plural and singular tag
// segments compare equal: "dairies"->"dairy", "creams"->"cream", "eggs"->"egg".
// Applied to a hyphenated compound it only affects the trailing word
// ("ice-creams"->"ice-cream"), which is the intended behavior.
func singular(w string) string {
	if strings.HasSuffix(w, "ies") {
		return w[:len(w)-3] + "y"
	}
	return strings.TrimSuffix(w, "s")
}

// tagUnits normalizes an OFF category tag into its match units: the
// hyphen-separated words plus the full hyphenated compound, after dropping a
// leading language prefix. "en:ice-creams" -> ["ice", "creams", "ice-creams"];
// "en:eggplants" -> ["eggplants"].
func tagUnits(tag string) []string {
	t := strings.ToLower(tag)
	if i := strings.Index(t, ":"); i >= 0 {
		t = t[i+1:]
	}
	if t == "" {
		return nil
	}
	words := strings.Split(t, "-")
	if len(words) == 1 {
		return words
	}
	return append(words, t)
}

// keywordTokens counts the hyphen-separated tokens in a keyword; a multi-word
// keyword is more specific and beats a single-word one.
func keywordTokens(kw string) int {
	return strings.Count(kw, "-") + 1
}

// AisleForCategories returns the aisle for the most specific category keyword
// that matches any tag segment, or nil when nothing matches. A keyword matches a
// tag unit by plural-insensitive equality (not substring). Among all matches the
// keyword with the most tokens wins; ties break by taxonomy priority
// (categoryGroups order).
func AisleForCategories(tags []string) *int {
	var (
		bestAisle  int
		bestTokens int
		bestGroup  int
		matched    bool
	)
	for gi, g := range categoryGroups {
		for _, kw := range g.keywords {
			kwTok := keywordTokens(kw)
			kwSing := singular(kw)
			for _, tag := range tags {
				for _, unit := range tagUnits(tag) {
					if singular(unit) != kwSing {
						continue
					}
					if !matched || kwTok > bestTokens || (kwTok == bestTokens && gi < bestGroup) {
						matched = true
						bestAisle = g.aisle
						bestTokens = kwTok
						bestGroup = gi
					}
				}
			}
		}
	}
	if !matched {
		return nil
	}
	return &bestAisle
}
```

(Changes vs the original: `"eggplant"` added to the produce group; the substring
loop replaced by the token-aware most-specific matcher plus `singular`/`tagUnits`/
`keywordTokens` helpers. `categoryGroups` data is otherwise identical.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/catalog/ -run TestAisleForCategories -v`
Expected: PASS — all existing cases plus the five new ones.

- [ ] **Step 5: Build, vet, gofmt, full suite**

Run: `go build ./... && go vet ./... && gofmt -l internal/catalog/`
Expected: no output.

Run: `go test ./...`
Expected: all packages ok; DB-backed packages skip cleanly without Docker.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/aisle_categories.go internal/catalog/aisle_categories_test.go
git commit -m "feat(catalog): whole-segment aisle matching for OFF categories

Replace strings.Contains substring matching in AisleForCategories with
token-aware, most-specific-match-wins matching over normalized tag
segments. Kills egg->eggplant and cream->ice-cream false positives:
ice-cream maps to frozen (7), eggplant to produce (1). Plural-insensitive
whole-word/compound equality also stops oil/tea matching unrelated tags.

Closes #31"
```

---

## Self-Review

**Spec coverage:**
- Match split tag segments / whole-word equality instead of `strings.Contains` → Step 3 `tagUnits` + `singular` + equality. ✓
- Most-specific (most-token) wins, group priority tie-break → Step 3 selection logic. ✓
- Regression cases ice-cream→7, eggplant→1, existing priority cases pass → Step 1 cases + unchanged existing cases; Step 4 runs them. ✓
- `"eggplant"` added to produce → Step 3 produce group. ✓
- Signature unchanged, ParseOFFLine untouched → `AisleForCategories(tags []string) *int` preserved. ✓
- `cream→dairy(2)` over-correction guard + `oil`/`boiled` whole-word → Step 1 cases `real cream stays dairy`, `oil no longer matches boiled`. ✓

**Placeholder scan:** none — full file contents and exact test cases provided.

**Type consistency:** `aisleGroup{aisle, keywords}` matches `aisle.go`; `singular`/`tagUnits`/`keywordTokens` names consistent between the matcher and their definitions; `intp` test helper matches existing usage in `aisle_categories_test.go`. ✓
