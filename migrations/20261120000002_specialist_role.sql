-- +goose Up
-- +goose StatementBegin
INSERT INTO roles (code, name)
VALUES ('specialist', 'Специалист')
ON CONFLICT (code) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM roles WHERE code = 'specialist' AND NOT EXISTS (
    SELECT 1 FROM users WHERE users.role_id = roles.id
);
-- +goose StatementEnd
