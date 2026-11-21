package api

import (
	"context"
	"time"

	"activity-descriptor/internal/programwatch"
)

// watchRepository создаёт репозиторий поверх того же пула, что и остальные
// запросы API. Репозиторий не хранит состояние, поэтому создание на каждый
// вызов безопасно.
func (p *PGStore) watchRepository() *programwatch.Repository {
	if p == nil || p.Pool == nil {
		return nil
	}
	return programwatch.NewRepository(p.Pool)
}

func (p *PGStore) CreateWatchSource(ctx context.Context, input programwatch.CreateInput) (programwatch.Source, error) {
	repository := p.watchRepository()
	if repository == nil {
		return programwatch.Source{}, programwatch.ErrUnavailable
	}
	return repository.CreateWatchSource(ctx, input)
}

func (p *PGStore) ListWatchSources(ctx context.Context) ([]programwatch.Source, error) {
	repository := p.watchRepository()
	if repository == nil {
		return nil, programwatch.ErrUnavailable
	}
	return repository.ListWatchSources(ctx)
}

func (p *PGStore) GetWatchSource(ctx context.Context, id string) (programwatch.Source, error) {
	repository := p.watchRepository()
	if repository == nil {
		return programwatch.Source{}, programwatch.ErrUnavailable
	}
	return repository.GetWatchSource(ctx, id)
}

func (p *PGStore) UpdateWatchSource(ctx context.Context, id string, patch programwatch.SourcePatch) (programwatch.Source, error) {
	repository := p.watchRepository()
	if repository == nil {
		return programwatch.Source{}, programwatch.ErrUnavailable
	}
	return repository.UpdateWatchSource(ctx, id, patch)
}

func (p *PGStore) DeleteWatchSource(ctx context.Context, id string) error {
	repository := p.watchRepository()
	if repository == nil {
		return programwatch.ErrUnavailable
	}
	return repository.DeleteWatchSource(ctx, id)
}

func (p *PGStore) DueWatchSources(ctx context.Context, now time.Time, limit int) ([]programwatch.Source, error) {
	repository := p.watchRepository()
	if repository == nil {
		return nil, programwatch.ErrUnavailable
	}
	return repository.DueWatchSources(ctx, now, limit)
}

func (p *PGStore) SaveWatchCheck(ctx context.Context, id string, update programwatch.CheckUpdate) error {
	repository := p.watchRepository()
	if repository == nil {
		return programwatch.ErrUnavailable
	}
	return repository.SaveWatchCheck(ctx, id, update)
}

// CreateProgramWatchDraft превращает результат воркера в обычный черновик
// импорта, чтобы администратор проверял его тем же UI и публиковал тем же
// эндпоинтом, что и файл, загруженный вручную.
func (p *PGStore) CreateProgramWatchDraft(ctx context.Context, input programwatch.DraftInput) (string, error) {
	warnings := nonNilStrings(input.Warnings)
	created, err := p.CreateProgramImportDraft(ctx, ProgramImportDraft{
		Status:       programDraftStatusDraft,
		Key:          input.ProgramKey,
		SourceName:   input.SourceName,
		SourceFormat: input.SourceFormat,
		Program:      input.Program,
		Report: ProgramImportReport{
			Competencies: input.Competencies,
			KSA:          input.KSA,
			Warnings:     warnings,
		},
		Warnings: warnings,
	})
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

var _ programwatch.Store = (*PGStore)(nil)
var _ programwatch.DraftCreator = (*PGStore)(nil)
