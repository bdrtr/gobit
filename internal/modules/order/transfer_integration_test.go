//go:build integration

package order_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// An order changes hands on the real database (ADR 0450): the move, the
// withdrawn tokens, the event's stored moment, an addition moving with its
// order, the race between an acceptance and an operator's move, and the
// erasure of the holder asked.

// transferCustomers are the receiving customers and their addresses on record.
var transferCustomers = map[string]string{
	"cus_RECEIVER": "receiver@example.com",
	// Erased by its own test, so no other test's order is hers.
	"cus_ERASED_RECEIVER": "erased-receiver@example.com",
	"cus_OPERATOR":        "picked@example.com",
}

// mailbox keeps the tokens the requests mailed, by transfer.
type mailbox struct {
	mu     sync.Mutex
	tokens map[string]string
}

func (m *mailbox) Send(_ context.Context, _, _, reference, _ string, data map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[reference] = data[service.TransferMailToken]

	return nil
}

func (m *mailbox) token(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.tokens[id]
}

// customers answers the customer entity from transferCustomers.
type customers struct{}

func (customers) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	id, _ := spec.Filters[query.IDField].(string)
	email, ok := transferCustomers[id]
	if !ok {
		return []query.Record{}, nil
	}

	return []query.Record{{query.IDField: id, "email": email}}, nil
}

// newTransferService is the real store with the mailbox and the customers.
func newTransferService(t *testing.T) (*service.Service, *mailbox) {
	t.Helper()

	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = bus.Shutdown(ctx)
	})
	box := &mailbox{tokens: map[string]string{}}
	svc, err := service.New(service.Options{
		Repo: repository.New(testPool.Pool()), Events: bus, Mail: box, Catalog: customers{},
	})
	require.NoError(t, err)

	return svc, box
}

// guestInput is the standard order placed by a guest.
func guestInput() service.CreateOrderInput {
	in := validInput()
	in.CustomerID = ""
	in.Email = "guest@example.com"

	return in
}

// TestAnAcceptedTransferMovesTheOrderOnTheRealDatabase follows a guest's
// order into an account.
func TestAnAcceptedTransferMovesTheOrderOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, box := newTransferService(t)
	ord, err := svc.CreateOrder(ctx, guestInput())
	require.NoError(t, err)
	link, err := svc.IssueAccessToken(ctx, ord.ID)
	require.NoError(t, err)

	request, err := svc.RequestTransfer(ctx, ord.ID, "cus_RECEIVER")
	require.NoError(t, err)
	var stored []byte
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT token_hash FROM order_transfers WHERE id = $1`, request.ID).Scan(&stored))
	assert.Equal(t, models.AccessTokenDigest(box.token(request.ID)), stored, "only the digest is kept")

	accepted, err := svc.AcceptTransfer(ctx, request.ID, box.token(request.ID))
	require.NoError(t, err)

	var customerID, email string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT customer_id, email FROM orders WHERE id = $1`, ord.ID).Scan(&customerID, &email))
	assert.Equal(t, "cus_RECEIVER", customerID)
	assert.Equal(t, "receiver@example.com", email)

	access, err := svc.StorefrontAccess(ctx, ord.ID, link)
	require.NoError(t, err)
	assert.False(t, access.TokenMatches, "the guest's link opens the order no more")

	var moment string
	var closedAt time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
        SELECT e.data->>'transferred_at', t.closed_at
        FROM event_outbox e JOIN order_transfers t ON t.id = e.data->>'transfer_id'
        WHERE e.name = $1 AND e.data->>'order_id' = $2`,
		service.EventOrderTransferred, ord.ID).Scan(&moment, &closedAt))
	parsed, err := time.Parse(time.RFC3339Nano, moment)
	require.NoError(t, err)
	assert.True(t, parsed.Equal(closedAt), "the event carries the stored moment")
	assert.True(t, accepted.ClosedAt.Equal(closedAt))
}

// TestAnAdditionChangesHandsWithItsOrderOnTheRealDatabase proves the move's
// statement reaches the orders that add to the one moved.
func TestAnAdditionChangesHandsWithItsOrderOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTransferService(t)
	parent, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	in := validInput()
	in.AddsToOrderID = parent.ID
	addition, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	_, err = svc.MoveOrder(ctx, parent.ID, "cus_OPERATOR", testCustomerID)
	require.NoError(t, err)

	var customerID string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT customer_id FROM orders WHERE id = $1`, addition.ID).Scan(&customerID))
	assert.Equal(t, "cus_OPERATOR", customerID)

	var events int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM event_outbox WHERE name = $1 AND data->>'order_id' = ANY($2)`,
		service.EventOrderTransferred, []string{parent.ID, addition.ID}).Scan(&events))
	assert.Equal(t, 2, events)
}

// TestAnAcceptanceAndAnOperatorsMoveDoNotBothWin races the two writes on one
// order: the order's lock serializes them, so exactly one moves it and the
// order ends with that one's customer.
func TestAnAcceptanceAndAnOperatorsMoveDoNotBothWin(t *testing.T) {
	ctx := context.Background()
	svc, box := newTransferService(t)

	for range 10 {
		ord, err := svc.CreateOrder(ctx, guestInput())
		require.NoError(t, err)
		request, err := svc.RequestTransfer(ctx, ord.ID, "cus_RECEIVER")
		require.NoError(t, err)

		var wg sync.WaitGroup
		var acceptErr, moveErr error
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, acceptErr = svc.AcceptTransfer(ctx, request.ID, box.token(request.ID))
		}()
		go func() {
			defer wg.Done()
			<-start
			_, moveErr = svc.MoveOrder(ctx, ord.ID, "cus_OPERATOR", "")
		}()
		close(start)
		wg.Wait()

		require.True(t, (acceptErr == nil) != (moveErr == nil),
			"exactly one write moves the order: accept=%v move=%v", acceptErr, moveErr)
		var customerID string
		require.NoError(t, testPool.Pool().QueryRow(ctx,
			`SELECT customer_id FROM orders WHERE id = $1`, ord.ID).Scan(&customerID))
		if acceptErr == nil {
			assert.Equal(t, "cus_RECEIVER", customerID)
			assert.Equal(t, service.CodeTransferCustomerRevised, errors.CodeOf(moveErr))
		} else {
			assert.Equal(t, "cus_OPERATOR", customerID)
			assert.Equal(t, service.CodeTransferClosed, errors.CodeOf(acceptErr))
		}
	}
}

// TestAnErasureEmptiesTheAddressATransferAskedOnTheRealDatabase forgets the
// guest who gave an order away, on an order that is now somebody else's.
func TestAnErasureEmptiesTheAddressATransferAskedOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, box := newTransferService(t)
	in := guestInput()
	in.Email = "gave-away@example.com"
	ord, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	request, err := svc.RequestTransfer(ctx, ord.ID, "cus_RECEIVER")
	require.NoError(t, err)
	_, err = svc.AcceptTransfer(ctx, request.ID, box.token(request.ID))
	require.NoError(t, err)

	result, err := svc.Erase(ctx, personaldata.Subject{Email: "gave-away@example.com"})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Rows)

	var fromEmail *string
	var toCustomer, orderEmail string
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
        SELECT t.from_email, t.to_customer_id, o.email
        FROM order_transfers t JOIN orders o ON o.id = t.order_id
        WHERE t.id = $1`, request.ID).Scan(&fromEmail, &toCustomer, &orderEmail))
	assert.Nil(t, fromEmail, "the address asked is emptied")
	assert.Equal(t, "cus_RECEIVER", toCustomer, "the ids are kept, as orders.customer_id is")
	assert.Equal(t, "receiver@example.com", orderEmail, "the receiver's order is not theirs to erase")
}

// TestAnErasedReceiversRequestIsWithdrawnOnTheRealDatabase proves the
// statement behind ADR 0459's withdrawal: the request waiting for the erased
// customer is closed canceled, and its holder's token moves nothing.
func TestAnErasedReceiversRequestIsWithdrawnOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, box := newTransferService(t)
	ord, err := svc.CreateOrder(ctx, guestInput())
	require.NoError(t, err)
	request, err := svc.RequestTransfer(ctx, ord.ID, "cus_ERASED_RECEIVER")
	require.NoError(t, err)

	result, err := svc.Erase(ctx, personaldata.Subject{CustomerID: "cus_ERASED_RECEIVER"})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Rows, "the request names the receiver")
	again, err := svc.Erase(ctx, personaldata.Subject{CustomerID: "cus_ERASED_RECEIVER"})
	require.NoError(t, err)
	assert.Equal(t, 1, again.Rows, "a closed request still names them, and is counted again")

	var status string
	var closedAt *time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT status, closed_at FROM order_transfers WHERE id = $1`, request.ID).Scan(&status, &closedAt))
	assert.Equal(t, string(models.TransferCanceled), status)
	assert.NotNil(t, closedAt, "a withdrawn request is closed")

	_, err = svc.AcceptTransfer(ctx, request.ID, box.token(request.ID))
	assert.Equal(t, service.CodeTransferClosed, errors.CodeOf(err))
	var customerID *string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT customer_id FROM orders WHERE id = $1`, ord.ID).Scan(&customerID))
	assert.Nil(t, customerID, "the order stays the guest's")
}

// TestTheTransferTableRefusesAShapeTheServiceNeverWrites holds the CHECKs.
func TestTheTransferTableRefusesAShapeTheServiceNeverWrites(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTransferService(t)
	ord, err := svc.CreateOrder(ctx, guestInput())
	require.NoError(t, err)

	for name, statement := range map[string]string{
		"a request without its token": `INSERT INTO order_transfers (id, order_id, status, requested_by, to_customer_id, expires_at)
            VALUES ('otr_check1', $1, 'requested', 'customer', 'cus_A', now())`,
		"an operator's move left waiting": `INSERT INTO order_transfers (id, order_id, status, requested_by, to_customer_id)
            VALUES ('otr_check2', $1, 'requested', 'operator', 'cus_A')`,
		"a transfer to the holder": `INSERT INTO order_transfers (id, order_id, status, requested_by, from_customer_id, to_customer_id, closed_at)
            VALUES ('otr_check3', $1, 'accepted', 'operator', 'cus_A', 'cus_A', now())`,
		"a short digest": `INSERT INTO order_transfers (id, order_id, status, requested_by, to_customer_id, token_hash, expires_at)
            VALUES ('otr_check4', $1, 'requested', 'customer', 'cus_A', '\x00', now())`,
	} {
		_, err := testPool.Pool().Exec(ctx, statement, ord.ID)
		assert.Error(t, err, name)
	}
}
