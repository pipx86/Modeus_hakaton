package programwatch

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const sourceColumns = `id::text,program_key,url,COALESCE(doc_format,''),interval_hours,enabled,
COALESCE(etag,''),COALESCE(last_modified,''),COALESCE(last_hash,''),
COALESCE(document_url,''),COALESCE(document_hash,''),COALESCE(document_etag,''),COALESCE(document_last_modified,''),
last_checked_at,
COALESCE(last_status,''),COALESCE(last_error,''),COALESCE(draft_id::text,''),created_at,updated_at,failure_count`

// Repository — PostgreSQL-хранилище реестра источников.
type Repository struct {
	db db.DBTX
}

// NewRepository принимает pgxpool.Pool, pgx.Tx или другой DBTX.
func NewRepository(database db.DBTX) *Repository {
	return &Repository{db: database}
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSource(row rowScanner) (Source, error) {
	var source Source
	var checkedAt pgtype.Timestamptz
	err := row.Scan(
		&source.ID, &source.ProgramKey, &source.URL, &source.DocFormat, &source.IntervalHours, &source.Enabled,
		&source.ETag, &source.LastModified, &source.LastHash,
		&source.DocumentURL, &source.DocumentHash, &source.DocumentETag, &source.DocumentLastModified,
		&checkedAt,
		&source.LastStatus, &source.LastError, &source.DraftID, &source.CreatedAt, &source.UpdatedAt, &source.FailureCount,
	)
	if err != nil {
		return source, translate(err)
	}
	if checkedAt.Valid {
		at := checkedAt.Time
		source.LastCheckedAt = &at
	}
	return source, nil
}

// collectSources вычитывает RETURNING/SELECT строки и закрывает rows.
func collectSources(rows pgx.Rows) ([]Source, error) {
	defer rows.Close()
	items := []Source{}
	for rows.Next() {
		item, scanErr := scanSource(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, translate(rows.Err())
}

// validateClaim проверяет параметры lease-блокировки до запроса, чтобы пустой
// owner не превратился в блокировку, которую невозможно корректно снять.
func validateClaim(lease time.Duration, owner string) error {
	if lease <= 0 {
		return errors.New("programwatch: lease должен быть положительным")
	}
	if strings.TrimSpace(owner) == "" {
		return errors.New("programwatch: owner обязателен")
	}
	return nil
}

func (r *Repository) CreateWatchSource(ctx context.Context, input CreateInput) (Source, error) {
	if r == nil || r.db == nil {
		return Source{}, ErrUnavailable
	}
	row := r.db.QueryRow(ctx, `INSERT INTO program_watch_sources(program_key,url,doc_format,interval_hours,enabled)
VALUES($1,$2,NULLIF($3,''),$4,true)
RETURNING `+sourceColumns, input.ProgramKey, input.URL, input.DocFormat, input.IntervalHours)
	return scanSource(row)
}

func (r *Repository) ListWatchSources(ctx context.Context) ([]Source, error) {
	if r == nil || r.db == nil {
		return nil, ErrUnavailable
	}
	rows, err := r.db.Query(ctx, `SELECT `+sourceColumns+` FROM program_watch_sources ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil, translate(err)
	}
	return collectSources(rows)
}

func (r *Repository) GetWatchSource(ctx context.Context, id string) (Source, error) {
	if !uuidPattern.MatchString(id) {
		return Source{}, ErrNotFound
	}
	if r == nil || r.db == nil {
		return Source{}, ErrUnavailable
	}
	row := r.db.QueryRow(ctx, `SELECT `+sourceColumns+` FROM program_watch_sources WHERE id=$1`, id)
	return scanSource(row)
}

func (r *Repository) UpdateWatchSource(ctx context.Context, id string, patch SourcePatch) (Source, error) {
	if !uuidPattern.MatchString(id) {
		return Source{}, ErrNotFound
	}
	if r == nil || r.db == nil {
		return Source{}, ErrUnavailable
	}
	var enabled any
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	var interval any
	if patch.IntervalHours != nil {
		interval = *patch.IntervalHours
	}
	row := r.db.QueryRow(ctx, `UPDATE program_watch_sources
SET enabled=COALESCE($2::boolean,enabled),interval_hours=COALESCE($3::int,interval_hours),updated_at=now()
WHERE id=$1
RETURNING `+sourceColumns, id, enabled, interval)
	return scanSource(row)
}

func (r *Repository) DeleteWatchSource(ctx context.Context, id string) error {
	if !uuidPattern.MatchString(id) {
		return ErrNotFound
	}
	if r == nil || r.db == nil {
		return ErrUnavailable
	}
	tag, err := r.db.Exec(ctx, `DELETE FROM program_watch_sources WHERE id=$1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DueWatchSources возвращает просроченные источники без блокировки. Оставлен
// для обратной совместимости и диагностики: воркер и API используют claim-
// методы, потому что этот запрос не защищает от параллельной проверки.
func (r *Repository) DueWatchSources(ctx context.Context, now time.Time, limit int) ([]Source, error) {
	if r == nil || r.db == nil {
		return nil, ErrUnavailable
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := r.db.Query(ctx, `SELECT `+sourceColumns+`
FROM program_watch_sources
WHERE enabled AND (last_checked_at IS NULL OR last_checked_at + make_interval(hours => interval_hours) <= $1)
ORDER BY last_checked_at ASC NULLS FIRST,id
LIMIT $2`, now, limit)
	if err != nil {
		return nil, translate(err)
	}
	return collectSources(rows)
}

// ClaimDueWatchSources одним UPDATE выбирает просроченные источники и ставит
// на них lease-блокировку. FOR UPDATE SKIP LOCKED не даёт двум воркерам взять
// одну строку: проигравший пропускает её и берёт следующую.
func (r *Repository) ClaimDueWatchSources(ctx context.Context, now time.Time, limit int, lease time.Duration, owner string) ([]Source, error) {
	if r == nil || r.db == nil {
		return nil, ErrUnavailable
	}
	if err := validateClaim(lease, owner); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := r.db.Query(ctx, `UPDATE program_watch_sources
SET locked_until=$1::timestamptz + $2::interval,
    locked_by=$3,
    updated_at=now()
WHERE id IN (
    SELECT id
    FROM program_watch_sources
    WHERE enabled
      AND (last_checked_at IS NULL OR last_checked_at + make_interval(hours => interval_hours) <= $1::timestamptz)
      AND (locked_until IS NULL OR locked_until < $1::timestamptz)
    ORDER BY last_checked_at ASC NULLS FIRST, id
    LIMIT $4
    FOR UPDATE SKIP LOCKED
)
RETURNING `+sourceColumns, now, lease, owner, limit)
	if err != nil {
		return nil, translate(err)
	}
	return collectSources(rows)
}

// ClaimWatchSource ставит lease-блокировку на один источник для ручной
// проверки. Расписание (enabled/due) здесь намеренно не проверяется: админ
// может проверить источник «сейчас» независимо от интервала, но не может
// вклиниться в уже идущую проверку — тогда возвращается ErrWatchSourceBusy.
func (r *Repository) ClaimWatchSource(ctx context.Context, id string, lease time.Duration, owner string) (Source, error) {
	if !uuidPattern.MatchString(id) {
		return Source{}, ErrNotFound
	}
	if r == nil || r.db == nil {
		return Source{}, ErrUnavailable
	}
	if err := validateClaim(lease, owner); err != nil {
		return Source{}, err
	}
	now := time.Now().UTC()
	row := r.db.QueryRow(ctx, `UPDATE program_watch_sources
SET locked_until=$2::timestamptz + $3::interval,
    locked_by=$4,
    updated_at=now()
WHERE id=$1
  AND (locked_until IS NULL OR locked_until < $2::timestamptz)
RETURNING `+sourceColumns, id, now, lease, owner)
	source, err := scanSource(row)
	if err == nil {
		return source, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Source{}, err
	}
	// Строка не обновилась: различаем «нет такого источника» (404) и
	// «источник занят» (409), чтобы API вернул осмысленный ответ.
	if _, getErr := r.GetWatchSource(ctx, id); getErr != nil {
		return Source{}, getErr
	}
	return Source{}, ErrWatchSourceBusy
}

// ReleaseWatchSource снимает блокировку при ошибке до сохранения результата.
// Освобождать можно только свою блокировку: чужой owner не совпадёт с
// locked_by, и запрос не изменит строку. Повторный release безвреден.
func (r *Repository) ReleaseWatchSource(ctx context.Context, id, owner string) error {
	if !uuidPattern.MatchString(id) {
		return ErrNotFound
	}
	if r == nil || r.db == nil {
		return ErrUnavailable
	}
	if strings.TrimSpace(owner) == "" {
		return errors.New("programwatch: owner обязателен")
	}
	if _, err := r.db.Exec(ctx, `UPDATE program_watch_sources
SET locked_until=NULL,
    locked_by=NULL,
    updated_at=now()
WHERE id=$1 AND locked_by=$2`, id, owner); err != nil {
		return translate(err)
	}
	return nil
}

func (r *Repository) SaveWatchCheck(ctx context.Context, id string, update CheckUpdate) error {
	if !uuidPattern.MatchString(id) {
		return ErrNotFound
	}
	if r == nil || r.db == nil {
		return ErrUnavailable
	}
	// Блокировку снимает только тот, кто её поставил: если lease истёк и
	// источник уже перехватил другой owner, его locked_until/locked_by
	// остаются нетронутыми (CASE вычисляется по старым значениям строки).
	tag, err := r.db.Exec(ctx, `UPDATE program_watch_sources
SET last_checked_at=$2,
    last_status=$3,
    last_error=NULLIF($4,''),
    draft_id=COALESCE(NULLIF($5,'')::uuid,draft_id),
    etag=CASE WHEN $6::boolean THEN $7::text ELSE etag END,
    last_modified=CASE WHEN $8::boolean THEN $9::text ELSE last_modified END,
    last_hash=CASE WHEN $10::boolean THEN $11::text ELSE last_hash END,
    document_url=CASE WHEN $14::boolean THEN $15::text ELSE document_url END,
    document_hash=CASE WHEN $16::boolean THEN $17::text ELSE document_hash END,
    document_etag=CASE WHEN $18::boolean THEN $19::text ELSE document_etag END,
    document_last_modified=CASE WHEN $20::boolean THEN $21::text ELSE document_last_modified END,
    failure_count=$12,
    locked_until=CASE WHEN locked_by=$13 THEN NULL ELSE locked_until END,
    locked_by=CASE WHEN locked_by=$13 THEN NULL ELSE locked_by END,
    updated_at=now()
WHERE id=$1`,
		id, update.CheckedAt, update.Status, update.Error, update.DraftID,
		update.ETag != nil, textValue(update.ETag),
		update.LastModified != nil, textValue(update.LastModified),
		update.LastHash != nil, textValue(update.LastHash),
		update.FailureCount, update.Owner,
		update.DocumentURL != nil, textValue(update.DocumentURL),
		update.DocumentHash != nil, textValue(update.DocumentHash),
		update.DocumentETag != nil, textValue(update.DocumentETag),
		update.DocumentLastModified != nil, textValue(update.DocumentLastModified))
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func textValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation: program_key+url
			return ErrDuplicate
		case "22P02": // invalid_text_representation: битый UUID в пути
			return ErrNotFound
		}
	}
	return err
}

var _ Store = (*Repository)(nil)
