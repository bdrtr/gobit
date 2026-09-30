package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// fakeAfterSales records the act that reached it and answers as scripted.
type fakeAfterSales struct {
	err      error
	warnings []string
	recorded bool
	already  bool

	acted  string
	id     string
	text   string
	amount int64
	reason string
}

func (f *fakeAfterSales) note(act, id, text string, amount int64, reason string) {
	f.acted, f.id, f.text, f.amount, f.reason = act, id, text, amount, reason
}

func (f *fakeAfterSales) ReceiveReturn(
	_ context.Context, id, location string,
) (lines int, units int64, warnings []string, err error) {
	f.note("receive", id, location, 0, "")
	return 2, 3, f.warnings, f.err
}

func (f *fakeAfterSales) RefundReturn(
	_ context.Context, id string, amount int64, reason string,
) (refunded int64, recorded bool, warnings []string, err error) {
	f.note("refund-return", id, "", amount, reason)
	return 1_250, f.recorded, f.warnings, f.err
}

func (f *fakeAfterSales) CancelReturn(_ context.Context, id string) error {
	f.note("cancel-return", id, "", 0, "")
	return f.err
}

func (f *fakeAfterSales) SettleClaim(
	_ context.Context, id string, amount int64, reason string,
) (refunded int64, recorded bool, warnings []string, err error) {
	f.note("settle", id, "", amount, reason)
	return 500, f.recorded, f.warnings, f.err
}

func (f *fakeAfterSales) CancelClaim(_ context.Context, id string) error {
	f.note("cancel-claim", id, "", 0, "")
	return f.err
}

func (f *fakeAfterSales) FundExchange(_ context.Context, id, collection string) error {
	f.note("fund", id, collection, 0, "")
	return f.err
}

func (f *fakeAfterSales) RefundExchange(_ context.Context, id, reason string) error {
	f.note("refund-exchange", id, "", 0, reason)
	return f.err
}

func (f *fakeAfterSales) CancelExchange(_ context.Context, id string) error {
	f.note("cancel-exchange", id, "", 0, "")
	return f.err
}

func (f *fakeAfterSales) DispatchReplacement(
	_ context.Context, id string,
) (parcel string, units int64, already bool, err error) {
	f.note("dispatch", id, "", 0, "")
	return "ful_7", 4, f.already, f.err
}

func (f *fakeAfterSales) WithdrawReplacement(_ context.Context, id string) error {
	f.note("withdraw", id, "", 0, "")
	return f.err
}

// act posts one act as an operator holding the given privileges.
func act(t *testing.T, surface AfterSalesAdmin, catalog *fakeCatalog, path string, form url.Values, scopes ...string) *httptest.ResponseRecorder {
	t.Helper()

	panel := newCatalogPanel(t, catalog)
	panel.afterSales = surface
	r := chi.NewRouter()
	r.Get(OrderPath, panel.showOrder)
	r.Post(OrderAfterSalePath, panel.submitAfterSale)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// actPath is the address of one act on order_1's record.
func actPath(kind, record, verb string) string {
	return OrdersPath + "/order_1/after-sales/" + kind + "/" + record + "/" + verb
}

// TestEachAfterSaleActReachesItsOwnMethod walks the acts: each hands the
// record and what its form asked for to its own method, and the order page
// comes back saying what happened.
func TestEachAfterSaleActReachesItsOwnMethod(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		kind, verb string
		form       url.Values
		acted      string
		text       string
		amount     int64
		reason     string
		done       string
	}{
		{"return", "receive", url.Values{"location_id": {" sloc_back "}}, "receive", "sloc_back", 0, "",
			"The return was received: 3 units of 2 lines went back to stock."},
		{"return", "refund", url.Values{"amount": {"12.50"}, "currency": {"try"}, "reason": {"scratched"}},
			"refund-return", "", 1_250, "scratched", "12.50 TRY was refunded."},
		{"return", "cancel", nil, "cancel-return", "", 0, "", "The return was withdrawn."},
		{"claim", "settle", url.Values{"currency": {"TRY"}}, "settle", "", 0, "", "5.00 TRY was refunded."},
		{"claim", "cancel", nil, "cancel-claim", "", 0, "", "The claim was withdrawn."},
		{"exchange", "fund", url.Values{"collection_id": {"paycol_9"}}, "fund", "paycol_9", 0, "",
			"The exchange's difference is answered by paycol_9."},
		{"exchange", "refund", url.Values{"reason": {"unsendable"}}, "refund-exchange", "", 0, "unsendable",
			"The exchange's money was sent back and the exchange withdrawn."},
		{"exchange", "cancel", nil, "cancel-exchange", "", 0, "", "The exchange was withdrawn."},
		{"replacement", "dispatch", nil, "dispatch", "", 0, "", "The replacement left in parcel ful_7 with 4 units."},
		{"replacement", "withdraw", nil, "withdraw", "", 0, "", "The replacement was withdrawn and its units given back."},
	} {
		surface := &fakeAfterSales{recorded: true}
		rec := act(t, surface, afterSalesCatalog(nil), actPath(tc.kind, "rec_1", tc.verb), tc.form,
			"order:read", "order:write")

		require.Equalf(t, http.StatusOK, rec.Code, "%s %s: %s", tc.kind, tc.verb, rec.Body.String())
		assert.Equal(t, tc.acted, surface.acted, tc.verb)
		assert.Equal(t, "rec_1", surface.id, tc.verb)
		assert.Equal(t, tc.text, surface.text, tc.verb)
		assert.Equal(t, tc.amount, surface.amount, tc.verb)
		assert.Equal(t, tc.reason, surface.reason, tc.verb)
		assert.Contains(t, rec.Body.String(), `<p role="status">`+strings.ReplaceAll(tc.done, "'", "&#39;")+`</p>`,
			"%s %s", tc.kind, tc.verb)
		assert.Contains(t, rec.Body.String(), "Order #1042", "the order page comes back")
	}
}

// TestAReplacementAlreadySentSaysNothingMoved words the repeat.
func TestAReplacementAlreadySentSaysNothingMoved(t *testing.T) {
	t.Parallel()

	rec := act(t, &fakeAfterSales{already: true}, afterSalesCatalog(nil),
		actPath("replacement", "rec_1", "dispatch"), nil, "order:read", "order:write")

	assert.Contains(t, rec.Body.String(), "The replacement had already left in parcel ful_7; nothing moved.")
}

// TestAnActsWarningsAreShown prints what needs a human, and adds the one a
// refund gives when the order does not record the money.
func TestAnActsWarningsAreShown(t *testing.T) {
	t.Parallel()

	rec := act(t, &fakeAfterSales{warnings: []string{"sloc_back counted fewer units"}}, afterSalesCatalog(nil),
		actPath("return", "rec_1", "refund"), url.Values{"currency": {"TRY"}}, "order:read", "order:write")

	body := rec.Body.String()
	assert.Contains(t, body, `<p role="alert">sloc_back counted fewer units</p>`)
	assert.Contains(t, body, "The money left, and the order does not record it")
}

// TestARefusedActSaysWhyOnTheOrderPage: the module's sentence, 422, and the
// page to act again from.
func TestARefusedActSaysWhyOnTheOrderPage(t *testing.T) {
	t.Parallel()

	for message, refusal := range map[string]error{
		"return rec_1 has not been received": errors.Conflict("returns_invalid_input", "return rec_1 has not been received"),
		"the location is required":           errors.Invalid("order_invalid", "the location is required"),
		"no such return":                     errors.NotFound("order_return_not_found", "no such return"),
	} {
		rec := act(t, &fakeAfterSales{err: refusal}, afterSalesCatalog(nil),
			actPath("return", "rec_1", "refund"), url.Values{"currency": {"TRY"}}, "order:read", "order:write")

		require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		assert.Contains(t, rec.Body.String(), `<p role="alert">`+message+`</p>`)
		assert.NotContains(t, rec.Body.String(), `role="status"`)
		assert.Contains(t, rec.Body.String(), "Order #1042", "the page to act again from")
	}
}

// TestAnUnexpectedFailureIsNotShownAsARefusal: a fault the operator cannot
// act on is the panel's error page, not a sentence on the order.
func TestAnUnexpectedFailureIsNotShownAsARefusal(t *testing.T) {
	t.Parallel()

	rec := act(t, &fakeAfterSales{err: errors.Unavailable("payment_down", "the provider did not answer")},
		afterSalesCatalog(nil), actPath("claim", "rec_1", "cancel"), nil, "order:read", "order:write")

	assert.GreaterOrEqual(t, rec.Code, http.StatusInternalServerError)
	assert.NotContains(t, rec.Body.String(), "the provider did not answer")
	assert.NotContains(t, rec.Body.String(), "Order #1042")
}

// TestAnActTheKindDoesNotTakeIsNotFound refuses an act the kind does not take,
// and names the missing surface when there is none.
func TestAnActTheKindDoesNotTakeIsNotFound(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		actPath("return", "rec_1", "dispatch"), actPath("order", "rec_1", "cancel"), actPath("claim", "rec_1", "fund"),
	} {
		surface := &fakeAfterSales{}
		rec := act(t, surface, afterSalesCatalog(nil), path, nil, "order:read", "order:write")
		assert.Equal(t, http.StatusNotFound, rec.Code, path)
		assert.Empty(t, surface.acted, path)
	}

	rec := act(t, nil, afterSalesCatalog(nil), actPath("return", "rec_1", "cancel"), nil, "order:write")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "panel surface is not registered")
}

// TestAnOperatorWhoMayOnlyActIsToldWhatHappened: order:write does not open the
// order page (ADR 0260), so the outcome is the whole answer and nothing of the
// order is read.
func TestAnOperatorWhoMayOnlyActIsToldWhatHappened(t *testing.T) {
	t.Parallel()

	catalog := afterSalesCatalog(nil)
	rec := act(t, &fakeAfterSales{}, catalog, actPath("return", "rec_1", "cancel"), nil, "order:write")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "The return was withdrawn.")
	assert.Empty(t, catalog.specs, "nothing of the order is read")
}

// TestTheOrderPageOffersTheActsARecordsStatusAllows draws the forms the
// operator may use, and none without order:write.
func TestTheOrderPageOffersTheActsARecordsStatusAllows(t *testing.T) {
	t.Parallel()

	records := map[string][]query.Record{
		EntityOrderReturn: {
			{"id": "ret_open", "status": "requested", "created_at": at(1)},
			{"id": "ret_back", "status": "received", "created_at": at(2)},
		},
		EntityOrderClaim: {
			{"id": "claim_money", "status": "requested", "type": "refund", "created_at": at(3)},
			{"id": "claim_goods", "status": "requested", "type": "replace", "created_at": at(4)},
		},
		EntityOrderExchange: {{"id": "exch_paid", "status": "funded", "created_at": at(5)}},
		EntityOrderReplacement: {
			{"id": "orepl_gone", "status": "dispatched", "claim_id": "claim_goods", "created_at": at(6)},
		},
	}
	panel := newCatalogPanel(t, afterSalesCatalog(records))
	panel.afterSales = &fakeAfterSales{}
	page := getOrderPageAs(panel, OrdersPath+"/order_1",
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: []string{"order:read", "order:write"}})
	body := page.Body.String()

	for _, offered := range []string{
		actPath("return", "ret_open", "receive"), actPath("return", "ret_open", "cancel"),
		actPath("return", "ret_back", "refund"),
		actPath("claim", "claim_money", "settle"), actPath("claim", "claim_money", "cancel"),
		actPath("claim", "claim_goods", "cancel"),
		actPath("exchange", "exch_paid", "refund"),
	} {
		assert.Contains(t, body, `action="`+offered+`"`)
	}
	for _, withheld := range []string{
		actPath("return", "ret_back", "cancel"), actPath("claim", "claim_goods", "settle"),
		actPath("exchange", "exch_paid", "cancel"), "orepl_gone/dispatch", "orepl_gone/withdraw",
	} {
		assert.NotContains(t, body, withheld)
	}
	assert.Contains(t, body, `name="location_id"`)
	assert.Contains(t, body, `placeholder="empty: the claim&#39;s own amount"`)
	assert.Contains(t, body, `<input type="hidden" name="currency" value="TRY">`,
		"a typed amount is read in the order's currency")

	readOnly := getOrderPageAs(panel, OrdersPath+"/order_1",
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: []string{"order:read"}})
	assert.NotContains(t, readOnly.Body.String(), "/after-sales/", "reading does not offer acting")
}

// TestReadingAnOrderDoesNotLetAnOperatorActOnIt: an act is an order write,
// and the router refuses it to an operator holding order:read alone, naming
// the privilege, before the surface is reached.
func TestReadingAnOrderDoesNotLetAnOperatorActOnIt(t *testing.T) {
	t.Parallel()

	ui, r := panelFor(t, nil, scopeOrderRead)
	surface := &fakeAfterSales{}
	ui.afterSales = surface
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, actPath("return", "rec_1", "cancel"), http.NoBody))

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), scopeOrderWrite)
	assert.Empty(t, surface.acted)
}
