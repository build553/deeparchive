// Command worker runs the DeepArchive background job processor. It shares
// domain logic and database access with the API process but is deployed and
// scaled independently (see infra/compose). Jobs implemented so far are
// listed in docs/implementation-status.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/internal/backups"
	"deeparchive/internal/config"
	"deeparchive/internal/observability"
	"deeparchive/internal/quotas"
	"deeparchive/internal/retention"
	"deeparchive/internal/storage"
	"deeparchive/internal/usage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	logger := observability.NewLogger(cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DB.URL)
	if err != nil {
		return fmt.Errorf("creating database pool: %w", err)
	}
	defer pool.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := pool.Ping(pingCtx); err != nil {
		cancel()
		return fmt.Errorf("database not reachable at startup: %w", err)
	}
	cancel()

	storageClient, err := storage.NewClient(cfg.S3)
	if err != nil {
		return fmt.Errorf("creating object storage client: %w", err)
	}
	usageService := usage.NewService(pool)
	quotasService := quotas.NewService(pool, usageService)
	backupsService := backups.NewService(pool, storageClient, quotasService, logger)
	retentionService := retention.NewService(pool, backupsService, logger)

	logger.Info("worker_starting", "retention_sweep_interval", cfg.Retention.SweepInterval)

	runSweep := func() {
		deleted, err := retentionService.Sweep(ctx)
		if err != nil {
			logger.Error("retention_sweep_failed", "error", err)
			return
		}
		logger.Info("retention_sweep_completed", "snapshots_deleted", deleted)
	}
	// Run once at startup rather than waiting for the first tick — a
	// freshly deployed worker shouldn't wait a full interval before doing
	// its one job.
	runSweep()

	ticker := time.NewTicker(cfg.Retention.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("worker_stopped")
			return nil
		case <-ticker.C:
			runSweep()
		}
	}
}
