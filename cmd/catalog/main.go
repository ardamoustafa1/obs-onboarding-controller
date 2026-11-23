// Command catalog runs the service-catalog REST API standalone (see
// internal/catalog). The operator talks to a deployed instance of this
// exact binary over HTTP; it is a real component of this repo, not a
// stand-in for one.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/acme/obs-onboarding-controller/internal/catalog"
)

func main() {
	addr := ":8080"
	if v := os.Getenv("CATALOG_ADDR"); v != "" {
		addr = v
	}

	server := catalog.NewServer(catalog.NewMemStore())
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("catalog listening on %s", addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("catalog server: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("catalog shutdown: %v", err)
	}
}
