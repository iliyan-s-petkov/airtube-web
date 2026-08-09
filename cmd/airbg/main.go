package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"airbg.org/internal/area"
	"airbg.org/internal/backfill"
	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: airbg <migrate|collect|backfill|import-areas|purge-outside-boundary>")
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

	case "backfill":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "usage: airbg backfill <sensor_id> <archive-csv-path>")
			os.Exit(2)
		}
		sensorID, err := strconv.ParseInt(os.Args[2], 10, 64)
		if err != nil {
			slog.Error("backfill", "error", err)
			os.Exit(1)
		}
		f, err := os.Open(os.Args[3])
		if err != nil {
			slog.Error("backfill", "error", err)
			os.Exit(1)
		}
		buckets, report, err := backfill.ParseCSV(f, sensorID)
		f.Close()
		if err != nil {
			slog.Error("backfill", "error", err)
			os.Exit(1)
		}

		// Report what filtering dropped before anything is written. An archive
		// file that is mostly rejected must be visible at the moment of
		// import — once the surviving buckets are in reading_hourly there is
		// no column recording how much of the day they were derived from, and
		// nothing ever rewrites a historical bucket.
		slog.Log(ctx, report.Level(), "backfill parsed archive file",
			append([]any{"sensor_id", sensorID, "path", os.Args[3]}, report.LogAttrs()...)...)

		n, err := backfill.WriteBuckets(ctx, pool, buckets)
		if err != nil {
			slog.Error("backfill", "error", err)
			os.Exit(1)
		}
		slog.Info("backfill complete", "sensor_id", sensorID, "buckets", n)

	case "import-areas":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "usage: airbg import-areas <path.geojson> <city|oblast|neighbourhood|country>")
			os.Exit(2)
		}
		n, err := area.Import(ctx, pool, os.Args[2], os.Args[3])
		if err != nil {
			slog.Error("import areas", "error", err)
			os.Exit(1)
		}
		assigned, err := area.AssignSensors(ctx, pool)
		if err != nil {
			slog.Error("assign sensors", "error", err)
			os.Exit(1)
		}
		slog.Info("areas imported", "areas", n, "assignments", assigned)

	case "purge-outside-boundary":
		// Deliberately a separate, operator-invoked step (task-17 review
		// finding 4) — never run automatically from import-areas or collect.
		// Deleting stored sensors must always be a decision a human makes on
		// purpose.
		removed, err := area.PurgeOutsideBoundary(ctx, pool)
		if err != nil {
			slog.Error("purge outside boundary", "error", err)
			os.Exit(1)
		}
		slog.Info("purge outside boundary complete", "sensors_removed", removed)

	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}
