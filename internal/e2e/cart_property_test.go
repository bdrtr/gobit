//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
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
func TestEveryCartAShopperCanBuildIsSoldAsQuoted(t *testing.T) {
	ctx := t.Context()

	prices := []int64{1, 7, 999, 11_999, 19_900, 123_457}
	pool := make([]string, len(prices))
	for i, price := range prices {
		pool[i], _ = newStockedVariant(ctx, t, fmt.Sprintf("E2E Property Product %d", i),
			map[string]int64{taxedCurrency: price}, 1_000_000)
	}
	card := newGiftCardVariantStocked(ctx, t, 5_000, 1_000_000)
	pool = append(pool, card)
	newAutomaticPercentagePromotion(ctx, t, "E2E-PROPERTY-15", 1_500, []string{pool[1], pool[3], pool[5]})
	customerID, email := newCustomer(ctx, t)
	writeStoreProfile(t)

	rapid.Check(t, func(rt *rapid.T) {
		country := rapid.SampledFrom([]string{taxedCountry, inclusiveTaxCountry}).Draw(rt, "country")
		inclusive := country == inclusiveTaxCountry

		cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: country, CustomerID: customerID})
		require.NoError(rt, err)
		var totals cartwf.Totals
		for range rapid.IntRange(1, 4).Draw(rt, "lines") {
			added, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
				CartID:    cart.CartID,
				VariantID: pool[rapid.IntRange(0, len(pool)-1).Draw(rt, "variant")],
				Quantity:  rapid.Int64Range(1, 5).Draw(rt, "quantity"),
			})
			require.NoError(rt, err, "every line a shopper adds has its cart's totals written")
			totals = added.Totals
		}
		require.Equal(rt, inclusive, totals.PricesIncludeTax)

		// The cart as stored: its lines against the quote, and its total
		// against the stickers.
		stored, err := cartSvc.GetCart(ctx, cart.CartID)
		require.NoError(rt, err)
		require.False(rt, stored.TotalsStale())
		require.Equal(rt, totals.Total, stored.Total)
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
		order, err := orderSvc.GetOrder(ctx, placed.OrderID)
		require.NoError(rt, err)
		require.Equal(rt, stored.Total, order.Total)
		require.Equal(rt, inclusive, order.PricesIncludeTax)
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
	})
}
