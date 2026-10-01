//go:build integration

package promotion_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/testdb"
)

// TestTheLatestUsesHaveTheirIndex is ADR 0313's schema: migration 000005
// puts an index on (promotion_id, id) in place of the one on promotion_id
// alone, and rolling it back restores the old one. The plans the index buys
// are measured, not asserted; what is held here is that the index is there.
func TestTheLatestUsesHaveTheirIndex(t *testing.T) {
	ctx := context.Background()
	src := promotion.New(nil).Migrations()
	// Rolled back in a database of its own, as the package's other rollback is (D141).
	dsn := testdb.New(t, testDSN, "promotion_latest_uses_index")
	require.NoError(t, db.Migrate(ctx, dsn, src, promotion.ModuleName))

	indexes := func() map[string]string {
		t.Helper()

		conn, err := pgx.Connect(ctx, dsn)
		require.NoError(t, err)
		defer func() { _ = conn.Close(ctx) }()
		rows, err := conn.Query(ctx,
			`SELECT indexname, indexdef FROM pg_indexes WHERE tablename = 'promotion_redemption'`)
		require.NoError(t, err)
		out := map[string]string{}
		for rows.Next() {
			var name, def string
			require.NoError(t, rows.Scan(&name, &def))
			out[name] = def
		}
		require.NoError(t, rows.Err())

		return out
	}

	after := indexes()
	require.Contains(t, after, "promotion_redemption_promotion_id_idx")
	assert.Contains(t, after["promotion_redemption_promotion_id_idx"], "(promotion_id, id)")
	assert.NotContains(t, after, "promotion_redemption_promotion_idx", "the new index replaces the old one")

	require.NoError(t, db.MigrateDown(ctx, dsn, src, promotion.ModuleName, 1))
	before := indexes()
	require.Contains(t, before, "promotion_redemption_promotion_idx")
	assert.Contains(t, before["promotion_redemption_promotion_idx"], "(promotion_id)")
	assert.NotContains(t, before, "promotion_redemption_promotion_id_idx")
}
