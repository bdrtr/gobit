//go:build integration

// The test in this file has the CLUSTER as its subject, not the repository.
//
// Every identity module guards its e-mail column with
// `CHECK (email <> ” AND email = lower(email))`, and that guard is the last
// defense behind the fold gobit applies in Go (ADR 0038): it is what would
// catch a direct SQL write, or a future code path that forgot. `lower()` folds
// what the CLUSTER's ctype knows, so on an ASCII-only cluster the guard stops
// guarding exactly where a Turkish address starts — and two rows for one person
// is what the unique index on that column then cannot prevent.
//
// MEASURED on 2026-09-09: on a `--locale=C` cluster this package, together with
// internal/modules/product and plugins/searchpg, went GREEN. gobit warns at
// startup (core/db/casefold.go) and nothing else changes, because every fixture
// in the suite was ASCII. This test is the one that goes red, and it is ALLOWED
// to depend on how the container was created, because that is its subject.
package repository_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clusterFoldUpper is the capital the constraint is asked about.
//
// It is written as an escape rather than as a letter: this file is English and
// the language ratchet's diacritic lane reads the source (ADR 0012), so the
// code point is spelled instead of typed. It is a capital C with cedilla
// (U+00C7), whose small form a `--locale=C` cluster never produces.
const clusterFoldUpper = "\u00c7"

// TestTheEmailGuardRefusesANonAsciiCapitalOnAContractCluster writes past the Go
// fold on purpose.
//
// The repository lower-cases in Go before it ever reaches SQL, so a value that
// arrives through it can never test the constraint. The insert here is raw for
// that reason: it is the shape a direct SQL write has, which is the only shape
// the CHECK exists to catch.
func TestTheEmailGuardRefusesANonAsciiCapitalOnAContractCluster(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO auth_user (id, email) VALUES ($1, $2)`,
		"usr_fold_"+clusterFoldUpper, clusterFoldUpper+"ada@example.com")

	require.Error(t, err,
		"the e-mail guard accepted an unfolded non-ASCII capital.\n"+
			"This is the cluster and not the schema: `email = lower(email)` holds only "+
			"where lower() folds more than ASCII, which ADR 0015 requires and this "+
			"cluster does not do. gobit's startup probe warns about it "+
			"(core/db/casefold.go) and every other test here passes anyway.")
	assert.Contains(t, err.Error(), "auth_user_email_check",
		"the insert was refused by some other rule, so this test says nothing about the fold")
}
