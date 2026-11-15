package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                   string
	MCPPort                string
	DatabaseURL            string
	RedisURL               string
	WeatherProvider        string
	WeatherH3Res           int
	WeatherDailyLimit      int
	WeatherOpenMeteoURL    string
	WeatherOpenMeteoModels string
	RunMigrations          bool
	OverpassURL            string
	IngestExtractDir       string
	IngestSource           string
	OsmiumPBF              string
	OsmiumCmd              string

	AppEnv string

	AppStoreBundleID     string
	AppStoreAllowSandbox bool
	// AppStorePlusProductIDs is the APPSTORE_PLUS_PRODUCT_IDS allowlist —
	// comma-separated product IDs that may grant Plus.
	AppStorePlusProductIDs []string

	AppleAppIDs   []string
	AppStoreAppID string

	TrustedProxies []string
	MaxBodyBytes   int64

	RateReadPerMin     int
	RateReadBurst      int
	RateWritePerMin    int
	RateWriteBurst     int
	RateDevicePerHour  int
	RateDeviceBurst    int
	RateAuthFailPerMin int
	// Extra per-IP limit on share-token routes (/p/, shared-plans,
	// plan-follows) on top of Read/Write: slows token guessing.
	RateTokenPerMin int
	RateTokenBurst  int

	// Plan evaluation thresholds (plans.Config).
	PlanBadBelow       int
	PlanWorseDrop      int
	PlanBadCaps        []string
	PlanNotifyHorizonH int
	PlanQuietStart     int
	PlanQuietEnd       int
	PlanConfirmFrom    int
	PlanAltRadiusM     int

	MFASecretKeys       string
	MFASecretKeysCLI    string
	AdminAllowedOrigins []string
	MFAChallengeTTL     time.Duration
	MFASessionTTL       time.Duration
	MFASessionIdleTTL   time.Duration
	MFAMaxFailures      int
	MFALockoutTTL       time.Duration

	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURI  string

	DBStatementTimeout       string
	DBWorkerStatementTimeout string
	DBLockTimeout            string
	DBMaxConns               int32
	DBJIT                    string
}

func Load() (Config, error) {
	_ = godotenv.Load()
	mfaChallengeTTL, err := getEnvDuration("MFA_CHALLENGE_TTL", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	mfaSessionTTL, err := getEnvDuration("MFA_SESSION_TTL", 12*time.Hour)
	if err != nil {
		return Config{}, err
	}
	mfaSessionIdleTTL, err := getEnvDuration("MFA_SESSION_IDLE_TTL", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	mfaLockoutTTL, err := getEnvDuration("MFA_LOCKOUT_TTL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	// Sandbox IAP receipts are only accepted outside production by default —
	// a free sandbox "purchase" must never grant production Plus. Enable
	// explicitly via APPSTORE_ALLOW_SANDBOX=true for the App Review window.
	appEnv := os.Getenv("APP_ENV")
	sandboxDefault := "true"
	if appEnv == "production" {
		sandboxDefault = "false"
	}
	cfg := Config{
		Port:              getEnv("PORT", "8080"),
		MCPPort:           getEnv("MCP_PORT", "8081"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		RedisURL:          os.Getenv("REDIS_URL"),
		WeatherProvider:   getEnv("WEATHER_PROVIDER", "open-meteo"),
		WeatherH3Res:      getEnvInt("WEATHER_H3_RES", 6),
		WeatherDailyLimit: getEnvInt("WEATHER_DAILY_LIMIT", 9000),
		// OPEN_METEO_URL points at a self-hosted instance for the Actions
		// weather-refresh job; OPEN_METEO_MODELS selects the model set
		// (e.g. "gfs_global" — required there since best_match needs the
		// full domain set). Empty models keeps public-API behavior.
		WeatherOpenMeteoURL:    getEnv("OPEN_METEO_URL", ""),
		WeatherOpenMeteoModels: getEnv("OPEN_METEO_MODELS", ""),
		RunMigrations:          getEnv("RUN_MIGRATIONS", "true") != "false",
		OverpassURL:            getEnv("OVERPASS_URL", "https://overpass-api.de/api/interpreter"),
		IngestExtractDir:       getEnv("INGEST_EXTRACT_DIR", "./data/extracts"),
		IngestSource:           getEnv("INGEST_SOURCE", "overpass"),
		OsmiumPBF:              os.Getenv("OSMIUM_PBF"),
		OsmiumCmd:              os.Getenv("OSMIUM_CMD"),

		AppEnv:                 appEnv,
		AppStoreBundleID:       os.Getenv("APPSTORE_BUNDLE_ID"),
		AppStoreAllowSandbox:   getEnv("APPSTORE_ALLOW_SANDBOX", sandboxDefault) != "false",
		AppStorePlusProductIDs: splitList(os.Getenv("APPSTORE_PLUS_PRODUCT_IDS")),
		AppleAppIDs:            splitList(os.Getenv("APPLE_APP_IDS")),
		AppStoreAppID:          strings.TrimSpace(os.Getenv("APPSTORE_APP_ID")),

		TrustedProxies: splitList(os.Getenv("TRUSTED_PROXIES")),
		MaxBodyBytes:   int64(getEnvInt("BODY_MAX_BYTES", 64<<10)),

		RateReadPerMin:     getEnvInt("RATE_READ_PER_MIN", 600),
		RateReadBurst:      getEnvInt("RATE_READ_BURST", 120),
		RateWritePerMin:    getEnvInt("RATE_WRITE_PER_MIN", 60),
		RateWriteBurst:     getEnvInt("RATE_WRITE_BURST", 30),
		RateDevicePerHour:  getEnvInt("RATE_DEVICE_PER_HOUR", 30),
		RateDeviceBurst:    getEnvInt("RATE_DEVICE_BURST", 10),
		RateAuthFailPerMin: getEnvInt("RATE_AUTH_FAIL_PER_MIN", 10),
		RateTokenPerMin:    getEnvInt("RATE_TOKEN_PER_MIN", 30),
		RateTokenBurst:     getEnvInt("RATE_TOKEN_BURST", 10),

		PlanBadBelow:       getEnvInt("PLAN_BAD_BELOW", 60),
		PlanWorseDrop:      getEnvInt("PLAN_WORSE_DROP", 15),
		PlanBadCaps:        splitList(getEnv("PLAN_BAD_CAPS", "rain,heat,cold")),
		PlanNotifyHorizonH: getEnvInt("PLAN_NOTIFY_HORIZON_HOURS", 72),
		PlanQuietStart:     getEnvInt("PLAN_QUIET_START_HOUR", 22),
		PlanQuietEnd:       getEnvInt("PLAN_QUIET_END_HOUR", 7),
		PlanConfirmFrom:    getEnvInt("PLAN_CONFIRM_FROM_HOUR", 18),
		PlanAltRadiusM:     getEnvInt("PLAN_ALT_RADIUS_M", 5000),

		MFASecretKeys:       os.Getenv("MFA_SECRET_KEYS"),
		MFASecretKeysCLI:    os.Getenv("MFA_SECRET_KEYS_CLI"),
		AdminAllowedOrigins: splitList(os.Getenv("ADMIN_ALLOWED_ORIGINS")),
		MFAChallengeTTL:     mfaChallengeTTL,
		MFASessionTTL:       mfaSessionTTL,
		MFASessionIdleTTL:   mfaSessionIdleTTL,
		MFAMaxFailures:      getEnvInt("MFA_MAX_FAILURES", 5),
		MFALockoutTTL:       mfaLockoutTTL,

		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURI:  os.Getenv("GOOGLE_REDIRECT_URI"),

		DBStatementTimeout:       getEnv("DB_STATEMENT_TIMEOUT", "5s"),
		DBWorkerStatementTimeout: getEnv("DB_WORKER_STATEMENT_TIMEOUT", "120s"),
		DBLockTimeout:            getEnv("DB_LOCK_TIMEOUT", "2s"),
		DBMaxConns:               int32(getEnvInt("DB_MAX_CONNS", 0)),
		DBJIT:                    getEnv("DB_JIT", "off"),
	}
	if cfg.MaxBodyBytes <= 0 {
		return Config{}, fmt.Errorf("BODY_MAX_BYTES must be > 0")
	}
	if cfg.WeatherH3Res < 0 || cfg.WeatherH3Res > 15 {
		return Config{}, fmt.Errorf("WEATHER_H3_RES must be 0-15, got %d", cfg.WeatherH3Res)
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.PlanQuietStart < 0 || cfg.PlanQuietStart > 23 || cfg.PlanQuietEnd < 0 || cfg.PlanQuietEnd > 23 ||
		cfg.PlanConfirmFrom < 0 || cfg.PlanConfirmFrom > 23 {
		return Config{}, fmt.Errorf("PLAN_*_HOUR values must be 0-23")
	}
	if cfg.PlanNotifyHorizonH < 1 || cfg.PlanBadBelow < 0 || cfg.PlanBadBelow > 100 ||
		cfg.PlanWorseDrop < 1 || cfg.PlanAltRadiusM < 1 {
		return Config{}, fmt.Errorf("invalid PLAN_* threshold")
	}
	if cfg.MFAMaxFailures < 1 {
		return Config{}, fmt.Errorf("MFA_MAX_FAILURES must be >= 1, got %d", cfg.MFAMaxFailures)
	}
	return cfg, nil
}

func (c Config) IsProduction() bool { return c.AppEnv == "production" }

func (c Config) DBRuntimeParams() map[string]string {
	params := map[string]string{}
	if c.DBStatementTimeout != "" {
		params["statement_timeout"] = c.DBStatementTimeout
	}
	if c.DBLockTimeout != "" {
		params["lock_timeout"] = c.DBLockTimeout
	}
	if c.DBJIT != "" {
		params["jit"] = c.DBJIT
	}
	return params
}

func (c Config) ConfigureGinMode(set func(mode string)) {
	switch {
	case os.Getenv("GIN_MODE") != "":
		set(os.Getenv("GIN_MODE"))
	case c.IsProduction():
		set("release")
	default:
		set("debug")
	}
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		slog.Warn("invalid integer env value, using fallback", "key", key, "value", v, "fallback", fallback)
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration (e.g. 12h), got %q", key, v)
	}
	return d, nil
}
