// Command migrate applies (up) or reverts (down) database migrations. It reads
// DATABASE_URL and is the local/CI migration entrypoint. A richer CLI
// (version/force/steps) is tracked as a follow-up issue.
//
//	go run ./cmd/migrate up
//	go run ./cmd/migrate down
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/stroem/shopping-list/backend/internal/config"
	"github.com/stroem/shopping-list/backend/internal/db"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down") {
		fmt.Fprintln(os.Stderr, "usage: migrate <up|down>")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	switch os.Args[1] {
	case "up":
		if err := db.Migrate(context.Background(), cfg.DatabaseURL); err != nil {
			log.Fatalf("migrate up: %v", err)
		}
		log.Println("migrations applied")
	case "down":
		if err := db.MigrateDown(cfg.DatabaseURL); err != nil {
			log.Fatalf("migrate down: %v", err)
		}
		log.Println("migrations reverted")
	}
}
