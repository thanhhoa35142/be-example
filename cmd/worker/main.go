package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/crowd"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"github.com/thanhhoa35142/be-example/internal/modules/notifier"
	"github.com/thanhhoa35142/be-example/internal/modules/scoring"
	"github.com/thanhhoa35142/be-example/internal/modules/weather"
	"github.com/thanhhoa35142/be-example/internal/platform/config"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
	"github.com/thanhhoa35142/be-example/internal/platform/jobs"
	platformlog "github.com/thanhhoa35142/be-example/internal/platform/log"
)

// job is a periodic background task. Workers compute ahead; the API only reads.
type job struct {
	name string
	fn   func(ctx context.Context) error
	// initialDelay staggers the first run after boot so jobs that share a
	// provider don't all fire at t=0. Subsequent ticks keep the offset
	// because intervals are fixed.
	initialDelay time.Duration
}

const jobSchedulePollInterval = 30 * time.Second

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	logger := platformlog.New("worker")
	geo.WeatherResolution = cfg.WeatherH3Res

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.RunMigrations {
		if err := persistence.RunMigrations(cfg.DatabaseURL); err != nil {
			log.Fatalf("migrations: %v", err)
		}
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()
	// Each job holds one conn for its advisory lock; size the pool so all
	// registered jobs can run concurrently.
	pool.Config().MaxConns = 12

	queries := persistence.New(pool)

	// Register scheduled jobs here as modules are implemented.
	scoringRepo := persistence.NewScoringRepository(queries)
	scoringSvc := scoring.NewService(scoringRepo)

	var weatherProvider weather.Provider
	switch cfg.WeatherProvider {
	case "weatherkit":
		weatherProvider = &weather.WeatherKit{}
	default:
		weatherProvider = weather.NewOpenMeteo(httpx.NewClient(httpx.Policy{
			Name: "open-meteo", RatePerSec: 5, Burst: 10,
		}))
	}
	weatherSvc := weather.NewService(persistence.NewWeatherRepo(queries), weatherProvider, cfg.WeatherDailyLimit)
	crowdSvc := crowd.NewService(persistence.NewCrowdRepo(queries))
	adminSvc := admin.NewService(persistence.NewAdminRepo(queries))
	entitlementSvc := entitlement.NewService(persistence.NewEntitlementRepo(queries))
	alertsRepo := persistence.NewAlertsRepo(queries)
	apnsClient, err := notifier.ClientFromEnv()
	if err != nil {
		logger.Warn("APNs disabled", "error", err)
	}
	alertsSvc := alerts.NewService(
		alertsRepo,
		entitlementSvc,
		notifier.NewSender(logger, alertsRepo, apnsClient),
	)

	registry := []job{
		{
			name:         "weather.refresh",
			initialDelay: 0, // provider-bound: goes first
			fn: func(ctx context.Context) error {
				stats, err := weatherSvc.Refresh(ctx)
				if err != nil {
					return err
				}
				logger.Info("weather done", "provider", weatherProvider.Name(), "cells", stats.Cells, "rows", stats.Fetched, "failed", stats.Failed)
				return nil
			},
		},
		{
			name:         "crowd.refresh",
			initialDelay: 7 * time.Minute, // daily, heaviest — offset far out
			fn: func(ctx context.Context) error {
				stats, err := crowdSvc.Refresh(ctx)
				if err != nil {
					return err
				}
				logger.Info("crowd done", "spots", stats.Spots, "written", stats.Written, "skipped", stats.Skipped)
				return nil
			},
		},
		{
			name:         "crowd.aggregate",
			initialDelay: 2 * time.Minute,
			fn: func(ctx context.Context) error {
				stats, err := crowdSvc.AggregateCommunity(ctx)
				if err != nil {
					return err
				}
				logger.Info("crowd aggregate done", "written", stats.Written)
				return nil
			},
		},
		{
			name:         "scoring.recompute",
			initialDelay: 4 * time.Minute, // after weather+crowd have fresh data
			fn: func(ctx context.Context) error {
				stats, err := scoringSvc.Recompute(ctx)
				if err != nil {
					return err
				}
				logger.Info("scoring done", "spots", stats.Spots, "hours", stats.Hours, "written", stats.Written, "skipped", stats.Skipped)
				return nil
			},
		},
		{
			name:         "alerts.evaluate",
			initialDelay: time.Minute,
			fn: func(ctx context.Context) error {
				stats, err := alertsSvc.Evaluate(ctx, time.Now())
				if err != nil {
					return err
				}
				logger.Info("alerts done", "rules", stats.Rules, "evaluated", stats.Evaluated,
					"fired", stats.Fired, "sent", stats.Sent, "failed", stats.Failed)
				return nil
			},
		},
		{
			name:         "retention.purge",
			initialDelay: 10 * time.Minute, // daily, lowest priority — runs last
			fn: func(ctx context.Context) error {
				stats, err := persistence.NewRetentionRepo(queries).Purge(ctx)
				if err != nil {
					return err
				}
				logger.Info("retention done",
					"weather", stats.WeatherForecasts, "scores", stats.SpotScores,
					"events", stats.Events, "crowd_reports", stats.CrowdReports,
					"job_runs", stats.JobRuns, "deliveries", stats.AlertDeliveries,
					"payloads_scrubbed", stats.PayloadsScrubbed)
				return nil
			},
		},
	}

	if len(registry) == 0 {
		logger.Info("worker started, no jobs registered")
	}

	for _, j := range registry {
		go runJob(ctx, pool, logger, adminSvc, j)
	}

	<-ctx.Done()
	pool.Close()
	logger.Info("worker stopped")
}

func runJob(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, adminSvc *admin.Service, j job) {
	run := func() {
		// Per-run advisory lock: only one replica executes the job at a time.
		release, err := jobs.Acquire(ctx, pool, j.name)
		if errors.Is(err, jobs.ErrLockNotAcquired) {
			return // another replica owns this tick
		}
		if err != nil {
			logger.Error("job lock failed", "job", j.name, "error", err)
			return
		}
		defer release()

		// Winning the lock means any 'running' rows for this job are orphans
		// from a dead worker — abandon them before recording this run.
		if err := adminSvc.AbandonJobRuns(ctx, j.name); err != nil {
			logger.Warn("job orphan sweep failed", "job", j.name, "error", err)
		}

		runID, err := adminSvc.RecordJobStart(ctx, j.name)
		if err != nil {
			logger.Error("job_run insert failed", "job", j.name, "error", err)
		}
		jobErr := j.fn(ctx)
		switch {
		case jobErr == nil:
			logger.Info("job ok", "job", j.name)
			if runID != 0 {
				adminSvc.RecordJobFinish(ctx, runID, "succeeded", nil, nil)
			}
		default:
			logger.Error("job failed", "job", j.name, "error", jobErr)
			if runID != 0 {
				adminSvc.RecordJobFinish(ctx, runID, "failed", nil, jobErr)
			}
		}
	}

	// First schedule load decides the boot offset and enabled flag; offset
	// comes from t_worker_job_schedules (falls back to the static
	// initialDelay when the row is missing).
	interval := time.Duration(0)
	enabled := true
	offset := j.initialDelay
	if schedule, err := adminSvc.JobSchedule(ctx, j.name); err == nil {
		interval = time.Duration(schedule.IntervalSeconds) * time.Second
		enabled = schedule.Enabled
		offset = time.Duration(schedule.OffsetSeconds) * time.Second
	} else {
		logger.Warn("job schedule missing, using defaults", "job", j.name, "error", err)
	}

	if offset > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(offset):
		}
	}
	if enabled {
		run() // run once at startup, then on every tick
	}

	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	pollTicker := time.NewTicker(jobSchedulePollInterval)
	defer pollTicker.Stop()

	refreshSchedule := func(resetTimer bool) {
		schedule, err := adminSvc.JobSchedule(ctx, j.name)
		if err != nil {
			logger.Error("job schedule load failed", "job", j.name, "error", err)
			return
		}
		enabled = schedule.Enabled
		nextInterval := time.Duration(schedule.IntervalSeconds) * time.Second
		if nextInterval == interval {
			return
		}
		interval = nextInterval
		logger.Info("job schedule updated", "job", j.name, "interval", interval)
		if timer == nil {
			timer = time.NewTimer(interval)
			timerC = timer.C
		} else if resetTimer {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(interval)
			timerC = timer.C
		}
	}
	refreshSchedule(false)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timerC:
			timer.Reset(interval)
			timerC = timer.C
			if enabled {
				run()
			}
			refreshSchedule(true)
		case <-pollTicker.C:
			refreshSchedule(true)
		}
	}
}
