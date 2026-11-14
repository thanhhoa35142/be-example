package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	infrahttp "github.com/thanhhoa35142/be-example/internal/infrastructure/http"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
	"github.com/thanhhoa35142/be-example/internal/modules/devices"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
	"github.com/thanhhoa35142/be-example/internal/modules/events"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"github.com/thanhhoa35142/be-example/internal/modules/notifier"
	"github.com/thanhhoa35142/be-example/internal/modules/recommendations"
	"github.com/thanhhoa35142/be-example/internal/modules/reports"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
	"github.com/thanhhoa35142/be-example/internal/platform/config"
	platformlog "github.com/thanhhoa35142/be-example/internal/platform/log"
	"github.com/thanhhoa35142/be-example/internal/platform/ratelimit"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	geo.WeatherResolution = cfg.WeatherH3Res

	ctx := context.Background()

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

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("db ping: %v", err)
	}

	queries := persistence.New(pool)
	spotsRepo := persistence.NewSpotsRepository(queries)
	spotsSvc := spots.NewService(spotsRepo)
	recSvc := recommendations.NewService(persistence.NewRecommendationsRepo(queries))
	devicesSvc := devices.NewService(persistence.NewDevicesRepo(queries, pool))
	reportsSvc := reports.NewService(persistence.NewReportsRepo(queries))
	logger := platformlog.New("api")
	entitlementSvc := entitlement.NewService(persistence.NewEntitlementRepo(queries, pool), appStoreVerifier(cfg, logger))
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

	adminSvc := admin.NewService(persistence.NewAdminRepo(queries))
	keysSvc := apikeys.NewService(persistence.NewAPIKeysRepo(queries, pool))
	eventsSvc := events.NewService(persistence.NewEventsRepo(queries))
	digestSvc := digest.NewService(persistence.NewDigestRepo(queries), entitlementSvc, nil, logger)

	// Gin reads its mode when the engine is built: set it first.
	cfg.ConfigureGinMode(gin.SetMode)

	limits := infrahttp.RateLimits{
		Read:   ratelimit.PerMinute(float64(cfg.RateReadPerMin), cfg.RateReadBurst),
		Write:  ratelimit.PerMinute(float64(cfg.RateWritePerMin), cfg.RateWriteBurst),
		Device: ratelimit.PerMinute(float64(cfg.RateDevicePerHour)/60, cfg.RateDeviceBurst),
	}
	authFail := ratelimit.PerMinute(float64(cfg.RateAuthFailPerMin), cfg.RateAuthFailPerMin)

	// Shared rate-limit buckets + GET response cache when REDIS_URL is set;
	// nil = per-process in-memory limiters, no cache. Both fail open.
	var rdb *redis.Client
	var limiterFor func(ratelimit.Rule, string) ratelimit.Checker
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			log.Fatalf("redis url: %v", err)
		}
		rdb = redis.NewClient(opts)
		pctx, pcancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := rdb.Ping(pctx).Err(); err != nil {
			log.Printf("redis unreachable (%v) — rate limits fail open, cache disabled", err)
			rdb = nil
		} else {
			log.Printf("redis enabled (%s): shared rate limits + response cache", opts.Addr)
		}
		pcancel()
		if rdb != nil {
			limiterFor = func(r ratelimit.Rule, ns string) ratelimit.Checker {
				return ratelimit.NewRedis(rdb, r, ns)
			}
		}
	}

	router, err := infrahttp.NewRouter(pool, infrahttp.Handlers{
		Spots:           infrahttp.NewSpotsHandler(spotsSvc),
		Recommendations: infrahttp.NewRecommendationsHandler(recSvc),
		Devices:         infrahttp.NewDevicesHandler(devicesSvc),
		Reports:         infrahttp.NewReportsHandler(reportsSvc),
		Alerts:          infrahttp.NewAlertsHandler(alertsSvc),
		Entitlement:     infrahttp.NewEntitlementHandler(entitlementSvc),
		Admin:           infrahttp.NewAdminHandler(adminSvc, keysSvc),
		Events:          infrahttp.NewEventsHandler(eventsSvc),
		Digest:          infrahttp.NewDigestHandler(digestSvc),
		Share: infrahttp.NewShareHandler(spotsSvc, infrahttp.ShareOptions{
			AppleAppIDs:   cfg.AppleAppIDs,
			AppStoreAppID: cfg.AppStoreAppID,
		}),
	}, infrahttp.Options{
		TrustedProxies: cfg.TrustedProxies,
		MaxBodyBytes:   cfg.MaxBodyBytes,
		Limits:         &limits,
		AdminToken:     os.Getenv("ADMIN_TOKEN"),
		AdminFailed:    &authFail,
		LimiterFor:     limiterFor,
		Cache:          rdb,
	})
	if err != nil {
		log.Fatalf("router: %v", err)
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second, // whole request incl. body (largest body is 512 KiB)
		WriteTimeout:      60 * time.Second, // covers the slowest admin aggregate query
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	go func() {
		log.Printf("server listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintln(os.Stderr, "shutdown:", err)
	}
	log.Println("server stopped")
}

// appStoreVerifier wires the App Store JWS verifier. Without
// APPSTORE_BUNDLE_ID the entitlement service stays fail-closed: POST
// /v1/entitlement/link and /v1/appstore/notifications answer 503 and no
// subscription can be created. There is no bypass.
func appStoreVerifier(cfg config.Config, logger *slog.Logger) entitlement.Option {
	if cfg.AppStoreBundleID == "" {
		logger.Error("APPSTORE_BUNDLE_ID is not set: subscription linking and App Store notifications are DISABLED (fail closed)")
		return entitlement.WithVerifier(nil, entitlement.Config{})
	}
	v, err := entitlement.NewProductionVerifier()
	if err != nil {
		log.Fatalf("app store verifier: %v", err)
	}
	if cfg.AppStoreAllowSandbox {
		logger.Warn("App Store Sandbox payloads are accepted (APPSTORE_ALLOW_SANDBOX=true); set it to false once the app is live")
	}
	return entitlement.WithVerifier(v, entitlement.Config{
		BundleID:     cfg.AppStoreBundleID,
		AllowSandbox: cfg.AppStoreAllowSandbox,
	})
}
