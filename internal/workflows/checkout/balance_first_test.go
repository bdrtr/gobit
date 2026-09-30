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

// The sessions of the customer's balances in these tests.
const (
	testCreditSessionID  = "scrses_1"
	testPointsSessionID  = "lpses_1"
	testPartialSessionTx = `{"partial":true}`
)

// balanceHarness is a happy path in which each tender holds what the map
// gives it and the provider holds the rest of the order.
func balanceHarness(t *testing.T, held map[string]int64) *harness {
	t.Helper()

	sessions := map[string]string{
		GiftCardProviderID:      testCardSessionID,
		StoreCreditProviderID:   testCreditSessionID,
		LoyaltyPointsProviderID: testPointsSessionID,
	}
	h := newHarness(t)
	h.payments.openSessionFn = func(_ context.Context, _, providerID, _ string, _ json.RawMessage) (string, error) {
		if id, ok := sessions[providerID]; ok {
			return id, nil
		}
		return testSessionID, nil
	}
	h.payments.authorizeFn = func(_ context.Context, sessionID string) (string, int64, error) {
		var first int64
		for provider, id := range sessions {
			if id == sessionID {
				return "authorized", held[provider], nil
			}
			first += held[provider]
		}
		return "authorized", testAmount - first, nil
	}
	h.payments.captureFn = func(_ context.Context, sessionID string, _ int64) (string, error) {
		return "pay_" + sessionID, nil
	}

	return h
}

// recordCancels makes the harness keep the sessions it cancels, in order.
func (h *harness) recordCancels() *[]string {
	var canceled []string
	h.payments.cancelFn = func(_ context.Context, sessionID string) error {
		canceled = append(canceled, sessionID)
		return nil
	}

	return &canceled
}

// recordCaptures makes the harness keep the sessions it captures, in order.
func (h *harness) recordCaptures() *[]string {
	var captured []string
	h.payments.captureFn = func(_ context.Context, sessionID string, _ int64) (string, error) {
		captured = append(captured, sessionID)
		return "pay_" + sessionID, nil
	}

	return &captured
}

// TestABalancePaysAfterTheCardAndTheProviderTheRest is ADR 0269: the card
// holds first, the customer's credit holds what it has of what is left, asked
// for as a partial hold, the provider is opened for the rest, and the captures
// run provider first, then in the order the tenders held.
func TestABalancePaysAfterTheCardAndTheProviderTheRest(t *testing.T) {
	h := balanceHarness(t, map[string]int64{GiftCardProviderID: 1_000, StoreCreditProviderID: 500})
	captured := h.recordCaptures()
	in := h.splitInput()
	in.PayFirstWith = []string{StoreCreditProviderID}

	result, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, [][2]string{
		{testProviderID, testCustomerID}, {GiftCardProviderID, testCustomerID}, {StoreCreditProviderID, testCustomerID},
	}, h.payments.checkedTenders, "a balance is refused before the order when it cannot pay")
	require.Len(t, h.payments.sessionData, 3)
	assert.JSONEq(t, `{"code":"`+testCardCode+`"}`, h.payments.sessionData[0], "the card first")
	assert.JSONEq(t, testPartialSessionTx, h.payments.sessionData[1], "the balance second, asked to hold part")
	assert.Equal(t, []string{testSessionID, testCardSessionID, testCreditSessionID}, *captured)
	assert.Equal(t, []int64{1_500, 1_000, 500}, h.payments.captureAmounts, "each takes what it held")
	assert.Equal(t, "pay_"+testSessionID, result.PaymentID, "the provider's capture is the order's payment")
	assert.Empty(t, h.orders.canceled)
}

// TestTwoBalancesThatCoverTheOrderAreItsOnlyPayment: the provider is named and
// not asked, and the balances are captured in the order the customer named
// them.
func TestTwoBalancesThatCoverTheOrderAreItsOnlyPayment(t *testing.T) {
	h := balanceHarness(t, map[string]int64{LoyaltyPointsProviderID: 1_000, StoreCreditProviderID: testAmount - 1_000})
	captured := h.recordCaptures()
	in := h.input()
	in.PayFirstWith = []string{LoyaltyPointsProviderID, StoreCreditProviderID}

	result, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, 2, h.rec.count("payment:session"), "no session at the provider")
	assert.Equal(t, []string{testPointsSessionID, testCreditSessionID}, *captured)
	assert.Equal(t, []int64{1_000, testAmount - 1_000}, h.payments.captureAmounts)
	assert.Equal(t, "pay_"+testPointsSessionID, result.PaymentID, "the first capture is the order's payment")
}

// TestABalanceThatCoversTheOrderLeavesTheNextOneUnasked: a balance named
// after the one that paid everything is not opened.
func TestABalanceThatCoversTheOrderLeavesTheNextOneUnasked(t *testing.T) {
	h := balanceHarness(t, map[string]int64{StoreCreditProviderID: testAmount})
	in := h.input()
	in.PayFirstWith = []string{StoreCreditProviderID, LoyaltyPointsProviderID}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, 1, h.rec.count("payment:session"), "neither the points nor the provider are opened")
	assert.Equal(t, []int64{testAmount}, h.payments.captureAmounts)
}

// TestAnEmptyBalanceStopsAndReleasesWhatWasHeld: the customer named the
// balance, so the payment stops rather than passing it over, and the card's
// hold goes back with the declined session's.
func TestAnEmptyBalanceStopsAndReleasesWhatWasHeld(t *testing.T) {
	h := balanceHarness(t, map[string]int64{GiftCardProviderID: 1_000})
	h.payments.authorizeFn = func(_ context.Context, sessionID string) (string, int64, error) {
		if sessionID == testCreditSessionID {
			return "", 0, errors.Conflict("payment_authorization_declined", "declined")
		}
		return "authorized", 1_000, nil
	}
	canceled := h.recordCancels()
	in := h.splitInput()
	in.PayFirstWith = []string{StoreCreditProviderID}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)

	assert.True(t, hasCode(err, "payment_authorization_declined"), "error: %v", err)
	assert.Equal(t, 2, h.rec.count("payment:session"), "the provider's session is never opened")
	assert.Equal(t, []string{testCreditSessionID, testCardSessionID}, *canceled, "released backwards")
	assert.Equal(t, 0, h.rec.count("payment:capture"))
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
}

// TestABalanceThatCannotBeOpenedReleasesTheOneBefore: nothing held stays held
// when the second balance's session is never opened.
func TestABalanceThatCannotBeOpenedReleasesTheOneBefore(t *testing.T) {
	h := balanceHarness(t, map[string]int64{StoreCreditProviderID: 500})
	h.payments.openSessionFn = func(_ context.Context, _, providerID, _ string, _ json.RawMessage) (string, error) {
		if providerID == LoyaltyPointsProviderID {
			return "", errors.Unavailable("payment_down", "the module did not answer")
		}
		return testCreditSessionID, nil
	}
	canceled := h.recordCancels()
	in := h.input()
	in.PayFirstWith = []string{StoreCreditProviderID, LoyaltyPointsProviderID}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)

	assert.Equal(t, []string{testCreditSessionID}, *canceled)
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
}

// TestTheProviderFallingShortReleasesEveryHold: the provider first, then the
// balances and the card from the last to hold to the first.
func TestTheProviderFallingShortReleasesEveryHold(t *testing.T) {
	h := balanceHarness(t, map[string]int64{GiftCardProviderID: 1_000, StoreCreditProviderID: 500})
	h.payments.authorizeFn = func(_ context.Context, sessionID string) (string, int64, error) {
		switch sessionID {
		case testCardSessionID:
			return "authorized", 1_000, nil
		case testCreditSessionID:
			return "authorized", 500, nil
		}
		return "authorized", 1_000, nil
	}
	canceled := h.recordCancels()
	in := h.splitInput()
	in.PayFirstWith = []string{StoreCreditProviderID}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)

	assert.True(t, hasCode(err, CodePaymentUnderauthorized), "error: %v", err)
	assert.Equal(t, []string{testSessionID, testCreditSessionID, testCardSessionID}, *canceled)
}

// TestAProviderCaptureWithNoMovementRollsBackEveryHold: the compensation built
// from the step's shared state releases the provider and every balance.
func TestAProviderCaptureWithNoMovementRollsBackEveryHold(t *testing.T) {
	h := balanceHarness(t, map[string]int64{StoreCreditProviderID: 500, LoyaltyPointsProviderID: 700})
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		return "", errors.Unavailable("provider_down", "the provider did not answer")
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "authorized", testAmount, testAmount, 0, 0, nil
	}
	canceled := h.recordCancels()
	in := h.input()
	in.PayFirstWith = []string{StoreCreditProviderID, LoyaltyPointsProviderID}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)

	assert.False(t, errors.Is(err, workflow.ErrUncompensated), "nothing was taken, everything is undone")
	assert.Equal(t, []int64{testAmount - 1_200}, h.payments.captureAmounts, "the provider's rest, and nothing after it")
	assert.Equal(t, []string{testSessionID, testPointsSessionID, testCreditSessionID}, *canceled)
}

// TestABalanceCaptureFailingAfterAnotherStopsForAPerson: once one balance's
// money is taken, a later capture failing rolls nothing back.
func TestABalanceCaptureFailingAfterAnotherStopsForAPerson(t *testing.T) {
	h := balanceHarness(t, map[string]int64{StoreCreditProviderID: 1_000, LoyaltyPointsProviderID: testAmount - 1_000})
	h.payments.captureFn = func(_ context.Context, sessionID string, _ int64) (string, error) {
		if sessionID == testPointsSessionID {
			return "", errors.Internal("ledger_down", "the points ledger did not answer")
		}
		return "pay_" + sessionID, nil
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "partially_captured", testAmount, testAmount, 1_000, 0, nil
	}
	in := h.input()
	in.PayFirstWith = []string{StoreCreditProviderID, LoyaltyPointsProviderID}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)

	assert.True(t, errors.Is(err, workflow.ErrUncompensated), "a paid order waits for a person")
	assert.Empty(t, h.orders.canceled)
	assert.Equal(t, 0, h.rec.count("payment:cancel"))
}

// TestAGuestCannotPayFirstWithABalance: the balance's own refusal arrives
// before the order is opened (ADR 0175).
func TestAGuestCannotPayFirstWithABalance(t *testing.T) {
	h := balanceHarness(t, nil)
	h.payments.checkTenderFn = func(_ context.Context, providerID, _ string) error {
		if providerID == StoreCreditProviderID {
			return errors.Conflict("payment_store_credit_no_customer", "a guest")
		}
		return nil
	}
	in := h.input()
	in.PayFirstWith = []string{StoreCreditProviderID}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)

	assert.True(t, hasCode(err, "payment_store_credit_no_customer"), "error: %v", err)
	assert.Equal(t, 0, h.rec.count("order:place"), "no order is opened")
}

// TestPayFirstWithNamesTheCustomersBalancesOnce refuses what the saga could
// not pay with: another provider, a balance named twice, and the balance that
// is also asked to pay the rest.
func TestPayFirstWithNamesTheCustomersBalancesOnce(t *testing.T) {
	for name, balances := range map[string][]string{
		"another provider": {"manual"},
		"the gift card":    {GiftCardProviderID},
		"named twice":      {StoreCreditProviderID, StoreCreditProviderID},
		"the rest too":     {LoyaltyPointsProviderID},
	} {
		t.Run(name, func(t *testing.T) {
			h := balanceHarness(t, nil)
			in := h.input()
			in.PayFirstWith = balances
			if name == "the rest too" {
				in.PaymentProviderID = LoyaltyPointsProviderID
			}

			_, err := h.wf.CompleteCart(context.Background(), in)
			require.Error(t, err)

			assert.True(t, errors.IsInvalid(err), "error: %v", err)
			assert.Equal(t, 0, h.rec.count("payment:collection"))
		})
	}
}

// TestThePayFirstBalancesAreRecorded: they name tenders, not credentials, and
// a recovered saga has to pay with the same ones.
func TestThePayFirstBalancesAreRecorded(t *testing.T) {
	recorded, err := json.Marshal(checkoutPlan{CartID: testCartID, PayFirstWith: []string{StoreCreditProviderID}})
	require.NoError(t, err)

	assert.Contains(t, string(recorded), `"pay_first_with":["store_credit"]`)
}
