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

// laterHarness is a happy path whose provider's money comes later (ADR 0284):
// its session is authorized for the whole order and the collection reads
// what the tenders that paid first captured.
func laterHarness(t *testing.T, captured int64) *harness {
	t.Helper()

	h := newHarness(t)
	h.payments.later = map[string]bool{testProviderID: true}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "authorized", testAmount, testAmount - captured, captured, 0, nil
	}

	return h
}

// TestAnOfflineMethodPlacesTheOrderOwingItsTotal is ADR 0284: the provider's
// session is authorized and not captured, and the order is placed, its cart
// completed and its stock confirmed, with nothing paid on it.
func TestAnOfflineMethodPlacesTheOrderOwingItsTotal(t *testing.T) {
	h := laterHarness(t, 0)

	result, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, 1, h.rec.count("payment:authorize"), "the promise is authorized")
	assert.Zero(t, h.rec.count("payment:capture"), "nothing is captured at the checkout")
	assert.Empty(t, h.payments.captureAmounts)
	assert.Empty(t, h.orders.canceled)
	assert.Empty(t, result.PaymentID, "no capture, no payment")
	assert.Equal(t, testSessionID, result.PaymentSessionID, "the session the operator captures later")
	assert.Equal(t, testAmount, result.Outstanding, "the order owes its total")
	assert.True(t, result.CartCompleted)
	assert.True(t, result.ReservationsConfirmed)
	require.Len(t, h.orders.summaries, 1)
	assert.Equal(t, reportedTotals{orderID: testOrderID}, h.orders.summaries[0], "the order is placed owing its total")
}

// TestACardPaysNowAndTheOfflineMethodLater: the gift card's hold is captured at
// the checkout and the provider's part is left to the operator.
func TestACardPaysNowAndTheOfflineMethodLater(t *testing.T) {
	h := splitHarness(t, 1_000)
	h.payments.later = map[string]bool{testProviderID: true}
	var captured []string
	h.payments.captureFn = func(_ context.Context, sessionID string, _ int64) (string, error) {
		captured = append(captured, sessionID)
		return "pay_" + sessionID, nil
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "partially_captured", testAmount, testAmount - 1_000, 1_000, 0, nil
	}

	result, err := h.wf.CompleteCart(context.Background(), h.splitInput())
	require.NoError(t, err)

	assert.Equal(t, []string{testCardSessionID}, captured, "only the card is captured")
	assert.Equal(t, []int64{1_000}, h.payments.captureAmounts)
	assert.Equal(t, "pay_"+testCardSessionID, result.PaymentID)
	assert.Equal(t, testAmount-1_000, result.Outstanding, "the order owes what the card did not pay")
	require.Len(t, h.orders.summaries, 1)
	assert.Equal(t, int64(1_000), h.orders.summaries[0].paidTotal)
}

// TestACardThatCapturedTooLittleBesideAnOfflineMethodIsDangling: the
// verification asks for what the first tenders held, and money moved, so a
// shortfall stops the saga for a person rather than rolling it back.
func TestACardThatCapturedTooLittleBesideAnOfflineMethodIsDangling(t *testing.T) {
	h := splitHarness(t, 1_000)
	h.payments.later = map[string]bool{testProviderID: true}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "partially_captured", testAmount, testAmount - 400, 400, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.splitInput())
	require.Error(t, err)

	assert.True(t, hasCode(err, CodePaymentUndercaptured), "error: %v", err)
	assert.ErrorIs(t, err, workflow.ErrUncompensated)
	assert.Empty(t, h.orders.canceled, "money moved, so the order stands")
}

// TestAnOfflineOrderWhoseCollectionCannotBeReadRollsBack: nothing was captured,
// so nothing can have moved, and the saga rolls back as it does before the
// pivot rather than stopping for a person.
func TestAnOfflineOrderWhoseCollectionCannotBeReadRollsBack(t *testing.T) {
	h := laterHarness(t, 0)
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "", 0, 0, 0, 0, errors.Internal("payment_query_failed", "the collection could not be read")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.NotErrorIs(t, err, workflow.ErrUncompensated)
	assert.Equal(t, []string{testOrderID}, h.orders.canceled, "the order is canceled")
	assert.Equal(t, 1, h.rec.count("payment:cancel"), "the promise is withdrawn")
	assert.Zero(t, h.rec.count("payment:capture"))
}

// TestTheQuestionIsAskedBeforeTheOrder: a provider the payment module cannot
// answer about opens no order.
func TestTheQuestionIsAskedBeforeTheOrder(t *testing.T) {
	h := newHarness(t)
	h.payments.laterErr = errors.NotFound("payment_provider_not_found", "not registered")

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Zero(t, h.rec.count("order:place"))
	assert.Zero(t, h.rec.count("payment:collection"))
}

// TestTheAnswerIsRecordedForRecovery: a saga resumed from its record captures
// what it would have captured when it began, whatever the provider is
// registered as by then.
func TestTheAnswerIsRecordedForRecovery(t *testing.T) {
	h := laterHarness(t, 0)

	plan, err := h.wf.prepare(context.Background(), h.input())
	require.NoError(t, err)
	recorded, err := json.Marshal(plan)
	require.NoError(t, err)
	assert.Contains(t, string(recorded), `"captures_later":true`)

	h.payments.later = nil
	recovered, err := h.wf.RecoveryWorkflow(recorded)
	require.NoError(t, err)
	var step *capturePaymentStep
	for _, s := range recovered.Steps {
		if capture, ok := s.(*capturePaymentStep); ok {
			step = capture
		}
	}
	require.NotNil(t, step)
	assert.True(t, step.plan.CapturesLater)
}

// TestAnOfflineOnlyCompletionRefusesAProviderItWouldCapture is ADR 0286: the
// operator's completion names a provider the checkout would capture, and it is
// refused before an order is opened — the operator holds no shopper's card.
func TestAnOfflineOnlyCompletionRefusesAProviderItWouldCapture(t *testing.T) {
	h := newHarness(t)
	in := h.input()
	in.OfflineOnly = true

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)

	assert.True(t, hasCode(err, CodeOfflineMethodRequired), "error: %v", err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid))
	assert.Zero(t, h.rec.count("order:place"))
	assert.Zero(t, h.rec.count("payment:collection"))
}

// TestAnOfflineOnlyCompletionTakesAnOfflineMethod: the same completion with an
// offline method places the order owing its total.
func TestAnOfflineOnlyCompletionTakesAnOfflineMethod(t *testing.T) {
	h := laterHarness(t, 0)
	in := h.input()
	in.OfflineOnly = true

	result, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, testAmount, result.Outstanding)
}

// TestTheInteropCarriesOfflineOnly: the flag the cart module sends reaches the
// flow through the JSON boundary, which the compiler does not check.
func TestTheInteropCarriesOfflineOnly(t *testing.T) {
	h := newHarness(t)

	_, err := NewInterop(h.wf).CompleteCartJSON(context.Background(), json.RawMessage(
		`{"cart_id":"`+testCartID+`","payment_provider_id":"`+testProviderID+`","offline_only":true}`))
	require.Error(t, err)
	assert.True(t, hasCode(err, CodeOfflineMethodRequired), "error: %v", err)
	assert.Zero(t, h.rec.count("order:place"))
}
