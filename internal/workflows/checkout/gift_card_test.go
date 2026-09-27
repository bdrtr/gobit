package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// testCardSessionID is the gift card's session in these tests.
const testCardSessionID = "gcses_1"

// testCardCode is a gift card code as a customer types it.
const testCardCode = "ABCD-EFGH-JKMN-PQRS"

// splitHarness is a happy path in which a gift card holds cardHeld of the
// order and the provider holds the rest.
func splitHarness(t *testing.T, cardHeld int64) *harness {
	t.Helper()

	h := newHarness(t)
	h.payments.openSessionFn = func(_ context.Context, _, providerID, _ string, _ json.RawMessage) (string, error) {
		if providerID == GiftCardProviderID {
			return testCardSessionID, nil
		}
		return testSessionID, nil
	}
	h.payments.authorizeFn = func(_ context.Context, sessionID string) (string, int64, error) {
		if sessionID == testCardSessionID {
			return "authorized", cardHeld, nil
		}
		return "authorized", testAmount - cardHeld, nil
	}

	return h
}

// splitInput is the happy path's input with a gift card.
func (h *harness) splitInput() CompleteCartInput {
	in := h.input()
	in.GiftCardCode = testCardCode

	return in
}

// TestAGiftCardPaysFirstAndTheProviderTheRest is ADR 0209: the card holds what
// it has, the provider's session is opened for the rest, and the provider is
// captured before the card.
func TestAGiftCardPaysFirstAndTheProviderTheRest(t *testing.T) {
	h := splitHarness(t, 1_000)
	var captured []string
	h.payments.captureFn = func(_ context.Context, sessionID string, _ int64) (string, error) {
		captured = append(captured, sessionID)
		return "pay_" + sessionID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.splitInput())
	require.NoError(t, err)

	assert.Equal(t, [][2]string{{testProviderID, testCustomerID}, {GiftCardProviderID, testCustomerID}},
		h.payments.checkedTenders, "the card is refused before the order when it cannot pay")
	require.Len(t, h.payments.sessionData, 2)
	assert.JSONEq(t, `{"code":"`+testCardCode+`"}`, h.payments.sessionData[0], "the card's session is opened first")
	assert.Equal(t, []string{testSessionID, testCardSessionID}, captured, "the uncertain capture first")
	assert.Equal(t, []int64{2_000, 1_000}, h.payments.captureAmounts, "each takes what it held")
	assert.Empty(t, h.orders.canceled)
}

// TestACardThatCoversTheOrderIsTheOnlyPayment: the provider is named and not
// asked.
func TestACardThatCoversTheOrderIsTheOnlyPayment(t *testing.T) {
	h := splitHarness(t, testAmount)

	result, err := h.wf.CompleteCart(context.Background(), h.splitInput())
	require.NoError(t, err)

	assert.Equal(t, 1, h.rec.count("payment:session"), "no session at the provider")
	assert.Equal(t, []int64{testAmount}, h.payments.captureAmounts)
	assert.Equal(t, testPaymentID, result.PaymentID, "the card's capture is the order's payment")
}

// TestTheProviderFallingShortReleasesTheCardToo: nothing stays held when the
// two together do not cover the order.
func TestTheProviderFallingShortReleasesTheCardToo(t *testing.T) {
	h := splitHarness(t, 1_000)
	h.payments.authorizeFn = func(_ context.Context, sessionID string) (string, int64, error) {
		if sessionID == testCardSessionID {
			return "authorized", 1_000, nil
		}
		return "authorized", 1_500, nil
	}
	var canceled []string
	h.payments.cancelFn = func(_ context.Context, sessionID string) error {
		canceled = append(canceled, sessionID)
		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.splitInput())
	require.Error(t, err)

	assert.True(t, hasCode(err, CodePaymentUnderauthorized), "error: %v", err)
	assert.Equal(t, []string{testSessionID, testCardSessionID}, canceled, "both holds are released")
	assert.Equal(t, 0, h.rec.count("payment:capture"))
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
}

// TestACardThatHoldsNothingStopsBeforeTheProvider: the customer named a card,
// and paying everything elsewhere would be another payment than the one chosen.
func TestACardThatHoldsNothingStopsBeforeTheProvider(t *testing.T) {
	h := splitHarness(t, 0)
	h.payments.authorizeFn = func(_ context.Context, sessionID string) (string, int64, error) {
		if sessionID == testCardSessionID {
			return "", 0, errors.Conflict("payment_authorization_declined", "declined")
		}
		return "authorized", testAmount, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.splitInput())
	require.Error(t, err)

	assert.Equal(t, 1, h.rec.count("payment:session"), "the provider's session is never opened")
	assert.Equal(t, 1, h.rec.count("payment:cancel"), "the card's session is canceled")
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
}

// TestAProviderCaptureWithNoMovementRollsBackBoth: the provider's capture
// failed and the collection proves nothing was taken, so the card's hold goes
// back with everything else.
func TestAProviderCaptureWithNoMovementRollsBackBoth(t *testing.T) {
	h := splitHarness(t, 1_000)
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		return "", errors.Unavailable("provider_down", "the provider did not answer")
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "authorized", testAmount, testAmount, 0, 0, nil
	}
	var canceled []string
	h.payments.cancelFn = func(_ context.Context, sessionID string) error {
		canceled = append(canceled, sessionID)
		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.splitInput())
	require.Error(t, err)

	assert.False(t, errors.Is(err, workflow.ErrUncompensated), "nothing was taken, everything is undone")
	assert.Equal(t, 1, h.rec.count("payment:capture"), "the card is not captured after the provider failed")
	assert.Equal(t, []string{testSessionID, testCardSessionID}, canceled)
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
}

// TestACardCaptureFailingAfterTheProviderStopsForAPerson: the provider's money
// is taken, so nothing is rolled back.
func TestACardCaptureFailingAfterTheProviderStopsForAPerson(t *testing.T) {
	h := splitHarness(t, 1_000)
	h.payments.captureFn = func(_ context.Context, sessionID string, _ int64) (string, error) {
		if sessionID == testCardSessionID {
			return "", errors.Internal("ledger_down", "the card's ledger did not answer")
		}
		return testPaymentID, nil
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "partially_captured", testAmount, 1_000, 2_000, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.splitInput())
	require.Error(t, err)

	assert.True(t, errors.Is(err, workflow.ErrUncompensated), "a paid order waits for a person")
	assert.Empty(t, h.orders.canceled)
	assert.Equal(t, 0, h.rec.count("payment:cancel"))
}

// TestAGiftCardCannotPayTheRestItself: the rest has to be another provider's.
func TestAGiftCardCannotPayTheRestItself(t *testing.T) {
	h := splitHarness(t, 1_000)
	in := h.splitInput()
	in.PaymentProviderID = GiftCardProviderID

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)

	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, 0, h.rec.count("payment:collection"))
}

// TestTheGiftCardCodeIsNotRecorded: the plan the engine keeps carries no code.
func TestTheGiftCardCodeIsNotRecorded(t *testing.T) {
	plan := checkoutPlan{CartID: testCartID, GiftCardCode: testCardCode, PaymentData: json.RawMessage(`{"token":"x"}`)}

	recorded, err := json.Marshal(plan)
	require.NoError(t, err)

	assert.NotContains(t, string(recorded), testCardCode)
	assert.NotContains(t, string(recorded), "token")
}

// TestASplitPaymentIsRestoredFromTheRecord: the record of the two steps brings
// back the card's session, what it held and its capture, so a compensation
// built from the record cancels both holds and a stop names both captures.
func TestASplitPaymentIsRestoredFromTheRecord(t *testing.T) {
	authorize, err := json.Marshal(authorizeOutput{
		CollectionID: testCollectionID, SessionID: testSessionID, Status: "authorized", Authorized: testAmount,
		GiftCardSessionID: testCardSessionID, GiftCardAuthorized: 1_000,
	})
	require.NoError(t, err)
	capture, err := json.Marshal(captureOutput{PaymentID: testPaymentID, GiftCardPaymentID: "pay_card", Captured: testAmount})
	require.NoError(t, err)
	sc := &workflow.StepContext{Shared: map[string]any{}}

	require.NoError(t, (&authorizePaymentStep{}).Restore(sc, authorize))
	require.NoError(t, (&capturePaymentStep{}).Restore(sc, capture))

	assert.Equal(t, testSessionID, sc.Shared[sharedSessionID])
	assert.Equal(t, testCardSessionID, sc.Shared[sharedGiftCardSessionID])
	assert.Equal(t, int64(1_000), sc.Shared[sharedGiftCardAuthorized])
	assert.Equal(t, "pay_card", sc.Shared[sharedGiftCardPaymentID])

	cardOnly, err := json.Marshal(authorizeOutput{
		CollectionID: testCollectionID, GiftCardSessionID: testCardSessionID, GiftCardAuthorized: testAmount,
	})
	require.NoError(t, err)
	fresh := &workflow.StepContext{Shared: map[string]any{}}
	require.NoError(t, (&authorizePaymentStep{}).Restore(fresh, cardOnly), "a card that paid everything has no provider session")
	assert.NotContains(t, fresh.Shared, sharedSessionID)
}
