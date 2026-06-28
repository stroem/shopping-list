package db

import "testing"

func TestMigrateURL(t *testing.T) {
	cases := map[string]string{
		"postgres://u:p@h:5432/d?sslmode=disable": "pgx5://u:p@h:5432/d?sslmode=disable",
		"postgresql://u:p@h:5432/d":               "pgx5://u:p@h:5432/d",
		"pgx5://u:p@h:5432/d":                     "pgx5://u:p@h:5432/d",
	}
	for in, want := range cases {
		if got := migrateURL(in); got != want {
			t.Errorf("migrateURL(%q) = %q, want %q", in, got, want)
		}
	}
}
