//go:build integration

package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// soldExpressOn places an order sold one delivery of 2,500 and returns it with
// the method's id.
func soldExpressOn(ctx context.Context, t *testing.T, svc *service.Service) (order models.Order, methodID string) {
	t.Helper()

	in := validInput()
	in.ShippingMethods = []service.CreateShippingMethodInput{
		{ShippingOptionID: "so_express", Name: "Express", Amount: in.ShippingTotal},
	}
	order, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.ShippingMethods, 1)

	return order, detail.ShippingMethods[0].ID
}

// changeTo is a quote for the given option.
func changeTo(methodID, optionID string, amount int64) service.ChangeDeliveryInput {
	return service.ChangeDeliveryInput{
		ShippingMethodID: methodID, ShippingOptionID: optionID, Name: optionID, Amount: amount,
	}
}

// TestACheaperDeliveryIsBookedAgainstShipping is ADR 0199 on the real schema:
// the change and its credit line are written together, and the journal books
// the credit as shipping given back rather than as a concession.
func TestACheaperDeliveryIsBookedAgainstShipping(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	from := time.Now().UTC().Add(-time.Second)
	order, methodID := soldExpressOn(ctx, t, svc)

	change, err := svc.ChangeDelivery(ctx, order.ID, changeTo(methodID, "so_pickup", 1000))
	require.NoError(t, err)
	require.NotNil(t, change)

	journal, err := svc.Journal(ctx, service.JournalQuery{
		From: from, To: time.Now().UTC().Add(time.Minute), CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	var kinds []models.JournalKind
	var receivable int64
	for _, entry := range journal.Entries {
		if entry.OrderID != order.ID {
			continue
		}
		kinds = append(kinds, entry.Kind)
		for _, line := range entry.Lines {
			if line.Account == models.AccountReceivable {
				receivable += line.Debit - line.Credit
			}
		}
		if entry.Kind == models.JournalDeliveryChanged {
			assert.Equal(t, change.ID, entry.ID, "the entry is the change's")
			assert.Equal(t, []models.JournalLine{
				{Account: models.AccountShipping, Debit: 1500},
				{Account: models.AccountReceivable, Credit: 1500},
			}, entry.Lines)
		}
	}
	assert.Equal(t, []models.JournalKind{models.JournalOrderPlaced, models.JournalDeliveryChanged}, kinds,
		"the credit line is booked once, as the change")
	assert.Equal(t, order.Total-1500, receivable)
}

// TestTheDeliveryChangeConstraintsAreTheLastDefence writes past the service,
// straight into the table, and each row the service would never write is
// refused.
func TestTheDeliveryChangeConstraintsAreTheLastDefence(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	order, methodID := soldExpressOn(ctx, t, svc)
	credit, err := svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{Amount: 100, Reason: "test"})
	require.NoError(t, err)

	insert := func(id string, difference int64, creditLineID *string) error {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO order_delivery_changes
			     (id, order_id, shipping_method_id, shipping_option_id, name, amount, difference, credit_line_id)
			 VALUES ($1, $2, $3, 'so_x', 'X', 0, $4, $5)`,
			id, order.ID, methodID, difference, creditLineID)

		return err
	}
	violation := func(err error, constraint string) {
		t.Helper()
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		assert.Equal(t, constraint, pgErr.ConstraintName)
	}

	violation(insert("odchg_dearer", 1, nil), "order_delivery_changes_costs_no_more")
	violation(insert("odchg_uncredited", -100, nil), "order_delivery_changes_credit_when_cheaper")
	violation(insert("odchg_free_credit", 0, &credit.ID), "order_delivery_changes_credit_when_cheaper")
	require.NoError(t, insert("odchg_first", -100, &credit.ID))
	violation(insert("odchg_second", -100, &credit.ID), "order_delivery_changes_credit_line_uniq")
}

// changeBarrierStore holds a delivery change after it has LOCKED the order and
// written its credit, and before it writes the change.
type changeBarrierStore struct {
	*repository.Repository
	locked  chan struct{}
	release chan struct{}
}

// CreateDeliveryChange announces that the order is locked and waits.
func (s *changeBarrierStore) CreateDeliveryChange(
	ctx context.Context, change models.DeliveryChange,
) (models.DeliveryChange, error) {
	close(s.locked)
	<-s.release

	return s.Repository.CreateDeliveryChange(ctx, change)
}

// TestTwoChangesAreEachPricedAgainstTheOneBefore is the race the order's lock
// exists for. The first change holds the lock with its credit written; the
// second has to WAIT, and then price itself against the first. Read without the
// lock, both are priced against the service the order was sold and the credits
// write off more shipping than the order charged.
func TestTwoChangesAreEachPricedAgainstTheOneBefore(t *testing.T) {
	ctx := context.Background()
	plain, _ := newService(t)
	order, methodID := soldExpressOn(ctx, t, plain)

	pool, firstPID := singleConnection(ctx, t)
	barrier := &changeBarrierStore{
		Repository: repository.New(pool.Pool()),
		locked:     make(chan struct{}),
		release:    make(chan struct{}),
	}
	first, _ := newServiceWithStore(t, barrier)

	firstDone := make(chan error, 1)
	go func() {
		_, err := first.ChangeDelivery(ctx, order.ID, changeTo(methodID, "so_economy", 1000))
		firstDone <- err
	}()
	select {
	case <-barrier.locked:
	case <-time.After(10 * time.Second):
		t.Fatal("the first change never locked the order")
	}

	type outcome struct {
		change *models.DeliveryChange
		err    error
	}
	secondDone := make(chan outcome, 1)
	go func() {
		change, err := plain.ChangeDelivery(ctx, order.ID, changeTo(methodID, "so_pickup", 400))
		secondDone <- outcome{change, err}
	}()

	require.Eventually(t, func() bool {
		waiters, err := lockWaiters(ctx, firstPID)
		return err == nil && waiters == 1
	}, 10*time.Second, 20*time.Millisecond, "the second change has to WAIT on the first one's lock")

	close(barrier.release)
	require.NoError(t, <-firstDone)
	second := <-secondDone
	require.NoError(t, second.err)
	require.NotNil(t, second.change)
	assert.Equal(t, int64(-600), second.change.Difference, "priced against 1,000, not against 2,500")

	detail, err := plain.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2100), detail.CreditedTotal, "2,500 sold less 400 now")
}
