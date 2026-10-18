package service

import (
	"context"
	"strconv"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// This file is how an order changes hands (ADR 0450).
//
// # Who says yes
//
// A shopper the storefront identity proves, who can already open the order —
// with one of its access tokens, since its own customer has nothing to ask —
// requests it. The token proves the shopper holds the order's link, not that
// the order is theirs: a link is forwarded with the mail it came in. So the
// request is answered by the address the order is mailed at, the one every
// order mail went to, with a token mailed there alone. The holder of that
// mailbox accepts or declines with it, signed in or not; the receiver is the
// shopper who asked, recorded when they asked. An operator moves an order at
// once, naming the customer it held.
//
// # What a transfer moves
//
// The order's customer and its e-mail, which become the receiver's and the
// receiver's address on record, for the order and every order that adds to
// it. The order's access tokens are withdrawn: the links the previous holder
// was mailed stop opening it, and the receiver opens it by their proof.
// Everything that reads the order's customer live follows: the customer's
// order list, the storefront's proof, the customer's totals, the b2b spending
// window and a review's purchase badge written afterwards.
//
// What it never moves is what the sale recorded: the addresses it shipped to
// and billed, the invoice and its amendments, the payment collection and
// whose balance or points paid it, the cart, the returns, claims and
// exchanges already opened, the reviews already written, and anything in the
// previous holder's address book or saved payment methods. Those name the
// sale or the person, and the person is not what changed hands.
//
// # Races
//
// Every write takes the order's lock first and the transfer's second, so a
// request, its answer, an operator's move, a cancellation and an erasure of
// one order run one after another. An acceptance moves the order only while
// it holds the customer and the address the request was written against: a
// request written before another move answers stale rather than undoing it.

// The refusals of an order's transfer (ADR 0450).
const (
	// CodeTransferNotFound answers an unknown transfer, and a token that is
	// not the transfer's, alike.
	CodeTransferNotFound = "order_transfer_not_found"
	// CodeTransferOwnOrder refuses a transfer to the customer the order
	// already belongs to.
	CodeTransferOwnOrder = "order_transfer_own_order"
	// CodeNotTransferable refuses a canceled order, one whose personal data
	// was erased, one an operator's move names no address for, and a
	// storefront request on an order with no address to ask.
	CodeNotTransferable = "order_not_transferable"
	// CodeTransferAddition refuses a transfer of an order that adds to
	// another: it moves with the order it adds to.
	CodeTransferAddition = "order_transfer_addition"
	// CodeTransferPending refuses a request while another one on the order
	// waits.
	CodeTransferPending = "order_transfer_pending"
	// CodeTransferExpired refuses an answer to a request past its bound.
	CodeTransferExpired = "order_transfer_expired"
	// CodeTransferClosed refuses an answer to a request already answered
	// otherwise or withdrawn.
	CodeTransferClosed = "order_transfer_closed"
	// CodeTransferStale refuses an acceptance after the order changed hands,
	// address, or state since the request was written.
	CodeTransferStale = "order_transfer_stale"
	// CodeTransferCustomerRevised refuses an operator's move drawn from
	// another customer than the order holds.
	CodeTransferCustomerRevised = "order_transfer_customer_revised"
	// CodeTransferReceiverUnknown refuses a transfer to a customer the
	// customer module does not hold.
	CodeTransferReceiverUnknown = "order_transfer_receiver_unknown"
	// CodeTransferMailFailed reports that the request's token could not be
	// mailed, or that nothing is bound to mail it; the request is withdrawn.
	CodeTransferMailFailed = "order_transfer_mail_failed"
)

// TransferTTL is how long a storefront request waits for its answer.
const TransferTTL = 7 * 24 * time.Hour

// TemplateTransferRequested is the template a request's token is mailed with
// to the order's address (ADR 0450). Its data is the TransferMail* keys below.
const TemplateTransferRequested = "order.transfer_requested"

// The data keys of the [TemplateTransferRequested] mail. Every value is a
// string.
const (
	// TransferMailOrderID is the order asked for.
	TransferMailOrderID = "order_id"
	// TransferMailDisplayID is its human readable number.
	TransferMailDisplayID = "display_id"
	// TransferMailTransferID is the request, which the answer names.
	TransferMailTransferID = "transfer_id"
	// TransferMailToken is the token that answers it, the only copy.
	TransferMailToken = "transfer_token"
	// TransferMailExpiresAt is the request's bound, RFC 3339, UTC.
	TransferMailExpiresAt = "expires_at"
)

// The customer module's entity and field the receiver's address is read
// from, through the Query layer. They are the customer module's and repeated
// here for the reason [EntityPaymentCollection] is.
const (
	// EntityCustomer is the customer module's entity in the Query layer.
	EntityCustomer = "customer"
	// fieldCustomerEmail is the customer's address on record.
	fieldCustomerEmail = "email"
)

// TransferMail is the narrow surface a request's token is mailed through:
// the notification module's "notification.interop" Send, declared here in
// primitive types because this module cannot import that one.
type TransferMail interface {
	Send(ctx context.Context, template, channel, reference, to string, data map[string]string) error
}

// RequestTransfer writes a request that the order become receiverID's and
// mails its token to the order's address (ADR 0450).
//
// The caller decided that the request may reach the order — the storefront
// route asks for one of its access tokens — and proved receiverID. The order
// must not be canceled, erased, an addition, or receiverID's already, and it
// must hold an address to ask; one request waits per order. A request whose
// mail fails is withdrawn and the failure returned, so no request waits that
// its holder was never asked.
func (s *Service) RequestTransfer(ctx context.Context, orderID, receiverID string) (models.OrderTransfer, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.OrderTransfer{}, err
	}
	if err := requireID("customer_id", receiverID); err != nil {
		return models.OrderTransfer{}, err
	}
	if s.mail == nil {
		return models.OrderTransfer{}, errors.Unavailable(CodeTransferMailFailed,
			"no notification surface is bound, so a transfer request cannot be mailed")
	}

	plaintext, digest, err := models.NewTransferToken()
	if err != nil {
		return models.OrderTransfer{}, errors.Wrap(err, errors.KindInternal, codeAccessTokenFailed,
			"no transfer token could be made for order %s", orderID)
	}

	var (
		written models.OrderTransfer
		order   models.Order
	)
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		var lockErr error
		order, lockErr = s.store.LockOrder(ctx, orderID)
		if lockErr != nil {
			return lockErr
		}
		if err := transferable(order); err != nil {
			return err
		}
		if order.Email == "" {
			return errors.Conflict(CodeNotTransferable,
				"order %s holds no address to ask; an operator can move it", orderID)
		}
		if order.CustomerID == receiverID {
			return errors.Conflict(CodeTransferOwnOrder,
				"order %s already belongs to the customer asking for it", orderID)
		}
		if err := s.refuseWaitingRequest(ctx, orderID); err != nil {
			return err
		}

		expiresAt := s.now().UTC().Add(TransferTTL)
		var writeErr error
		written, writeErr = s.store.InsertTransfer(ctx, models.OrderTransfer{
			ID:             models.NewTransferID(),
			OrderID:        orderID,
			Status:         models.TransferRequested,
			RequestedBy:    models.TransferByCustomer,
			FromCustomerID: order.CustomerID,
			ToCustomerID:   receiverID,
			FromEmail:      order.Email,
			TokenHash:      digest,
			ExpiresAt:      &expiresAt,
		})

		return writeErr
	})
	if err != nil {
		return models.OrderTransfer{}, err
	}

	if err := s.mailTransferRequest(ctx, order, written, plaintext); err != nil {
		return models.OrderTransfer{}, err
	}

	return written, nil
}

// refuseWaitingRequest refuses a request while another one on the order
// waits; one past its bound waits no more and is left as it is.
func (s *Service) refuseWaitingRequest(ctx context.Context, orderID string) error {
	waiting, err := s.store.RequestedTransfers(ctx, orderID)
	if err != nil {
		return err
	}
	now := s.now()
	for i := range waiting {
		if !waiting[i].ExpiredAt(now) {
			return errors.Conflict(CodeTransferPending,
				"order %s already has a transfer request waiting for its answer", orderID)
		}
	}

	return nil
}

// mailTransferRequest mails the token to the address the request asks, and
// withdraws the request when the mail fails.
func (s *Service) mailTransferRequest(
	ctx context.Context, order models.Order, transfer models.OrderTransfer, token string,
) error {
	expiresAt := ""
	if transfer.ExpiresAt != nil {
		expiresAt = transfer.ExpiresAt.UTC().Format(time.RFC3339)
	}
	err := s.mail.Send(ctx, TemplateTransferRequested, coreprovider.ChannelEmail, transfer.ID,
		transfer.FromEmail, map[string]string{
			TransferMailOrderID:    order.ID,
			TransferMailDisplayID:  strconv.FormatInt(order.DisplayID, 10),
			TransferMailTransferID: transfer.ID,
			TransferMailToken:      token,
			TransferMailExpiresAt:  expiresAt,
		})
	if err == nil {
		return nil
	}

	// The provider may have sent it anyway (a timed-out send); a holder who
	// answers a withdrawn request is told it was closed.
	if _, closeErr := s.closeWaiting(ctx, transfer.OrderID, transfer.ID, models.TransferCanceled); closeErr != nil {
		s.log.ErrorContext(ctx, "a transfer request whose mail failed could not be withdrawn",
			"transfer_id", transfer.ID, "order_id", transfer.OrderID, "error", closeErr)
	}

	return errors.Wrap(err, errors.KindUnavailable, CodeTransferMailFailed,
		"the transfer request of order %s could not be mailed; it was withdrawn", transfer.OrderID)
}

// closeWaiting closes a waiting request under its order's lock.
func (s *Service) closeWaiting(
	ctx context.Context, orderID, transferID string, status models.TransferStatus,
) (models.OrderTransfer, error) {
	var closed models.OrderTransfer
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.store.LockOrder(ctx, orderID); err != nil {
			return err
		}
		var err error
		closed, err = s.store.CloseTransfer(ctx, transferID, status)

		return err
	})

	return closed, err
}

// AcceptTransfer answers a request yes with the token mailed for it and moves
// the order to the receiver the request named (ADR 0450).
//
// A token that is not the transfer's answers what an unknown transfer does.
// An accepted request accepted again with its token answers it as it is, so
// a repeated click is not an error.
func (s *Service) AcceptTransfer(ctx context.Context, transferID, token string) (models.OrderTransfer, error) {
	read, err := s.answerableTransfer(ctx, transferID, token)
	if err != nil {
		return models.OrderTransfer{}, err
	}
	if read.Status == models.TransferAccepted {
		return read, nil
	}

	receiverEmail, err := s.receiverEmail(ctx, read.ToCustomerID)
	if err != nil {
		return models.OrderTransfer{}, err
	}

	var (
		accepted models.OrderTransfer
		moved    []string
	)
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.store.LockOrder(ctx, read.OrderID)
		if err != nil {
			return err
		}
		transfer, err := s.store.LockTransfer(ctx, transferID)
		if err != nil {
			return err
		}
		if transfer.Status == models.TransferAccepted {
			accepted = transfer
			return nil
		}
		if err := s.waitingForAnswer(transfer); err != nil {
			return err
		}
		if err := transferable(order); err != nil {
			return errors.Wrap(err, errors.KindConflict, CodeTransferStale,
				"order %s can no longer change hands", order.ID)
		}
		if order.CustomerID != transfer.FromCustomerID || order.Email == "" || order.Email != transfer.FromEmail {
			return errors.Conflict(CodeTransferStale,
				"order %s changed hands or address since the transfer was requested", order.ID)
		}

		accepted, moved, err = s.moveOrder(ctx, order, transfer, receiverEmail, func(ctx context.Context) (models.OrderTransfer, error) {
			return s.store.CloseTransfer(ctx, transfer.ID, models.TransferAccepted)
		})

		return err
	})
	if err != nil {
		return models.OrderTransfer{}, err
	}

	s.publishOrderTransferred(ctx, accepted, moved)

	return accepted, nil
}

// DeclineTransfer answers a request no with the token mailed for it. A
// declined request declined again answers it as it is.
func (s *Service) DeclineTransfer(ctx context.Context, transferID, token string) (models.OrderTransfer, error) {
	read, err := s.answerableTransfer(ctx, transferID, token)
	if err != nil {
		return models.OrderTransfer{}, err
	}

	return s.closeRequest(ctx, read, models.TransferDeclined)
}

// CancelTransfer withdraws a request on behalf of the customer who asked;
// a request another customer asked answers what an unknown one does. A
// withdrawn request withdrawn again answers it as it is.
func (s *Service) CancelTransfer(ctx context.Context, transferID, customerID string) (models.OrderTransfer, error) {
	if err := requireID("transfer_id", transferID); err != nil {
		return models.OrderTransfer{}, err
	}
	read, err := s.store.GetTransfer(ctx, transferID)
	if err != nil {
		return models.OrderTransfer{}, err
	}
	if read.RequestedBy != models.TransferByCustomer || customerID == "" || read.ToCustomerID != customerID {
		return models.OrderTransfer{}, transferNotFound(transferID)
	}

	return s.closeRequest(ctx, read, models.TransferCanceled)
}

// closeRequest closes a waiting request as status under the order's lock, or
// answers one already closed as status as it is.
func (s *Service) closeRequest(
	ctx context.Context, read models.OrderTransfer, status models.TransferStatus,
) (models.OrderTransfer, error) {
	if read.Status == status {
		return read, nil
	}

	var closed models.OrderTransfer
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.store.LockOrder(ctx, read.OrderID); err != nil {
			return err
		}
		transfer, err := s.store.LockTransfer(ctx, read.ID)
		if err != nil {
			return err
		}
		if transfer.Status == status {
			closed = transfer
			return nil
		}
		if err := s.waitingForAnswer(transfer); err != nil {
			return err
		}
		closed, err = s.store.CloseTransfer(ctx, transfer.ID, status)

		return err
	})
	if err != nil {
		return models.OrderTransfer{}, err
	}

	return closed, nil
}

// MoveOrder is an operator's transfer: it moves the order to customerID at
// once, drawn from the customer the operator read the order with —
// expectedCustomerID, empty for a guest's order — and closes a request still
// waiting on it (ADR 0450).
func (s *Service) MoveOrder(
	ctx context.Context, orderID, customerID, expectedCustomerID string,
) (models.OrderTransfer, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.OrderTransfer{}, err
	}
	if err := requireID("customer_id", customerID); err != nil {
		return models.OrderTransfer{}, err
	}
	if err := optionalID("expected_customer_id", expectedCustomerID); err != nil {
		return models.OrderTransfer{}, err
	}
	if _, err := s.store.GetOrder(ctx, orderID); err != nil {
		return models.OrderTransfer{}, err
	}

	receiverEmail, err := s.receiverEmail(ctx, customerID)
	if err != nil {
		return models.OrderTransfer{}, err
	}

	var (
		accepted models.OrderTransfer
		moved    []string
	)
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.store.LockOrder(ctx, orderID)
		if err != nil {
			return err
		}
		if err := transferable(order); err != nil {
			return err
		}
		if order.CustomerID != expectedCustomerID {
			return errors.Conflict(CodeTransferCustomerRevised,
				"order %s belongs to another customer than the one it was read with; draw the page again",
				orderID)
		}
		if order.CustomerID == customerID {
			return errors.Conflict(CodeTransferOwnOrder,
				"order %s already belongs to customer %s", orderID, customerID)
		}

		if err := s.cancelWaitingRequests(ctx, orderID); err != nil {
			return err
		}

		transfer := models.OrderTransfer{
			ID:             models.NewTransferID(),
			OrderID:        orderID,
			RequestedBy:    models.TransferByOperator,
			FromCustomerID: order.CustomerID,
			ToCustomerID:   customerID,
			FromEmail:      order.Email,
		}
		accepted, moved, err = s.moveOrder(ctx, order, transfer, receiverEmail, func(ctx context.Context) (models.OrderTransfer, error) {
			transfer.Status = models.TransferAccepted
			return s.store.InsertTransfer(ctx, transfer)
		})

		return err
	})
	if err != nil {
		return models.OrderTransfer{}, err
	}

	s.publishOrderTransferred(ctx, accepted, moved)

	return accepted, nil
}

// cancelWaitingRequests closes every request on the order that still waits:
// an operator's move answers them.
func (s *Service) cancelWaitingRequests(ctx context.Context, orderID string) error {
	waiting, err := s.store.RequestedTransfers(ctx, orderID)
	if err != nil {
		return err
	}
	now := s.now()
	for i := range waiting {
		if waiting[i].ExpiredAt(now) {
			continue
		}
		if _, err := s.store.CloseTransfer(ctx, waiting[i].ID, models.TransferCanceled); err != nil {
			return err
		}
	}

	return nil
}

// moveOrder writes the move inside the caller's transaction, under the
// order's lock: the order and its additions change hands, their access
// tokens are withdrawn, the transfer is closed accepted by accept, and one
// event per moved order goes into the outbox.
func (s *Service) moveOrder(
	ctx context.Context, order models.Order, transfer models.OrderTransfer, receiverEmail string,
	accept func(ctx context.Context) (models.OrderTransfer, error),
) (models.OrderTransfer, []string, error) {
	moved, err := s.store.MoveOrderCustomer(ctx, order.ID, order.CustomerID, transfer.ToCustomerID, receiverEmail)
	if err != nil {
		return models.OrderTransfer{}, nil, err
	}
	if len(moved) == 0 {
		return models.OrderTransfer{}, nil, errors.Conflict(CodeTransferStale,
			"order %s changed hands while it was being moved", order.ID)
	}
	if _, err := s.store.WithdrawAccessTokens(ctx, moved); err != nil {
		return models.OrderTransfer{}, nil, err
	}
	accepted, err := accept(ctx)
	if err != nil {
		return models.OrderTransfer{}, nil, err
	}
	if err := s.recordOrderTransferred(ctx, accepted, moved); err != nil {
		return models.OrderTransfer{}, nil, err
	}

	return accepted, moved, nil
}

// ListTransfers returns a page of the order's transfers, newest first;
// NotFound when there is no such order.
func (s *Service) ListTransfers(
	ctx context.Context, orderID string, page Page,
) ([]models.OrderTransfer, int64, error) {
	if err := requireID("order_id", orderID); err != nil {
		return nil, 0, err
	}
	page, err := page.normalize()
	if err != nil {
		return nil, 0, err
	}
	if _, err := s.store.GetOrder(ctx, orderID); err != nil {
		return nil, 0, err
	}

	return s.store.ListTransfers(ctx, orderID, int32(page.Limit), int32(page.Offset)) //nolint:gosec // bounded by MaxLimit and the page's own check
}

// Now is the clock a transfer's expiry is read with, for a reader that tells
// a request's status.
func (s *Service) Now() time.Time { return s.now() }

// answerableTransfer reads the transfer a token answers; an unknown id and a
// token that is not its own answer alike.
func (s *Service) answerableTransfer(ctx context.Context, transferID, token string) (models.OrderTransfer, error) {
	if err := requireID("transfer_id", transferID); err != nil {
		return models.OrderTransfer{}, err
	}
	transfer, err := s.store.GetTransfer(ctx, transferID)
	if err != nil {
		return models.OrderTransfer{}, err
	}
	if !transfer.TransferTokenMatches(token) {
		return models.OrderTransfer{}, transferNotFound(transferID)
	}

	return transfer, nil
}

// waitingForAnswer refuses a transfer that no longer waits, or whose bound
// has passed.
func (s *Service) waitingForAnswer(transfer models.OrderTransfer) error {
	if transfer.Status != models.TransferRequested {
		return errors.Conflict(CodeTransferClosed,
			"the transfer %s was already %s", transfer.ID, transfer.Status)
	}
	if transfer.ExpiredAt(s.now()) {
		return errors.Conflict(CodeTransferExpired,
			"the transfer %s expired; the shopper can ask again", transfer.ID)
	}

	return nil
}

// receiverEmail reads the receiver's address on record from the customer
// module, through the Query layer.
func (s *Service) receiverEmail(ctx context.Context, customerID string) (string, error) {
	if s.catalog == nil {
		return "", errors.Internal(CodeNotReady,
			"the query layer is not wired, so a transfer's receiver cannot be read")
	}
	records, err := s.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityCustomer,
		Fields:  []string{query.IDField, fieldCustomerEmail},
		Filters: map[string]any{query.IDField: customerID},
		Limit:   1,
	})
	if err != nil {
		return "", errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"the receiving customer %s could not be read", customerID)
	}
	if len(records) == 0 {
		return "", errors.Conflict(CodeTransferReceiverUnknown,
			"there is no customer %s to receive the order", customerID)
	}
	email := models.NormalizeEmail(recordText(records[0], fieldCustomerEmail))
	if email == "" {
		return "", errors.Conflict(CodeTransferReceiverUnknown,
			"customer %s holds no address for the order's mails", customerID)
	}

	return email, nil
}

// transferable refuses an order that cannot change hands.
func transferable(order models.Order) error {
	switch {
	case order.Status == models.OrderCanceled:
		return errors.Conflict(CodeNotTransferable, "order %s is canceled", order.ID)
	case order.PersonalDataErasedAt != nil:
		return errors.Conflict(CodeNotTransferable,
			"order %s's personal data was erased; no address is written back", order.ID)
	case order.AddsToOrderID != "":
		return errors.Conflict(CodeTransferAddition,
			"order %s adds to order %s and changes hands with it", order.ID, order.AddsToOrderID)
	}

	return nil
}

// transferNotFound is the answer to an unknown transfer and to a caller it
// does not open to, word for word.
func transferNotFound(id string) error {
	return errors.NotFound(CodeTransferNotFound, "order transfer not found: %s", id)
}
