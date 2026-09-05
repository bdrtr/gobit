package service_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file exercises the warehouse selection POLICY: elimination, ranking and
// tie breaking.
//
// The tests DO NOT WANT a real database, because the decision itself is a pure
// function; the fake store only carries the policy records. The proof that the
// policy runs on real Postgres and with the real saga is on the e2e side.

// writePolicy sets up a warehouse policy for the test.
func writePolicy(
	t *testing.T,
	setup testSetup,
	locationID string,
	priority int64,
	regionIDs ...string,
) {
	t.Helper()
	_, err := setup.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: locationID,
		Priority:   priority,
		RegionIDs:  regionIDs,
	})
	if err != nil {
		t.Fatalf("the warehouse policy could not be written (%s): %v", locationID, err)
	}
}

// TestRankLocationsEliminatesACandidateNotServingTheTargetRegion proves the
// coverage elimination.
//
// The eliminated candidate is the one the tie-breaking rule (smallest
// identifier) would put FIRST; otherwise the test would have exercised the
// identifier order rather than the policy. An eliminated candidate drops out of
// the order COMPLETELY, it is not pushed to the end: a fallback would try it
// again.
func TestRankLocationsEliminatesACandidateNotServingTheTargetRegion(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_ankara", 0, "reg_de")
	writePolicy(t, setup, "sloc_izmir", 0, testRegionID)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_izmir"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_izmir"}, ranked,
		"a candidate that does not serve the target region has to be eliminated, even with a smaller identifier")
}

// TestRankLocationsARegionlessWarehouseServesEveryRegion proves that a warehouse
// with NO link is not eliminated.
//
// The rule is the same as the sales channel scope's and carries the same trap:
// deleting a warehouse's last region link does not close it, it OPENS it to ALL
// regions. The test pins that the trap is deliberate — had the rule been
// inverted, every setup that has no policy today would become unable to take
// orders.
func TestRankLocationsARegionlessWarehouseServesEveryRegion(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_central", 0)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_central"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_central"}, ranked)
}

// TestRankLocationsPriorityOverridesTheIdentifierOrder proves that the ranking
// comes BEFORE the tie-breaking rule.
//
// The claim is the reason this feature exists: without the policy the candidate
// with the smallest identifier would go first and the operator could not express
// a preference. A candidate that is not eliminated STAYS in the order — priority
// is not an elimination but a lineup rule.
func TestRankLocationsPriorityOverridesTheIdentifierOrder(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_ankara", 10, testRegionID)
	writePolicy(t, setup, "sloc_izmir", 1, testRegionID)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_izmir"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_izmir", "sloc_ankara"}, ranked,
		"the smaller priority has to go first and the other has to stay in the order")
}

// TestRankLocationsNegativePriorityBeatsAPolicylessWarehouse proves that a
// negative priority overtakes a warehouse that has NO policy.
//
// That is the concrete reason negatives are allowed: lifting one warehouse to the
// front has to take a single row, and it must not require writing rows for the
// warehouses one does NOT want lifted.
func TestRankLocationsNegativePriorityBeatsAPolicylessWarehouse(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_zonguldak", -1, testRegionID)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_zonguldak"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_zonguldak", "sloc_ankara"}, ranked,
		"a negative priority has to be above a warehouse with no record (priority zero)")
}

// TestRankLocationsAWarehouseWithNoRecordEqualsPriorityZero proves that "no
// record" and "priority explicitly zero" are at THE SAME rank.
//
// Had the two been separated, writing priority zero for a warehouse would
// silently move it forward or backward; yet the value written is the default
// itself.
func TestRankLocationsAWarehouseWithNoRecordEqualsPriorityZero(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_izmir", 0, testRegionID)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_izmir"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_ankara", "sloc_izmir"}, ranked,
		"at equal priority the order has to be built by identifier; a warehouse with no record is at priority zero")
}

// TestRankLocationsAllCandidatesEliminatedIsAConflict pins the KIND and the CODE
// of the error when the elimination leaves an empty result.
//
// The kind has to be Conflict and the rationale IS NOT the caller's branching:
// the kind determines the error's HTTP counterpart, and the engine's default
// retry predicate does not retry KindConflict, it retries KindInternal. An
// eliminated candidate set does not change by trying again; had Internal been
// chosen, a configuration error the operator has to fix by hand would be taken
// for a transient fault.
//
// The code is SEPARATE because the work the operator has to do is separate: here
// there is stock, what is set up wrong is the region coverage.
func TestRankLocationsAllCandidatesEliminatedIsAConflict(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_ankara", 0, "reg_de")
	writePolicy(t, setup, "sloc_izmir", 0, "reg_fr")

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_izmir"})
	require.Error(t, err)
	assert.Empty(t, ranked)
	assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
	assert.Equal(t, service.CodeNoServiceableLocation, errors.CodeOf(err))
}

// TestRankLocationsAnEmptyRegionIsInvalid proves that the ranking cannot be done
// without a target region.
//
// SKIPPING the elimination on an empty region would mean silently picking a
// warehouse that does not serve that region; the failure would surface a module
// away from its cause.
func TestRankLocationsAnEmptyRegionIsInvalid(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	for _, region := range []string{"", "   "} {
		ranked, err := setup.svc.RankLocations(context.Background(), region,
			[]string{"sloc_ankara"})
		require.Error(t, err)
		assert.Empty(t, ranked)
		assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
		assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
	}
}

// TestRankLocationsDoesNotModifyTheCandidateSlice proves that the decision
// surface DOES NOT CORRUPT the data it is handed.
//
// The policy ranks, but it does not sort the candidates IN PLACE: the caller's
// slice is the saga's candidate ledger and a decision surface cannot modify the
// data it is given.
func TestRankLocationsDoesNotModifyTheCandidateSlice(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_izmir", -5, testRegionID)

	candidates := []string{"sloc_ankara", "sloc_izmir", "sloc_bursa"}
	before := slices.Clone(candidates)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID, candidates)
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_izmir", "sloc_ankara", "sloc_bursa"}, ranked)
	assert.Equal(t, before, candidates, "the candidate slice must not be modified")
}

// TestSetShippingLocationWritesRegionsWholesale proves that the region list is
// ABSOLUTE: the old links do not survive.
//
// Had merging (adding to the old) been chosen, there would be no way to remove a
// region and the coverage could only widen.
func TestSetShippingLocationWritesRegionsWholesale(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_ankara", 0, "reg_de", testRegionID)

	record, err := setup.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_ankara",
		RegionIDs:  []string{"reg_de"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"reg_de"}, record.RegionIDs)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara"})
	require.Error(t, err, "the removed region link has to affect the ranking too")
	assert.Empty(t, ranked)
	assert.Equal(t, service.CodeNoServiceableLocation, errors.CodeOf(err))
}

// TestSetShippingLocationAnEmptyRegionListOpensEveryRegion proves that emptying
// the region list DOES NOT CLOSE the warehouse but OPENS it to every region.
//
// The trap is written down and the test pins it: an operator who deletes the last
// link in order to narrow the coverage gets exactly the opposite.
func TestSetShippingLocationAnEmptyRegionListOpensEveryRegion(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_ankara", 0, "reg_de")

	_, err := setup.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_ankara",
	})
	require.NoError(t, err)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_ankara"}, ranked)
}

// TestSetShippingLocationDropsARepeatedRegion proves that giving the same region
// twice is NOT an error but a single link.
func TestSetShippingLocationDropsARepeatedRegion(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	record, err := setup.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_ankara",
		RegionIDs:  []string{testRegionID, " " + testRegionID + " ", "reg_de"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"reg_de", testRegionID}, record.RegionIDs,
		"the repeated region has to be dropped; the returned order is NOT the input's but by identifier — "+
			"the links form a set, not a list")
}

// TestSetShippingLocationAnEmptyRegionIdentifierIsInvalid proves that an empty
// region identifier is not written.
func TestSetShippingLocationAnEmptyRegionIdentifierIsInvalid(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	_, err := setup.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_ankara",
		RegionIDs:  []string{testRegionID, "   "},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)

	_, getErr := setup.svc.GetShippingLocation(context.Background(), "sloc_ankara")
	require.Error(t, getErr, "a request that fails validation must not write any row")
	assert.True(t, errors.IsNotFound(getErr))
}

// TestDeleteShippingLocationReturnsToTheDefault proves that the deletion DOES NOT
// CLOSE the warehouse but returns it to the default.
//
// The distinction matters: the shipping module cannot take a warehouse out of
// candidacy, the candidate list is produced by a stock fact. Deleting only means
// "there is no special rule for this warehouse".
func TestDeleteShippingLocationReturnsToTheDefault(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_ankara", 0, "reg_de")

	_, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara"})
	require.Error(t, err, "an out-of-scope warehouse has to be eliminated BEFORE the deletion")

	require.NoError(t, setup.svc.DeleteShippingLocation(context.Background(), "sloc_ankara"))

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara"})
	require.NoError(t, err, "a warehouse whose policy was deleted has to return to the default")
	assert.Equal(t, []string{"sloc_ankara"}, ranked)
}

// TestDeleteShippingLocationOnAnUnknownRecordReturnsNotFound proves that deleting
// a record that does not exist does not SILENTLY succeed.
//
// DELETE returns without an error for a row that does not exist either; without
// the check, a deletion made with a wrong identifier would look successful and
// the operator would keep working with a rule they believed they had removed.
func TestDeleteShippingLocationOnAnUnknownRecordReturnsNotFound(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	err := setup.svc.DeleteShippingLocation(context.Background(), "sloc_missing")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the error has to be errors.NotFound: %v", err)
}

// TestListShippingLocationsReturnsInPriorityOrder pins the listing's order:
// priority first, identifier second — the same order the selection applies.
func TestListShippingLocationsReturnsInPriorityOrder(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_ankara", 5, testRegionID)
	writePolicy(t, setup, "sloc_bursa", -2, testRegionID)
	writePolicy(t, setup, "sloc_izmir", 5, testRegionID)

	records, total, err := setup.svc.ListShippingLocations(context.Background(), service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)

	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.LocationID)
	}
	assert.Equal(t, []string{"sloc_bursa", "sloc_ankara", "sloc_izmir"}, ids)
}

// TestRankLocationsTheEliminationErrorNamesTheLinks proves that the reason for
// the elimination IS VISIBLE in the message.
//
// The claim is not a convenience: the sneakiest cause of elimination is a dead
// region identifier. If the operator deletes a region and reopens it under the
// same name the identifier changes, the policy rows keep carrying the old one and
// EVERY order in the store is eliminated. With a message that only said "no
// warehouse serves it" the operator cannot see that the identifiers have
// diverged; because the message writes the links, they can.
func TestRankLocationsTheEliminationErrorNamesTheLinks(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_ankara", 0, "reg_dead")
	writePolicy(t, setup, "sloc_izmir", 0, "reg_dead")

	_, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_izmir"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reg_dead",
		"the message has to write the region the warehouse is ACTUALLY bound to")
	assert.Contains(t, err.Error(), testRegionID,
		"the message has to write which region was being looked for")
}

// TestRankLocationsReturnsTheCallersStringVerbatim proves that the returned
// elements are EXACTLY the strings the caller gave.
//
// The matching is done with the leading/trailing whitespace stripped, but the
// return CANNOT BE a normalized copy: the caller looks the result up in its own
// candidate ledger and, if it cannot find it, drops the flow as an internal
// error. Had a trimmed copy been returned, a caller that wrote " sloc_a " would
// get a 500 without having broken the contract.
func TestRankLocationsReturnsTheCallersStringVerbatim(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	writePolicy(t, setup, "sloc_izmir", -1, testRegionID)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"  sloc_izmir  ", "sloc_ankara"})
	require.NoError(t, err)
	assert.Equal(t, []string{"  sloc_izmir  ", "sloc_ankara"}, ranked,
		"the matching uses the trimmed key, the return is the caller's string")
}

// TestRankLocationsARepeatedCandidateIsRankedOnce proves that giving the same
// candidate twice DOES NOT SHOW it twice in the order.
//
// Had it shown up twice, the caller would try to reserve at the same warehouse
// twice: a warehouse that dropped because it was exhausted the first time gives
// the same answer on the second round and the fallback would waste one of its
// rounds.
func TestRankLocationsARepeatedCandidateIsRankedOnce(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_ankara", "sloc_izmir"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_ankara", "sloc_izmir"}, ranked)
}

// TestSetShippingLocationTheRegionCountIsBounded nails BOTH SIDES of the bound.
//
// A one-sided test (only "101 is rejected") does not catch the bound being turned
// into `>=`; the two sides together pin that the bound both EXISTS and is IN THE
// RIGHT PLACE.
func TestSetShippingLocationTheRegionCountIsBounded(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	atLimit := make([]string, 0, 100)
	for i := range 100 {
		atLimit = append(atLimit, fmt.Sprintf("reg_%03d", i))
	}

	record, err := setup.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_limit",
		RegionIDs:  atLimit,
	})
	require.NoError(t, err, "a request exactly at the bound has to be ACCEPTED")
	assert.Len(t, record.RegionIDs, 100)

	_, err = setup.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_over_limit",
		RegionIDs:  append(atLimit, "reg_extra"),
	})
	require.Error(t, err, "a request one over the bound has to be REJECTED")
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestSetShippingLocationRejectsAnOverlongRegion proves that the text length
// bound is applied to the region identifiers as well.
//
// Without the bound a single request would write text of unbounded size into the
// database; leaving this unexercised while the sibling checks (empty identifier,
// count bound) are exercised would mean only half the protection is nailed down.
func TestSetShippingLocationRejectsAnOverlongRegion(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	_, err := setup.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_long_region",
		RegionIDs:  []string{"reg_" + strings.Repeat("x", 1024)},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestRankLocationsRejectsAnOverlongCandidate proves that the same bound is
// applied to the CANDIDATE identifiers too.
func TestRankLocationsRejectsAnOverlongCandidate(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_" + strings.Repeat("y", 1024)})
	require.Error(t, err)
	assert.Empty(t, ranked)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestRankLocationsTieBreakingUsesTheTrimmedIdentifier proves that the ranking
// rests on the TRIMMED key.
//
// Had it rested on the raw string, the order would depend on how the caller wrote
// the identifiers: "  sloc_z" comes BEFORE "sloc_a" in a raw comparison (a space
// is smaller than a letter) and the result would come out differently for the
// same two warehouses under different spellings. The return value is still the
// caller's string; the matching and the return are different things and this test
// nails both at once.
func TestRankLocationsTieBreakingUsesTheTrimmedIdentifier(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	ranked, err := setup.svc.RankLocations(context.Background(), testRegionID,
		[]string{"  sloc_z", "sloc_a"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_a", "  sloc_z"}, ranked,
		"the order has to be built by the trimmed identifier but the elements have to stay the caller's strings")
}

// TestRankLocationsValidationOrderIsPinned pins which error wins.
//
// The third block is the real one: when both inputs are malformed, which error
// the caller sees is a CHOICE, and if it is not written down it changes silently
// the day the checks move. The empty candidate list comes first, because it is a
// WORLD state (Conflict) and the caller's "the order cannot be placed" branch
// hangs off it; an empty region, on the other hand, is a caller defect and cannot
// even arise in this package's single production caller.
func TestRankLocationsValidationOrderIsPinned(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ctx := context.Background()

	_, err := setup.svc.RankLocations(ctx, "", []string{"sloc_a"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "Invalid when the region is empty: %v", err)

	_, err = setup.svc.RankLocations(ctx, testRegionID, nil)
	require.Error(t, err)
	assert.Equal(t, service.CodeNoShippingLocation, errors.CodeOf(err),
		"Conflict/CodeNoShippingLocation when the candidates are empty: %v", err)

	_, err = setup.svc.RankLocations(ctx, "", nil)
	require.Error(t, err)
	assert.Equal(t, service.CodeNoShippingLocation, errors.CodeOf(err),
		"when BOTH are empty the candidate check wins; the order is deliberate and is pinned here: %v", err)
}

// TestPolicyReadAndDeleteRejectAnEmptyIdentifier proves that a location
// identifier carrying nothing but whitespace never reaches the database.
//
// Without the check the empty identifier would go down to the store and come back
// as NotFound: the client would see "there is no such policy", while the real
// defect is in its own request. The sibling check is exercised on the write path;
// the read and delete paths have to give the same guarantee.
func TestPolicyReadAndDeleteRejectAnEmptyIdentifier(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ctx := context.Background()

	for _, id := range []string{"", "   "} {
		_, err := setup.svc.GetShippingLocation(ctx, id)
		require.Error(t, err, "the read has to reject an empty identifier: %q", id)
		assert.True(t, errors.IsInvalid(err), "the read error has to be Invalid: %v", err)

		err = setup.svc.DeleteShippingLocation(ctx, id)
		require.Error(t, err, "the delete has to reject an empty identifier: %q", id)
		assert.True(t, errors.IsInvalid(err), "the delete error has to be Invalid: %v", err)
	}
}
