# Activity Descriptor вЂ” Go backend DevOps entry points.
#
# `make` is optional: every target is a thin wrapper around a command that is
# documented in docs/ (and can be run directly, e.g. on Windows in pwsh).
SHELL := /bin/sh

GO ?= go
PYTHON ?= python3
COMPOSE ?= docker compose
COMPOSE_FILE ?= docker-compose.yml
IMAGE ?= activity-descriptor-api:local
DEMO_ADDR ?= 127.0.0.1:18080

# Load local overrides (ports, passwords) when present. Never committed.
ifneq (,$(wildcard .env))
include .env
export
endif

# Safe local fallback so `make run`/`make token`/`make demo-smoke` work without
# a .env. Production injects JWT_SECRET from a secret manager.
ifeq ($(strip $(JWT_SECRET)),)
JWT_SECRET := local-dev-jwt-secret-change-me-32-bytes-min
endif
export JWT_SECRET

.DEFAULT_GOAL := help

## help: list available targets
help:
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## env: create .env from .env.example when missing
env:
	@if [ ! -f .env ]; then cp .env.example .env; echo "created .env (local placeholders only)"; else echo ".env already exists"; fi

## deps: install Go modules, frontend packages and Python tooling
deps:
	$(GO) mod download
	cd frontend && npm ci
	$(PYTHON) -m pip install -r requirements-dev.txt

# --- build & quality -------------------------------------------------------

## build: compile all Go packages
build:
	$(GO) build ./...

## test: run the test suite (no database or LLM required)
test:
	$(GO) test -count=1 ./...

## test-race: run tests with the race detector
test-race:
	$(GO) test -race -count=1 ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## fmt: format Go sources
fmt:
	gofmt -w cmd internal

## fmt-check: fail if sources are not gofmt-formatted
fmt-check:
	@out="$$(gofmt -l cmd internal)"; \
	if [ -n "$$out" ]; then echo "gofmt required for:"; echo "$$out"; exit 1; fi

## lint: run golangci-lint (install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.62.2)
lint:
	golangci-lint run ./...

## tidy: sync go.mod/go.sum
tidy:
	$(GO) mod tidy

## ci: local equivalent of the CI lint+test jobs
ci: fmt-check vet test

# --- local run -------------------------------------------------------------

## run: start the demo API (in-memory store + chat fallback)
run:
	HTTP_ADDR=$${HTTP_ADDR:-127.0.0.1:8080} $(GO) run ./cmd/api

## token: issue a local JWT for the protected /api/v1 endpoints
token:
	$(GO) run ./cmd/local-token -role admin

## e2e: run the offline interview в†’ analysis в†’ synthesis pipeline (fake model, no DB)
e2e:
	$(GO) run ./cmd/e2e -out e2e-output.json

## demo-smoke: build and curl the demo API (/health, /metrics, JWT chat flow)
demo-smoke:
	@set -eu; \
	$(GO) build -o bin/api ./cmd/api; \
	$(GO) build -o bin/local-token ./cmd/local-token; \
	HTTP_ADDR=$(DEMO_ADDR) ./bin/api > bin/demo-smoke.log 2>&1 & \
	pid=$$!; \
	trap 'kill $$pid 2>/dev/null || true' EXIT INT TERM; \
	for i in $$(seq 1 100); do curl -fsS "http://$(DEMO_ADDR)/health" >/dev/null 2>&1 && break; sleep 0.1; done; \
	curl -fsS "http://$(DEMO_ADDR)/health" >/dev/null; \
	curl -fsS "http://$(DEMO_ADDR)/metrics" | grep -q activity_descriptor; \
	token=$$(./bin/local-token -role admin); \
	curl -fsS -X POST "http://$(DEMO_ADDR)/api/v1/runs" \
		-H "Authorization: Bearer $$token" -H 'Content-Type: application/json' \
		-d '{"profession":"analyst"}' | grep -q '"id"'; \
	sleep 0.3; \
	curl -fsS "http://$(DEMO_ADDR)/api/v1/runs/run-1/messages" \
		-H "Authorization: Bearer $$token" | grep -q 'agent'; \
	echo "demo smoke OK (health, metrics, JWT chat flow)"

# --- docker ----------------------------------------------------------------

## docker-build: build the API image
docker-build:
	$(COMPOSE) -f $(COMPOSE_FILE) build api

## docker-size: print the API image size in bytes (< 104857600 required)
docker-size:
	docker image inspect $(IMAGE) --format '{{.Size}} bytes'

## up: start api + web + postgres
up:
	$(COMPOSE) -f $(COMPOSE_FILE) up -d --build api web postgres

## down: stop every profile (keeps volumes)
down:
	$(COMPOSE) -f $(COMPOSE_FILE) --profile monitoring --profile automation down

## logs: follow stack logs
logs:
	$(COMPOSE) -f $(COMPOSE_FILE) logs -f --tail=100 api web postgres

## up-monitoring: start prometheus + grafana
up-monitoring:
	$(COMPOSE) -f $(COMPOSE_FILE) --profile monitoring up -d prometheus grafana

## up-automation: start n8n + n8n-postgres
up-automation:
	$(COMPOSE) -f $(COMPOSE_FILE) --profile automation up -d n8n n8n-postgres

## check-compose: statically validate compose (default + all profiles)
check-compose:
	$(COMPOSE) -f $(COMPOSE_FILE) config --quiet
	$(COMPOSE) -f $(COMPOSE_FILE) --profile monitoring --profile automation config --quiet

## check-deploy: validate compose/Grafana/Prometheus/n8n assets and docs
check-deploy:
	python3 scripts/check_deploy.py

# --- n8n -------------------------------------------------------------------

## import-n8n: import deploy/n8n/workflows/$(FILE) into n8n
import-n8n:
	@test -n "$(FILE)" || { echo "usage: make import-n8n FILE=nightly_ingestion.json"; exit 1; }
	$(COMPOSE) -f $(COMPOSE_FILE) --profile automation exec n8n n8n import:workflow --input=/workflows/$(FILE)

## clean: remove build artifacts
clean:
	rm -rf bin coverage.out e2e-output.json

.PHONY: help env deps build test test-race vet fmt fmt-check lint tidy ci run token e2e demo-smoke docker-build docker-size up down logs up-monitoring up-automation check-compose check-deploy import-n8n clean
