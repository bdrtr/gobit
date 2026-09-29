package returns

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// heldHarness is the dispatch harness after a dispatch that stopped half way:
// the first line's units are set aside under a promise, the second's are not.
func heldHarness(t *testing.T) *harness {
	t.Helper()
	h := dispatchHarness(t)
	h.orders.replacement.Lines[0].ReservationID = "invres_held"
	return h
}

// TestAWithdrawnReplacementGivesBackWhatItHeld is D159 closed (ADR 0237): the
// promise a stopped dispatch left is released, and only then is the record
// withdrawn.
func TestAWithdrawnReplacementGivesBackWhatItHeld(t *testing.T) {
	h := heldHarness(t)

	out, err := h.wf.WithdrawReplacement(context.Background(), testReplacementID)
	require.NoError(t, err)

	assert.Equal(t, []string{"invres_held"}, h.inventory.released, "the held units go back, and only those")
	assert.Equal(t, []string{testReplacementID}, h.orders.canceled)
	assert.Equal(t, 1, out.ReleasedPromises)
}

// TestTheUnitsGoBackBeforeTheRecordIsWithdrawn holds the order: a release that
// fails leaves the record open, so nothing claims to be withdrawn while its
// units are still held.
func TestTheUnitsGoBackBeforeTheRecordIsWithdrawn(t *testing.T) {
	h := heldHarness(t)
	h.inventory.releaseErr = coreerrors.Unavailable("inventory_down", "the database is unreachable")

	_, err := h.wf.WithdrawReplacement(context.Background(), testReplacementID)
	require.Error(t, err)
	assert.Equal(t, CodeStockNotReleased, coreerrors.CodeOf(err))
	assert.Empty(t, h.orders.canceled, "the record stays open while its units are held")
}

// TestUnitsThatLeftWithAParcelAreNotWithdrawn is the one promise that cannot
// go back: a dispatch confirmed it after opening its parcel and died before it
// recorded the parcel. The answer is to dispatch again, and nothing is
// withdrawn.
func TestUnitsThatLeftWithAParcelAreNotWithdrawn(t *testing.T) {
	h := heldHarness(t)
	h.inventory.releaseErr = coreerrors.Conflict("inventory_reservation_confirmed", "confirmed")

	_, err := h.wf.WithdrawReplacement(context.Background(), testReplacementID)
	require.Error(t, err)
	assert.Equal(t, CodeStockNotReleased, coreerrors.CodeOf(err))
	assert.True(t, coreerrors.IsConflict(err))
	assert.Contains(t, err.Error(), "dispatch the replacement again")
	assert.Empty(t, h.orders.canceled)
}

// TestASentReplacementIsNotWithdrawn keeps a dispatched record out of the
// flow's hands before any promise is touched.
func TestASentReplacementIsNotWithdrawn(t *testing.T) {
	h := heldHarness(t)
	h.orders.replacement.Status = statusReplacementDispatched

	_, err := h.wf.WithdrawReplacement(context.Background(), testReplacementID)
	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err))
	assert.Empty(t, h.inventory.released)
	assert.Empty(t, h.orders.canceled)
}

// TestAWithdrawalIsFinishedByAskingAgain is the repeat: the promise released
// and the record written by an earlier attempt, a second one does the same
// again, which the inventory and the order both answer as done.
func TestAWithdrawalIsFinishedByAskingAgain(t *testing.T) {
	h := heldHarness(t)
	h.orders.replacement.Status = "canceled"

	_, err := h.wf.WithdrawReplacement(context.Background(), testReplacementID)
	require.NoError(t, err)
	assert.Equal(t, []string{"invres_held"}, h.inventory.released)
	assert.Equal(t, []string{testReplacementID}, h.orders.canceled)
}
