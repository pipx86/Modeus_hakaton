package api

import (
	"context"
	"encoding/json"
	"errors"

	"activity-descriptor/internal/ingestion"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// CreateProgramImportDraft сохраняет разобранную программу как черновик. Пока
// черновик в статусе draft, публикация и отклонение — единственные решения.
func (p *PGStore) CreateProgramImportDraft(ctx context.Context, draft ProgramImportDraft) (ProgramImportDraft, error) {
	if p == nil || p.Pool == nil {
		return draft, ErrServiceUnavailable
	}
	programJSON, err := json.Marshal(draft.Program)
	if err != nil {
		return draft, err
	}
	reportJSON, err := json.Marshal(draft.Report)
	if err != nil {
		return draft, err
	}
	warningsJSON, err := json.Marshal(nonNilStrings(draft.Warnings))
	if err != nil {
		return draft, err
	}
	var createdBy any
	if validUUID(draft.CreatedBy) {
		createdBy = draft.CreatedBy
	}
	err = p.Pool.QueryRow(ctx, `INSERT INTO program_import_drafts(status,program_key,source_name,source_format,program_json,report_json,warnings,created_by)
VALUES($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7::jsonb,$8) RETURNING id::text,status,created_at`,
		programDraftStatusDraft, draft.Key, draft.SourceName, draft.SourceFormat, programJSON, reportJSON, warningsJSON, createdBy,
	).Scan(&draft.ID, &draft.Status, &draft.CreatedAt)
	return draft, translate(err)
}

func (p *PGStore) ListProgramImportDrafts(ctx context.Context) ([]ProgramImportDraftSummary, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	rows, err := p.Pool.Query(ctx, `SELECT id::text,program_key,source_name,status,created_at,
COALESCE((report_json->>'competencies')::int,0),COALESCE((report_json->>'ksa')::int,0),COALESCE(jsonb_array_length(warnings),0)
FROM program_import_drafts ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	items := []ProgramImportDraftSummary{}
	for rows.Next() {
		var item ProgramImportDraftSummary
		if err = rows.Scan(&item.ID, &item.Key, &item.SourceName, &item.Status, &item.CreatedAt, &item.Counts.Competencies, &item.Counts.KSA, &item.Counts.Warnings); err != nil {
			return nil, translate(err)
		}
		items = append(items, item)
	}
	return items, translate(rows.Err())
}

func (p *PGStore) GetProgramImportDraft(ctx context.Context, id string) (ProgramImportDraft, error) {
	draft := ProgramImportDraft{}
	if !validUUID(id) {
		return draft, ErrNotFound
	}
	if p == nil || p.Pool == nil {
		return draft, ErrServiceUnavailable
	}
	var programJSON, reportJSON, warningsJSON []byte
	var decidedAt pgtype.Timestamptz
	err := p.Pool.QueryRow(ctx, `SELECT id::text,status,program_key,source_name,source_format,program_json,report_json,warnings,
COALESCE(created_by::text,''),created_at,decided_at,COALESCE(decided_by::text,''),COALESCE(error,'')
FROM program_import_drafts WHERE id=$1`, id).Scan(
		&draft.ID, &draft.Status, &draft.Key, &draft.SourceName, &draft.SourceFormat, &programJSON, &reportJSON, &warningsJSON,
		&draft.CreatedBy, &draft.CreatedAt, &decidedAt, &draft.DecidedBy, &draft.Error,
	)
	if err != nil {
		return draft, translate(err)
	}
	if err = json.Unmarshal(programJSON, &draft.Program); err != nil {
		return draft, err
	}
	if err = json.Unmarshal(reportJSON, &draft.Report); err != nil {
		return draft, err
	}
	if err = json.Unmarshal(warningsJSON, &draft.Warnings); err != nil {
		return draft, err
	}
	draft.Warnings = nonNilStrings(draft.Warnings)
	if decidedAt.Valid {
		at := decidedAt.Time
		draft.DecidedAt = &at
	}
	return draft, nil
}

// SaveProgramImportDraftProgram сохраняет обновлённую программу черновика и
// пересчитанные счётчики отчёта. Статус и предупреждения импорта не меняются:
// добавление KSA из ОПОП/РПД — уточнение черновика, а не новое решение по нему.
func (p *PGStore) SaveProgramImportDraftProgram(ctx context.Context, id string, program ingestion.EducationProgram, report ProgramImportReport) error {
	if !validUUID(id) {
		return ErrNotFound
	}
	if p == nil || p.Pool == nil {
		return ErrServiceUnavailable
	}
	programJSON, err := json.Marshal(program)
	if err != nil {
		return err
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return err
	}
	tag, err := p.Pool.Exec(ctx, `UPDATE program_import_drafts SET program_json=$2::jsonb,report_json=$3::jsonb WHERE id=$1`,
		id, programJSON, reportJSON)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return ErrNotFound
}

func (p *PGStore) MarkProgramImportDraftPublished(ctx context.Context, id, decidedBy string, report ingestion.EducationReport) error {
	if !validUUID(id) {
		return ErrNotFound
	}
	if p == nil || p.Pool == nil {
		return ErrServiceUnavailable
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return err
	}
	var decidedByValue any
	if validUUID(decidedBy) {
		decidedByValue = decidedBy
	}
	tag, err := p.Pool.Exec(ctx, `UPDATE program_import_drafts SET status=$2,decided_at=now(),decided_by=$3,report_json=$4::jsonb
WHERE id=$1 AND status=$5`, id, programDraftStatusPublished, decidedByValue, reportJSON, programDraftStatusDraft)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return p.programDraftDecisionError(ctx, id)
}

func (p *PGStore) MarkProgramImportDraftRejected(ctx context.Context, id, decidedBy, reason string) error {
	if !validUUID(id) {
		return ErrNotFound
	}
	if p == nil || p.Pool == nil {
		return ErrServiceUnavailable
	}
	var decidedByValue any
	if validUUID(decidedBy) {
		decidedByValue = decidedBy
	}
	tag, err := p.Pool.Exec(ctx, `UPDATE program_import_drafts SET status=$2,decided_at=now(),decided_by=$3,error=NULLIF($4,'')
WHERE id=$1 AND status=$5`, id, programDraftStatusRejected, decidedByValue, reason, programDraftStatusDraft)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return p.programDraftDecisionError(ctx, id)
}

// programDraftDecisionError различает отсутствующий черновик и уже принятое
// решение: первому соответствует 404, второму — 409.
func (p *PGStore) programDraftDecisionError(ctx context.Context, id string) error {
	var status string
	err := p.Pool.QueryRow(ctx, `SELECT status FROM program_import_drafts WHERE id=$1`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return errProgramDraftDecided
}

// PublishEducation — единственная точка публикации: хендлер не работает с SQL
// напрямую, поэтому тесты подменяют его интерфейсом programPublisher.
func (p *PGStore) PublishEducation(ctx context.Context, key, taxonomy string, program ingestion.EducationProgram) (ingestion.EducationReport, error) {
	if p == nil || p.Pool == nil {
		return ingestion.EducationReport{}, ErrServiceUnavailable
	}
	return ingestion.SaveEducation(ctx, p.Pool, key, taxonomy, program)
}

var _ programDraftStore = (*PGStore)(nil)
var _ programPublisher = (*PGStore)(nil)
