// Package testdb gives an integration test a PostgreSQL database of its own
// on the server its package already runs.
//
// A test that rolls a migration back drops the schema every other test of its
// package writes into. In the database the package shares it rewinds their
// rows, and passes or fails by which of them ran first; D135 and D141 were two
// such tests, found when a down migration began to refuse on data. A database
// of the test's own has nobody else's rows in it.
//
// It is a package rather than a helper per test file because eleven test files
// had written their own, and four of them left their database behind.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// New creates an empty database on the server sharedDSN points at, drops it
// when the test ends, and returns its address.
//
// The name is prefix and a moment, so a leftover in a server that outlived its
// test says which test left it.
func New(t testing.TB, sharedDSN, prefix string) string {
	t.Helper()

	name := prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	exec(t, sharedDSN, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	t.Cleanup(func() {
		exec(t, sharedDSN, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})

	parsed, err := url.Parse(sharedDSN)
	require.NoError(t, err, "the shared address could not be read")
	parsed.Path = "/" + name

	return parsed.String()
}

// TableExists reports whether a table of that name is in dsn's database, in
// its current schema.
func TableExists(t testing.TB, dsn, table string) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err, "the test database could not be reached")
	defer func() { _ = conn.Close(ctx) }()

	var exists bool
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT EXISTS (
             SELECT 1 FROM pg_class c
             JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE c.relname = $1 AND c.relkind = 'r' AND n.nspname = current_schema()
         )`, table).Scan(&exists))

	return exists
}

// exec runs one statement on a connection of its own.
func exec(t testing.TB, dsn, statement string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err, "the database server could not be reached")
	defer func() { _ = conn.Close(ctx) }()

	_, err = conn.Exec(ctx, statement)
	require.NoError(t, err, fmt.Sprintf("%q failed", statement))
}
