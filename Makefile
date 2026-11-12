COMPOSE := docker compose

.PHONY: help deps db-up db-down migrate sqlc gen run dev build test lint tidy

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

deps: ## Download Go dependencies
	go mod tidy

db-up: ## Start Postgres via docker compose
	$(COMPOSE) up -d db

db-down: ## Stop Postgres
	$(COMPOSE) down

sqlc: ## Generate Go code from SQL queries (requires sqlc binary)
	sqlc generate

gen: sqlc ## Alias for sqlc

migrate: ## Run migrations manually (API also runs them on boot)
	go run ./cmd/migrate

run: ## Run the API server
	go run ./cmd/api

worker: ## Run the background worker
	go run ./cmd/worker

dev: db-up ## Hot-reload dev mode (requires air)
	air

build: ## Build binary
	go build -o bin/api ./cmd/api

test: ## Run unit tests
	go test ./...

lint: ## Vet + gofmt check
	go vet ./...
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

tidy: ## Tidy modules
	go mod tidy
