// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port            string
	DatabaseURL     string
	WeatherProvider string // "open-meteo" (default) | "weatherkit"
	RunMigrations   bool   // auto-run DB migrations on boot (default true)
}

func Load() (Config, error) {
	_ = godotenv.Load()
	cfg := Config{
		Port:            getEnv("PORT", "8080"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		WeatherProvider: getEnv("WEATHER_PROVIDER", "open-meteo"),
		RunMigrations:   getEnv("RUN_MIGRATIONS", "true") != "false",
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
