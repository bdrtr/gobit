//go:build integration

// The test in this file has the CLUSTER as its subject, not the read layer.
//
// [TestTheReadLayerSearchesTheTitleAgainstTheDatabase] keeps its fixture in
// ASCII and says why: a non-ASCII one would pass or fail on how the container
// was created rather than on the filter dispatch that test covers. The
// reasoning is right, and it leaves a hole this file closes — MEASURED on
// 2026-09-09, not assumed.
//
// A cluster created with `--locale=C` breaks the one hard requirement of
// ADR 0015's contract: it folds ASCII only. gobit notices and WARNS
// (core/db/casefold.go), and then this package, plugins/searchpg and
// internal/modules/auth/repository all went GREEN on such a cluster. So the
// suite could not tell a cluster that satisfies the contract from one that
// breaks it, on the three paths the contract exists for.
//
// This test is the one that can, and it is ALLOWED to depend on how the
// container was created, because that dependence is its subject.
package product_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
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

// TestTheStorefrontSearchFoldsTheClustersNonAsciiCase asks the cluster the
// contract's question through the storefront's own filter.
//
// The stored title and the searched term differ in ONE letter's case, and that
// letter is outside ASCII. `title ILIKE '%' || $q || '%'` folds both sides with
// the cluster's ctype, so on a compliant cluster the two meet and on an
// ASCII-only one they do not — the shopper gets an empty list, the product sits
// in the catalog, and nothing anywhere returns an error.
func TestTheStorefrontSearchFoldsTheClustersNonAsciiCase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	products := service.NewProductProvider(repository.New(testPool.Pool()))

	// The token keeps the row unique per run: the tests share one database and
	// a fixed word would be matched by whatever an earlier test left behind.
	token := uniqueHandle("qfold")
	stored, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("fold-product"),
		Title:  clusterFoldUpper + "anta " + token,
		Status: models.StatusPublished,
	})
	require.NoError(t, err)

	found, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": clusterFoldLower + "anta " + token},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{stored.ID}, providerIDs(t, found),
		"the storefront filter did not fold a non-ASCII capital.\n"+
			"This is the cluster and not the read layer: ADR 0015 requires a ctype that "+
			"folds more than ASCII, and this one does not. gobit's startup probe warns "+
			"about it (core/db/casefold.go) and every other test here passes anyway.")
}
