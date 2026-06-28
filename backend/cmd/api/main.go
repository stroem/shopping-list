// Command api runs the HTTP backend locally. It is the local-dev baseline and
// shares its router with the Lambda entrypoint.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/stroem/shopping-list/backend/internal/router"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	log.Printf("api listening on %s", addr)
	if err := http.ListenAndServe(addr, router.New()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
