package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: airbg <migrate|collect|backfill|import-areas>")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(1)
	}

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	switch os.Args[1] {
	case "migrate":
		if err := db.Migrate(ctx, pool); err != nil {
			slog.Error("migrate", "error", err)
			os.Exit(1)
		}
		slog.Info("migrations applied")

	case "collect":
		client := upstream.New(cfg.UpstreamURL, 30*time.Second)
		ing := ingest.New(client, store.New(pool), quality.NewHistory(12))
		ing.Loop(ctx, cfg.PollInterval)

	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}
