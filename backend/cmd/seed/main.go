// Command seed imports food reference data into Postgres. It reads DATABASE_URL.
//
//	go run ./cmd/seed livsmedelsverket [--file <path>]   # Livsmedelsverket -> food_catalog
//	go run ./cmd/seed openfoodfacts   [--file <path>]    # Open Food Facts   -> ean_mappings
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/stroem/shopping-list/backend/internal/catalog"
	"github.com/stroem/shopping-list/backend/internal/config"
	"github.com/stroem/shopping-list/backend/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "livsmedelsverket":
		if err := seedLivsmedelsverket(os.Args[2:]); err != nil {
			log.Fatalf("seed livsmedelsverket: %v", err)
		}
	case "openfoodfacts":
		if err := seedOpenFoodFacts(os.Args[2:]); err != nil {
			log.Fatalf("seed openfoodfacts: %v", err)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: seed <livsmedelsverket|openfoodfacts> [--file <path>]")
	os.Exit(2)
}

func seedLivsmedelsverket(args []string) error {
	fs := flag.NewFlagSet("livsmedelsverket", flag.ExitOnError)
	file := fs.String("file", "../data/food/livsmedelsverket_products.json", "path to livsmedelsverket products json")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	f, err := os.Open(*file)
	if err != nil {
		return fmt.Errorf("open %s: %w", *file, err)
	}
	defer f.Close()

	rows, err := catalog.ParseLivsmedelsverket(f, nil)
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	inserted, updated, err := catalog.UpsertFood(ctx, pool, rows)
	if err != nil {
		return err
	}
	log.Printf("livsmedelsverket: %d parsed, %d inserted, %d updated", len(rows), inserted, updated)
	return nil
}

// seedStats holds the running counts produced while streaming OFF JSONL.
type seedStats struct {
	Parsed    int
	Skipped   int
	Malformed int
	Inserted  int
	Updated   int
}

// streamOFF reads Open Food Facts JSONL from r, batches parsed rows up to
// batchSize, and flushes each full batch (and the remainder at EOF) through
// upsert. A malformed line (ParseOFFLine error) or a skippable line (nameless/
// codeless) is counted and the stream continues — one bad line never aborts.
// It uses bufio.Reader (not Scanner) so lines above Scanner's 64 KB cap stream
// fine. Returns the accumulated stats and the first read or upsert error.
func streamOFF(r io.Reader, batchSize int, upsert func(batch []catalog.EanRow) (ins, upd int, err error)) (seedStats, error) {
	var (
		stats seedStats
		batch []catalog.EanRow
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		ins, upd, err := upsert(batch)
		if err != nil {
			return err
		}
		stats.Inserted += ins
		stats.Updated += upd
		batch = batch[:0]
		log.Printf("openfoodfacts: progress — %d parsed, %d inserted, %d updated", stats.Parsed, stats.Inserted, stats.Updated)
		return nil
	}

	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			row, ok, perr := catalog.ParseOFFLine(line)
			switch {
			case perr != nil:
				stats.Malformed++
			case !ok:
				stats.Skipped++
			default:
				stats.Parsed++
				batch = append(batch, row)
				if len(batch) >= batchSize {
					if ferr := flush(); ferr != nil {
						return stats, ferr
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return stats, err
		}
	}
	if ferr := flush(); ferr != nil {
		return stats, ferr
	}
	return stats, nil
}

func seedOpenFoodFacts(args []string) error {
	fs := flag.NewFlagSet("openfoodfacts", flag.ExitOnError)
	file := fs.String("file", "../data/food/swedish_food_products.jsonl", "path to open food facts jsonl")
	batchSize := fs.Int("batch", 500, "rows per upsert batch")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	f, err := os.Open(*file)
	if err != nil {
		return fmt.Errorf("open %s: %w", *file, err)
	}
	defer f.Close()

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	stats, err := streamOFF(f, *batchSize, func(batch []catalog.EanRow) (int, int, error) {
		return catalog.UpsertEAN(ctx, pool, batch)
	})
	if err != nil {
		return fmt.Errorf("read %s: %w", *file, err)
	}

	log.Printf("openfoodfacts: done — %d parsed, %d skipped, %d malformed, %d inserted, %d updated",
		stats.Parsed, stats.Skipped, stats.Malformed, stats.Inserted, stats.Updated)
	return nil
}
