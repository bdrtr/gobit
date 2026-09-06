package service_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// createFulfillment creates a fulfillment for the test.
func (k testSetup) createFulfillment(t *testing.T, optionID, key string) models.Fulfillment {
	t.Helper()
	ful, err := k.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   key,
	})
	require.NoError(t, err)
	return ful
}

// readyOption opens a profile and an option and returns the option's identifier.
func readyOption(t *testing.T, setup testSetup) string {
	t.Helper()
	profileID := setup.createProfile(t, "default")
	return setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})
}

// TestTheFulfillmentGivesTheProviderItsOwnID proves that the Reference passed to
// the provider is THE FULFILLMENT's identifier.
//
// The core contract (core/provider) defines Reference as "the field
// that matches the two systems during reconciliation". Had the order identifier
// been given, two fulfillments of the same order could not be told apart on the
// provider's side.
func TestTheFulfillmentGivesTheProviderItsOwnID(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)

	ful := setup.createFulfillment(t, optionID, "key-1")

	input := setup.provider.lastCreateInput()
	assert.Equal(t, ful.ID, input.Reference, "the fulfillment's own identifier has to be given to the provider")
	assert.NotEqual(t, "order_1", input.Reference, "the order identifier must not be given")
	assert.Equal(t, optionID, input.OptionID)
	assert.Equal(t, "key-1", input.IdempotencyKey)

	assert.Equal(t, "ext_key-1", ful.ExternalID, "the provider's identifier has to be written to the record")
	assert.Equal(t, models.StatusPending, ful.Status)
	assert.Equal(t, "TK-key-1", ful.TrackingNumber)
	assert.Nil(t, ful.ShippedAt, "a pending fulfillment must not have a dispatch stamp")
}

// TestTheOptionConfigurationReachesTheProvider proves that the option's Data
// field is handed to the provider while the fulfillment is being opened as well.
//
// Had it not been handed over, the provider could not know which account
// (contract number, carrier setting) to print the label against; the
// configuration that works in the price query disappearing while the fulfillment
// is opened would be a silent inconsistency.
//
// On a clash THE REQUEST's data wins: the configuration is the store's fixed
// setting, while the request is specific to that fulfillment and is more
// particular.
func TestTheOptionConfigurationReachesTheProvider(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
		Data: map[string]any{
			"contract_no": "SZ-42",
			"branch":      "central",
		},
	})

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-1",
		Data:             map[string]any{"branch": "downtown", "address": "..."},
	})
	require.NoError(t, err)

	input := setup.provider.lastCreateInput()
	assert.Equal(t, "SZ-42", input.Data["contract_no"], "the option's configuration has to be handed over")
	assert.Equal(t, "...", input.Data["address"], "the request's data has to be handed over")
	assert.Equal(t, "downtown", input.Data["branch"], "on a clash the request's data has to win")

	option, err := setup.svc.GetShippingOption(context.Background(), optionID)
	require.NoError(t, err)
	assert.Equal(t, "central", option.Data["branch"],
		"the merge MUST NOT MODIFY the option's configuration")
}

// TestTheSameKeyDoesNotProduceASecondFulfillment proves the idempotency
// requirement.
//
// This is the core contract's requirement: a repeat without a key would mean A
// SECOND SHIPPING LABEL. That the second call does NOT go to the provider at all
// is exercised too.
func TestTheSameKeyDoesNotProduceASecondFulfillment(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)

	first := setup.createFulfillment(t, optionID, "key-1")
	second := setup.createFulfillment(t, optionID, "key-1")

	assert.Equal(t, first.ID, second.ID, "the same key has to return the same fulfillment")
	assert.Equal(t, first.ExternalID, second.ExternalID)

	_, create, _ := setup.provider.callCounts()
	assert.Equal(t, 1, create, "the second call MUST NOT go to the provider")
}

// TestTheSameKeyCannotBeUsedForADifferentFulfillment proves that idempotency
// means "repeating the same request".
//
// Had it been accepted silently, the fulfillment the caller believed it opened
// would never have been opened.
func TestTheSameKeyCannotBeUsedForADifferentFulfillment(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.createFulfillment(t, optionID, "key-1")

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_2",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
	assert.Equal(t, service.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestTheSameKeyCannotBeUsedForADifferentOption exercises the SECOND half of the
// comparison: same reference, different shipping OPTION.
//
// Regression: a test that only changed the reference COULD NOT CATCH a mutation
// that deleted the option comparison from the condition; there was no proof at
// all of the claim "the same key cannot be used with a different option". Had it
// been accepted silently, a caller asking for express shipping would get a
// fulfillment opened with standard shipping and the customer would pay for the
// wrong service.
func TestTheSameKeyCannotBeUsedForADifferentOption(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	firstOption := readyOption(t, setup)
	profileID := setup.createProfile(t, "express")
	secondOption := setup.createOption(t, service.CreateOptionInput{
		Name:              "Express shipping",
		ShippingProfileID: profileID,
		Amount:            9_000,
	})

	first := setup.createFulfillment(t, firstOption, "key-1")

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		// The reference is THE SAME; the only thing that changes is the shipping
		// option.
		Reference:        first.Reference,
		ShippingOptionID: secondOption,
		IdempotencyKey:   "key-1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
	assert.Equal(t, service.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestTheSameKeyCannotBeUsedWithADifferentItemList proves that the repeat branch
// compares THE ITEM LIST as well.
//
// Regression: only the reference and the option were being compared. A second
// request arriving with a different item breakdown returned no error and handed
// back the existing fulfillment WITH ITS OWN items; the caller (e.g. an admin
// request that believed it had corrected the breakdown) believed the write had
// happened, while in fact nothing had been written.
func TestTheSameKeyCannotBeUsedWithADifferentItemList(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)

	first, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-1",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "li_1", Quantity: 1},
		},
	})
	require.NoError(t, err)
	require.Len(t, first.Items, 1)

	_, err = setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-1",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "li_9", Quantity: 7},
		},
	})
	require.Error(t, err, "a different item list must not be swallowed silently")
	assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
	assert.Equal(t, service.CodeIdempotencyMismatch, errors.CodeOf(err))

	// The record has to be UNCHANGED: a rejected request writes nothing.
	current, err := setup.svc.GetFulfillment(context.Background(), first.ID)
	require.NoError(t, err)
	require.Len(t, current.Items, 1)
	assert.Equal(t, "li_1", current.Items[0].LineItemID)
}

// TestTheSameItemSetInADifferentOrderIsARepeat proves that the comparison is a
// SET.
//
// A difference in order is not a difference: a repeat that sends the same items
// in another order is a real repeat and must not return Conflict.
func TestTheSameItemSetInADifferentOrderIsARepeat(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)

	first, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-1",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "li_1", Quantity: 2},
			{LineItemID: "li_2", Quantity: 3},
		},
	})
	require.NoError(t, err)

	second, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-1",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "li_2", Quantity: 3},
			{LineItemID: "li_1", Quantity: 2},
		},
	})
	require.NoError(t, err, "the same item set in another order is a repeat too")
	assert.Equal(t, first.ID, second.ID)
}

// TestTwoConcurrentCreatesProduceOneFulfillment proves that the race is resolved
// at a single point.
//
// The fake store imitates the unique key constraint; of the goroutines running
// with the same key only one must be able to write the row.
func TestTwoConcurrentCreatesProduceOneFulfillment(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)

	const concurrent = 8
	ids := make([]string, concurrent)
	errs := make([]error, concurrent)

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(concurrent)

	for i := range concurrent {
		go func() {
			defer done.Done()
			start.Wait()
			ful, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
				Reference:        "order_1",
				ShippingOptionID: optionID,
				IdempotencyKey:   "key-race",
			})
			ids[i], errs[i] = ful.ID, err
		}()
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "call %d returned an error", i)
	}
	for i := 1; i < concurrent; i++ {
		assert.Equal(t, ids[0], ids[i], "all calls have to return the same fulfillment")
	}

	_, create, _ := setup.provider.callCounts()
	assert.Equal(t, 1, create, "the provider has to be called EXACTLY once")
}

// TestFulfillmentItemsAreWritten proves that the items are recorded together
// with the fulfillment.
func TestFulfillmentItemsAreWritten(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)

	ful, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-1",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "line_1", Quantity: 2},
			{LineItemID: "line_2", Quantity: 1},
		},
	})
	require.NoError(t, err)
	require.Len(t, ful.Items, 2)

	stored, err := setup.svc.GetFulfillment(context.Background(), ful.ID)
	require.NoError(t, err)
	require.Len(t, stored.Items, 2, "the items have to be returned on the read path too")

	total := int64(0)
	for _, item := range stored.Items {
		total += item.Quantity
	}
	assert.Equal(t, int64(3), total)
}

// TestAnItemWriteFailureRollsBackTheFulfillment proves that the transaction
// boundary really is atomic.
//
// When the item write blows up, the fulfillment row has to be rolled back as
// well; otherwise a fulfillment whose label was printed at the provider but whose
// items are empty would be left behind.
func TestAnItemWriteFailureRollsBackTheFulfillment(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.store.failCreateItem = errors.Internal("test_item_write_failed", "the item could not be written")

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-1",
		Items:            []service.FulfillmentItemInput{{LineItemID: "line_1", Quantity: 1}},
	})
	require.Error(t, err)

	list, count, err := setup.svc.ListFulfillments(context.Background(), service.ListFulfillmentsInput{})
	require.NoError(t, err)
	assert.Zero(t, count, "no fulfillment must be left behind by a failed transaction")
	assert.Empty(t, list)
}

// TestProviderErrorLeavesNoFulfillment proves that the row is rolled back when
// the provider blows up.
//
// Had it stayed, a fulfillment record with no counterpart at the provider would
// be left behind and the cancellation flow would be dealing with a row it could
// never close.
func TestProviderErrorLeavesNoFulfillment(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.provider.createErr = errors.Unavailable("test_provider_down", "the carrier is unreachable")

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-1",
	})
	require.Error(t, err)

	_, count, err := setup.svc.ListFulfillments(context.Background(), service.ListFulfillmentsInput{})
	require.NoError(t, err)
	assert.Zero(t, count)
}

// TestAnEmptyProviderIDIsAContractViolation proves that the provider failing to
// give an identifier is not accepted silently.
//
// Had it been accepted, the field that matches the two systems during
// reconciliation would stay empty.
func TestAnEmptyProviderIDIsAContractViolation(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.provider.createErr = nil
	setup.provider.createStatus = coreprovider.FulfillmentPending

	emptyProvider := &emptyIDProvider{}
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(emptyProvider))
	svc, err := service.New(service.Options{Store: setup.store, Providers: registry})
	require.NoError(t, err)

	_, err = svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-1",
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeProviderContract, errors.CodeOf(err))
}

// emptyIDProvider is a provider that returns an empty identifier; it is there to
// exercise the contract violation.
type emptyIDProvider struct{}

// ID returns the fake provider's identifier.
func (p *emptyIDProvider) ID() string { return "fake" }

// Quote returns a zero fee; this provider only exercises the Create branch.
func (p *emptyIDProvider) Quote(
	_ context.Context,
	in coreprovider.QuoteInput,
) (coreprovider.ShippingQuote, error) {
	return coreprovider.ShippingQuote{OptionID: in.OptionID, CurrencyCode: in.CurrencyCode}, nil
}

// Create returns a fulfillment with an EMPTY identifier.
func (p *emptyIDProvider) Create(
	_ context.Context,
	_ coreprovider.CreateFulfillmentInput,
) (coreprovider.Fulfillment, error) {
	return coreprovider.Fulfillment{Status: coreprovider.FulfillmentPending}, nil
}

// Cancel does nothing.
func (p *emptyIDProvider) Cancel(_ context.Context, _ string) error { return nil }

// TestCancellationIsIdempotent proves the requirement of the saga compensation.
//
// A second cancellation MUST NOT fail, MUST NOT go to the provider A SECOND TIME
// and MUST NOT write to the fulfillment row again.
func TestCancellationIsIdempotent(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	ful := setup.createFulfillment(t, optionID, "key-1")

	require.NoError(t, setup.svc.CancelFulfillment(context.Background(), ful.ID))
	writeCount := setup.store.fulfillmentWriteCount()

	require.NoError(t, setup.svc.CancelFulfillment(context.Background(), ful.ID),
		"the second cancellation must not return an error")

	_, _, cancel := setup.provider.callCounts()
	assert.Equal(t, 1, cancel, "the provider has to be called only once")
	assert.Equal(t, writeCount, setup.store.fulfillmentWriteCount(),
		"the second cancellation must not write to the row")

	stored, err := setup.svc.GetFulfillment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, stored.Status)
	require.NotNil(t, stored.CanceledAt, "the cancellation moment has to be written")
	assert.Equal(t, testNow, *stored.CanceledAt)
}

// TestCancelOnAnUnknownIDReturnsNotFound proves that idempotency DOES NOT MEAN
// "swallow everything silently".
//
// A real fulfillment canceled twice and an identifier that never existed are
// different situations; the second is an error on the caller's side.
func TestCancelOnAnUnknownIDReturnsNotFound(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	err := setup.svc.CancelFulfillment(context.Background(), "ful_NOSUCHTHING")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the error has to be errors.NotFound: %v", err)
}

// TestADeliveredFulfillmentCannotBeCanceled proves the decision Phase 7 asks for
// explicitly.
//
// Delivery is a physical fact that cannot be undone; "cancellation" would be a
// lie about the physical world and its remedy is a RETURN. The rule is the same
// as a captured session in payment not being cancelable but refundable.
func TestADeliveredFulfillmentCannotBeCanceled(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	ful := setup.createFulfillment(t, optionID, "key-1")

	_, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err)
	_, err = setup.svc.MarkDelivered(context.Background(), ful.ID)
	require.NoError(t, err)

	err = setup.svc.CancelFulfillment(context.Background(), ful.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))

	_, _, cancel := setup.provider.callCounts()
	assert.Zero(t, cancel, "a rejected cancellation must not go to the provider")
}

// TestAShippedFulfillmentCanBeCanceled proves the other half of the decision.
//
// A parcel in transit can be recalled by the carrier; closing that off would
// force the operator to work outside the system.
func TestAShippedFulfillmentCanBeCanceled(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	ful := setup.createFulfillment(t, optionID, "key-1")

	_, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err)

	require.NoError(t, setup.svc.CancelFulfillment(context.Background(), ful.ID))

	stored, err := setup.svc.GetFulfillment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, stored.Status)
	require.NotNil(t, stored.ShippedAt, "the dispatch moment HAS TO BE KEPT; cancellation does not erase history")
}

// TestCancelGoesToTheProviderWithTheProvidersID proves that what is given to the
// provider is NOT the module's own identifier but the provider's.
//
// Had the module's identifier been given, the provider could never find the
// right record.
func TestCancelGoesToTheProviderWithTheProvidersID(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	ful := setup.createFulfillment(t, optionID, "key-1")

	require.NoError(t, setup.svc.CancelFulfillment(context.Background(), ful.ID))

	setup.provider.mu.Lock()
	canceled := append([]string(nil), setup.provider.canceledIDs...)
	setup.provider.mu.Unlock()

	require.Len(t, canceled, 1)
	assert.Equal(t, ful.ExternalID, canceled[0])
	assert.NotEqual(t, ful.ID, canceled[0])
}

// TestAProviderCancelFailureDoesNotChangeTheRecord proves that the compensation
// does not stop halfway.
//
// Writing "canceled" into the record while the provider could not cancel would
// mean telling the saga it was rolled back while in reality the label stays open.
func TestAProviderCancelFailureDoesNotChangeTheRecord(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	ful := setup.createFulfillment(t, optionID, "key-1")
	setup.provider.cancelErr = errors.Unavailable("test_cancel_failed", "the carrier is unreachable")

	err := setup.svc.CancelFulfillment(context.Background(), ful.ID)
	require.Error(t, err)

	stored, err := setup.svc.GetFulfillment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusPending, stored.Status, "the status must not change")
	assert.Nil(t, stored.CanceledAt)
}

// TestStateTransitions exercises the state machine's counterpart on the service.
func TestStateTransitions(t *testing.T) {
	t.Parallel()

	t.Run("a pending fulfillment CAN be delivered and its dispatch stays unknown", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		ful := setup.createFulfillment(t, readyOption(t, setup), "key-1")

		delivered, err := setup.svc.MarkDelivered(context.Background(), ful.ID)
		require.NoError(t, err,
			"a delivery reported before any collection is the ordinary out-of-order case; "+
				"refusing it would discard the delivery too")
		assert.Equal(t, models.StatusDelivered, delivered.Status)
		require.NotNil(t, delivered.DeliveredAt)
		assert.Nil(t, delivered.ShippedAt,
			"the dispatch moment was never reported and must NOT be invented; a stamp taken "+
				"from the clock here would sit AFTER the delivery it precedes")
	})

	t.Run("a fulfillment that came back cannot be delivered", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		ful := setup.createFulfillment(t, readyOption(t, setup), "key-1")
		_, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
		require.NoError(t, err)
		_, err = setup.svc.MarkReturned(context.Background(), ful.ID)
		require.NoError(t, err)

		_, err = setup.svc.MarkDelivered(context.Background(), ful.ID)
		require.Error(t, err)
		assert.True(t, errors.IsConflict(err),
			"a parcel cannot be in the merchant's warehouse and the customer's hands at once: %v", err)
	})

	t.Run("a canceled fulfillment cannot be shipped", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		ful := setup.createFulfillment(t, readyOption(t, setup), "key-1")
		require.NoError(t, setup.svc.CancelFulfillment(context.Background(), ful.ID))

		_, err := setup.svc.MarkShipped(context.Background(), ful.ID, "", "")
		require.Error(t, err)
		assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
	})

	t.Run("a delivered fulfillment cannot be shipped", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		ful := setup.createFulfillment(t, readyOption(t, setup), "key-1")
		_, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
		require.NoError(t, err)
		_, err = setup.svc.MarkDelivered(context.Background(), ful.ID)
		require.NoError(t, err)

		_, err = setup.svc.MarkShipped(context.Background(), ful.ID, "TK-2", "")
		require.Error(t, err)
		assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
	})
}

// TestMarkShippedIsIdempotent proves that a second call with the same tracking
// number does not fail, while a DIFFERENT number conflicts.
func TestMarkShippedIsIdempotent(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := setup.createFulfillment(t, readyOption(t, setup), "key-1")

	first, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "https://shipping/1")
	require.NoError(t, err)
	assert.Equal(t, models.StatusShipped, first.Status)
	require.NotNil(t, first.ShippedAt)
	assert.Equal(t, testNow, *first.ShippedAt)

	second, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err, "a second call with the same number must not fail")
	assert.Equal(t, first.ShippedAt, second.ShippedAt, "the dispatch moment must not change")

	empty, err := setup.svc.MarkShipped(context.Background(), ful.ID, "", "")
	require.NoError(t, err, "a repeat without a number must not fail either")
	assert.Equal(t, "TK-1", empty.TrackingNumber)

	_, err = setup.svc.MarkShipped(context.Background(), ful.ID, "TK-2", "")
	require.Error(t, err, "a different number is a new request")
	assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
}

// TestMarkDeliveredIsIdempotent proves that a second delivery notification does
// not fail and DOES NOT CHANGE the delivery moment.
func TestMarkDeliveredIsIdempotent(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := setup.createFulfillment(t, readyOption(t, setup), "key-1")
	_, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err)

	first, err := setup.svc.MarkDelivered(context.Background(), ful.ID)
	require.NoError(t, err)
	require.NotNil(t, first.DeliveredAt)

	writeCount := setup.store.fulfillmentWriteCount()
	second, err := setup.svc.MarkDelivered(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, first.DeliveredAt, second.DeliveredAt)
	assert.Equal(t, writeCount, setup.store.fulfillmentWriteCount(),
		"the idempotent branch must not write to the row")
}

// TestStateChangingFlowsTakeALock proves the concurrency contract.
//
// Had the lock not been taken, two calls canceling the same fulfillment at the
// same time would go to the provider TWICE. The fake store hands out the lock
// only inside a transaction; when the lock is not taken the test still compiles
// but the claim fails.
func TestStateChangingFlowsTakeALock(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := setup.createFulfillment(t, readyOption(t, setup), "key-1")
	// The setup takes locks as well (creating an option locks the profile in
	// shared mode); what is under test is the locks of the two flows below.
	setup.store.resetLocks()

	_, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err)
	_, err = setup.svc.MarkDelivered(context.Background(), ful.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{"fulfillment", "fulfillment"}, setup.store.lockOrder(),
		"every state-changing flow has to lock the fulfillment row")
}

// TestFulfillmentInputIsValidated proves that an invalid input is rejected with
// errors.Invalid.
func TestFulfillmentInputIsValidated(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)

	cases := []struct {
		name  string
		input service.CreateFulfillmentInput
	}{
		{"no reference", service.CreateFulfillmentInput{
			ShippingOptionID: optionID, IdempotencyKey: "a",
		}},
		{"no option", service.CreateFulfillmentInput{
			Reference: "order_1", IdempotencyKey: "a",
		}},
		{"wrong option prefix", service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: "sprof_XYZ", IdempotencyKey: "a",
		}},
		{"no key", service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: optionID,
		}},
		{"zero item quantity", service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "a",
			Items: []service.FulfillmentItemInput{{LineItemID: "line_1", Quantity: 0}},
		}},
		{"the same line twice", service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "a",
			Items: []service.FulfillmentItemInput{
				{LineItemID: "line_1", Quantity: 1},
				{LineItemID: "line_1", Quantity: 2},
			},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := setup.svc.CreateFulfillment(context.Background(), tc.input)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
		})
	}
}

// TestFulfillmentListingAttachesTheItems proves that the list path fetches the
// items in a batch.
func TestFulfillmentListingAttachesTheItems(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)

	for i, key := range []string{"key-1", "key-2"} {
		_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
			Reference:        "order_1",
			ShippingOptionID: optionID,
			IdempotencyKey:   key,
			Items: []service.FulfillmentItemInput{
				{LineItemID: "line_" + key, Quantity: int64(i + 1)},
			},
		})
		require.NoError(t, err)
	}

	list, count, err := setup.svc.ListFulfillments(context.Background(), service.ListFulfillmentsInput{})
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	require.Len(t, list, 2)
	for _, ful := range list {
		assert.Len(t, ful.Items, 1, "every fulfillment's item has to be attached")
	}
}

// TestCarrierEventsArriveOutOfOrderAndRepeat is the correctness proof of the
// tolerance a carrier plugin depends on.
//
// It is written as a REPLAY of event sequences rather than as separate cases
// per transition, because the property is about a SEQUENCE: a carrier's webhook
// is at-least-once and unordered, so the same three events reach us in an order
// nobody chose and some of them twice. Asserting the end state of each sequence
// is the only way to state "however they arrive, the record ends up saying the
// same thing".
//
// The measurement this replaced: before 2026-09-06 the module tolerated the
// REPEAT and refused the REORDER. Duplicates landed on the noop branch of all
// three transitions, while "delivered" before "shipped" — the single most
// common carrier reordering — returned errors.Conflict. Half of at-least-once
// delivery was handled and the other half returned an error the carrier would
// retry forever.
func TestCarrierEventsArriveOutOfOrderAndRepeat(t *testing.T) {
	t.Parallel()

	const (
		ship    = "ship"
		deliver = "deliver"
	)

	sequences := []struct {
		name string
		// events is the order the carrier's messages REACH us, duplicates
		// included.
		events []string
		// shippedKnown says whether the dispatch moment ends up recorded. It is
		// false exactly when the collection message never arrived BEFORE the
		// delivery: the moment is not invented afterwards.
		shippedKnown bool
	}{
		{"in order", []string{ship, deliver}, true},
		{"reversed", []string{deliver, ship}, false},
		{"reversed with both repeated", []string{deliver, ship, deliver, ship}, false},
		{"in order with the delivery repeated three times", []string{ship, deliver, deliver, deliver}, true},
		{"the collection repeated before the delivery", []string{ship, ship, deliver}, true},
		{"every message twice, interleaved", []string{deliver, deliver, ship, ship}, false},
	}

	for _, sequence := range sequences {
		t.Run(sequence.name, func(t *testing.T) {
			t.Parallel()

			setup := newSetup(t)
			ful := setup.createFulfillment(t, readyOption(t, setup), "key-"+sequence.name)

			for i, event := range sequence.events {
				var err error
				switch event {
				case ship:
					// The carrier repeats the waybill number it issued when the
					// shipment was opened; a DIFFERENT number is a separate
					// case and is a conflict on purpose
					// (TestALateCollectionMessageStillRefusesAContradiction).
					_, err = setup.svc.MarkShipped(context.Background(), ful.ID, ful.TrackingNumber, "")
				case deliver:
					_, err = setup.svc.MarkDelivered(context.Background(), ful.ID)
				}
				require.NoErrorf(t, err,
					"message %d (%q) of the sequence was refused; a carrier cannot choose the "+
						"order its webhooks arrive in, and a refusal here is a message it retries forever",
					i+1, event)
			}

			final, err := setup.svc.GetFulfillment(context.Background(), ful.ID)
			require.NoError(t, err)

			assert.Equal(t, models.StatusDelivered, final.Status,
				"however the messages arrive, the parcel was delivered and the record has to say so")
			assert.NotNil(t, final.DeliveredAt, "the delivery moment is always recorded")
			assert.Equal(t, ful.TrackingNumber, final.TrackingNumber,
				"the tracking number must survive every order the messages arrive in")

			if sequence.shippedKnown {
				assert.NotNil(t, final.ShippedAt,
					"the collection was reported while the parcel was still pending, so its moment is known")
				return
			}
			assert.Nil(t, final.ShippedAt,
				"the collection message lost the race, so the dispatch moment was never measured. "+
					"Filling it in from the clock would date the dispatch AFTER its own delivery")
		})
	}
}

// TestALateCollectionMessageCarriesTheTrackingNumber pins the one thing the
// [models.ActionRecord] branch WRITES.
//
// On several carriers the collection message is the one that carries the
// waybill number. If it loses the race with the delivery message, dropping it
// on the floor would leave the shipment permanently without the number the
// shopper is given — which is what "tolerating" the reorder would amount to if
// the branch only avoided the error.
func TestALateCollectionMessageCarriesTheTrackingNumber(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.provider.createWithoutTracking = true
	ful := setup.createFulfillment(t, readyOption(t, setup), "key-late")
	require.Empty(t, ful.TrackingNumber, "this carrier issues the number with the collection event")

	delivered, err := setup.svc.MarkDelivered(context.Background(), ful.ID)
	require.NoError(t, err)
	require.Empty(t, delivered.TrackingNumber, "no message has carried a number yet")

	late, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-LATE", "https://carrier/TK-LATE")
	require.NoError(t, err)

	assert.Equal(t, models.StatusDelivered, late.Status, "the status must not move backwards")
	assert.Equal(t, "TK-LATE", late.TrackingNumber)
	assert.Equal(t, "https://carrier/TK-LATE", late.TrackingURL)
	assert.Nil(t, late.ShippedAt, "the moment is still unknown; only the number arrived")
}

// TestALateCollectionMessageStillRefusesAContradiction separates "arrived late"
// from "disagrees".
//
// Tolerating a reorder must not turn into accepting anything: a tracking number
// that differs from the stored one is not the same event arriving twice, it is
// a message about a different parcel, and it stays a conflict.
func TestALateCollectionMessageStillRefusesAContradiction(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := setup.createFulfillment(t, readyOption(t, setup), "key-contradiction")

	_, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err)
	_, err = setup.svc.MarkDelivered(context.Background(), ful.ID)
	require.NoError(t, err)

	_, err = setup.svc.MarkShipped(context.Background(), ful.ID, "TK-2", "")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
}

// TestARepeatedLateMessageDoesNotTouchTheRow proves the record branch is free
// when it has nothing to add.
//
// This path is a webhook's retry loop. Writing on every repeat would leave
// updated_at moving on a shipment nobody changed, and every listing ordered by
// it would show a parcel that has been sitting still for a week as the most
// recently touched record in the store.
func TestARepeatedLateMessageDoesNotTouchTheRow(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := setup.createFulfillment(t, readyOption(t, setup), "key-quiet")

	_, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err)
	_, err = setup.svc.MarkDelivered(context.Background(), ful.ID)
	require.NoError(t, err)

	before := setup.store.fulfillmentWriteCount()
	for range 5 {
		_, err = setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
		require.NoError(t, err)
	}
	assert.Equal(t, before, setup.store.fulfillmentWriteCount(),
		"a late message carrying nothing new must not write the row")
}

// TestTheParcelCameBackIsItsOwnStatus proves the "iade" terminal state.
//
// The three things asserted here are the three lies the status exists to
// prevent: it is not a cancellation (we asked for none), it is not a delivery
// (the recipient never got it), and it is not the shipment silently staying in
// transit forever.
func TestTheParcelCameBackIsItsOwnStatus(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := setup.createFulfillment(t, readyOption(t, setup), "key-iade")

	_, err := setup.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err)

	returned, err := setup.svc.MarkReturned(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusReturned, returned.Status)
	require.NotNil(t, returned.ReturnedAt, "the moment is written with the status")
	assert.Nil(t, returned.CanceledAt, "nobody canceled this parcel")
	assert.Nil(t, returned.DeliveredAt, "the recipient never received it")
	assert.NotNil(t, returned.ShippedAt, "it did set out, and that moment stands")

	// Idempotent: the carrier repeats this message like every other one.
	again, err := setup.svc.MarkReturned(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, returned.ReturnedAt, again.ReturnedAt, "a repeat must not re-stamp the moment")

	// Terminal in both directions that could undo it.
	_, err = setup.svc.MarkDelivered(context.Background(), ful.ID)
	assert.True(t, errors.IsConflict(err), "a returned parcel is not in the customer's hands: %v", err)
	err = setup.svc.CancelFulfillment(context.Background(), ful.ID)
	assert.True(t, errors.IsConflict(err), "there is nothing left to recall: %v", err)
}

// TestAParcelThatNeverSetOutCannotComeBack states the one refusal in the return
// table that could be mistaken for missing tolerance.
//
// It is NOT an ordering artifact: a return implies a collection, so "it came
// back" and "it was collected" cannot legitimately arrive in this order about
// the same parcel. Accepting it would let a single stray message mark a parcel
// still sitting in the warehouse as having traveled twice.
func TestAParcelThatNeverSetOutCannotComeBack(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := setup.createFulfillment(t, readyOption(t, setup), "key-never")

	_, err := setup.svc.MarkReturned(context.Background(), ful.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
}
