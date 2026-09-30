package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// The payment module's answers about one person (ADR 0277), against a store
// that holds rows for two customers and says which reads ran inside the
// snapshot.

type personalStore struct {
	collections        []models.PaymentCollection
	sessions           []models.PaymentSession
	payments           []models.Payment
	refunds            []models.Refund
	manual             []models.ManualSession
	giftCards          []models.TenderSession
	storeCreditEntries []models.StoreCreditEntry
	storeCreditSession []models.TenderSession
	loyaltyEntries     []models.LoyaltyEntry
	loyaltySessions    []models.TenderSession

	inSnapshot  bool
	outside     []string
	failLoyalty error
}

func (s *personalStore) note(read string) {
	if !s.inSnapshot {
		s.outside = append(s.outside, read)
	}
}

func (s *personalStore) WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error {
	s.inSnapshot = true
	defer func() { s.inSnapshot = false }()

	return fn(ctx)
}

func (s *personalStore) CollectionsOfCustomer(_ context.Context, id string) ([]models.PaymentCollection, error) {
	s.note("collections")
	var out []models.PaymentCollection
	for i := range s.collections {
		if s.collections[i].CustomerID == id {
			out = append(out, s.collections[i])
		}
	}

	return out, nil
}

func (s *personalStore) SessionsOfCollections(_ context.Context, ids []string) ([]models.PaymentSession, error) {
	s.note("sessions")
	var out []models.PaymentSession
	for i := range s.sessions {
		if contains(ids, s.sessions[i].PaymentCollectionID) {
			out = append(out, s.sessions[i])
		}
	}

	return out, nil
}

func (s *personalStore) PaymentsOfCollections(_ context.Context, ids []string) ([]models.Payment, error) {
	s.note("payments")
	var out []models.Payment
	for i := range s.payments {
		if contains(ids, s.payments[i].PaymentCollectionID) {
			out = append(out, s.payments[i])
		}
	}

	return out, nil
}

func (s *personalStore) RefundsOfPayments(_ context.Context, ids []string) ([]models.Refund, error) {
	s.note("refunds")
	var out []models.Refund
	for i := range s.refunds {
		if contains(ids, s.refunds[i].PaymentID) {
			out = append(out, s.refunds[i])
		}
	}

	return out, nil
}

func (s *personalStore) ManualSessionsOfCollections(_ context.Context, ids []string) ([]models.ManualSession, error) {
	s.note("manual")
	var out []models.ManualSession
	for i := range s.manual {
		if contains(ids, s.manual[i].Reference) {
			out = append(out, s.manual[i])
		}
	}

	return out, nil
}

func (s *personalStore) GiftCardSessionsOfCollections(_ context.Context, ids []string) ([]models.TenderSession, error) {
	s.note("gift cards")
	var out []models.TenderSession
	for i := range s.giftCards {
		if contains(ids, s.giftCards[i].Reference) {
			out = append(out, s.giftCards[i])
		}
	}

	return out, nil
}

func (s *personalStore) StoreCreditEntriesOfCustomer(_ context.Context, id string) ([]models.StoreCreditEntry, error) {
	s.note("store credit")
	var out []models.StoreCreditEntry
	for i := range s.storeCreditEntries {
		if s.storeCreditEntries[i].CustomerID == id {
			out = append(out, s.storeCreditEntries[i])
		}
	}

	return out, nil
}

func (s *personalStore) StoreCreditSessionsOfCustomer(_ context.Context, id string) ([]models.TenderSession, error) {
	s.note("store credit sessions")

	return owned(s.storeCreditSession, id), nil
}

func (s *personalStore) LoyaltyEntriesOfCustomer(_ context.Context, id string) ([]models.LoyaltyEntry, error) {
	s.note("loyalty")
	var out []models.LoyaltyEntry
	for i := range s.loyaltyEntries {
		if s.loyaltyEntries[i].CustomerID == id {
			out = append(out, s.loyaltyEntries[i])
		}
	}

	return out, nil
}

func (s *personalStore) LoyaltySessionsOfCustomer(_ context.Context, id string) ([]models.TenderSession, error) {
	s.note("loyalty sessions")
	if s.failLoyalty != nil {
		return nil, s.failLoyalty
	}

	return owned(s.loyaltySessions, id), nil
}

func owned(sessions []models.TenderSession, id string) []models.TenderSession {
	var out []models.TenderSession
	for i := range sessions {
		if sessions[i].OwnerID == id {
			out = append(out, sessions[i])
		}
	}

	return out
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}

	return false
}

// twoCustomers holds one of everything for cus_ada and a collection and a
// credit for cus_bob, whose rows must never reach Ada's answers.
func twoCustomers() *personalStore {
	return &personalStore{
		collections: []models.PaymentCollection{
			{ID: "paycol_ada", CustomerID: "cus_ada", Metadata: map[string]any{"note": "gift for Ada's mother"}},
			{ID: "paycol_bob", CustomerID: "cus_bob"},
		},
		sessions: []models.PaymentSession{
			{
				ID: "payses_ada", PaymentCollectionID: "paycol_ada", IdempotencyKey: "pay-ada-1",
				Data: json.RawMessage(`{"payer":"Ada L."}`), DeclineReason: "card holder name mismatch",
			},
			{ID: "payses_bob", PaymentCollectionID: "paycol_bob", IdempotencyKey: "pay-bob-1"},
		},
		payments: []models.Payment{{ID: "pay_ada", PaymentCollectionID: "paycol_ada"}},
		refunds: []models.Refund{
			{ID: "refund_ada", PaymentID: "pay_ada", Reason: "Ada returned the scarf"},
			{ID: "refund_quiet", PaymentID: "pay_ada"},
		},
		manual:    []models.ManualSession{{ID: "manses_ada", Reference: "paycol_ada", IdempotencyKey: "pay-ada-1"}},
		giftCards: []models.TenderSession{{ID: "gcses_ada", Reference: "paycol_ada", IdempotencyKey: "pay-ada-2"}},
		storeCreditEntries: []models.StoreCreditEntry{
			{ID: "scredit_ada", CustomerID: "cus_ada", Amount: 500, Reason: "late parcel", Reference: "ticket 42"},
			{ID: "scredit_bob", CustomerID: "cus_bob", Amount: 100},
		},
		storeCreditSession: []models.TenderSession{{
			ID: "scrses_ada", OwnerID: "cus_ada", IdempotencyKey: "pay-ada-3",
			DeclineReason: "the balance does not cover this payment: 500 held, 900 asked (EUR)",
		}},
		loyaltyEntries:  []models.LoyaltyEntry{{ID: "lpoint_ada", CustomerID: "cus_ada", Points: 70}},
		loyaltySessions: []models.TenderSession{{ID: "lpses_ada", OwnerID: "cus_ada", IdempotencyKey: "pay-ada-4"}},
	}
}

func fieldOf(record personaldata.Record, column string) (any, bool) {
	for _, field := range record.Fields {
		if field.Column == column {
			return field.Value, true
		}
	}

	return nil, false
}

// TestADisclosureCarriesEveryRowOfTheCustomerAndNoOneElses is the whole
// claim, on one person with one of everything.
func TestADisclosureCarriesEveryRowOfTheCustomerAndNoOneElses(t *testing.T) {
	t.Parallel()

	store := twoCustomers()
	disclosure, err := service.NewPersonalData(store, nil).Disclose(context.Background(),
		personaldata.Subject{CustomerID: " cus_ada ", Email: "ada@example.com"})
	require.NoError(t, err)

	assert.Equal(t, service.Holder, disclosure.Holder)
	assert.Equal(t, personaldata.Disclosed, disclosure.State)
	assert.Empty(t, store.outside, "every read of one answer runs in one snapshot")

	ids := make([]string, 0, len(disclosure.Records))
	for _, record := range disclosure.Records {
		ids = append(ids, record.Table+" "+record.ID)
	}
	assert.Equal(t, []string{
		"payment_collections paycol_ada",
		"payment_sessions paycol_ada/payses_ada",
		"refunds paycol_ada/refund_ada",
		"payment_manual_sessions paycol_ada/manses_ada",
		"payment_gift_card_sessions paycol_ada/gcses_ada",
		"payment_store_credit_entries scredit_ada",
		"payment_store_credit_sessions scrses_ada",
		"payment_loyalty_entries lpoint_ada",
		"payment_loyalty_sessions lpses_ada",
	}, ids, "each collection with what it gathered, then the ledgers; a refund with no reason says nothing")

	byTable := map[string]personaldata.Record{}
	for _, record := range disclosure.Records {
		byTable[record.Table] = record
		if customer, present := fieldOf(record, "customer_id"); present {
			assert.Equal(t, "cus_ada", customer, "%s %s names somebody else", record.Table, record.ID)
		}
	}
	for table, want := range map[string]struct {
		column string
		value  any
	}{
		"payment_collections":           {"metadata", map[string]any{"note": "gift for Ada's mother"}},
		"payment_sessions":              {"data", map[string]any{"payer": "Ada L."}},
		"refunds":                       {"reason", "Ada returned the scarf"},
		"payment_store_credit_entries":  {"amount", int64(500)},
		"payment_store_credit_sessions": {"decline_reason", "the balance does not cover this payment: 500 held, 900 asked (EUR)"},
		"payment_loyalty_entries":       {"points", int64(70)},
	} {
		value, present := fieldOf(byTable[table], want.column)
		require.True(t, present, "%s.%s is not on its record", table, want.column)
		assert.Equal(t, want.value, value, "%s.%s", table, want.column)
	}
}

// TestAnEMailAloneIsUnresolvableHere holds the one shape this module cannot
// answer: a guest's collection names nobody, so an address finds nothing and
// proves nothing.
func TestAnEMailAloneIsUnresolvableHere(t *testing.T) {
	t.Parallel()

	store := twoCustomers()
	answers := service.NewPersonalData(store, nil)

	disclosure, err := answers.Disclose(context.Background(), personaldata.Subject{Email: "ada@example.com"})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Unresolvable, disclosure.State)
	assert.Contains(t, disclosure.Why, "customer id")
	assert.Empty(t, disclosure.Records)

	result, err := answers.Erase(context.Background(), personaldata.Subject{Email: "ada@example.com"})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Retained, result.Outcome)
	assert.Zero(t, result.Rows)
	assert.Empty(t, result.Kept, "nothing of this person was looked at, so nothing is named as kept")
	assert.Contains(t, result.Why, "customer id")
	assert.Empty(t, store.outside)
}

// TestACustomerWithNoRowIsNothingAndSaysWhereItLooked holds the empty answer
// to its sentence.
func TestACustomerWithNoRowIsNothingAndSaysWhereItLooked(t *testing.T) {
	t.Parallel()

	answers := service.NewPersonalData(twoCustomers(), nil)

	disclosure, err := answers.Disclose(context.Background(), personaldata.Subject{CustomerID: "cus_carol"})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Nothing, disclosure.State)
	assert.Contains(t, disclosure.Why, "store credit")

	result, err := answers.Erase(context.Background(), personaldata.Subject{CustomerID: "cus_carol"})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Retained, result.Outcome)
	assert.Zero(t, result.Rows)
	assert.Empty(t, result.Kept)
	assert.NotEmpty(t, result.Why)
}

// TestAnErasureKeepsEverythingAndSaysWhat is the retained answer about a
// customer with rows: every declared column is named as kept.
func TestAnErasureKeepsEverythingAndSaysWhat(t *testing.T) {
	t.Parallel()

	store := twoCustomers()
	result, err := service.NewPersonalData(store, nil).Erase(context.Background(),
		personaldata.Subject{CustomerID: "cus_ada"})
	require.NoError(t, err)

	assert.Equal(t, service.Holder, result.Holder)
	assert.Equal(t, personaldata.Retained, result.Outcome)
	assert.Equal(t, 10, result.Rows, "one collection, its session, both refunds, two provider sessions, "+
		"two ledger entries and two ledger sessions")
	assert.Equal(t, personaldata.Declaration{Holdings: service.PersonalDataHoldings()}.Paths(), result.Kept)
	assert.Contains(t, result.Why, "balance")
	assert.Empty(t, store.outside)
}

// TestARequestThatNamesNobodyIsRefused holds both answers to a refusal.
func TestARequestThatNamesNobodyIsRefused(t *testing.T) {
	t.Parallel()

	answers := service.NewPersonalData(twoCustomers(), nil)

	_, err := answers.Disclose(context.Background(), personaldata.Subject{CustomerID: "  "})
	require.Error(t, err)
	assert.Equal(t, service.CodePersonalDataSubjectEmpty, errors.CodeOf(err))
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = answers.Erase(context.Background(), personaldata.Subject{})
	require.Error(t, err)
	assert.Equal(t, service.CodePersonalDataSubjectEmpty, errors.CodeOf(err))
}

// TestAFailedReadIsAnErrorAndNotAnEmptyAnswer holds a store failure to the
// caller: an answer built from half the reads would be short with nothing
// saying so.
func TestAFailedReadIsAnErrorAndNotAnEmptyAnswer(t *testing.T) {
	t.Parallel()

	store := twoCustomers()
	store.failLoyalty = errors.Unavailable("payment_query_failed", "the database went away")
	answers := service.NewPersonalData(store, nil)

	_, err := answers.Disclose(context.Background(), personaldata.Subject{CustomerID: "cus_ada"})
	require.Error(t, err)
	_, err = answers.Erase(context.Background(), personaldata.Subject{CustomerID: "cus_ada"})
	require.Error(t, err)
}
