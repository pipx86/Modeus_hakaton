-- +goose Up
-- Lease-блокировка защищает реестр от параллельной проверки одного источника
-- двумя воркерами или воркером и ручной проверкой администратора. Строка
-- считается занятой, пока locked_until > now(); владелец записан в locked_by,
-- чтобы снять блокировку могла только начавшая проверку сторона.
ALTER TABLE program_watch_sources
    ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS locked_by TEXT;

COMMENT ON COLUMN program_watch_sources.locked_until IS
    'До какого момента источник занят проверкой (lease). NULL — источник свободен; '
    'просроченная блокировка переиспользуется следующим воркером.';
COMMENT ON COLUMN program_watch_sources.locked_by IS
    'Идентификатор владельца lease-блокировки (hostname-pid или api-...). '
    'Блокировку снимает только совпадающий owner.';

-- Claim выбирает свободные источники по enabled + locked_until, поэтому
-- составной индекс закрывает и фильтр, и сортировку по блокировке.
CREATE INDEX IF NOT EXISTS program_watch_sources_lock_idx
    ON program_watch_sources(enabled, locked_until);

-- +goose Down
DROP INDEX IF EXISTS program_watch_sources_lock_idx;
ALTER TABLE program_watch_sources
    DROP COLUMN IF EXISTS locked_by,
    DROP COLUMN IF EXISTS locked_until;
