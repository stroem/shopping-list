// Command seed imports food reference data into Postgres. It reads DATABASE_URL.
//
//	go run ./cmd/seed livsmedelsverket [--file <path>]
//
// Open Food Facts (--> ean_mappings) is added by a later subcommand.
package main

import (
	"context"
	"flag"
	"fmt"
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
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: seed livsmedelsverket [--file <path>]")
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
