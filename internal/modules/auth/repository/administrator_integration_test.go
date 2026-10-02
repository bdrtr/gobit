//go:build integration

package repository_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
	"github.com/bdrtr/gobit/internal/testdb"
)

// administratorsOnly is a repository over a database of its own, so the
// administrators in it are the test's alone: the package's shared database
// holds every other test's.
func administratorsOnly(ctx context.Context, t *testing.T) (*repository.Repo, *db.Pool) {
	t.Helper()

	dsn := testdb.New(t, testDSN, "gobit_auth_admins")
	require.NoError(t, db.Migrate(ctx, dsn, auth.New(auth.Options{}).Migrations(), auth.ModuleName))
	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return repository.New(pool.Pool()), pool
}

// newUserWith writes a user holding the scopes.
func newUserWith(ctx context.Context, t *testing.T, repo *repository.Repo, scopes ...string) models.User {
	t.Helper()

	now := time.Now().UTC()
	id := models.NewUserID(now)
	user, err := repo.CreateUser(ctx, models.User{
		ID: id, Email: strings.ToLower("u" + id[len(models.UserIDPrefix):] + "@example.test"),
		Scopes: append([]string{}, scopes...), CreatedAt: now,
	}, nil)
	require.NoError(t, err)

	return user
}

// TestTheLastAdministratorKeepsAdmin is ADR 0346: admin is taken from an
// administrator while another live one is left, a deleted one not counted,
// and refused from the last one, whether by narrowing their scopes or by
// deleting them; a write that keeps admin, or one on a user who never held
// it, is not held to it.
func TestTheLastAdministratorKeepsAdmin(t *testing.T) {
	ctx := context.Background()
	repo, _ := administratorsOnly(ctx, t)
	first := newUserWith(ctx, t, repo, models.ScopeAdmin)
	second := newUserWith(ctx, t, repo, models.ScopeAdmin, "order:read")
	clerk := newUserWith(ctx, t, repo, "order:read")
	spare := newUserWith(ctx, t, repo, models.ScopeAdmin)
	require.NoError(t, repo.DeleteUser(ctx, spare.ID, time.Now()), "a deleted administrator holds admin no more")

	_, err := repo.UpdateUser(ctx, first.ID, models.UserPatch{Scopes: []string{"order:read"}}, time.Now())
	require.NoError(t, err, "another administrator is left")
	_, err = repo.UpdateUser(ctx, second.ID, models.UserPatch{Scopes: []string{"order:read"}}, time.Now())
	require.Error(t, err)
	assert.Equal(t, repository.CodeLastAdministrator, errors.CodeOf(err), "%v", err)
	assert.True(t, errors.IsConflict(err))
	err = repo.DeleteUser(ctx, second.ID, time.Now())
	assert.Equal(t, repository.CodeLastAdministrator, errors.CodeOf(err), "nor is the last one deleted: %v", err)
	stored, err := repo.GetUser(ctx, second.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{models.ScopeAdmin, "order:read"}, stored.Scopes, "the refused write wrote nothing")

	_, err = repo.UpdateUser(ctx, second.ID, models.UserPatch{Scopes: []string{models.ScopeAdmin}}, time.Now())
	require.NoError(t, err, "a write that keeps admin")
	name := "Renamed"
	_, err = repo.UpdateUser(ctx, second.ID, models.UserPatch{FirstName: &name}, time.Now())
	require.NoError(t, err, "a write that leaves the scopes alone")
	_, err = repo.UpdateUser(ctx, clerk.ID, models.UserPatch{Scopes: []string{}}, time.Now())
	require.NoError(t, err, "a user who never held admin")
	require.NoError(t, repo.DeleteUser(ctx, clerk.ID, time.Now()))
	require.NoError(t, repo.DeleteUser(ctx, first.ID, time.Now()), "nor a former administrator")
}

// TestTwoWritesCannotTakeAdminFromTheLastTwo is ADR 0346's lock: while one
// transaction has taken admin from one of two administrators and not yet
// committed, a write taking it from the other waits for it, and is answered
// from the row as it was left, so the second administrator keeps admin.
// Its own write touches only the other row, so only the lock on the
// administrators makes it wait.
func TestTwoWritesCannotTakeAdminFromTheLastTwo(t *testing.T) {
	ctx := context.Background()
	repo, pool := administratorsOnly(ctx, t)
	first := newUserWith(ctx, t, repo, models.ScopeAdmin)
	second := newUserWith(ctx, t, repo, models.ScopeAdmin)

	conn, err := pool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var blocker int32
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker))
	_, err = tx.Exec(ctx, `UPDATE auth_user SET scopes = '{}' WHERE id = $1`, first.ID)
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() {
		_, err := repo.UpdateUser(ctx, second.ID, models.UserPatch{Scopes: []string{}}, time.Now())
		result <- err
	}()
	require.Eventually(t, func() bool {
		var waiting int
		err := pool.Pool().QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			  AND $1 = ANY(pg_blocking_pids(pid))`, blocker).Scan(&waiting)
		return err == nil && waiting > 0
	}, 10*time.Second, 10*time.Millisecond, "the second write had to wait on the first")
	require.NoError(t, tx.Commit(ctx))

	var waited error
	select {
	case waited = <-result:
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting write did not finish in time")
	}
	require.Error(t, waited, "the second administrator is the last one once the first commits")
	assert.Equal(t, repository.CodeLastAdministrator, errors.CodeOf(waited), "%v", waited)
	stored, err := repo.GetUser(ctx, second.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{models.ScopeAdmin}, stored.Scopes, "and keeps admin")
}

// TestAUsersScopesAreWrittenOnlyAsTheyWereRead is ADR 0347 against a real
// PostgreSQL: the scopes are written while they are, as a set, the ones read,
// in whatever order and with whatever repeats; a set that differs by one, a
// deleted user and taking admin from the last administrator write nothing.
func TestAUsersScopesAreWrittenOnlyAsTheyWereRead(t *testing.T) {
	ctx := context.Background()
	repo, _ := administratorsOnly(ctx, t)
	admin := newUserWith(ctx, t, repo, models.ScopeAdmin)
	clerk := newUserWith(ctx, t, repo, "order:read", "order:write")

	revised, written, err := repo.ReviseUserScopes(ctx, clerk.ID,
		[]string{"order:write", "order:read", "order:write"}, []string{"order:read", "customer:read"}, time.Now())
	require.NoError(t, err)
	require.True(t, written, "the set read, in another order")
	assert.Equal(t, []string{"order:read", "customer:read"}, revised.Scopes)
	stored, err := repo.GetUser(ctx, clerk.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"order:read", "customer:read"}, stored.Scopes)
	assert.True(t, stored.UpdatedAt.After(clerk.UpdatedAt), "the moment it was written moves")

	for label, read := range map[string][]string{
		"one more":  {"order:read", "customer:read", "order:write"},
		"one fewer": {"order:read"},
		"none":      {},
	} {
		_, written, err = repo.ReviseUserScopes(ctx, clerk.ID, read, []string{"product:read"}, time.Now())
		require.NoError(t, err, label)
		assert.False(t, written, "%s than they are", label)
	}
	stored, err = repo.GetUser(ctx, clerk.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"order:read", "customer:read"}, stored.Scopes, "nothing was written")

	_, written, err = repo.ReviseUserScopes(ctx, clerk.ID, stored.Scopes, nil, time.Now())
	require.NoError(t, err)
	assert.True(t, written, "no privilege at all, given as nil")
	_, written, err = repo.ReviseUserScopes(ctx, clerk.ID, nil, []string{"order:read"}, time.Now())
	require.NoError(t, err)
	assert.True(t, written, "from no privilege at all, read as nil")

	_, _, err = repo.ReviseUserScopes(ctx, admin.ID, []string{models.ScopeAdmin}, []string{"order:read"}, time.Now())
	assert.Equal(t, repository.CodeLastAdministrator, errors.CodeOf(err), "%v", err)
	require.NoError(t, repo.DeleteUser(ctx, clerk.ID, time.Now()))
	_, written, err = repo.ReviseUserScopes(ctx, clerk.ID, []string{"order:read"}, []string{}, time.Now())
	require.NoError(t, err)
	assert.False(t, written, "a deleted user")
}
