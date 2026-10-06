package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// The tests here are about a return's parcels on the order page (ADR 0413) and
// a move naming a parcel that is not the order's (gap D266).

// fakeReturnParcels moves parcels as the shared fake does and opens a return's
// parcel, recording each open.
type fakeReturnParcels struct {
	fakeParcelMover
	opens      []string
	already    bool
	openErr    error
	options    string
	optionsErr error
	regions    []string
}

func (f *fakeReturnParcels) OpenReturnParcel(
	_ context.Context, orderID, returnID, optionID, key string, lineIDs []string, quantities []int64,
) (parcel string, already bool, err error) {
	raw, _ := json.Marshal(quantities)
	f.opens = append(f.opens, strings.Join([]string{orderID, returnID, optionID, key, strings.Join(lineIDs, ","), string(raw)}, "|"))
	return "ful_back_new", f.already, f.openErr
}

func (f *fakeReturnParcels) ReturnOptionsJSON(_ context.Context, region, currency string) (json.RawMessage, error) {
	f.regions = append(f.regions, region+"|"+currency)
	if f.options == "" {
		return json.RawMessage(`[{"id":"sopt_back","name":"Return by post"}]`), f.optionsErr
	}
	return json.RawMessage(f.options), f.optionsErr
}

// returnsCatalog is an order with a requested return of three rings, a
// received one and a canceled one; the requested return has a live parcel
// holding one ring and a canceled one that held two. The order's own parcel is
// ful_out, a parcel it joined as an addition is ful_joined, and ful_other is
// another order's.
func returnsCatalog() *fakeCatalog {
	opened := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	parcels := map[string]query.Record{
		"ful_out":    {fieldID: "ful_out", FieldParcelReference: "order_1"},
		"ful_joined": {fieldID: "ful_joined", FieldParcelReference: "order_parent"},
		"ful_live":   {fieldID: "ful_live", FieldParcelReference: "order_1"},
		"ful_other":  {fieldID: "ful_other", FieldParcelReference: "order_2"},
	}
	return &fakeCatalog{
		byEntity: map[string][]query.Record{
			EntityRegion:        {currencyRecord("TRY", 2)},
			EntityOrderLineItem: {{"id": "oli_ring", "title": "Silver ring", "quantity": int64(3)}},
			EntityOrderReturn: {
				{
					"id": "ret_A", "status": "requested", "created_at": opened.Add(2 * time.Hour),
					"items": []map[string]any{{"line_item_id": "oli_ring", "quantity": int64(3)}},
				},
				{
					"id": "ret_B", "status": "received", "created_at": opened.Add(time.Hour),
					"items": []map[string]any{{"line_item_id": "oli_ring", "quantity": int64(1)}},
				},
				{
					"id": "ret_C", "status": "canceled", "created_at": opened,
					"items": []map[string]any{{"line_item_id": "oli_ring", "quantity": int64(1)}},
				},
			},
		},
		answer: func(spec query.GraphSpec) ([]query.Record, error, bool) {
			switch spec.Entity {
			case EntityFulfillment:
				if id, ok := spec.Filters[filterID].(string); ok {
					if record, known := parcels[id]; known {
						return []query.Record{record}, nil, true
					}
					return nil, nil, true
				}
				if spec.Filters[FieldParcelReturnID] != "ret_A" {
					return nil, nil, true
				}
				return []query.Record{
					{
						fieldID: "ful_live", fieldStatus: "pending", fieldCreatedAt: opened.Add(3 * time.Hour),
						fieldItems: []map[string]any{{"line_item_id": "oli_ring", "quantity": int64(1)}},
					},
					{
						fieldID: "ful_dropped", fieldStatus: "canceled", fieldCreatedAt: opened.Add(2 * time.Hour),
						fieldItems: []map[string]any{{"line_item_id": "oli_ring", "quantity": int64(2)}},
					},
				}, nil, true
			case EntityOrder:
				if spec.Filters[fieldAddsToOrderID] != nil {
					return nil, nil, true
				}
				if len(spec.Expand) == 0 {
					record := orderRecord()
					record[FieldOrderRegion] = "reg_tr"
					return []query.Record{record}, nil, true
				}
				record := query.Record{"id": "order_1"}
				if spec.Expand[0].Link == linkOrderFulfillment {
					record[linkOrderFulfillment] = []query.Record{
						{fieldID: "ful_out", fieldStatus: "pending", fieldCreatedAt: opened},
						{fieldID: "ful_joined", fieldStatus: "pending", fieldCreatedAt: opened},
					}
				}
				return []query.Record{record}, nil, true
			}
			return nil, nil, false
		},
	}
}

// returnsPanel is a panel over returnsCatalog with the given parcel surface.
func returnsPanel(t *testing.T, catalog *fakeCatalog, parcels ParcelMover) *UI {
	t.Helper()

	panel := newCatalogPanel(t, catalog)
	panel.parcels = parcels
	panel.scopes = builtInScopes()

	return panel
}

// returnParcelAction is the open form of return ret_A.
const returnParcelAction = `action="` + OrdersPath + `/order_1/returns/ret_A/parcels"`

// unitsField reads the units field the return's form draws for the ring.
var unitsField = regexp.MustCompile(`name="units_oli_ring" min="0" max="(\d+)" value="(\d+)" aria-label="units of Silver ring coming back"`)

// TestAReturnListsItsParcelsAndOffersWhatItAwaits is ADR 0413: the requested
// return lists the parcels bringing it back, found by the return they name, and
// offers a parcel for its three rings less the one its live parcel holds; the
// canceled parcel frees its two. A received return is offered none, and a
// canceled one is not read.
func TestAReturnListsItsParcelsAndOffersWhatItAwaits(t *testing.T) {
	t.Parallel()

	catalog := returnsCatalog()
	parcels := &fakeReturnParcels{}
	panel := returnsPanel(t, catalog, parcels)
	all := []string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead, scopeFulfillmentWrite}

	rec := campaignsRequest(panel, http.MethodGet, OrdersPath+"/order_1", nil, all...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "ful_live")
	assert.Contains(t, body, "ful_dropped")
	assert.Contains(t, body, `action="`+OrdersPath+`/order_1/parcels/ful_live/ship"`, "a return's parcel is moved like any")
	assert.Equal(t, 1, strings.Count(body, `/returns/`), "one form, the requested return's")
	assert.Contains(t, body, returnParcelAction)
	assert.Contains(t, body, `<option value="sopt_back">Return by post</option>`)
	units := unitsField.FindStringSubmatch(body)
	require.Len(t, units, 3, "the ring is offered")
	assert.Equal(t, []string{"2", "2"}, units[1:], "three asked back, one in a live parcel, the canceled parcel's two freed")
	assert.Equal(t, []string{"reg_tr|TRY"}, parcels.regions, "the options are the order's region's, read once")

	var asked []any
	for _, spec := range catalog.specs {
		if spec.Entity == EntityFulfillment {
			if _, byID := spec.Filters[filterID]; !byID {
				asked = append(asked, spec.Filters[FieldParcelReturnID])
			}
		}
	}
	assert.ElementsMatch(t, []any{"ret_A", "ret_B"}, asked,
		"each return that is not canceled is read by the return it names, not by the order's reference")
}

// TestTheReturnParcelFormNeedsTheFulfillmentWrite: a reader of the parcels sees
// a return's parcels and is drawn no form; one who may not read them is shown
// none and nothing is read.
func TestTheReturnParcelFormNeedsTheFulfillmentWrite(t *testing.T) {
	t.Parallel()

	catalog := returnsCatalog()
	parcels := &fakeReturnParcels{}
	panel := returnsPanel(t, catalog, parcels)

	body := campaignsRequest(panel, http.MethodGet, OrdersPath+"/order_1", nil,
		scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead).Body.String()
	assert.Contains(t, body, "ful_live")
	assert.NotContains(t, body, returnParcelAction, "no fulfillment:write, no form")
	assert.Empty(t, parcels.regions, "and no options are read")

	before := len(catalog.specs)
	body = campaignsRequest(panel, http.MethodGet, OrdersPath+"/order_1", nil, scopeOrderRead, scopeOrderWrite).Body.String()
	assert.NotContains(t, body, "ful_live", "no fulfillment:read, no parcels")
	for _, spec := range catalog.specs[before:] {
		assert.NotEqual(t, EntityFulfillment, spec.Entity, "nothing of the parcels is read")
	}
}

// TestAReturnAwaitingNothingOffersNoParcel: once its live parcels hold all it
// names, a requested return is drawn no form; a region offering no return
// option, or options that cannot be read, say so instead of a form.
func TestAReturnAwaitingNothingOffersNoParcel(t *testing.T) {
	t.Parallel()

	all := []string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead, scopeFulfillmentWrite}
	catalog := returnsCatalog()
	catalog.byEntity[EntityOrderReturn][0]["items"] = []map[string]any{{"line_item_id": "oli_ring", "quantity": int64(1)}}
	body := campaignsRequest(returnsPanel(t, catalog, &fakeReturnParcels{}), http.MethodGet,
		OrdersPath+"/order_1", nil, all...).Body.String()
	assert.NotContains(t, body, returnParcelAction, "its one ring is in a live parcel")

	body = campaignsRequest(returnsPanel(t, returnsCatalog(), &fakeReturnParcels{options: "[]"}), http.MethodGet,
		OrdersPath+"/order_1", nil, all...).Body.String()
	assert.Contains(t, body, "offers no return option")
	assert.NotContains(t, body, returnParcelAction)

	body = campaignsRequest(returnsPanel(t, returnsCatalog(), &fakeReturnParcels{optionsErr: errors.Unavailable("x", "down")}),
		http.MethodGet, OrdersPath+"/order_1", nil, all...).Body.String()
	assert.Contains(t, body, "The return options could not be read")
	assert.NotContains(t, body, returnParcelAction)
}

// TestAReturnParcelIsOpenedOnce: the form posts its key, option and units; a
// second press is told nothing new was opened; a form naming no option or no
// unit opens nothing, and the module's refusal is drawn on the order.
func TestAReturnParcelIsOpenedOnce(t *testing.T) {
	t.Parallel()

	parcels := &fakeReturnParcels{}
	panel := returnsPanel(t, returnsCatalog(), parcels)
	all := []string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead, scopeFulfillmentWrite}
	path := OrdersPath + "/order_1/returns/ret_A/parcels"
	form := url.Values{formParcelKey: {"panel-k"}, formReturnOption: {"sopt_back"}, "units_oli_ring": {"2"}}

	rec := campaignsRequest(panel, http.MethodPost, path, form, all...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Parcel ful_back_new was opened to bring the return back.")
	assert.Equal(t, []string{"order_1|ret_A|sopt_back|panel-k|oli_ring|[2]"}, parcels.opens)

	parcels.already = true
	rec = campaignsRequest(panel, http.MethodPost, path, form, all...)
	assert.Contains(t, rec.Body.String(), "This form had already opened parcel ful_back_new; nothing new was opened.")

	for _, missing := range []string{formReturnOption, "units_oli_ring", formParcelKey} {
		partial := url.Values{}
		for field, values := range form {
			if field != missing {
				partial[field] = values
			}
		}
		rec = campaignsRequest(panel, http.MethodPost, path, partial, all...)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "without %s", missing)
	}
	assert.Len(t, parcels.opens, 2, "a form missing a part opens nothing")

	parcels.openErr = errors.Conflict("fulfillment_return_not_awaited", "return ret_A of order order_1 awaits no goods")
	rec = campaignsRequest(panel, http.MethodPost, path, form, all...)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "awaits no goods")

	rec = campaignsRequest(panel, http.MethodPost, path, form, scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead)
	assert.Equal(t, http.StatusForbidden, rec.Code, "the open is the fulfillment module's write")
}

// TestAMoveRefusesAParcelThatIsNotTheOrders is gap D266: the move names a
// parcel by its id, and the page in its path is the order's. The order's own
// parcel and a return's parcel carry the order as their reference and are
// moved; another order's is refused and not moved; a parcel the order joined as
// an addition is the order's through its link, which an operator who may read
// the order is asked, and one who may not is refused.
func TestAMoveRefusesAParcelThatIsNotTheOrders(t *testing.T) {
	t.Parallel()

	parcels := &fakeReturnParcels{}
	panel := returnsPanel(t, returnsCatalog(), parcels)
	all := []string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead, scopeFulfillmentWrite}
	move := func(parcel string, scopes ...string) int {
		return campaignsRequest(panel, http.MethodPost, OrdersPath+"/order_1/parcels/"+parcel+"/deliver",
			url.Values{}, scopes...).Code
	}

	rec := campaignsRequest(panel, http.MethodPost, OrdersPath+"/order_1/parcels/ful_other/deliver", url.Values{}, all...)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Parcel ful_other is not one of this order&#39;s; nothing was moved.")
	assert.Equal(t, http.StatusNotFound, move("ful_unknown", all...), "a parcel that does not exist")
	assert.Empty(t, parcels.moves, "neither was moved")

	assert.Equal(t, http.StatusOK, move("ful_out", all...))
	assert.Equal(t, http.StatusOK, move("ful_live", all...), "a return's parcel is the order's")
	assert.Equal(t, http.StatusOK, move("ful_joined", all...), "the order's through its link")
	assert.Equal(t, []string{"deliver|ful_out", "deliver|ful_live", "deliver|ful_joined"}, parcels.moves)

	assert.NotEqual(t, http.StatusNotFound, move("ful_out", scopeFulfillmentWrite),
		"a writer who cannot read the order still moves the order's own parcel")
	assert.Equal(t, http.StatusNotFound, move("ful_joined", scopeFulfillmentWrite),
		"the link is not read for a writer who cannot read the order")
}
