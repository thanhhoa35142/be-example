package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/crowd"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"github.com/thanhhoa35142/be-example/internal/modules/goauth"
	"github.com/thanhhoa35142/be-example/internal/modules/holidays"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest/osm"
	"github.com/thanhhoa35142/be-example/internal/modules/notifier"
	"github.com/thanhhoa35142/be-example/internal/modules/scoring"
	"github.com/thanhhoa35142/be-example/internal/modules/traffic"
	"github.com/thanhhoa35142/be-example/internal/modules/weather"
	"github.com/thanhhoa35142/be-example/internal/platform/cache"
	"github.com/thanhhoa35142/be-example/internal/platform/config"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
	"github.com/thanhhoa35142/be-example/internal/platform/jobs"
	platformlog "github.com/thanhhoa35142/be-example/internal/platform/log"
)

type job struct {
	name string
	fn   func(ctx context.Context) error

	initialDelay time.Duration
}

type jobRunIDKey struct{}

func jobRunID(ctx context.Context) int64 {
	id, _ := ctx.Value(jobRunIDKey{}).(int64)
	return id
}

type jobStatsKey struct{}

type jobStatsBag struct {
	m map[string]any
}

func (b *jobStatsBag) set(key string, v any) {
	if b.m == nil {
		b.m = map[string]any{}
	}
	b.m[key] = v
}

const jobSchedulePollInterval = 30 * time.Second

func main() {
	jobName := flag.String("job", "", "run a single job once and exit")
	packSlug := flag.String("pack", "", "ingest.osm: restrict to one city pack slug")
	dryRun := flag.Bool("dry-run", false, "ingest.osm: run the full tx then roll back")
	forceGuard := flag.Bool("force-guard", false, "ingest.osm: commit even if the blast-radius guard trips (use with -pack after review)")
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

	poolCfg.MaxConns = 12

	// Start from the shared runtime params (lock_timeout, jit=off, …) so the
	// worker doesn't drift from the API's settings.
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	for k, v := range cfg.DBRuntimeParams() {
		poolCfg.ConnConfig.RuntimeParams[k] = v
	}
	// Batch jobs run heavy statements (ingest upserts, score recompute) that
	// legitimately exceed the API's statement timeout, so the worker uses
	// its own, larger budget.
	if cfg.DBWorkerStatementTimeout != "" {
		poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = cfg.DBWorkerStatementTimeout
	}
	if cfg.DBMaxConns > 0 {
		poolCfg.MaxConns = cfg.DBMaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	var rdb *redis.Client
	if cfg.RedisURL != "" {
		if opts, err := redis.ParseURL(cfg.RedisURL); err == nil {
			rdb = redis.NewClient(opts)
			pctx, pcancel := context.WithTimeout(ctx, 3*time.Second)
			if err := rdb.Ping(pctx).Err(); err != nil {
				logger.Warn("redis unreachable — cache version bumps will fail until it recovers", "error", err)
			}
			pcancel()
		} else {
			logger.Warn("bad REDIS_URL — cache version bumps disabled", "error", err)
		}
	}

	type cacheDirty struct{ data, weather atomic.Bool }
	type cacheDirtyKey struct{}
	bumpCache := func(fn func(context.Context) error) func(context.Context) error {
		return func(ctx context.Context) error {
			dirty := &cacheDirty{}
			ctx = context.WithValue(ctx, cacheDirtyKey{}, dirty)
			if err := fn(ctx); err != nil {
				return err
			}
			if rdb != nil && (dirty.data.Load() || dirty.weather.Load()) {
				// Retry once — a transient Redis blip shouldn't leave the API
				// serving stale cached responses silently. If it still fails,
				// fail the job so the miss is visible in t_job_runs.
				var bumpErr error
				for attempt := 0; attempt < 2; attempt++ {
					bctx, cancel := context.WithTimeout(ctx, 2*time.Second)
					bumpErr = nil
					if dirty.data.Load() {
						if err := cache.Bump(bctx, rdb); err != nil {
							bumpErr = err
						}
					}
					if dirty.weather.Load() && bumpErr == nil {
						if err := cache.BumpWeather(bctx, rdb); err != nil {
							bumpErr = err
						}
					}
					cancel()
					if bumpErr == nil {
						break
					}
					if attempt == 0 {
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(500 * time.Millisecond):
						}
					}
				}
				if bumpErr != nil {
					return fmt.Errorf("cache version bump failed after retry: %w", bumpErr)
				}
			}
			return nil
		}
	}

	touchCache := func(ctx context.Context) {
		if d, ok := ctx.Value(cacheDirtyKey{}).(*cacheDirty); ok {
			d.data.Store(true)
		}
	}

	touchWeatherCache := func(ctx context.Context) {
		if d, ok := ctx.Value(cacheDirtyKey{}).(*cacheDirty); ok {
			d.weather.Store(true)
		}
	}

	queries := persistence.New(pool)

	scoringRepo := persistence.NewScoringRepository(queries)
	scoringSvc := scoring.NewService(scoringRepo)

	var weatherProvider weather.Provider
	switch cfg.WeatherProvider {
	case "weatherkit":
		weatherProvider = &weather.WeatherKit{}
	default:
		weatherProvider = weather.NewOpenMeteo(httpx.NewClient(httpx.Policy{
			Name: "open-meteo", RatePerSec: 5, Burst: 10,
		}), cfg.WeatherOpenMeteoURL, cfg.WeatherOpenMeteoModels)
	}
	weatherSvc := weather.NewService(persistence.NewWeatherRepo(queries, pool), weatherProvider, cfg.WeatherDailyLimit)
	crowdSvc := crowd.NewService(persistence.NewCrowdRepo(queries))
	holidaysSvc := holidays.NewService(persistence.NewHolidaysRepo(queries))
	adminSvc := admin.NewService(persistence.NewAdminRepo(queries, pool))

	// Google traffic sync needs the OAuth link config + the sealing key.
	// Without it the job still registers (so the schedule row resolves) but
	// Sync() is a no-op.
	var trafficSvc *traffic.Service
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" &&
		cfg.GoogleRedirectURI != "" && cfg.MFASecretKeys != "" {
		if key, err := hex.DecodeString(cfg.MFASecretKeys); err == nil && len(key) == 32 {
			if goAuthSvc, err := goauth.NewService(persistence.NewGoAuthRepo(queries), key, goauth.Config{
				ClientID:     cfg.GoogleClientID,
				ClientSecret: cfg.GoogleClientSecret,
				RedirectURL:  cfg.GoogleRedirectURI,
			}); err == nil {
				trafficSvc = traffic.NewService(goAuthSvc, persistence.NewTrafficRepo(queries, pool), logger)
			} else {
				logger.Warn("google sync disabled: goauth init failed", "error", err)
			}
		} else {
			logger.Warn("google sync disabled: MFA_SECRET_KEYS is not 64 hex chars")
		}
	} else {
		logger.Info("google sync disabled: GOOGLE_* or MFA_SECRET_KEYS not configured")
	}
	if trafficSvc == nil {
		trafficSvc = traffic.NewService(nil, persistence.NewTrafficRepo(queries, pool), logger)
	}
	entitlementSvc := entitlement.NewService(persistence.NewEntitlementRepo(queries, pool))
	alertsRepo := persistence.NewAlertsRepo(queries, pool)
	apnsClient, err := notifier.ClientFromEnv()
	if err != nil {
		logger.Warn("APNs disabled", "error", err)
	}
	alertsSvc := alerts.NewService(
		alertsRepo,
		entitlementSvc,
		notifier.NewSender(logger, alertsRepo, apnsClient),
	)

	digestSvc := digest.NewService(
		persistence.NewDigestRepo(queries),
		entitlementSvc,
		notifier.NewDigestSender(notifier.NewSender(logger, alertsRepo, apnsClient)),
		logger,
	)

	var osmiumSrc *osm.OsmiumSource

	ingestRepo := persistence.NewIngestRepo(pool)
	var featureSource osm.FeatureSource = &osm.OverpassSource{
		URL:    cfg.OverpassURL,
		Client: httpx.NewClient(httpx.Policy{Name: "overpass", RatePerSec: 1, Burst: 2, Timeout: 300 * time.Second}),
	}
	if cfg.IngestSource == "osmium" {

		var cmd []string
		if cfg.OsmiumCmd != "" {
			cmd = strings.Fields(cfg.OsmiumCmd)
		}
		osmiumSrc = &osm.OsmiumSource{
			PBFPath: cfg.OsmiumPBF,
			WorkDir: cfg.IngestExtractDir,
			Cmd:     cmd,
		}
		featureSource = osmiumSrc
	}

	osmiumMode := osmiumSrc != nil && cfg.OsmiumPBF == ""
	prepareExtract := func(ctx context.Context, e string) error {
		if err := osmiumSrc.Prepare(ctx, e); err != nil {
			return err
		}
		ingest.RemoveExtract(cfg.IngestExtractDir, e)
		return nil
	}
	ingestSvc := &ingest.Service{
		Src:            featureSource,
		Store:          ingestRepo,
		Clf:            osm.Classifier{Policy: osm.DefaultQualityPolicy},
		Pol:            osm.DefaultPolicy,
		AmenityRadiusM: 300,
		ForceGuard:     *forceGuard,
	}

	runIngest := func(ctx context.Context) error {
		packs, err := ingestRepo.ListIngestPacks(ctx)
		if err != nil {
			return err
		}

		var todo []int
		for i := range packs {
			if *packSlug != "" && packs[i].Slug != *packSlug {
				continue
			}

			if *packSlug == "" && packs[i].ExtractFetchedAt != nil &&
				packs[i].LastImportAt != nil &&
				!packs[i].ExtractFetchedAt.After(*packs[i].LastImportAt) {
				continue
			}
			todo = append(todo, i)
		}

		failedExtract := map[string]error{}
		if osmiumMode {
			seen := map[string]bool{}
			for _, i := range todo {
				e := packs[i].OSMExtract
				if e == "" || seen[e] {
					continue
				}
				seen[e] = true
				if osmiumSrc.ArtifactsReady(e) {
					// Extract already on disk (restored or placed manually)
					// but extract_fetched_at may be NULL — record it so the
					// pack doesn't re-ingest on every run forever.
					if err := ingestRepo.MarkExtractFetched(ctx, e, time.Now().UTC()); err != nil {
						failedExtract[e] = err
					}
					continue
				}
				downloaded, err := ingest.EnsureExtract(ctx, nil, cfg.IngestExtractDir, e)
				if err == nil && downloaded {
					err = ingestRepo.MarkExtractFetched(ctx, e, time.Now().UTC())
				}
				if err == nil {
					err = prepareExtract(ctx, e)
				}
				if err != nil {
					failedExtract[e] = err
				}
			}
		}

		var errs []error
		mutated := false
		for _, i := range todo {
			if err := ctx.Err(); err != nil {
				errs = append(errs, err)
				break
			}
			if err, bad := failedExtract[packs[i].OSMExtract]; bad {
				errs = append(errs, fmt.Errorf("pack %s: extract: %w", packs[i].Slug, err))
				continue
			}
			bounds, err := ingestRepo.PackBounds(ctx, packs[i].ID)
			if err != nil {
				errs = append(errs, fmt.Errorf("pack %s bounds: %w", packs[i].Slug, err))
				continue
			}
			packs[i].Bounds = bounds
			started := time.Now()
			st, err := ingestSvc.RunPack(ctx, packs[i], jobRunID(ctx), *dryRun)
			if err == nil && !*dryRun {
				mutated = mutated || st.Created+st.Updated+st.Closed+
					st.HiddenVanished+st.Absorbed+st.AmenitiesLinked > 0
			}
			if bag, ok := ctx.Value(jobStatsKey{}).(*jobStatsBag); ok {
				existing, _ := bag.m["packs"].([]any)
				entry := map[string]any{"pack": packs[i].Slug, "stats": st,
					"duration_ms": time.Since(started).Milliseconds()}
				if err != nil {
					entry["error"] = err.Error()
				}
				bag.set("packs", append(existing, entry))
			}
			if err != nil {
				logger.Error("ingest pack failed", "pack", packs[i].Slug, "error", err)
				errs = append(errs, fmt.Errorf("pack %s: %w", packs[i].Slug, err))
				continue
			}
			logger.Info("ingest pack done", "pack", packs[i].Slug, "dry_run", *dryRun,
				"duration", time.Since(started).Round(time.Millisecond),
				"features", st.FeaturesRead, "created", st.Created, "updated", st.Updated,
				"unchanged", st.Unchanged, "activated", st.Activated, "pending", st.Pending,
				"closed", st.Closed, "absorbed", st.Absorbed, "amenities", st.AmenitiesLinked,
				"stage_ms", st.StageMs)
		}
		if mutated {
			touchCache(ctx)
		}
		return errors.Join(errs...)
	}

	registry := []job{
		{
			name:         "weather.refresh",
			initialDelay: 0,
			fn: bumpCache(func(ctx context.Context) error {
				stats, err := weatherSvc.Refresh(ctx)
				if err != nil {
					return err
				}

				if stats.Replaced > 0 {
					touchWeatherCache(ctx)
				}
				logger.Info("weather done", "provider", weatherProvider.Name(), "cells", stats.Cells, "rows", stats.Fetched, "replaced", stats.Replaced, "failed", stats.Failed)
				return nil
			}),
		},
		{

			name:         "holidays.refresh",
			initialDelay: 6 * time.Minute,
			fn: bumpCache(func(ctx context.Context) error {
				stats, err := holidaysSvc.Refresh(ctx, time.Now())
				if bag, ok := ctx.Value(jobStatsKey{}).(*jobStatsBag); ok {
					bag.set("countries", stats.Countries)
					bag.set("years", stats.Years)
					bag.set("rows_upserted", stats.Rows)
					bag.set("uncovered_countries", stats.Uncovered)
					bag.set("per_country", stats.PerCountry)
				}

				if err != nil {
					return err
				}
				logger.Info("holidays done", "countries", stats.Countries, "rows", stats.Rows, "uncovered", stats.Uncovered)
				return nil
			}),
		},
		{
			name:         "crowd.refresh",
			initialDelay: 7 * time.Minute,
			fn: bumpCache(func(ctx context.Context) error {
				stats, err := crowdSvc.Refresh(ctx)
				if err != nil {
					return err
				}

				logger.Info("crowd done", "spots", stats.Spots, "slots", stats.Slots, "written", stats.Written, "skipped", stats.Skipped)
				return nil
			}),
		},
		{
			name:         "crowd.aggregate",
			initialDelay: 2 * time.Minute,
			fn: bumpCache(func(ctx context.Context) error {
				stats, err := crowdSvc.AggregateCommunity(ctx)
				if err != nil {
					return err
				}

				logger.Info("crowd aggregate done", "written", stats.Written, "deleted", stats.Deleted)
				return nil
			}),
		},
		{
			name:         "scoring.recompute",
			initialDelay: 4 * time.Minute,
			fn: bumpCache(func(ctx context.Context) error {
				stats, err := scoringSvc.Recompute(ctx)
				if err != nil {
					return err
				}
				if stats.Written > 0 {
					touchCache(ctx)
				}
				logger.Info("scoring done", "spots", stats.Spots, "hours", stats.Hours, "written", stats.Written, "unchanged", stats.Hours-stats.Written, "skipped", stats.Skipped)
				return nil
			}),
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

			name:         "digest.weekly",
			initialDelay: 5 * time.Minute,
			fn: func(ctx context.Context) error {
				stats, err := digestSvc.RunWeekly(ctx, time.Now())
				if bag, ok := ctx.Value(jobStatsKey{}).(*jobStatsBag); ok {
					bag.set("candidates", stats.Candidates)
					bag.set("due", stats.Due)
					bag.set("sent", stats.Sent)
					bag.set("no_window", stats.NoWindow)
					bag.set("skipped", stats.Skipped)
					bag.set("failed", stats.Failed)
				}
				if err != nil {
					return err
				}
				logger.Info("digest done", "candidates", stats.Candidates, "due", stats.Due,
					"sent", stats.Sent, "no_window", stats.NoWindow, "skipped", stats.Skipped, "failed", stats.Failed)
				return nil
			},
		},
		{
			name:         "ingest.osm",
			initialDelay: 5 * time.Minute,
			fn:           bumpCache(runIngest),
		},
		{
			name:         "ingest.extract.refresh",
			initialDelay: 11 * time.Minute,
			fn: func(ctx context.Context) error {
				extracts, err := ingestRepo.DistinctExtracts(ctx)
				if err != nil {
					return err
				}
				fetched, err := ingest.RefreshExtracts(ctx, nil, cfg.IngestExtractDir, extracts, 7*24*time.Hour, false,
					func(e string) error {

						if err := ingestRepo.MarkExtractFetched(ctx, e, time.Now().UTC()); err != nil {
							return err
						}
						if osmiumMode {
							return prepareExtract(ctx, e)
						}
						return nil
					})
				logger.Info("extract refresh done", "extracts", len(extracts), "fetched", len(fetched), "error", err)
				return err
			},
		},
		{
			name:         "google.sync",
			initialDelay: 7 * time.Minute,
			fn: func(ctx context.Context) error {
				stats, err := trafficSvc.Sync(ctx)
				if bag, ok := ctx.Value(jobStatsKey{}).(*jobStatsBag); ok && stats != nil {
					bag.set("accounts", stats.Accounts)
					bag.set("synced", stats.Synced)
					bag.set("skipped", stats.Skipped)
					bag.set("failed", stats.Failed)
					bag.set("gsc_rows", stats.GscRows)
					bag.set("ga4_rows", stats.Ga4Rows)
				}
				if err != nil {
					return err
				}
				logger.Info("google sync done",
					"accounts", stats.Accounts, "synced", stats.Synced,
					"skipped", stats.Skipped, "failed", stats.Failed,
					"gsc_rows", stats.GscRows, "ga4_rows", stats.Ga4Rows)
				return nil
			},
		},
		{
			name:         "retention.purge",
			initialDelay: 10 * time.Minute,

			fn: func(ctx context.Context) error {
				stats, err := persistence.NewRetentionRepo(queries).Purge(ctx)
				if err != nil {
					return err
				}
				logger.Info("retention done",
					"weather", stats.WeatherForecasts, "scores", stats.SpotScores,
					"events", stats.Events, "crowd_reports", stats.CrowdReports,
					"job_runs", stats.JobRuns, "deliveries", stats.AlertDeliveries,
					"payloads_scrubbed", stats.PayloadsScrubbed,
					"admin_sessions", stats.AdminSessions, "plans", stats.Plans)
				return nil
			},
		},
	}

	if len(registry) == 0 {
		logger.Info("worker started, no jobs registered")
	}

	if *jobName != "" {
		for _, j := range registry {
			if j.name == *jobName {
				_, jobErr := executeJob(ctx, pool, logger, adminSvc, j)
				if jobErr != nil {
					// One-shot mode (CI weather-refresh): a failed job must
					// fail the process so the workflow step goes red.
					log.Fatalf("job %s failed: %v", j.name, jobErr)
				}
				return
			}
		}
		log.Fatalf("unknown job %q", *jobName)
	}

	for _, j := range registry {
		go runJob(ctx, pool, logger, adminSvc, j)
	}

	go listenIngestDone(ctx, pool, logger, adminSvc, registry)

	<-ctx.Done()
	pool.Close()
	logger.Info("worker stopped")
}

func runJob(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, adminSvc *admin.Service, j job) {
	run := func() {
		_, _ = executeJob(ctx, pool, logger, adminSvc, j)
	}

	// Fallback interval when the schedule row is missing — without it the job
	// would run once at boot and never reschedule (timer stays nil forever).
	interval := time.Hour
	enabled := true
	offset := j.initialDelay
	if schedule, err := adminSvc.JobSchedule(ctx, j.name); err == nil {
		interval = time.Duration(schedule.IntervalSeconds) * time.Second
		enabled = schedule.Enabled
		offset = time.Duration(schedule.OffsetSeconds) * time.Second
	} else {
		logger.Warn("job schedule missing, using 1h fallback", "job", j.name, "error", err)
	}

	if offset > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(offset):
		}
	}
	if enabled {
		run()
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
			// Still arm the timer with the fallback interval — a missing row
			// must not silently disable scheduling.
			if timer == nil && interval > 0 {
				timer = time.NewTimer(interval)
				timerC = timer.C
			}
			return
		}
		enabled = schedule.Enabled
		nextInterval := time.Duration(schedule.IntervalSeconds) * time.Second
		if timer == nil {
			// First successful load must always arm the timer — returning early
			// on an unchanged interval would leave timerC nil forever and the
			// job would never run on schedule again.
			if nextInterval <= 0 {
				return
			}
			interval = nextInterval
			timer = time.NewTimer(interval)
			timerC = timer.C
			return
		}
		if nextInterval == interval {
			return
		}
		interval = nextInterval
		logger.Info("job schedule updated", "job", j.name, "interval", interval)
		if interval <= 0 {
			// A non-positive interval disables scheduling; disarm the timer
			// rather than Reset(0), which would busy-loop.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer = nil
			timerC = nil
			return
		}
		if resetTimer {
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

			if requested, err := adminSvc.ClaimJobRunRequest(ctx, j.name); err != nil {
				logger.Error("job run request check failed", "job", j.name, "error", err)
			} else if requested {
				logger.Info("job run requested from admin console", "job", j.name)
				run()
			}
		}
	}
}

// runJobFunc invokes a job function with panic recovery: a panic becomes a
// recorded job failure instead of crashing the whole worker process.
func runJobFunc(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("job panic: %v\n%s", r, debug.Stack())
		}
	}()
	return fn(ctx)
}

// executeJob runs one job under its advisory lock. It reports whether the
// lock was acquired and the job actually ran — callers that trigger runs
// indirectly (ingest notify) use this to re-arm instead of dropping work.
func executeJob(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, adminSvc *admin.Service, j job) (ran bool, jobErr error) {
	// Top-level recovery: panics in job bookkeeping (not just j.fn) must not
	// kill the worker process. Registered first so it runs after release().
	defer func() {
		if r := recover(); r != nil {
			logger.Error("job panicked", "job", j.name, "panic", r, "stack", string(debug.Stack()))
			jobErr = fmt.Errorf("job panic: %v", r)
		}
	}()

	release, err := jobs.Acquire(ctx, pool, j.name)
	if errors.Is(err, jobs.ErrLockNotAcquired) {
		return false, nil
	}
	if err != nil {
		logger.Error("job lock failed", "job", j.name, "error", err)
		return false, nil
	}
	defer release()

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
	statsBag := &jobStatsBag{}
	ctx = context.WithValue(ctx, jobStatsKey{}, statsBag)
	jobErr = runJobFunc(ctx, j.fn)
	var statsJSON []byte
	if len(statsBag.m) > 0 {
		statsJSON, _ = json.Marshal(statsBag.m)
	}
	switch jobErr {
	case nil:
		logger.Info("job ok", "job", j.name)
		if runID != 0 {
			adminSvc.RecordJobFinish(ctx, runID, "succeeded", nil, nil, statsJSON)
		}
	default:
		logger.Error("job failed", "job", j.name, "error", jobErr)
		if runID != 0 {
			adminSvc.RecordJobFinish(ctx, runID, "failed", nil, jobErr, statsJSON)
		}
	}
	return true, jobErr
}

// ingestDebounce is how long ingest notifications are coalesced before
// triggering scoring.recompute.
const ingestDebounce = 15 * time.Second

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

	pending := false
	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	// The LISTEN connection can drop on any Postgres restart or network blip.
	// Reconnect with backoff instead of dying silently — pg_notify is not
	// persistent, so notifications during the gap are lost, but the listener
	// must come back.
	backoff := time.Second
	for ctx.Err() == nil {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			logger.Warn("ingest listener: acquire conn", "error", err)
		} else {
			pgxConn := conn.Conn()
			if _, err := pgxConn.Exec(ctx, "LISTEN pickhour_ingest"); err != nil {
				logger.Warn("ingest listener: LISTEN failed", "error", err)
				conn.Release()
			} else {
				logger.Info("ingest listener ready")
				backoff = time.Second
				if listenSession(ctx, pool, pgxConn, logger, adminSvc, scoring, &pending, &timer, &timerC) {
					conn.Release()
					return // ctx done
				}
				// Connection dropped — loop reconnects after backoff.
				conn.Release()
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				backoff = min(backoff*2, 60*time.Second)
				continue
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 60*time.Second)
	}
}

// listenSession consumes notifications on an established LISTEN connection
// until it dies or ctx is canceled. It returns true when ctx ended. The
// pending/timer state is shared across reconnects so a notification received
// just before a disconnect still triggers a recompute after reconnecting.
func listenSession(
	ctx context.Context,
	pool *pgxpool.Pool,
	pgxConn *pgx.Conn,
	logger *slog.Logger,
	adminSvc *admin.Service,
	scoring *job,
	pending *bool,
	timer **time.Timer,
	timerC *<-chan time.Time,
) bool {
	notifyCh := make(chan struct{}, 64)
	done := make(chan struct{})
	go func() {
		defer close(notifyCh)
		defer close(done)
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

	for {
		select {
		case <-ctx.Done():
			<-done
			return true
		case _, ok := <-notifyCh:
			if !ok {
				<-done
				return false
			}
			*pending = true
			if *timer == nil {
				*timer = time.NewTimer(ingestDebounce)
			} else {
				(*timer).Reset(ingestDebounce)
			}
			*timerC = (*timer).C
		case <-*timerC:
			*timerC = nil
			if *pending {
				logger.Info("ingest notify received, running scoring.recompute")
				if ran, _ := executeJob(ctx, pool, logger, adminSvc, *scoring); ran {
					*pending = false
					continue
				}
				// Lock held (scheduled run or previous trigger still going):
				// keep pending and retry after the debounce window instead of
				// dropping the request and leaving scores stale.
				*timer = time.NewTimer(ingestDebounce)
				*timerC = (*timer).C
			}
		}
	}
}
