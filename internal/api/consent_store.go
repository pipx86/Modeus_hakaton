package api

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// consentStore — операции с согласием, нужные API. Store не расширяем, чтобы
// не ломать существующие in-memory фейки.
type consentStore interface {
	ConsentStatus(ctx context.Context, userID string) (ConsentState, error)
	AcceptConsent(ctx context.Context, userID, version, ip, userAgent string) (ConsentState, error)
}

var _ consentStore = (*PGStore)(nil)
var _ consentStore = (*memStore)(nil)

// ConsentStatus возвращает текущее согласие пользователя.
func (p *PGStore) ConsentStatus(ctx context.Context, userID string) (ConsentState, error) {
	if !validUUID(userID) {
		return ConsentState{}, ErrNotFound
	}
	var state ConsentState
	var acceptedAt pgtype.Timestamptz
	err := p.Pool.QueryRow(ctx, `
		SELECT COALESCE(consent_version, ''), consent_at
		FROM users
		WHERE id = $1`, userID).Scan(&state.Version, &acceptedAt)
	if err != nil {
		return ConsentState{}, translate(err)
	}
	if acceptedAt.Valid {
		state.AcceptedAt = acceptedAt.Time
	}
	return state, nil
}

// AcceptConsent фиксирует согласие существующего пользователя. Повторный
// вызов с той же версией не создаёт вторую строку и возвращает исходное
// время принятия. Advisory lock сериализует одновременные вызовы для
// пользователя, поэтому дубликат не появится и без уникального индекса.
func (p *PGStore) AcceptConsent(ctx context.Context, userID, version, ip, userAgent string) (ConsentState, error) {
	if !validUUID(userID) {
		return ConsentState{}, ErrNotFound
	}
	version = normalizeConsentVersion(version)
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return ConsentState{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "modeus.consent:"+userID); err != nil {
		return ConsentState{}, translate(err)
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&exists); err != nil {
		return ConsentState{}, translate(err)
	}
	if !exists {
		return ConsentState{}, ErrNotFound
	}

	var state ConsentState
	var acceptedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx, `
		SELECT document_version, accepted_at
		FROM user_consents
		WHERE user_id = $1 AND document_version = $2
		ORDER BY accepted_at DESC
		LIMIT 1`, userID, version).Scan(&state.Version, &acceptedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		state.Version = version
		err = tx.QueryRow(ctx, `
			INSERT INTO user_consents (user_id, document_version, ip, user_agent)
			VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''))
			RETURNING accepted_at`, userID, version, ip, userAgent).Scan(&acceptedAt)
		if err != nil {
			return ConsentState{}, translate(err)
		}
	case err != nil:
		return ConsentState{}, translate(err)
	}
	if acceptedAt.Valid {
		state.AcceptedAt = acceptedAt.Time
	}
	tag, err := tx.Exec(ctx, `
		UPDATE users
		SET consent_version = $2, consent_at = $3, updated_at = now()
		WHERE id = $1`, userID, state.Version, state.AcceptedAt)
	if err != nil {
		return ConsentState{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ConsentState{}, ErrNotFound
	}
	if err = tx.Commit(ctx); err != nil {
		return ConsentState{}, err
	}
	return state, nil
}

type consentEntry struct {
	DocumentVersion string
	AcceptedAt      time.Time
	IP              string
	UserAgent       string
}

func (m *memStore) ConsentStatus(_ context.Context, userID string) (ConsentState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.users[userID]; !ok {
		return ConsentState{}, ErrNotFound
	}
	entries := m.consents[userID]
	if len(entries) == 0 {
		return ConsentState{}, nil
	}
	last := entries[len(entries)-1]
	return ConsentState{Version: last.DocumentVersion, AcceptedAt: last.AcceptedAt}, nil
}

func (m *memStore) AcceptConsent(_ context.Context, userID, version, ip, userAgent string) (ConsentState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return ConsentState{}, ErrNotFound
	}
	for _, entry := range m.consents[userID] {
		if entry.DocumentVersion == version {
			return ConsentState{Version: entry.DocumentVersion, AcceptedAt: entry.AcceptedAt}, nil
		}
	}
	entry := consentEntry{
		DocumentVersion: version,
		AcceptedAt:      time.Now().UTC(),
		IP:              ip,
		UserAgent:       userAgent,
	}
	m.consents[userID] = append(m.consents[userID], entry)
	return ConsentState{Version: entry.DocumentVersion, AcceptedAt: entry.AcceptedAt}, nil
}

// appendConsentLocked вызывается из регистрации: пользователь и его согласие
// создаются одной операцией над memStore.
func (m *memStore) appendConsentLocked(userID string, consent ConsentAcceptance) {
	m.consents[userID] = append(m.consents[userID], consentEntry{
		DocumentVersion: consent.Version,
		AcceptedAt:      time.Now().UTC(),
		IP:              consent.IP,
		UserAgent:       consent.UserAgent,
	})
}
