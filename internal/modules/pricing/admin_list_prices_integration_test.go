//go:build integration

package pricing_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// TestThePanelPutsAVariantsPriceOnAList is ADR 0327 against a real
// PostgreSQL: a price on an override list limited to a group is added beside
// the base price, which is kept; the computation charges a customer whose
// group ranking first is that group the list's price and anybody else the
// base price; the set's list prices are listed with their list's title and
// rule; the same list price twice, a list that is not there and removing the
// base price are refused; and the list price is removed again.
func TestThePanelPutsAVariantsPriceOnAList(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := service.NewAdminSurface(svc)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 10_000, MinQuantity: 1}})
	require.NoError(t, err)
	list, err := surface.CreatePriceList(ctx, "Wholesale", "", "override", "active", nil, nil)
	require.NoError(t, err)

	require.NoError(t, surface.AddListPrice(ctx, set.ID, list, "try", 7_500, []string{"custgrp_trade"}))
	price := func(group string) int64 {
		t.Helper()
		attributes := map[string]string{}
		if group != "" {
			attributes[models.AttrCustomerGroupID] = group
		}
		calculated, err := svc.CalculatePrice(ctx, set.ID, service.CalculateParams{
			CurrencyCode: "TRY", Quantity: 1, Attributes: attributes,
		})
		require.NoError(t, err)
		return calculated.Amount
	}
	assert.Equal(t, int64(7_500), price("custgrp_trade"), "the group's customer pays the list's price")
	assert.Equal(t, int64(10_000), price("custgrp_retail"), "another group pays the base price")
	assert.Equal(t, int64(10_000), price(""), "a guest pays the base price")

	raw, err := surface.ListPricesJSON(ctx, set.ID)
	require.NoError(t, err)
	var rows []struct {
		ID        string `json:"id"`
		ListID    string `json:"price_list_id"`
		ListTitle string `json:"price_list_title"`
		Currency  string `json:"currency_code"`
		Amount    int64  `json:"amount"`
		Rules     []struct {
			Attribute string   `json:"attribute"`
			Operator  string   `json:"operator"`
			Values    []string `json:"values"`
		} `json:"rules"`
	}
	require.NoError(t, json.Unmarshal(raw, &rows))
	require.Len(t, rows, 1, "the list price alone; the base price is the page's own")
	row := rows[0]
	assert.Equal(t, list+"|Wholesale|TRY|7500",
		row.ListID+"|"+row.ListTitle+"|"+row.Currency+"|"+strconv.FormatInt(row.Amount, 10))
	require.Len(t, row.Rules, 1)
	assert.Equal(t, models.AttrCustomerGroupID+"|in", row.Rules[0].Attribute+"|"+row.Rules[0].Operator)
	assert.Equal(t, []string{"custgrp_trade"}, row.Rules[0].Values)

	err = surface.AddListPrice(ctx, set.ID, list, "TRY", 7_000, []string{"custgrp_trade"})
	require.Error(t, err)
	assert.Equal(t, service.CodeListPriceTaken, errors.CodeOf(err), "the same list price twice: %v", err)
	err = surface.AddListPrice(ctx, set.ID, "plist_missing", "TRY", 7_000, nil)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a list that is not there: %v", err)

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	var base string
	for _, p := range prices {
		if p.PriceListID == nil {
			base = p.ID
		}
	}
	require.NotEmpty(t, base, "the base price is kept")
	err = surface.RemoveListPrice(ctx, set.ID, base)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a base price is not removed here: %v", err)

	require.NoError(t, surface.RemoveListPrice(ctx, set.ID, row.ID))
	assert.Equal(t, int64(10_000), price("custgrp_trade"), "without the list price the group pays the base price")
	err = surface.RemoveListPrice(ctx, set.ID, row.ID)
	assert.True(t, errors.IsNotFound(err), "a price the set no longer holds: %v", err)
}
