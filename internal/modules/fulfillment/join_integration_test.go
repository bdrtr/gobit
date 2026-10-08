//go:build integration

package fulfillment_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	"github.com/bdrtr/gobit/internal/testdb"
)

// boundByOrder answers each order's own ceilings, the lines asked for or every
// line; an order it does not name sold nothing.
type boundByOrder map[string]map[string]int64

// DispatchCeilings answers the order's ceilings.
func (b boundByOrder) DispatchCeilings(
	_ context.Context, orderID string, lineItemIDs []string,
) (ceilings, spoken map[string]int64, err error) {
	out := map[string]int64{}
	for line, units := range b[orderID] {
		if len(lineItemIDs) == 0 || containsLine(lineItemIDs, line) {
			out[line] = units
		}
	}

	return out, nil, nil
}

// ReturnLines awaits nothing; these parcels go out.
func (boundByOrder) ReturnLines(context.Context, string, string) (awaited bool, lines map[string]int64, err error) {
	return false, nil, nil
}

// joinService is a service on the shared pool over store, answering bound.
func joinService(t *testing.T, store service.Store, bound boundByOrder) *service.Service {
	t.Helper()

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repository.New(testPool.Pool()), nil)))
	svc, err := service.New(service.Options{Store: store, Providers: registry, DispatchBound: bound})
	require.NoError(t, err)

	return svc
}

// joinOrders are a parent sold two units of its line and an addition sold one
// of its own, under references no other test uses.
func joinOrders() (parent, addition, parentLine, additionLine string, bound boundByOrder) {
	suffix := models.NewFulfillmentID()
	parent, addition = "order_parent_"+suffix, "order_addition_"+suffix
	parentLine, additionLine = "line_parent_"+suffix, "line_addition_"+suffix

	return parent, addition, parentLine, additionLine, boundByOrder{
		parent:   {parentLine: 2},
		addition: {additionLine: 1},
	}
}

// TestAJoinedAdditionsUnitsAreCountedForIt is ADR 0428 on the real schema: the
// addition's unit joins the parent's parcel as an item the addition owns, the
// counts by reference read it for the addition and not for the parent, and a
// second parcel of the addition can no longer take it.
func TestAJoinedAdditionsUnitsAreCountedForIt(t *testing.T) {
	ctx := context.Background()
	parent, addition, parentLine, additionLine, bound := joinOrders()
	svc := joinService(t, repository.New(testPool.Pool()), bound)
	option := newOption(ctx, t, svc, newProfile(ctx, t, svc).ID, 2_500)

	parcel, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference: parent, ShippingOptionID: option.ID, IdempotencyKey: parent + "-parcel", ItemsOwed: true,
	})
	require.NoError(t, err)

	joined, err := svc.JoinParcel(ctx, service.JoinParcelInput{
		FulfillmentID: parcel.ID, ParentReference: parent, AdditionReference: addition, ItemsOwed: true,
	})
	require.NoError(t, err)
	require.Len(t, joined, 1)
	assert.Equal(t, addition, joined[0].Reference, "the item belongs to the addition")

	held, err := svc.QuantitiesOfFulfillment(ctx, parcel.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{parentLine: 2, additionLine: 1}, held, "the box holds both orders' units")

	forAddition, err := svc.HeldForReferenceLocked(ctx, addition, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{additionLine: 1}, forAddition, "the addition's unit counts for it")
	forParent, err := svc.CommittedQuantitiesForReference(ctx, parent, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{parentLine: 2}, forParent, "and not for the parent")

	_, err = svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference: addition, ShippingOptionID: option.ID, IdempotencyKey: addition + "-second",
		Items: []service.FulfillmentItemInput{{LineItemID: additionLine, Quantity: 1}},
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeLineNotDispatchable, errors.CodeOf(err), "%v", err)
}

// meetingStore holds the first count of one reference until a second count of
// it arrives or pause runs out.
//
// It is the slow participant the race needs: two acts counting what an order's
// parcels hold, each before the other writes, would both see the unit owed.
// Under the order's dispatch lock the second act cannot count until the first
// one commits, so the first waits out the pause alone; without the lock both
// counts meet, and both acts go on to write.
type meetingStore struct {
	*repository.Repository
	reference string
	pause     time.Duration

	mu    sync.Mutex
	count int
	both  chan struct{}
}

// HeldQuantitiesForReference counts, then waits for the other count of the
// reference.
func (s *meetingStore) HeldQuantitiesForReference(
	ctx context.Context, reference string,
) (map[string]models.HeldUnits, error) {
	units, err := s.Repository.HeldQuantitiesForReference(ctx, reference)
	if reference != s.reference {
		return units, err
	}

	s.mu.Lock()
	s.count++
	if s.count == 2 {
		close(s.both)
	}
	s.mu.Unlock()

	select {
	case <-s.both:
	case <-time.After(s.pause):
	}

	return units, err
}

// TestAJoinAndAnOpenOfTheAdditionHoldItsUnitOnce is a join racing an open of
// the addition naming its one unit (ADR 0428): each reads the addition's
// ceiling before either writes, so each alone fits. Exactly one of them holds
// the unit and the other is refused, because both count the addition's parcels
// under its dispatch lock.
func TestAJoinAndAnOpenOfTheAdditionHoldItsUnitOnce(t *testing.T) {
	ctx := context.Background()
	parent, addition, _, additionLine, bound := joinOrders()
	store := &meetingStore{
		Repository: repository.New(testPool.Pool()),
		reference:  addition,
		pause:      time.Second,
		both:       make(chan struct{}),
	}
	svc := joinService(t, store, bound)
	option := newOption(ctx, t, svc, newProfile(ctx, t, svc).ID, 2_500)
	parcel, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference: parent, ShippingOptionID: option.ID, IdempotencyKey: parent + "-parcel", ItemsOwed: true,
	})
	require.NoError(t, err)

	var (
		joinErr, openErr error
		start, done      sync.WaitGroup
	)
	start.Add(1)
	done.Add(2)
	go func() {
		defer done.Done()
		start.Wait()
		_, joinErr = svc.JoinParcel(ctx, service.JoinParcelInput{
			FulfillmentID: parcel.ID, ParentReference: parent, AdditionReference: addition,
			Items: []service.FulfillmentItemInput{{LineItemID: additionLine, Quantity: 1}},
		})
	}()
	go func() {
		defer done.Done()
		start.Wait()
		_, openErr = svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
			Reference: addition, ShippingOptionID: option.ID, IdempotencyKey: addition + "-own",
			Items: []service.FulfillmentItemInput{{LineItemID: additionLine, Quantity: 1}},
		})
	}()
	start.Done()
	done.Wait()

	switch {
	case joinErr == nil && openErr != nil:
		assert.Equal(t, service.CodeLineNotDispatchable, errors.CodeOf(openErr), "%v", openErr)
	case openErr == nil && joinErr != nil:
		assert.Equal(t, service.CodeLineNotDispatchable, errors.CodeOf(joinErr), "%v", joinErr)
	default:
		t.Errorf("exactly one of the two holds the unit: the join answered %v, the open %v", joinErr, openErr)
	}

	var held int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COALESCE(SUM(i.quantity), 0) FROM fulfillment_items i
		   JOIN fulfillments f ON f.id = i.fulfillment_id
		  WHERE i.reference = $1 AND f.status <> 'canceled'`, addition).Scan(&held))
	assert.Equal(t, int64(1), held, "the addition's one unit is held once")
}

// TestAnItemWrittenBeforeTheUpgradeBelongsToItsParcelsOrder is fulfillment
// migration 000009 (ADR 0428): an item written before it is given its parcel's
// reference, so every count reads it as before, and the down migration drops
// the column.
func TestAnItemWrittenBeforeTheUpgradeBelongsToItsParcelsOrder(t *testing.T) {
	ctx := context.Background()
	src := fulfillment.New().Migrations()
	// The rollback runs in a database of its own, for D141's reason.
	dsn := testdb.New(t, testDSN, "fulfillment_item_reference")
	require.NoError(t, db.Migrate(ctx, dsn, src, fulfillment.ModuleName))
	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	svc, _ := newServiceOn(t, pool)
	option := newOption(ctx, t, svc, newProfile(ctx, t, svc).ID, 2_500)
	const reference, line = "order_before_items", "line_before_items"
	before, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference: reference, ShippingOptionID: option.ID, IdempotencyKey: reference,
		Items: []service.FulfillmentItemInput{{LineItemID: line, Quantity: 2}},
	})
	require.NoError(t, err)
	owner := func() (string, error) {
		var ref string
		err := pool.Pool().QueryRow(ctx,
			`SELECT reference FROM fulfillment_items WHERE fulfillment_id = $1`, before.ID).Scan(&ref)

		return ref, err
	}

	require.NoError(t, db.MigrateDown(ctx, dsn, src, fulfillment.ModuleName, stepsBelow(t, src, 9)))
	_, err = owner()
	require.Error(t, err, "the down migration drops the column")
	assert.Contains(t, err.Error(), "reference")

	require.NoError(t, db.Migrate(ctx, dsn, src, fulfillment.ModuleName))
	ref, err := owner()
	require.NoError(t, err)
	assert.Equal(t, reference, ref, "an item written before belongs to its parcel's order")

	held, err := svc.HeldForReferenceLocked(ctx, reference, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{line: 2}, held, "and is counted for it as before")

	_, err = pool.Pool().Exec(ctx,
		`INSERT INTO fulfillment_items (id, fulfillment_id, line_item_id, quantity, reference)
		 VALUES ($1, $2, 'line_blank', 1, ' ')`, models.NewFulfillmentItemID(), before.ID)
	require.Error(t, err, "an item belongs to some order")
	assert.Contains(t, err.Error(), "fulfillment_items_reference_check")
}
