package programwatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

const claimedSourceID = "11111111-1111-1111-1111-111111111111"

var testSourceColumnNames = []string{
	"id", "program_key", "url", "doc_format", "interval_hours", "enabled",
	"etag", "last_modified", "last_hash",
	"document_url", "document_hash", "document_etag", "document_last_modified",
	"last_checked_at",
	"last_status", "last_error", "draft_id", "created_at", "updated_at", "failure_count",
}

func newWatchRepositoryMock(t *testing.T) (pgxmock.PgxPoolIface, *Repository) {
	t.Helper()
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, NewRepository(pool)
}

func claimedSourceRows() *pgxmock.Rows {
	return pgxmock.NewRows(testSourceColumnNames).AddRow(
		claimedSourceID,
		"mirea/09.03.01/2024/full-time",
		"https://example.com/program.csv",
		"",
		int32(24),
		true,
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		nil,
		"",
		"",
		"",
		testNow,
		testNow,
		int32(0),
	)
}

func TestClaimDueWatchSourcesUsesSkipLockedAndLease(t *testing.T) {
	pool, repository := newWatchRepositoryMock(t)
	lease := 10 * time.Minute
	owner := "worker-test"

	pool.ExpectQuery(`UPDATE program_watch_sources.*FOR UPDATE SKIP LOCKED`).
		WithArgs(testNow, lease, owner, 2).
		WillReturnRows(claimedSourceRows())

	sources, err := repository.ClaimDueWatchSources(context.Background(), testNow, 2, lease, owner)
	if err != nil {
		t.Fatalf("ClaimDueWatchSources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("ожидали один источник, получили %d", len(sources))
	}
	if sources[0].ID != claimedSourceID || sources[0].ProgramKey != "mirea/09.03.01/2024/full-time" {
		t.Fatalf("источник: %+v", sources[0])
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("ожидания pgxmock: %v", err)
	}
}

func TestClaimWatchSourceBusy(t *testing.T) {
	pool, repository := newWatchRepositoryMock(t)
	lease := 5 * time.Minute
	owner := "api-test"

	// UPDATE не обновил строку: источник занят чужой lease-блокировкой.
	pool.ExpectQuery(`UPDATE program_watch_sources.*WHERE id=\$1`).
		WithArgs(claimedSourceID, pgxmock.AnyArg(), lease, owner).
		WillReturnRows(pgxmock.NewRows(testSourceColumnNames))
	pool.ExpectQuery(`SELECT .*FROM program_watch_sources WHERE id=\$1`).
		WithArgs(claimedSourceID).
		WillReturnRows(claimedSourceRows())

	_, err := repository.ClaimWatchSource(context.Background(), claimedSourceID, lease, owner)
	if !errors.Is(err, ErrWatchSourceBusy) {
		t.Fatalf("ожидали ErrWatchSourceBusy, получили %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("ожидания pgxmock: %v", err)
	}
}

func TestClaimWatchSourceNotFound(t *testing.T) {
	pool, repository := newWatchRepositoryMock(t)
	lease := 5 * time.Minute
	owner := "api-test"

	pool.ExpectQuery(`UPDATE program_watch_sources.*WHERE id=\$1`).
		WithArgs(claimedSourceID, pgxmock.AnyArg(), lease, owner).
		WillReturnRows(pgxmock.NewRows(testSourceColumnNames))
	pool.ExpectQuery(`SELECT .*FROM program_watch_sources WHERE id=\$1`).
		WithArgs(claimedSourceID).
		WillReturnRows(pgxmock.NewRows(testSourceColumnNames))

	_, err := repository.ClaimWatchSource(context.Background(), claimedSourceID, lease, owner)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound, получили %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("ожидания pgxmock: %v", err)
	}
}

func TestSaveWatchCheckClearsLeaseForOwner(t *testing.T) {
	pool, repository := newWatchRepositoryMock(t)
	owner := "worker-test"
	update := CheckUpdate{
		CheckedAt: testNow,
		Status:    StatusUnchanged,
		Owner:     owner,
	}

	pool.ExpectExec(`UPDATE program_watch_sources.*locked_until=CASE WHEN locked_by=\$13 THEN NULL ELSE locked_until END.*locked_by=CASE WHEN locked_by=\$13 THEN NULL ELSE locked_by END.*WHERE id=\$1`).
		WithArgs(claimedSourceID, testNow, StatusUnchanged, "", "", false, nil, false, nil, false, nil, 0, owner,
			false, nil, false, nil, false, nil, false, nil).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repository.SaveWatchCheck(context.Background(), claimedSourceID, update); err != nil {
		t.Fatalf("SaveWatchCheck: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("ожидания pgxmock: %v", err)
	}
}

// TestSaveWatchCheckStoresDocumentFingerprint проверяет, что отпечаток
// привязанного документа записывается отдельными CASE WHEN ... ELSE old:
// без этого повторная проверка не увидела бы обновление xlsx/pdf.
func TestSaveWatchCheckStoresDocumentFingerprint(t *testing.T) {
	pool, repository := newWatchRepositoryMock(t)
	owner := "worker-test"
	documentURL := "https://example.com/files/program.pdf"
	documentHash := "document-hash"
	documentETag := `"doc-v1"`
	documentLastModified := "Mon, 20 Nov 2026 10:00:00 GMT"
	update := CheckUpdate{
		CheckedAt:            testNow,
		Status:               StatusChanged,
		Owner:                owner,
		DocumentURL:          &documentURL,
		DocumentHash:         &documentHash,
		DocumentETag:         &documentETag,
		DocumentLastModified: &documentLastModified,
	}

	pool.ExpectExec(`UPDATE program_watch_sources.*document_url=CASE WHEN \$14::boolean THEN \$15::text ELSE document_url END.*document_hash=CASE WHEN \$16::boolean THEN \$17::text ELSE document_hash END.*document_etag=CASE WHEN \$18::boolean THEN \$19::text ELSE document_etag END.*document_last_modified=CASE WHEN \$20::boolean THEN \$21::text ELSE document_last_modified END.*WHERE id=\$1`).
		WithArgs(claimedSourceID, testNow, StatusChanged, "", "", false, nil, false, nil, false, nil, 0, owner,
			true, documentURL, true, documentHash, true, documentETag, true, documentLastModified).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repository.SaveWatchCheck(context.Background(), claimedSourceID, update); err != nil {
		t.Fatalf("SaveWatchCheck: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("ожидания pgxmock: %v", err)
	}
}

func TestReleaseWatchSourceOnlyOwnLock(t *testing.T) {
	pool, repository := newWatchRepositoryMock(t)
	owner := "worker-test"

	pool.ExpectExec(`UPDATE program_watch_sources.*WHERE id=\$1 AND locked_by=\$2`).
		WithArgs(claimedSourceID, owner).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repository.ReleaseWatchSource(context.Background(), claimedSourceID, owner); err != nil {
		t.Fatalf("ReleaseWatchSource: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("ожидания pgxmock: %v", err)
	}
}
