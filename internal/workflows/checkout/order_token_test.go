package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// The completion's order token (ADR 0439): asked for by the storefront's
// completion, issued after the saga for the placed order, and never in the
// execution record.

// TestACompletionThatAsksIssuesTheOrdersToken is the storefront's half: the
// token comes back for the order the saga placed, once the saga is done.
func TestACompletionThatAsksIssuesTheOrdersToken(t *testing.T) {
	h := newHarness(t)
	in := h.input()
	in.IssueOrderToken = true

	out, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, "ordtok_"+testOrderID, out.OrderToken)
	assert.Equal(t, []string{testOrderID}, h.orders.tokens)
	calls := h.rec.snapshot()
	assert.Equal(t, "order:token", calls[len(calls)-1], "the token is issued after the saga: %v", calls)
}

// TestACompletionThatDoesNotAskIssuesNoToken is the operator's half: no
// credential is made for a completion that hands it to nobody.
func TestACompletionThatDoesNotAskIssuesNoToken(t *testing.T) {
	h := newHarness(t)

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Empty(t, out.OrderToken)
	assert.Empty(t, h.orders.tokens)
	assert.NotContains(t, h.rec.snapshot(), "order:token")
}

// TestACompletionWhoseTokenFailsStillSucceeds keeps a placed and paid order
// a success: the shopper is not sent to retry a checkout that worked, and the
// order mail carries a token of its own.
func TestACompletionWhoseTokenFailsStillSucceeds(t *testing.T) {
	h := newHarness(t)
	h.orders.tokenErr = errors.Unavailable("order_down", "the order module is down")
	in := h.input()
	in.IssueOrderToken = true

	out, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, testOrderID, out.OrderID)
	assert.Empty(t, out.OrderToken)
	assert.Empty(t, out.Warnings, "the token is the shopper's, and a warning is the operator's")
}

// TestTheOrderTokenIsNotInTheExecutionRecord holds the field out of the JSON
// the engine stores the saga's output as: the order keeps a digest, and a
// plaintext there would sit in the workflow tables.
func TestTheOrderTokenIsNotInTheExecutionRecord(t *testing.T) {
	recorded, err := json.Marshal(CompleteCartResult{OrderID: testOrderID, OrderToken: "ordtok_secret"})
	require.NoError(t, err)

	assert.NotContains(t, string(recorded), "ordtok_secret")
}

// TestTheInteropAnswersTheTokenOnlyWhenAsked is the surface the cart module
// reads: issue_order_token in, order_token out.
func TestTheInteropAnswersTheTokenOnlyWhenAsked(t *testing.T) {
	for _, ask := range []bool{true, false} {
		h := newHarness(t)
		// The interop takes no location, so the reservation asks where the
		// stock is, and the test location holds it.
		h.inventory.locationsFn = func(context.Context, string, int64) ([]string, error) {
			return []string{testLocationID}, nil
		}
		h.fulfillment.rankFn = rankByGreatestID
		request, err := json.Marshal(map[string]any{
			"cart_id": testCartID, "payment_provider_id": h.input().PaymentProviderID,
			"expected_total": testAmount, "issue_order_token": ask,
		})
		require.NoError(t, err)

		raw, err := NewInterop(h.wf).CompleteCartJSON(context.Background(), request)
		require.NoError(t, err)
		var response map[string]any
		require.NoError(t, json.Unmarshal(raw, &response))

		if ask {
			assert.Equal(t, "ordtok_"+testOrderID, response["order_token"])
		} else {
			assert.NotContains(t, response, "order_token")
		}
	}
}
