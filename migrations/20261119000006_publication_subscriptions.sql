-- +goose Up
CREATE TABLE publication_subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id TEXT NOT NULL,
    topic TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('openalex', 'arxiv', 'openalex+arxiv')),
    telegram_chat_id TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    last_checked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX publication_subscriptions_owner_idx ON publication_subscriptions (owner_id, created_at DESC, id);

CREATE TABLE publication_notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id UUID REFERENCES publication_subscriptions(id) ON DELETE SET NULL,
    owner_id TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    article_url TEXT NOT NULL,
    article_source TEXT NOT NULL CHECK (article_source IN ('openalex', 'arxiv')),
    is_read BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (subscription_id, article_url)
);
CREATE INDEX publication_notifications_owner_idx ON publication_notifications (owner_id, created_at DESC, id);
CREATE INDEX publication_notifications_unread_idx ON publication_notifications (owner_id) WHERE NOT is_read;

-- +goose Down
DROP TABLE publication_notifications;
DROP TABLE publication_subscriptions;
