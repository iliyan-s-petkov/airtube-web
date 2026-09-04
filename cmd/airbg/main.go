package main

import (
	"context"
	"errors"
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
	"airbg.org/internal/web"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: airbg <migrate|collect|serve|backfill|import-areas|purge-outside-boundary|validate-config>")
		os.Exit(2)
	}

	// validate-config is checked before any database or listener setup: it
	// exists so an operator can catch a bad airbg.yaml before deploying
	// rather than at server start.
	if os.Args[1] == "validate-config" {
		os.Exit(runValidateConfig(os.Stdout, os.Stderr))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		// Fail closed and print the whole list: a config error is an operator
		// error, and one problem per restart is a bad trade for them.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// serve is the only subcommand that runs two workloads in one process, so it
	// is the only one that needs the bulkhead — and it must not also hold a
	// third, unused pool. Handled before the shared pool is opened.
	if os.Args[1] == "serve" {
		if err := serveCommand(ctx, cfg); err != nil {
			slog.Error("serve", "error", err)
			os.Exit(1)
		}
		return
	}

	pool, err := db.Open(ctx, cfg.Database)
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
		client := upstream.New(cfg.Upstream)
		ing := ingest.New(client, store.New(pool, cfg.Store, cfg.Database.StatementTimeouts.Series), quality.NewHistory(cfg.Quality.HistoryDepth), quality.NewScorer(cfg.Quality), cfg.Database.StatementTimeouts.Assign, cfg.Upstream.Countries)
		ing.Loop(ctx, cfg.Upstream.PollInterval)

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
		buckets, report, err := backfill.ParseCSV(f, sensorID, cfg.Quality)
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
		slog.Log(ctx, report.Level(cfg.Backfill), "backfill parsed archive file",
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
		assigned, revoked, err := area.AssignSensors(ctx, pool, cfg.Database.StatementTimeouts.Assign)
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
		result, err := area.PurgeOutsideBoundary(ctx, pool, cfg.Database.StatementTimeouts.Operator)
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

// serveCommand owns the two pools' lifetimes. Separate from runServe so the
// deferred Close calls actually run: main's error paths call os.Exit, which
// skips defers.
func serveCommand(ctx context.Context, cfg config.Config) error {
	apiPool, collectorPool, err := db.OpenPair(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer apiPool.Close()
	defer collectorPool.Close()

	return runServe(ctx, cfg, apiPool, collectorPool)
}

func runServe(ctx context.Context, cfg config.Config, apiPool, collectorPool *pgxpool.Pool) error {
	// Fail closed on the exact regression this function exists to prevent. A
	// future refactor that collapses the two pools back into one would otherwise
	// reintroduce the starvation silently — nothing about it is visible in a
	// response, a metric, or a log line until the poll cycle happens to overlap
	// with traffic.
	if apiPool == collectorPool {
		return errors.New("serve: the request and collector pools are the same pool; " +
			"the collector holds connections for up to " + cfg.Database.StatementTimeouts.Assign.String() +
			" per cycle and would starve request handlers (see db.OpenPair)")
	}

	log := slog.Default()

	// Request handlers get the API pool. The collector and the snapshot
	// publisher get the collector pool: building a snapshot is background work
	// that queries every area, so it belongs on the side of the bulkhead that is
	// allowed to be slow.
	apiStore := store.New(apiPool, cfg.Store, cfg.Database.StatementTimeouts.Series)
	collectorStore := store.New(collectorPool, cfg.Store, cfg.Database.StatementTimeouts.Series)

	holder := snapshot.NewHolder(cfg.Series)
	pub := server.NewPublisher(collectorStore, holder, log)

	cat, err := i18n.LoadWithOverrides(cfg.I18n.Dir)
	if err != nil {
		return err
	}

	// Build once at startup so the first visitor is not met with a 503 for a
	// whole poll interval. A failure here is logged, not fatal: the process
	// still serves "data is not ready yet" and the next cycle fixes it.
	if err := pub.Publish(ctx, time.Now().UTC()); err != nil {
		log.Error("initial snapshot build failed; starting with no data", "error", err)
	}

	// One line, so a developer who ran `go run ./cmd/airbg` without building the
	// frontend discovers it in one second rather than wondering why the map is
	// missing. The no-manifest path is a supported mode, not an error — hence
	// Info, not Warn.
	if assets, found := web.LoadAssets(); found {
		log.Info("assets", "state", "loaded", "script", assets.Script("main"))
	} else {
		log.Info("assets", "state", "no manifest — serving without islands (run 'npm run build' in web/)")
	}

	// Built here, in main, rather than inside server.New: this is the one place
	// the configured basemap host reaches the CSP, and it keeps the server
	// package from needing to know how a policy is assembled.
	srv, err := server.New(server.Options{
		Config:    cfg,
		Catalogue: cat,
		Snapshots: holder,
		Store:     apiStore,
		Publisher: pub,
		Logger:    log,
	})
	if err != nil {
		return err
	}

	// Same construction as the existing "collect" case — one poller, not two.
	ing := ingest.New(
		upstream.New(cfg.Upstream),
		collectorStore,
		quality.NewHistory(cfg.Quality.HistoryDepth),
		quality.NewScorer(cfg.Quality),
		cfg.Database.StatementTimeouts.Assign,
		cfg.Upstream.Countries,
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
		ing.Loop(pollCtx, cfg.Upstream.PollInterval) // returns when pollCtx is cancelled
	}()

	err = srv.Run(ctx)

	// Stop the poller and wait for it, so the process does not exit with a
	// half-written cycle in flight.
	stopPolling()
	<-polled
	return err
}
