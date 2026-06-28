// Command api runs the HTTP backend locally. It is the local-dev baseline and
// shares its router with the Lambda entrypoint.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/stroem/shopping-list/backend/internal/router"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	srv := &http.Server{
		Addr:              addr,
		Handler:           router.New(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("api listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
