//go:build integration

package repository_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// TestTheSessionStatementsKeepEachSessionApart is ADR 0267 on the real schema:
// the listing holds a person's open, unexpired sessions newest first; closing
// one closes it for its owner only and once; closing the others keeps one;
// pruning forgets the expired; and the table refuses a session that ends
// before it begins.
func TestTheSessionStatementsKeepEachSessionApart(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	owner, stranger := newUser(ctx, t, repo), newUser(ctx, t, repo)
	now := time.Now().UTC().Truncate(time.Microsecond)

	session := func(id, userID string, created time.Time, lasts time.Duration) {
		require.NoError(t, repo.InsertSession(ctx, models.Session{
			ID: id, UserID: userID, CreatedAt: created, ExpiresAt: created.Add(lasts),
		}))
	}
	prefix := owner.ID + "_"
	session(prefix+"old", owner.ID, now.Add(-2*time.Hour), time.Hour)
	session(prefix+"a", owner.ID, now.Add(-time.Minute), time.Hour)
	session(prefix+"b", owner.ID, now, time.Hour)
	session(prefix+"c", owner.ID, now.Add(time.Second), time.Hour)
	session(stranger.ID+"_x", stranger.ID, now, time.Hour)

	ids := func() []string {
		listed, err := repo.ListUnclosedSessions(ctx, owner.ID, now)
		require.NoError(t, err)
		var out []string
		for _, s := range listed {
			out = append(out, s.ID)
		}
		return out
	}
	assert.Equal(t, []string{prefix + "c", prefix + "b", prefix + "a"}, ids(),
		"open and unexpired, newest first; the expired one and the stranger's are not listed")

	closed, err := repo.CloseSession(ctx, stranger.ID, prefix+"a", now)
	require.NoError(t, err)
	assert.False(t, closed, "a session closes for its owner only")
	closed, err = repo.CloseSession(ctx, owner.ID, prefix+"a", now)
	require.NoError(t, err)
	assert.True(t, closed)
	closed, err = repo.CloseSession(ctx, owner.ID, prefix+"a", now)
	require.NoError(t, err)
	assert.False(t, closed, "a closed session is not closed twice")
	got, err := repo.GetSession(ctx, prefix+"a")
	require.NoError(t, err)
	require.NotNil(t, got.RevokedAt)

	count, err := repo.CloseOtherSessions(ctx, owner.ID, prefix+"c", now)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "b is the only other open, unexpired session")
	assert.Equal(t, []string{prefix + "c"}, ids())
	foreign, err := repo.GetSession(ctx, stranger.ID+"_x")
	require.NoError(t, err)
	assert.Nil(t, foreign.RevokedAt, "another person's sessions are not touched")

	require.NoError(t, repo.PruneSessions(ctx, owner.ID, now))
	_, err = repo.GetSession(ctx, prefix+"old")
	require.Error(t, err, "an expired session is forgotten at the next sign-in")
	_, err = repo.GetSession(ctx, prefix+"a")
	require.NoError(t, err, "an unexpired one is kept, closed or not")

	err = repo.InsertSession(ctx, models.Session{
		ID: prefix + "backwards", UserID: owner.ID, CreatedAt: now, ExpiresAt: now,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth_session_expires_after_it_begins")
}

// TestASessionKeepsItsBrowser is ADR 0276 over the real schema: the row keeps
// the browser it was opened from, and the schema refuses a description longer
// than a session keeps.
func TestASessionKeepsItsBrowser(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	owner := newUser(ctx, t, repo)
	now := time.Now().UTC().Truncate(time.Microsecond)

	require.NoError(t, repo.InsertSession(ctx, models.Session{
		ID: owner.ID + "_browser", UserID: owner.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		UserAgent: "Mozilla/5.0 Firefox/131.0",
	}))
	listed, err := repo.ListUnclosedSessions(ctx, owner.ID, now)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "Mozilla/5.0 Firefox/131.0", listed[0].UserAgent)

	err = repo.InsertSession(ctx, models.Session{
		ID: owner.ID + "_long", UserID: owner.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		UserAgent: strings.Repeat("a", models.MaxUserAgent+1),
	})
	require.Error(t, err, "the schema bounds the description the service bounds")
	assert.Contains(t, err.Error(), "auth_session_user_agent_bounded")
}
