//go:build integration

// The tests here run against a real PostgreSQL (and therefore Docker); to run
// them: make test-integration
//
// They exist because what they cover IS SQL. The unit tests prove the service's
// decisions against a fake store, and the two halves a fake cannot hold are the
// ones here: a CHECK constraint it does not have, and a ceiling that is summed
// by a QUERY. The fake sums the same rows in Go — which is exactly why the
// query itself needs a witness: if it counted a withdrawn promise the unit
// tests would stay green.
package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// replaceableClaim places an order, opens a claim to be settled with goods and
// returns it with the id of the order's single line, which carries three units.
func replaceableClaim(
	ctx context.Context, t *testing.T, svc *service.Service,
) (claim models.Claim, lineID string) {
	t.Helper()

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)

	claim, err = svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID: ord.ID, Type: models.ClaimReplace,
	})
	require.NoError(t, err)

	return claim, detail.Items[0].ID
}

// requestOf is a replacement request for one line.
func requestOf(claimID, lineID string, quantity int64) service.CreateReplacementInput {
	return service.CreateReplacementInput{
		ClaimID:          claimID,
		ShippingOptionID: "so_integration",
		LocationID:       "sloc_integration",
		Lines: []service.ReplacementLineInput{
			{OrderLineItemID: lineID, Quantity: quantity},
		},
	}
}

// TestTheReplacementCeilingIsSummedByTheRealQuery is the rule that spans rows,
// proved where it is really decided.
//
// The sum comes from SumReplacedQuantities, and its whole content is the
// exclusion of withdrawn promises. Both directions are here: while the first
// promise stands the line is spoken for, and once it is withdrawn the units are
// free again.
func TestTheReplacementCeilingIsSummedByTheRealQuery(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	claim, lineID := replaceableClaim(ctx, t, svc)

	first, err := svc.CreateReplacement(ctx, requestOf(claim.ID, lineID, 2))
	require.NoError(t, err)

	_, err = svc.CreateReplacement(ctx, requestOf(claim.ID, lineID, 2))
	require.Error(t, err, "two of three plus two of three is more than was bought")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeReplacementQuantityExceeded, errors.CodeOf(err))

	_, err = svc.CancelReplacement(ctx, first.ID)
	require.NoError(t, err)

	third, err := svc.CreateReplacement(ctx, requestOf(claim.ID, lineID, 3))
	require.NoError(t, err, "a withdrawn promise sends nothing and holds nothing")
	assert.Equal(t, models.ReplacementRequested, third.Status)
}

// TestWithdrawingAReplacementStampsTheDatabaseClock verifies that the moment is
// the DATABASE's and that it is ON THE ROW.
//
// A RETURNING clause can report a value the row does not keep, and the point of
// the column is that the next read still finds it.
func TestWithdrawingAReplacementStampsTheDatabaseClock(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	claim, lineID := replaceableClaim(ctx, t, svc)

	record, err := svc.CreateReplacement(ctx, requestOf(claim.ID, lineID, 1))
	require.NoError(t, err)
	require.Nil(t, record.CanceledAt, "an open request carries no withdrawal moment")

	withdrawn, err := svc.CancelReplacement(ctx, record.ID)
	require.NoError(t, err)
	require.NotNil(t, withdrawn.CanceledAt)
	assert.Equal(t, "UTC", withdrawn.CanceledAt.Location().String())

	var stamped bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT canceled_at IS NOT NULL FROM order_replacements WHERE id = $1`,
		record.ID).Scan(&stamped))
	assert.True(t, stamped, "the moment has to be ON THE ROW, not only in the response")
}

// TestTheSchemaRefusesAReplacementStateNothingCanReach is 000008's rule applied
// to a table born after it.
//
// The vocabulary is two words because two are all the code can write. A row
// stamped 'dispatched' would be a claim that goods left the warehouse, made by
// a database nobody dispatched anything through.
func TestTheSchemaRefusesAReplacementStateNothingCanReach(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	claim, lineID := replaceableClaim(ctx, t, svc)

	record, err := svc.CreateReplacement(ctx, requestOf(claim.ID, lineID, 1))
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_replacements SET status = 'dispatched' WHERE id = $1`, record.ID)

	require.Error(t, err, "a state nothing can reach must not be writable")
	assert.Contains(t, err.Error(), "order_replacements_status_valid")
}

// TestAWithdrawnReplacementAndItsMomentImplyEachOther holds the mirror CHECK in
// both directions.
func TestAWithdrawnReplacementAndItsMomentImplyEachOther(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	claim, lineID := replaceableClaim(ctx, t, svc)

	record, err := svc.CreateReplacement(ctx, requestOf(claim.ID, lineID, 1))
	require.NoError(t, err)

	// A status without its moment.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_replacements SET status = 'canceled' WHERE id = $1`, record.ID)
	require.Error(t, err, "a withdrawn promise with no moment cannot be dated afterwards")
	assert.Contains(t, err.Error(), "order_replacements_canceled_stamp")

	// And the reverse: a moment without its status.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_replacements SET canceled_at = now() WHERE id = $1`, record.ID)
	require.Error(t, err, "a moment on an open request would date a withdrawal that did not happen")
	assert.Contains(t, err.Error(), "order_replacements_canceled_stamp")
}

// TestOneLineIsPromisedAtMostOnceInOneReplacement verifies the unique index.
//
// The service refuses the duplicate before the write and names the mistake; the
// index is what holds when the write comes from anywhere else, and the count
// belongs in the quantity rather than in a second row.
func TestOneLineIsPromisedAtMostOnceInOneReplacement(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	claim, lineID := replaceableClaim(ctx, t, svc)

	record, err := svc.CreateReplacement(ctx, requestOf(claim.ID, lineID, 1))
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO order_replacement_items
             (id, order_replacement_id, order_line_item_id, quantity)
         VALUES ($1, $2, $3, 1)`,
		models.NewReplacementItemID(), record.ID, lineID)

	require.Error(t, err, "the same line twice in one replacement is two counts of one thing")
	assert.Contains(t, err.Error(), "order_replacement_items_line_uniq")
}

// TestAPromisedQuantityHasToBePositive verifies the CHECK the service's own
// validation stands in front of.
func TestAPromisedQuantityHasToBePositive(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	claim, lineID := replaceableClaim(ctx, t, svc)

	record, err := svc.CreateReplacement(ctx, requestOf(claim.ID, lineID, 1))
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_replacement_items SET quantity = 0 WHERE order_replacement_id = $1`,
		record.ID)

	require.Error(t, err, "a line that sends nothing is not a line")
	assert.Contains(t, err.Error(), "order_replacement_items_quantity_positive")
}

// TestAReplacementDoesNotOutliveItsClaim verifies the cascade.
//
// The record exists to say what a claim will send; kept after the claim is
// gone it would be a promise with nobody to make it.
func TestAReplacementDoesNotOutliveItsClaim(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	claim, lineID := replaceableClaim(ctx, t, svc)

	record, err := svc.CreateReplacement(ctx, requestOf(claim.ID, lineID, 1))
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx, `DELETE FROM order_claims WHERE id = $1`, claim.ID)
	require.NoError(t, err)

	var rows int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM order_replacements WHERE id = $1`, record.ID).Scan(&rows))
	assert.Zero(t, rows, "the replacement has to go with the claim that promised it")

	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM order_replacement_items WHERE order_replacement_id = $1`,
		record.ID).Scan(&rows))
	assert.Zero(t, rows, "and its lines with it")
}
