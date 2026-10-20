//go:build integration

package order_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/internal/modules/order"
)

// TestASearchFoldsTheAsciiCapitalIOnATurkishCluster runs the order module's
// search functions on a database whose default collation is ICU's Turkish
// (ADR 0460). There lower('I') is the dotless i, so a name stored as "WILLIS"
// was indexed as "w\u0131ll\u0131s" and no folded q found it. The cluster is one
// ADR 0015's contract admits: UTF-8, any locale.
func TestASearchFoldsTheAsciiCapitalIOnATurkishCluster(t *testing.T) {
	ctx := context.Background()
	_, err := testPool.Pool().Exec(ctx, `DROP DATABASE IF EXISTS gobit_turkish`)
	require.NoError(t, err)
	_, err = testPool.Pool().Exec(ctx, `CREATE DATABASE gobit_turkish TEMPLATE template0
        ENCODING 'UTF8' LOCALE_PROVIDER icu ICU_LOCALE 'tr-TR' LOCALE 'C.UTF-8'`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testPool.Pool().Exec(context.Background(), `DROP DATABASE IF EXISTS gobit_turkish WITH (FORCE)`)
	})

	turkish, err := url.Parse(testDSN)
	require.NoError(t, err)
	turkish.Path = "/gobit_turkish"
	require.NoError(t, db.Migrate(ctx, turkish.String(), order.New().Migrations(), order.ModuleName))
	conn, err := pgx.Connect(ctx, turkish.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	var lowered string
	require.NoError(t, conn.QueryRow(ctx, `SELECT lower('I')`).Scan(&lowered))
	require.Equal(t, "\u0131", lowered, "the cluster lowers as a Turkish one does, or the test proves nothing")

	for _, stored := range []string{"Iris WILLIS", "\u0130ris W\u0131ll\u0131s", "iris willis"} {
		for _, q := range []string{"iris willis", "IRIS WILLIS", "\u0130ris W\u0131ll\u0131s", "willis"} {
			var found bool
			require.NoError(t, conn.QueryRow(ctx,
				`SELECT order_search_document(NULL, $1) @@ order_search_query($2)`, stored, q).Scan(&found))
			assert.True(t, found, "%q is found by %q", stored, q)
		}
	}
}
