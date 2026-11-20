-- +goose Up
-- +goose StatementBegin
-- Карточка 18 плана MVP: методист проверяет элементы дерева.
--
-- Решение хранится на самом узле: дерево сохраняется один раз (SaveTree
-- вставляет узлы с клиентскими id и не перезаписывает их), поэтому статус не
-- теряется при повторных чтениях и правках названия.
ALTER TABLE nodes
    ADD COLUMN review_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (review_status IN ('pending', 'approved', 'rejected')),
    ADD COLUMN review_comment TEXT NOT NULL DEFAULT '',
    ADD COLUMN reviewed_at TIMESTAMPTZ,
    ADD COLUMN reviewed_by TEXT NOT NULL DEFAULT '';

CREATE INDEX nodes_review_idx ON nodes (run_id, review_status);

-- evidence.approved — legacy-поле; раньше оно расходилось со status, потому что
-- триггер висел только на UPDATE OF status. Теперь поля синхронизируются в обе
-- стороны: изменение status задаёт approved, изменение только approved задаёт
-- status (approved -> approved, иначе -> pending).
CREATE OR REPLACE FUNCTION evidence_sync_approved() RETURNS TRIGGER AS $$
DECLARE
    status_changed boolean;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        status_changed := NEW.status IS DISTINCT FROM OLD.status;
    ELSE
        status_changed := NEW.status <> 'pending';
    END IF;
    IF TG_OP = 'UPDATE' AND NOT status_changed AND NEW.approved IS DISTINCT FROM OLD.approved THEN
        NEW.status := CASE WHEN NEW.approved THEN 'approved' ELSE 'pending' END;
    ELSIF TG_OP = 'INSERT' AND NEW.approved AND NEW.status = 'pending' THEN
        NEW.status := 'approved';
    END IF;
    NEW.approved := (NEW.status = 'approved');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_evidence_sync_approved ON evidence;
CREATE TRIGGER trg_evidence_sync_approved
    BEFORE INSERT OR UPDATE ON evidence
    FOR EACH ROW EXECUTE FUNCTION evidence_sync_approved();

-- Карточка 25 плана MVP: ожидаемый результат исследования выбирается при
-- запуске. Режим «только описание деятельности» программу не требует.
ALTER TABLE runs
    ADD COLUMN result_kind TEXT NOT NULL DEFAULT 'activity'
        CHECK (result_kind IN ('activity', 'program')),
    ADD COLUMN program_id UUID REFERENCES education_programs(id) ON DELETE SET NULL;

ALTER TABLE runs ADD CONSTRAINT runs_result_kind_program_check
    CHECK (
        (result_kind = 'program' AND program_id IS NOT NULL)
        OR (result_kind = 'activity')
    );

-- Карточки 21 и 23: связь узла деятельности с элементом образовательной
-- программы. Кандидатов предлагает ядро (карточка 22), а решение методиста
-- хранится здесь: до решения связь в статусе proposed и не считается фактом.
CREATE TABLE activity_program_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    program_id UUID NOT NULL REFERENCES education_programs(id) ON DELETE CASCADE,
    competency_id UUID NOT NULL REFERENCES program_competencies(id) ON DELETE CASCADE,
    ksa_id UUID REFERENCES competency_ksa(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'proposed'
        CHECK (status IN ('proposed', 'approved', 'rejected')),
    -- Объяснение, почему система предложила связь. Методист его читает перед
    -- решением, поэтому пустым оно быть не должно у предложений ядра.
    reason TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'system'
        CHECK (source IN ('system', 'methodist')),
    reviewed_by TEXT NOT NULL DEFAULT '',
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, node_id, competency_id)
);

CREATE INDEX activity_program_links_run_idx ON activity_program_links (run_id, status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS activity_program_links;
ALTER TABLE runs DROP CONSTRAINT IF EXISTS runs_result_kind_program_check;
ALTER TABLE runs DROP COLUMN IF EXISTS program_id;
ALTER TABLE runs DROP COLUMN IF EXISTS result_kind;

DROP TRIGGER IF EXISTS trg_evidence_sync_approved ON evidence;
CREATE OR REPLACE FUNCTION evidence_sync_approved() RETURNS TRIGGER AS $$
BEGIN
    NEW.approved := (NEW.status = 'approved');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_evidence_sync_approved
    BEFORE UPDATE OF status ON evidence
    FOR EACH ROW EXECUTE FUNCTION evidence_sync_approved();

DROP INDEX IF EXISTS nodes_review_idx;
ALTER TABLE nodes
    DROP COLUMN IF EXISTS reviewed_by,
    DROP COLUMN IF EXISTS reviewed_at,
    DROP COLUMN IF EXISTS review_comment,
    DROP COLUMN IF EXISTS review_status;
-- +goose StatementEnd
