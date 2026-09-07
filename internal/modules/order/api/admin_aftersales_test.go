package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// settleClaimPath is the claim's settlement route. The order id and the claim
// id in it are DIFFERENT strings on purpose: the handler has to pick the claim
// id out of the path, and a test that used the same value for both would pass
// just as happily if it picked the order id.
const settleClaimPath = "/admin/v1/orders/order_1/claims/clm_7/settle"

// TestSettlingAClaimSendsTheCLAIMsIdAndTheMoneyThroughTheFlow is the endpoint
// that turns a recorded complaint into money leaving the shop.
//
// It goes through the FLOW and not the service for the same reason refunding a
// return does: the money reaches the payment module and the record reaches this
// one, and an endpoint bound to either alone would do half of it — either a
// claim stamped "completed" with nothing sent, or money sent against a claim
// still sitting on the operator's list as owed.
//
// The identifier matters as much as the amount. A claim lives under an order in
// the URL, and the two ids are adjacent in the path; a handler reaching for the
// wrong one would settle against an order id, which the flow cannot resolve to
// a claim — or worse, could resolve to a different record entirely if the two
// namespaces ever overlapped.
//
// summary_recorded and warnings are asserted here rather than in a test of
// their own because the settle handler MAPS the flow's three results onto one
// response, and mapping "the money left" onto "the order was told" is a single
// wrong field away: an operator reading true there stops looking for the
// discrepancy that is actually sitting on the order.
func TestSettlingAClaimSendsTheCLAIMsIdAndTheMoneyThroughTheFlow(t *testing.T) {
	flow := &fakeReceiving{
		refunded:       1200,
		recorded:       false,
		refundWarnings: []string{"the order was not told about the settlement"},
	}
	svc := &fakeOrders{}
	r := newRouterWithFlow(svc, flow)

	rec := doRequest(t, r, http.MethodPost, settleClaimPath,
		`{"amount":1200,"reason":"two units arrived broken"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, flow.settleCalls)
	assert.Equal(t, "clm_7", flow.gotClaimID,
		"the flow has to be given the CLAIM id from the path, not the order id it hangs off")
	assert.Equal(t, int64(1200), flow.gotAmount,
		"the amount the operator typed is what leaves the shop; a dropped amount means "+
			"zero, which the flow reads as \"refund everything left on the collection\"")
	assert.Equal(t, "two units arrived broken", flow.gotReason)
	assert.Empty(t, svc.calls,
		"the settlement does not touch the service directly; going around the flow would "+
			"stamp the claim without sending anything")

	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.InDelta(t, 1200, data["refunded_amount"], 0.0)
	assert.Equal(t, false, data["summary_recorded"],
		"money that left without the order being told has to say so; false here is what "+
			"sends somebody to look at the order's summary")
	assert.NotEmpty(t, data["warnings"],
		"the warning belongs in the response, not only in a log nobody is watching")
}

// TestAClaimIsNotSettledWhenTheFlowIsNotBound pins the fail-closed branch.
//
// The receiving flow is an optional argument of [api.New]. Without it the only
// thing this handler could still do is reach the service and stamp the claim —
// which would announce a settlement to the customer, close the record, and send
// no money at all. Refusing outright leaves the claim exactly where it was, and
// that is the only safe outcome.
func TestAClaimIsNotSettledWhenTheFlowIsNotBound(t *testing.T) {
	svc := &fakeOrders{}
	r := newRouterWithFlow(svc, nil)

	rec := doRequest(t, r, http.MethodPost, settleClaimPath, `{"amount":1200}`)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Empty(t, svc.calls, "nothing may be stamped when no money can follow")

	failure, ok := decodeResponse(t, rec)["error"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "order_workflow_unavailable", failure["code"])
}

// TestAClaimTheFlowRefusesIsNotReportedAsSettled carries the refusal out
// unchanged.
//
// The flow refuses a claim that is not "requested" and one whose type is
// "replacement" — shipping goods against an existing order is not something
// this framework can do. Both are CONFLICTS, not server faults, and the client
// has to see the flow's own code: an operator told "internal error" retries,
// and an operator told the claim wants a replacement goes and does the thing
// the software cannot. A 200 here would be the worst of the three — a
// settlement recorded in the operator's mind that never happened anywhere else.
func TestAClaimTheFlowRefusesIsNotReportedAsSettled(t *testing.T) {
	flow := &fakeReceiving{refundErr: errors.Conflict(
		"returns_claim_not_refundable", "a replacement claim cannot be settled with money")}
	r := newRouterWithFlow(&fakeOrders{}, flow)

	rec := doRequest(t, r, http.MethodPost, settleClaimPath, `{"amount":1200}`)

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	payload := decodeResponse(t, rec)
	assert.NotContains(t, payload, "data",
		"a refusal must not carry a data envelope; a client reading refunded_amount off "+
			"it would take a zero for a settlement of zero")

	failure, ok := payload["error"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "returns_claim_not_refundable", failure["code"],
		"the flow's own code has to survive the hop, otherwise the operator cannot tell "+
			"a refusable claim from a broken server")
}

// TestAnAfterSalesListCountsEVERYRecordAndEchoesThePageItReallyApplied is what
// a paging client needs and cannot compute.
//
// The count is the total the FILTER matches, not the number of rows on the
// page. A client handed len(data) instead would stop after the first page —
// with three exchanges on a limit of one, the operator sees one and concludes
// the customer swapped a single item. And the limit and offset in the envelope
// are the ones REALLY applied: when the client sent none, the server picked a
// default, and an envelope that echoed the client's silence back would make the
// second page start in the wrong place.
//
// The two record types are checked together because they are the same handler
// written twice, and the way that shape fails is one of the two being edited.
func TestAnAfterSalesListCountsEVERYRecordAndEchoesThePageItReallyApplied(t *testing.T) {
	for name, tc := range map[string]struct {
		path  string
		call  string
		field string
		value any
	}{
		"exchanges": {
			path: "/admin/v1/orders/order_1/exchanges", call: "ListExchanges",
			field: "difference_due", value: float64(-500),
		},
		"claims": {
			path: "/admin/v1/orders/order_1/claims", call: "ListClaims",
			field: "type", value: "refund",
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeOrders{
				count: 7,
				exchanges: []models.Exchange{{
					ID: "exch_1", OrderID: "order_1",
					Status: models.ExchangeRequested, DifferenceDue: -500,
				}},
				claims: []models.Claim{{
					ID: "claim_1", OrderID: "order_1",
					Type: models.ClaimRefund, Status: models.ClaimRequested,
				}},
			}
			r := newRouter(svc)

			rec := doRequest(t, r, http.MethodGet, tc.path+"?limit=1&offset=4", "")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, []string{tc.call}, svc.calls)
			assert.Equal(t, "order_1", svc.gotOrderID,
				"the records are read for the order in the path, not for all orders")
			assert.Equal(t, int64(1), svc.page.Limit)
			assert.Equal(t, int64(4), svc.page.Offset)

			body := decodeResponse(t, rec)
			assert.Equal(t, float64(7), body["count"],
				"count is how many records the order HAS; a page length here hides the rest")
			assert.Equal(t, float64(1), body["limit"])
			assert.Equal(t, float64(4), body["offset"])

			data, ok := body["data"].([]any)
			require.True(t, ok, rec.Body.String())
			require.Len(t, data, 1)

			record, ok := data[0].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tc.value, record[tc.field],
				"every row on the page goes through the DTO; a list that answered with "+
					"the models would publish columns the API never promised")
		})
	}
}

// TestAnAfterSalesListWithNoRecordsIsAnEmptyArrayAndNotNull is one character in
// the handler and a crash in the client.
//
// Go encodes a nil slice as null. A storefront or admin UI that iterates
// data — which is the only thing anyone does with a list — throws on null,
// and the screen that breaks is the one an operator opens for an order that
// simply has no exchanges: the ordinary case, not the exceptional one.
func TestAnAfterSalesListWithNoRecordsIsAnEmptyArrayAndNotNull(t *testing.T) {
	for name, path := range map[string]string{
		"exchanges": "/admin/v1/orders/order_1/exchanges",
		"claims":    "/admin/v1/orders/order_1/claims",
	} {
		t.Run(name, func(t *testing.T) {
			r := newRouter(&fakeOrders{})

			rec := doRequest(t, r, http.MethodGet, path, "")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			body := decodeResponse(t, rec)
			data, ok := body["data"].([]any)
			require.True(t, ok,
				"an order with no records still answers with a list: %s", rec.Body.String())
			assert.Empty(t, data)
			assert.Equal(t, float64(0), body["count"])
		})
	}
}

// TestAnAfterSalesListRefusesAMalformedPageBeforeReachingTheService keeps a
// typo from becoming a full table scan.
//
// "limit=all" is not a number, and the two answers a handler can give are to
// refuse it or to fall back to a default. Falling back would silently serve a
// different page than the one asked for, and the client would page through the
// list convinced it was in control. Refusing before the service is reached also
// means a malformed parameter costs no query at all.
func TestAnAfterSalesListRefusesAMalformedPageBeforeReachingTheService(t *testing.T) {
	for name, path := range map[string]string{
		"exchanges": "/admin/v1/orders/order_1/exchanges?limit=all",
		"claims":    "/admin/v1/orders/order_1/claims?offset=first",
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeOrders{}
			r := newRouter(svc)

			rec := doRequest(t, r, http.MethodGet, path, "")

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Empty(t, svc.calls,
				"on an invalid parameter the service must not be reached")
		})
	}
}
