package service

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; the files beside it stay Turkish.

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// assertStackable refuses every stack the calculation could not act on, at the
// moment it would be written.
//
// The calculation refuses them too, loudly, but by then a merchant has a
// configuration they cannot use and no idea which write made it so. Every rule
// below is therefore decided HERE, under the region's lock the caller already
// holds, and the calculation's copies are the second line of defense.
//
// selfID is the rate being updated, so its own row is skipped while its chain
// is walked; it is empty on a create.
func (s *Service) assertStackable(
	ctx context.Context,
	regionID, stacksOnID string,
	isDefault bool,
	rateBps int32,
	compound bool,
	selfID string,
) error {
	if stacksOnID == "" {
		// A rate that stands on nothing cannot compound: there would be nothing
		// under it to compound on. The schema says the same thing in a CHECK;
		// this says it with the word the operator used.
		if compound {
			return errors.Invalid(CodeStackNotAllowed,
				"bir oran ancak BAŞKA bir oranın üstünde dururken bileşik olabilir")
		}

		return nil
	}

	if isDefault {
		// The default is what applies when no rule matched, so it is chosen
		// directly; a stacked rate is never chosen. One row cannot be both.
		return errors.Invalid(CodeStackNotAllowed,
			"varsayılan oran başka bir oranın üstünde duramaz: varsayılan SEÇİLİR, "+
				"üstte duran oran ise seçilenin genişletilmesiyle uygulanır")
	}

	rates, err := s.repo.ListTaxRates(ctx, regionID)
	if err != nil {
		return err
	}

	byID := make(map[string]models.TaxRate, len(rates))
	standing := make(map[string]models.TaxRate, len(rates))
	for i := range rates {
		if rates[i].ID == selfID {
			continue
		}
		byID[rates[i].ID] = rates[i]
		if rates[i].StacksOnID != nil {
			standing[*rates[i].StacksOnID] = rates[i]
		}
	}

	base, ok := byID[stacksOnID]
	if !ok {
		// The foreign key is composite and would refuse this too, with a
		// constraint name. Saying it here says WHICH id and why.
		return errors.Invalid(CodeStackNotAllowed,
			"üstünde durulacak oran bu bölgede yok: %s", stacksOnID)
	}
	if _, taken := standing[stacksOnID]; taken {
		return errors.Conflict(CodeStackNotAllowed,
			"%s oranının üstünde zaten bir oran duruyor; bir yığın LİSTEDİR, "+
				"iki dal olamaz", stacksOnID)
	}

	if err := s.assertNoRules(ctx, base.ID); err != nil {
		return err
	}
	if err := s.assertExclusiveRegion(ctx, regionID); err != nil {
		return err
	}

	// The chain the new rate joins. The walk goes DOWNWARD from the base — a
	// rate knows what it stands on, not what stands on it — and the order is
	// reversed afterwards, because the stack applies upward and a compound
	// component's base is everything below it.
	chain := make([]models.TaxRate, 0, maxStackDepth+1)
	seen := map[string]bool{}
	for current := base; ; {
		if seen[current.ID] {
			return errors.Conflict(CodeStackNotAllowed,
				"vergi oranı yığını kendine dönüyor: %s", current.ID)
		}
		seen[current.ID] = true
		chain = append(chain, current)

		if current.StacksOnID == nil {
			break
		}
		below, ok := byID[*current.StacksOnID]
		if !ok {
			// The row it stands on is gone (retired, or in another region past
			// a hand-written write). The chain cannot be priced, and guessing
			// its missing half would price a stack nobody configured.
			return errors.Conflict(CodeStackNotAllowed,
				"%s oranının üstünde durduğu oran bu bölgede yok: %s",
				current.ID, *current.StacksOnID)
		}
		current = below
	}

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	chain = append(chain, models.TaxRate{RateBps: rateBps, Compound: compound})

	if len(chain) > maxStackDepth {
		return errors.Conflict(CodeStackNotAllowed,
			"bir yığın en fazla %d bileşen taşıyabilir; bu yazma %d yapardı",
			maxStackDepth, len(chain))
	}

	return assertStackWithinBase(chain)
}

// assertNoRules refuses stacking onto a rate that carries rules.
//
// A ruled rate is chosen by matching; a rate standing on it would then apply
// only when that match happened, which is a second scoping mechanism nobody
// declared. The base of a stack is therefore either the region's default or a
// rate whose own rules decide the whole stack — and the second is the one this
// refuses, because the rule belongs to the base and the stack is not the base.
func (s *Service) assertNoRules(ctx context.Context, rateID string) error {
	rules, err := s.repo.ListTaxRateRules(ctx, rateID)
	if err != nil {
		return err
	}
	if len(rules) > 0 {
		return errors.Conflict(CodeStackNotAllowed,
			"%s oranının kuralları var; kurallı bir oranın üstüne yığın kurulamaz",
			rateID)
	}

	return nil
}

// assertExclusiveRegion refuses a stack where prices include their tax.
//
// TaxIncludedIn's divisor is 10000+rate for exactly ONE rate: extracting n
// components from a gross amount needs a reverse peel whose rounding residue
// has to be assigned to some component by fiat, and ADR 0086's measurement is
// the record of how easily that goes wrong.
func (s *Service) assertExclusiveRegion(ctx context.Context, regionID string) error {
	chain, err := s.regionChainOf(ctx, regionID)
	if err != nil {
		return err
	}
	if pricesIncludeTax(chain) {
		return errors.Conflict(CodeStackNotAllowed,
			"bu bölgede fiyatlar vergi DAHİL yazılıyor; kapsayıcı fiyatta ters hesap "+
				"tek orana göre tanımlıdır (ADR 0086) ve yığın kurulamaz")
	}

	return nil
}

// assertStackWithinBase refuses a stack that would take more than the line.
//
// # Why the check has to exist at all
//
// Each rate is bounded to [0, 10000] on its own, and nothing bounds their sum:
// two legal 6000 bps rates take 1.2x the line. The calculation would then
// return a tax greater than the amount, which validateLine rejects as a
// PROVIDER fault — "the rate can be at most 100%, so the tax can under no
// condition exceed the base" — and the merchant would read an accusation
// against the provider for their own configuration.
//
// # Why it rounds UP
//
// The guard computes on a large notional base with ceiling rounding, so it is
// strictly more pessimistic than the floored arithmetic the calculation runs.
// A stack this accepts can therefore never reach the base in practice; one it
// refuses might have been borderline, and refusing a borderline configuration
// is the cheaper mistake.
func assertStackWithinBase(chain []models.TaxRate) error {
	const notionalBase int64 = 100_000_000

	var taken int64
	for i := range chain {
		base := notionalBase
		if chain[i].Compound {
			base = notionalBase + taken
		}
		// Ceiling division: base <= 2e8 and the rate <= 1e4, so the product is
		// under 2e12 and int64 cannot overflow here.
		component := (base*int64(chain[i].RateBps) + BpsScale - 1) / BpsScale
		taken += component
	}

	if taken > notionalBase {
		return errors.Conflict(CodeStackExceedsBase,
			"bu yığın satırın kendisinden fazlasını alırdı (%d birimde %d); "+
				"oranların toplamı satırı aşamaz", notionalBase, taken)
	}

	return nil
}

// regionChainOf resolves the region's own chain, most specific first.
//
// The chain rather than the row, because both inheritance rules this module has
// — the provider and prices-include-tax — are answered by walking it, and a
// province that says nothing inherits its country's answer.
func (s *Service) regionChainOf(
	ctx context.Context, regionID string,
) ([]models.TaxRegion, error) {
	region, err := s.repo.GetTaxRegion(ctx, regionID)
	if err != nil {
		return nil, err
	}

	province := ""
	if region.ProvinceCode != nil {
		province = *region.ProvinceCode
	}

	return s.repo.ResolveTaxRegions(ctx, region.CountryCode, province)
}

// assertNoStackedRate refuses opening a tax-inclusive region over a stack.
//
// # Why the check has to be on the REGION write too
//
// "Prices include their tax" is inherited down the chain, so the forbidden pair
// — an inclusive chain and a stacked rate — can be produced from either end. The
// rate write refuses the first order (a stack under an inclusive chain); this
// refuses the second (an inclusive province over a chain that already stacks),
// which the rate write can never see because the region does not exist yet.
//
// # Only when the new region says INCLUSIVE
//
// A province that says nothing inherits, and a province that says exclusive
// narrows nothing: in both cases the pair the calculation refuses cannot be
// reached through this write.
func (s *Service) assertNoStackedRate(
	ctx context.Context, parentID string, includes *bool,
) error {
	if includes == nil || !*includes {
		return nil
	}

	parent, err := s.repo.GetTaxRegion(ctx, parentID)
	if err != nil {
		return err
	}

	rates, err := s.repo.ListTaxRates(ctx, parent.ID)
	if err != nil {
		return err
	}
	for i := range rates {
		if rates[i].StacksOnID != nil {
			return errors.Conflict(CodeStackNotAllowed,
				"bu bölgenin üstünde yığınlı bir oran var (%s); fiyatların vergi DAHİL "+
					"yazıldığı bir bölge yığının üzerine açılamaz (ADR 0086)", rates[i].ID)
		}
	}

	return nil
}
