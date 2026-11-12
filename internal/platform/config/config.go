// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Port              string
	DatabaseURL       string
	WeatherProvider   string // "open-meteo" (default) | "weatherkit"
	WeatherH3Res      int    // H3 resolution for weather cells (default 6)
	WeatherDailyLimit int    // max provider calls/day (default 9000)
	RunMigrations     bool   // auto-run DB migrations on boot (default true)
	OverpassURL       string // Overpass interpreter endpoint (ingest dev path)
	IngestExtractDir  string // local dir for Geofabrik .pbf snapshots
}

func Load() (Config, error) {
	_ = godotenv.Load()
	cfg := Config{
		Port:              getEnv("PORT", "8080"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		WeatherProvider:   getEnv("WEATHER_PROVIDER", "open-meteo"),
		WeatherH3Res:      getEnvInt("WEATHER_H3_RES", 6),
		WeatherDailyLimit: getEnvInt("WEATHER_DAILY_LIMIT", 9000),
		RunMigrations:     getEnv("RUN_MIGRATIONS", "true") != "false",
		OverpassURL:       getEnv("OVERPASS_URL", "https://overpass-api.de/api/interpreter"),
		IngestExtractDir:  getEnv("INGEST_EXTRACT_DIR", "./data/extracts"),
	}
	if cfg.WeatherH3Res < 0 || cfg.WeatherH3Res > 15 {
		return Config{}, fmt.Errorf("WEATHER_H3_RES must be 0-15, got %d", cfg.WeatherH3Res)
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return cfg, nil
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
