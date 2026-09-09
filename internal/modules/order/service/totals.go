package service

import (
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// normalizeCreateOrder VALIDATES the order input and returns it in its
// normalized form.
//
// The validation and the normalization are in a single place: the currency and
// the e-mail cannot be validated without being normalized ("try" and "TRY" are
// the same code) and they cannot be stored without being validated. Separating
// the two would leave the door open for one of them to be forgotten at a call
// site.
//
// The input is taken BY VALUE and a modified copy of it is returned; the
// structure the caller gave does not change. The Items slice is shared but its
// elements ARE NOT WRITTEN, only read.
//
// For the order of the layers and its rationale see [Service.CreateOrder].
func normalizeCreateOrder(in CreateOrderInput) (CreateOrderInput, error) {
	if err := requireID("region_id", in.RegionID); err != nil {
		return CreateOrderInput{}, err
	}
	if err := optionalID("customer_id", in.CustomerID); err != nil {
		return CreateOrderInput{}, err
	}
	if err := optionalID("cart_id", in.CartID); err != nil {
		return CreateOrderInput{}, err
	}
	if err := optionalID("idempotency_key", in.IdempotencyKey); err != nil {
		return CreateOrderInput{}, err
	}

	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return CreateOrderInput{}, err
	}
	in.CurrencyCode = currency

	email, err := normalizeEmail(in.Email)
	if err != nil {
		return CreateOrderInput{}, err
	}
	in.Email = email

	if err := validateOrderTotals(in); err != nil {
		return CreateOrderInput{}, err
	}
	if err := validateOrderItems(in); err != nil {
		return CreateOrderInput{}, err
	}
	return in, nil
}

// validateOrderTotals validates the range and the identity of the amounts at
// the order level.
func validateOrderTotals(in CreateOrderInput) error {
	for _, field := range []struct {
		label string
		value int64
	}{
		{"subtotal", in.Subtotal},
		{"discount_total", in.DiscountTotal},
		{"tax_total", in.TaxTotal},
		{"shipping_total", in.ShippingTotal},
		{"total", in.Total},
	} {
		if err := checkAmount(field.label, field.value, models.MaxTotal); err != nil {
			return err
		}
	}

	// The discount CANNOT EXCEED the subtotal.
	//
	// The identity check (below) is not enough on its own: when an excessive
	// discount is swallowed by the tax and the shipping the identity HOLDS and
	// the total does not even go negative. Example: subtotal=1000,
	// discount=3000, shipping=2500 -> total=500. The customer pays 500 for goods
	// worth 1000 together with shipping worth 2500 and neither the service nor
	// the orders_totals_consistent constraint sees it.
	//
	// A shipping discount is OUTSIDE this rule: a flow that wants to discount
	// the shipping does it by lowering shipping_total, not by inflating the
	// discount.
	if in.DiscountTotal > in.Subtotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"the discount cannot exceed the subtotal: discount_total=%d, subtotal=%d",
			in.DiscountTotal, in.Subtotal)
	}

	expected := in.Subtotal - in.DiscountTotal + in.TaxTotal + in.ShippingTotal
	if in.Total != expected {
		return errors.Invalid(CodeTotalsInconsistent,
			"the order total is inconsistent: total=%d was given, subtotal(%d) - discount_total(%d) + tax_total(%d) + shipping_total(%d) = %d",
			in.Total, in.Subtotal, in.DiscountTotal, in.TaxTotal, in.ShippingTotal, expected)
	}
	return nil
}

// validateOrderItems validates the lines and checks that their subtotals add up
// to the subtotal of the order.
func validateOrderItems(in CreateOrderInput) error {
	if len(in.Items) == 0 {
		return errors.Invalid(CodeOrderEmpty,
			"the order has to contain at least one line: in an order without lines nothing was sold")
	}
	if len(in.Items) > maxOrderItems {
		return errors.Invalid(CodeInvalidInput,
			"the order can contain at most %d lines: %d", maxOrderItems, len(in.Items))
	}

	var sum int64
	// The loop is walked by index: the line input is large and copying it by
	// value would carry a few hundred bytes for nothing on every turn.
	for i := range in.Items {
		if err := validateOrderItem(i, in.Items[i]); err != nil {
			return err
		}
		next, err := addAmount(sum, in.Items[i].Subtotal)
		if err != nil {
			return err
		}
		sum = next
	}

	// The subtotal of the order is the SUM of the subtotals of the lines.
	// Because a discount and a tax can also arise at the order level (a
	// campaign, shipping tax) only the subtotal is subject to this rule.
	//
	// The check is the answer to the "forgetting to send the lines" mistake: the
	// identity check would count an order with subtotal=0 and total=0 as
	// CONSISTENT and an order in which the customer paid nothing would be
	// written.
	if in.Subtotal != sum {
		return errors.Invalid(CodeTotalsInconsistent,
			"the subtotal of the order has to equal the sum of the line subtotals: %d was given, the lines add up to %d",
			in.Subtotal, sum)
	}
	return nil
}

// validateOrderItem validates a single order line.
//
// The position (index) of the line is written into the error message: the lines
// do not have identifiers yet — the identifiers are produced at the moment of
// the write — and the only answer to the question "which line" is the order in
// which the caller sent them.
func validateOrderItem(index int, item CreateOrderItemInput) error {
	if err := requireID("items[].variant_id", item.VariantID); err != nil {
		return err
	}
	if err := requireText("items[].title", item.Title); err != nil {
		return err
	}
	if err := checkQuantity(item.Quantity); err != nil {
		return err
	}
	if err := checkAmount("items[].unit_price", item.UnitPrice, models.MaxAmount); err != nil {
		return err
	}
	for _, field := range []struct {
		label string
		value int64
	}{
		{"items[].subtotal", item.Subtotal},
		{"items[].discount_total", item.DiscountTotal},
		{"items[].tax_total", item.TaxTotal},
		{"items[].total", item.Total},
	} {
		if err := checkAmount(field.label, field.value, models.MaxTotal); err != nil {
			return err
		}
	}

	// The line subtotal = the unit price x the quantity. This is the only place
	// where the quantity and the price stand together; a line priced with the
	// wrong quantity would be caught at no other gate.
	expectedSubtotal, err := multiplyAmount(item.UnitPrice, item.Quantity)
	if err != nil {
		return err
	}
	if item.Subtotal != expectedSubtotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"the line subtotal is inconsistent (line %d, %s): subtotal=%d was given, unit_price(%d) x quantity(%d) = %d",
			index, item.VariantID, item.Subtotal, item.UnitPrice, item.Quantity, expectedSubtotal)
	}

	// At the line level too the discount cannot exceed the subtotal; the same
	// rationale as at the order level (the tax can swallow an excessive discount
	// and make the identity hold).
	if item.DiscountTotal > item.Subtotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"the line discount cannot exceed the subtotal (line %d, %s): discount_total=%d, subtotal=%d",
			index, item.VariantID, item.DiscountTotal, item.Subtotal)
	}

	expectedTotal := item.Subtotal - item.DiscountTotal + item.TaxTotal
	if item.Total != expectedTotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"the line total is inconsistent (line %d, %s): total=%d was given, subtotal(%d) - discount_total(%d) + tax_total(%d) = %d",
			index, item.VariantID, item.Total, item.Subtotal, item.DiscountTotal, item.TaxTotal, expectedTotal)
	}

	return validateLineTaxComponents(index, item)
}

// maxLineTaxComponents is the most components one line's tax may be split into.
//
// Four is the depth the tax module allows a stack (federal + state + municipal +
// a surcharge, plus one), and the number is REPEATED here rather than imported:
// the two modules do not know each other (Principle 2.1), and what this module
// is protecting is its own table rather than that module's rule. If they ever
// disagree the stricter one wins and the caller is told which line it was.
const maxLineTaxComponents = 4

// maxLineTaxRateBps is the largest rate a tax component may carry (100%).
//
// The bound is written here rather than imported from the tax module for the
// same reason as the count above: the two modules do not know each other, and
// the same number is a CHECK on this module's own table (migration 000013).
const maxLineTaxRateBps int32 = 10_000

// validateLineTaxComponents validates one line's per-rate tax breakdown.
//
// # Why the sum is checked here rather than left to the database
//
// It spans rows. A CHECK sees one row, and the identity that matters — the
// components add up to the line's tax — is over all of them. This is the gate
// that keeps a document from printing a breakdown that disagrees with the amount
// the customer was charged.
func validateLineTaxComponents(index int, item CreateOrderItemInput) error {
	if len(item.TaxComponents) == 0 {
		return nil
	}
	if len(item.TaxComponents) < 2 {
		return errors.Invalid(CodeInvalidInput,
			"a tax breakdown of one says nothing the line does not (line %d, %s); "+
				"send it empty when a single rate applied",
			index, item.VariantID)
	}
	if len(item.TaxComponents) > maxLineTaxComponents {
		return errors.Invalid(CodeInvalidInput,
			"a line's tax can be split into at most %d components (line %d, %s): %d",
			maxLineTaxComponents, index, item.VariantID, len(item.TaxComponents))
	}

	var sum int64
	for at := range item.TaxComponents {
		component := item.TaxComponents[at]
		if component.RateBps < 0 || component.RateBps > maxLineTaxRateBps {
			return errors.Invalid(CodeInvalidInput,
				"the rate of a tax component must be in [0, %d] basis points "+
					"(line %d, %s, component %d): %d",
				maxLineTaxRateBps, index, item.VariantID, at, component.RateBps)
		}
		if at == 0 && component.Compound {
			return errors.Invalid(CodeInvalidInput,
				"the first tax component stands on nothing and cannot be compound "+
					"(line %d, %s)", index, item.VariantID)
		}
		if err := checkAmount("items[].tax_components[].taxable_amount",
			component.TaxableAmount, models.MaxTotal); err != nil {
			return err
		}
		// The same bound the line carries, one component at a time: a rate is at
		// most 100%, so a component cannot take more than the base it was
		// computed on. A line whose total stays inside its own amount can still
		// hide a component that does not.
		if component.TaxAmount < 0 || component.TaxAmount > component.TaxableAmount {
			return errors.Invalid(CodeInvalidInput,
				"a tax component cannot exceed its own base (line %d, %s, component %d): "+
					"tax=%d, base=%d",
				index, item.VariantID, at, component.TaxAmount, component.TaxableAmount)
		}

		next, err := addAmount(sum, component.TaxAmount)
		if err != nil {
			return err
		}
		sum = next
	}

	if sum != item.TaxTotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"the line's tax components have to add up to its tax (line %d, %s): "+
				"the components total %d, tax_total=%d",
			index, item.VariantID, sum, item.TaxTotal)
	}

	return nil
}
