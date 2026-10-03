package service_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// Constants the tests use.
const (
	testProviderID = "fake"
	testReference  = "cart_TEST"
	testCurrency   = "TRY"
	testAmount     = int64(10_000)
)

// newTestService builds a service that runs over a fake store and a fake
// provider.
func newTestService(t *testing.T) (*service.Service, *fakeStore, *fakeProvider) {
	t.Helper()

	store := newFakeStore()
	prov := newFakeProvider(testProviderID)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{
		Store: store, Providers: registry, Events: newFakeBus(),
	})
	require.NoError(t, err)

	return svc, store, prov
}

// newTestServiceWithBus builds the same service as newTestService and returns
// the bus as well.
//
// The reason it is a separate helper is most of its callers: nearly all of the
// tests do not care about the bus, and a fourth return value would make every
// one of them write "_". The tests that look at the published event call this
// one.
func newTestServiceWithBus(t *testing.T) (*service.Service, *fakeStore, *fakeBus) {
	t.Helper()

	store := newFakeStore()
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider(testProviderID)))

	bus := newFakeBus()
	svc, err := service.New(service.Options{
		Store: store, Providers: registry, Events: bus,
	})
	require.NoError(t, err)

	return svc, store, bus
}

// fakeBus collects events and publishes none of them.
type fakeBus struct {
	mu        sync.Mutex
	published []eventbus.Event
}

// newFakeBus returns an empty bus.
func newFakeBus() *fakeBus { return &fakeBus{} }

// Publish records the event.
func (b *fakeBus) Publish(_ context.Context, e eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, e)

	return nil
}

// events returns a copy of the published events.
func (b *fakeBus) events() []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	return slices.Clone(b.published)
}

// openCollection opens a payment collection for a test.
func openCollection(t *testing.T, svc *service.Service, amount int64) models.PaymentCollection {
	t.Helper()

	col, err := svc.CreatePaymentCollection(context.Background(), service.CreateCollectionInput{
		Reference:    testReference,
		Amount:       amount,
		CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	return col
}

// openPaymentSession opens a payment session for a test.
func openPaymentSession(t *testing.T, svc *service.Service, collectionID, key string) models.PaymentSession {
	t.Helper()

	ses, err := svc.CreateSession(context.Background(), collectionID, testProviderID,
		service.CreateSessionInput{IdempotencyKey: key})
	require.NoError(t, err)
	return ses
}

// TestServiceCannotBeBuiltWithAMissingDependency verifies that the setup error
// comes back EXPLICITLY.
//
// A service set up with a nil store would panic on its first request, and the
// error would surface long after the setup.
func TestServiceCannotBeBuiltWithAMissingDependency(t *testing.T) {
	_, err := service.New(service.Options{Providers: service.NewProviderRegistry()})
	require.Error(t, err)

	_, err = service.New(service.Options{Store: newFakeStore()})
	require.Error(t, err)
}

// TestCreatePaymentCollectionIsBornNotPaid verifies a new collection's status
// and fields.
func TestCreatePaymentCollectionIsBornNotPaid(t *testing.T) {
	svc, _, _ := newTestService(t)

	col, err := svc.CreatePaymentCollection(context.Background(), service.CreateCollectionInput{
		Reference:    "  " + testReference + "  ",
		Amount:       testAmount,
		CurrencyCode: "try",
		Metadata:     map[string]any{"source": "test"},
	})

	require.NoError(t, err)
	assert.Equal(t, models.CollectionNotPaid, col.Status)
	assert.Equal(t, testReference, col.Reference, "the reference must be trimmed")
	assert.Equal(t, "TRY", col.CurrencyCode, "the currency must be turned to UPPER case")
	assert.Equal(t, testAmount, col.Amount)
	assert.Zero(t, col.AuthorizedAmount)
	assert.Zero(t, col.CapturedAmount)
	assert.Zero(t, col.RefundedAmount)
	assert.Equal(t, models.PaymentCollectionIDPrefix, col.ID[:len(models.PaymentCollectionIDPrefix)])
}

// TestCreatePaymentCollectionMoneyValidation tests every branch of the amount
// and currency validation.
//
// Rejecting a zero amount is deliberate: a collection whose amount is zero could
// never become "captured", so it would be a dead record waiting for payment
// forever.
func TestCreatePaymentCollectionMoneyValidation(t *testing.T) {
	tests := []struct {
		name string
		in   service.CreateCollectionInput
	}{
		{"no reference", service.CreateCollectionInput{Amount: testAmount, CurrencyCode: testCurrency}},
		{"reference of only whitespace", service.CreateCollectionInput{
			Reference: "   ", Amount: testAmount, CurrencyCode: testCurrency,
		}},
		{"zero amount", service.CreateCollectionInput{
			Reference: testReference, Amount: 0, CurrencyCode: testCurrency,
		}},
		{"negative amount", service.CreateCollectionInput{
			Reference: testReference, Amount: -1, CurrencyCode: testCurrency,
		}},
		{"amount above the ceiling", service.CreateCollectionInput{
			Reference: testReference, Amount: models.MaxAmount + 1, CurrencyCode: testCurrency,
		}},
		{"no currency", service.CreateCollectionInput{Reference: testReference, Amount: testAmount}},
		{"currency too long", service.CreateCollectionInput{
			Reference: testReference, Amount: testAmount, CurrencyCode: "TRYX",
		}},
		{"currency with a digit", service.CreateCollectionInput{
			Reference: testReference, Amount: testAmount, CurrencyCode: "TR1",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, _ := newTestService(t)

			_, err := svc.CreatePaymentCollection(context.Background(), tt.in)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)
			assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
		})
	}
}

// TestListPaymentCollectionsFilterAndPaging verifies that the filter and the
// paging envelope work correctly together.
//
// That the total count is the count of the FILTER and not of the PAGE is tested
// separately: even with a page size of one the total must come back as three,
// otherwise the client cannot know that a second page exists.
func TestListPaymentCollectionsFilterAndPaging(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	for range 3 {
		openCollection(t, svc, testAmount)
	}
	_, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: "cart_OTHER", Amount: testAmount, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	reference := testReference
	firstPage, count, err := svc.ListPaymentCollections(ctx, service.ListCollectionsInput{
		Reference: &reference,
		Page:      service.Page{Limit: 1},
	})

	require.NoError(t, err)
	assert.Len(t, firstPage, 1, "the page size must be one")
	assert.Equal(t, int64(3), count, "the total is the filter's count, not the page's")
}

// TestListPaymentCollectionsPagingValidation verifies that invalid paging
// parameters are rejected.
func TestListPaymentCollectionsPagingValidation(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	tests := map[string]service.Page{
		"negative limit":    {Limit: -1},
		"negative offset":   {Offset: -1},
		"above the ceiling": {Limit: service.MaxLimit + 1},
	}
	for name, page := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := svc.ListPaymentCollections(ctx, service.ListCollectionsInput{Page: page})

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)
		})
	}
}

// TestListPaymentCollectionsRejectsAnUnknownStatus verifies that a typo written
// into the filter does not silently return "no results".
//
// Silently filtering on an unknown status would lead the client to believe that
// there really are no records at all.
func TestListPaymentCollectionsRejectsAnUnknownStatus(t *testing.T) {
	svc, _, _ := newTestService(t)
	status := "paid"

	_, _, err := svc.ListPaymentCollections(context.Background(), service.ListCollectionsInput{
		Status: &status,
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)
}

// TestReadsOfAnUnknownIDAreNotFound verifies that the read surface returns
// NotFound for a missing record.
func TestReadsOfAnUnknownIDAreNotFound(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetPaymentCollection(ctx, "paycol_MISSING")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)

	_, err = svc.GetPaymentSession(ctx, "payses_MISSING")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)

	_, err = svc.GetPayment(ctx, "pay_MISSING")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)

	_, err = svc.ListPaymentSessions(ctx, "paycol_MISSING")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)

	_, err = svc.ListPayments(ctx, "paycol_MISSING")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)

	_, err = svc.ListRefunds(ctx, "pay_MISSING")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)
}

// TestAnEmptyIDIsInvalid verifies that an empty id is "invalid", not "not
// found"; for the caller those are two different errors.
func TestAnEmptyIDIsInvalid(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetPaymentCollection(ctx, "")
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)

	_, err = svc.CreateSession(ctx, "", testProviderID, service.CreateSessionInput{IdempotencyKey: "k"})
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)

	_, err = svc.AuthorizePayment(ctx, " ")
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)

	assert.True(t, errors.HasKind(svc.CancelPayment(ctx, ""), errors.KindInvalid))
}

// TestProviderIDsReturnsTheRegisteredProviders verifies which payment methods
// the storefront will see.
func TestProviderIDsReturnsTheRegisteredProviders(t *testing.T) {
	svc, _, _ := newTestService(t)

	assert.Equal(t, []string{testProviderID}, svc.ProviderIDs(context.Background()))
}

// --- store credit ------------------------------------------------------------

// The SERVICE half of store credit (ADR 0152): the credit the operator issues
// and the balance that is read. The spending side is in the provider and is
// tested there.

// TestIssuedCreditEntersTheBalance is the decision in its shortest form.
func TestIssuedCreditEntersTheBalance(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	entry, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID:   "cus_1",
		CurrencyCode: "try",
		Amount:       5_000,
		Reason:       "credit instead of a refund",
	})
	require.NoError(t, err)

	assert.Equal(t, models.StoreCreditIssue, entry.Kind)
	assert.Equal(t, int64(5_000), entry.Amount)
	assert.Equal(t, "TRY", entry.CurrencyCode,
		"the currency is NORMALIZED: 'try' and 'TRY' must read the same ledger, otherwise "+
			"the same customer would have two balances")

	balance, err := svc.StoreCreditBalance(ctx, "cus_1", "TRY")
	require.NoError(t, err)
	assert.Equal(t, int64(5_000), balance)
}

// TestCreditWithoutAReasonIsRefused guards the half that the balance column
// cannot hold.
func TestCreditWithoutAReasonIsRefused(t *testing.T) {
	svc, _, _ := newTestService(t)

	_, err := svc.IssueCredit(context.Background(), service.IssueCreditInput{
		CustomerID:   "cus_1",
		CurrencyCode: "TRY",
		Amount:       5_000,
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeStoreCreditInvalidInput, errors.CodeOf(err))
}

// TestNegativeCreditIsRefused stops the operator from taking the balance below
// zero.
//
// "Take the credit back" is a separate decision: writing a negative amount would
// be a hidden way of taking back money the customer has spent, and it would show
// in the ledger not as a TAKE-BACK but as an ISSUE.
func TestNegativeCreditIsRefused(t *testing.T) {
	svc, _, _ := newTestService(t)

	for name, amount := range map[string]int64{"negative": -100, "zero": 0} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.IssueCredit(context.Background(), service.IssueCreditInput{
				CustomerID:   "cus_1",
				CurrencyCode: "TRY",
				Amount:       amount,
				Reason:       "test",
			})

			require.Error(t, err)
			assert.Equal(t, service.CodeStoreCreditInvalidInput, errors.CodeOf(err))
		})
	}
}

// TestTheBalanceIsPerCURRENCY pins that credit in one currency is not credit in
// another.
//
// Converting between currencies is not this module's job, and converting
// silently would give the customer an amount other than the one promised.
func TestTheBalanceIsPerCURRENCY(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: "cus_1", CurrencyCode: "TRY", Amount: 5_000, Reason: "x",
	})
	require.NoError(t, err)

	other, err := svc.StoreCreditBalance(ctx, "cus_1", "EUR")
	require.NoError(t, err)
	assert.Zero(t, other, "TRY credit must not show in the EUR balance")
}

// TestACustomerWithoutCreditReturnsZERO does not tell absence apart from zero.
//
// Someone who was never issued credit and someone who was issued credit and
// spent all of it hold the same amount of money; the ledger itself tells the
// difference between them.
func TestACustomerWithoutCreditReturnsZERO(t *testing.T) {
	svc, _, _ := newTestService(t)

	balance, err := svc.StoreCreditBalance(context.Background(), "cus_missing", "TRY")

	require.NoError(t, err)
	assert.Zero(t, balance)
}

// TestCreditHistoryIsNewestFirst answers the operator's "why" question.
func TestCreditHistoryIsNewestFirst(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	for _, reason := range []string{"first", "second"} {
		_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
			CustomerID: "cus_1", CurrencyCode: "TRY", Amount: 1_000, Reason: reason,
		})
		require.NoError(t, err)
	}

	entries, total, err := svc.ListStoreCredit(ctx, service.ListStoreCreditInput{
		CustomerID: "cus_1", CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2), total)
	require.Len(t, entries, 2)
	assert.Equal(t, "second", entries[0].Reason, "the newest row comes first")
}

// TestACreditIsReadForTheOrderItCompensates is ADR 0274: an issue keeps the
// order it names, and the history narrowed to that order holds its credits
// alone.
func TestACreditIsReadForTheOrderItCompensates(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	named, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: "cus_1", CurrencyCode: "TRY", Amount: 5_000, Reason: "a late delivery", OrderID: " order_7 ",
	})
	require.NoError(t, err)
	assert.Equal(t, "order_7", named.OrderID)
	_, err = svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: "cus_1", CurrencyCode: "TRY", Amount: 1_000, Reason: "goodwill",
	})
	require.NoError(t, err)

	forOrder, count, err := svc.ListStoreCredit(ctx, service.ListStoreCreditInput{
		CustomerID: "cus_1", CurrencyCode: "TRY", OrderID: "order_7",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	require.Len(t, forOrder, 1)
	assert.Equal(t, named.ID, forOrder[0].ID)

	all, count, err := svc.ListStoreCredit(ctx, service.ListStoreCreditInput{CustomerID: "cus_1", CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
	assert.Len(t, all, 2)
}

// TestAnOrderIDThatIsNoIdentifierIsRefused bounds the field to an id's size.
func TestAnOrderIDThatIsNoIdentifierIsRefused(t *testing.T) {
	svc, _, _ := newTestService(t)

	_, err := svc.IssueCredit(context.Background(), service.IssueCreditInput{
		CustomerID: "cus_1", CurrencyCode: "TRY", Amount: 5_000, Reason: "a late delivery",
		OrderID: strings.Repeat("o", 129),
	})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "error: %v", err)
}
