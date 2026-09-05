package manual_test

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// newProvider sets up a manual provider that works on an in-memory ledger.
func newProvider() (*manual.Provider, *memStore) {
	store := newMemStore()
	return manual.New(store, nil), store
}

// TestProviderID proves that the registration name is fixed.
func TestProviderID(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()
	assert.Equal(t, "manual", provider.ID())
	assert.Equal(t, manual.ID, provider.ID())
}

// TestQuoteHasNoSideEffects proves the harshest condition of the core contract.
//
// Quote is called over and over while a cart total is being computed; if it
// wrote a single row to the ledger, a customer who left the cart open would
// produce hundreds of records.
func TestQuoteHasNoSideEffects(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	input := coreprovider.QuoteInput{
		OptionID:     "sopt_1",
		CurrencyCode: "TRY",
		ItemCount:    3,
		TotalWeight:  1_200,
		Data:         map[string]any{manual.DataKeyBaseAmount: 1_000},
	}

	first, err := provider.Quote(context.Background(), input)
	require.NoError(t, err)

	for range 5 {
		repeat, err := provider.Quote(context.Background(), input)
		require.NoError(t, err)
		assert.Equal(t, first.Amount, repeat.Amount, "the same input must return the same fee")
	}

	assert.Zero(t, store.writeCount(), "Quote must NEVER write to the ledger")
}

// TestQuoteFormula proves that the fee components are summed the way they are
// documented.
//
// The weight is rounded to the STARTED kilogram: 1200 grams counts as TWO
// kilograms. Rounding down would mean carrying a 1999 gram parcel for the price
// of one kilogram.
func TestQuoteFormula(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		data      map[string]any
		itemCount int64
		weight    int64
		want      int64
	}{
		{"an unconfigured option is free", nil, 3, 5_000, 0},
		{"base only", map[string]any{manual.DataKeyBaseAmount: 2_500}, 3, 5_000, 2_500},
		{
			"base + item",
			map[string]any{manual.DataKeyBaseAmount: 1_000, manual.DataKeyPerItemAmount: 250},
			4, 0, 2_000,
		},
		{
			"a whole kilogram is not rounded up",
			map[string]any{manual.DataKeyPerKilogramAmount: 500},
			0, 1_000, 500,
		},
		{
			"a started kilogram counts as a whole one",
			map[string]any{manual.DataKeyPerKilogramAmount: 500},
			0, 1_001, 1_000,
		},
		{
			"even a single gram is one kilogram",
			map[string]any{manual.DataKeyPerKilogramAmount: 500},
			0, 1, 500,
		},
		{
			"all components",
			map[string]any{
				manual.DataKeyBaseAmount:        1_000,
				manual.DataKeyPerItemAmount:     100,
				manual.DataKeyPerKilogramAmount: 400,
			},
			3, 2_500, 1_000 + 300 + 1_200,
		},
		{
			"the direct amount overrides the components",
			map[string]any{
				manual.DataKeyQuoteAmount:   7_777,
				manual.DataKeyBaseAmount:    1_000,
				manual.DataKeyPerItemAmount: 100,
			},
			5, 9_000, 7_777,
		},
		{
			"a zero amount is valid",
			map[string]any{manual.DataKeyQuoteAmount: 0, manual.DataKeyBaseAmount: 5_000},
			1, 1_000, 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider, _ := newProvider()
			quote, err := provider.Quote(context.Background(), coreprovider.QuoteInput{
				OptionID:     "sopt_1",
				CurrencyCode: "TRY",
				ItemCount:    tc.itemCount,
				TotalWeight:  tc.weight,
				Data:         tc.data,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, quote.Amount)
			assert.Equal(t, "TRY", quote.CurrencyCode)
			assert.Equal(t, "sopt_1", quote.OptionID)
		})
	}
}

// TestQuoteInputValidation proves that invalid input is rejected.
func TestQuoteInputValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input coreprovider.QuoteInput
	}{
		{"no option", coreprovider.QuoteInput{CurrencyCode: "TRY"}},
		{"broken currency", coreprovider.QuoteInput{OptionID: "sopt_1", CurrencyCode: "TR"}},
		{"negative item count", coreprovider.QuoteInput{
			OptionID: "sopt_1", CurrencyCode: "TRY", ItemCount: -1,
		}},
		{"negative weight", coreprovider.QuoteInput{
			OptionID: "sopt_1", CurrencyCode: "TRY", TotalWeight: -1,
		}},
		{"negative fee component", coreprovider.QuoteInput{
			OptionID: "sopt_1", CurrencyCode: "TRY",
			Data: map[string]any{manual.DataKeyBaseAmount: -1},
		}},
		{"unrecognized behavior", coreprovider.QuoteInput{
			OptionID: "sopt_1", CurrencyCode: "TRY",
			Data: map[string]any{manual.DataKeyOutcome: "explode"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider, _ := newProvider()
			_, err := provider.Quote(context.Background(), tc.input)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "the error must be errors.Invalid: %v", err)
		})
	}
}

// TestQuoteFailureInjection proves that saga tests can blow up the fulfillment
// step.
func TestQuoteFailureInjection(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()
	_, err := provider.Quote(context.Background(), coreprovider.QuoteInput{
		OptionID:     "sopt_1",
		CurrencyCode: "TRY",
		Data:         map[string]any{manual.DataKeyOutcome: manual.OutcomeError},
	})
	require.Error(t, err)
	assert.Equal(t, manual.CodeSimulatedFailure, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"the injected error must be RETRYABLE: %v", err)
}

// TestQuoteOverflowIsCaught proves that the product has an overflow check,
// because the item count and the weight come from outside.
//
// Had it overflowed, a NEGATIVE shipping fee would silently come out — that is,
// an order that pays money to the customer.
func TestQuoteOverflowIsCaught(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()
	_, err := provider.Quote(context.Background(), coreprovider.QuoteInput{
		OptionID:     "sopt_1",
		CurrencyCode: "TRY",
		ItemCount:    1 << 40,
		Data:         map[string]any{manual.DataKeyPerItemAmount: 1_000_000_000},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error must be errors.Invalid: %v", err)
}

// TestQuoteKilogramRoundingDoesNotOverflow proves that the rounding does not
// overflow IN ITS INTERMEDIATE STEP.
//
// Regression: the rounding was in the "(grams + 999) / 1000" form and the
// addition OVERFLOWED at the top of int64, producing a negative kilogram count.
// A negative kilogram count slipped through the overflow checks, which only
// look at the positive operand, and Quote returned a NEGATIVE fee without an
// error (measured: -9223372036854774000). That was a violation of the
// provider's "I return a negative fee for no input" contract.
func TestQuoteKilogramRoundingDoesNotOverflow(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()
	for _, weight := range []int64{
		math.MaxInt64,
		math.MaxInt64 - 500,
		math.MaxInt64 - 999,
	} {
		quote, err := provider.Quote(context.Background(), coreprovider.QuoteInput{
			OptionID:     "sopt_1",
			CurrencyCode: "TRY",
			TotalWeight:  weight,
			Data:         map[string]any{manual.DataKeyPerKilogramAmount: 1_000},
		})
		require.Error(t, err, "%d grams must return an error", weight)
		assert.True(t, errors.IsInvalid(err), "the error must be errors.Invalid: %v", err)
		assert.GreaterOrEqual(t, quote.Amount, models.MinAmount,
			"no negative fee may leak even on the error branch: %d", quote.Amount)
	}
}

// TestQuoteBoundaryWeightIsPricedWithoutOverflow proves that the LARGEST weight
// the module allows can still be computed.
//
// It shows that the overflow fix came at no price: the bounds do not shut valid
// input out.
func TestQuoteBoundaryWeightIsPricedWithoutOverflow(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()
	quote, err := provider.Quote(context.Background(), coreprovider.QuoteInput{
		OptionID:     "sopt_1",
		CurrencyCode: "TRY",
		TotalWeight:  models.MaxTotalWeight,
		Data:         map[string]any{manual.DataKeyPerKilogramAmount: 1},
	})
	require.NoError(t, err)
	assert.Equal(t, models.MaxTotalWeight/1_000, quote.Amount)
}

// TestCreateIsIdempotent proves that a second call with the same key does not
// open a NEW shipment.
func TestCreateIsIdempotent(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	input := coreprovider.CreateFulfillmentInput{
		Reference:      "ful_1",
		OptionID:       "sopt_1",
		IdempotencyKey: "key-1",
	}

	first, err := provider.Create(context.Background(), input)
	require.NoError(t, err)
	writes := store.writeCount()

	second, err := provider.Create(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID, "the same key must return the same shipment")
	assert.Equal(t, writes, store.writeCount(), "the second call must not write to the ledger")
}

// TestCreateSameKeyDifferentBodyConflicts proves that idempotency means
// "repeating the same request".
func TestCreateSameKeyDifferentBodyConflicts(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()
	_, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_1", OptionID: "sopt_1", IdempotencyKey: "key-1",
	})
	require.NoError(t, err)

	_, err = provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_2", OptionID: "sopt_1", IdempotencyKey: "key-1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error must be errors.Conflict: %v", err)
	assert.Equal(t, manual.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestCreateSameKeyDifferentOptionConflicts exercises the SECOND half of the
// comparison: the reference is the same, the shipping OPTION is different.
//
// A test that only changed the reference could not catch a mutation that
// deleted the option comparison; that was the proof of the claim "the same key
// may not be used with a different option", and it was missing. Had the second
// one been accepted silently, a caller asking for express shipping would get a
// shipment opened with standard shipping.
func TestCreateSameKeyDifferentOptionConflicts(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()
	_, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_1", OptionID: "sopt_1", IdempotencyKey: "key-1",
	})
	require.NoError(t, err)

	_, err = provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_1", OptionID: "sopt_2", IdempotencyKey: "key-1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error must be errors.Conflict: %v", err)
	assert.Equal(t, manual.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestCreateConcurrentProducesOneShipment proves that the race is settled at
// the ledger level.
func TestCreateConcurrentProducesOneShipment(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()

	const concurrency = 8
	ids := make([]string, concurrency)
	errs := make([]error, concurrency)

	var start sync.WaitGroup
	var finished sync.WaitGroup
	start.Add(1)
	finished.Add(concurrency)

	for i := range concurrency {
		go func() {
			defer finished.Done()
			start.Wait()
			ful, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
				Reference: "ful_1", OptionID: "sopt_1", IdempotencyKey: "key-race",
			})
			ids[i], errs[i] = ful.ID, err
		}()
	}
	start.Done()
	finished.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "call %d returned an error", i)
	}
	for i := 1; i < concurrency; i++ {
		assert.Equal(t, ids[0], ids[i], "all calls must return the same shipment")
	}
	assert.Equal(t, 1, store.writeCount(), "EXACTLY one row must be written to the ledger")
}

// TestCreateFailureInjection proves that the shipment-opening step can be blown
// up and that the ledger stays EMPTY.
func TestCreateFailureInjection(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	_, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference:      "ful_1",
		OptionID:       "sopt_1",
		IdempotencyKey: "key-1",
		Data:           map[string]any{manual.DataKeyOutcome: manual.OutcomeError},
	})
	require.Error(t, err)
	assert.Equal(t, manual.CodeSimulatedFailure, errors.CodeOf(err))
	assert.Zero(t, store.writeCount(), "a call that blows up must not write to the ledger")
}

// TestCreateAcceptsTrackingDetails proves that the injected tracking details
// are STORED together with the shipment.
//
// Had they not been stored, a later read (possibly even in a different process)
// would not find the tracking number.
func TestCreateAcceptsTrackingDetails(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()
	ful, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference:      "ful_1",
		OptionID:       "sopt_1",
		IdempotencyKey: "key-1",
		Data: map[string]any{
			manual.DataKeyTrackingNumber: "TK-42",
			manual.DataKeyTrackingURL:    "https://carrier.example/TK-42",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "TK-42", ful.TrackingNumber)
	assert.Equal(t, "https://carrier.example/TK-42", ful.TrackingURL)

	stored, err := provider.GetShipment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, "TK-42", stored.TrackingNumber, "the tracking details must be durable")
}

// TestCreateInputValidation proves that the required fields are checked.
func TestCreateInputValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input coreprovider.CreateFulfillmentInput
	}{
		{"no key", coreprovider.CreateFulfillmentInput{Reference: "ful_1", OptionID: "sopt_1"}},
		{"no reference", coreprovider.CreateFulfillmentInput{OptionID: "sopt_1", IdempotencyKey: "a"}},
		{"no option", coreprovider.CreateFulfillmentInput{Reference: "ful_1", IdempotencyKey: "a"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider, _ := newProvider()
			_, err := provider.Create(context.Background(), tc.input)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "the error must be errors.Invalid: %v", err)
		})
	}
}

// TestCancelIsIdempotent proves the condition of the saga compensation.
func TestCancelIsIdempotent(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	ful, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_1", OptionID: "sopt_1", IdempotencyKey: "key-1",
	})
	require.NoError(t, err)

	require.NoError(t, provider.Cancel(context.Background(), ful.ID))
	writes := store.writeCount()

	require.NoError(t, provider.Cancel(context.Background(), ful.ID),
		"the second cancellation must not return an error")
	assert.Equal(t, writes, store.writeCount(),
		"the second cancellation must not write to the ledger")

	stored, err := provider.GetShipment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, stored.Status)
}

// TestCancelUnknownIDReturnsNotFound proves that idempotency does NOT mean
// "swallow everything silently".
func TestCancelUnknownIDReturnsNotFound(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()

	err := provider.Cancel(context.Background(), "manful_NOSUCHID")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the error must be errors.NotFound: %v", err)

	err = provider.Cancel(context.Background(), "   ")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an empty identifier must be errors.Invalid: %v", err)
}

// TestCancelPreservesTrackingDetails proves that which label a canceled
// shipment was opened with stays readable for diagnosis.
func TestCancelPreservesTrackingDetails(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()
	ful, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_1", OptionID: "sopt_1", IdempotencyKey: "key-1",
		Data: map[string]any{manual.DataKeyTrackingNumber: "TK-42"},
	})
	require.NoError(t, err)
	require.NoError(t, provider.Cancel(context.Background(), ful.ID))

	stored, err := provider.GetShipment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, "TK-42", stored.TrackingNumber)
}

// TestGetShipmentRejectsEmptyID proves that the diagnostic surface is validated
// as well.
func TestGetShipmentRejectsEmptyID(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()
	_, err := provider.GetShipment(context.Background(), " ")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error must be errors.Invalid: %v", err)
}

// TestCoreContractIsSatisfied pins down at compile time that the provider
// satisfies the core interface.
func TestCoreContractIsSatisfied(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()
	var contract coreprovider.FulfillmentProvider = provider
	assert.Equal(t, manual.ID, contract.ID())
}
