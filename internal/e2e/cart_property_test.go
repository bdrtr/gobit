//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	returnswf "github.com/bdrtr/gobit/internal/workflows/returns"
)

// TestEveryCartAShopperCanBuildIsSoldAsQuoted is ADR 0249's property across
// the modules: every cart a shopper can build, in either market, has its totals
// written by the cart module, becomes an order the order module accepts, is
// charged its total, and is invoiced by a document the invoice module accepts.
//
// Nothing between the draws and the assertions is generated: the draws are a
// shopper's own choices — a market, variants from a pool, quantities — and
// every intermediate is produced by the real wiring, which is what ADR 0080
// found a generator would otherwise hide. D169 is the defect this would have
// caught: a tax-inclusive cart failed the cart module's own check.
//
// The prices are chosen so a 20% tax, taken out or added on, rounds every way,
// a 15% promotion on half the pool puts a discount beside either kind of tax,
// and the pool's last variant is a gift card, which carries no tax (ADR 0247).
// A shopper may also choose a paid or a free delivery in their market and type
// a 10% coupon that covers the other half of the pool. One variant is a gift
// box sold from two of the pool's variants (ADR 0234), and one takes an
// engraving as an add-on line of its own (ADR 0229), which the order writes
// under its line (ADR 0393).
func TestEveryCartAShopperCanBuildIsSoldAsQuoted(t *testing.T) {
	ctx := t.Context()

	prices := []int64{1, 7, 999, 11_999, 19_900, 123_457}
	pool := make([]string, len(prices))
	items := map[string]string{}
	for i, price := range prices {
		var item string
		pool[i], item = newStockedVariant(ctx, t, fmt.Sprintf("E2E Property Product %d", i),
			map[string]int64{taxedCurrency: price}, 1_000_000)
		items[pool[i]] = item
	}
	card := newGiftCardVariantStocked(ctx, t, 5_000, 1_000_000)
	pool = append(pool, card)
	box := newVariant(ctx, t, "E2E Property Gift Box", map[string]int64{taxedCurrency: 19_900})
	_, err := productSvc.SetVariantBundle(ctx, box, []productmodels.BundleComponent{
		{VariantID: pool[2], Quantity: 1}, {VariantID: pool[4], Quantity: 2},
	})
	require.NoError(t, err)
	pool = append(pool, box)
	engraving, engravingItem := newStockedVariant(ctx, t, "E2E Property Engraving", map[string]int64{taxedCurrency: 4_999}, 1_000_000)
	items[engraving] = engravingItem
	// parts is what one unit of a variant takes off the shelf, per stocked
	// variant: itself, or the box's parts.
	parts := func(variant string) map[string]int64 {
		if variant == box {
			return map[string]int64{pool[2]: 1, pool[4]: 2}
		}
		if _, stocked := items[variant]; stocked {
			return map[string]int64{variant: 1}
		}
		return nil
	}
	shelf := func(rt *rapid.T) map[string]int64 {
		out := map[string]int64{}
		for variant, item := range items {
			levels, err := inventorySvc.ListInventoryLevels(ctx, item)
			require.NoError(rt, err)
			require.Len(rt, levels, 1)
			out[variant] = levels[0].StockedQuantity
		}
		return out
	}
	ring, err := productSvc.GetVariant(ctx, pool[3])
	require.NoError(t, err)
	_, err = productSvc.SetProductAddOns(ctx, ring.ProductID, []string{engraving})
	require.NoError(t, err)
	newAutomaticPercentagePromotion(ctx, t, "E2E-PROPERTY-15", 1_500, []string{pool[1], pool[3], pool[5]})
	const coupon = "E2E-PROPERTY-COUPON"
	newCouponPromotion(ctx, t, coupon, 1_000, []string{pool[0], pool[2], pool[4]})
	profileID := newShippingProfile(ctx, t, "E2E Property Profile")
	deliveries := map[string]map[string]string{}
	for country, regionID := range map[string]string{taxedCountry: taxedRegionID, inclusiveTaxCountry: inclusiveRegionID} {
		deliveries[country] = map[string]string{
			"paid": newShippingOptionIn(ctx, t, regionID, profileID, "E2E Property Paid", 2_490, false),
			"free": newShippingOptionIn(ctx, t, regionID, profileID, "E2E Property Free", 0, false),
		}
	}
	customerID, email := newCustomer(ctx, t)
	writeStoreProfile(t)
	returnsFlow, err := container.Resolve[*returnswf.Interop](ctr, returnswf.InteropName)
	require.NoError(t, err, "the return flow is the one the installation provides")

	rapid.Check(t, func(rt *rapid.T) {
		country := rapid.SampledFrom([]string{taxedCountry, inclusiveTaxCountry}).Draw(rt, "country")
		inclusive := country == inclusiveTaxCountry

		cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: country, CustomerID: customerID})
		require.NoError(rt, err)
		for range rapid.IntRange(1, 4).Draw(rt, "lines") {
			line := cartwf.AddLineItemInput{
				CartID:    cart.CartID,
				VariantID: pool[rapid.IntRange(0, len(pool)-1).Draw(rt, "variant")],
				Quantity:  rapid.Int64Range(1, 5).Draw(rt, "quantity"),
			}
			if line.VariantID == pool[3] && rapid.Bool().Draw(rt, "engraved") {
				line.AddOns = []cartwf.AddOnRequest{{VariantID: engraving, Properties: map[string]string{
					"Text": rapid.SampledFrom([]string{"Ada", "Bo"}).Draw(rt, "engraving"),
				}}}
			}
			_, err := workflows.AddLineItem(ctx, line)
			require.NoError(rt, err, "every line a shopper adds has its cart's totals written")
		}
		fee := int64(0)
		if delivery := rapid.SampledFrom([]string{"none", "paid", "free"}).Draw(rt, "delivery"); delivery != "none" {
			_, err := workflows.AddQuotedShippingMethod(ctx, cart.CartID, deliveries[country][delivery], nil)
			require.NoError(rt, err, "a delivery of the shopper's market can be chosen")
			if delivery == "paid" {
				fee = 2_490
			}
		}
		if rapid.Bool().Draw(rt, "coupon") {
			require.NoError(rt, workflows.ApplyPromotionCode(ctx, cart.CartID, coupon))
		}
		totals, err := workflows.CalculateTotals(ctx, cart.CartID)
		require.NoError(rt, err, "every cart a shopper can build has its totals written")
		require.Equal(rt, inclusive, totals.PricesIncludeTax)

		// The cart as stored: its lines against the quote, and its total
		// against the stickers.
		stored, err := cartSvc.GetCart(ctx, cart.CartID)
		require.NoError(rt, err)
		require.False(rt, stored.TotalsStale())
		require.Equal(rt, totals.Total, stored.Total)
		require.Equal(rt, fee, stored.ShippingTotal, "the delivery costs what its option says")
		var quoted int64
		for _, line := range stored.Items {
			sticker := line.UnitPrice * line.Quantity
			quoted += sticker
			if inclusive {
				require.Equal(rt, sticker, line.Subtotal+line.TaxTotal, "line %s", line.ID)
			} else {
				require.Equal(rt, sticker, line.Subtotal, "line %s", line.ID)
			}
			require.LessOrEqual(rt, line.DiscountTotal, line.Subtotal)
			if line.VariantID == card {
				require.Zero(rt, line.TaxTotal, "a gift card carries no tax")
			}
			require.Equal(rt, line.Subtotal-line.DiscountTotal+line.TaxTotal, line.Total)
		}
		charged := quoted - stored.DiscountTotal + stored.ShippingTotal
		if !inclusive {
			charged += stored.TaxTotal
		}
		require.Equal(rt, charged, stored.Total, "the shopper pays the stickers, less the discount, plus any tax added on")

		// What the sale takes off the shelf: each line's units, a box's as its
		// parts.
		taken := map[string]int64{}
		for _, line := range stored.Items {
			for variant, per := range parts(line.VariantID) {
				taken[variant] += per * line.Quantity
			}
		}
		before := shelf(rt)

		// The order, its payment and its document.
		placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
			CartID:            cart.CartID,
			LocationID:        stockLocationID,
			PaymentProviderID: paymentmanual.ID,
			PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
			Email:             email,
			ExpectedTotal:     stored.Total,
		})
		require.NoError(rt, err, "every cart a shopper can build becomes an order")
		after := shelf(rt)
		for variant := range items {
			require.Equal(rt, taken[variant], before[variant]-after[variant], "the sale takes %s off the shelf", variant)
		}
		order, err := orderSvc.GetOrder(ctx, placed.OrderID)
		require.NoError(rt, err)
		require.Equal(rt, stored.Total, order.Total)
		require.Equal(rt, inclusive, order.PricesIncludeTax)
		// Each add-on is written under its line (ADR 0393): right after it, or
		// after another add-on of the same line.
		for k, line := range order.Items {
			if line.ParentLineItemID == nil {
				continue
			}
			require.Positive(rt, k, "an add-on is not the order's first line")
			above := order.Items[k-1]
			require.True(rt, above.ID == *line.ParentLineItemID ||
				(above.ParentLineItemID != nil && *above.ParentLineItemID == *line.ParentLineItemID),
				"the add-on %s is written under its line", line.ID)
		}
		collection, err := paymentSvc.GetPaymentCollection(ctx, placed.PaymentCollectionID)
		require.NoError(rt, err)
		require.Equal(rt, stored.Total, collection.Amount)

		recorder, err := adminRequestWithBody(http.MethodPost,
			"/admin/v1/orders/"+placed.OrderID+"/invoice", issueInvoiceBody())
		require.NoError(rt, err)
		require.Equal(rt, http.StatusCreated, recorder.Code, "every order is invoiced: %s", recorder.Body.String())
		var issued invoiceIssueResponse
		require.NoError(rt, json.Unmarshal(recorder.Body.Bytes(), &issued))
		document, err := adminRequestWithBody(http.MethodGet, "/admin/v1/invoices/"+issued.Data.InvoiceID, nil)
		require.NoError(rt, err)
		var invoice invoiceDocumentResponse
		require.NoError(rt, json.Unmarshal(document.Body.Bytes(), &invoice))
		require.Equal(rt, stored.Total, invoice.Data.Total)

		// Some of it comes back: lines drawn from the order at any quantity it
		// sold, an add-on with its line, received at the warehouse and refunded
		// in part. A gift card line is final (ADR 0213) and is asked back only
		// to be refused.
		if !rapid.Bool().Draw(rt, "a return") {
			return
		}
		request := ordersvc.CreateReturnInput{OrderID: order.ID, Reason: "property"}
		back := map[string]int64{}
		askedForACard := false
		for _, line := range order.Items {
			if line.ParentLineItemID != nil || !rapid.Bool().Draw(rt, "returned line") {
				continue
			}
			if line.IsGiftcard {
				askedForACard = true
			}
			quantity := rapid.Int64Range(1, line.Quantity).Draw(rt, "returned units")
			request.Lines = append(request.Lines, ordersvc.ReturnLineInput{OrderLineItemID: line.ID, Quantity: quantity})
			back[line.ID] = quantity
			for _, addOn := range order.Items {
				if addOn.ParentLineItemID != nil && *addOn.ParentLineItemID == line.ID {
					request.Lines = append(request.Lines, ordersvc.ReturnLineInput{OrderLineItemID: addOn.ID, Quantity: quantity})
					back[addOn.ID] = quantity
				}
			}
		}
		if len(request.Lines) == 0 {
			return
		}
		opened, err := orderSvc.CreateReturn(ctx, request)
		if askedForACard {
			require.Error(rt, err)
			require.Equal(rt, ordersvc.CodeGiftCardLineFinal, errors.CodeOf(err), "%v", err)
			return
		}
		require.NoError(rt, err, "a return of what was sold is opened")

		restocked := map[string]int64{}
		var units int64
		for _, line := range order.Items {
			for variant, per := range parts(line.VariantID) {
				restocked[variant] += per * back[line.ID]
				units += per * back[line.ID]
			}
		}
		beforeReceipt := shelf(rt)
		_, restockedUnits, receiptWarnings, err := returnsFlow.ReceiveReturn(ctx, opened.ID, stockLocationID)
		require.NoError(rt, err)
		require.Empty(rt, receiptWarnings)
		require.Equal(rt, units, restockedUnits)
		afterReceipt := shelf(rt)
		for variant := range items {
			require.Equal(rt, restocked[variant], afterReceipt[variant]-beforeReceipt[variant], "the return puts %s back", variant)
		}

		// What comes back is refunded up to what its units were sold for, the
		// figure the return's single read publishes (ADR 0433): an amount past
		// it, up to the order's total, is refused and moves nothing. This draw
		// went up to the order's total before, the fault as a property (D269).
		read, err := adminRequestWithBody(http.MethodGet, "/admin/v1/orders/"+order.ID+"/returns/"+opened.ID, nil)
		require.NoError(rt, err)
		require.Equal(rt, http.StatusOK, read.Code, read.Body.String())
		var record struct {
			Data returnRecordBody `json:"data"`
		}
		require.NoError(rt, json.Unmarshal(read.Body.Bytes(), &record))
		require.Len(rt, record.Data.Lines, len(request.Lines), "the record names the lines asked back")
		soldFor := record.Data.SoldFor
		require.GreaterOrEqual(rt, soldFor, int64(0))
		require.LessOrEqual(rt, soldFor, stored.Total, "the units are worth no more than the order")
		if soldFor < stored.Total {
			over := rapid.Int64Range(soldFor+1, stored.Total).Draw(rt, "refunded past the units")
			_, _, _, err := returnsFlow.RefundReturn(ctx, opened.ID, over, "property")
			require.Error(rt, err, "%d past the %d the units were sold for", over, soldFor)
			require.Equal(rt, returnswf.CodeRefundExceedsReturn, errors.CodeOf(err), "%v", err)
			collection, err = paymentSvc.GetPaymentCollection(ctx, placed.PaymentCollectionID)
			require.NoError(rt, err)
			require.Zero(rt, collection.RefundedAmount, "the refused refund moved nothing")
		}
		if soldFor == 0 {
			return
		}

		amount := rapid.Int64Range(1, soldFor).Draw(rt, "refunded")
		refunded, _, refundWarnings, err := returnsFlow.RefundReturn(ctx, opened.ID, amount, "property")
		require.NoError(rt, err)
		require.Empty(rt, refundWarnings)
		require.Equal(rt, amount, refunded)
		collection, err = paymentSvc.GetPaymentCollection(ctx, placed.PaymentCollectionID)
		require.NoError(rt, err)
		require.Equal(rt, amount, collection.RefundedAmount, "the refund is the collection's")
		require.LessOrEqual(rt, collection.RefundedAmount, collection.CapturedAmount)
	})
}
