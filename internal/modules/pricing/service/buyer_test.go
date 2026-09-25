package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// The buyer rung of the ladder (ADR 0185).
//
// Every candidate below is an OVERRIDE price, so list priority never decides,
// and the one that should win is always the MORE EXPENSIVE: the amount rung
// favors the loser, so a win can only come from the rung being tested.

// buyerContext is a customer of company comp_1 in the vip group, in region reg_1.
var buyerContext = map[string]string{
	"region_id":           "reg_1",
	"customer_group_id":   "vip",
	models.AttrCompanyID:  "comp_1",
	models.AttrCustomerID: "cus_1",
}

// override puts a candidate on an active override list of its own.
func override(id string, amount int64, rules ...models.PriceRule) models.PriceCandidate {
	return withList(withRules(basePrice(id, "TRY", amount, 1, nil), rules...),
		"plist_"+id, activeList("plist_"+id, models.PriceListOverride))
}

// TestTheBuyersOwnContractOutranksTheirCompanyAndTheirSegment is the rung's
// order: the customer's own price, then their company's, then anything else.
func TestTheBuyersOwnContractOutranksTheirCompanyAndTheirSegment(t *testing.T) {
	segment := override("price_segment", 800, rule("customer_group_id", models.OpEq, "vip"))
	company := override("price_company", 900, rule(models.AttrCompanyID, models.OpEq, "comp_1"))
	customer := override("price_customer", 1000, rule(models.AttrCustomerID, models.OpIn, "cus_1", "cus_9"))

	got, ok := selectPrice([]models.PriceCandidate{segment, company, customer}, "TRY", 1, buyerContext, testNow)
	require.True(t, ok)
	assert.Equal(t, "price_customer", got.PriceID, "the customer's own contract wins, at any amount")

	got, ok = selectPrice([]models.PriceCandidate{segment, company}, "TRY", 1, buyerContext, testNow)
	require.True(t, ok)
	assert.Equal(t, "price_company", got.PriceID, "the company's contract beats the segment price")
}

// TestTheBuyerOutranksTheRuleCount places the rung above specificity by count: a
// segment price with a region condition as well is still not a contract.
func TestTheBuyerOutranksTheRuleCount(t *testing.T) {
	segment := override("price_segment", 800,
		rule("customer_group_id", models.OpEq, "vip"), rule("region_id", models.OpEq, "reg_1"))
	company := override("price_company", 900, rule(models.AttrCompanyID, models.OpEq, "comp_1"))

	got, ok := selectPrice([]models.PriceCandidate{segment, company}, "TRY", 1, buyerContext, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_company", got.PriceID)
}

// TestListPriorityStillComesFirst places the rung BELOW list priority: a
// contract written on a campaign list does not beat an override, because the
// merchant's list type is the first thing the ladder reads.
func TestListPriorityStillComesFirst(t *testing.T) {
	segment := override("price_segment", 1000, rule("customer_group_id", models.OpEq, "vip"))
	contractOnSale := withList(withRules(basePrice("price_contract", "TRY", 800, 1, nil),
		rule(models.AttrCustomerID, models.OpEq, "cus_1")),
		"plist_sale", activeList("plist_sale", models.PriceListSale))

	got, ok := selectPrice([]models.PriceCandidate{segment, contractOnSale}, "TRY", 1, buyerContext, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_segment", got.PriceID)
}

// TestAnExclusionNamesNobody keeps ne and nin out of the rung: "everybody but
// cus_2" matches cus_1 and is still no contract with them, so the two-rule
// segment price wins on count.
func TestAnExclusionNamesNobody(t *testing.T) {
	segment := override("price_segment", 1000,
		rule("customer_group_id", models.OpEq, "vip"), rule("region_id", models.OpEq, "reg_1"))

	for _, exclusion := range []models.PriceRule{
		rule(models.AttrCustomerID, models.OpNe, "cus_2"),
		rule(models.AttrCompanyID, models.OpNin, "comp_2", "comp_3"),
	} {
		excluded := override("price_excluded", 800, exclusion)

		got, ok := selectPrice([]models.PriceCandidate{segment, excluded}, "TRY", 1, buyerContext, testNow)

		require.True(t, ok)
		assert.Equal(t, "price_segment", got.PriceID, "%s %s names nobody", exclusion.Attribute, exclusion.Operator)
	}
}

// TestAContractForSomebodyElseDoesNotMatch is elimination doing its part: the
// rung ranks only what matched, and another customer's contract never does.
func TestAContractForSomebodyElseDoesNotMatch(t *testing.T) {
	segment := override("price_segment", 1000, rule("customer_group_id", models.OpEq, "vip"))
	theirs := override("price_theirs", 500, rule(models.AttrCustomerID, models.OpEq, "cus_2"))

	got, ok := selectPrice([]models.PriceCandidate{segment, theirs}, "TRY", 1, buyerContext, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_segment", got.PriceID)
}
