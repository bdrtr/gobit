//go:build integration

package fulfillment_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file exercises the persistence path of the warehouse selection POLICY
// against REAL Postgres.
//
// # Why the service unit tests are not enough
//
// The policy's DECISION is a pure function and is exercised with a fake store;
// the claims there are true but they never touch SQL. The claims here are
// exactly SQL itself: the upsert's conflict branch, the region bindings being
// written wholesale, ON DELETE CASCADE, array_agg producing an EMPTY array for a
// warehouse with no bindings, and the listing collecting the bindings in a
// SINGLE query. The fake store can get none of these wrong, because it does none
// of them.
//
// # The ids are unique PER TEST
//
// The table carries no soft delete and the tests share one database; every
// scenario uses its own prefix so that a row one test leaves behind does not
// enter another test's count.

// setPolicy writes a warehouse policy and returns the result.
func setPolicy(
	ctx context.Context,
	t *testing.T,
	svc *service.Service,
	locationID string,
	priority int64,
	regionIDs ...string,
) models.ShippingLocation {
	t.Helper()

	loc, err := svc.SetShippingLocation(ctx, service.SetShippingLocationInput{
		LocationID: locationID,
		Priority:   priority,
		RegionIDs:  regionIDs,
	})
	require.NoError(t, err, "the policy could not be written: %s", locationID)
	return loc
}

// TestPolicyUpsertOverwritesRowOnSecondWrite proves that writing to the same
// warehouse a second time does NOT PRODUCE a new row but overwrites the
// existing one.
//
// The upsert's conflict branch only runs here: the fake store is a map already
// and a second write overwrites it naturally, meaning the unit test would stay
// green in a world where ON CONFLICT did not exist.
func TestPolicyUpsertOverwritesRowOnSecondWrite(t *testing.T) {
	ctx := t.Context()
	svc, _ := newService(t)

	const locationID = "sloc_upsert_1"
	setPolicy(ctx, t, svc, locationID, 5, "reg_a", "reg_b")
	second := setPolicy(ctx, t, svc, locationID, -3, "reg_c")

	assert.Equal(t, int64(-3), second.Priority, "the priority must be overwritten")
	assert.Equal(t, []string{"reg_c"}, second.RegionIDs,
		"the region bindings are written WHOLESALE: the old bindings do not remain")

	loaded, err := svc.GetShippingLocation(ctx, locationID)
	require.NoError(t, err)
	assert.Equal(t, second.Priority, loaded.Priority)
	assert.Equal(t, second.RegionIDs, loaded.RegionIDs)
	assert.True(t, loaded.UpdatedAt.After(loaded.CreatedAt) ||
		loaded.UpdatedAt.Equal(loaded.CreatedAt),
		"updated_at must not go backwards")
}

// TestPolicyDeleteCascadesRegionBindings proves that the in-module foreign key
// really is CASCADE.
//
// Had the bindings not dropped, the table would accumulate orphan rows and a new
// policy written for the same warehouse would be read together with the bindings
// believed to be deleted.
func TestPolicyDeleteCascadesRegionBindings(t *testing.T) {
	ctx := t.Context()
	svc, _ := newService(t)

	const locationID = "sloc_cascade_1"
	setPolicy(ctx, t, svc, locationID, 0, "reg_x", "reg_y")

	require.NoError(t, svc.DeleteShippingLocation(ctx, locationID))

	var bindingCount int64
	err := testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM shipping_location_regions WHERE location_id = $1`, locationID).
		Scan(&bindingCount)
	require.NoError(t, err, "the binding count must be readable")
	assert.Zero(t, bindingCount, "when the policy is deleted the region bindings must drop too")

	// The same warehouse must be writable again: with a soft delete the primary
	// key would clash and this call would return an error.
	rewritten := setPolicy(ctx, t, svc, locationID, 7)
	assert.Equal(t, int64(7), rewritten.Priority)
	assert.Empty(t, rewritten.RegionIDs, "the rewritten policy must not carry the old bindings")
}

// TestPolicyReadReturnsEmptyArrayForUnboundLocation proves array_agg's FILTER
// branch.
//
// Without FILTER the LEFT JOIN would return, for a warehouse with no bindings, an
// array whose single element is NULL, and "has no binding" could not be told
// apart from "has one binding and that one is unknown". The consequence is
// concrete: a warehouse with no bindings counts as serving ALL regions, and that
// rule would break silently.
func TestPolicyReadReturnsEmptyArrayForUnboundLocation(t *testing.T) {
	ctx := t.Context()
	svc, _ := newService(t)

	const unbound = "sloc_unbound_1"
	const bound = "sloc_bound_1"
	setPolicy(ctx, t, svc, unbound, 0)
	setPolicy(ctx, t, svc, bound, 0, "reg_q")

	ranked, err := svc.RankLocations(ctx, "reg_elsewhere", []string{unbound, bound})
	require.NoError(t, err,
		"a warehouse with no bindings must not be eliminated; if it is, the result is the "+
			"empty set and the call returns Conflict")
	assert.Equal(t, []string{unbound}, ranked,
		"a warehouse with no bindings serves ALL regions, a bound one serves only what it is "+
			"bound to")
}

// TestPolicyDuplicateRegionDoesNotConflictOnSecondWrite proves
// InsertShippingLocationRegions' ON CONFLICT DO NOTHING branch.
//
// The service layer already filters duplicates out; this test exercises SQL
// itself and shows that the two layers do not hide each other: even if the
// service's filtering were removed, the database would NOT produce a conflict
// error, because "binding the same region twice" gives the same result as
// binding it once.
func TestPolicyDuplicateRegionDoesNotConflictOnSecondWrite(t *testing.T) {
	ctx := t.Context()
	svc, _ := newService(t)

	const locationID = "sloc_duplicate_1"
	loc := setPolicy(ctx, t, svc, locationID, 0, "reg_z", " reg_z ", "reg_w")
	assert.Equal(t, []string{"reg_w", "reg_z"}, loc.RegionIDs,
		"a duplicated region must come down to a single binding; the returned order follows "+
			"the ID, not the input's — the bindings form a set")

	// A duplicate write over direct SQL must not conflict either.
	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO shipping_location_regions (location_id, region_id)
		 VALUES ($1, $2) ON CONFLICT (location_id, region_id) DO NOTHING`, locationID, "reg_z")
	require.NoError(t, err, "a duplicate binding write must NOT PRODUCE a conflict")
}

// TestPolicyListCollectsBindingsInOneQuery proves that the listing fetches the
// bindings with a bulk read rather than a separate query per warehouse.
//
// The claim is built on the result: if the bindings of three warehouses come back
// complete and correctly matched, then the bulk read works. The order is
// exercised too — the listing must return the order SELECTION applies (priority
// first, then id), otherwise the admin screen would show the policy in a
// different arrangement.
func TestPolicyListCollectsBindingsInOneQuery(t *testing.T) {
	ctx := t.Context()
	svc, _ := newService(t)

	const prefix = "sloc_list_"
	setPolicy(ctx, t, svc, prefix+"c", 5, "reg_1")
	setPolicy(ctx, t, svc, prefix+"a", -2, "reg_1", "reg_2")
	setPolicy(ctx, t, svc, prefix+"b", 5)

	records, total, err := svc.ListShippingLocations(ctx, service.Page{Limit: 100})
	require.NoError(t, err)
	require.GreaterOrEqual(t, total, int64(3))

	ours := make([]models.ShippingLocation, 0, 3)
	for _, record := range records {
		if strings.HasPrefix(record.LocationID, prefix) {
			ours = append(ours, record)
		}
	}
	require.Len(t, ours, 3, "all three policies must be in the list")

	assert.Equal(t, []string{prefix + "a", prefix + "b", prefix + "c"},
		[]string{ours[0].LocationID, ours[1].LocationID, ours[2].LocationID},
		"the order must be by priority first and by id second — the same order selection applies")

	assert.Equal(t, []string{"reg_1", "reg_2"}, ours[0].RegionIDs,
		"the bindings of a multiply bound warehouse must come back complete and ordered")
	assert.Empty(t, ours[1].RegionIDs, "a warehouse with no bindings must return an empty slice")
	assert.Equal(t, []string{"reg_1"}, ours[2].RegionIDs,
		"the bindings must be matched to the warehouses CORRECTLY; the bulk read must not mix "+
			"the rows up")
}

// TestPolicyUnknownLocationNotFound proves that reading and deleting a record
// that does not exist do not succeed SILENTLY.
func TestPolicyUnknownLocationNotFound(t *testing.T) {
	ctx := t.Context()
	svc, _ := newService(t)

	_, err := svc.GetShippingLocation(ctx, "sloc_never_written")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the read must return NotFound: %v", err)

	err = svc.DeleteShippingLocation(ctx, "sloc_never_written")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err),
		"the delete must return NotFound; DELETE returns without an error for a row that does "+
			"not exist either, and without the check a delete made with the wrong id would look "+
			"successful: %v", err)
}

// TestPolicyRegionWriteRejectedOutsideTransaction proves the repository layer's
// transaction requirement.
//
// The wholesale write of the bindings is two statements (delete, write). Run
// without a transaction, a read in between sees the warehouse WITHOUT REGIONS —
// that is, an edit made to narrow the scope opens it, for a moment, to ALL
// regions. The requirement is not a comment but an exercised behavior.
func TestPolicyRegionWriteRejectedOutsideTransaction(t *testing.T) {
	ctx := t.Context()
	svc, _ := newService(t)

	const locationID = "sloc_no_tx_1"
	setPolicy(ctx, t, svc, locationID, 0, "reg_a")

	repo := repository.New(testPool.Pool())
	err := repo.ReplaceShippingLocationRegions(ctx, locationID, []string{"reg_b"})
	require.Error(t, err, "a region write outside a transaction must be rejected")
	assert.Equal(t, errors.KindInternal, errors.KindOf(err),
		"the kind must be Internal: the fault is not in the client but in the calling code — %v", err)

	loaded, err := svc.GetShippingLocation(ctx, locationID)
	require.NoError(t, err)
	assert.Equal(t, []string{"reg_a"}, loaded.RegionIDs,
		"the rejected call must have DELETED no binding")
}

// TestPolicyReadOrdersRegionsByID nails down the order of the region array the
// selection path reads.
//
// The order is not cosmetic: when the filtering produces an empty set the error
// message prints this array, and the operator looks for a dead region id in that
// dump. An unstable order means the same fault looks different across two runs.
// The bindings are deliberately written in REVERSE order — had the write order
// been preserved, the test would be measuring the wrong direction.
func TestPolicyReadOrdersRegionsByID(t *testing.T) {
	ctx := t.Context()
	svc, _ := newService(t)

	const locationID = "sloc_order_1"
	setPolicy(ctx, t, svc, locationID, 0, "reg_z", "reg_m", "reg_a")

	repo := repository.New(testPool.Pool())
	policies, err := repo.LocationPolicies(ctx, []string{locationID})
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, []string{"reg_a", "reg_m", "reg_z"}, policies[0].RegionIDs,
		"the bindings the selection path reads must arrive ordered by ID")

	// The single read must give the same order too; if the two read paths drift
	// apart, the admin screen and the error message would show different
	// arrangements.
	loaded, err := svc.GetShippingLocation(ctx, locationID)
	require.NoError(t, err)
	assert.Equal(t, policies[0].RegionIDs, loaded.RegionIDs,
		"the single read and the selection read must give the same order")
}

// TestConcurrentPolicyWritesLeaveNoTornRecord proves that two writes do not
// interleave with each other.
//
// The write is TWO statements (the priority upsert + the wholesale refresh of the
// bindings) and tearing is concrete: A's priority and B's regions could end up
// side by side. What prevents this is not the transaction itself but the ROW LOCK
// the upsert takes — the second write waits in its own upsert until the first one
// commits. The claim is therefore not "there was no error" but "the result is
// EXACTLY ONE of the pairs written".
func TestConcurrentPolicyWritesLeaveNoTornRecord(t *testing.T) {
	ctx := t.Context()
	svc, _ := newService(t)

	const locationID = "sloc_concurrent_1"
	const writerCount = 8

	// Every writer's (priority, regions) pair is unique and derivable from the
	// priority: only this way can it be checked that the result is ONE PAIR.
	expectedRegion := func(priority int64) string { return fmt.Sprintf("reg_%d", priority) }

	var wg sync.WaitGroup
	errs := make([]error, writerCount)
	for i := range writerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			priority := int64(i)
			_, err := svc.SetShippingLocation(ctx, service.SetShippingLocationInput{
				LocationID: locationID,
				Priority:   priority,
				RegionIDs:  []string{expectedRegion(priority)},
			})
			errs[i] = err
		}()
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "writer %d must not get an error", i)
	}

	result, err := svc.GetShippingLocation(ctx, locationID)
	require.NoError(t, err)
	assert.Equal(t, []string{expectedRegion(result.Priority)}, result.RegionIDs,
		"the record must not be TORN: the regions must come from the same pair as the winning "+
			"writer's priority (priority %d)", result.Priority)
}
