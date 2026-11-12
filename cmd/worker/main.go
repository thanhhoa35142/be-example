package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/crowd"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest/osm"
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

// jobRunIDKey carries the current t_job_runs.id to job functions that need
// it (ingest stamps it on t_ingest_features.run_id for traceability).
type jobRunIDKey struct{}

func jobRunID(ctx context.Context) int64 {
	id, _ := ctx.Value(jobRunIDKey{}).(int64)
	return id
}

const jobSchedulePollInterval = 30 * time.Second

func main() {
	jobName := flag.String("job", "", "run a single job once and exit")
	packSlug := flag.String("pack", "", "ingest.osm: restrict to one city pack slug")
	dryRun := flag.Bool("dry-run", false, "ingest.osm: run the full tx then roll back")
	flag.Parse()

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

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db config: %v", err)
	}
	// Each job holds one conn for its advisory lock; size the pool so all
	// registered jobs can run concurrently.
	poolCfg.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

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

	// Ingest: Overpass dev path by default; INGEST_SOURCE=osmium switches to
	// the production .pbf pipeline behind the same osm.FeatureSource port.
	ingestRepo := persistence.NewIngestRepo(pool)
	var featureSource osm.FeatureSource = &osm.OverpassSource{
		URL:    cfg.OverpassURL,
		Client: httpx.NewClient(httpx.Policy{Name: "overpass", RatePerSec: 1, Burst: 2, Timeout: 300 * time.Second}),
	}
	if cfg.IngestSource == "osmium" {
		if cfg.OsmiumPBF == "" {
			log.Fatalf("INGEST_SOURCE=osmium requires OSMIUM_PBF")
		}
		var cmd []string
		if cfg.OsmiumCmd != "" {
			cmd = strings.Fields(cfg.OsmiumCmd)
		}
		featureSource = &osm.OsmiumSource{
			PBFPath: cfg.OsmiumPBF,
			WorkDir: cfg.IngestExtractDir,
			Cmd:     cmd,
		}
	}
	ingestSvc := &ingest.Service{
		Src:            featureSource,
		Store:          ingestRepo,
		Clf:            osm.Classifier{Policy: osm.DefaultQualityPolicy},
		Pol:            osm.DefaultPolicy,
		AmenityRadiusM: 300,
	}

	runIngest := func(ctx context.Context) error {
		packs, err := ingestRepo.ListIngestPacks(ctx)
		if err != nil {
			return err
		}
		for i := range packs {
			if *packSlug != "" && packs[i].Slug != *packSlug {
				continue
			}
			// Freshness gate: skip packs whose extract hasn't been
			// re-fetched since the last import (scheduled runs only —
			// an explicit -pack one-shot always runs).
			if *packSlug == "" && packs[i].ExtractFetchedAt != nil &&
				packs[i].LastImportAt != nil &&
				!packs[i].ExtractFetchedAt.After(*packs[i].LastImportAt) {
				continue
			}
			bounds, err := ingestRepo.PackBounds(ctx, packs[i].ID)
			if err != nil {
				return fmt.Errorf("pack %s bounds: %w", packs[i].Slug, err)
			}
			packs[i].Bounds = bounds
			st, err := ingestSvc.RunPack(ctx, packs[i], jobRunID(ctx), *dryRun)
			if err != nil {
				return fmt.Errorf("pack %s: %w", packs[i].Slug, err)
			}
			logger.Info("ingest pack done", "pack", packs[i].Slug, "dry_run", *dryRun,
				"features", st.FeaturesRead, "created", st.Created, "updated", st.Updated,
				"activated", st.Activated, "pending", st.Pending, "closed", st.Closed,
				"absorbed", st.Absorbed, "amenities", st.AmenitiesLinked)
		}
		return nil
	}

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
			name:         "ingest.osm",
			initialDelay: 5 * time.Minute,
			fn:           runIngest,
		},
		{
			name:         "ingest.extract.refresh",
			initialDelay: 11 * time.Minute,
			fn: func(ctx context.Context) error {
				extracts, err := ingestRepo.DistinctExtracts(ctx)
				if err != nil {
					return err
				}
				fetched, err := ingest.RefreshExtracts(ctx, nil, cfg.IngestExtractDir, extracts, 7*24*time.Hour, false)
				if err != nil {
					return err
				}
				for _, e := range fetched {
					if err := ingestRepo.MarkExtractFetched(ctx, e, time.Now().UTC()); err != nil {
						return err
					}
				}
				logger.Info("extract refresh done", "extracts", len(extracts), "fetched", len(fetched))
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

	// One-shot mode: `worker -job ingest.osm -pack amsterdam -dry-run`.
	if *jobName != "" {
		for _, j := range registry {
			if j.name == *jobName {
				executeJob(ctx, pool, logger, adminSvc, j)
				return
			}
		}
		log.Fatalf("unknown job %q", *jobName)
	}

	for _, j := range registry {
		go runJob(ctx, pool, logger, adminSvc, j)
	}

	// Wake-up channel: a committed ingest notifies 'pickhour_ingest' and we
	// kick scoring.recompute immediately instead of waiting for its tick.
	go listenIngestDone(ctx, pool, logger, adminSvc, registry)

	<-ctx.Done()
	pool.Close()
	logger.Info("worker stopped")
}

func runJob(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, adminSvc *admin.Service, j job) {
	run := func() {
		executeJob(ctx, pool, logger, adminSvc, j)
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

// executeJob runs one job under its advisory lock with t_job_runs
// instrumentation. Used by both the periodic loop and -job one-shot mode.
func executeJob(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, adminSvc *admin.Service, j job) {
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
	if runID != 0 {
		ctx = context.WithValue(ctx, jobRunIDKey{}, runID)
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

// listenIngestDone LISTENs on 'pickhour_ingest' and runs scoring.recompute
// shortly after each committed ingest, debounced so a multi-pack run
// triggers at most one extra scoring pass.
func listenIngestDone(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, adminSvc *admin.Service, registry []job) {
	var scoring *job
	for i := range registry {
		if registry[i].name == "scoring.recompute" {
			scoring = &registry[i]
			break
		}
	}
	if scoring == nil {
		return
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		logger.Warn("ingest listener: acquire conn", "error", err)
		return
	}
	defer conn.Release()
	pgxConn := conn.Conn()
	if _, err := pgxConn.Exec(ctx, "LISTEN pickhour_ingest"); err != nil {
		logger.Warn("ingest listener: LISTEN failed", "error", err)
		return
	}
	logger.Info("ingest listener ready")

	// WaitForNotification blocks, so it runs in its own goroutine feeding a
	// channel; the select below can then debounce on a timer.
	notifyCh := make(chan struct{}, 64)
	go func() {
		defer close(notifyCh)
		for {
			if _, err := pgxConn.WaitForNotification(ctx); err != nil {
				return
			}
			select {
			case notifyCh <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	}()

	const debounce = 15 * time.Second
	pending := false
	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-notifyCh:
			if !ok {
				return // listener conn died
			}
			pending = true
			if timer == nil {
				timer = time.NewTimer(debounce)
			} else {
				timer.Reset(debounce)
			}
			timerC = timer.C
		case <-timerC:
			timerC = nil
			if pending {
				pending = false
				logger.Info("ingest notify received, running scoring.recompute")
				executeJob(ctx, pool, logger, adminSvc, *scoring)
			}
		}
	}
}
