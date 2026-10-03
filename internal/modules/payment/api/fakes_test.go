package api_test

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/api"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// fakePayments is a scriptable stand-in for api.Payments.
//
// It exists so that the HTTP behavior can be exercised without a real
// database: the handlers' job is not to CHOOSE a status code but to hand the
// service's typed error to corehttp.WriteError, and that can only be verified
// case by case by putting a fake in place of the service.
type fakePayments struct {
	providerIDs []string

	collection models.PaymentCollection
	session    models.PaymentSession
	payment    models.Payment
	refund     models.Refund

	collections []models.PaymentCollection
	count       int64
	sessions    []models.PaymentSession
	payments    []models.Payment
	refunds     []models.Refund

	// creditEntry, creditBalance and creditHistory are the scripted answers
	// of the store credit endpoints (ADR 0152).
	creditEntry   models.StoreCreditEntry
	creditBalance int64
	creditHistory []models.StoreCreditEntry
	// lastCreditInput is the input the handler passed to the service; since
	// this translation is the endpoints' only job, its correctness is visible
	// only here.
	lastCreditInput service.IssueCreditInput
	// lastCreditQuery is which customer and currency the last balance or
	// history read asked for.
	lastCreditQuery [2]string
	// lastCreditList is the whole input the history was read with (ADR 0274).
	lastCreditList service.ListStoreCreditInput

	// loyaltyBalance and loyaltyHistory are the scripted answers of the
	// loyalty points endpoints (ADR 0164).
	loyaltyBalance int64
	loyaltyHistory []models.LoyaltyEntry
	// lastLoyaltyQuery is which customer and currency the points were asked
	// for.
	lastLoyaltyQuery [2]string

	// journal is the scripted journal and journalQueries how it was asked.
	journal        service.Journal
	journalQueries []service.JournalQuery

	// issuedCard, giftCards and giftEntries are the gift card endpoints'
	// scripted answers (ADR 0208); lastGiftInput and lastGiftID are what the
	// handlers passed on.
	issuedCard    service.IssuedGiftCard
	giftCards     []service.GiftCardWithBalance
	giftEntries   []models.GiftCardEntry
	lastGiftInput service.IssueGiftCardInput
	lastGiftID    string
	// lastDisableReason is the reason a close was asked with (ADR 0213).
	lastDisableReason string
	// lastLoyaltyPage is the paging the list endpoint passed to the service.
	lastLoyaltyPage service.Page

	// If err is set, every method called returns this error; that is how the
	// error kind's correct mapping to a status code is tested.
	err error

	// lastCreateSession is the input of the last CreateSession call; it proves
	// that the body reaches the service UNALTERED.
	lastCreateSession service.CreateSessionInput
	// lastCollectionInput is the input of the last CreatePaymentCollection
	// call.
	lastCollectionInput service.CreateCollectionInput
	// lastListInput is the input of the last ListPaymentCollections call.
	lastListInput service.ListCollectionsInput
	// lastCaptureAmount is the amount of the last CapturePayment call.
	lastCaptureAmount int64
	// lastRefundAmount and lastRefundReason are the arguments of the last
	// RefundPayment call.
	lastRefundAmount int64
	lastRefundReason string
	// cancelCalled reports whether CancelPayment was called.
	cancelCalled bool

	// The IDs that reach the read endpoints FROM THE PATH. A read handler's
	// only job is to ask the service for the record in the URL; since the fake
	// service always returns its scripted answer, "did it ask for the right
	// record" CANNOT be told apart by looking at the answer. The ID asked for
	// is therefore recorded separately.
	lastSessionListID string
	lastPaymentListID string
	lastPaymentID     string
	lastRefundListID  string
}

// That the fake satisfies the surface the handler expects is verified at
// compile time.
var _ api.Payments = (*fakePayments)(nil)

// ProviderIDs returns the registered provider IDs.
func (f *fakePayments) ProviderIDs(_ context.Context) []string { return f.providerIDs }

// CreatePaymentCollection returns the scripted collection.
func (f *fakePayments) CreatePaymentCollection(
	_ context.Context,
	in service.CreateCollectionInput,
) (models.PaymentCollection, error) {
	f.lastCollectionInput = in
	if f.err != nil {
		return models.PaymentCollection{}, f.err
	}
	return f.collection, nil
}

// GetPaymentCollection returns the scripted collection.
func (f *fakePayments) GetPaymentCollection(_ context.Context, _ string) (models.PaymentCollection, error) {
	if f.err != nil {
		return models.PaymentCollection{}, f.err
	}
	return f.collection, nil
}

// ListPaymentCollections returns the scripted page.
func (f *fakePayments) ListPaymentCollections(
	_ context.Context,
	in service.ListCollectionsInput,
) ([]models.PaymentCollection, int64, error) {
	f.lastListInput = in
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.collections, f.count, nil
}

// CreateSession returns the scripted session.
func (f *fakePayments) CreateSession(
	_ context.Context,
	_, _ string,
	in service.CreateSessionInput,
) (models.PaymentSession, error) {
	f.lastCreateSession = in
	if f.err != nil {
		return models.PaymentSession{}, f.err
	}
	return f.session, nil
}

// GetPaymentSession returns the scripted session.
func (f *fakePayments) GetPaymentSession(_ context.Context, _ string) (models.PaymentSession, error) {
	if f.err != nil {
		return models.PaymentSession{}, f.err
	}
	return f.session, nil
}

// ListPaymentSessions returns the scripted sessions.
func (f *fakePayments) ListPaymentSessions(
	_ context.Context,
	collectionID string,
) ([]models.PaymentSession, error) {
	f.lastSessionListID = collectionID
	if f.err != nil {
		return nil, f.err
	}
	return f.sessions, nil
}

// AuthorizePayment returns the scripted session.
func (f *fakePayments) AuthorizePayment(_ context.Context, _ string) (models.PaymentSession, error) {
	if f.err != nil {
		return models.PaymentSession{}, f.err
	}
	return f.session, nil
}

// CapturePayment returns the scripted payment.
func (f *fakePayments) CapturePayment(_ context.Context, _ string, amount int64) (models.Payment, error) {
	f.lastCaptureAmount = amount
	if f.err != nil {
		return models.Payment{}, f.err
	}
	return f.payment, nil
}

// CancelPayment records the cancellation.
func (f *fakePayments) CancelPayment(_ context.Context, _ string) error {
	f.cancelCalled = true
	return f.err
}

// GetPayment returns the scripted payment.
func (f *fakePayments) GetPayment(_ context.Context, paymentID string) (models.Payment, error) {
	f.lastPaymentID = paymentID
	if f.err != nil {
		return models.Payment{}, f.err
	}
	return f.payment, nil
}

// ListPayments returns the scripted payments.
func (f *fakePayments) ListPayments(_ context.Context, collectionID string) ([]models.Payment, error) {
	f.lastPaymentListID = collectionID
	if f.err != nil {
		return nil, f.err
	}
	return f.payments, nil
}

// RefundPayment returns the scripted refund.
func (f *fakePayments) RefundPayment(
	_ context.Context,
	_ string,
	amount int64,
	reason string,
) (models.Refund, error) {
	f.lastRefundAmount, f.lastRefundReason = amount, reason
	if f.err != nil {
		return models.Refund{}, f.err
	}
	return f.refund, nil
}

// ListRefunds returns the scripted refunds.
func (f *fakePayments) ListRefunds(_ context.Context, paymentID string) ([]models.Refund, error) {
	f.lastRefundListID = paymentID
	if f.err != nil {
		return nil, f.err
	}
	return f.refunds, nil
}

// notFound is the typed error the tests use.
func notFound() error {
	return errors.NotFound("payment_collection_not_found", "collection not found")
}

// --- store credit (ADR 0152) ------------------------------------------------

// IssueCredit records the input and returns the scripted row.
func (f *fakePayments) IssueCredit(
	_ context.Context, in service.IssueCreditInput,
) (models.StoreCreditEntry, error) {
	f.lastCreditInput = in
	if f.err != nil {
		return models.StoreCreditEntry{}, f.err
	}

	return f.creditEntry, nil
}

// StoreCreditBalance records the ledger asked for and returns the balance.
func (f *fakePayments) StoreCreditBalance(
	_ context.Context, customerID, currencyCode string,
) (int64, error) {
	f.lastCreditQuery = [2]string{customerID, currencyCode}
	if f.err != nil {
		return 0, f.err
	}

	return f.creditBalance, nil
}

// ListStoreCredit records what the history was read with and returns it.
func (f *fakePayments) ListStoreCredit(
	_ context.Context, in service.ListStoreCreditInput,
) ([]models.StoreCreditEntry, int64, error) {
	f.lastCreditQuery = [2]string{in.CustomerID, in.CurrencyCode}
	f.lastCreditList = in
	if f.err != nil {
		return nil, 0, f.err
	}

	return f.creditHistory, int64(len(f.creditHistory)), nil
}

// LoyaltyBalance returns the customer's points.
func (f *fakePayments) LoyaltyBalance(
	_ context.Context, customerID, currencyCode string,
) (int64, error) {
	f.lastLoyaltyQuery = [2]string{customerID, currencyCode}
	if f.err != nil {
		return 0, f.err
	}

	return f.loyaltyBalance, nil
}

// ListLoyalty returns the points history.
func (f *fakePayments) ListLoyalty(
	_ context.Context, in service.ListLoyaltyInput,
) ([]models.LoyaltyEntry, int64, error) {
	f.lastLoyaltyQuery = [2]string{in.CustomerID, in.CurrencyCode}
	f.lastLoyaltyPage = in.Page
	if f.err != nil {
		return nil, 0, f.err
	}

	return f.loyaltyHistory, int64(len(f.loyaltyHistory)), nil
}
