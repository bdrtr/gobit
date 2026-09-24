//go:build integration

package db_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
)

// isolationRefusedCode is the code a refused session carries. It is repeated
// here rather than exported: an operator reads it in a log line, and a string
// the package would have to keep forever is not worth one assertion.
const isolationRefusedCode = "db_isolation_unsupported"

// withDefaultIsolation is the test DSN with every session's default isolation
// set by the connection itself, the way an operator's DSN would set it.
func withDefaultIsolation(level string) string {
	return testDSN + "&default_transaction_isolation=" + url.QueryEscape(level)
}

// TestAPoolRefusesADatabaseThatStartsTransactionsAtAnotherLevel is ADR 0166 at
// startup: a pool whose sessions would start at any level but READ COMMITTED is
// not built.
//
// The error has to say what it is. The refusal happens inside the pool's first
// connection, and the old path wrapped every first-connection failure as
// "the database is unreachable" — which would send an operator to the network
// for a database that answered perfectly well at the wrong level.
func TestAPoolRefusesADatabaseThatStartsTransactionsAtAnotherLevel(t *testing.T) {
	t.Parallel()

	for _, level := range []string{"repeatable read", "serializable"} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()

			pool, err := db.New(context.Background(), db.DefaultConfig(withDefaultIsolation(level)), nil)
			require.Error(t, err, "a pool at %q must not be built", level)
			assert.Nil(t, pool)
			assert.Equal(t, isolationRefusedCode, errors.CodeOf(err),
				"the refusal must carry its own code, not the unreachable one: %v", err)
			assert.Contains(t, err.Error(), level, "the message has to name the level it found")
			assert.Contains(t, err.Error(), "ALTER ROLE", "and the fix")
		})
	}

	t.Run("read committed, named", func(t *testing.T) {
		t.Parallel()

		pool, err := db.New(context.Background(),
			db.DefaultConfig(withDefaultIsolation("read committed")), nil)
		require.NoError(t, err, "the one level the repository is written for is accepted")
		pool.Close()
	})
}

// TestAConnectionOpenedAfterTheDefaultChangedIsRefused is the half a startup
// check would miss.
//
// The default is read when a session starts, and a pool starts sessions for as
// long as it runs. A database whose default is changed under a running process
// reaches it through the next connection the pool opens, so the check is on
// every connection: the change becomes refused connections — loud — instead of
// a quiet second spend. Setting the default back is enough to recover, with no
// restart, because the next connection is checked again.
//
// It runs in a database of its own, because what it changes is the database's
// default and the other tests in this package share one.
func TestAConnectionOpenedAfterTheDefaultChangedIsRefused(t *testing.T) {
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, testDSN)
	require.NoError(t, err)
	t.Cleanup(admin.Close)

	const database = "isolation_drift"
	_, err = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+database)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `CREATE DATABASE `+database)
	require.NoError(t, err)

	target, err := url.Parse(testDSN)
	require.NoError(t, err)
	target.Path = "/" + database

	pool, err := db.New(ctx, db.DefaultConfig(target.String()), nil)
	require.NoError(t, err, "the database starts at the server's default, READ COMMITTED")
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+database+` WITH (FORCE)`)
	})

	_, err = admin.Exec(ctx,
		`ALTER DATABASE `+database+` SET default_transaction_isolation = 'repeatable read'`)
	require.NoError(t, err)

	// Every session the pool holds was opened before the change; closing them
	// makes the next query open a new one, which is what a pool does on its own
	// as connections age out.
	pool.Pool().Reset()

	_, err = pool.Pool().Exec(ctx, `SELECT 1`)
	require.Error(t, err, "a session opened after the default changed must be refused")
	assert.Equal(t, isolationRefusedCode, errors.CodeOf(err), "%v", err)
	assert.True(t, strings.Contains(err.Error(), "repeatable read"), "%v", err)

	_, err = admin.Exec(ctx, `ALTER DATABASE `+database+` RESET default_transaction_isolation`)
	require.NoError(t, err)
	pool.Pool().Reset()

	_, err = pool.Pool().Exec(ctx, `SELECT 1`)
	assert.NoError(t, err, "setting the default back is enough; the next session is checked again")
}
