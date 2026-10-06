package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakeDeliveryOpener opens parcels as fakeParcelOpener does and lists the
// order's deliveries as scripted.
type fakeDeliveryOpener struct {
	fakeParcelOpener
	deliveries string
	listErr    error
	listed     []string
}

func (f *fakeDeliveryOpener) DeliveriesJSON(_ context.Context, orderID string) (json.RawMessage, error) {
	f.listed = append(f.listed, orderID)
	return json.RawMessage(f.deliveries), f.listErr
}

// deliveryPanel is a panel over the order, its deliveries listed as
// scripted.
func deliveryPanel(t *testing.T, opener *fakeDeliveryOpener) *UI {
	t.Helper()

	panel := newCatalogPanel(t, linkedOrderCatalog())
	panel.afterSales = opener
	panel.scopes = builtInScopes()

	return panel
}

// openForm is the page's open form.
func openForm(t *testing.T, body string) string {
	t.Helper()

	_, form, found := strings.Cut(body, `action="`+OrdersPath+`/order_1/parcels"`)
	require.True(t, found, "the page offers the open form")
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// TestAParcelIsOpenedOnTheDeliveryChosen is ADR 0332: an order sold several
// deliveries is asked which one the parcel goes on, each named with its
// option, and the surface is asked to open on the one chosen; an order sold
// one is opened on it by the flow and asked nothing; an order sold none is
// told so and offered no form; deliveries that cannot be read leave the
// choice to the flow; a reader's page reads none of them.
func TestAParcelIsOpenedOnTheDeliveryChosen(t *testing.T) {
	t.Parallel()

	page := OrdersPath + "/order_1"
	writer := []string{scopeOrderRead, scopeOrderWrite}
	two := &fakeDeliveryOpener{deliveries: `[
		{"id":"osm_books","shipping_option_id":"so_post","name":"Books by post"},
		{"id":"osm_bulky","shipping_option_id":"so_freight","name":"Bulky by van"}]`}
	panel := deliveryPanel(t, two)

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := openForm(t, rec.Body.String())
	assert.Contains(t, form, `<select name="delivery" aria-label="delivery" required>`)
	assert.Contains(t, form, `<option value="osm_books">Books by post (so_post)</option>`)
	assert.Contains(t, form, `<option value="osm_bulky">Bulky by van (so_freight)</option>`)
	assert.Equal(t, []string{"order_1"}, two.listed, "the order's own deliveries are read")
	key := parcelKey.FindStringSubmatch(form)
	require.Len(t, key, 2, "the form still carries its key")

	rec = campaignsRequest(panel, http.MethodPost, page+"/parcels",
		url.Values{formParcelKey: {key[1]}, formParcelDelivery: {" osm_bulky "}, "units_oli_ring": {"1"}}, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"order_1|osm_bulky|" + key[1]}, two.keys, "the delivery chosen")
	assert.Contains(t, rec.Body.String(), "Parcel ful_new was opened.")

	two.openErr = errors.NotFound("order_delivery_missing", "order order_1 has no delivery osm_gone; draw the page again")
	rec = campaignsRequest(panel, http.MethodPost, page+"/parcels",
		url.Values{formParcelKey: {key[1]}, formParcelDelivery: {"osm_gone"}, "units_oli_ring": {"1"}}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "has no delivery osm_gone")

	one := &fakeDeliveryOpener{deliveries: `[{"id":"osm_only","shipping_option_id":"so_post","name":"Post"}]`}
	form = openForm(t, campaignsRequest(deliveryPanel(t, one), http.MethodGet, page, nil, writer...).Body.String())
	assert.NotContains(t, form, `name="delivery"`, "one delivery is the flow's to take")

	none := &fakeDeliveryOpener{deliveries: `[]`}
	body := campaignsRequest(deliveryPanel(t, none), http.MethodGet, page, nil, writer...).Body.String()
	assert.Contains(t, body, "This order was sold no delivery, so there is none to open a parcel on.")
	assert.NotContains(t, body, `action="`+page+`/parcels"`, "and no form")

	failing := &fakeDeliveryOpener{listErr: errors.Unavailable("db_down", "no answer")}
	form = openForm(t, campaignsRequest(deliveryPanel(t, failing), http.MethodGet, page, nil, writer...).Body.String())
	assert.NotContains(t, form, `name="delivery"`, "unread deliveries leave the choice to the flow")

	reader := &fakeDeliveryOpener{deliveries: `[]`}
	body = campaignsRequest(deliveryPanel(t, reader), http.MethodGet, page, nil, scopeOrderRead).Body.String()
	assert.Empty(t, reader.listed, "a reader's page reads no deliveries")
	assert.NotContains(t, body, "sold no delivery")
}
