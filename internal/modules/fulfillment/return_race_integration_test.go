//go:build integration

package fulfillment_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// gatedReturn answers that one return names fixed units, and answers no caller
// of ReturnLines until every caller it was built for has asked.
//
// The gate opens the window ADR 0420 closes for a parcel bringing a return
// back: each open learns what the return names before either has written a
// unit. Outgoing parcels are answered at once from owed.
type gatedReturn struct {
	returnID string
	named    map[string]int64
	owed     map[string]int64
	arrived  sync.WaitGroup
}

// newGatedReturn is a return naming named that answers once callers have asked.
func newGatedReturn(returnID string, named map[string]int64, callers int) *gatedReturn {
	g := &gatedReturn{returnID: returnID, named: named}
	g.arrived.Add(callers)

	return g
}

// DispatchCeilings answers owed for an outgoing parcel.
func (g *gatedReturn) DispatchCeilings(context.Context, string, []string) (map[string]int64, error) {
	return g.owed, nil
}

// ReturnLines answers the return's units once every caller has asked; another
// return names its units without waiting.
func (g *gatedReturn) ReturnLines(
	_ context.Context, _, returnID string,
) (awaited bool, lines map[string]int64, err error) {
	if returnID != g.returnID {
		return true, g.named, nil
	}
	g.arrived.Done()
	g.arrived.Wait()

	return true, g.named, nil
}

// raceService is a fulfillment service on the real schema over bound, with the
// manual provider, or with provider in its place when one is given.
func raceService(t *testing.T, bound service.DispatchBound, provider coreprovider.FulfillmentProvider) *service.Service {
	t.Helper()

	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	if provider == nil {
		provider = manual.New(repo, nil)
	}
	require.NoError(t, registry.Register(provider))
	svc, err := service.New(service.Options{Store: repo, Providers: registry, DispatchBound: bound})
	require.NoError(t, err)

	return svc
}

// TestTwoReturnParcelsOpenedAtOnceBringBackNoMoreThanTheReturnNames is gap
// D265 for a parcel bringing a return back, on the real schema (ADR 0420): the
// return names two units of a line, and two parcels of two units each are
// opened for it at the same moment under different keys. Each read what the
// return names before the other wrote; together they would bring back four.
// Exactly one opens, the other is refused, and the return's live parcels hold
// what it names.
func TestTwoReturnParcelsOpenedAtOnceBringBackNoMoreThanTheReturnNames(t *testing.T) {
	ctx := context.Background()
	reference := "order_return_race_" + models.NewFulfillmentID()
	returnID := "ret_race_" + models.NewFulfillmentID()
	const line = "line_coming_back"

	svc := raceService(t, newGatedReturn(returnID, map[string]int64{line: 2}, 2), nil)
	option := newReturnOption(ctx, t, svc, newProfile(ctx, t, svc).ID)

	errs := make([]error, 2)
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(2)
	for i := range 2 {
		go func() {
			defer done.Done()
			start.Wait()
			_, errs[i] = svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
				Reference:        reference,
				ShippingOptionID: option.ID,
				IdempotencyKey:   reference + "-" + string(rune('a'+i)),
				ReturnID:         returnID,
				Items:            []service.FulfillmentItemInput{{LineItemID: line, Quantity: 2}},
			})
		}()
	}
	start.Done()
	done.Wait()

	opened, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			opened++
		case errors.IsConflict(err) && errors.CodeOf(err) == service.CodeLineNotDispatchable:
			refused++
		default:
			t.Fatalf("an open failed in an unexpected way: %v", err)
		}
	}
	assert.Equal(t, 1, opened, "exactly one of the two parcels opens")
	assert.Equal(t, 1, refused, "the other is refused as more than the return names")

	var held int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COALESCE(SUM(i.quantity), 0) FROM fulfillment_items i
		   JOIN fulfillments f ON f.id = i.fulfillment_id
		  WHERE f.return_id = $1 AND f.status IN ('pending', 'shipped', 'delivered')`,
		returnID).Scan(&held))
	assert.Equal(t, int64(2), held, "the return's live parcels hold what it names")
}

// TestAnotherReturnsParcelDoesNotCountOnTheRealSchema opens a parcel bringing
// one return of an order back while a parcel bringing another return of the same
// order back holds the same line: the count under the lock is the return's own.
func TestAnotherReturnsParcelDoesNotCountOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	reference := "order_two_returns_" + models.NewFulfillmentID()
	const line = "line_in_both"

	svc := raceService(t, newGatedReturn("ret_unused", map[string]int64{line: 2}, 1), nil)
	option := newReturnOption(ctx, t, svc, newProfile(ctx, t, svc).ID)
	for _, returnID := range []string{"ret_b_" + reference, "ret_a_" + reference} {
		_, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
			Reference:        reference,
			ShippingOptionID: option.ID,
			IdempotencyKey:   returnID,
			ReturnID:         returnID,
			Items:            []service.FulfillmentItemInput{{LineItemID: line, Quantity: 2}},
		})
		require.NoError(t, err, "return %s brings back the two units it names", returnID)
	}
}

// blockingProvider is the manual provider whose Create waits for release after
// telling entered, which is an open's provider call in flight.
type blockingProvider struct {
	*manual.Provider
	entered chan struct{}
	release chan struct{}
}

// Create tells entered, waits for release, then creates the shipment.
func (p *blockingProvider) Create(
	ctx context.Context, in coreprovider.CreateFulfillmentInput,
) (coreprovider.Fulfillment, error) {
	close(p.entered)
	<-p.release

	return p.Provider.Create(ctx, in)
}

// TestTheHeldCountRefusesWhileAParcelIsBeingOpened is the cancellation's read
// on the real schema (ADR 0420): an outgoing parcel of three units is being
// opened and its provider call is in flight when the count is asked. The open
// took the order's lock before it reached the provider, so when entered closes
// the lock is held: the count answers busy at once rather than waiting, and
// asked again after the open commits it answers the parcel's three units. Read
// without the lock it would answer none, and a write-off read then would put
// back units about to be in a box; read by waiting, it would hold a pooled
// connection for the carrier's call.
func TestTheHeldCountRefusesWhileAParcelIsBeingOpened(t *testing.T) {
	ctx := context.Background()
	reference := "order_held_" + models.NewFulfillmentID()
	const line = "line_in_flight"

	provider := &blockingProvider{
		Provider: manual.New(repository.New(testPool.Pool()), nil),
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	svc := raceService(t, newGatedBound(map[string]int64{line: 3}, 1), provider)
	option := newOption(ctx, t, svc, newProfile(ctx, t, svc).ID, 2_500)

	opened := make(chan error, 1)
	go func() {
		_, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
			Reference:        reference,
			ShippingOptionID: option.ID,
			IdempotencyKey:   reference,
			Items:            []service.FulfillmentItemInput{{LineItemID: line, Quantity: 3}},
		})
		opened <- err
	}()
	<-provider.entered

	// A count that waited for the lock would wait for the release below, which
	// only comes after it; the deadline turns that wait into a failure.
	busyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	held, err := svc.HeldForReferenceLocked(busyCtx, reference)
	require.Error(t, err, "the count does not wait for the open")
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err), "a busy answer is one a retry may pass")
	assert.Equal(t, service.CodeDispatchBusy, errors.CodeOf(err))
	assert.Nil(t, held)

	close(provider.release)
	require.NoError(t, <-opened)

	held, err = svc.HeldForReferenceLocked(ctx, reference)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{line: 3}, held, "asked again, the count includes the parcel that committed")
}

// TestAReturnedParcelStillCountsAsGone is the status set of the count under the
// lock, which this record keeps from ADR 0409: a parcel that came back
// undelivered still counts and a canceled one does not.
func TestAReturnedParcelStillCountsAsGone(t *testing.T) {
	ctx := context.Background()
	reference := "order_gone_" + models.NewFulfillmentID()
	const line = "line_gone"

	svc := raceService(t, generousBound{}, nil)
	option := newOption(ctx, t, svc, newProfile(ctx, t, svc).ID, 2_500)
	open := func(key string, units int64) models.Fulfillment {
		ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
			Reference: reference, ShippingOptionID: option.ID, IdempotencyKey: reference + key,
			Items: []service.FulfillmentItemInput{{LineItemID: line, Quantity: units}},
		})
		require.NoError(t, err)

		return ful
	}

	returned := open("-returned", 2)
	_, err := svc.MarkShipped(ctx, returned.ID, "TRK-1", "")
	require.NoError(t, err)
	_, err = svc.MarkReturned(ctx, returned.ID)
	require.NoError(t, err)
	canceled := open("-canceled", 5)
	require.NoError(t, svc.CancelFulfillment(ctx, canceled.ID))

	held, err := svc.HeldForReferenceLocked(ctx, reference)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{line: 2}, held,
		"the returned parcel counts as gone and the canceled one does not")
}
