// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English.
//
// It carries NO build tag and needs NO database, which is the point: the
// property under test is a refusal that happens BEFORE the pool is touched, and
// a check that only ran under `make test-integration` would be one nobody runs
// while they are writing the code it protects.
package repository_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository"
)

// TestAMovementIsRefusedOutsideATransaction is one half of ADR 0068.
//
// The decision keeps inventory_levels.stocked_quantity authoritative and lets
// the ledger EXPLAIN it, which leaves exactly one way for the two to disagree:
// one being written without the other. The repository closes that way by
// refusing an append outside a transaction, exactly as the Lock methods do —
// a movement written on the pool would commit while the level update it
// explains rolled back, and the ledger would report units that never moved.
//
// The repository is built over a NIL pool deliberately. The guard runs before
// any query, so a nil pool proves the refusal is the FIRST thing that happens
// rather than something the database happened to reject; were the guard removed,
// this test would panic instead of passing, which is a failure either way.
func TestAMovementIsRefusedOutsideATransaction(t *testing.T) {
	t.Parallel()

	repo := repository.New(nil)

	_, err := repo.AppendMovement(context.Background(), models.Movement{
		ID:              "invmov_TEST",
		InventoryItemID: "invitem_TEST",
		LocationID:      "sloc_TEST",
		Reason:          models.MovementAdjustment,
		Delta:           1,
		StockedAfter:    1,
	})

	require.Error(t, err,
		"an append outside a transaction has to be refused; on the pool it would "+
			"survive the rollback of the level change it explains")
	assert.True(t, errors.HasKind(err, errors.KindInternal),
		"it is a programming error rather than a caller's mistake: no request can "+
			"produce it, and no retry fixes it")
	assert.Contains(t, err.Error(), "AppendMovement",
		"the message has to name the call, or the reader is left looking for which of "+
			"the transaction-only methods was reached")
}
