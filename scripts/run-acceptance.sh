#!/bin/sh
set -eu
set -o pipefail
apk add --no-cache make
mkdir -p /tmp/database /tmp/backend
tar -C /schema --exclude=.git -cf - . | tar -C /tmp/database -xf -
tar -C /backend --exclude=.git --exclude=data/acceptance -cf - . | tar -C /tmp/backend -xf -
cd /tmp/database
export STORE_TEST_DATABASE_URL="$DATABASE_URL"
export VECTOR_TEST_DATABASE_URL="$DATABASE_URL"
go test -v -count=1 ./internal/store/... ./internal/demofill/... ./cmd/data-audit/... 2>&1 | tee /artifacts/database-tests.log
unset STORE_TEST_DATABASE_URL VECTOR_TEST_DATABASE_URL
export OUTBOX_TEST_DATABASE_URL="$DATABASE_URL"
go test -v -count=1 ./internal/outbox/... ./internal/webhook/... ./internal/domain/... 2>&1 | tee /artifacts/event-tests.log
unset OUTBOX_TEST_DATABASE_URL
make seed
make seed
export DATA_REPORTS_DIR=/tmp/current-reports
make check-data
cp /tmp/current-reports/db-audit-*.json /artifacts/
cd /tmp/backend
export INGEST_TEST_DATABASE_URL="$DATABASE_URL"
go test -v -count=1 ./... 2>&1 | tee /artifacts/backend-tests.log
