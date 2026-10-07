//go:build integration

package fulfillment_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// gatedBound answers a fixed bound, and answers no caller until every caller it
// was built for has asked.
//
// The gate is what opens the window gap D265 is about: each parcel reads what
// the order owes before either of them has written a unit, so both see the
// whole of it. Without the gate the second open usually starts after the first
// one has committed and the race goes unseen.
type gatedBound struct {
	owed    map[string]int64
	arrived sync.WaitGroup
}

// newGatedBound is a bound of owed that answers once callers have asked.
func newGatedBound(owed map[string]int64, callers int) *gatedBound {
	b := &gatedBound{owed: owed}
	b.arrived.Add(callers)

	return b
}

// DispatchCeilings answers the fixed ceiling once every caller has asked.
func (b *gatedBound) DispatchCeilings(
	_ context.Context, _ string, lineItemIDs []string,
) (ceilings, spoken map[string]int64, err error) {
	b.arrived.Done()
	b.arrived.Wait()

	out := make(map[string]int64, len(b.owed))
	for line, units := range b.owed {
		if len(lineItemIDs) == 0 || containsLine(lineItemIDs, line) {
			out[line] = units
		}
	}

	return out, nil, nil
}

// ReturnLines awaits nothing; these parcels go out.
func (b *gatedBound) ReturnLines(
	context.Context, string, string,
) (awaited bool, lines map[string]int64, err error) {
	return false, nil, nil
}

// containsLine reports whether lines names line.
func containsLine(lines []string, line string) bool {
	for _, l := range lines {
		if l == line {
			return true
		}
	}

	return false
}

// TestTwoParcelsOpenedAtOnceHoldNoMoreThanTheOrderOwes is gap D265 on the real
// schema (ADR 0409): an order owes three units of a line, and two parcels of two
// units each are opened for it at the same moment under different keys. Each one
// read the bound before the other wrote, so each alone fits; together they
// would hold four. Exactly one opens, the other is refused, and the units the
// order's live parcels hold stay within what it owes.
func TestTwoParcelsOpenedAtOnceHoldNoMoreThanTheOrderOwes(t *testing.T) {
	ctx := context.Background()
	reference := "order_race_" + models.NewFulfillmentID()
	const line = "line_raced"

	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	svc, err := service.New(service.Options{
		Store: repo, Providers: registry,
		DispatchBound: newGatedBound(map[string]int64{line: 3}, 2),
	})
	require.NoError(t, err)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)

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
	assert.Equal(t, 1, refused, "the other is refused as more than the order owes")

	var held int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COALESCE(SUM(i.quantity), 0) FROM fulfillment_items i
		   JOIN fulfillments f ON f.id = i.fulfillment_id
		  WHERE f.reference = $1 AND f.status <> 'canceled'`, reference).Scan(&held))
	assert.LessOrEqual(t, held, int64(3), "the order's live parcels hold more units than it owes")
}
