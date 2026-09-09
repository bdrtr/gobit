package checkout

import (
	"testing"

	"github.com/bdrtr/gobit/core/errors"
)

// planWithin builds a plan that satisfies every rule validate() already
// checked, so a test below fails for the ONE reason it is about.
//
// Both totals are computed rather than written: a literal that got the identity
// wrong would be refused by the identity check and the test would pass for a
// reason it is not testing.
func planWithin(lineSubtotal, lineDiscount, cartDiscount int64) *checkoutPlan {
	line := planLine{
		LineItemID: "li_1",
		VariantID:  "var_1",
		Unmanaged:  true,
		Quantity:   1,
		UnitPrice:  lineSubtotal,
		Subtotal:   lineSubtotal,

		DiscountTotal: lineDiscount,
	}
	line.Total = line.Subtotal - line.DiscountTotal + line.TaxTotal

	plan := &checkoutPlan{
		CartID:        "cart_1",
		Lines:         []planLine{line},
		Subtotal:      lineSubtotal,
		DiscountTotal: cartDiscount,
		ShippingTotal: 0,
	}
	plan.Amount = plan.Subtotal - plan.DiscountTotal + plan.TaxTotal + plan.ShippingTotal

	return plan
}

// TestAnAcceptedPlanCannotDiscountMoreThanItCharges is about a layer of
// defense, not about a live defect, and the distinction is written here so
// nobody reads it as the second.
//
// # Nothing reaches validate() with this shape today
//
// The validator has ONE production caller ([Workflows.prepare]), which builds
// the plan from the cart workflow's own totals — and cart/discount.go refuses a
// line discount outside [0, line subtotal] before that. The cart discount is
// then the sum of the line discounts and the cart subtotal the sum of the line
// subtotals, so the ceiling holds by construction upstream.
//
// # Then why check it here at all
//
// Because that is what this function is FOR. Its own godoc says the repetition
// is deliberate: "the price of corrupt totals must not be stock that is reserved
// and released again". A defense-in-depth layer that is weaker than the three
// layers it duplicates — the cart service, the order service, and the CHECK
// constraints orders_discount_within_subtotal and
// order_line_items_discount_within_subtotal — is not defense, it is the
// appearance of it. Every one of those refuses a discount above the subtotal
// and this validator did not.
//
// # It fails on the tree it was written against
//
// Which is the whole reason it exists: the identity check accepts
// Subtotal=1000, DiscountTotal=3000, Total=-2000, because -2000 really is
// 1000 - 3000 + 0. An identity is not a bound.
func TestAnAcceptedPlanCannotDiscountMoreThanItCharges(t *testing.T) {
	t.Parallel()

	for name, plan := range map[string]*checkoutPlan{
		// The cart-level ceiling: one line whose own discount is legal, and a
		// cart discount larger than the cart subtotal.
		"a cart discount above the cart subtotal": planWithin(1000, 0, 3000),
		// The line-level ceiling: the line gives back more than it charges.
		"a line discount above the line subtotal": planWithin(1000, 3000, 0),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := plan.validate()
			if err == nil {
				t.Fatalf("the plan was ACCEPTED with subtotal %d, cart discount %d and line "+
					"discount %d. The order module refuses this shape, the cart service "+
					"refuses it, and two CHECK constraints refuse it — this validator is "+
					"the layer that exists to refuse it before any stock is reserved",
					plan.Subtotal, plan.DiscountTotal, plan.Lines[0].DiscountTotal)
			}

			if kind := errors.KindOf(err); kind != errors.KindInternal {
				t.Errorf("the refusal is %v; a plan that reached this validator with a "+
					"corrupt total is a fault on gobit's side of the boundary, which is "+
					"what every other refusal in this function reports (%v). Error: %v",
					kind, errors.KindInternal, err)
			}
		})
	}
}

// TestAPlanAtTheCeilingIsStillAccepted is what keeps the two rows above from
// being satisfied by a validator that refuses everything.
//
// A discount EQUAL to the subtotal is legal arithmetic — it is a fully
// discounted cart — and it is refused one check later, by the positive-amount
// rule, with a message that says so. What must not happen is this shape being
// refused as a corrupt total.
func TestAPlanAtTheCeilingIsStillAccepted(t *testing.T) {
	t.Parallel()

	plan := planWithin(1000, 1000, 1000)
	plan.ShippingTotal = 500
	plan.Amount = plan.Subtotal - plan.DiscountTotal + plan.TaxTotal + plan.ShippingTotal

	err := plan.validate()
	if err != nil {
		t.Fatalf("a cart discounted to exactly its subtotal was refused: %v", err)
	}
}
