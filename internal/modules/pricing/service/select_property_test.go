package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// drawPriceCandidates draws the prices one variant can carry, from small pools
// so that two prices tie on every criterion but their id as an ordinary draw:
// the currency, the quantity band, the amount, buyer rules and a list that may
// be a sale or an override, active, a draft, or outside its window.
func drawPriceCandidates(t *rapid.T, at time.Time) []models.PriceCandidate {
	var out []models.PriceCandidate
	for i := range rapid.IntRange(0, 6).Draw(t, "prices") {
		price := models.Price{
			ID: fmt.Sprintf("price_%02d", i), PriceSetID: "pset_1",
			CurrencyCode: rapid.SampledFrom([]string{"TRY", "TRY", "TRY", "EUR"}).Draw(t, "currency"),
			Amount:       rapid.SampledFrom([]int64{90, 100, 100, 110}).Draw(t, "amount"),
			MinQuantity:  rapid.SampledFrom([]int32{1, 1, 2, 5}).Draw(t, "min quantity"),
		}
		if rapid.Bool().Draw(t, "a max quantity") {
			limit := rapid.SampledFrom([]int32{1, 3, 10}).Draw(t, "max quantity")
			price.MaxQuantity = &limit
		}
		for j := range rapid.IntRange(0, 2).Draw(t, "rules") {
			price.Rules = append(price.Rules, models.PriceRule{
				ID: fmt.Sprintf("rule_%d_%d", i, j), PriceID: price.ID,
				Attribute: rapid.SampledFrom([]string{models.AttrCustomerID, models.AttrCompanyID, "region_id"}).Draw(t, "attribute"),
				Operator:  rapid.SampledFrom([]models.RuleOperator{models.OpEq, models.OpIn}).Draw(t, "operator"),
				Values:    []string{rapid.SampledFrom([]string{"a", "b"}).Draw(t, "value")},
			})
		}
		candidate := models.PriceCandidate{Price: price}
		if rapid.Bool().Draw(t, "on a list") {
			listID := fmt.Sprintf("plist_%d", rapid.IntRange(0, 1).Draw(t, "list"))
			candidate.Price.PriceListID = &listID
			list := models.PriceListInfo{
				ID:     listID,
				Type:   rapid.SampledFrom([]models.PriceListType{models.PriceListSale, models.PriceListOverride}).Draw(t, "list type"),
				Status: rapid.SampledFrom([]models.PriceListStatus{models.PriceListActive, models.PriceListActive, models.PriceListDraft}).Draw(t, "list status"),
			}
			if rapid.IntRange(0, 3).Draw(t, "a window") == 0 {
				ends := at.Add(time.Duration(rapid.IntRange(-2, 2).Draw(t, "ends in hours")) * time.Hour)
				list.EndsAt = &ends
			}
			candidate.List = &list
		}
		out = append(out, candidate)
	}
	// A second price that says what another says, but for its id, is the one
	// case only the id can decide. Drawn on its own it tied in under one draw
	// of a hundred, and the tie-break could be deleted with the property
	// passing; it is now a shape of its own.
	if len(out) > 0 && rapid.Bool().Draw(t, "a price said twice") {
		twin := out[rapid.IntRange(0, len(out)-1).Draw(t, "said again")]
		twin.Price.ID = fmt.Sprintf("price_%02d", len(out))
		out = append(out, twin)
	}

	return out
}

// TestThePriceChosenDoesNotDependOnTheOrderOfThePrices is ADR 0249 on price
// selection: whatever order a variant's prices are read in, the same price is
// chosen, it is one the quantity, the currency, the buyer and the list's window
// admit, no admitted price ranks above it, its total is its amount times the
// quantity, and none is chosen when none is admitted.
func TestThePriceChosenDoesNotDependOnTheOrderOfThePrices(t *testing.T) {
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	rapid.Check(t, func(t *rapid.T) {
		candidates := drawPriceCandidates(t, at)
		quantity := rapid.Int32Range(1, 12).Draw(t, "quantity")
		attributes := map[string]string{
			models.AttrCustomerID: rapid.SampledFrom([]string{"a", "b"}).Draw(t, "customer"),
			models.AttrCompanyID:  rapid.SampledFrom([]string{"a", "b"}).Draw(t, "company"),
			"region_id":           "a",
		}

		chosen, found := selectPrice(candidates, "TRY", quantity, attributes, at)

		var admitted []models.PriceCandidate
		for i := range candidates {
			if eligible(candidates[i], "TRY", quantity, attributes, at) {
				admitted = append(admitted, candidates[i])
			}
		}
		require.Equal(t, len(admitted) > 0, found)
		if found {
			var winner *models.PriceCandidate
			for i := range admitted {
				if admitted[i].Price.ID == chosen.PriceID {
					winner = &admitted[i]
				}
			}
			require.NotNil(t, winner, "the chosen price is an admitted one")
			for i := range admitted {
				require.False(t, better(score(admitted[i]), score(*winner)),
					"%s ranks above the chosen %s", admitted[i].Price.ID, chosen.PriceID)
			}
			require.Equal(t, winner.Price.Amount*int64(quantity), chosen.Total)
			require.Equal(t, quantity, chosen.Quantity)
		}

		for _, order := range [][]models.PriceCandidate{rapid.Permutation(candidates).Draw(t, "prices reordered"), reversedPrices(candidates)} {
			again, foundAgain := selectPrice(order, "TRY", quantity, attributes, at)
			require.Equal(t, found, foundAgain)
			require.Equal(t, chosen, again, "the order the prices are read in changes nothing")
		}
	})
}

// reversedPrices is the candidates the other way round, which moves every pair.
func reversedPrices(candidates []models.PriceCandidate) []models.PriceCandidate {
	out := make([]models.PriceCandidate, len(candidates))
	for i := range candidates {
		out[len(candidates)-1-i] = candidates[i]
	}
	return out
}
