-- +goose Up
-- +goose StatementBegin
-- Согласие на обработку персональных данных (152-ФЗ). Версия документа
-- хранится и в users (текущее состояние для быстрых проверок), и в
-- user_consents (история принятий: версия, время, IP и User-Agent).
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS consent_version TEXT;
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS consent_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS user_consents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    document_version TEXT NOT NULL,
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ip TEXT,
    user_agent TEXT
);

CREATE INDEX IF NOT EXISTS user_consents_user_id_idx ON user_consents (user_id);

-- Демо-учётки существовали до появления согласия. Им проставляем текущую
-- версию, чтобы демонстрация не упиралась в 403, а в истории помечаем
-- принятие как унаследованное (v1-legacy), а не как реальное действие
-- пользователя. Остальные учётные записи остаются без согласия.
UPDATE users
SET consent_version = 'v1',
    consent_at = created_at
WHERE email LIKE '%@modeus.local'
  AND consent_version IS NULL;

INSERT INTO user_consents (user_id, document_version, accepted_at)
SELECT u.id, 'v1-legacy', u.created_at
FROM users u
WHERE u.email LIKE '%@modeus.local'
  AND NOT EXISTS (
      SELECT 1
      FROM user_consents c
      WHERE c.user_id = u.id
        AND c.document_version = 'v1-legacy'
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_consents;
ALTER TABLE users DROP COLUMN IF EXISTS consent_at;
ALTER TABLE users DROP COLUMN IF EXISTS consent_version;
-- +goose StatementEnd
