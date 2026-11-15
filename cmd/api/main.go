package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	infrahttp "github.com/thanhhoa35142/be-example/internal/infrastructure/http"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
	"github.com/thanhhoa35142/be-example/internal/modules/devices"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
	"github.com/thanhhoa35142/be-example/internal/modules/events"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"github.com/thanhhoa35142/be-example/internal/modules/goauth"
	"github.com/thanhhoa35142/be-example/internal/modules/notifier"
	"github.com/thanhhoa35142/be-example/internal/modules/plans"
	"github.com/thanhhoa35142/be-example/internal/modules/recommendations"
	"github.com/thanhhoa35142/be-example/internal/modules/reports"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
	"github.com/thanhhoa35142/be-example/internal/modules/traffic"
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

	pool, err := persistence.NewPool(ctx, cfg.DatabaseURL, cfg.DBRuntimeParams(), cfg.DBMaxConns)
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

	adminSvc := admin.NewService(persistence.NewAdminRepo(queries, pool))
	keysSvc := apikeys.NewService(persistence.NewAPIKeysRepo(queries, pool))
	eventsSvc := events.NewService(persistence.NewEventsRepo(queries))
	digestSvc := digest.NewService(persistence.NewDigestRepo(queries), entitlementSvc, nil, logger)
	plansSvc := plans.NewService(persistence.NewPlansRepo(queries, pool), entitlementSvc, plans.ConfigFrom(cfg))

	cfg.ConfigureGinMode(gin.SetMode)

	limits := infrahttp.RateLimits{
		Read:   ratelimit.PerMinute(float64(cfg.RateReadPerMin), cfg.RateReadBurst),
		Write:  ratelimit.PerMinute(float64(cfg.RateWritePerMin), cfg.RateWriteBurst),
		Device: ratelimit.PerMinute(float64(cfg.RateDevicePerHour)/60, cfg.RateDeviceBurst),
	}
	authFail := ratelimit.PerMinute(float64(cfg.RateAuthFailPerMin), cfg.RateAuthFailPerMin)
	tokenLimit := ratelimit.PerMinute(float64(cfg.RateTokenPerMin), cfg.RateTokenBurst)

	var rdb *redis.Client
	redisHealthy := false
	limiterFor := func(r ratelimit.Rule, _ string) ratelimit.Checker { return ratelimit.New(r) }
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			log.Fatalf("redis url: %v", err)
		}
		rdb = redis.NewClient(opts)
		pctx, pcancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := rdb.Ping(pctx).Err(); err != nil {

			log.Printf("redis unreachable at boot (%v) — using in-memory rate limits, cache bypassed until it recovers", err)
		} else {
			redisHealthy = true
			log.Printf("redis enabled (%s): shared rate limits + response cache", opts.Addr)
		}
		pcancel()
		limiterFor = func(r ratelimit.Rule, ns string) ratelimit.Checker {
			return ratelimit.NewRedis(rdb, r, ns)
		}
	}

	var adminAuthHandler *infrahttp.AdminAuthHandler
	if cfg.MFASecretKeys != "" {
		key, err := hex.DecodeString(cfg.MFASecretKeys)
		if err != nil || len(key) != 32 {
			log.Fatalf("MFA_SECRET_KEYS must be 64 hex chars (32 bytes)")
		}
		// Login challenges live in Redis (TTL-expiring, single-use). If Redis
		// was unreachable at boot, fall back to process memory so logins keep
		// working — challenges are then not shared across instances.
		var challengeStore adminauth.ChallengeStore = adminauth.NewMemoryChallengeStore()
		if rdb != nil && redisHealthy {
			challengeStore = persistence.NewAdminAuthChallengeStore(rdb)
		} else {
			logger.Warn("admin auth challenges are in-memory (REDIS_URL unset or Redis down at boot); lost on restart, not shared across instances")
		}
		authSvc, err := adminauth.NewService(persistence.NewAdminAuthRepo(queries), challengeStore, key, adminauth.Config{
			ChallengeTTL: cfg.MFAChallengeTTL,
			SessionTTL:   cfg.MFASessionTTL,
			IdleTTL:      cfg.MFASessionIdleTTL,
			MaxFailures:  cfg.MFAMaxFailures,
			Lockout:      cfg.MFALockoutTTL,
		})
		if err != nil {
			log.Fatalf("admin auth: %v", err)
		}
		adminAuthHandler = infrahttp.NewAdminAuthHandler(authSvc, limiterFor(authFail, "authfail"))
	} else {
		logger.Warn("MFA_SECRET_KEYS unset: admin account login disabled, MFA_SECRET_KEYS_CLI bearer only")
	}

	var goAuthSvc *goauth.Service
	var goAuthHandler *infrahttp.GoAuthHandler
	if cfg.GoogleClientID != "" || cfg.GoogleClientSecret != "" || cfg.GoogleRedirectURI != "" {
		if cfg.MFASecretKeys == "" {
			logger.Warn("GOOGLE_* set but MFA_SECRET_KEYS unset: Google account link disabled")
		} else {
			goAuthCfg := goauth.Config{
				ClientID:     cfg.GoogleClientID,
				ClientSecret: cfg.GoogleClientSecret,
				RedirectURL:  cfg.GoogleRedirectURI,
			}
			key, err := hex.DecodeString(cfg.MFASecretKeys)
			if err != nil || len(key) != 32 {
				log.Fatalf("MFA_SECRET_KEYS must be 64 hex chars (32 bytes)")
			}
			svc, err := goauth.NewService(persistence.NewGoAuthRepo(queries), key, goAuthCfg)
			if err != nil {
				log.Fatalf("goauth: %v", err)
			}
			if !goAuthCfg.Ready() {
				logger.Warn("GOOGLE_CLIENT_ID/SECRET/REDIRECT_URI incomplete: Google account link disabled")
			} else {
				goAuthSvc = svc
				goAuthHandler = infrahttp.NewGoAuthHandler(svc)
			}
		}
	}

	// Traffic reads work even without Google config — they just return empty
	// until the worker has synced something.
	trafficSvc := traffic.NewService(goAuthSvc, persistence.NewTrafficRepo(queries, pool), logger)

	router, err := infrahttp.NewRouter(pool, infrahttp.Handlers{
		Spots:           infrahttp.NewSpotsHandler(spotsSvc),
		Recommendations: infrahttp.NewRecommendationsHandler(recSvc),
		Devices:         infrahttp.NewDevicesHandler(devicesSvc),
		DeviceSvc:       devicesSvc,
		Reports:         infrahttp.NewReportsHandler(reportsSvc),
		Alerts:          infrahttp.NewAlertsHandler(alertsSvc),
		Entitlement:     infrahttp.NewEntitlementHandler(entitlementSvc),
		Admin:           infrahttp.NewAdminHandler(adminSvc, keysSvc, rdb),
		AdminAuth:       adminAuthHandler,
		GoAuth:          goAuthHandler,
		Traffic:         infrahttp.NewTrafficHandler(trafficSvc),
		Events:          infrahttp.NewEventsHandler(eventsSvc),
		Digest:          infrahttp.NewDigestHandler(digestSvc),
		Plans:           infrahttp.NewPlansHandler(plansSvc),
		Share: infrahttp.NewShareHandler(spotsSvc, infrahttp.ShareOptions{
			AppleAppIDs:   cfg.AppleAppIDs,
			AppStoreAppID: cfg.AppStoreAppID,
			Plans:         plansSvc,
		}),
	}, infrahttp.Options{
		TrustedProxies:      cfg.TrustedProxies,
		MaxBodyBytes:        cfg.MaxBodyBytes,
		Limits:              &limits,
		AdminToken:          cfg.MFASecretKeysCLI,
		AdminAllowedOrigins: cfg.AdminAllowedOrigins,
		AdminFailed:         &authFail,
		LimiterFor:          limiterFor,
		Cache:               rdb,
		TokenLimit:          &tokenLimit,
	})
	if err != nil {
		log.Fatalf("router: %v", err)
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
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
	productIDs := make(map[string]bool, len(cfg.AppStorePlusProductIDs))
	for _, id := range cfg.AppStorePlusProductIDs {
		productIDs[id] = true
	}
	if len(productIDs) == 0 {
		logger.Warn("APPSTORE_PLUS_PRODUCT_IDS is empty: every product in the bundle can grant Plus — set it before go-live")
	}
	return entitlement.WithVerifier(v, entitlement.Config{
		BundleID:     cfg.AppStoreBundleID,
		AllowSandbox: cfg.AppStoreAllowSandbox,
		ProductIDs:   productIDs,
	})
}
