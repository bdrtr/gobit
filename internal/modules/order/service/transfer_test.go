package service_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// The receiver of the transfers below and their address on record.
const (
	transferReceiver      = "cus_RECEIVER"
	transferReceiverEmail = "receiver@example.com"
)

// sentMail is one message the fake mail was handed.
type sentMail struct {
	template, channel, reference, to string
	data                             map[string]string
}

// fakeTransferMail records what a request mails, and fails when err is set.
type fakeTransferMail struct {
	mu   sync.Mutex
	sent []sentMail
	err  error
}

func (m *fakeTransferMail) Send(
	_ context.Context, template, channel, reference, to string, data map[string]string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{template, channel, reference, to, data})

	return m.err
}

// customerCatalog answers the customer entity from a map of addresses.
type customerCatalog struct {
	emails map[string]string
}

func (c customerCatalog) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	if spec.Entity != service.EntityCustomer {
		return nil, errors.Internal("unexpected_entity", "asked for %s", spec.Entity)
	}
	id, _ := spec.Filters[query.IDField].(string)
	email, ok := c.emails[id]
	if !ok {
		return []query.Record{}, nil
	}

	return []query.Record{{query.IDField: id, "email": email}}, nil
}

// transferEnv is a service with the fake store, mail, catalog and a clock the
// test moves.
type transferEnv struct {
	env
	mail *fakeTransferMail
	now  *time.Time
	// customers are the customer module's addresses on record, which a test
	// rewrites as that module's erasure does.
	customers map[string]string
}

func newTransferEnv(t *testing.T) transferEnv {
	t.Helper()

	base := newEnv(t)
	mail := &fakeTransferMail{}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	customers := map[string]string{
		transferReceiver: "Receiver@Example.com", testCustomerID: "customer@example.com",
	}
	svc, err := service.New(service.Options{
		Repo: base.store, Events: base.bus, Mail: mail,
		Catalog: customerCatalog{emails: customers},
		Now:     func() time.Time { return now },
	})
	require.NoError(t, err)
	base.svc = svc

	return transferEnv{env: base, mail: mail, now: &now, customers: customers}
}

// guestOrder places an order with no customer, the case a transfer is for.
func (e transferEnv) guestOrder(t *testing.T) models.Order {
	t.Helper()

	in := validInput()
	in.CustomerID = ""
	order, err := e.svc.CreateOrder(context.Background(), in)
	require.NoError(t, err)

	return order
}

// requested asks for the order for the receiver and returns the request and
// the token the mail carried.
func (e transferEnv) requested(t *testing.T, orderID string) (transfer models.OrderTransfer, token string) {
	t.Helper()

	transfer, err := e.svc.RequestTransfer(context.Background(), orderID, transferReceiver)
	require.NoError(t, err)
	require.NotEmpty(t, e.mail.sent)

	return transfer, e.mail.sent[len(e.mail.sent)-1].data[service.TransferMailToken]
}

// TestARequestIsMailedToTheOrdersAddress proves who is asked and with what.
func TestARequestIsMailedToTheOrdersAddress(t *testing.T) {
	e := newTransferEnv(t)
	order := e.guestOrder(t)

	transfer, token := e.requested(t, order.ID)

	assert.Equal(t, models.TransferRequested, transfer.Status)
	assert.Equal(t, models.TransferByCustomer, transfer.RequestedBy)
	assert.Empty(t, transfer.FromCustomerID, "a guest's order had no customer")
	assert.Equal(t, transferReceiver, transfer.ToCustomerID)
	assert.Equal(t, order.Email, transfer.FromEmail)
	require.NotNil(t, transfer.ExpiresAt)
	assert.Equal(t, e.now.Add(service.TransferTTL), *transfer.ExpiresAt)

	require.Len(t, e.mail.sent, 1)
	mail := e.mail.sent[0]
	assert.Equal(t, service.TemplateTransferRequested, mail.template)
	assert.Equal(t, coreprovider.ChannelEmail, mail.channel)
	assert.Equal(t, transfer.ID, mail.reference, "a request is mailed once, keyed on itself")
	assert.Equal(t, order.Email, mail.to, "the ORDER's address is asked, not the receiver's")
	assert.Equal(t, order.ID, mail.data[service.TransferMailOrderID])
	assert.Equal(t, "1", mail.data[service.TransferMailDisplayID])
	assert.Equal(t, transfer.ID, mail.data[service.TransferMailTransferID])
	assert.True(t, strings.HasPrefix(token, models.TransferTokenPrefix))
	assert.True(t, transfer.TransferTokenMatches(token), "the transfer keeps the mailed token's digest")
	assert.NotContains(t, string(transfer.TokenHash), token, "the plaintext is stored nowhere")

	assert.Empty(t, e.bus.eventsNamed(service.EventOrderTransferred), "a request moves nothing")
}

// TestAnAcceptanceMovesTheOrderAndWithdrawsItsLinks is the transfer itself.
func TestAnAcceptanceMovesTheOrderAndWithdrawsItsLinks(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	order := e.guestOrder(t)
	link, err := e.svc.IssueAccessToken(ctx, order.ID)
	require.NoError(t, err)
	transfer, token := e.requested(t, order.ID)

	accepted, err := e.svc.AcceptTransfer(ctx, transfer.ID, token)
	require.NoError(t, err)

	assert.Equal(t, models.TransferAccepted, accepted.Status)
	require.NotNil(t, accepted.ClosedAt)
	moved, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, transferReceiver, moved.CustomerID)
	assert.Equal(t, transferReceiverEmail, moved.Email,
		"the order's mails go to the receiver's address on record, folded")

	access, err := e.svc.StorefrontAccess(ctx, order.ID, link)
	require.NoError(t, err)
	assert.False(t, access.TokenMatches, "the previous holder's link opens the order no more")
	assert.Equal(t, transferReceiver, access.CustomerID, "the receiver opens it by proof")

	events := e.bus.eventsNamed(service.EventOrderTransferred)
	require.Len(t, events, 1)
	assert.Equal(t, map[string]any{
		service.EventFieldOrderID:       order.ID,
		service.EventFieldTransferID:    transfer.ID,
		service.EventFieldTransferredAt: accepted.ClosedAt.UTC().Format(time.RFC3339Nano),
	}, events[0].Data, "the event names no customer and no address")
	assert.Contains(t, e.store.outbox, events[0].ID, "the event is in the outbox with the move")

	again, err := e.svc.AcceptTransfer(ctx, transfer.ID, token)
	require.NoError(t, err, "a repeated click answers the accepted transfer")
	assert.Equal(t, accepted.ID, again.ID)
	assert.Len(t, e.bus.eventsNamed(service.EventOrderTransferred), 1, "and moves nothing again")
}

// TestAnAdditionMovesWithItsOrder proves an order that adds to the moved one
// follows it, and is refused on its own.
func TestAnAdditionMovesWithItsOrder(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	parent := placeOrder(t, e.env)
	in := validInput()
	in.AddsToOrderID = parent.ID
	addition, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	_, err = e.svc.RequestTransfer(ctx, addition.ID, transferReceiver)
	assert.Equal(t, service.CodeTransferAddition, errors.CodeOf(err))

	moved, err := e.svc.MoveOrder(ctx, parent.ID, transferReceiver, testCustomerID)
	require.NoError(t, err)
	assert.Equal(t, models.TransferByOperator, moved.RequestedBy)

	after, err := e.svc.GetOrder(ctx, addition.ID)
	require.NoError(t, err)
	assert.Equal(t, transferReceiver, after.CustomerID)
	assert.Len(t, e.bus.eventsNamed(service.EventOrderTransferred), 2, "one event per moved order")
}

// TestATokenThatIsNotTheTransfersOpensNothing proves a wrong token answers as
// an unknown transfer, for an accept and a decline alike.
func TestATokenThatIsNotTheTransfersOpensNothing(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	transfer, token := e.requested(t, e.guestOrder(t).ID)

	for _, answer := range []func(context.Context, string, string) (models.OrderTransfer, error){
		e.svc.AcceptTransfer, e.svc.DeclineTransfer,
	} {
		_, err := answer(ctx, transfer.ID, token+"x")
		assert.Equal(t, service.CodeTransferNotFound, errors.CodeOf(err))
		_, err = answer(ctx, transfer.ID, "")
		assert.Equal(t, service.CodeTransferNotFound, errors.CodeOf(err))
		_, err = answer(ctx, "otr_NOPE", token)
		assert.True(t, errors.IsNotFound(err))
	}
	still, err := e.store.GetTransfer(ctx, transfer.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TransferRequested, still.Status)
}

// TestARequestIsRefusedWhatCannotChangeHands walks the request's refusals.
func TestARequestIsRefusedWhatCannotChangeHands(t *testing.T) {
	ctx := context.Background()

	t.Run("a customer's order", func(t *testing.T) {
		e := newTransferEnv(t)
		order := placeOrder(t, e.env)
		for _, receiver := range []string{transferReceiver, testCustomerID} {
			_, err := e.svc.RequestTransfer(ctx, order.ID, receiver)
			assert.Equal(t, service.CodeNotTransferable, errors.CodeOf(err), receiver)
		}
		assert.Empty(t, e.mail.sent, "the address typed at a customer's checkout is not asked")
		assert.Equal(t, testCustomerID, e.store.orders[order.ID].CustomerID)
	})
	t.Run("a canceled order", func(t *testing.T) {
		e := newTransferEnv(t)
		order := e.guestOrder(t)
		err := e.svc.CancelOrder(ctx, order.ID, "test")
		require.NoError(t, err)
		_, err = e.svc.RequestTransfer(ctx, order.ID, transferReceiver)
		assert.Equal(t, service.CodeNotTransferable, errors.CodeOf(err))
	})
	t.Run("an order with no address to ask", func(t *testing.T) {
		e := newTransferEnv(t)
		in := validInput()
		in.CustomerID, in.Email = "", ""
		order, err := e.svc.CreateOrder(ctx, in)
		require.NoError(t, err)
		_, err = e.svc.RequestTransfer(ctx, order.ID, transferReceiver)
		assert.Equal(t, service.CodeNotTransferable, errors.CodeOf(err))
		assert.Empty(t, e.mail.sent)
	})
	t.Run("a second request while one waits", func(t *testing.T) {
		e := newTransferEnv(t)
		order := e.guestOrder(t)
		e.requested(t, order.ID)
		_, err := e.svc.RequestTransfer(ctx, order.ID, "cus_OTHER")
		assert.Equal(t, service.CodeTransferPending, errors.CodeOf(err))

		*e.now = e.now.Add(service.TransferTTL)
		_, err = e.svc.RequestTransfer(ctx, order.ID, "cus_OTHER")
		assert.NoError(t, err, "a request past its bound waits no more")
	})
	t.Run("no mail bound", func(t *testing.T) {
		base := newEnv(t)
		order := placeOrder(t, base)
		_, err := base.svc.RequestTransfer(ctx, order.ID, transferReceiver)
		assert.Equal(t, service.CodeTransferMailFailed, errors.CodeOf(err))
	})
}

// TestARequestWhoseMailFailsIsWithdrawn proves no request waits that its
// holder was never asked.
func TestARequestWhoseMailFailsIsWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	e.mail.err = errors.Unavailable("smtp_down", "the provider is down")
	order := e.guestOrder(t)

	_, err := e.svc.RequestTransfer(ctx, order.ID, transferReceiver)
	assert.Equal(t, service.CodeTransferMailFailed, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable))

	transfer := e.store.transfers[e.mail.sent[0].reference]
	assert.Equal(t, models.TransferCanceled, transfer.Status)
	_, err = e.svc.AcceptTransfer(ctx, transfer.ID, e.mail.sent[0].data[service.TransferMailToken])
	assert.Equal(t, service.CodeTransferClosed, errors.CodeOf(err),
		"a mail the provider sent anyway answers a closed request")

	e.mail.err = nil
	_, err = e.svc.RequestTransfer(ctx, order.ID, transferReceiver)
	assert.NoError(t, err, "a withdrawn request holds the order no more")
}

// TestAnAnswerIsRefusedWhatNoLongerWaits walks the acceptance's refusals.
func TestAnAnswerIsRefusedWhatNoLongerWaits(t *testing.T) {
	ctx := context.Background()

	t.Run("expired", func(t *testing.T) {
		e := newTransferEnv(t)
		order := e.guestOrder(t)
		transfer, token := e.requested(t, order.ID)
		*e.now = e.now.Add(service.TransferTTL)

		_, err := e.svc.AcceptTransfer(ctx, transfer.ID, token)
		assert.Equal(t, service.CodeTransferExpired, errors.CodeOf(err))
		_, err = e.svc.DeclineTransfer(ctx, transfer.ID, token)
		assert.Equal(t, service.CodeTransferExpired, errors.CodeOf(err))
		assert.Equal(t, models.TransferExpired, e.store.transfers[transfer.ID].StatusAt(*e.now))
	})
	t.Run("declined", func(t *testing.T) {
		e := newTransferEnv(t)
		transfer, token := e.requested(t, e.guestOrder(t).ID)
		declined, err := e.svc.DeclineTransfer(ctx, transfer.ID, token)
		require.NoError(t, err)
		assert.Equal(t, models.TransferDeclined, declined.Status)
		_, err = e.svc.DeclineTransfer(ctx, transfer.ID, token)
		assert.NoError(t, err, "declining again answers it as it is")

		_, err = e.svc.AcceptTransfer(ctx, transfer.ID, token)
		assert.Equal(t, service.CodeTransferClosed, errors.CodeOf(err))
	})
	t.Run("the order changed hands since", func(t *testing.T) {
		e := newTransferEnv(t)
		order := e.guestOrder(t)
		transfer, token := e.requested(t, order.ID)
		_, err := e.svc.MoveOrder(ctx, order.ID, testCustomerID, "")
		require.NoError(t, err)

		_, err = e.svc.AcceptTransfer(ctx, transfer.ID, token)
		assert.Equal(t, service.CodeTransferClosed, errors.CodeOf(err),
			"the operator's move closed the waiting request")
	})
	t.Run("the order's customer changed since", func(t *testing.T) {
		e := newTransferEnv(t)
		order := e.guestOrder(t)
		transfer, token := e.requested(t, order.ID)
		changed := e.store.orders[order.ID]
		changed.CustomerID = testCustomerID
		e.store.orders[order.ID] = changed

		_, err := e.svc.AcceptTransfer(ctx, transfer.ID, token)
		assert.Equal(t, service.CodeTransferStale, errors.CodeOf(err))
		assert.Equal(t, testCustomerID, e.store.orders[order.ID].CustomerID, "nothing moved")
	})
	t.Run("the order's address changed since", func(t *testing.T) {
		e := newTransferEnv(t)
		order := e.guestOrder(t)
		transfer, token := e.requested(t, order.ID)
		changed := e.store.orders[order.ID]
		changed.Email = "someone-else@example.com"
		e.store.orders[order.ID] = changed

		_, err := e.svc.AcceptTransfer(ctx, transfer.ID, token)
		assert.Equal(t, service.CodeTransferStale, errors.CodeOf(err))
		assert.Empty(t, e.store.orders[order.ID].CustomerID, "nothing moved")
	})
	t.Run("the order was canceled since", func(t *testing.T) {
		e := newTransferEnv(t)
		order := e.guestOrder(t)
		transfer, token := e.requested(t, order.ID)
		err := e.svc.CancelOrder(ctx, order.ID, "test")
		require.NoError(t, err)

		_, err = e.svc.AcceptTransfer(ctx, transfer.ID, token)
		assert.Equal(t, service.CodeTransferStale, errors.CodeOf(err))
	})
	t.Run("the receiver is gone", func(t *testing.T) {
		e := newTransferEnv(t)
		order := e.guestOrder(t)
		transfer, err := e.svc.RequestTransfer(ctx, order.ID, "cus_GONE")
		require.NoError(t, err)

		_, err = e.svc.AcceptTransfer(ctx, transfer.ID, e.mail.sent[0].data[service.TransferMailToken])
		assert.Equal(t, service.CodeTransferReceiverUnknown, errors.CodeOf(err))
	})
}

// TestOnlyTheAskerWithdrawsARequest proves the cancel is the receiver's.
func TestOnlyTheAskerWithdrawsARequest(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	transfer, token := e.requested(t, e.guestOrder(t).ID)

	_, err := e.svc.CancelTransfer(ctx, transfer.ID, "cus_STRANGER")
	assert.Equal(t, service.CodeTransferNotFound, errors.CodeOf(err))

	canceled, err := e.svc.CancelTransfer(ctx, transfer.ID, transferReceiver)
	require.NoError(t, err)
	assert.Equal(t, models.TransferCanceled, canceled.Status)
	_, err = e.svc.AcceptTransfer(ctx, transfer.ID, token)
	assert.Equal(t, service.CodeTransferClosed, errors.CodeOf(err))
}

// TestAnOperatorMovesFromTheCustomerTheyRead proves the compare-and-set.
func TestAnOperatorMovesFromTheCustomerTheyRead(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	order := placeOrder(t, e.env)

	_, err := e.svc.MoveOrder(ctx, order.ID, transferReceiver, "")
	assert.Equal(t, service.CodeTransferCustomerRevised, errors.CodeOf(err),
		"a page that read a guest's order does not move a customer's")
	_, err = e.svc.MoveOrder(ctx, order.ID, testCustomerID, testCustomerID)
	assert.Equal(t, service.CodeTransferOwnOrder, errors.CodeOf(err))
	_, err = e.svc.MoveOrder(ctx, order.ID, "cus_GONE", testCustomerID)
	assert.Equal(t, service.CodeTransferReceiverUnknown, errors.CodeOf(err))

	moved, err := e.svc.MoveOrder(ctx, order.ID, transferReceiver, testCustomerID)
	require.NoError(t, err)
	assert.Equal(t, models.TransferAccepted, moved.Status)
	assert.Equal(t, testCustomerID, moved.FromCustomerID)
	assert.Empty(t, e.mail.sent, "an operator's move mails nothing")

	page, count, err := e.svc.ListTransfers(ctx, order.ID, service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, moved.ID, page[0].ID)
	_, _, err = e.svc.ListTransfers(ctx, "order_NOPE", service.Page{})
	assert.True(t, errors.IsNotFound(err))
}

// TestAnErasureEmptiesTheAddressATransferAsked proves the previous holder is
// forgotten on an order that is no longer theirs, and disclosed before that.
func TestAnErasureEmptiesTheAddressATransferAsked(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	order := e.guestOrder(t)
	guest := order.Email
	transfer, token := e.requested(t, order.ID)
	_, err := e.svc.AcceptTransfer(ctx, transfer.ID, token)
	require.NoError(t, err)

	dossier, err := e.svc.PersonalDataOf(ctx, personaldata.Subject{Email: guest})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, dossier.State,
		"the order is no longer the guest's, and the transfer still names them")
	require.Len(t, dossier.Records, 1)
	assert.Equal(t, "order_transfers", dossier.Records[0].Table)

	result, err := e.svc.Erase(ctx, personaldata.Subject{Email: guest})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Rows)
	assert.Empty(t, e.store.transfers[transfer.ID].FromEmail)
	assert.Equal(t, transferReceiverEmail, e.store.orders[order.ID].Email,
		"the receiver's order keeps the receiver's address")
}

// TestARequestOnACustomersOrderIsNotAcceptedAfterTheRule proves ADR 0459
// reaches a request written on a customer's order before it: the address
// typed at that checkout, a gift's recipient's, would otherwise still take
// the order from the account that bought it.
func TestARequestOnACustomersOrderIsNotAcceptedAfterTheRule(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	in := validInput()
	in.Email = "gift-recipient@example.com"
	order, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	token, digest, err := models.NewTransferToken()
	require.NoError(t, err)
	expiresAt := e.now.Add(service.TransferTTL)
	e.store.transfers["otr_BEFORE"] = models.OrderTransfer{
		ID: "otr_BEFORE", OrderID: order.ID, Status: models.TransferRequested,
		RequestedBy: models.TransferByCustomer, FromCustomerID: testCustomerID,
		ToCustomerID: transferReceiver, FromEmail: order.Email, TokenHash: digest, ExpiresAt: &expiresAt,
	}

	_, err = e.svc.AcceptTransfer(ctx, "otr_BEFORE", token)
	assert.Equal(t, service.CodeNotTransferable, errors.CodeOf(err))
	assert.Equal(t, testCustomerID, e.store.orders[order.ID].CustomerID, "nothing moved")
	assert.Empty(t, e.bus.eventsNamed(service.EventOrderTransferred))
}

// TestAnOrderIsAskedForAtMostThreeTimesAWeek proves the order's address is
// not mailed without end by a shopper who withdraws and asks again (ADR
// 0459).
func TestAnOrderIsAskedForAtMostThreeTimesAWeek(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	order := e.guestOrder(t)

	for range service.MaxTransferRequests {
		transfer, _ := e.requested(t, order.ID)
		_, err := e.svc.CancelTransfer(ctx, transfer.ID, transferReceiver)
		require.NoError(t, err)
		*e.now = e.now.Add(time.Hour)
	}
	_, err := e.svc.RequestTransfer(ctx, order.ID, transferReceiver)
	assert.Equal(t, service.CodeTransferRequestsSpent, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindTooManyRequests))
	assert.Len(t, e.mail.sent, service.MaxTransferRequests, "the refused request mailed nothing")

	*e.now = e.now.Add(service.TransferTTL - time.Duration(service.MaxTransferRequests-1)*time.Hour)
	_, err = e.svc.RequestTransfer(ctx, order.ID, transferReceiver)
	assert.NoError(t, err, "the first request's bound passed, and it is counted no more")
}

// TestAnErasedReceiverReceivesNoOrder proves a receiver's erasure withdraws
// the request that waits for them (ADR 0459): the holder's acceptance would
// write the order to an address that reaches nobody.
func TestAnErasedReceiverReceivesNoOrder(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	order := e.guestOrder(t)
	transfer, token := e.requested(t, order.ID)

	result, err := e.svc.Erase(ctx, personaldata.Subject{CustomerID: transferReceiver})
	require.NoError(t, err)
	assert.Equal(t, models.TransferCanceled, e.store.transfers[transfer.ID].Status)
	assert.Equal(t, 1, result.Rows, "the request names the receiver, and its id stays")
	assert.Contains(t, result.Kept, "order_transfers.to_customer_id")

	again, err := e.svc.Erase(ctx, personaldata.Subject{CustomerID: transferReceiver})
	require.NoError(t, err)
	assert.Equal(t, result.Rows, again.Rows, "a second sweep counts what the first did")

	_, err = e.svc.AcceptTransfer(ctx, transfer.ID, token)
	assert.Equal(t, service.CodeTransferClosed, errors.CodeOf(err))
	assert.Empty(t, e.store.orders[order.ID].CustomerID, "the order stays the guest's")
}

// TestAReceiverErasedByTheirAddressReceivesNoOrder proves the acceptance
// refuses a receiver the customer module erased, when the erasure named the
// person by address alone and so reached no request here (ADR 0459).
func TestAReceiverErasedByTheirAddressReceivesNoOrder(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	order := e.guestOrder(t)
	transfer, token := e.requested(t, order.ID)

	_, err := e.svc.Erase(ctx, personaldata.Subject{Email: transferReceiverEmail})
	require.NoError(t, err)
	assert.Equal(t, models.TransferRequested, e.store.transfers[transfer.ID].Status,
		"an address is no receiver's handle here")
	e.customers[transferReceiver] = "cust_receiver@erased.invalid"

	_, err = e.svc.AcceptTransfer(ctx, transfer.ID, token)
	assert.Equal(t, service.CodeTransferReceiverUnknown, errors.CodeOf(err))
	assert.Empty(t, e.store.orders[order.ID].CustomerID, "the order stays the guest's")
	_, err = e.svc.MoveOrder(ctx, order.ID, transferReceiver, "")
	assert.Equal(t, service.CodeTransferReceiverUnknown, errors.CodeOf(err),
		"an operator moves no order to an erased customer either")
}

// TestATransferHoldingNothingOfThePersonIsNotDisclosed proves a transfer on
// the person's order between two earlier holders is left out of their
// dossier, not handed over with every field empty.
func TestATransferHoldingNothingOfThePersonIsNotDisclosed(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	order := e.guestOrder(t)
	transfer, token := e.requested(t, order.ID)
	_, err := e.svc.AcceptTransfer(ctx, transfer.ID, token)
	require.NoError(t, err)
	moved, err := e.svc.MoveOrder(ctx, order.ID, testCustomerID, transferReceiver)
	require.NoError(t, err)

	dossier, err := e.svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)
	var transfers []string
	for _, record := range dossier.Records {
		if record.Table == "order_transfers" {
			transfers = append(transfers, record.ID)
		}
	}
	assert.Equal(t, []string{order.ID + "/" + moved.ID}, transfers,
		"only the move to the person names them")
}

// TestATransferDisclosesEachSideItsOwn proves the receiver is not handed the
// holder's address as their own, nor the holder the receiver's id (ADR 0459).
func TestATransferDisclosesEachSideItsOwn(t *testing.T) {
	ctx := context.Background()
	e := newTransferEnv(t)
	order := e.guestOrder(t)
	holder := order.Email
	transfer, token := e.requested(t, order.ID)
	_, err := e.svc.AcceptTransfer(ctx, transfer.ID, token)
	require.NoError(t, err)

	fieldsOf := func(subject personaldata.Subject) map[string]any {
		t.Helper()
		dossier, err := e.svc.PersonalDataOf(ctx, subject)
		require.NoError(t, err)
		for _, record := range dossier.Records {
			if record.Table != "order_transfers" {
				continue
			}
			fields := map[string]any{}
			for _, f := range record.Fields {
				fields[f.Column] = f.Value
			}

			return fields
		}
		t.Fatalf("no transfer was disclosed to %+v", subject)

		return nil
	}

	received := fieldsOf(personaldata.Subject{CustomerID: transferReceiver})
	assert.Equal(t, transferReceiver, received["to_customer_id"])
	assert.Nil(t, received["from_email"], "the holder's address is not the receiver's")
	assert.Nil(t, received["from_customer_id"])

	given := fieldsOf(personaldata.Subject{Email: holder})
	assert.Equal(t, holder, given["from_email"])
	assert.Nil(t, given["to_customer_id"], "the receiver's id is not the holder's")
}
