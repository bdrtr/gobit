package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// TestAChannelOnlyPriceTiesWithTheRegions pins the cost ADR 0397 declares: a
// channel's price has no rank of its own, so one ruled on the channel alone is
// one matched rule against the region's one, and the cheaper wins; one ruled
// on the region AND the channel is the more specific and wins though dearer.
//
// The channel's price is the dearer one and has the smaller id, so neither the
// amount nor the id can make it win by accident.
func TestAChannelOnlyPriceTiesWithTheRegions(t *testing.T) {
	attrs := map[string]string{"region_id": "reg_1", models.AttrSalesChannelID: "sc_A"}
	region := withRules(basePrice("price_b", "TRY", 800, 1, nil), rule("region_id", models.OpEq, "reg_1"))

	channelOnly := withRules(basePrice("price_a", "TRY", 1000, 1, nil),
		rule(models.AttrSalesChannelID, models.OpEq, "sc_A"))
	got, ok := selectPrice([]models.PriceCandidate{channelOnly, region}, "TRY", 1, attrs, testNow)
	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "a tie on the rule count goes to the cheaper price")

	both := withRules(basePrice("price_a", "TRY", 1000, 1, nil),
		rule("region_id", models.OpEq, "reg_1"), rule(models.AttrSalesChannelID, models.OpEq, "sc_A"))
	got, ok = selectPrice([]models.PriceCandidate{both, region}, "TRY", 1, attrs, testNow)
	require.True(t, ok)
	assert.Equal(t, "price_a", got.PriceID, "naming the region too makes the channel's price the specific one")
}
