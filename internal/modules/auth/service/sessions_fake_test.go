package service_test

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// memSessions keeps the session rows the way the table does (ADR 0267); the
// fakes embed it so a sign-in writes a row and a request reads it.
type memSessions struct {
	mu   sync.Mutex
	rows map[string]models.Session
}

func (m *memSessions) InsertSession(_ context.Context, session models.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]models.Session{}
	}
	m.rows[session.ID] = session
	return nil
}

func (m *memSessions) GetSession(_ context.Context, id string) (models.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.rows[id]
	if !ok {
		return models.Session{}, errors.NotFound("test_session_missing", "no session %s", id)
	}
	return session, nil
}

func (m *memSessions) ListUnclosedSessions(_ context.Context, userID string, now time.Time) ([]models.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []models.Session
	for _, session := range m.rows {
		if session.UserID == userID && session.RevokedAt == nil && session.ExpiresAt.After(now) {
			out = append(out, session)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *memSessions) CloseSession(_ context.Context, userID, id string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.rows[id]
	if !ok || session.UserID != userID || session.RevokedAt != nil {
		return false, nil
	}
	session.RevokedAt = &now
	m.rows[id] = session
	return true, nil
}

func (m *memSessions) CloseOtherSessions(_ context.Context, userID, keep string, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var closed int64
	for id, session := range m.rows {
		if session.UserID == userID && id != keep && session.RevokedAt == nil && session.ExpiresAt.After(now) {
			session.RevokedAt = &now
			m.rows[id] = session
			closed++
		}
	}
	return closed, nil
}

func (m *memSessions) PruneSessions(_ context.Context, userID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, session := range m.rows {
		if session.UserID == userID && !session.ExpiresAt.After(now) {
			delete(m.rows, id)
		}
	}
	return nil
}
