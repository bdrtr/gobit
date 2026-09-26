package fulfilling

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// The refusals of a delivery change that are this flow's (ADR 0199).
const (
	// CodeQuoteFailed reports that the fulfillment module could not price the
	// order's options.
	CodeQuoteFailed = "fulfilling_quote_failed"
	// CodeOptionUnavailable refuses an option the fulfillment module did not
	// quote for the order.
	CodeOptionUnavailable = "fulfilling_option_unavailable"
)

// deliveryFacts is the order module's DeliveryFactsJSON answer. The producing
// side documents it; it is repeated here because this package cannot import
// that module (ADR 0006).
type deliveryFacts struct {
	RegionID     string `json:"region_id"`
	CurrencyCode string `json:"currency_code"`
	CountryCode  string `json:"country_code"`
	Subtotal     int64  `json:"subtotal"`
	ItemCount    int64  `json:"item_count"`
}

// optionsRequest is the fulfillment module's option listing request, the
// fields this flow fills.
type optionsRequest struct {
	RegionID         string `json:"region_id"`
	CurrencyCode     string `json:"currency_code"`
	CountryCode      string `json:"country_code"`
	Subtotal         int64  `json:"subtotal"`
	ItemCount        int64  `json:"item_count"`
	TotalWeight      int64  `json:"total_weight"`
	IncludeAdminOnly bool   `json:"include_admin_only"`
}

// quotedOption is one option as the fulfillment module priced it.
type quotedOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Amount       int64  `json:"amount"`
	CurrencyCode string `json:"currency_code"`
	IsReturn     bool   `json:"is_return"`
}

// optionsResponse is the option listing's answer.
type optionsResponse struct {
	Options []quotedOption `json:"options"`
}

// deliveryChangeRequest is the order module's ChangeDeliveryJSON request.
type deliveryChangeRequest struct {
	ShippingMethodID string `json:"shipping_method_id"`
	ShippingOptionID string `json:"shipping_option_id"`
	Name             string `json:"name"`
	Amount           int64  `json:"amount"`
}

// ChangeDelivery puts one of the order's deliveries on another shipping option,
// at the price the fulfillment module quotes for the order, and returns the
// order module's answer: the change, or JSON null when it was already on that
// option (ADR 0199).
//
// A parcel already opened was handed to its carrier on the old service, so the
// change is refused while any parcel of the order is pending, shipped or
// delivered, as an address correction is (ADR 0195).
//
// The option is priced on the order's own facts and has to be among the
// options the fulfillment module lists for them; an option it does not list is
// not one this order can go on. Admin-only options are listed, because the
// operator is the one changing it, and return options are not.
//
// The quote and the write are not one transaction. A price changed between
// them is the price the operator was quoted, which is the one they asked for.
func (w *Workflows) ChangeDelivery(
	ctx context.Context, orderID, shippingMethodID, shippingOptionID string,
) (json.RawMessage, error) {
	shippingOptionID = strings.TrimSpace(shippingOptionID)
	switch {
	case strings.TrimSpace(orderID) == "":
		return nil, errors.Invalid(CodeInvalidInput, "the order id is required")
	case shippingOptionID == "":
		return nil, errors.Invalid(CodeInvalidInput, "the shipping option id is required")
	}

	if err := w.refuseWhileUnderway(ctx, orderID, "the service it was opened on", "changing the delivery"); err != nil {
		return nil, err
	}

	option, err := w.quoteForOrder(ctx, orderID, shippingOptionID)
	if err != nil {
		return nil, err
	}

	request, err := json.Marshal(deliveryChangeRequest{
		ShippingMethodID: shippingMethodID,
		ShippingOptionID: option.ID,
		Name:             option.Name,
		Amount:           option.Amount,
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"the delivery change could not be built")
	}

	return w.orders.ChangeDeliveryJSON(ctx, orderID, request)
}

// quoteForOrder prices the order's options and returns the one asked for.
func (w *Workflows) quoteForOrder(ctx context.Context, orderID, shippingOptionID string) (quotedOption, error) {
	raw, err := w.orders.DeliveryFactsJSON(ctx, orderID)
	if err != nil {
		return quotedOption{}, errors.Wrap(err, errors.KindOf(err), CodeOrderUnreadable,
			"order %s could not be read, so its delivery was not changed", orderID)
	}
	var facts deliveryFacts
	if err := json.Unmarshal(raw, &facts); err != nil {
		return quotedOption{}, errors.Wrap(err, errors.KindInternal, CodeOrderUnreadable,
			"order %s's delivery facts could not be parsed", orderID)
	}

	request, err := json.Marshal(optionsRequest{
		RegionID:     facts.RegionID,
		CurrencyCode: facts.CurrencyCode,
		CountryCode:  facts.CountryCode,
		Subtotal:     facts.Subtotal,
		ItemCount:    facts.ItemCount,
		// An order carries no weight, as the cart it came from did not; a
		// weight-banded option prices at its lowest band, as it did at checkout.
		TotalWeight:      0,
		IncludeAdminOnly: true,
	})
	if err != nil {
		return quotedOption{}, errors.Wrap(err, errors.KindInternal, CodeQuoteFailed,
			"the quote request for order %s could not be built", orderID)
	}

	answer, err := w.fulfillments.ListOptionsJSON(ctx, request)
	if err != nil {
		return quotedOption{}, errors.Wrap(err, errors.KindOf(err), CodeQuoteFailed,
			"the shipping options could not be quoted for order %s", orderID)
	}
	var quoted optionsResponse
	if err := json.Unmarshal(answer, &quoted); err != nil {
		return quotedOption{}, errors.Wrap(err, errors.KindInternal, CodeQuoteFailed,
			"the shipping quote for order %s could not be parsed", orderID)
	}

	for i := range quoted.Options {
		option := quoted.Options[i]
		if option.ID != shippingOptionID || option.IsReturn {
			continue
		}
		// A quote in another currency would be subtracted from the order's
		// amount as a bare integer.
		if option.CurrencyCode != facts.CurrencyCode {
			break
		}

		return option, nil
	}

	return quotedOption{}, errors.Conflict(CodeOptionUnavailable,
		"the shipping option %q is not available for order %s", shippingOptionID, orderID)
}
