//go:build integration

// The test in this file needs a real PostgreSQL instance (and therefore Docker).
// To run it: make test-integration
package arch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
)

const postgresImage = "postgres:16-alpine"

// TestMigrationsCanReallyBeRolledBack runs every module's migrations up -> down
// -> up on a REAL PostgreSQL.
//
// Why a separate gate: [TestMigrationsCanBeRolledBack] only checks that the
// .down.sql file EXISTS. In Phase 5 a bug went through exactly that hole — the
// region module's seed rollback stood syntactically but blew up with a foreign key
// violation when run, leaving golang-migrate's version ledger "dirty". Because
// cmd/server calls Migrate per module at every startup, the server NEVER CAME UP
// again from that point on.
//
// The migration sources are read from THE FILE SYSTEM rather than from the
// modules' embed.FSes: that way this test does not have to be updated when a new
// module is added and the arch package does not have to import the modules.
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

	for _, mod := range moduleNames(t) {
		migDir := filepath.Join(repoRoot, modulesDir, mod, migrationsDirName)
		entries, globErr := filepath.Glob(filepath.Join(migDir, "*.up.sql"))
		require.NoError(t, globErr)
		if len(entries) == 0 {
			continue
		}
		ran++

		t.Run(mod, func(t *testing.T) {
			src := os.DirFS(migDir)

			require.NoError(t, db.Migrate(ctx, dsn, src, mod), "the first up failed")

			version, dirty, verErr := db.Version(ctx, dsn, mod)
			require.NoError(t, verErr)
			require.False(t, dirty, "the schema is dirty after the up")
			require.Positive(t, version, "the version stayed zero after the up")

			// Roll all the way back. A down that blows up leaves the version ledger dirty
			// and makes the module permanently unable to come up.
			require.NoError(t, db.MigrateDown(ctx, dsn, src, mod, 0),
				"the down failed — this means the module can NEVER BE migrated again")

			_, dirty, verErr = db.Version(ctx, dsn, mod)
			require.NoError(t, verErr)
			require.False(t, dirty, "the schema stayed dirty after the down")

			// Up again: proves the down really cleaned the schema. A down leaving a
			// leftover table blows up here with "already exists".
			require.NoError(t, db.Migrate(ctx, dsn, src, mod), "the up after the down failed")

			after, dirty, verErr := db.Version(ctx, dsn, mod)
			require.NoError(t, verErr)
			assert.False(t, dirty)
			assert.Equal(t, version, after, "the version changed after the round trip")

			// Let the next module start on clean ground.
			require.NoError(t, db.MigrateDown(ctx, dsn, src, mod, 0))
		})
	}

	// This test emptying out quietly is MORE EXPENSIVE than the others: the run opens
	// a container, takes minutes and writes "ok" at the end — that is, it both gives
	// the impression of having done something and has run no migration at all. An
	// empty list is not a case to skip but a fault to diagnose.
	require.Positive(t, ran,
		"no migration to run was found in any module; the check must have gone BLIND — "+
			"the files may have been moved outside the %q directory or the naming may have "+
			"left the \".up.sql\" pattern.\nA gate that runs no up/down at all on a real "+
			"database cannot catch a down leaving golang-migrate's ledger \"dirty\" — that "+
			"was exactly the fault that made the server unable to come up in Phase 5.", migrationsDirName)
}
