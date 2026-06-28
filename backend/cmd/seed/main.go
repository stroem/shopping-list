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

	rows, err := catalog.ParseLivsmedelsverket(f)
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

	// bufio.Reader (not Scanner): some OFF lines exceed Scanner's 64 KB cap.
	r := bufio.NewReaderSize(f, 1<<20)
	var (
		parsed, skipped, malformed, inserted, updated int
		batch                                         []catalog.EanRow
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		ins, upd, err := catalog.UpsertEAN(ctx, pool, batch)
		if err != nil {
			return err
		}
		inserted += ins
		updated += upd
		batch = batch[:0]
		log.Printf("openfoodfacts: progress — %d parsed, %d inserted, %d updated", parsed, inserted, updated)
		return nil
	}

	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			row, ok, perr := catalog.ParseOFFLine(line)
			switch {
			case perr != nil:
				malformed++
			case !ok:
				skipped++
			default:
				parsed++
				batch = append(batch, row)
				if len(batch) >= *batchSize {
					if ferr := flush(); ferr != nil {
						return ferr
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", *file, err)
		}
	}
	if ferr := flush(); ferr != nil {
		return ferr
	}

	log.Printf("openfoodfacts: done — %d parsed, %d skipped, %d malformed, %d inserted, %d updated",
		parsed, skipped, malformed, inserted, updated)
	return nil
}
