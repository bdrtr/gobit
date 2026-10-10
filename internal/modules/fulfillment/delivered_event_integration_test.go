//go:build integration

package fulfillment_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestAParcelsMovesArePromisedWithTheStoredMoment is ADR 0437 and D278 over
// the real schema: each of two parcels of one order writes its own
// fulfillment.delivered into event_outbox in the delivery's transaction, a
// canceled parcel its fulfillment.canceled and one that came back its
// fulfillment.returned, and each carries, at full precision, the moment the
// column holds. A repeated delivery, a collection reported after it and a
// refused delivery write nothing.
func TestAParcelsMovesArePromisedWithTheStoredMoment(t *testing.T) {
	ctx := context.Background()
	svc := newServiceWithBus(t)
	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 1_000)
	reference := "order_" + models.NewFulfillmentID()

	open := func(key string) models.Fulfillment {
		t.Helper()
		ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
			Reference: reference, ShippingOptionID: option.ID, IdempotencyKey: key + "-" + reference,
		})
		require.NoError(t, err)

		return ful
	}
	shippedFirst, deliveredFirst := open("shipped"), open("delivered")
	canceled, cameBack := open("canceled"), open("came-back")

	_, err := svc.MarkShipped(ctx, shippedFirst.ID, shippedFirst.TrackingNumber, "")
	require.NoError(t, err)
	_, err = svc.MarkDelivered(ctx, shippedFirst.ID)
	require.NoError(t, err)
	_, err = svc.MarkDelivered(ctx, deliveredFirst.ID)
	require.NoError(t, err)
	_, err = svc.MarkDelivered(ctx, deliveredFirst.ID)
	require.NoError(t, err, "a repeated delivery is absorbed")
	_, err = svc.MarkShipped(ctx, deliveredFirst.ID, deliveredFirst.TrackingNumber, "")
	require.NoError(t, err, "a collection reported after the delivery is a late message")
	require.NoError(t, svc.CancelFulfillment(ctx, canceled.ID))
	_, err = svc.MarkDelivered(ctx, canceled.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err), "a canceled parcel is not delivered")
	_, err = svc.MarkShipped(ctx, cameBack.ID, cameBack.TrackingNumber, "")
	require.NoError(t, err)
	_, err = svc.MarkReturned(ctx, cameBack.ID)
	require.NoError(t, err)

	// The moment is compared in Go, at full precision: a comparison in SQL
	// would round the payload's text to the column's microseconds and pass a
	// moment no reader of the parcel sees.
	rows, err := testPool.Pool().Query(ctx, `
        SELECT e.id,
               COALESCE(e.data->>'delivered_at', e.data->>'canceled_at', e.data->>'returned_at'),
               CASE e.name
                   WHEN $2 THEN f.delivered_at
                   WHEN $3 THEN f.canceled_at
                   WHEN $4 THEN f.returned_at
               END
        FROM event_outbox e
        JOIN fulfillments f ON f.id = e.data->>'fulfillment_id'
        WHERE e.data->>'reference' = $1
        ORDER BY e.id`,
		reference, service.EventFulfillmentDelivered, service.EventFulfillmentCanceled,
		service.EventFulfillmentReturned)
	require.NoError(t, err)
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		var text *string
		var column *time.Time
		require.NoError(t, rows.Scan(&id, &text, &column))
		ids = append(ids, id)
		if !assert.NotNil(t, text, "%s carries no moment", id) || !assert.NotNil(t, column, "%s's column is empty", id) {
			continue
		}
		moment, parseErr := time.Parse(time.RFC3339Nano, *text)
		require.NoError(t, parseErr)
		assert.True(t, moment.Equal(*column), "%s carries %s and the column holds %s", id, moment, *column)
	}
	require.NoError(t, rows.Err())

	assert.ElementsMatch(t, []string{
		service.EventFulfillmentDelivered + ":" + shippedFirst.ID,
		service.EventFulfillmentDelivered + ":" + deliveredFirst.ID,
		service.EventFulfillmentCanceled + ":" + canceled.ID,
		service.EventFulfillmentReturned + ":" + cameBack.ID,
	}, ids, "one row per move, and none for the repeat, the late collection or the refusal")
}

// newServiceWithBus is [newService] with an event bus, which is what makes the
// service write its events into the outbox.
//
// Its clock always reads 999 nanoseconds past a microsecond, so a payload built
// from the clock rather than from the row differs from the column on every
// parcel.
func newServiceWithBus(t *testing.T) *service.Service {
	t.Helper()

	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() { _ = bus.Shutdown(context.Background()) })

	svc, err := service.New(service.Options{
		Store: repo, Providers: registry, DispatchBound: generousBound{}, Events: bus,
		Clock: func() time.Time { return time.Now().Truncate(time.Microsecond).Add(999 * time.Nanosecond) },
	})
	require.NoError(t, err)

	return svc
}
