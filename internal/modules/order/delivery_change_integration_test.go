//go:build integration

package order_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// soldExpressOn places an order sold one delivery of 2,500 and returns it with
// the method's id.
func soldExpressOn(ctx context.Context, t *testing.T, svc *service.Service) (placed models.Order, methodID string) {
	t.Helper()

	in := validInput()
	in.ShippingMethods = []service.CreateShippingMethodInput{
		{ShippingOptionID: "so_express", Name: "Express", Amount: in.ShippingTotal},
	}
	placed, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	require.Len(t, detail.ShippingMethods, 1)

	return placed, detail.ShippingMethods[0].ID
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
	placed, methodID := soldExpressOn(ctx, t, svc)

	change, err := svc.ChangeDelivery(ctx, placed.ID, changeTo(methodID, "so_pickup", 1000))
	require.NoError(t, err)
	require.NotNil(t, change)

	journal, err := svc.Journal(ctx, service.JournalQuery{
		From: from, To: time.Now().UTC().Add(time.Minute), CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	var kinds []models.JournalKind
	var receivable int64
	for _, entry := range journal.Entries {
		if entry.OrderID != placed.ID {
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
	assert.Equal(t, placed.Total-1500, receivable)
}

// TestTheDeliveryChangeConstraintsAreTheLastDefence writes past the service,
// straight into the table, and each row the service would never write is
// refused.
func TestTheDeliveryChangeConstraintsAreTheLastDefence(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	placed, methodID := soldExpressOn(ctx, t, svc)
	credit, err := svc.CreateCreditLine(ctx, placed.ID, service.CreateCreditLineInput{Amount: 100, Reason: "test"})
	require.NoError(t, err)

	insert := func(id string, difference int64, creditLineID, collectionID *string) error {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO order_delivery_changes
			     (id, order_id, shipping_method_id, shipping_option_id, name, amount, difference,
			      credit_line_id, payment_collection_id)
			 VALUES ($1, $2, $3, 'so_x', 'X', 0, $4, $5, $6)`,
			id, placed.ID, methodID, difference, creditLineID, collectionID)

		return err
	}
	violation := func(err error, constraint string) {
		t.Helper()
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		assert.Equal(t, constraint, pgErr.ConstraintName)
	}

	violation(insert("odchg_dearer", 1, nil, nil), "order_delivery_changes_paid_when_dearer")
	violation(insert("odchg_uncredited", -100, nil, nil), "order_delivery_changes_credit_when_cheaper")
	violation(insert("odchg_free_credit", 0, &credit.ID, nil), "order_delivery_changes_credit_when_cheaper")
	collection := "pay_col_constraint"
	violation(insert("odchg_paid_for_nothing", 0, nil, &collection), "order_delivery_changes_paid_when_dearer")
	require.NoError(t, insert("odchg_first", -100, &credit.ID, nil))
	violation(insert("odchg_second", -100, &credit.ID, nil), "order_delivery_changes_credit_line_uniq")
	require.NoError(t, insert("odchg_paid", 1, nil, &collection))
	violation(insert("odchg_paid_twice", 1, nil, &collection), "order_delivery_changes_payment_collection_uniq")
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
	placed, methodID := soldExpressOn(ctx, t, plain)

	pool, firstPID := singleConnection(ctx, t)
	barrier := &changeBarrierStore{
		Repository: repository.New(pool.Pool()),
		locked:     make(chan struct{}),
		release:    make(chan struct{}),
	}
	first, _ := newServiceWithStore(t, barrier)

	firstDone := make(chan error, 1)
	go func() {
		_, err := first.ChangeDelivery(ctx, placed.ID, changeTo(methodID, "so_economy", 1000))
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
		change, err := plain.ChangeDelivery(ctx, placed.ID, changeTo(methodID, "so_pickup", 400))
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

	detail, err := plain.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2100), detail.CreditedTotal, "2,500 sold less 400 now")
}

// paidChangeTo is a quote for the option paid on the collection.
func paidChangeTo(methodID, optionID string, amount int64, collectionID string, paid int64) service.ChangeDeliveryInput {
	in := changeTo(methodID, optionID, amount)
	in.PaymentCollectionID, in.Paid = collectionID, paid

	return in
}

// TestADearerDeliveryIsBookedAsShippingOwed is ADR 0200 on the real schema: the
// paid change names its collection, and the journal books the difference as
// receivable against shipping, which the payment journal's capture credits.
func TestADearerDeliveryIsBookedAsShippingOwed(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	from := time.Now().UTC().Add(-time.Second)
	placed, methodID := soldExpressOn(ctx, t, svc)
	collectionID := fmt.Sprintf("pay_col_booked_%d", time.Now().UnixNano())

	change, err := svc.ChangeDelivery(ctx, placed.ID, paidChangeTo(methodID, "so_same_day", 4000, collectionID, 1500))
	require.NoError(t, err)
	require.NotNil(t, change)
	assert.Equal(t, collectionID, change.PaymentCollectionID)

	journal, err := svc.Journal(ctx, service.JournalQuery{
		From: from, To: time.Now().UTC().Add(time.Minute), CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	var kinds []models.JournalKind
	for _, entry := range journal.Entries {
		if entry.OrderID != placed.ID {
			continue
		}
		kinds = append(kinds, entry.Kind)
		if entry.Kind == models.JournalDeliveryUpgraded {
			assert.Equal(t, change.ID, entry.ID)
			assert.Equal(t, []models.JournalLine{
				{Account: models.AccountReceivable, Debit: 1500},
				{Account: models.AccountShipping, Credit: 1500},
			}, entry.Lines)
		}
	}
	assert.Equal(t, []models.JournalKind{models.JournalOrderPlaced, models.JournalDeliveryUpgraded}, kinds)
}

// deliveryBarrierStore holds a delivery change after it has locked the order
// and found its collection free, and before it writes the change.
type deliveryBarrierStore struct {
	*repository.Repository
	locked  chan struct{}
	release chan struct{}
}

// CreateDeliveryChange announces the lock and waits.
func (s *deliveryBarrierStore) CreateDeliveryChange(
	ctx context.Context, change models.DeliveryChange,
) (models.DeliveryChange, error) {
	close(s.locked)
	<-s.release

	return s.Repository.CreateDeliveryChange(ctx, change)
}

// TestAnExchangeWaitsForAChangeTakingItsCollection is D142's race. A delivery
// change holds the order's lock with a collection it found free; an exchange
// of the same order naming that collection has to WAIT on the order's lock,
// and then find the collection taken. Locking the exchange alone, it would
// read the collection free before the change commits and fund itself with
// money the change took.
func TestAnExchangeWaitsForAChangeTakingItsCollection(t *testing.T) {
	ctx := context.Background()
	plain, _ := newService(t)
	placed, methodID := soldExpressOn(ctx, t, plain)
	exchange, err := plain.CreateExchange(ctx, service.CreateExchangeInput{OrderID: placed.ID, DifferenceDue: 1500})
	require.NoError(t, err)
	collectionID := fmt.Sprintf("pay_col_race_%d", time.Now().UnixNano())

	pool, changerPID := singleConnection(ctx, t)
	barrier := &deliveryBarrierStore{
		Repository: repository.New(pool.Pool()),
		locked:     make(chan struct{}),
		release:    make(chan struct{}),
	}
	changer, _ := newServiceWithStore(t, barrier)

	changed := make(chan error, 1)
	go func() {
		_, err := changer.ChangeDelivery(ctx, placed.ID, paidChangeTo(methodID, "so_same_day", 4000, collectionID, 1500))
		changed <- err
	}()
	select {
	case <-barrier.locked:
	case <-time.After(10 * time.Second):
		t.Fatal("the delivery change never locked the order")
	}

	funded := make(chan error, 1)
	go func() {
		_, err := plain.FundExchange(ctx, exchange.ID, collectionID)
		funded <- err
	}()
	require.Eventually(t, func() bool {
		waiters, err := lockWaiters(ctx, changerPID)
		return err == nil && waiters == 1
	}, 10*time.Second, 20*time.Millisecond, "the exchange's funding has to WAIT on the order's lock")

	close(barrier.release)
	require.NoError(t, <-changed)
	err = <-funded
	require.Error(t, err, "the exchange was funded with the money a delivery change took")
	assert.Equal(t, service.CodeCollectionTaken, errors.CodeOf(err))
}

// TestARollbackRefusesADatabaseHoldingAPaidDelivery keeps 000026's down
// migration from dropping which collection paid for a change.
func TestARollbackRefusesADatabaseHoldingAPaidDelivery(t *testing.T) {
	ctx := context.Background()
	dsn, pool := isolatedDatabase(ctx, t, "order_paid_delivery_rollback")
	svc, _ := newServiceWithStore(t, repository.New(pool.Pool()))
	placed, methodID := soldExpressOn(ctx, t, svc)
	_, err := svc.ChangeDelivery(ctx, placed.ID, paidChangeTo(methodID, "so_same_day", 4000, "pay_col_rollback", 1500))
	require.NoError(t, err)

	err = rollBackThrough(ctx, t, dsn, 26)

	require.Error(t, err, "the rollback dropped the collection a change was paid with")
	// The server's report quotes the constraint; the bare name is also in the
	// migration's own text, which the error carries (D148).
	assert.Contains(t, err.Error(), `check constraint "order_delivery_changes_costs_no_more"`)
}

// TestAChangeRefusesACollectionAnExchangeTook reads the exchanges' column on
// the real schema: money that funded an exchange pays for no delivery.
func TestAChangeRefusesACollectionAnExchangeTook(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	placed, methodID := soldExpressOn(ctx, t, svc)
	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: placed.ID, DifferenceDue: 1500})
	require.NoError(t, err)
	collectionID := fmt.Sprintf("pay_col_exchange_%d", time.Now().UnixNano())
	_, err = svc.FundExchange(ctx, exchange.ID, collectionID)
	require.NoError(t, err)

	_, err = svc.ChangeDelivery(ctx, placed.ID, paidChangeTo(methodID, "so_same_day", 4000, collectionID, 1500))

	require.Error(t, err)
	assert.Equal(t, service.CodeCollectionTaken, errors.CodeOf(err))
}

// heldLockStore holds the first order lock asked for until it is released,
// AFTER the transaction asking for it has begun: the shape of a change that
// waited on the lock another change held.
type heldLockStore struct {
	*repository.Repository
	holding sync.Once
	arrived chan struct{}
	release chan struct{}
}

// LockOrder holds the first call, then takes the lock.
func (s *heldLockStore) LockOrder(ctx context.Context, id string) (models.Order, error) {
	s.holding.Do(func() {
		close(s.arrived)
		<-s.release
	})

	return s.Repository.LockOrder(ctx, id)
}

// TestTheChangeMadeLastIsTheCurrentDelivery is the reproduction of ADR 0241:
// a change whose transaction began first and waited on the order's lock is
// written after the change that held it, and it is the current delivery. Its
// row was stamped with the moment its transaction BEGAN, before the other's,
// so the reads put it first and took the other for the current one, while the
// money the waiting change booked was reckoned from the other's amount.
func TestTheChangeMadeLastIsTheCurrentDelivery(t *testing.T) {
	ctx := context.Background()
	plain, _ := newService(t)
	placed, methodID := soldExpressOn(ctx, t, plain)

	held := &heldLockStore{
		Repository: repository.New(testPool.Pool()),
		arrived:    make(chan struct{}), release: make(chan struct{}),
	}
	waiting, _ := newServiceWithStore(t, held)

	done := make(chan error, 1)
	go func() {
		_, err := waiting.ChangeDelivery(ctx, placed.ID, changeTo(methodID, "so_last", 1000))
		done <- err
	}()
	<-held.arrived

	_, err := plain.ChangeDelivery(ctx, placed.ID, changeTo(methodID, "so_first", 1500))
	require.NoError(t, err, "the other change holds the lock and finishes")
	close(held.release)
	require.NoError(t, <-done)

	detail, err := plain.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	require.Len(t, detail.DeliveryChanges, 2)
	assert.Equal(t, "so_first", detail.DeliveryChanges[0].ShippingOptionID, "the changes read in the order they were made")
	assert.Equal(t, "so_last", detail.DeliveryChanges[1].ShippingOptionID)
	current := models.CurrentDeliveries(detail.ShippingMethods, detail.DeliveryChanges)
	require.Len(t, current, 1)
	assert.Equal(t, "so_last", current[0].ShippingOptionID, "the change made last is the delivery")
	var last models.DeliveryChange
	for _, change := range detail.DeliveryChanges {
		if change.ShippingOptionID == "so_last" {
			last = change
		}
	}
	require.Equal(t, int64(-500), last.Difference,
		"precondition: the waiting change reckoned its difference from the other's amount")
}
