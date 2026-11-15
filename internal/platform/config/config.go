package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port              string
	DatabaseURL       string
	RedisURL          string
	WeatherProvider   string
	WeatherH3Res      int
	WeatherDailyLimit int
	RunMigrations     bool
	OverpassURL       string
	IngestExtractDir  string
	IngestSource      string
	OsmiumPBF         string
	OsmiumCmd         string

	AppEnv string

	AppStoreBundleID     string
	AppStoreAllowSandbox bool

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

	DBStatementTimeout string
	DBLockTimeout      string
	DBMaxConns         int32
	DBJIT              string
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
	cfg := Config{
		Port:              getEnv("PORT", "8080"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		RedisURL:          os.Getenv("REDIS_URL"),
		WeatherProvider:   getEnv("WEATHER_PROVIDER", "open-meteo"),
		WeatherH3Res:      getEnvInt("WEATHER_H3_RES", 6),
		WeatherDailyLimit: getEnvInt("WEATHER_DAILY_LIMIT", 9000),
		RunMigrations:     getEnv("RUN_MIGRATIONS", "true") != "false",
		OverpassURL:       getEnv("OVERPASS_URL", "https://overpass-api.de/api/interpreter"),
		IngestExtractDir:  getEnv("INGEST_EXTRACT_DIR", "./data/extracts"),
		IngestSource:      getEnv("INGEST_SOURCE", "overpass"),
		OsmiumPBF:         os.Getenv("OSMIUM_PBF"),
		OsmiumCmd:         os.Getenv("OSMIUM_CMD"),

		AppEnv:               os.Getenv("APP_ENV"),
		AppStoreBundleID:     os.Getenv("APPSTORE_BUNDLE_ID"),
		AppStoreAllowSandbox: getEnv("APPSTORE_ALLOW_SANDBOX", "true") != "false",
		AppleAppIDs:          splitList(os.Getenv("APPLE_APP_IDS")),
		AppStoreAppID:        strings.TrimSpace(os.Getenv("APPSTORE_APP_ID")),

		TrustedProxies: splitList(os.Getenv("TRUSTED_PROXIES")),
		MaxBodyBytes:   int64(getEnvInt("BODY_MAX_BYTES", 64<<10)),

		RateReadPerMin:     getEnvInt("RATE_READ_PER_MIN", 600),
		RateReadBurst:      getEnvInt("RATE_READ_BURST", 120),
		RateWritePerMin:    getEnvInt("RATE_WRITE_PER_MIN", 60),
		RateWriteBurst:     getEnvInt("RATE_WRITE_BURST", 30),
		RateDevicePerHour:  getEnvInt("RATE_DEVICE_PER_HOUR", 30),
		RateDeviceBurst:    getEnvInt("RATE_DEVICE_BURST", 10),
		RateAuthFailPerMin: getEnvInt("RATE_AUTH_FAIL_PER_MIN", 10),

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

		DBStatementTimeout: getEnv("DB_STATEMENT_TIMEOUT", "5s"),
		DBLockTimeout:      getEnv("DB_LOCK_TIMEOUT", "2s"),
		DBMaxConns:         int32(getEnvInt("DB_MAX_CONNS", 0)),
		DBJIT:              getEnv("DB_JIT", "off"),
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
