package db_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/stroem/shopping-list/backend/internal/db"
)

// indexColsRe grabs the parenthesised column list at the end of a pg_indexes
// indexdef, e.g. "... USING btree (household_id, updated_at)".
var indexColsRe = regexp.MustCompile(`\(([^)]*)\)\s*$`)

// pgIndex is one row of pg_indexes for a table, with the column list parsed out
// of its indexdef.
type pgIndex struct {
	name string
	def  string
	cols []string // column names in order, lowercased, direction/opclass stripped
}

// tableIndexes returns every public index on the given table (parsed), reusing
// startPostgres' running container via the supplied URL.
func tableIndexes(t *testing.T, url, table string) []pgIndex {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx,
		`SELECT indexname, indexdef FROM pg_indexes
		   WHERE schemaname = 'public' AND tablename = $1`, table)
	if err != nil {
		t.Fatalf("query pg_indexes for %s: %v", table, err)
	}
	defer rows.Close()

	var out []pgIndex
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatalf("scan pg_indexes row: %v", err)
		}
		out = append(out, pgIndex{name: name, def: def, cols: parseIndexCols(def)})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pg_indexes rows: %v", err)
	}
	return out
}

// parseIndexCols extracts the ordered column names from an indexdef, dropping
// any sort direction or operator class (e.g. "purchase_count DESC" -> "purchase_count").
func parseIndexCols(def string) []string {
	m := indexColsRe.FindStringSubmatch(def)
	if m == nil {
		return nil
	}
	var cols []string
	for _, part := range strings.Split(m[1], ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		cols = append(cols, strings.ToLower(fields[0]))
	}
	return cols
}

// hasIndexOnCols reports whether any index has exactly the wanted ordered
// column list.
func hasIndexOnCols(indexes []pgIndex, want ...string) bool {
	for _, idx := range indexes {
		if equalCols(idx.cols, want) {
			return true
		}
	}
	return false
}

func equalCols(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestRecipeSchemaHasSyncAndFKIndexes asserts that migration 0006 creates the
// pull-sync cursor indexes and the FK-lookup index the schema needs. The
// primary-key indexes (recipes_pkey / recipe_ingredients_pkey) already exist and
// cover only (id); these assertions target the sync + FK indexes specifically.
func TestRecipeSchemaHasSyncAndFKIndexes(t *testing.T) {
	url := startPostgres(t)
	if err := db.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	recipes := tableIndexes(t, url, "recipes")
	ingredients := tableIndexes(t, url, "recipe_ingredients")

	// recipes: pull-sync cursor scoped to the household.
	if !hasIndexOnCols(recipes, "household_id", "updated_at") {
		t.Errorf("recipes: missing sync index on (household_id, updated_at); got indexes %s",
			formatIndexes(recipes))
	}

	// recipe_ingredients: a recipe's own sync cursor.
	if !hasIndexOnCols(ingredients, "recipe_id", "updated_at") {
		t.Errorf("recipe_ingredients: missing sync index on (recipe_id, updated_at); got indexes %s",
			formatIndexes(ingredients))
	}

	// recipe_ingredients: FK lookup on recipe_id — a plain single-column index,
	// distinct from the (recipe_id, updated_at) composite above.
	if !hasIndexOnCols(ingredients, "recipe_id") {
		t.Errorf("recipe_ingredients: missing FK-lookup index on (recipe_id); got indexes %s",
			formatIndexes(ingredients))
	}
}

func formatIndexes(indexes []pgIndex) string {
	var b strings.Builder
	for _, idx := range indexes {
		b.WriteString("\n  ")
		b.WriteString(idx.name)
		b.WriteString(" -> ")
		b.WriteString(idx.def)
	}
	return b.String()
}
