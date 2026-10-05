package cart

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// QuoteUnitPrices answers, for each variant, the unit price a one-unit line of
// it would be charged in a cart of the given customer in the given region,
// before any promotion (ADR 0216).
//
// It is the cart's own pricing asked without a cart: the region's currency, the
// same rule context a cart of that customer carries that names no sales channel
// — the region, the customer, their company and their head group (ADR 0397) —
// and quantity one. So a price
// list that would price the customer's cart prices the quote, and a promotion,
// which is the cart's discount and not its price, does not.
//
// A region that does not exist is [CodeQuoteRegionUnknown], so a caller can
// tell it from a region it could not read. A variant with no price set, or no
// price in the region's currency, is absent:
// asked about as one of many, it has no price to compare, which is not an error
// of the others. A failure to read the customer's groups or company prices
// without them, as a cart's totals do.
func (w *Workflows) QuoteUnitPrices(
	ctx context.Context, regionID, customerID string, variantIDs []string,
) (currency string, prices map[string]int64, err error) {
	if strings.TrimSpace(regionID) == "" {
		return "", nil, errors.Invalid(CodeInvalidInput, "a quote needs a region")
	}
	currency, _, err = w.regions.RegionCurrency(ctx, regionID)
	if err != nil {
		if errors.IsNotFound(err) {
			return "", nil, errors.Wrap(err, errors.KindNotFound, CodeQuoteRegionUnknown,
				"region %s does not exist", regionID)
		}
		return "", nil, err
	}
	prices = map[string]int64{}
	if len(variantIDs) == 0 {
		return currency, prices, nil
	}

	linked, err := w.links.ListMany(ctx, LinkVariantPriceSet, variantIDs)
	if err != nil {
		return "", nil, errors.Wrap(err, errors.KindOf(err), CodeLinkReadFailed,
			"could not read the %q link (%d variants)", LinkVariantPriceSet, len(variantIDs))
	}
	var priced []string
	req := priceRequest{CurrencyCode: currency}
	for _, variantID := range variantIDs {
		if sets := linked[variantID]; len(sets) == 1 {
			priced = append(priced, variantID)
			req.Items = append(req.Items, priceRequestItem{PriceSetID: sets[0], Quantity: 1})
		}
	}
	if len(priced) == 0 {
		return currency, prices, nil
	}

	attributes, _, contextErr := w.ruleContext(ctx, Snapshot{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	if contextErr != nil {
		w.log.WarnContext(ctx, "the customer's groups or company could not be read; quoting without them",
			"error", contextErr, "customer_id", customerID)
	}
	req.Attributes = attributes

	payload, err := json.Marshal(req)
	if err != nil {
		return "", nil, errors.Wrap(err, errors.KindInternal, CodePriceResponseInvalid,
			"the quote request could not be encoded")
	}
	raw, err := w.prices.CalculateAmountsJSON(ctx, payload)
	if err != nil {
		return "", nil, err
	}
	var resp priceResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", nil, errors.Wrap(err, errors.KindInternal, CodePriceResponseInvalid,
			"the quote result could not be decoded")
	}
	if len(resp.Items) != len(priced) {
		return "", nil, errors.Internal(CodePriceResponseInvalid,
			"for %d variants the quote returned %d records", len(priced), len(resp.Items))
	}
	for i := range resp.Items {
		if resp.Items[i].Priced {
			prices[priced[i]] = resp.Items[i].Amount
		}
	}

	return currency, prices, nil
}
