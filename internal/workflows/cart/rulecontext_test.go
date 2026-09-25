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
	assert.Len(t, attributes, 3,
		"one group, one region and the customer in the SINGLE-VALUED context, and no "+
			"company without the b2b surface; the set lives beside it since ADR 0144 and is "+
			"asserted in its own test")
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

// stubCompanies scripts the b2b surface: the company each customer buys for.
type stubCompanies struct {
	companies map[string]string
	err       error
	calls     int
}

// CompanyOfCustomer returns the scripted company, "" for nobody's employee.
func (s *stubCompanies) CompanyOfCustomer(_ context.Context, customerID string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}

	return s.companies[customerID], nil
}

// TestTheRuleContextNamesTheBuyer is ADR 0185's half in the cart: the customer's
// own id and the company they buy for go into the context, so a price ruled on
// either can match.
func TestTheRuleContextNamesTheBuyer(t *testing.T) {
	t.Parallel()

	flows := &Workflows{
		customers: &stubCustomers{},
		companies: &stubCompanies{companies: map[string]string{"cus_1": "comp_1"}},
		log:       slog.New(slog.DiscardHandler),
	}

	attributes, _, err := flows.ruleContext(context.Background(),
		Snapshot{CustomerID: "cus_1", RegionID: "reg_1"})

	require.NoError(t, err)
	assert.Equal(t, map[string]string{attrRegionID: "reg_1", AttrCustomerID: "cus_1", AttrCompanyID: "comp_1"},
		attributes)
}

// TestOnlyAnEmployeeCarriesACompany omits the company for a customer who is no
// company's employee and for an installation without the b2b surface, for the
// reason a guest omits the group: a missing attribute keeps a company price
// closed, and an empty one would ask every rule whether it lists "".
func TestOnlyAnEmployeeCarriesACompany(t *testing.T) {
	t.Parallel()

	for name, companies := range map[string]Companies{
		"nobody's employee": &stubCompanies{companies: map[string]string{}},
		"no b2b surface":    nil,
	} {
		flows := &Workflows{customers: &stubCustomers{}, companies: companies,
			log: slog.New(slog.DiscardHandler)}

		attributes, _, err := flows.ruleContext(context.Background(),
			Snapshot{CustomerID: "cus_1", RegionID: "reg_1"})

		require.NoError(t, err, name)
		assert.NotContains(t, attributes, AttrCompanyID, name)
		assert.Equal(t, "cus_1", attributes[AttrCustomerID], name)
	}
}

// TestAGuestNamesNobody keeps a contract price closed to a cart with no
// customer, and does not ask the b2b module about nobody.
func TestAGuestNamesNobody(t *testing.T) {
	t.Parallel()

	companies := &stubCompanies{companies: map[string]string{"": "comp_1"}}
	flows := &Workflows{customers: &stubCustomers{}, companies: companies,
		log: slog.New(slog.DiscardHandler)}

	attributes, _, err := flows.ruleContext(context.Background(), Snapshot{RegionID: "reg_1"})

	require.NoError(t, err)
	assert.Equal(t, map[string]string{attrRegionID: "reg_1"}, attributes)
	assert.Zero(t, companies.calls, "a guest has no company to look up")
}

// TestAnUnreadableCompanyStillPricesTheCart is the availability decision for the
// company: the error comes back for the caller to log, and everything that WAS
// read — the customer and the groups — is still in the context.
func TestAnUnreadableCompanyStillPricesTheCart(t *testing.T) {
	t.Parallel()

	failure := errors.New("b2b is down")
	flows := &Workflows{
		customers: &stubCustomers{groups: map[string][]string{"cus_1": {"wholesale"}}},
		companies: &stubCompanies{err: failure},
		log:       slog.New(slog.DiscardHandler),
	}

	attributes, lists, err := flows.ruleContext(context.Background(),
		Snapshot{CustomerID: "cus_1", RegionID: "reg_1"})

	require.ErrorIs(t, err, failure)
	assert.NotContains(t, attributes, AttrCompanyID)
	assert.Equal(t, "cus_1", attributes[AttrCustomerID])
	assert.Equal(t, "wholesale", attributes[attrCustomerGroupID], "the groups were read and stay")
	assert.Equal(t, []string{"wholesale"}, lists[attrCustomerGroupID])
}
