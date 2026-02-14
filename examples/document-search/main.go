package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/matiasinsaurralde/go-pageindex/examples/document-search/internal/app"
)

func main() {
	srv, err := app.NewFromEnv()
	if err != nil {
		log.Fatalf("create server: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
