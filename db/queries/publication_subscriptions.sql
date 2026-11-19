-- name: CreatePublicationSubscription :one
INSERT INTO publication_subscriptions (owner_id, topic, source, telegram_chat_id, email)
VALUES (sqlc.arg(owner_id), sqlc.arg(topic), sqlc.arg(source), sqlc.arg(telegram_chat_id), sqlc.arg(email))
RETURNING id;

-- name: ListPublicationSubscriptions :many
SELECT id, topic, source, NULLIF(telegram_chat_id, '')::text AS telegram_chat_id,
       NULLIF(email, '')::text AS email, true AS is_active, last_checked_at, created_at
FROM publication_subscriptions WHERE owner_id=sqlc.arg(owner_id)
ORDER BY created_at DESC, id;

-- name: DeletePublicationSubscription :execrows
DELETE FROM publication_subscriptions
WHERE id=sqlc.arg(id) AND (owner_id=sqlc.arg(owner_id) OR sqlc.arg(is_admin)::boolean);

-- name: ListPublicationPollingTargets :many
SELECT id, topic, source, last_checked_at
FROM publication_subscriptions
WHERE (sqlc.narg(after_id)::uuid IS NULL OR id > sqlc.narg(after_id)::uuid)
ORDER BY id LIMIT sqlc.arg(page_size)::int;

-- name: RecordPublicationUpdates :one
WITH target AS (
    UPDATE publication_subscriptions SET last_checked_at=now()
    WHERE id=sqlc.arg(subscription_id)::uuid
    RETURNING id, owner_id, source
), created AS (
    INSERT INTO publication_notifications (subscription_id, owner_id, title, body, article_url, article_source)
    SELECT target.id, target.owner_id, article->>'title', article->>'abstract', article->>'url', article->>'source'
    FROM target CROSS JOIN LATERAL jsonb_array_elements(sqlc.arg(articles)::jsonb) AS article
    WHERE target.source='openalex+arxiv' OR target.source=article->>'source'
    ON CONFLICT (subscription_id, article_url) DO NOTHING
    RETURNING id
)
SELECT EXISTS (SELECT 1 FROM target) AS found, (SELECT count(*) FROM created) AS created;

-- name: ListPublicationNotifications :many
SELECT id, subscription_id, title, body, article_url, article_source, 'info'::text AS severity, is_read, created_at
FROM publication_notifications
WHERE owner_id=sqlc.arg(owner_id) AND (NOT sqlc.arg(unread_only)::boolean OR NOT is_read)
ORDER BY created_at DESC, id
LIMIT sqlc.arg(page_size)::int;

-- name: MarkPublicationNotificationRead :execrows
UPDATE publication_notifications SET is_read=true
WHERE id=sqlc.arg(id) AND (owner_id=sqlc.arg(owner_id) OR sqlc.arg(is_admin)::boolean);

-- name: CountUnreadPublicationNotifications :one
SELECT count(*) FROM publication_notifications WHERE owner_id=sqlc.arg(owner_id) AND NOT is_read;
