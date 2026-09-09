package service

import (
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TaxComponent is one rate applied inside a line's stack.
//
// # Why a line needs more than one of these
//
// A stacked line is taxed by several rates and the line can carry only one of
// them (ADR 0095 stores the stack's BASE). That is enough to total an order and
// not enough to print it: an invoice states every rate it charged and the
// amount charged under it, and "5%" on a line taxed at 5+8 is a statement the
// customer's own arithmetic disagrees with.
//
// # The amounts are the ones that were CHARGED, not a re-derivation
//
// Every component is floored on its own base, so the components' amounts are
// what the customer paid; recomputing them later from the line's total would
// give a different split, and the split is what the document has to show.
//
// # Why a domain type carries JSON tags
//
// The cross-module schema reuses this type as it is, because [toInteropItemTax]
// converts a whole line by DIRECT struct conversion and Go requires the slice's
// element type to be THE SAME type — not merely a convertible one. That
// conversion is the gate that stops a new field from being dropped in silence,
// and it is worth more than the purity of keeping the tags in one file further
// out.
type TaxComponent struct {
	// RateID is the id of the applied rate; it can be empty for an external
	// provider that has no ids of its own.
	RateID string `json:"rate_id"`
	// RateBps is the applied rate (basis points; 2000 = 20%).
	RateBps int32 `json:"rate_bps"`
	// Compound says the component was computed on the line's amount PLUS the
	// taxes below it, rather than on the amount alone.
	//
	// It travels with the amounts because the two together are what makes the
	// figures reproducible: without it the reader cannot tell why a component's
	// base is larger than the line's.
	Compound bool `json:"compound"`
	// TaxableAmount is the base THIS component was computed on (minor unit).
	TaxableAmount int64 `json:"taxable_amount"`
	// TaxAmount is the tax this component produced (minor unit).
	TaxAmount int64 `json:"tax_amount"`
}

// validateComponents checks a provider's per-component breakdown of one line.
//
// # An empty list is legal and means something
//
// It says the line was taxed by ONE rate — the one the line already carries.
// Every provider that has never heard of stacking therefore stays correct
// without changing, and a consumer reads "components present" as "this line was
// taxed by a stack" rather than having to compare a list of one against the
// line.
//
// That is also why a list of ONE is refused: it would say nothing the line does
// not already say, while making the two readings above ambiguous.
//
// # The sum is checked, the split is not
//
// Σ component tax must equal the line's tax, because that identity is what lets
// a document print the components INSTEAD of the line's figure. How a provider
// arrives at each component — which base, in what order — is its own business;
// pinning that here would refuse a legitimate market this module has not met.
func validateComponents(
	providerID, lineID string, components []TaxComponent, lineTax int64,
) error {
	if len(components) == 0 {
		return nil
	}
	if len(components) < 2 {
		return errors.Internal(CodeProviderInvalidResult,
			"provider %q returned a single tax component for line %q; a breakdown "+
				"of one says nothing the line does not, so it must be left empty",
			providerID, lineID)
	}
	if len(components) > maxStackDepth {
		return errors.Internal(CodeProviderInvalidResult,
			"provider %q returned %d tax components for line %q; at most %d are allowed",
			providerID, len(components), lineID, maxStackDepth)
	}

	var total int64
	for i := range components {
		c := components[i]
		if c.RateBps < models.MinRateBps || c.RateBps > models.MaxRateBps {
			return errors.Internal(CodeProviderInvalidResult,
				"provider %q returned an out-of-contract rate in component %d of line %q: "+
					"%d basis points ([%d, %d] expected)",
				providerID, i, lineID, c.RateBps, models.MinRateBps, models.MaxRateBps)
		}
		if c.TaxAmount < 0 || c.TaxAmount > c.TaxableAmount {
			return errors.Internal(CodeProviderInvalidResult,
				"provider %q returned an out-of-contract tax in component %d of line %q: "+
					"%d on a base of %d",
				providerID, i, lineID, c.TaxAmount, c.TaxableAmount)
		}

		sum, err := addAmount(total, c.TaxAmount)
		if err != nil {
			return err
		}
		total = sum
	}

	if total != lineTax {
		return errors.Internal(CodeProviderInvalidResult,
			"provider %q returned components for line %q that do not add up to the "+
				"line's tax: %d components total %d, the line says %d",
			providerID, lineID, len(components), total, lineTax)
	}

	return nil
}
