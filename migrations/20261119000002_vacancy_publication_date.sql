-- +goose Up
ALTER TABLE sources ADD COLUMN vacancy_published_at TIMESTAMPTZ;

CREATE INDEX idx_sources_vacancy_published_at
    ON sources (vacancy_published_at)
    WHERE type = 'vacancy' AND vacancy_published_at IS NOT NULL;

-- +goose Down
DROP INDEX idx_sources_vacancy_published_at;
ALTER TABLE sources DROP COLUMN vacancy_published_at;
