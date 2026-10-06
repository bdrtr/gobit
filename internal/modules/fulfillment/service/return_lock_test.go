package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// The tests here are gap D265 for a parcel bringing a return back (ADR 0420):
// it takes its order's dispatch lock, and what the return's live parcels hold is
// counted under it. A parcel that another open committed while this one waited
// for the lock is put in place by the store's onLock hook.

// TestAReturnParcelWaitsForTheOrdersLock is the race: the return names two
// units, and while this open waited for the order's lock another one committed
// a parcel of both. Counted under the lock, nothing is left, so the open is
// refused and the carrier is not asked.
func TestAReturnParcelWaitsForTheOrdersLock(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 2}
	setup.store.onLock = func(f *fakeStore) {
		f.onLock = nil
		f.putLiveReturnParcel("ful_first", "order_1", "ret_A", "oli_1", 2)
	}

	_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-second", 2))

	require.Error(t, err, "the units were taken while the open waited")
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Equal(t, service.CodeLineNotDispatchable, coreerrors.CodeOf(err))
	assert.Contains(t, setup.store.locks, "dispatch:order_1", "the order's lock was taken")
	assert.Empty(t, setup.provider.createInputs, "no label was printed")
}

// TestAReturnParcelMayTakeExactlyWhatItsReturnLeaves is the boundary under the
// lock: one of two units is on its way back, so one more fits and two do not.
func TestAReturnParcelMayTakeExactlyWhatItsReturnLeaves(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		asked int64
		fits  bool
	}{
		{name: "exactly what is left", asked: 1, fits: true},
		{name: "one more", asked: 2, fits: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := newSetup(t)
			optionID := returnOption(t, setup)
			setup.bound.returnLines = map[string]int64{"oli_1": 2}
			setup.store.onLock = func(f *fakeStore) {
				f.onLock = nil
				f.putLiveReturnParcel("ful_first", "order_1", "ret_A", "oli_1", 1)
			}

			_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-next", tc.asked))

			if tc.fits {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, service.CodeLineNotDispatchable, coreerrors.CodeOf(err))
		})
	}
}

// TestAnotherReturnsParcelLeavesThisReturnWhole counts only the return's own
// parcels: a parcel bringing another return of the same order back holds that
// return's units, not this one's.
func TestAnotherReturnsParcelLeavesThisReturnWhole(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 2}
	setup.store.onLock = func(f *fakeStore) {
		f.onLock = nil
		f.putLiveReturnParcel("ful_other", "order_1", "ret_B", "oli_1", 2)
	}

	_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-own", 2))

	require.NoError(t, err, "another return's parcel does not count against this return")
}

// TestTheHeldCountRefusesAtOnceWhileTheLockIsHeld is the cancellation's read
// while a parcel of the order is being opened (ADR 0420): the lock is not
// waited for, the answer is a fault that passes, and nothing is counted.
func TestTheHeldCountRefusesAtOnceWhileTheLockIsHeld(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.store.putLiveParcel("ful_before", "order_1", "li_a", 1)
	setup.store.busy = map[string]bool{"order_1": true}

	held, err := setup.svc.HeldForReferenceLocked(context.Background(), "order_1")

	require.Error(t, err, "a held lock is not waited for")
	assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err), "the refusal is one a retry may pass")
	assert.Equal(t, service.CodeDispatchBusy, coreerrors.CodeOf(err))
	assert.Nil(t, held)
	assert.Equal(t, []string{"busy:order_1"}, setup.store.locks, "the lock was only tried")
}

// TestTheHeldCountIsReadUnderTheOrdersLock is the cancellation's read
// (ADR 0420): it takes the order's lock in a transaction of its own and counts,
// by reference, a parcel that committed before the lock was taken.
func TestTheHeldCountIsReadUnderTheOrdersLock(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.store.putLiveParcel("ful_before", "order_1", "li_a", 1)
	setup.store.onLock = func(f *fakeStore) {
		f.onLock = nil
		f.putLiveParcel("ful_meanwhile", "order_1", "li_a", 2)
	}

	held, err := setup.svc.HeldForReferenceLocked(context.Background(), " order_1 ")

	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 3}, held, "the parcel committed while it waited is counted")
	assert.Equal(t, []string{"dispatch:order_1"}, setup.store.locks)
}
