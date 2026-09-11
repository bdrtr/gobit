package cart

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheRuleContextCarriesTheHighestRankedGroup is ADR 0049's decision, checked
// where it takes effect.
//
// A customer in several groups gets ONE of them into the pricing rule context,
// and which one is the merchant's choice rather than the ladder's. The surface
// promises rank order, so the head of the slice is the answer — and taking
// anything else would hand the decision back to the pricing ladder, whose last
// usable rung compares AMOUNT and would give the customer the cheapest segment
// price regardless of what the merchant ranked.
func TestTheRuleContextCarriesTheHighestRankedGroup(t *testing.T) {
	t.Parallel()

	customers := &stubCustomers{groups: map[string][]string{
		// As the surface returns them: rank first. "wholesale" is the merchant's
		// choice; "vip" would be the ladder's if the amount happened to be lower.
		"cus_1": {"wholesale", "vip"},
	}}
	flows := &Workflows{customers: customers, log: slog.New(slog.DiscardHandler)}

	attributes, _, err := flows.ruleContext(context.Background(),
		Snapshot{CustomerID: "cus_1", RegionID: "reg_1"})

	require.NoError(t, err)
	assert.Equal(t, "wholesale", attributes[attrCustomerGroupID],
		"the HEAD of the ordered slice is the group the merchant ranked first")
	assert.Equal(t, "reg_1", attributes[attrRegionID],
		"the region the context already carried must survive")
	assert.Len(t, attributes, 2,
		"one group and one region in the SINGLE-VALUED context; the set lives beside it "+
			"since ADR 0144 and is asserted in its own test")
}

// TestAGuestCartOmitsTheGroupAttributeEntirely is the default direction.
//
// A cart with no customer has no segment. The attribute is left OUT rather than
// sent empty, because the elimination rule treats a missing attribute as a
// non-match — so a segment price stays CLOSED. Sending "" would instead ask every
// rule whether its value list contains the empty string, which is a value a
// merchant could configure by accident.
func TestAGuestCartOmitsTheGroupAttributeEntirely(t *testing.T) {
	t.Parallel()

	flows := &Workflows{customers: &stubCustomers{}, log: slog.New(slog.DiscardHandler)}

	attributes, _, err := flows.ruleContext(context.Background(), Snapshot{RegionID: "reg_1"})

	require.NoError(t, err)
	assert.NotContains(t, attributes, attrCustomerGroupID,
		"a guest has no segment, and an empty one is not the same as none")
}

// TestACustomerInNoGroupAlsoOmitsIt is the other half of the same rule.
//
// A signed-in customer who belongs to nothing must look like a guest to the rule
// engines, not like a customer in a group called "".
func TestACustomerInNoGroupAlsoOmitsIt(t *testing.T) {
	t.Parallel()

	flows := &Workflows{customers: &stubCustomers{groups: map[string][]string{}}, log: slog.New(slog.DiscardHandler)}

	attributes, _, err := flows.ruleContext(context.Background(),
		Snapshot{CustomerID: "cus_1", RegionID: "reg_1"})

	require.NoError(t, err)
	assert.NotContains(t, attributes, attrCustomerGroupID)
}

// TestAFailedGroupReadStillPricesTheCart is the availability decision.
//
// A cart total that cannot be computed because the customer module is briefly
// unavailable is worse than one computed at the base price: the first stops the
// shop, the second charges the ordinary price. The error comes back so the caller
// can say so in the log, and the region survives in the context.
func TestAFailedGroupReadStillPricesTheCart(t *testing.T) {
	t.Parallel()

	customers := &stubCustomers{groupErr: errors.New("customer module unavailable")}
	flows := &Workflows{customers: customers, log: slog.New(slog.DiscardHandler)}

	attributes, _, err := flows.ruleContext(context.Background(),
		Snapshot{CustomerID: "cus_1", RegionID: "reg_1"})

	require.Error(t, err, "the caller has to be able to log that the segment was not applied")
	assert.Equal(t, "reg_1", attributes[attrRegionID],
		"the cart is still priced; only the segment is missing")
	assert.NotContains(t, attributes, attrCustomerGroupID)
}

// TestEVERYGroupReachesTheWireAndTheHeadStaysWhereItWas is the witness the
// producer feeds the consumer.
//
// The operator, the migration and the schema field can all ship while ruleContext
// still sends one group — a ninth operator no request could ever satisfy, passing
// every gate because no gate asks whether the producer feeds the consumer. So the
// subject of this test is the CART FLOW and not the promotion engine (ADR 0144).
func TestEVERYGroupReachesTheWireAndTheHeadStaysWhereItWas(t *testing.T) {
	t.Parallel()

	flows := &Workflows{
		customers: &stubCustomers{groups: map[string][]string{"cus_1": {"retail", "vip"}}},
		log:       slog.New(slog.DiscardHandler),
	}

	attributes, lists, err := flows.ruleContext(context.Background(),
		Snapshot{RegionID: "reg_1", CustomerID: "cus_1"})
	require.NoError(t, err)

	assert.Equal(t, "retail", attributes[attrCustomerGroupID],
		"the single-valued context keeps the merchant-ranked HEAD, because every rule "+
			"shipped before this read it there and `eq retail` has to keep its answer")
	assert.Equal(t, []string{"retail", "vip"}, lists[attrCustomerGroupID],
		"and the list carries ALL of them, which is the only thing that lets a rule "+
			"written for vip reach a customer whose head is retail")
}

// TestACustomerWithNoGroupsSendsNoList keeps the two absences apart.
//
// An empty list and a missing one are the same answer to the matcher — no match —
// and sending an empty one would put a key in the wire that means nothing.
func TestACustomerWithNoGroupsSendsNoList(t *testing.T) {
	t.Parallel()

	flows := &Workflows{customers: &stubCustomers{}, log: slog.New(slog.DiscardHandler)}

	attributes, lists, err := flows.ruleContext(context.Background(),
		Snapshot{RegionID: "reg_1", CustomerID: "cus_1"})
	require.NoError(t, err)

	assert.NotContains(t, attributes, attrCustomerGroupID)
	assert.Empty(t, lists)
}
