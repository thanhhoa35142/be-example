package main

import (
	"fmt"
	"log"

	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/platform/config"
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
