//go:build integration

// The test in this file has the CLUSTER as its subject, not the plugin.
//
// Every other test here keeps its fixture in ASCII, and the reason is written
// beside them: PostgreSQL's lower-casing depends on the database's ctype, so a
// non-ASCII fixture would make a plugin test pass or fail on how the container
// was initialized. That reasoning is right, and it leaves a hole this file
// closes — MEASURED on 2026-09-09, not assumed.
//
// A cluster created with `--locale=C` violates the one hard requirement of
// ADR 0015's contract: it folds ASCII only. gobit notices and WARNS
// (core/db/casefold.go), and then this package, internal/modules/product and
// internal/modules/auth/repository all went GREEN on such a cluster. The
// warning is the only thing that changes. So the suite could not tell a cluster
// that satisfies the contract from one that breaks it, on the very three paths
// the contract exists for.
//
// This test is the one that can. It is ALLOWED to depend on how the container
// was created, because that dependence is its subject.
package searchpg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clusterFoldUpper and clusterFoldLower are the letter pair the cluster is
// asked about.
//
// They are written as escapes rather than as letters: this file is English and
// the language ratchet's diacritic lane reads the source (ADR 0012), so the two
// code points are spelled instead of typed. They are a capital C with cedilla
// (U+00C7) and its small form (U+00E7) — the pair a `--locale=C` cluster leaves
// alone, and the letter a Turkish shop's catalog is full of.
const (
	clusterFoldUpper = "\u00c7"
	clusterFoldLower = "\u00e7"
)

// TestTheIndexFoldsTheClustersNonAsciiCase asks the cluster the contract's
// question through the plugin's own SQL.
//
// `to_tsvector('simple', …)` lower-cases with the cluster's ctype. On a
// compliant cluster the indexed title and the searched term meet; on an
// ASCII-only one they do not, and a shopper typing the small letter finds
// nothing while the row sits in the table.
func TestTheIndexFoldsTheClustersNonAsciiCase(t *testing.T) {
	i := realIndex(t)

	write(t, i, document{
		productID: "prod_fold",
		title:     clusterFoldUpper + "anta",
		keywords:  "fold-case",
		body:      "Leather",
	})

	ids, err := i.Search(t.Context(), clusterFoldLower+"anta", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_fold"}, ids,
		"the index did not fold a non-ASCII capital.\n"+
			"This is the cluster and not the plugin: ADR 0015 requires a ctype that folds "+
			"more than ASCII, and this one does not. gobit's startup probe warns about it "+
			"(core/db/casefold.go) and every other test in this package passes anyway.")
}
