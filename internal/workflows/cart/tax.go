package cart

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// This file is the TAX leg of the cart calculation (plan Phase 7).
//
// In Phase 5 the tax came from the region's single flat rate, and the region's
// godoc had marked that as temporary with "the tax module will take this
// over"; the takeover happens here. The region path is NOT DELETED, it stays
// as the fallback path — which one was used is VISIBLE in the
// [Totals.TaxSource] field.

// Values reporting which source the tax came from ([Totals.TaxSource]).
//
// The existence of the field is this round's most important decision. The only
// acceptable form of moving back and forth between two tax sources is that
// which one answered can be READ IN THE RESULT: the same cart's tax coming out
// differently in two installations is normal, but not being able to understand
// WHERE the difference came from is not.
const (
	// TaxSourceTax reports that the tax module computed the tax and that the
	// country's tax region WAS FOUND.
	TaxSourceTax = "tax"
	// TaxSourceTaxUnconfigured reports that the tax module was called but that
	// the country's tax region IS NOT CONFIGURED; the tax is zero in that case.
	//
	// It is kept apart from [TaxSourceTax] because a zero tax arises from two
	// different reasons: the rate really is zero, or there is no configuration
	// at all for that country. A field that swallowed the distinction would be
	// an invitation to mistake a missing setup for a "tax-free country" (the tax
	// module makes the same distinction with "region_found" as well).
	TaxSourceTaxUnconfigured = "tax_unconfigured"
	// TaxSourceRegion reports that the tax was computed with the region module's
	// rate, that is, over the Phase 5 path.
	TaxSourceRegion = "region"
)

// codeProviderNotFound is the Query layer's "the provider of this entity is not
// registered in the container" error code.
//
// The code is REPEATED HERE because its counterpart in core/query is unexported
// and the only portable link across packages is the error code (the same
// repetition exists in the product module's storefront listing too). If its
// value changes, the calculation ERRORS instead of falling back when the
// region's country cannot be read — which is preferable to being silently more
// permissive.
const codeProviderNotFound = "query_provider_not_found"

// taxRequest is the JSON schema of the tax calculation request going to the tax
// module.
//
// The field names MUST BE EXACTLY the same as tax's interop schema: the other
// side REJECTS unknown fields, and because the two packages cannot import each
// other the compiler cannot see the match (ADR 0006's accepted price).
type taxRequest struct {
	// CountryCode is the ISO 3166-1 alpha-2 code; where it comes from is in the
	// [Workflows.countryForRegion] godoc.
	CountryCode string `json:"country_code"`
	// ProvinceCode is the state/province code and is ALWAYS EMPTY in this phase:
	// the cart only holds a region, the address is not visible from this surface.
	ProvinceCode string `json:"province_code"`
	// Items are the lines to be taxed and go IN THE ORDER they have in the cart.
	Items []taxRequestItem `json:"items"`
	// Shipping is the shipping line; taxable ALWAYS goes as false.
	Shipping taxRequestShipping `json:"shipping"`
}

// taxRequestItem is the schema of a single line in the request.
type taxRequestItem struct {
	// ID is the identity of the cart line; the tax comes back with the same
	// identity.
	ID string `json:"id"`
	// ProductID is for rule matching and is EMPTY in this phase: the cart line
	// knows the variant, it does not know the product.
	ProductID string `json:"product_id"`
	// ProductTypeID is for rule matching and is EMPTY in this phase.
	ProductTypeID string `json:"product_type_id"`
	// Amount is the line's taxable base AFTER DISCOUNT.
	Amount int64 `json:"amount"`
}

// taxRequestShipping is the schema of the shipping line in the request.
type taxRequestShipping struct {
	// OptionID is the identity of the shipping option; it is for rule matching.
	OptionID string `json:"option_id"`
	// Amount is the shipping amount (minor unit).
	Amount int64 `json:"amount"`
	// Taxable is whether the shipping will or will not be taxed.
	Taxable bool `json:"taxable"`
}

// taxResponse is the JSON schema of the calculation result returned from the tax
// module.
//
// Unknown fields are SILENTLY SKIPPED (the opposite of the request): when tax
// grows its schema, this package should not have to be updated in the same
// round. The invariants the recognized fields carry are VALIDATED one by one
// inside [applyTaxResponse].
type taxResponse struct {
	// RegionID is the most specific tax region the calculation rests on; empty if
	// there is no region.
	RegionID string `json:"region_id"`
	// RegionFound is whether a tax region belonging to the country was found.
	RegionFound bool `json:"region_found"`
	// ProviderID is the identity of the provider that did the calculation.
	ProviderID string `json:"provider_id"`
	// TaxTotal is the total tax (minor unit).
	TaxTotal int64 `json:"tax_total"`
	// Items is the per-line tax; it returns IN THE ORDER OF THE REQUEST.
	Items []taxResponseLine `json:"items"`
	// Shipping is the tax of the shipping line; zero is expected because shipping
	// is not taxed.
	Shipping taxResponseLine `json:"shipping"`
}

// taxResponseLine is the schema of a single line's tax in the response.
type taxResponseLine struct {
	// ID is the line the tax belongs to.
	ID string `json:"id"`
	// RateID is the identity of the applied rate; empty if no rate was found.
	RateID string `json:"rate_id"`
	// RateBps is the applied rate (BASIS POINTS; 2000 = 20%).
	RateBps int32 `json:"rate_bps"`
	// TaxableAmount is the base the tax was computed on (minor unit).
	TaxableAmount int64 `json:"taxable_amount"`
	// TaxAmount is the computed tax (minor unit).
	TaxAmount int64 `json:"tax_amount"`
}

// applyTaxes computes the tax of the lines, WRITES it onto the lines and returns
// the SOURCE that was used.
//
// The discount must already have been computed: the tax base is the line's
// subtotal MINUS the line's discount (see the package comment, "Tax contract").
// Shipping does not enter the base and goes into the request as taxable=false —
// the tax module makes shipping optional, this flow does not open that option.
//
// # The authority is SINGLE and is chosen AT SETUP
//
// The ladder has three rungs and the choice is read in the result:
//
//  1. If the tax surface is NOT registered → the region's rate
//     ([TaxSourceRegion]).
//  2. If it is registered but the cart's region does not resolve to a SINGLE
//     country → again the region's rate ([TaxSourceRegion]); tax is never asked.
//  3. If it is registered and the country is known → tax's answer
//     ([TaxSourceTax], or [TaxSourceTaxUnconfigured] if the country has no
//     configuration).
//
// On the third rung tax's answer "this country has no tax region" is accepted AS
// IT IS and there is NO FALLING BACK to region. The distinction is this: there
// tax WAS CALLED and gave an authoritative answer; on the second rung it could
// not be called at all, because which jurisdiction was to be asked was not known
// — an authority that has no answer does not overthrow the previous authority.
// Turning this into moving back and forth according to the data (tax if there is
// configuration in tax, region if not) would be the tax changing silently
// according to which country a tax region happens to be defined for.
//
// # Why the tax does not FALL TO ZERO
//
// If the discount is missing the customer pays MORE and sees it; if the tax is
// missing the seller under-collects, the difference never appears on the invoice
// at all and only comes out at reconciliation. The region's rate is not lost data
// either — Phase 5's authority is still there and the takeover is a WIRING step,
// not a data-deletion step. That is why the fallback is not zero but the previous
// authority.
func (w *Workflows) applyTaxes(
	ctx context.Context,
	snap Snapshot,
	shippingTotal int64,
	lines []LineTotals,
) (string, error) {
	if w.taxes == nil {
		return TaxSourceRegion, w.applyRegionTax(ctx, snap, lines)
	}

	country, reason, err := w.countryForRegion(ctx, snap.RegionID)
	if err != nil {
		return "", err
	}
	if reason != "" {
		w.log.WarnContext(ctx, "the cart's region did not resolve to a single country; the tax is being computed with the region rate",
			slog.String("cart_id", snap.ID),
			slog.String("region_id", snap.RegionID),
			slog.String("reason", reason),
			slog.String("tax_source", TaxSourceRegion),
		)
		return TaxSourceRegion, w.applyRegionTax(ctx, snap, lines)
	}
	return w.applyModuleTax(ctx, snap, country, shippingTotal, lines)
}

// applyRegionTax computes the tax with the region's flat rate (the Phase 5 path).
func (w *Workflows) applyRegionTax(ctx context.Context, snap Snapshot, lines []LineTotals) error {
	rateBps, err := w.taxRate(ctx, snap.RegionID)
	if err != nil {
		return err
	}

	for i := range lines {
		tax, taxErr := taxOf(lines[i].Subtotal-lines[i].DiscountTotal, rateBps)
		if taxErr != nil {
			return taxErr
		}
		lines[i].TaxTotal = tax
	}
	return nil
}

// taxRate returns the tax rate the region is to apply, as basis points.
//
// If the tax is not AUTOMATIC the rate is zero: the region has chosen to leave
// the tax outside instead of computing it itself, and applying the rate anyway
// would silently reverse that choice.
func (w *Workflows) taxRate(ctx context.Context, regionID string) (int32, error) {
	rateBps, automatic, err := w.regions.RegionTax(ctx, regionID)
	if err != nil {
		return 0, err
	}
	if !automatic {
		return 0, nil
	}
	if rateBps < 0 || rateBps > MaxTaxRateBps {
		return 0, errors.Internal(CodeTaxRateInvalid,
			"region %s reported an out-of-contract tax rate: %d basis points ([0, %d] expected)",
			regionID, rateBps, MaxTaxRateBps)
	}
	return rateBps, nil
}

// applyModuleTax takes the tax from the tax module and writes it onto the lines.
func (w *Workflows) applyModuleTax(
	ctx context.Context,
	snap Snapshot,
	countryCode string,
	shippingTotal int64,
	lines []LineTotals,
) (string, error) {
	items := make([]taxRequestItem, 0, len(lines))
	for i := range lines {
		items = append(items, taxRequestItem{
			ID:     lines[i].LineItemID,
			Amount: lines[i].Subtotal - lines[i].DiscountTotal,
		})
	}

	payload, err := json.Marshal(taxRequest{
		CountryCode: countryCode,
		Items:       items,
		// Shipping DOES NOT ENTER the base; the rationale is under the "Tax
		// contract" heading in the package comment. The amount is reported anyway
		// so that the request schema does not change when the tax module is later
		// opened up to taxing shipping.
		Shipping: taxRequestShipping{Amount: shippingTotal, Taxable: false},
	})
	if err != nil {
		return "", errors.Wrap(err, errors.KindInternal, CodeTaxFailed,
			"the tax request could not be converted to JSON: %s", snap.ID)
	}

	raw, err := w.taxes.CalculateTaxJSON(ctx, payload)
	if err != nil {
		// The class is PRESERVED: an invalid country code must stay Invalid, a
		// database outage must stay Unavailable; turning them all into Internal
		// would make a fixable setup error look like a server fault.
		return "", errors.Wrap(err, errors.KindOf(err), CodeTaxFailed,
			"the cart tax could not be computed: %s (%q, %d lines)", snap.ID, countryCode, len(lines))
	}

	var resp taxResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", errors.Wrap(err, errors.KindInternal, CodeTaxInvalid,
			"the tax result could not be decoded: %s", snap.ID)
	}
	if err := applyTaxResponse(snap, lines, resp); err != nil {
		return "", err
	}

	if !resp.RegionFound {
		w.log.WarnContext(ctx, "the country's tax region is not configured; the tax was computed as zero",
			slog.String("cart_id", snap.ID),
			slog.String("country_code", countryCode),
			slog.String("tax_source", TaxSourceTaxUnconfigured),
		)
		return TaxSourceTaxUnconfigured, nil
	}
	return TaxSourceTax, nil
}

// applyTaxResponse VALIDATES the response and writes it onto the lines.
//
// The tax module already validates its provider's output; the second validation
// here is not a copy of that one, it checks the invariants belonging to THIS
// side of the boundary: the line identities are this cart's lines, the order is
// preserved, the base is the one we sent and the total is the sum of the lines.
// Because the compiler does not check the boundary (ADR 0006), this is the only
// protection.
//
// The write is done after the WHOLE of the validation passes: half-written lines
// would leave an inconsistent slice in the caller's hands even when an error is
// returned.
func applyTaxResponse(snap Snapshot, lines []LineTotals, resp taxResponse) error {
	if len(resp.Items) != len(lines) {
		return errors.Internal(CodeTaxInvalid,
			"for %d lines the tax result returned %d records (%s)",
			len(lines), len(resp.Items), snap.ID)
	}
	if resp.Shipping.TaxAmount != 0 {
		return errors.Internal(CodeTaxInvalid,
			"shipping tax was returned even though shipping was asked not to be taxed: %d (%s)",
			resp.Shipping.TaxAmount, snap.ID)
	}

	var sum int64
	for i := range resp.Items {
		line := resp.Items[i]
		base := lines[i].Subtotal - lines[i].DiscountTotal

		if line.ID != lines[i].LineItemID {
			return errors.Internal(CodeTaxInvalid,
				"the tax result did not preserve the order of the request: record %d is %q, expected %q (%s)",
				i, line.ID, lines[i].LineItemID, snap.ID)
		}
		if line.TaxableAmount != base {
			return errors.Internal(CodeTaxInvalid,
				"the tax base differs from the one sent: %q -> %d, sent %d (%s)",
				line.ID, line.TaxableAmount, base, snap.ID)
		}
		// The upper bound being the BASE is deliberate: since the rate can be at
		// most 100%, the tax can under no condition exceed the base, and a value
		// that exceeds it is the most likely sign that cents and whole units have
		// been mixed up.
		if line.TaxAmount < 0 || line.TaxAmount > base {
			return errors.Internal(CodeTaxInvalid,
				"the line tax must be in the range [0, %d]: %q -> %d (%s)",
				base, line.ID, line.TaxAmount, snap.ID)
		}

		var err error
		if sum, err = addAmount(sum, line.TaxAmount); err != nil {
			return err
		}
	}

	// The cart's tax is Σ of the line taxes. tax reports the same identity with
	// its own total as well; the two diverging means that the tax written onto
	// the lines and the one written onto the cart are different.
	if sum != resp.TaxTotal {
		return errors.Internal(CodeTaxInvalid,
			"the tax total does not match the line taxes: Σ=%d, reported=%d (%s)",
			sum, resp.TaxTotal, snap.ID)
	}

	for i := range resp.Items {
		lines[i].TaxTotal = resp.Items[i].TaxAmount
	}
	return nil
}

// countryForRegion reads the COUNTRY code of the cart's region from the Query
// layer.
//
// If the second return value is NOT empty the country could not be resolved and
// its value tells the reason; in that case the first value is meaningless.
//
// # Why the country comes from the region and not from the cart's address
//
// Tax follows the jurisdiction delivered to, so the correct source is the cart's
// shipping address. But that address IS NOT VISIBLE from this boundary: the cart
// module's snapshot schema ([Snapshot]) carries no address and this package
// cannot import cart and grow the schema (ADR 0006). Even if it were visible it
// would not be filled on every round of the calculation — the total is computed
// before an address is entered into the cart as well, and leaving those rounds
// untaxed would mean showing the customer less than what they will pay.
//
// The region, on the other hand, is ALWAYS filled ([Snapshot.validate] rejects an
// empty region) and is already the source of the cart's currency, of the price
// context and, in Phase 5, of the tax. The day the address surfaces, the correct
// order becomes "the address's country first, the region if there is none", and
// this is the place where it will be wired in.
//
// # Why there is a SINGLE country condition
//
// A region can carry more than one country ("Europe"). In that case which
// country's tax is to be applied cannot be derived from the cart's data, and
// picking one from the map would mean tying the tax to an ordering accident. If
// the region is bound to a single country there is no ambiguity; if it is not,
// the country counts as UNRESOLVED and the calculation falls back to region's one
// rate per region — that is the answer the system already gives for a
// multi-country region.
//
// # If Query is not registered
//
// If the region provider is not in the container (if the region module has not
// been opened up to Query) the country counts as unresolved. The fallback is
// narrowed by CODE and not by ERROR CLASS: a NotFound produced inside a
// registered provider, or a database outage, DOES NOT PASS through here and is
// returned to the caller as an error — the tax shifting silently to another
// authority because of a transient fault is the worst outcome that is being
// guarded against.
func (w *Workflows) countryForRegion(ctx context.Context, regionID string) (code, reason string, err error) {
	records, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityRegion,
		Fields:  []string{query.IDField, FieldCountries},
		Filters: map[string]any{query.IDField: regionID},
		Limit:   1,
	})
	if err != nil {
		if errors.CodeOf(err) == codeProviderNotFound {
			return "", "the region provider is not registered in the Query layer", nil
		}
		return "", "", errors.Wrap(err, errors.KindOf(err), CodeRegionReadFailed,
			"region %s could not be read from the Query layer", regionID)
	}
	if len(records) == 0 {
		return "", "the region was not found in the Query layer", nil
	}

	codes := countryCodes(records[0][FieldCountries])
	switch len(codes) {
	case 1:
		return codes[0], "", nil
	case 0:
		return "", "no country is bound to the region", nil
	default:
		return "", "the region is bound to more than one country", nil
	}
}

// countryCodes extracts the ISO codes of the country sub-records in the region
// record.
//
// All three shapes are accepted: the region provider writes []map[string]any,
// Query records may carry query.Record, and a value that has been through a JSON
// round becomes []any. A single type assertion would silently swallow the code
// and show the region as "country-less"; the same tolerance exists in the product
// module's expansion reading as well.
//
// A sub-record whose code cannot be read is SKIPPED, not errored on: the number
// of codes left in the result already determines the decision, and a missing code
// does not wrongly show a plural region as singular — it only makes a singular
// region unresolvable, which is the safe direction.
func countryCodes(value any) []string {
	records := make([]query.Record, 0, 4)
	switch typed := value.(type) {
	case []map[string]any:
		for i := range typed {
			records = append(records, typed[i])
		}
	case []query.Record:
		records = append(records, typed...)
	case []any:
		for i := range typed {
			if record := asCountryRecord(typed[i]); record != nil {
				records = append(records, record)
			}
		}
	default:
		return nil
	}

	out := make([]string, 0, len(records))
	for _, record := range records {
		if code, ok := record[FieldCode].(string); ok && code != "" {
			out = append(out, code)
		}
	}
	return out
}

// asCountryRecord resolves a single country sub-record; returns nil if it cannot
// be resolved.
func asCountryRecord(value any) query.Record {
	switch typed := value.(type) {
	case query.Record:
		return typed
	case map[string]any:
		return typed
	default:
		return nil
	}
}
