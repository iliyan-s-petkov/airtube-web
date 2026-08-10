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
	"airbg.org/internal/i18n"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/server"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
	"airbg.org/internal/upstream"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: airbg <migrate|collect|serve|backfill|import-areas|purge-outside-boundary>")
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

	case "serve":
		if err := runServe(ctx, cfg, pool); err != nil {
			slog.Error("serve", "error", err)
			os.Exit(1)
		}

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
		// Refuse before reading the file: a backfill for an unknown or
		// out-of-boundary sensor_id creates reading_hourly rows that the
		// documented cleanup command cannot reach by sensor.
		if err := backfill.CheckSensorInBoundary(ctx, pool, sensorID); err != nil {
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
		assigned, revoked, err := area.AssignSensors(ctx, pool)
		if err != nil {
			slog.Error("assign sensors", "error", err)
			os.Exit(1)
		}
		// revoked is reported because re-importing a *smaller* boundary
		// legitimately withdraws memberships, and an operator who did not
		// intend to shrink anything should see a non-zero count here rather
		// than discover it from a Phase 2 map with sensors missing.
		slog.Info("areas imported", "areas", n, "assignments", assigned, "revoked", revoked)

	case "purge-outside-boundary":
		// Deliberately a separate, operator-invoked step (task-17 review
		// finding 4) — never run automatically from import-areas or collect.
		// Deleting stored sensors must always be a decision a human makes on
		// purpose.
		result, err := area.PurgeOutsideBoundary(ctx, pool)
		if err != nil {
			slog.Error("purge outside boundary", "error", err)
			os.Exit(1)
		}
		slog.Info("purge outside boundary complete",
			"sensors_removed", result.SensorsRemoved,
			"readings_removed", result.ReadingsRemoved,
			"hourly_rows_removed", result.HourlyRowsRemoved,
			// Orphans have a different cause from foreign sensors — readings
			// written for a sensor_id that has no sensor row — so they are
			// reported separately rather than folded into the totals above.
			"orphan_readings_removed", result.OrphanRawRows,
			"orphan_hourly_rows_removed", result.OrphanHourlyRows)

	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runServe(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	log := slog.Default()
	st := store.New(pool)
	holder := snapshot.NewHolder()
	pub := server.NewPublisher(st, holder, log)

	cat, err := i18n.Load()
	if err != nil {
		return err
	}

	// Build once at startup so the first visitor is not met with a 503 for a
	// whole poll interval. A failure here is logged, not fatal: the process
	// still serves "data is not ready yet" and the next cycle fixes it.
	if err := pub.Publish(ctx, time.Now().UTC()); err != nil {
		log.Error("initial snapshot build failed; starting with no data", "error", err)
	}

	srv, err := server.New(server.Options{
		ListenAddr:        cfg.ListenAddr,
		MetricsAddr:       cfg.MetricsAddr,
		Catalogue:         cat,
		Snapshots:         holder,
		Store:             st,
		Publisher:         pub,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
		BaseURL:           cfg.BaseURL,
		Logger:            log,
	})
	if err != nil {
		return err
	}

	// Same construction as the existing "collect" case — one poller, not two.
	ing := ingest.New(
		upstream.New(cfg.UpstreamURL, 30*time.Second),
		st,
		quality.NewHistory(12),
	)
	ing.SetSnapshotPublisher(pub)

	// The poller and the server share one process because the snapshot lives in
	// this process's memory: a separately deployed collector could fill the
	// database but could never swap the pointer this server reads.
	pollCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling()

	polled := make(chan struct{})
	go func() {
		defer close(polled)
		ing.Loop(pollCtx, cfg.PollInterval) // returns when pollCtx is cancelled
	}()

	err = srv.Run(ctx)

	// Stop the poller and wait for it, so the process does not exit with a
	// half-written cycle in flight.
	stopPolling()
	<-polled
	return err
}
