-- +goose Up
-- Расширяем полезную нагрузку события run.completed для n8n-уведомления:
-- профессия, статус, человеческое сообщение и счётчики собранных данных.
-- Старые поля (event, run_id, profession, finished_at) остаются без изменений,
-- поэтому уже настроенные получатели и HMAC-подпись не ломаются.
-- Событие создаётся и для терминального статуса degraded: анализ в этом случае
-- тоже обновлён, но с ограничениями (degraded_reason).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_run_completed()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status IN ('done', 'degraded')
       AND (TG_OP = 'INSERT' OR OLD.status IS DISTINCT FROM NEW.status) THEN
        INSERT INTO agent_messages (run_id, message_type, payload)
        VALUES (
            NEW.id,
            'run.completed',
            jsonb_build_object(
                'payload_version', 2,
                'event', 'run.completed',
                'run_id', NEW.id,
                'profession', NEW.profession,
                'status', NEW.status,
                'stage', NEW.stage,
                'type', NEW.type,
                'message', CASE WHEN NEW.status = 'done'
                    THEN 'Данные собраны, анализ обновлён'
                    ELSE 'Данные собраны, анализ обновлён с ограничениями' END,
                'degraded_reason', NEW.degraded_reason,
                'nodes_count', (SELECT count(*) FROM nodes WHERE run_id = NEW.id),
                'evidence_count', (SELECT count(*) FROM evidence e
                    JOIN nodes n ON n.id = e.node_id WHERE n.run_id = NEW.id),
                'approved_evidence_count', (SELECT count(*) FROM evidence e
                    JOIN nodes n ON n.id = e.node_id WHERE n.run_id = NEW.id AND e.approved),
                'sources_count', (SELECT count(*) FROM sources WHERE run_id = NEW.id),
                'started_at', NEW.started_at,
                'finished_at', NEW.finished_at,
                'created_at', NEW.created_at
            )
        )
        ON CONFLICT DO NOTHING;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- Возвращаем исходный минимальный контракт события (только done).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_run_completed()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status = 'done'
       AND (TG_OP = 'INSERT' OR OLD.status IS DISTINCT FROM 'done') THEN
        INSERT INTO agent_messages (run_id, message_type, payload)
        VALUES (
            NEW.id,
            'run.completed',
            jsonb_build_object(
                'event', 'run.completed',
                'run_id', NEW.id,
                'profession', NEW.profession,
                'finished_at', NEW.finished_at
            )
        )
        ON CONFLICT DO NOTHING;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
