-- +goose Up
-- Проверка откладывается до конца транзакции, чтобы дерево и подтверждения
-- можно было записывать по частям внутри одной операции.
-- +goose StatementBegin
CREATE FUNCTION check_stored_tree(p_run_id UUID) RETURNS VOID AS $$
DECLARE root UUID;
BEGIN
    SELECT root_node_id INTO root FROM runs WHERE id = p_run_id;
    IF NOT FOUND THEN RETURN; END IF;

    IF NOT EXISTS (SELECT 1 FROM nodes WHERE run_id = p_run_id) THEN
        IF root IS NOT NULL THEN
            RAISE EXCEPTION 'У исследования указан отсутствующий корень' USING ERRCODE = 'check_violation';
        END IF;
        RETURN;
    END IF;
    IF root IS NULL THEN
        RAISE EXCEPTION 'У сохранённого дерева отсутствует корень' USING ERRCODE = 'check_violation', CONSTRAINT = 'ORPHAN_NODE';
    END IF;
    IF EXISTS (SELECT 1 FROM nodes WHERE run_id=p_run_id AND title ~ '^[[:space:]]*$') THEN
        RAISE EXCEPTION 'Название узла пустое' USING ERRCODE = 'check_violation', CONSTRAINT = 'EMPTY_TITLE';
    END IF;
    IF EXISTS (
        WITH RECURSIVE ancestors AS (
            SELECT id, parent_id, ARRAY[id]::uuid[] AS visited, false AS has_cycle
            FROM nodes WHERE run_id=p_run_id
            UNION ALL
            SELECT p.id, p.parent_id, a.visited || p.id, p.id = ANY(a.visited)
            FROM ancestors a JOIN nodes p ON p.id=a.parent_id AND p.run_id=p_run_id
            WHERE NOT a.has_cycle
        ) SELECT 1 FROM ancestors WHERE has_cycle
    ) THEN
        RAISE EXCEPTION 'Дерево содержит цикл' USING ERRCODE = 'check_violation', CONSTRAINT = 'CYCLE_DETECTED';
    END IF;
    IF EXISTS (
        SELECT 1 FROM nodes n LEFT JOIN nodes p ON p.id=n.parent_id AND p.run_id=n.run_id
        WHERE n.run_id=p_run_id AND n.id<>root AND (n.parent_id IS NULL OR p.id IS NULL)
    ) THEN
        RAISE EXCEPTION 'У узла отсутствует родитель' USING ERRCODE = 'check_violation', CONSTRAINT = 'ORPHAN_NODE';
    END IF;
    IF EXISTS (
        SELECT 1 FROM nodes n LEFT JOIN nodes p ON p.id=n.parent_id AND p.run_id=n.run_id
        WHERE n.run_id=p_run_id AND (
            (n.id=root AND (n.parent_id IS NOT NULL OR n.level<>0 OR n.type<>'role'))
            OR (n.id<>root AND (n.level<>p.level+1
                OR (n.level=1 AND n.type<>'function')
                OR (n.level=2 AND n.type<>'task')
                OR (n.level=3 AND n.type<>'action')
                OR n.level NOT BETWEEN 1 AND 3))
        )
    ) THEN
        RAISE EXCEPTION 'Корень или уровень узла неверен' USING ERRCODE = 'check_violation', CONSTRAINT = 'INVALID_TREE_DEPTH';
    END IF;
    IF EXISTS (
        SELECT 1 FROM nodes WHERE run_id=p_run_id
        GROUP BY parent_id, regexp_replace(title, '^[[:space:]]+|[[:space:]]+$', '', 'g')
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'У одного родителя есть узлы с одинаковым названием' USING ERRCODE = 'check_violation', CONSTRAINT = 'DUPLICATE_SIBLING_TITLE';
    END IF;
    IF EXISTS (
        SELECT 1 FROM nodes n WHERE n.run_id=p_run_id
        AND NOT EXISTS (SELECT 1 FROM evidence e WHERE e.node_id=n.id)
    ) THEN
        RAISE EXCEPTION 'У узла отсутствует подтверждение' USING ERRCODE = 'check_violation', CONSTRAINT = 'MISSING_EVIDENCE';
    END IF;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- Сериализуем изменения дерева одного исследования, включая подтверждения.
-- +goose StatementBegin
CREATE FUNCTION lock_stored_tree() RETURNS TRIGGER AS $$
DECLARE old_run UUID; new_run UUID;
BEGIN
    IF TG_TABLE_NAME = 'nodes' THEN
        IF TG_OP <> 'INSERT' THEN old_run := OLD.run_id; END IF;
        IF TG_OP <> 'DELETE' THEN new_run := NEW.run_id; END IF;
    ELSE
        IF TG_OP <> 'INSERT' THEN old_run := OLD.run_id; END IF;
        IF TG_OP <> 'DELETE' THEN SELECT run_id INTO new_run FROM nodes WHERE id = NEW.node_id; END IF;
    END IF;
    PERFORM id FROM runs WHERE id = old_run OR id = new_run ORDER BY id FOR UPDATE;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER a_lock_node_tree BEFORE INSERT OR UPDATE OR DELETE ON nodes
FOR EACH ROW EXECUTE FUNCTION lock_stored_tree();
CREATE TRIGGER a_lock_evidence_tree BEFORE INSERT OR UPDATE OR DELETE ON evidence
FOR EACH ROW EXECUTE FUNCTION lock_stored_tree();

-- +goose StatementBegin
CREATE FUNCTION check_changed_tree() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN PERFORM check_stored_tree(OLD.run_id); END IF;
    IF TG_OP <> 'DELETE' THEN PERFORM check_stored_tree(NEW.run_id); END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER trg_nodes_tree_integrity
AFTER INSERT OR UPDATE OR DELETE ON nodes DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION check_changed_tree();
CREATE CONSTRAINT TRIGGER trg_evidence_tree_integrity
AFTER INSERT OR UPDATE OR DELETE ON evidence DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION check_changed_tree();

-- +goose StatementBegin
CREATE FUNCTION check_changed_root() RETURNS TRIGGER AS $$
BEGIN
    PERFORM check_stored_tree(NEW.id);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER trg_runs_tree_integrity
AFTER INSERT OR UPDATE OF root_node_id ON runs DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION check_changed_root();

-- Не принимаем миграцию, если существующие исследования уже некорректны.
-- +goose StatementBegin
DO $$
DECLARE item RECORD;
BEGIN
    FOR item IN SELECT id FROM runs LOOP
        PERFORM check_stored_tree(item.id);
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS trg_runs_tree_integrity ON runs;
DROP FUNCTION IF EXISTS check_changed_root;
DROP TRIGGER IF EXISTS trg_evidence_tree_integrity ON evidence;
DROP TRIGGER IF EXISTS trg_nodes_tree_integrity ON nodes;
DROP FUNCTION IF EXISTS check_changed_tree;
DROP TRIGGER IF EXISTS a_lock_evidence_tree ON evidence;
DROP TRIGGER IF EXISTS a_lock_node_tree ON nodes;
DROP FUNCTION IF EXISTS lock_stored_tree;
DROP FUNCTION IF EXISTS check_stored_tree;
