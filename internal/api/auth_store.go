package api

import (
	"context"
	"errors"
	"fmt"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrEmailTaken = errors.New("EMAIL_TAKEN")

// authUserStore — операции с учётными записями, нужные регистрации/логину.
// Store не расширяем, чтобы не ломать существующие in-memory фейки.
type authUserStore interface {
	RegisterUser(ctx context.Context, email, displayName, passwordHash string) (*domain.User, error)
	UserByEmail(ctx context.Context, email string) (*domain.User, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
}

var _ authUserStore = (*PGStore)(nil)
var _ authUserStore = (*memStore)(nil)

func (p *PGStore) RegisterUser(ctx context.Context, email, displayName, passwordHash string) (*domain.User, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL READ COMMITTED`); err != nil {
		return nil, err
	}
	// Параллельные регистрации получают роль последовательно, включая запуск нескольких экземпляров API.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('modeus.auth.register'))`); err != nil {
		return nil, err
	}

	role := auth.RoleUser
	var hasRegisteredUsers bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE password_hash <> '')`).Scan(&hasRegisteredUsers); err != nil {
		return nil, err
	}
	if !hasRegisteredUsers {
		role = auth.RoleAdmin
	}
	user := domain.User{Role: string(role)}
	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, display_name, role_id, password_hash)
		SELECT $1, $2, r.id, $3 FROM roles r WHERE r.code = $4
		RETURNING id::text, email, display_name`,
		email, displayName, passwordHash, string(role),
	).Scan(&user.ID, &user.Email, &user.DisplayName)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrEmailTaken
		}
		return nil, translate(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &user, nil
}

func (p *PGStore) UserByEmail(_ context.Context, email string) (*domain.User, error) {
	ctx, cancel := operation()
	defer cancel()
	var user domain.User
	err := p.Pool.QueryRow(ctx, `
		SELECT u.id::text, u.email, u.display_name, COALESCE(r.code, 'user'), u.password_hash
		FROM users u LEFT JOIN roles r ON r.id = u.role_id
		WHERE lower(u.email) = lower($1)`,
		email,
	).Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role, &user.PasswordHash)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, translate(err)
	}
	return &user, nil
}

func (p *PGStore) ExistingUsers(_ context.Context) ([]domain.User, error) {
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx, `
		SELECT u.id::text, u.email, u.display_name, COALESCE(r.code, 'user')
		FROM users u LEFT JOIN roles r ON r.id = u.role_id
		ORDER BY u.created_at, u.email`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	var users []domain.User
	for rows.Next() {
		var user domain.User
		if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role); err != nil {
			return nil, translate(err)
		}
		users = append(users, user)
	}
	return users, translate(rows.Err())
}

// ListUsers is the public interface name used by the admin endpoint.
func (p *PGStore) ListUsers(ctx context.Context) ([]domain.User, error) {
	return p.ExistingUsers(ctx)
}

func (m *memStore) ListUsers(_ context.Context) ([]domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	users := make([]domain.User, 0, len(m.users))
	for _, user := range m.users {
		user.PasswordHash = ""
		users = append(users, user)
	}
	return users, nil
}

func (m *memStore) RegisterUser(_ context.Context, email, displayName, passwordHash string) (*domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.users {
		if existing.Email == email {
			return nil, ErrEmailTaken
		}
	}
	role := auth.RoleUser
	if len(m.users) == 0 {
		role = auth.RoleAdmin
	}
	m.seq++
	user := domain.User{
		ID:           fmt.Sprintf("user-%d", m.seq),
		Email:        email,
		DisplayName:  displayName,
		Role:         string(role),
		PasswordHash: passwordHash,
	}
	m.users[user.ID] = user
	return &user, nil
}

func (m *memStore) UserByEmail(_ context.Context, email string) (*domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, user := range m.users {
		if user.Email == email {
			return &user, nil
		}
	}
	return nil, ErrNotFound
}
