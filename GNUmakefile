.PHONY: check-data seed db-coverage
check-data:
	go run ./cmd/data-audit
seed:
	go run ./cmd/demo-fixture

DB_COVERAGE_FILE ?= coverage-db.out
DB_COVERAGE_PACKAGES = ./internal/store,./internal/domain,./internal/demofill,./internal/outbox,./internal/webhook
DB_TEST_PACKAGES = ./internal/store/... ./internal/domain/... ./internal/demofill/... ./internal/outbox/... ./internal/webhook/... ./cmd/data-audit/... ./tests/database/...
db-coverage:
	go test -count=1 -coverprofile=$(DB_COVERAGE_FILE) -coverpkg=$(DB_COVERAGE_PACKAGES) $(DB_TEST_PACKAGES)
	go tool cover -func=$(DB_COVERAGE_FILE)
