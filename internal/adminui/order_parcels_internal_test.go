package adminui

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakeParcelOpener acts on after-sales records as the shared fake does and
// opens parcels, recording each delivery and key.
type fakeParcelOpener struct {
	fakeAfterSales
	keys    []string
	already bool
	openErr error
}

func (f *fakeParcelOpener) OpenParcel(
	_ context.Context, orderID, delivery, key string,
) (parcel string, already bool, err error) {
	f.keys = append(f.keys, orderID+"|"+delivery+"|"+key)
	return "ful_new", f.already, f.openErr
}

// fakeParcelMover records each move.
type fakeParcelMover struct {
	moves []string
	err   error
}

func (f *fakeParcelMover) ShipParcel(_ context.Context, id, number, page string) error {
	f.moves = append(f.moves, "ship|"+id+"|"+number+"|"+page)
	return f.err
}

func (f *fakeParcelMover) DeliverParcel(_ context.Context, id string) error {
	f.moves = append(f.moves, "deliver|"+id)
	return f.err
}

func (f *fakeParcelMover) ReturnParcel(_ context.Context, id string) error {
	f.moves = append(f.moves, "return|"+id)
	return f.err
}

func (f *fakeParcelMover) CancelParcel(_ context.Context, id string) error {
	f.moves = append(f.moves, "cancel|"+id)
	return f.err
}

// parcelsPanel is a panel over the order with two pending parcels and a
// delivered one.
func parcelsPanel(t *testing.T, opener *fakeParcelOpener, mover ParcelMover) *UI {
	t.Helper()

	panel := newCatalogPanel(t, linkedOrderCatalog())
	if opener != nil {
		panel.afterSales = opener
	}
	panel.parcels = mover
	panel.scopes = builtInScopes()

	return panel
}

// parcelKey reads the key the page's open form carries.
var parcelKey = regexp.MustCompile(`name="key" value="(panel-[0-9a-f]{32})"`)

// TestAnOrdersParcelsAreOpenedAndMovedOnItsPage is ADR 0324: an order writer
// is offered an open form carrying a fresh key, and the surface is asked with
// it; a fulfillment writer is offered each parcel's moves from its status,
// none on a delivered one; a reader is offered neither.
func TestAnOrdersParcelsAreOpenedAndMovedOnItsPage(t *testing.T) {
	t.Parallel()

	opener := &fakeParcelOpener{}
	mover := &fakeParcelMover{}
	panel := parcelsPanel(t, opener, mover)
	page := OrdersPath + "/order_1"
	all := []string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead, scopeFulfillmentWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, all...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	first := parcelKey.FindStringSubmatch(body)
	require.Len(t, first, 2, "the open form carries a key")
	assert.Contains(t, body, `action="`+page+`/parcels"`)
	again := parcelKey.FindStringSubmatch(campaignsRequest(panel, http.MethodGet, page, nil, all...).Body.String())
	require.Len(t, again, 2)
	assert.NotEqual(t, first[1], again[1], "each drawing carries its own key")
	assert.Contains(t, body, `action="`+page+`/parcels/ful_late/ship"`)
	assert.Contains(t, body, `action="`+page+`/parcels/ful_late/cancel"`)
	assert.Contains(t, body, `name="tracking_number"`)
	assert.NotContains(t, body, "/parcels/ful_early/", "a delivered parcel takes no move")

	rec = campaignsRequest(panel, http.MethodPost, page+"/parcels", url.Values{formParcelKey: {first[1]}}, all...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"order_1||" + first[1]}, opener.keys, "a surface that lists no deliveries names none")
	assert.Contains(t, rec.Body.String(), "Parcel ful_new was opened.")
	opener.already = true
	rec = campaignsRequest(panel, http.MethodPost, page+"/parcels", url.Values{formParcelKey: {first[1]}}, all...)
	assert.Contains(t, rec.Body.String(), "This form had already opened parcel ful_new; nothing new was opened.")

	rec = campaignsRequest(panel, http.MethodPost, page+"/parcels/ful_late/ship",
		url.Values{formTrackingNumber: {" TK-9 "}, formTrackingURL: {" https://carrier.example/TK-9 "}}, all...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Parcel ful_late is on its way.")
	for _, act := range []string{"deliver", "return", "cancel"} {
		campaignsRequest(panel, http.MethodPost, page+"/parcels/ful_late/"+act, url.Values{}, all...)
	}
	assert.Equal(t, []string{
		"ship|ful_late|TK-9|https://carrier.example/TK-9", "deliver|ful_late", "return|ful_late", "cancel|ful_late",
	}, mover.moves)

	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeOrderRead, scopeFulfillmentRead)
	assert.NotContains(t, rec.Body.String(), `name="key"`, "a reader opens nothing")
	assert.NotContains(t, rec.Body.String(), "/parcels/ful_late/", "a reader moves nothing")
	rec = campaignsRequest(panel, http.MethodPost, page+"/parcels/ful_late/fly", url.Values{}, all...)
	assert.Equal(t, http.StatusNotFound, rec.Code, "a move the panel does not know")
}

// TestAParcelRefusalIsDrawnOnTheOrder: the module's refusal of a move or an
// open, and a form without a key, are drawn on the order; to a writer who
// cannot read the order as the reason alone; a failure is not a refusal; an
// installation without the surfaces offers nothing and answers 503.
func TestAParcelRefusalIsDrawnOnTheOrder(t *testing.T) {
	t.Parallel()

	opener := &fakeParcelOpener{}
	mover := &fakeParcelMover{}
	panel := parcelsPanel(t, opener, mover)
	page := OrdersPath + "/order_1"
	all := []string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead, scopeFulfillmentWrite}

	rec := campaignsRequest(panel, http.MethodPost, page+"/parcels", url.Values{}, all...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "The form carried no key, so nothing was opened; draw the page again.")
	assert.Empty(t, opener.keys, "nothing is opened without a key")

	opener.openErr = errors.Conflict("fulfilling_shipment_canceled", "the idempotency key names shipment ful_x, which was canceled")
	rec = campaignsRequest(panel, http.MethodPost, page+"/parcels", url.Values{formParcelKey: {"panel-k"}}, all...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "which was canceled")
	assert.Contains(t, rec.Body.String(), "Silver ring", "the refusal is drawn on the order")

	mover.err = errors.Conflict("fulfillment_status_conflict", "fulfillment ful_early is delivered; it cannot be canceled")
	rec = campaignsRequest(panel, http.MethodPost, page+"/parcels/ful_early/cancel", url.Values{}, all...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "it cannot be canceled")
	rec = campaignsRequest(panel, http.MethodPost, page+"/parcels/ful_early/cancel", url.Values{}, scopeFulfillmentWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Silver ring", "a writer who cannot read the order is shown none of it")

	mover.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/parcels/ful_late/deliver", url.Values{}, all...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	bare := parcelsPanel(t, nil, nil)
	rec = campaignsRequest(bare, http.MethodGet, page, nil, all...)
	assert.NotContains(t, rec.Body.String(), `name="key"`, "no opener, no open form")
	assert.NotContains(t, rec.Body.String(), "/parcels/ful_late/", "no mover, no moves")
	rec = campaignsRequest(bare, http.MethodPost, page+"/parcels/ful_late/deliver", url.Values{}, all...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(bare, http.MethodPost, page+"/parcels", url.Values{formParcelKey: {"panel-k"}}, all...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
