//go:build integration

// The test in this file needs a real PostgreSQL instance (and therefore Docker).
// To run it: make test-integration
package arch_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
)

const postgresImage = "postgres:16-alpine"

// TestMigrationsCanReallyBeRolledBack runs EVERY migration directory in the
// repository up -> down -> up on a REAL PostgreSQL.
//
// Why a separate gate: [TestMigrationsCanBeRolledBack] only checks that the
// .down.sql file EXISTS. In Phase 5 a bug went through exactly that hole — the
// region module's seed rollback stood syntactically but blew up with a foreign key
// violation when run, leaving golang-migrate's version ledger "dirty". Because
// cmd/server calls Migrate per module at every startup, the server NEVER CAME UP
// again from that point on.
//
// # ~~"every module"~~ — the four schemas that come FIRST were never run
//
// Until 2026-09-06 the loop was over [moduleNames] and this gate round-tripped
// sixteen of the repository's twenty-four migration directories. The eight it
// did not touch were the four plugin schemas and the four CORE ones —
// core/audit, core/eventbus/outbox, internal/core/workflow/pgstore,
// internal/core/job/jobpg — and the core four are the ones openApplication
// applies BEFORE any module's, on every startup. The Phase 5 fault recounted
// above is a dirty ledger blocking the boot; a dirty ledger on a core schema
// blocks it one step earlier, and nothing was running those downs. The input is
// now [migrationDirs].
//
// The migration sources are read from THE FILE SYSTEM rather than from the
// owning packages' embed.FSes: that way this test does not have to be updated
// when a module or a plugin is added and the arch package does not have to
// import them.
//
// A LIMIT: this gate does not catch DATA-DEPENDENT rollback failures. The region
// bug above only appeared while a region row was in the table; scenarios like that
// have to be set up in the module's own integration test.
func TestMigrationsCanReallyBeRolledBack(t *testing.T) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_arch"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	t.Cleanup(func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			t.Logf("the postgres container could not be stopped: %v", termErr)
		}
	})
	require.NoError(t, err, "the postgres container could not be started")

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	ran := 0

	for _, set := range migrationDirs(t) {
		entries, globErr := filepath.Glob(filepath.Join(set.dir, "*"+upMigrationSuffix))
		require.NoError(t, globErr)
		if len(entries) == 0 {
			continue
		}
		ran++

		owner := migrationLedgerName(set.owner)

		t.Run(set.owner, func(t *testing.T) {
			src := os.DirFS(set.dir)

			require.NoError(t, db.Migrate(ctx, dsn, src, owner), "the first up failed")

			version, dirty, verErr := db.Version(ctx, dsn, owner)
			require.NoError(t, verErr)
			require.False(t, dirty, "the schema is dirty after the up")
			require.Positive(t, version, "the version stayed zero after the up")

			// Roll all the way back. A down that blows up leaves the version ledger dirty
			// and makes the module permanently unable to come up.
			require.NoError(t, db.MigrateDown(ctx, dsn, src, owner, 0),
				"the down failed — this means the module can NEVER BE migrated again")

			_, dirty, verErr = db.Version(ctx, dsn, owner)
			require.NoError(t, verErr)
			require.False(t, dirty, "the schema stayed dirty after the down")

			// Up again: proves the down really cleaned the schema. A down leaving a
			// leftover table blows up here with "already exists".
			require.NoError(t, db.Migrate(ctx, dsn, src, owner), "the up after the down failed")

			after, dirty, verErr := db.Version(ctx, dsn, owner)
			require.NoError(t, verErr)
			assert.False(t, dirty)
			assert.Equal(t, version, after, "the version changed after the round trip")

			// Let the next owner start on clean ground.
			require.NoError(t, db.MigrateDown(ctx, dsn, src, owner, 0))
		})
	}

	// This test emptying out quietly is MORE EXPENSIVE than the others: the run opens
	// a container, takes minutes and writes "ok" at the end — that is, it both gives
	// the impression of having done something and has run no migration at all. An
	// empty list is not a case to skip but a fault to diagnose.
	require.Positive(t, ran,
		"no migration to run was found anywhere; the check must have gone BLIND — "+
			"the files may have been moved outside the %q directory or the naming may have "+
			"left the \"%s\" pattern.\nA gate that runs no up/down at all on a real "+
			"database cannot catch a down leaving golang-migrate's ledger \"dirty\" — that "+
			"was exactly the fault that made the server unable to come up in Phase 5.",
		migrationsDirName, upMigrationSuffix)
}

// migrationLedgerName turns a migration set's owner path into a name
// [github.com/bdrtr/gobit/core/db.MigrationsTable] accepts.
//
// The owner path is what a reader has to open ("internal/modules/product",
// "core/eventbus/outbox"); the ledger name is what becomes an SQL identifier,
// and db validates it against ^[a-z][a-z0-9_]{0,39}$ precisely so that no
// unvalidated string can reach a table name.
//
// The name produced here is NOT the one the binary uses — the binary passes
// pgstore.MigrationOwner and the module's own Name(). It does not have to be:
// the container this test opens is empty, so the ledger name only has to be
// distinct per set, and holding it against the production constants would mean
// importing the four core packages to compare two strings that the round trip
// itself never depends on. What the round trip proves is the up/down/up, and
// that is a property of the SQL.
func migrationLedgerName(owner string) string {
	var name strings.Builder
	for _, r := range strings.ToLower(owner) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			name.WriteRune(r)
			continue
		}
		name.WriteByte('_')
	}

	// The pattern demands a leading letter and at most forty characters; the
	// tail is the distinguishing half of a path, so a long one is cut from the
	// FRONT rather than the back.
	trimmed := strings.TrimLeft(name.String(), "_0123456789")
	if len(trimmed) > 40 {
		trimmed = strings.TrimLeft(trimmed[len(trimmed)-40:], "_0123456789")
	}

	return trimmed
}
