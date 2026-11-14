// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port              string
	DatabaseURL       string
	RedisURL          string // REDIS_URL: shared rate-limit buckets; empty = in-memory
	WeatherProvider   string // "open-meteo" (default) | "weatherkit"
	WeatherH3Res      int    // H3 resolution for weather cells (default 6)
	WeatherDailyLimit int    // max provider calls/day (default 9000)
	RunMigrations     bool   // auto-run DB migrations on boot (default true)
	OverpassURL       string // Overpass interpreter endpoint (ingest dev path)
	IngestExtractDir  string // local dir for Geofabrik .pbf snapshots
	IngestSource      string // "overpass" (default, dev) | "osmium" (prod path)
	OsmiumPBF         string // path to the .osm.pbf extract for osmium source
	OsmiumCmd         string // optional command prefix (e.g. docker wrapper), space-separated

	AppEnv string // APP_ENV: "production" switches Gin to release mode

	// App Store verification (entitlement). Empty bundle id = every signed
	// payload is refused (fail closed).
	AppStoreBundleID     string // APPSTORE_BUNDLE_ID
	AppStoreAllowSandbox bool   // APPSTORE_ALLOW_SANDBOX (default true)
	// Share page / universal links (GET /s/{spotId}).
	AppleAppIDs   []string // APPLE_APP_IDS: comma-separated "<TeamID>.<bundle id>"; empty = no association file
	AppStoreAppID string   // APPSTORE_APP_ID: numeric App Store id for the "Get the App" link

	// HTTP hardening.
	TrustedProxies []string // TRUSTED_PROXIES: comma-separated IPs/CIDRs allowed to set X-Forwarded-For (default none)
	MaxBodyBytes   int64    // BODY_MAX_BYTES: default request body cap (default 64 KiB)

	RateReadPerMin     int // RATE_READ_PER_MIN: per-IP GET budget (default 600)
	RateReadBurst      int // RATE_READ_BURST (default 120)
	RateWritePerMin    int // RATE_WRITE_PER_MIN: per-IP POST/PUT/PATCH/DELETE (default 60)
	RateWriteBurst     int // RATE_WRITE_BURST (default 30)
	RateDevicePerHour  int // RATE_DEVICE_PER_HOUR: per-IP POST /v1/devices (default 30)
	RateDeviceBurst    int // RATE_DEVICE_BURST (default 10)
	RateAuthFailPerMin int // RATE_AUTH_FAIL_PER_MIN: failed admin / MCP auth attempts per IP (default 10)
}

func Load() (Config, error) {
	_ = godotenv.Load()
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
	return cfg, nil
}

// IsProduction reports APP_ENV=production.
func (c Config) IsProduction() bool { return c.AppEnv == "production" }

// ConfigureGinMode sets Gin's mode; call it BEFORE gin.New() — Gin reads the
// mode when the engine is built. An explicit GIN_MODE always wins; otherwise
// APP_ENV=production selects release mode and everything else debug.
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
