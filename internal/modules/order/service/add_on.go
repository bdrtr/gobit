package service

import (
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// CodeAddOnFollows reports an after-sales act that would part an add-on from
// the line it was sold with (ADR 0230).
const CodeAddOnFollows = "order_add_on_follows_its_line"

// addOnsOf returns the lines of the order that are add-ons of parent
// (ADR 0229).
func addOnsOf(lines []models.OrderLineItem, parent string) []models.OrderLineItem {
	var out []models.OrderLineItem
	for i := range lines {
		if lines[i].ParentLineItemID != nil && *lines[i].ParentLineItemID == parent {
			out = append(out, lines[i])
		}
	}
	return out
}

// addOnFollows refuses an act on an add-on alone.
func addOnFollows(addOn, parent string) error {
	return errors.Invalid(CodeAddOnFollows,
		"line %s is an add-on of line %s and goes back only with it, at its quantity", addOn, parent)
}

// checkAddOnReturn holds a return to the bond between an add-on and its line
// (ADR 0230): a line carrying add-ons comes back with each of them at its own
// quantity, and an add-on only with its line at the same quantity. What each
// line refunds stays the operator's, nothing included.
func checkAddOnReturn(lines []models.OrderLineItem, requested []ReturnLineInput) error {
	asked := make(map[string]int64, len(requested))
	for i := range requested {
		asked[requested[i].OrderLineItemID] = requested[i].Quantity
	}
	for i := range lines {
		quantity, named := asked[lines[i].ID]
		if lines[i].ParentLineItemID != nil {
			parent := *lines[i].ParentLineItemID
			if named && asked[parent] != quantity {
				return addOnFollows(lines[i].ID, parent)
			}
			continue
		}
		if !named {
			continue
		}
		addOns := addOnsOf(lines, lines[i].ID)
		for j := range addOns {
			if asked[addOns[j].ID] != quantity {
				return errors.Invalid(CodeAddOnFollows,
					"line %s carries add-on %s, which comes back with it: name it at %d",
					lines[i].ID, addOns[j].ID, quantity)
			}
		}
	}
	return nil
}
