package main

import (
	"fmt"
	"log"

	"gitlab.com/walkalike-group/pickhour/internal/infrastructure/persistence"
	"gitlab.com/walkalike-group/pickhour/internal/platform/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := persistence.RunMigrations(cfg.DatabaseURL); err != nil {
		log.Fatalf("migrations: %v", err)
	}
	fmt.Println("migrations applied")
}
