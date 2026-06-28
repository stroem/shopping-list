package suggest

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// failQuerier fails if any query runs — proves the empty-q path never hits the DB.
type failQuerier struct{}

func (failQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("Query must not be called")
}
func (failQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	return errRow{}
}

type errRow struct{}

func (errRow) Scan(...any) error { return errors.New("QueryRow must not be called") }

func TestSuggest_EmptyQueryReturnsEmptyWithoutDB(t *testing.T) {
	s := New(failQuerier{})
	for _, q := range []string{"", "   ", "\t"} {
		got, err := s.Suggest(context.Background(), "dev-1", q, 10)
		if err != nil {
			t.Fatalf("q=%q: unexpected err %v", q, err)
		}
		if len(got) != 0 {
			t.Fatalf("q=%q: got %d results, want 0", q, len(got))
		}
	}
}

func TestClampLimit(t *testing.T) {
	cases := map[int]int{0: 10, -5: 10, 1: 1, 10: 10, 25: 25, 26: 25, 999: 25}
	for in, want := range cases {
		if got := clampLimit(in); got != want {
			t.Errorf("clampLimit(%d) = %d, want %d", in, got, want)
		}
	}
}
