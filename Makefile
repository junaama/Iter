# Iter v1 — repo Makefile
# At this stage (design phase) the only working targets are database/migration helpers.
# Build, test, and release targets land later per ARCHITECTURE.md §9.

SHELL := /bin/bash

# trufflehog pin — kept in sync with `trufflehog.version` at the repo root.
# `internal/redact` reads `trufflehog.version` at test time and asserts the
# installed binary matches.
TRUFFLEHOG_VERSION := 3.95.3

# Migration runner: goose (https://github.com/pressly/goose)
# Driver: postgres
MIGRATIONS_DIR := migrations
GOOSE := goose -dir $(MIGRATIONS_DIR) postgres

# Default to a local docker postgres. Override DATABASE_URL to target Railway dev.
DATABASE_URL ?= postgres://iter:iter@localhost:5433/iter?sslmode=disable
PG_IMAGE := pgvector/pgvector:pg16
PG_CONTAINER := iter-pg-dev

.PHONY: help
help:
	@echo "Go targets:"
	@echo "  make test          Run go test ./..."
	@echo "  make lint          Run golangci-lint run"
	@echo ""
	@echo "Migration targets:"
	@echo "  make db-up         Start a local pgvector/pg16 container (port 5433)"
	@echo "  make db-down       Stop and remove the local container"
	@echo "  make db-psql       Open psql against the local container"
	@echo "  make migrate-up    Apply all pending migrations"
	@echo "  make migrate-down  Roll back the most recent migration"
	@echo "  make migrate-status Show migration status"
	@echo "  make migrate-reset Roll back all migrations"
	@echo "  make db-verify     Apply migrations and verify schema invariants"
	@echo ""
	@echo "DATABASE_URL=$(DATABASE_URL)"

.PHONY: test
test:
	go test ./...

.PHONY: lint
lint:
	golangci-lint run

.PHONY: db-up
db-up:
	@if docker ps -a --format '{{.Names}}' | grep -q '^$(PG_CONTAINER)$$'; then \
		docker start $(PG_CONTAINER) >/dev/null; \
	else \
		docker run -d --name $(PG_CONTAINER) \
			-e POSTGRES_USER=iter -e POSTGRES_PASSWORD=iter -e POSTGRES_DB=iter \
			-p 5433:5432 $(PG_IMAGE) >/dev/null; \
	fi
	@echo "Waiting for Postgres to accept connections..."
	@until docker exec $(PG_CONTAINER) pg_isready -U iter -d iter >/dev/null 2>&1; do sleep 0.5; done
	@echo "Postgres ready at $(DATABASE_URL)"

.PHONY: db-down
db-down:
	-@docker rm -f $(PG_CONTAINER) >/dev/null 2>&1
	@echo "Removed $(PG_CONTAINER)"

.PHONY: db-psql
db-psql:
	@docker exec -it $(PG_CONTAINER) psql -U iter -d iter

.PHONY: migrate-up
migrate-up:
	$(GOOSE) "$(DATABASE_URL)" up

.PHONY: migrate-down
migrate-down:
	$(GOOSE) "$(DATABASE_URL)" down

.PHONY: migrate-status
migrate-status:
	$(GOOSE) "$(DATABASE_URL)" status

.PHONY: migrate-reset
migrate-reset:
	$(GOOSE) "$(DATABASE_URL)" reset

# db-verify: smoke test that confirms the migration applies cleanly and
# the resulting schema satisfies the invariants documented in ARCHITECTURE.md §3.
# This is NOT the full RLS / cascade test suite (that lands in issue 004).
.PHONY: db-verify
db-verify: migrate-up
	@scripts/verify-migration.sh "$(DATABASE_URL)"
