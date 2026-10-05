package cart

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// A cart is priced in the sales channel it was opened in (ADR 0397).

// TestTheRuleContextCarriesTheCartsSalesChannel holds the attribute where the
// rule engines read it: a guest's cart and a customer's carry the channel the
// cart names, and a cart that names none carries no attribute at all, so a
// channel price stays closed rather than matching "". A cart in none written
// under a key bound to one channel stays in none: the price follows the cart,
// not the request.
func TestTheRuleContextCarriesTheCartsSalesChannel(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		ctx  context.Context
		snap Snapshot
		want string
	}{
		"a guest's cart":    {snap: Snapshot{RegionID: "reg_1", SalesChannelID: "sc_A"}, want: "sc_A"},
		"a customer's cart": {snap: Snapshot{RegionID: "reg_1", CustomerID: "cus_1", SalesChannelID: "sc_A"}, want: "sc_A"},
		"a cart in none":    {snap: Snapshot{RegionID: "reg_1", CustomerID: "cus_1"}},
		"a cart in none, written under a key bound to one": {
			ctx: withChannels([]string{"sc_B"}), snap: Snapshot{RegionID: "reg_1"},
		},
	} {
		flows := &Workflows{customers: &stubCustomers{}, log: slog.New(slog.DiscardHandler)}
		ctx := tc.ctx
		if ctx == nil {
			ctx = context.Background()
		}

		attributes, _, err := flows.ruleContext(ctx, tc.snap)

		require.NoError(t, err, name)
		if tc.want == "" {
			assert.NotContains(t, attributes, AttrSalesChannelID, "%s: no channel is no attribute", name)
			continue
		}
		assert.Equal(t, tc.want, attributes[AttrSalesChannelID], name)
		assert.Equal(t, "reg_1", attributes[attrRegionID], "%s: the region still stands", name)
	}
}

// withChannels is a request whose identity holds the given channels; nil is
// a principal holding none.
func withChannels(channels []string) context.Context {
	return corehttp.WithPrincipal(context.Background(), corehttp.Principal{
		ID: "pk_1", Kind: "publishable_key", SalesChannelIDs: channels,
	})
}

// TestACartIsOpenedInTheOneChannelItsRequestHolds holds [pricingChannel]: the
// cart records the principal's channel only when it holds exactly one, and the
// channel travels in its own argument, not in the operator's.
func TestACartIsOpenedInTheOneChannelItsRequestHolds(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		ctx  context.Context
		want string
	}{
		"no principal":          {ctx: context.Background()},
		"a principal with none": {ctx: withChannels([]string{})},
		"one channel":           {ctx: withChannels([]string{"sc_A"}), want: "sc_A"},
		"two channels":          {ctx: withChannels([]string{"sc_A", "sc_B"})},
	} {
		h := newHarness(t)
		recordOpenCart(h.carts, testCartID)

		out, err := h.wf.CreateCart(tc.ctx, CreateCartInput{CountryCode: "TR", OpenedBy: "usr_operator"})

		require.NoError(t, err, name)
		assert.Equal(t, tc.want, h.carts.openedChannel, "%s: the channel the cart is opened in", name)
		assert.Equal(t, tc.want, out.SalesChannelID, name)
		assert.Equal(t, "usr_operator", h.carts.openedBy, "%s: the operator stays in its own argument", name)
	}
}

// TestTheCartsChannelPricesItWhoeverWrites holds the price to the CART's
// channel: a round run under another storefront's key, or under an operator
// who holds no channel, asks the price in the channel the cart was opened in.
func TestTheCartsChannelPricesItWhoeverWrites(t *testing.T) {
	t.Parallel()

	for name, ctx := range map[string]context.Context{
		"another storefront's key": withChannels([]string{"sc_B"}),
		"an operator with none": corehttp.WithPrincipal(context.Background(), corehttp.Principal{
			ID: "usr_operator", Kind: "user", Scopes: []string{"cart:write"},
		}),
	} {
		h := newHarness(t)
		h.carts.snapshotFn = func(_ context.Context, _ string) (json.RawMessage, error) {
			snap := snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil)
			snap.SalesChannelID = "sc_A"

			return json.Marshal(snap)
		}

		_, err := h.wf.CalculateTotals(ctx, testCartID)

		require.NoError(t, err, name)
		require.NotEmpty(t, h.prices.requests, name)
		assert.Equal(t, "sc_A", h.prices.requests[0].Attributes[AttrSalesChannelID],
			"%s: the price is asked in the cart's channel, not the request's", name)
	}
}

// TestTheSnapshotReadsTheChannelUnderTheProducersName decodes the field as the
// cart module writes it. The producer's spelling is typed here by hand, so a
// consumer tag that drifted from it decodes to "" and the test says so.
func TestTheSnapshotReadsTheChannelUnderTheProducersName(t *testing.T) {
	t.Parallel()

	snap, err := decodeSnapshot(testCartID, json.RawMessage(`{"id":"`+testCartID+`","region_id":"`+
		testRegionID+`","currency_code":"`+testCurrency+`","sales_channel_id":"sc_A","revision":1,`+
		`"items":[],"shipping_methods":[],"promotion_codes":[]}`))

	require.NoError(t, err)
	assert.Equal(t, "sc_A", snap.SalesChannelID)
}

// TestTheDiscountRoundAsksInTheCartsChannel holds the promotion half: the
// discount request carries the channel the cart was opened in, so a promotion
// ruled on `sales_channel_id` meets it.
func TestTheDiscountRoundAsksInTheCartsChannel(t *testing.T) {
	h := newModuleHarness(t)
	h.carts.snapshotFn = func(_ context.Context, cartID string) (json.RawMessage, error) {
		snap := snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil)
		snap.ID = cartID
		snap.SalesChannelID = "sc_A"

		return json.Marshal(snap)
	}

	attributes := contextOf(t, h)

	assert.Equal(t, "sc_A", attributes[AttrSalesChannelID])
}
