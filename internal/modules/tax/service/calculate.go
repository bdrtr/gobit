package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// ShippingLineID is the fixed id of the shipping line in the result.
//
// Shipping is not a line item and has no id given by the caller; it still has
// to be named in the result so that the JSON surface
// ([Interop.CalculateTaxJSON]) can use the same shape as the line items. A
// fixed name has to be distinguishable from the caller's line item ids:
// starting with an underscore keeps it from colliding with any module's prefix.
const ShippingLineID = "_shipping"

// TaxableItem is a single line item to be taxed.
//
// # The base is AFTER DISCOUNT
//
// [TaxableItem.Amount] is the base computed by the caller with the discount
// SUBTRACTED. This module does not compute discounts and does not see discount
// data; it taxes the base as it is. The decision is exactly the cart flow's
// current contract (internal/workflows/cart, "Tax contract"): tax follows the
// price actually paid, and taxing the pre-discount amount would mean taking
// tax on money never taken from the customer. The cart already subtracts the
// promotion discount (DiscountTotal) before it sends the amount; a discount
// shrinks the base's value, not its DEFINITION.
type TaxableItem struct {
	// ID is the line item's id on the CALLER's side (e.g. a cart line) and is
	// returned as is in the result. This module neither validates nor stores
	// it.
	ID string
	// ProductID is the product id for rule matching; it may be left empty.
	ProductID string
	// ProductTypeID is the product type id for rule matching; it may be left
	// empty.
	ProductTypeID string
	// TaxClassID is the product's TAX CLASS for rule matching, and the CALLER
	// DOES NOT FILL IT: [Service.CalculateTax] resolves it from its own table.
	//
	// The reason is the same as PricesIncludeTax's: the classification is this
	// module's own data, not the caller's. Adding a field to the wire would
	// create an answer that has to be repeated on every cart request and can be
	// filled in wrongly.
	TaxClassID string
	// Amount is the taxable base (minor unit, AFTER DISCOUNT).
	Amount int64
}

// ShippingInput is the calculation input of the shipping line.
type ShippingInput struct {
	// OptionID is the id of the shipping option; it is used for rule matching.
	OptionID string
	// Amount is the shipping amount (minor unit).
	Amount int64
	// Taxable is whether shipping is to be taxed.
	//
	// The default is FALSE and shipping does NOT ENTER the base unless the
	// caller asks for it EXPLICITLY; the reasoning is in the
	// [Service.CalculateTax] godoc.
	Taxable bool
}

// CalculateTaxInput is the input of a tax calculation.
type CalculateTaxInput struct {
	// CountryCode is the ISO 3166-1 alpha-2 code; it is required.
	CountryCode string
	// ProvinceCode is the state/province code; it is optional.
	ProvinceCode string
	// Items are the line items to be taxed; it may be empty.
	Items []TaxableItem
	// Shipping is the shipping line.
	Shipping ShippingInput
}

// ItemTax is the computed tax of a single line item.
type ItemTax struct {
	// ID is the line item's id on the caller's side; on the shipping line it
	// is [ShippingLineID].
	ID string
	// RateID is the id of the rate applied; empty if no rate was found.
	RateID string
	// RateBps is the rate applied (basis points); zero if no rate was found.
	RateBps int32
	// TaxableAmount is the base the tax was computed on (minor unit).
	TaxableAmount int64
	// TaxAmount is the computed tax (minor unit).
	TaxAmount int64
	// Components is the per-rate breakdown when a STACK taxed this line, base
	// first; it is empty when a single rate applied ([RateID], [RateBps]).
	//
	// Σ Components[i].TaxAmount = TaxAmount whenever the list is filled, which
	// is what lets a document print the components instead of the line's own
	// figure (ADR 0096).
	Components []TaxComponent
}

// CalculateTaxResult is the result of a tax calculation.
//
// The identity always holds: TaxTotal = Σ(Items[i].TaxAmount) + Shipping.TaxAmount.
type CalculateTaxResult struct {
	// RegionID is the MOST SPECIFIC region the calculation rests on (the
	// province if there is one, otherwise the country root); empty if no
	// region was found.
	RegionID string
	// PricesIncludeTax says that the calculation was made on tax-INCLUSIVE
	// prices.
	//
	// The field exists so that the caller CAN READ what
	// [ItemTax.TaxableAmount] means: in an inclusive calculation the base is
	// SMALLER than the amount sent, and a caller that did not know that would
	// take the difference for an error. The validation on the cart side
	// branches on exactly this field.
	PricesIncludeTax bool
	// RegionFound is whether a tax region was found for the country.
	//
	// The field is REQUIRED: zero tax can arise for two different reasons —
	// the rate really is zero, or there is no configuration at all for that
	// country. A caller unable to tell the two apart would take a missing
	// configuration for a "tax-free country" and sell silently.
	RegionFound bool
	// ProviderID is the id of the provider that made the calculation; empty if
	// there is no region.
	ProviderID string
	// Items is the per-line tax; it comes back in the input's ORDER.
	Items []ItemTax
	// Shipping is the tax of the shipping line; zero if it was not taxed.
	Shipping ItemTax
	// TaxTotal is the total tax.
	TaxTotal int64
}

// CalculateTax computes the tax for the given country/province and line items.
//
// THIS METHOD IS THE HEART OF THE MODULE. The decisions it makes and their
// reasons:
//
// # 1. The tax base is AFTER DISCOUNT
//
// A line item's [TaxableItem.Amount] field is the base the caller computed by
// subtracting the discount; this module does not see the discount. The
// decision is the same as the cart flow's current contract (the
// internal/workflows/cart package comment): tax follows the price actually
// paid.
//
// # 2. ROUNDING: per line, DOWN
//
// Tax is computed SEPARATELY for each line and rounded DOWN by integer division
// (see [TaxOf]). The total tax is the SUM of the ROUNDED line taxes — it is not
// recomputed over the sum of the line bases.
//
// This distinction produces a DIFFERENCE, and where the difference stays has to
// be documented explicitly: since
// Σ(floor(baseᵢ × rate)) ≤ floor(Σbaseᵢ × rate), the per-line calculation comes
// out at most (number of lines - 1) minor units LESS than a calculation over
// the whole cart. The difference stays IN THE CUSTOMER'S FAVOR; the seller
// never collects too much.
//
// The per-line calculation was chosen for two reasons: (a) on an invoice the
// tax of every line has to be explainable one by one, (b) since DIFFERENT rates
// can apply to different lines, taxing the cart base in one go is not possible
// anyway.
//
// # 3. SHIPPING is NOT TAXED by default
//
// Unless [ShippingInput.Taxable] is given explicitly as true, shipping does not
// enter the base and the shipping tax in the result is zero. The cart flow does
// NOT ADD shipping to the base today, and this module does not change that
// behavior; whether shipping is taxed varies by jurisdiction, and assuming "it
// is the same as the goods" is a silent guess. When taxation is turned on, the
// shipping line chooses its own rate too: a rate matching a "shipping_option"
// rule if there is one, otherwise the region's default rate.
//
// # 4. THE PROVINCE OVERRIDES THE COUNTRY; rates are NOT ADDED
//
// If there is a province region its rates are tried first; the country rate is
// fallen back to only when the province yields no rate at all (neither a
// matching rule nor a default). No addition is made — the reasoning and the
// rejected alternative are in the [LocalProvider] godoc.
//
// # 5. IF THERE IS NO TAX REGION: zero tax, NOT AN ERROR
//
// If no root region is found for the country, every tax comes back zero,
// [CalculateTaxResult.RegionFound] is false, and the situation is logged as a
// WARNING. Returning an error would mean that every cart in a country whose tax
// is not configured yet could not be opened at all; the cart calculation calls
// this method on every round. Silence is not acceptable either, though: the
// RegionFound field and the log entry make the missing configuration VISIBLE to
// the caller, and the caller (e.g. the cart flow) can choose to refuse if it
// wants to.
//
// If the country code's FORMAT is invalid, errors.Invalid is returned; "invalid
// code" and "unconfigured country" are separate situations.
//
// # 6. The provider is INHERITED along the chain, its result is VALIDATED
//
// The [TaxProvider] makes the calculation; which provider is called is said by
// the MOST SPECIFIC NON-EMPTY provider_id in the chain. If the province's field
// is empty the country's provider is inherited — the reasoning is in the
// [Service.providerFor] godoc — and if none is filled in, the local calculation
// applies. The returned result is NOT accepted blindly: every line id has to be
// in the input, no line may be missing or repeated, the rate has to stay within
// [0, 100%] and the tax within [0, base]. The total is not taken from the
// provider either; it is summed again HERE, so that the identity (TaxTotal = Σ
// lines + shipping) does not depend on the provider's correctness.
func (s *Service) CalculateTax(ctx context.Context, in CalculateTaxInput) (CalculateTaxResult, error) {
	if err := s.ready(); err != nil {
		return CalculateTaxResult{}, err
	}

	normalized, err := s.normalizeCalculateInput(in)
	if err != nil {
		return CalculateTaxResult{}, err
	}

	chain, err := s.repo.ResolveTaxRegions(ctx, normalized.CountryCode, normalized.ProvinceCode)
	if err != nil {
		return CalculateTaxResult{}, err
	}
	if len(chain) == 0 {
		s.log.WarnContext(ctx, "no tax region is configured, the tax was computed as zero",
			slog.String("country_code", normalized.CountryCode),
			slog.String("province_code", normalized.ProvinceCode),
			slog.Int("item_count", len(normalized.Items)),
		)
		return zeroResult(normalized), nil
	}

	regionIDs := make([]string, 0, len(chain))
	for i := range chain {
		regionIDs = append(regionIDs, chain[i].ID)
	}

	// The MOST SPECIFIC NON-EMPTY provider_id in the chain decides the
	// provider: a province can choose its own tax authority, and if it did not,
	// it INHERITS the country's.
	provider, err := s.providerFor(chain)
	if err != nil {
		return CalculateTaxResult{}, err
	}

	included := pricesIncludeTax(chain)

	// The line items' tax class is resolved HERE: one query, independent of the
	// number of lines. Before the provider call, because the class is a match
	// key and the provider cannot read it from its own table — an external
	// provider does not have this module's tables.
	if err := s.attachTaxClasses(ctx, normalized.Items); err != nil {
		return CalculateTaxResult{}, err
	}

	raw, err := provider.Calculate(ctx, ProviderInput{
		RegionIDs:        regionIDs,
		CountryCode:      normalized.CountryCode,
		ProvinceCode:     normalized.ProvinceCode,
		Items:            normalized.Items,
		Shipping:         normalized.Shipping,
		PricesIncludeTax: included,
	})
	if err != nil {
		return CalculateTaxResult{}, err
	}

	result, err := assembleResult(normalized, raw, provider.ID(), included)
	if err != nil {
		return CalculateTaxResult{}, err
	}
	result.RegionID = chain[0].ID
	result.RegionFound = true
	result.PricesIncludeTax = included
	return result, nil
}

// attachTaxClasses writes the tax class of their products onto the line items.
//
// One query, independent of the number of lines: however many lines the cart
// has, the number of round trips does not change (no N+1). If no line item has
// a product id, the query is NOT made at all.
//
// A product with no class is ABSENT from the map and the line's class stays
// empty — that line matches only on the product and type keys, which is
// exactly the behavior from before classes existed.
func (s *Service) attachTaxClasses(ctx context.Context, items []TaxableItem) error {
	productIDs := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for i := range items {
		id := items[i].ProductID
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		productIDs = append(productIDs, id)
	}
	if len(productIDs) == 0 {
		return nil
	}

	classes, err := s.repo.ClassesOfProducts(ctx, productIDs)
	if err != nil {
		return err
	}

	for i := range items {
		items[i].TaxClassID = classes[items[i].ProductID]
	}

	return nil
}

// pricesIncludeTax resolves from the region CHAIN whether prices are written
// tax inclusive; the chain runs from the most specific to the general.
//
// The inheritance is the same as [Service.providerFor]'s, and deliberately so:
// a province can give its own answer, and if it did not, it INHERITS the
// country's answer. If nobody gave an answer, prices are tax EXCLUSIVE — that
// is what every installation did before the field existed, and an existing row
// must not acquire a view that was never written into it.
//
// Not inventing a second inheritance rule is a decision too: in the provider
// "empty = inherit", here "nil = inherit". The same chain, the same sentence.
func pricesIncludeTax(chain []models.TaxRegion) bool {
	for i := range chain {
		if chain[i].PricesIncludeTax != nil {
			return *chain[i].PricesIncludeTax
		}
	}
	return false
}

// providerFor resolves the provider of the region CHAIN; the chain runs from
// the most specific to the general.
//
// # An empty provider_id INHERITS
//
// The chain is walked from the most specific to the general and the FIRST
// NON-EMPTY provider_id wins; if none is filled in, [LocalProviderID] applies.
// So an empty field does not mean "local" but "my parent's authority".
//
// The decision closes a money bug. A province region is most often opened for
// A SINGLE EXCEPTION (see [LocalProvider]), and writing a provider into that
// row does not occur to anyone; had an empty value counted as local, in an
// installation whose country is bound to an external authority (Avalara,
// TaxJar …), EVERY cart in that province would silently be taxed from the
// local table. An invoice issued under the wrong authority means the error is
// never noticed.
//
// The rejected alternative: FORBIDDING the child's field to be left empty when
// a province is created while the parent's provider is filled in. The ban would
// leave the empty string meaning "local", so the data model would still be
// unable to express "use my parent's provider", and it would not cover rows
// written directly with SQL at all. With inheritance every intent can be
// expressed: empty = inherit, "local" = explicitly local, any other id = that
// provider.
//
// # An id that is not registered
//
// It is a SETUP error and is turned into KindInternal. Had the registry layer's
// NotFound passed through as is, a client asking for a cart total would get a
// 404 and believe its cart or its product had vanished. Silently falling back
// to local is worse still: an invoice computed with the wrong authority's rate
// means the error is never noticed.
func (s *Service) providerFor(chain []models.TaxRegion) (TaxProvider, error) {
	if s.providers == nil {
		return nil, errors.Internal(CodeProviderMisconfigured,
			"the tax provider registry is not configured")
	}

	regionID, providerID := "", LocalProviderID
	if len(chain) > 0 {
		regionID = chain[0].ID
	}
	for i := range chain {
		if id := strings.TrimSpace(chain[i].ProviderID); id != "" {
			regionID, providerID = chain[i].ID, id
			break
		}
	}

	provider, err := s.providers.Get(providerID)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeProviderMisconfigured,
			"region %s points to the tax provider %q, which is not registered",
			regionID, providerID)
	}
	return provider, nil
}

// normalizeCalculateInput validates and normalizes the calculation input.
//
// Validation is done WITHOUT GOING TO THE DATABASE: running a region query for
// a request whose outcome is known up front would mean a faulty client keeping
// the database busy.
func (s *Service) normalizeCalculateInput(in CalculateTaxInput) (CalculateTaxInput, error) {
	country, err := NormalizeCountryCode(in.CountryCode)
	if err != nil {
		return CalculateTaxInput{}, err
	}
	province, err := NormalizeProvinceCode(in.ProvinceCode)
	if err != nil {
		return CalculateTaxInput{}, err
	}
	if len(in.Items) > MaxItems {
		return CalculateTaxInput{}, errors.Invalid(CodeInvalidInput,
			"a single calculation can have at most %d line items, %d were given", MaxItems, len(in.Items))
	}

	items := make([]TaxableItem, 0, len(in.Items))
	seen := make(map[string]struct{}, len(in.Items))
	for i := range in.Items {
		item := in.Items[i]
		if item.ID == "" {
			return CalculateTaxInput{}, errors.Invalid(CodeInvalidInput,
				"the id of line item %d is empty", i)
		}
		if item.ID == ShippingLineID {
			// The shipping line's id is reserved; if a line item used it, the
			// two lines in the result could not be told apart.
			return CalculateTaxInput{}, errors.Invalid(CodeInvalidInput,
				"the id %q is reserved for the shipping line and cannot be a line item id", ShippingLineID)
		}
		if _, dup := seen[item.ID]; dup {
			return CalculateTaxInput{}, errors.Invalid(CodeInvalidInput,
				"the line item id %q was given more than once", item.ID)
		}
		seen[item.ID] = struct{}{}

		if err := checkTaxableAmount("the line item's tax base", item.Amount); err != nil {
			return CalculateTaxInput{}, err
		}
		items = append(items, item)
	}

	if err := checkTaxableAmount("the shipping amount", in.Shipping.Amount); err != nil {
		return CalculateTaxInput{}, err
	}

	return CalculateTaxInput{
		CountryCode:  country,
		ProvinceCode: province,
		Items:        items,
		Shipping:     in.Shipping,
	}, nil
}

// zeroResult produces a result with no tax; the line items come back in the
// input's order.
func zeroResult(in CalculateTaxInput) CalculateTaxResult {
	items := make([]ItemTax, 0, len(in.Items))
	for i := range in.Items {
		items = append(items, ItemTax{ID: in.Items[i].ID, TaxableAmount: in.Items[i].Amount})
	}

	shipping := ItemTax{ID: ShippingLineID}
	if in.Shipping.Taxable {
		shipping.TaxableAmount = in.Shipping.Amount
	}
	return CalculateTaxResult{Items: items, Shipping: shipping}
}

// assembleResult VALIDATES the provider's output and turns it into the result.
//
// That the validation belongs here and not to the provider is deliberate: the
// provider can be a third party and cannot be expected to check its own output.
// The total is summed here too, over the rounded line taxes — the identity
// (TaxTotal = Σ lines + shipping) does not depend on the provider's arithmetic.
func assembleResult(
	in CalculateTaxInput, raw ProviderResult, providerID string, included bool,
) (CalculateTaxResult, error) {
	byID := make(map[string]ProviderItemTax, len(raw.Items))
	for i := range raw.Items {
		line := raw.Items[i]
		if _, dup := byID[line.ID]; dup {
			return CalculateTaxResult{}, errors.Internal(CodeProviderInvalidResult,
				"provider %q returned two results for line %q", providerID, line.ID)
		}
		byID[line.ID] = line
	}
	if len(byID) != len(in.Items) {
		return CalculateTaxResult{}, errors.Internal(CodeProviderInvalidResult,
			"provider %q was given %d line items and returned %d results",
			providerID, len(in.Items), len(byID))
	}

	out := CalculateTaxResult{
		ProviderID: providerID,
		Items:      make([]ItemTax, 0, len(in.Items)),
	}

	var total int64
	for i := range in.Items {
		line, ok := byID[in.Items[i].ID]
		if !ok {
			return CalculateTaxResult{}, errors.Internal(CodeProviderInvalidResult,
				"provider %q returned no result for line %q", providerID, in.Items[i].ID)
		}
		item, err := validateLine(providerID, line, in.Items[i].ID, in.Items[i].Amount, included)
		if err != nil {
			return CalculateTaxResult{}, err
		}

		total, err = addAmount(total, item.TaxAmount)
		if err != nil {
			return CalculateTaxResult{}, err
		}
		out.Items = append(out.Items, item)
	}

	shippingBase := int64(0)
	if in.Shipping.Taxable {
		shippingBase = in.Shipping.Amount
	}
	shipping, err := validateLine(providerID, raw.Shipping, ShippingLineID, shippingBase, included)
	if err != nil {
		return CalculateTaxResult{}, err
	}
	total, err = addAmount(total, shipping.TaxAmount)
	if err != nil {
		return CalculateTaxResult{}, err
	}

	out.Shipping = shipping
	out.TaxTotal = total
	return out, nil
}

// validateLine validates a single provider line and turns it into a result
// line.
//
// That the upper bound is the BASE is deliberate: since the rate can be at most
// 100%, the tax can under no condition exceed the base. A value that does is
// the most likely sign that the provider mixed up cents and units (or skipped a
// currency conversion), and had it passed silently it would invoice the
// customer twice over.
func validateLine(
	providerID string, line ProviderItemTax, wantID string, amount int64, included bool,
) (ItemTax, error) {
	if line.RateBps < models.MinRateBps || line.RateBps > models.MaxRateBps {
		return ItemTax{}, errors.Internal(CodeProviderInvalidResult,
			"provider %q returned an out-of-contract rate for line %q: %d basis points ([%d, %d] expected)",
			providerID, wantID, line.RateBps, models.MinRateBps, models.MaxRateBps)
	}
	if line.TaxAmount < 0 || line.TaxAmount > amount {
		return ItemTax{}, errors.Internal(CodeProviderInvalidResult,
			"provider %q returned an out-of-contract tax for line %q: %d ([0, %d] expected)",
			providerID, wantID, line.TaxAmount, amount)
	}

	// In a market that is NOT inclusive the base is the amount sent itself, and
	// the base the provider reports is NOT READ. This lets a provider that never
	// fills the field keep working as it does today.
	base := amount
	if included {
		// In an inclusive market the base is REQUIRED and validated. The check
		// is not "is it in range" but EQUALITY: adding the base and the tax has
		// to give the gross that was sent. A range check would let a one-cent
		// drift through and the customer would be charged above the label —
		// which is exactly the reason inclusive pricing exists.
		if line.TaxableAmount < 0 || line.TaxableAmount+line.TaxAmount != amount {
			return ItemTax{}, errors.Internal(CodeProviderInvalidResult,
				"provider %q did not hit the tax-inclusive price for line %q: "+
					"base %d + tax %d = %d, gross sent %d",
				providerID, wantID, line.TaxableAmount, line.TaxAmount,
				line.TaxableAmount+line.TaxAmount, amount)
		}
		base = line.TaxableAmount
	}

	if err := validateComponents(providerID, wantID, line.Components, line.TaxAmount); err != nil {
		return ItemTax{}, err
	}

	return ItemTax{
		ID:            wantID,
		RateID:        line.RateID,
		RateBps:       line.RateBps,
		TaxableAmount: base,
		TaxAmount:     line.TaxAmount,
		Components:    line.Components,
	}, nil
}

// DefaultRateForCountry returns the DEFAULT rate of a country root in basis
// points.
//
// It is the cart flow's plainest path and the counterpart of the region
// module's TEMPORARY RegionTax method; its primitive cross-module signature is
// published as [Interop.RateForCountry].
//
// # What it does NOT EVALUATE
//
// Province regions, rules and shipping are not looked at on this path at all; a
// caller that needs them has to use [Service.CalculateTax]. The region's
// PROVIDER is not called either: asking an external tax service only "what is
// this country's rate" would mean a network call on every cart round, and the
// answers of external services depend on the line item anyway.
//
// # Telling the two situations apart
//
// If found is false, the country's root tax region either does not exist at
// all or has no default rate; the rate is then always zero. Without the
// distinction the caller would take a missing configuration for a "tax-free
// country".
func (s *Service) DefaultRateForCountry(ctx context.Context, countryCode string) (rateBps int32, found bool, err error) {
	if err := s.ready(); err != nil {
		return 0, false, err
	}

	country, err := NormalizeCountryCode(countryCode)
	if err != nil {
		return 0, false, err
	}

	chain, err := s.repo.ResolveTaxRegions(ctx, country, "")
	if err != nil {
		return 0, false, err
	}

	var root models.TaxRegion
	for i := range chain {
		if chain[i].IsRoot() {
			root = chain[i]
			break
		}
	}
	if root.ID == "" {
		return 0, false, nil
	}

	rates, err := s.repo.ListTaxRates(ctx, root.ID)
	if err != nil {
		return 0, false, err
	}
	for i := range rates {
		if !rates[i].IsDefault {
			continue
		}
		// An out-of-contract rate (it may have been written by hand with SQL)
		// does not pass silently: the caller multiplies the amount by it
		// directly, and a 1000% rate would multiply the cart tenfold.
		if rates[i].RateBps < models.MinRateBps || rates[i].RateBps > models.MaxRateBps {
			return 0, false, errors.Internal(CodeRateOutOfRange,
				"rate %s is out of contract: %d basis points ([%d, %d] expected)",
				rates[i].ID, rates[i].RateBps, models.MinRateBps, models.MaxRateBps)
		}
		return rates[i].RateBps, true, nil
	}
	return 0, false, nil
}
