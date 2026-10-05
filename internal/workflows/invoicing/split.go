package invoicing

import (
	"cmp"
	"math/bits"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
)

// The split of an act's amount over the rows of the sale document it amends
// (ADR 0406). This file is the only place one is computed.

// apportion shares amount over the weights by the largest remainder: each share
// is its weight's part rounded down, and what the rounding left goes one unit
// at a time to the largest remainders, the earlier position winning a tie. The
// products are taken in 128 bits, so no weight or amount overflows them.
//
// When the amount is at most the weights' sum, no share exceeds its weight: a
// share rounded down is below its weight wherever it has a remainder to be
// raised by. Weights adding to zero can share nothing, and an amount over them
// is an error rather than a division by zero.
func apportion(amount int64, weights []int64) ([]int64, error) {
	if amount < 0 {
		return nil, errors.Internal(CodeInvalidInput, "an amount to share cannot be negative: %d", amount)
	}
	var total uint64
	for _, weight := range weights {
		if weight < 0 {
			return nil, errors.Internal(CodeInvalidInput, "a weight cannot be negative: %d", weight)
		}
		sum, carry := bits.Add64(total, uint64(weight), 0)
		if carry != 0 {
			return nil, errors.Internal(CodeInvalidInput, "the weights add up past what can be carried")
		}
		total = sum
	}
	shares := make([]int64, len(weights))
	if amount == 0 {
		return shares, nil
	}
	if total == 0 {
		return nil, errors.Internal(CodeInvalidInput, "%d cannot be shared over weights that add to zero", amount)
	}

	remainders := make([]uint64, len(weights))
	var given int64
	for i, weight := range weights {
		hi, lo := bits.Mul64(uint64(amount), uint64(weight)) //nolint:gosec // G115: both are checked not negative
		if hi >= total {
			return nil, errors.Internal(CodeInvalidInput, "%d is more than its weights can share", amount)
		}
		quotient, remainder := bits.Div64(hi, lo, total)
		// The quotient is at most amount, since weight is at most total.
		shares[i], remainders[i] = int64(quotient), remainder //nolint:gosec // G115: at most amount
		given += int64(quotient)                              //nolint:gosec // G115: at most amount
	}

	order := make([]int, len(weights))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(remainders[b], remainders[a]) })
	for k := int64(0); k < amount-given; k++ {
		shares[order[k]]++
	}

	return shares, nil
}

// mulDiv returns a·b/c rounded down, or up when ceil is set, in 128 bits. Its
// callers pass amounts that are not negative, a positive c and a b at most c,
// a share of a whole, so the quotient is at most a.
func mulDiv(a, b, c int64, ceil bool) int64 {
	hi, lo := bits.Mul64(uint64(a), uint64(b))           //nolint:gosec // G115: not negative, see above
	quotient, remainder := bits.Div64(hi, lo, uint64(c)) //nolint:gosec // G115: positive, see above
	if ceil && remainder != 0 {
		quotient++
	}

	return int64(quotient) //nolint:gosec // G115: at most a, see above
}

// saleRow is one row of the sale document as an amendment splits over it.
type saleRow struct {
	LineID     string
	Position   int32
	Total      int64
	TaxTotal   int64
	TaxRateBps int32
	LeftTotal  int64
	LeftTax    int64
	Components []saleRowComponent
	// Description is what the row printed; Quantity what it sold.
	Description string
	Quantity    int64
	// GiftCard says the row sold a gift card, which no price lowered falls on.
	GiftCard bool
	// Carriage says the row is the carriage the sale was shipped with, or a
	// row a dearer delivery added to a sale that shipped free.
	Carriage bool
	// Charged says the row is a live charge's, not the sale's own.
	Charged bool
	// OrderLineID is the order line the row printed; empty for carriage.
	OrderLineID string
}

// saleRowComponent is one rate of a stacked sale row.
type saleRowComponent struct {
	RateID        string
	RateBps       int32
	Compound      bool
	TaxableAmount int64
	TaxAmount     int64
	LeftTax       int64
}

// rowPart is the part of an act's amount one sale row takes, and the units a
// return took back of it.
type rowPart struct {
	row      *saleRow
	amount   int64
	returned int64
}

// amendingLine prints one part as a row of one unit carrying its share
// (ADR 0406). Its tax is rounded on the running total the row has given back:
// what the row's ratio gives on everything given back with this part, less
// what it gave on everything before, so the parts of a row never collect the
// rounding of the parts before them. The part that empties its row takes the
// tax the row has left up to its own amount, so a line given back only through
// this flow gives back exactly the tax it charged; no part takes more tax than
// its own amount, and a refund issued outside the flow at a lower tax ratio
// can leave tax no later part gives back. Each rate takes
// the largest-remainder share of the part's tax over what the rates have
// left, on a base never below its own tax.
func amendingLine(part rowPart, pricesIncludeTax bool) documentLine {
	row := part.row
	tax := int64(0)
	switch {
	case part.amount == row.LeftTotal:
		tax = row.LeftTax
	case row.Total > 0:
		given := min(max(row.Total-row.LeftTotal, 0), row.Total)
		upto := min(given+part.amount, row.Total)
		tax = mulDiv(upto, row.TaxTotal, row.Total, false) - mulDiv(given, row.TaxTotal, row.Total, false)
		tax = min(tax, row.LeftTax)
	}
	tax = max(min(tax, part.amount), 0)

	description := row.Description
	if part.returned > 0 {
		description = formatReturned(part.returned, row.Description)
	}
	line := documentLine{
		Description:  description,
		Quantity:     1,
		UnitPrice:    part.amount - tax,
		Subtotal:     part.amount - tax,
		TaxRateBps:   row.TaxRateBps,
		TaxTotal:     tax,
		Total:        part.amount,
		AmendsLineID: row.LineID,
	}
	if pricesIncludeTax {
		line.UnitPrice = part.amount
	}
	if len(row.Components) == 0 {
		return line
	}

	weights := make([]int64, len(row.Components))
	for i := range row.Components {
		weights[i] = row.Components[i].LeftTax
	}
	shares, err := apportion(tax, weights)
	if err != nil {
		// The row's tax left is the sum of its rates' tax left, so a tax held
		// to it always fits them; a fault here prints the shares as nothing
		// and the invoice module refuses the row.
		shares = make([]int64, len(row.Components))
	}
	for i := range row.Components {
		component := row.Components[i]
		base := part.amount
		if row.Total > 0 {
			base = mulDiv(component.TaxableAmount, part.amount, row.Total, true)
		}
		line.TaxComponents = append(line.TaxComponents, documentLineTax{
			RateID: component.RateID, RateBps: component.RateBps, Compound: component.Compound,
			TaxableAmount: max(base, shares[i]), TaxAmount: shares[i],
		})
	}

	return line
}
