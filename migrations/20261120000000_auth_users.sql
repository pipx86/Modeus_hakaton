-- +goose Up
-- +goose StatementBegin
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS password_hash TEXT NOT NULL DEFAULT '';

INSERT INTO roles (code, name)
VALUES
    ('admin', 'Администратор'),
    ('user', 'Пользователь')
ON CONFLICT (code) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM roles WHERE code IN ('admin', 'user') AND NOT EXISTS (
    SELECT 1 FROM users WHERE users.role_id = roles.id
);
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
-- +goose StatementEnd
