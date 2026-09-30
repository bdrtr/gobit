package adminui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// openPath is the address that opens a record of one kind on order_1.
func openPath(kind string) string { return OrdersPath + "/order_1/after-sales/" + kind }

// TestEachOpeningReachesItsOwnMethod is ADR 0272: each form hands the order
// and what it asked for to its own method, the lines being the ones given a
// quantity, and the page says what was opened.
func TestEachOpeningReachesItsOwnMethod(t *testing.T) {
	t.Parallel()

	lines := url.Values{"line_id": {"oli_ring", "oli_card", "oli_box"}, "quantity": {" 2 ", "", "0"}}
	for _, tc := range []struct {
		kind       string
		form       url.Values
		acted, id  string
		text       string
		amount     int64
		reason     string
		lines      []string
		quantities []int64
		done       string
	}{
		{
			kind: "return", form: merged(lines, url.Values{"amount": {"12.50"}, "currency": {"TRY"}, "reason": {"scratched"}}),
			acted: "open-return", id: "order_1", amount: 1_250, reason: "scratched",
			lines: []string{"oli_ring"}, quantities: []int64{2}, done: "The return ret_new was opened.",
		},
		{
			kind: "claim", form: url.Values{"type": {"replace"}, "currency": {"TRY"}, "reason": {"crushed"}},
			acted: "open-claim", id: "order_1", text: "replace", reason: "crushed", done: "The claim claim_new was opened.",
		},
		{
			kind: "exchange", form: url.Values{"amount": {"-25.00"}, "currency": {"TRY"}, "note": {"a size down"}},
			acted: "open-exchange", id: "order_1", amount: -2_500, reason: "a size down",
			done: "The exchange exch_new was opened.",
		},
		{
			kind: "replacement", form: merged(lines, url.Values{
				"source": {"exchange:exch_2"}, "shipping_option_id": {" so_std "}, "location_id": {"sloc_main"},
			}),
			acted: "open-replacement", id: "|exch_2", text: "so_std|sloc_main",
			lines: []string{"oli_ring"}, quantities: []int64{2}, done: "The replacement orepl_new was opened.",
		},
	} {
		surface := &fakeAfterSales{}
		rec := act(t, surface, afterSalesCatalog(nil), openPath(tc.kind), tc.form, "order:read", "order:write")

		require.Equalf(t, http.StatusOK, rec.Code, "%s: %s", tc.kind, rec.Body.String())
		assert.Equal(t, tc.acted, surface.acted, tc.kind)
		assert.Equal(t, tc.id, surface.id, tc.kind)
		assert.Equal(t, tc.text, surface.text, tc.kind)
		assert.Equal(t, tc.amount, surface.amount, tc.kind)
		assert.Equal(t, tc.reason, surface.reason, tc.kind)
		assert.Equal(t, tc.lines, surface.lines, tc.kind)
		assert.Equal(t, tc.quantities, surface.quantities, tc.kind)
		assert.Contains(t, rec.Body.String(), `<p role="status">`+tc.done+`</p>`, tc.kind)
	}
}

// merged puts two forms together.
func merged(a, b url.Values) url.Values {
	out := url.Values{}
	for _, form := range []url.Values{a, b} {
		for key, values := range form {
			out[key] = append(out[key], values...)
		}
	}

	return out
}

// TestAReplacementSettlesAClaimNamedAsItsSource reads the source's kind.
func TestAReplacementSettlesAClaimNamedAsItsSource(t *testing.T) {
	t.Parallel()

	surface := &fakeAfterSales{}
	act(t, surface, afterSalesCatalog(nil), openPath("replacement"),
		url.Values{"source": {"claim:claim_3"}, "line_id": {"oli_ring"}, "quantity": {"1"}}, "order:read", "order:write")

	assert.Equal(t, "claim_3|", surface.id)
}

// TestAnOpeningTheFormCannotSayIsRefusedOnThePage: a replacement without a
// source, and a quantity that is no whole number, are refused before the
// module is asked.
func TestAnOpeningTheFormCannotSayIsRefusedOnThePage(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		kind string
		form url.Values
		says string
	}{
		"no source":  {"replacement", url.Values{"line_id": {"oli_ring"}, "quantity": {"1"}}, "Choose the claim or the exchange"},
		"a fraction": {"return", url.Values{"line_id": {"oli_ring"}, "quantity": {"1.5"}}, "A quantity must be a whole number"},
		"unpaired":   {"return", url.Values{"line_id": {"oli_ring", "oli_card"}, "quantity": {"1"}}, "Every line needs its quantity box."},
		"negative":   {"return", url.Values{"line_id": {"oli_ring"}, "quantity": {"-1"}}, "A quantity must be a whole number"},
	} {
		surface := &fakeAfterSales{}
		rec := act(t, surface, afterSalesCatalog(nil), openPath(tc.kind), tc.form, "order:read", "order:write")

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, name)
		assert.Contains(t, rec.Body.String(), tc.says, name)
		assert.Empty(t, surface.acted, name)
	}

	rec := act(t, &fakeAfterSales{}, afterSalesCatalog(nil), openPath("order"), nil, "order:read", "order:write")
	assert.Equal(t, http.StatusNotFound, rec.Code, "no other kind is opened")
}

// TestReadingAnOrderDoesNotLetAnOperatorOpenARecord: opening is an order
// write, refused by the router to order:read alone.
func TestReadingAnOrderDoesNotLetAnOperatorOpenARecord(t *testing.T) {
	t.Parallel()

	ui, r := panelFor(t, nil, scopeOrderRead)
	surface := &fakeAfterSales{}
	ui.afterSales = surface
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, openPath("return"), http.NoBody))

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), scopeOrderWrite)
	assert.Empty(t, surface.acted)
}

// TestTheOrderPageOffersTheOpenings draws a quantity box per line and a
// replacement form only when there is something for it to settle.
func TestTheOrderPageOffersTheOpenings(t *testing.T) {
	t.Parallel()

	operator := corehttp.Principal{ID: "user_1", Kind: "user", Scopes: []string{"order:read", "order:write"}}
	without := newCatalogPanel(t, afterSalesCatalog(nil))
	without.afterSales = &fakeAfterSales{}
	body := getOrderPageAs(without, OrdersPath+"/order_1", operator).Body.String()

	for _, kind := range []string{"return", "claim", "exchange"} {
		assert.Contains(t, body, `action="`+openPath(kind)+`"`)
	}
	assert.Contains(t, body, `<input type="hidden" name="line_id" value="oli_ring">`)
	assert.NotContains(t, body, `action="`+openPath("replacement")+`"`, "nothing to settle yet")

	with := newCatalogPanel(t, afterSalesCatalog(map[string][]query.Record{
		EntityOrderClaim: {
			{"id": "claim_goods", "status": "requested", "type": "replace", "created_at": at(1)},
			{"id": "claim_money", "status": "requested", "type": "refund", "created_at": at(2)},
		},
		EntityOrderExchange: {
			{"id": "exch_done", "status": "completed", "created_at": at(3)},
			{"id": "exch_paid", "status": "funded", "created_at": at(4)},
		},
	}))
	with.afterSales = &fakeAfterSales{}
	body = getOrderPageAs(with, OrdersPath+"/order_1", operator).Body.String()
	assert.Contains(t, body, `action="`+openPath("replacement")+`"`)
	assert.Contains(t, body, `<option value="claim:claim_goods">`)
	assert.Contains(t, body, `<option value="exchange:exch_paid">`, "a funded exchange still sends its goods")
	assert.NotContains(t, body, `value="claim:claim_money"`, "a refund claim sends nothing")
	assert.NotContains(t, body, `value="exchange:exch_done"`, "a completed exchange sends nothing more")

	reader := getOrderPageAs(with, OrdersPath+"/order_1",
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: []string{"order:read"}})
	assert.NotContains(t, reader.Body.String(), "Open a record")
}
