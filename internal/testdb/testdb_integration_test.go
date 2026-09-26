//go:build integration

package testdb_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/testdb"
)

// currentDatabase is the name of the database dsn connects to.
func currentDatabase(ctx context.Context, t *testing.T, dsn string) string {
	t.Helper()

	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	var name string
	require.NoError(t, conn.QueryRow(ctx, `SELECT current_database()`).Scan(&name))

	return name
}

// TestADatabaseOfTheTestsOwn is the helper's promise: an address on the same
// server naming another database, a table made there not seen in the shared
// one, and nothing left behind when the test ends.
func TestADatabaseOfTheTestsOwn(t *testing.T) {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("gobit_testdb"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })
	require.NoError(t, err)
	shared, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	var own string
	t.Run("the test", func(t *testing.T) {
		dsn := testdb.New(t, shared, "probe")
		own = currentDatabase(ctx, t, dsn)
		assert.True(t, strings.HasPrefix(own, "probe_"), "the name says which test made it: %s", own)
		assert.NotEqual(t, "gobit_testdb", own)

		conn, err := pgx.Connect(ctx, dsn)
		require.NoError(t, err)
		_, err = conn.Exec(ctx, `CREATE TABLE only_here (id int)`)
		require.NoError(t, err)
		require.NoError(t, conn.Close(ctx))

		assert.True(t, testdb.TableExists(t, dsn, "only_here"))
		assert.False(t, testdb.TableExists(t, shared, "only_here"), "the shared database saw the test's table")
	})

	conn, err := pgx.Connect(ctx, shared)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	var left int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, own).Scan(&left))
	assert.Zero(t, left, "the test's database outlived the test")
}
