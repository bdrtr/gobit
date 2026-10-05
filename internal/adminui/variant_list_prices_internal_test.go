package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeListPrices lists price lists and a set's list prices as scripted, and
// records each write.
type fakeListPrices struct {
	fakePriceLists
	listPrices string
	readErr    error
	added      []string
	removed    []string
	listErr    error
}

func (f *fakeListPrices) ListPricesJSON(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(f.listPrices), f.readErr
}

func (f *fakeListPrices) AddListPrice(_ context.Context, setID, listID, currency string, amount int64, groups []string) error {
	f.added = append(f.added, fmt.Sprintf("%s|%s|%s|%d|%s", setID, listID, currency, amount, strings.Join(groups, ",")))
	return f.listErr
}

func (f *fakeListPrices) RemoveListPrice(_ context.Context, setID, priceID string) error {
	f.removed = append(f.removed, setID+"|"+priceID)
	return f.listErr
}

// wholesalePrice is a price on the wholesale list for the trade group, and
// one on the sale list for everybody.
const wholesalePrice = `[
	{"id":"price_w","price_list_id":"plist_1","price_list_title":"Wholesale 2026","currency_code":"TRY",
	 "amount":15000,"min_quantity":1,"max_quantity":null,
	 "rules":[{"attribute":"customer_group_id","operator":"in","values":["custgrp_trade"]}]},
	{"id":"price_s","price_list_id":"plist_2","price_list_title":"","currency_code":"TRY",
	 "amount":18000,"min_quantity":1,"max_quantity":null,"rules":[]}]`

// listPricesPanel is a variant page over the price set, with the shop's
// groups readable.
func listPricesPanel(t *testing.T, prices PriceWriter) (*UI, *fakeCatalog) {
	t.Helper()

	catalog := variantCatalog(int64(2))
	catalog.byEntity[EntityVariant][0][keyPriceSet] = query.Record{"id": "pset_1", "prices": []map[string]any{
		{"id": "price_1", "currency_code": "TRY", "amount": int64(19990)},
		{"id": "price_s", "currency_code": "TRY", "amount": int64(18000), "price_list_id": "plist_2"},
	}}
	catalog.byEntity[EntityCustomerGroup] = []query.Record{{fieldID: "custgrp_trade", fieldName: "Trade"}}
	panel := newCatalogPanel(t, catalog)
	panel.prices = prices
	panel.scopes = builtInScopes()

	return panel, catalog
}

// TestAVariantListsItsPricesOnLists is ADR 0327: the set's list prices with
// their list, amount and customers, the groups named for an operator who may
// read the customers; the expansion's list price is not printed twice; a
// writer is offered the lists, the currencies and the groups, and a remove on
// each; a reader is offered nothing.
func TestAVariantListsItsPricesOnLists(t *testing.T) {
	t.Parallel()

	prices := &fakeListPrices{fakePriceLists: fakePriceLists{body: twoLists, total: 2}, listPrices: wholesalePrice}
	panel, _ := listPricesPanel(t, prices)
	page := variantURLFor()
	writer := []string{scopeProductRead, scopePricingRead, scopePricingWrite, scopeCustomerRead}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "<td>Wholesale 2026</td><td>150.00 TRY</td><td>1 or more, for Trade</td>")
	assert.Contains(t, body, "<td>plist_2</td><td>180.00 TRY</td><td>1 or more, for every customer</td>",
		"a list with no title is named by its id")
	assert.NotContains(t, body, "on price list plist_2", "the expansion's list price is not printed twice")
	assert.Contains(t, body, `action="`+page+`/list-prices/price_w/remove"`)
	_, form, found := strings.Cut(body, `<form method="post" action="`+page+`/list-prices">`)
	require.True(t, found, "the form that adds a price on a list")
	form, _, _ = strings.Cut(form, "</form>")
	assert.Contains(t, form, `<input type="hidden" name="price_set_id" value="pset_1">`, "the form names the variant's set")
	assert.Contains(t, body, `<option value="plist_1">Wholesale 2026 (override, active)</option>`)
	assert.Contains(t, body, `<option value="TRY">TRY</option>`)
	assert.Contains(t, body, `<option value="custgrp_trade">Trade</option>`)

	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeProductRead, scopePricingRead)
	body = rec.Body.String()
	assert.Contains(t, body, "1 or more, for custgrp_trade", "the group by its id to an operator who may not read the customers")
	assert.NotContains(t, body, "/list-prices\"", "a reader adds nothing")
	assert.NotContains(t, body, "/remove\"", "a reader removes nothing")

	prices.readErr = errors.Unavailable("db_down", "no")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, "the variant stands when its list prices cannot be read")
	assert.Contains(t, rec.Body.String(), "The variant's prices on price lists could not be read.")
	assert.Contains(t, rec.Body.String(), "on price list plist_2", "the expansion's list price is kept then")
}

// TestAListPriceRuledOnTheCartsBagSaysItMatchesNoCart is ADR 0403 on the
// variant page: a list price whose rule names a `cart.` attribute was written
// before pricing refused one, and the page says it matches no cart instead of
// printing it as a working condition; a rule on another attribute is printed
// as it is.
func TestAListPriceRuledOnTheCartsBagSaysItMatchesNoCart(t *testing.T) {
	t.Parallel()

	prices := &fakeListPrices{fakePriceLists: fakePriceLists{body: twoLists, total: 2}, listPrices: `[
	{"id":"price_arm","price_list_id":"plist_1","price_list_title":"Wholesale 2026","currency_code":"TRY",
	 "amount":15000,"min_quantity":1,"max_quantity":null,
	 "rules":[{"attribute":"cart.arm","operator":"eq","values":["B"]}]},
	{"id":"price_tier","price_list_id":"plist_2","price_list_title":"","currency_code":"TRY",
	 "amount":18000,"min_quantity":1,"max_quantity":null,
	 "rules":[{"attribute":"region_id","operator":"eq","values":["reg_1"]}]}]`}
	panel, _ := listPricesPanel(t, prices)

	rec := campaignsRequest(panel, http.MethodGet, variantURLFor(), nil, scopeProductRead, scopePricingRead)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "<td>1 or more, cart.arm eq B (matches no cart, ADR 0403)</td>")
	assert.Contains(t, body, "<td>1 or more, region_id eq reg_1</td>")
}

// TestAPriceIsPutOnAListFromTheVariant: the form's list, currency, amount in
// the currency's decimals and groups reach the surface with the set the form
// names, and the variant comes back; with no group the price is for every
// customer; a refusal is printed on the page; a price is removed.
func TestAPriceIsPutOnAListFromTheVariant(t *testing.T) {
	t.Parallel()

	prices := &fakeListPrices{fakePriceLists: fakePriceLists{body: twoLists, total: 2}, listPrices: wholesalePrice}
	panel, _ := listPricesPanel(t, prices)
	page := variantURLFor()
	writer := []string{scopeProductRead, scopePricingRead, scopePricingWrite}

	rec := campaignsRequest(panel, http.MethodPost, page+"/list-prices", url.Values{
		formPriceSetID: {"pset_1"}, formListPriceList: {" plist_1 "}, formListPriceCurrency: {" try "},
		formListPriceAmount: {"149.90"}, formListPriceGroup: {" custgrp_trade ", ""},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, page, rec.Header().Get("Location"))
	assert.Equal(t, []string{"pset_1|plist_1|TRY|14990|custgrp_trade"}, prices.added)

	campaignsRequest(panel, http.MethodPost, page+"/list-prices", url.Values{
		formPriceSetID: {"pset_1"}, formListPriceList: {"plist_2"}, formListPriceCurrency: {"TRY"}, formListPriceAmount: {"170"},
	}, writer...)
	assert.Equal(t, "pset_1|plist_2|TRY|17000|", prices.added[1], "no group, every customer")

	rec = campaignsRequest(panel, http.MethodPost, page+"/list-prices/price_w/remove", url.Values{formPriceSetID: {"pset_1"}}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"pset_1|price_w"}, prices.removed)

	prices.listErr = errors.Conflict("pricing_list_price_taken", "the variant already has a price on price list plist_1 in TRY for those customers")
	rec = campaignsRequest(panel, http.MethodPost, page+"/list-prices", url.Values{
		formPriceSetID: {"pset_1"}, formListPriceList: {"plist_1"}, formListPriceCurrency: {"TRY"}, formListPriceAmount: {"1"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "already has a price on price list plist_1")
	prices.listErr = errors.NotFound("pricing_price_not_found", "price price_x is not on price set pset_1")
	rec = campaignsRequest(panel, http.MethodPost, page+"/list-prices/price_x/remove", url.Values{formPriceSetID: {"pset_1"}}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "price price_x is not on price set pset_1")

	prices.listErr = nil
	sent := len(prices.added)
	rec = campaignsRequest(panel, http.MethodPost, page+"/list-prices", url.Values{
		formPriceSetID: {"pset_1"}, formListPriceList: {"plist_1"}, formListPriceCurrency: {"TRY"}, formListPriceAmount: {"lots"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "an amount the panel cannot read")
	assert.Len(t, prices.added, sent, "an amount the panel cannot read is not sent")

	prices.listErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/list-prices/price_w/remove", url.Values{formPriceSetID: {"pset_1"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
