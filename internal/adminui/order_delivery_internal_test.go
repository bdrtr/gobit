package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// fakeDeliveryChanger opens parcels, lists the scripted deliveries, quotes
// the scripted options counting each quote, and records each change.
type fakeDeliveryChanger struct {
	fakeAfterSales
	deliveries string
	quote      string
	quoteErr   error
	quotes     int
	answer     string
	changed    []string
	changeErr  error
}

func (f *fakeDeliveryChanger) OpenParcel(
	context.Context, string, string, string, map[string]int64,
) (parcel string, already bool, err error) {
	return "ful_1", false, nil
}

func (f *fakeDeliveryChanger) DeliveriesJSON(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(f.deliveries), nil
}

func (f *fakeDeliveryChanger) DeliveryQuoteJSON(context.Context, string) (json.RawMessage, error) {
	f.quotes++
	return json.RawMessage(f.quote), f.quoteErr
}

func (f *fakeDeliveryChanger) ChangeDelivery(
	_ context.Context, orderID, deliveryID, optionID, collectionID string, quotedAmount int64,
) (json.RawMessage, error) {
	f.changed = append(f.changed, fmt.Sprintf("%s|%s|%s|%s|%d", orderID, deliveryID, optionID, collectionID, quotedAmount))
	return json.RawMessage(f.answer), f.changeErr
}

// expressSold is an order sold one delivery of 25.00, and expressQuote the
// options it can be put on, its own among them.
const (
	expressSold  = `[{"id":"oship_1","shipping_option_id":"so_express","name":"Express","amount":2500}]`
	expressQuote = `[{"id":"so_express","name":"Express","amount":2500},
		{"id":"so_pickup","name":"Pickup","amount":1000},
		{"id":"so_same_day","name":"Same day","amount":4000},
		{"id":"so_other","name":"Other courier","amount":2500}]`
)

// orderCatalogWith is the linked order in the status given, its parcels in
// the statuses given, and extra fields on the order's own record.
func orderCatalogWith(status string, parcels []string, extra query.Record) *fakeCatalog {
	catalog := linkedOrderCatalog()
	inner := catalog.answer
	catalog.answer = func(spec query.GraphSpec) ([]query.Record, error, bool) {
		if spec.Entity == EntityOrder && spec.Filters[fieldAddsToOrderID] == nil {
			switch {
			case len(spec.Expand) == 0:
				record := orderRecord()
				record["status"] = status
				maps.Copy(record, extra)
				return []query.Record{record}, nil, true
			case spec.Expand[0].Link == linkOrderFulfillment && parcels != nil:
				var rows []query.Record
				for i, parcelStatus := range parcels {
					rows = append(rows, query.Record{
						"id": fmt.Sprintf("ful_%d", i), "status": parcelStatus,
						"created_at": time.Date(2026, 9, 4, 9, 20, i, 0, time.UTC),
					})
				}
				return []query.Record{{"id": "order_1", linkOrderFulfillment: rows}}, nil, true
			}
		}
		return inner(spec)
	}

	return catalog
}

// changerPanel is a panel over the order with the changing surface.
func changerPanel(t *testing.T, catalog *fakeCatalog, changer *fakeDeliveryChanger) *UI {
	t.Helper()

	panel := newCatalogPanel(t, catalog)
	panel.afterSales = changer
	panel.scopes = builtInScopes()

	return panel
}

// TestADeliveryIsChangedOnItsOrdersPage is ADR 0388: a writer sees what the
// delivery costs and is offered every other quoted option, each saying what
// it costs against the delivery and carrying the price drawn; the change is
// sent with the delivery, the option, the collection and that price, and the
// page says what it cost; what the panel cannot read is not sent, and a
// refusal, a quote that moved, is drawn on the order.
func TestADeliveryIsChangedOnItsOrdersPage(t *testing.T) {
	t.Parallel()

	changer := &fakeDeliveryChanger{deliveries: expressSold, quote: expressQuote, answer: "null"}
	panel := changerPanel(t, orderCatalogWith("pending", nil, nil), changer)
	page := OrdersPath + "/order_1"
	writer := []string{scopeOrderRead, scopeOrderWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "<h2>Deliveries</h2>")
	assert.Contains(t, body, "25.00 TRY")
	assert.Contains(t, body, `action="`+page+`/deliveries/oship_1"`)
	for _, want := range []string{
		`<option value="so_pickup|1000">Pickup, 10.00 TRY (credits 15.00 TRY)</option>`,
		`<option value="so_same_day|4000">Same day, 40.00 TRY (costs 15.00 TRY more)</option>`,
		`<option value="so_other|2500">Other courier, 25.00 TRY (costs the same)</option>`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, `value="so_express|`, "the option the delivery stands on is not offered")
	assert.Equal(t, 1, changer.quotes, "the page asks one quote")

	for answer, said := range map[string]string{
		`{"name":"Pickup","difference":-1500}`: "The delivery is on Pickup now; 15.00 TRY was credited against shipping.",
		`{"name":"Same day","difference":1500,"payment_collection_id":"pay_col_1"}`: "The delivery is on Same day now; " +
			"collection pay_col_1 paid 15.00 TRY more.",
		`{"name":"Other courier","difference":0}`: "The delivery is on Other courier now; it costs the same.",
		`null`: "The delivery was on that option already; nothing changed.",
	} {
		changer.answer = answer
		rec = campaignsRequest(panel, http.MethodPost, page+"/deliveries/oship_1", url.Values{
			formDeliveryOption: {"so_pickup|1000"}, formDeliveryCollection: {" pay_col_1 "}, formDeliveryCurrency: {"TRY"},
		}, writer...)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), said)
	}
	assert.Equal(t, "order_1|oship_1|so_pickup|pay_col_1|1000", changer.changed[0])
	sent := len(changer.changed)

	for _, option := range []string{"so_pickup", "so_pickup|ten", "|1000", ""} {
		rec = campaignsRequest(panel, http.MethodPost, page+"/deliveries/oship_1",
			url.Values{formDeliveryOption: {option}}, writer...)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, option)
		assert.Contains(t, rec.Body.String(), "The option the page was drawn with could not be read", option)
	}
	assert.Len(t, changer.changed, sent, "what the panel cannot read is not sent")

	changer.changeErr = errors.Conflict("fulfilling_quote_moved", "Pickup costs 1100 now, not 1000; draw the page again")
	rec = campaignsRequest(panel, http.MethodPost, page+"/deliveries/oship_1",
		url.Values{formDeliveryOption: {"so_pickup|1000"}}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "draw the page again")
	changer.changeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/deliveries/oship_1",
		url.Values{formDeliveryOption: {"so_pickup|1000"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	bare := cancelPanel(t, &fakeAfterSales{})
	rec = campaignsRequest(bare, http.MethodPost, page+"/deliveries/oship_1", url.Values{}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// deliveryForm is the change form of the delivery given, empty when it has
// none.
func deliveryForm(body, deliveryID string) string {
	_, form, found := strings.Cut(body, `/deliveries/`+deliveryID+`">`)
	if !found {
		return ""
	}
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// TestEachDeliveryIsPricedAgainstItself is ADR 0388 on an order sold a
// delivery per shipping profile (ADR 0332): each delivery's form offers every
// quoted option but its own, each saying what it costs against that delivery.
func TestEachDeliveryIsPricedAgainstItself(t *testing.T) {
	t.Parallel()

	changer := &fakeDeliveryChanger{quote: expressQuote, deliveries: `[
		{"id":"oship_1","shipping_option_id":"so_express","name":"Express","amount":2500},
		{"id":"oship_2","shipping_option_id":"so_pickup","name":"Pickup","amount":1000}]`}
	panel := changerPanel(t, orderCatalogWith("pending", nil, nil), changer)
	rec := campaignsRequest(panel, http.MethodGet, OrdersPath+"/order_1", nil, scopeOrderRead, scopeOrderWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()

	express := deliveryForm(body, "oship_1")
	require.NotEmpty(t, express, "the first delivery is offered a change")
	assert.Contains(t, express, `<option value="so_pickup|1000">Pickup, 10.00 TRY (credits 15.00 TRY)</option>`)
	assert.NotContains(t, express, `value="so_express|`, "its own option is not offered")

	pickup := deliveryForm(body, "oship_2")
	require.NotEmpty(t, pickup, "the second delivery is offered a change")
	for _, want := range []string{
		`<option value="so_express|2500">Express, 25.00 TRY (costs 15.00 TRY more)</option>`,
		`<option value="so_same_day|4000">Same day, 40.00 TRY (costs 30.00 TRY more)</option>`,
		`<option value="so_other|2500">Other courier, 25.00 TRY (costs 15.00 TRY more)</option>`,
	} {
		assert.Contains(t, pickup, want)
	}
	assert.NotContains(t, pickup, `value="so_pickup|`, "its own option is not offered")
	assert.Equal(t, 1, changer.quotes, "one quote serves every delivery")
}

// TestADeliveryIsChangedOnlyWhereItCanBe: the deliveries are read for a
// writer alone, and the options quoted only when a change is offered — a
// pending order with a delivery and no parcel the page reads on its way —
// since a quote asks every calculated option's provider; parcels the page
// may not read are left to the flow.
func TestADeliveryIsChangedOnlyWhereItCanBe(t *testing.T) {
	t.Parallel()

	page := OrdersPath + "/order_1"
	for name, tc := range map[string]struct {
		status     string
		parcels    []string
		deliveries string
		scopes     []string
		listed     bool
		offered    bool
	}{
		"a writer, parcels hidden": {"pending", []string{"pending"}, expressSold,
			[]string{scopeOrderRead, scopeOrderWrite}, true, true},
		"a writer, parcels canceled and returned": {"pending", []string{"canceled", "returned"}, expressSold,
			[]string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead}, true, true},
		"a writer, a parcel pending": {"pending", []string{"canceled", "pending"}, expressSold,
			[]string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead}, true, false},
		"a writer, a parcel shipped": {"pending", []string{"shipped"}, expressSold,
			[]string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead}, true, false},
		"a writer, a parcel delivered": {"pending", []string{"delivered"}, expressSold,
			[]string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead}, true, false},
		"a writer, a completed order": {"completed", nil, expressSold,
			[]string{scopeOrderRead, scopeOrderWrite}, true, false},
		"a writer, no delivery sold": {"pending", nil, `[]`,
			[]string{scopeOrderRead, scopeOrderWrite}, false, false},
		"a reader": {"pending", nil, expressSold, []string{scopeOrderRead}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			changer := &fakeDeliveryChanger{deliveries: tc.deliveries, quote: expressQuote}
			panel := changerPanel(t, orderCatalogWith(tc.status, tc.parcels, nil), changer)
			rec := campaignsRequest(panel, http.MethodGet, page, nil, tc.scopes...)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			body := rec.Body.String()

			assert.Equal(t, tc.listed, strings.Contains(body, "Express"), "the delivery listed")
			assert.Equal(t, tc.offered, strings.Contains(body, "/deliveries/oship_1"), "the change offered")
			wantQuotes := 0
			if tc.offered {
				wantQuotes = 1
			}
			assert.Equal(t, wantQuotes, changer.quotes, "a quote is asked only where a change is offered")
		})
	}
}

// TestAQuoteThatFailsOrOffersNothingSaysSo: a quote that cannot be read
// draws the deliveries without a form, and one with nothing but the
// delivery's own option says no other is quoted.
func TestAQuoteThatFailsOrOffersNothingSaysSo(t *testing.T) {
	t.Parallel()

	writer := []string{scopeOrderRead, scopeOrderWrite}
	failing := &fakeDeliveryChanger{deliveries: expressSold, quoteErr: errors.Unavailable("carrier_down", "no")}
	rec := campaignsRequest(changerPanel(t, orderCatalogWith("pending", nil, nil), failing),
		http.MethodGet, OrdersPath+"/order_1", nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "The options could not be quoted.")
	assert.Contains(t, rec.Body.String(), "25.00 TRY", "the deliveries are still listed")
	assert.NotContains(t, rec.Body.String(), "/deliveries/oship_1")

	alone := &fakeDeliveryChanger{deliveries: expressSold, quote: `[{"id":"so_express","name":"Express","amount":2500}]`}
	rec = campaignsRequest(changerPanel(t, orderCatalogWith("pending", nil, nil), alone),
		http.MethodGet, OrdersPath+"/order_1", nil, writer...)
	assert.Contains(t, rec.Body.String(), "No other option is quoted for this order.")
	assert.NotContains(t, rec.Body.String(), "/deliveries/oship_1")
}

// TestOnlyAWriterChangesADelivery holds the gate on its own: the page reads
// the deliveries for a writer alone, so a reader never reaches it there, and
// the gate does not lean on that.
func TestOnlyAWriterChangesADelivery(t *testing.T) {
	t.Parallel()

	panel := changerPanel(t, orderCatalogWith("pending", nil, nil), &fakeDeliveryChanger{})
	detail := orderDetail{orderRow: orderRow{Status: "pending"}}
	for scopes, offered := range map[string]bool{scopeOrderRead: false, scopeOrderWrite: true} {
		r := (&http.Request{}).WithContext(corehttp.WithPrincipal(context.Background(),
			corehttp.Principal{ID: "user_1", Kind: "user", Scopes: []string{scopes}}))
		assert.Equal(t, offered, panel.canChangeDelivery(r, &detail), scopes)
	}
}
