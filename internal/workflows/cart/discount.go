package cart

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// This file is the DISCOUNT leg of the cart calculation (plan Phase 7).
//
// In Phase 5 the discount was ALWAYS ZERO and the field was left with a
// "promotion will take this over" note; the takeover happens here. The promotion
// module does the calculation, this package only translates the shape of the cart
// into its contract, VALIDATES the result and writes it onto the lines.

// attrVariantID is the line item attribute that discount rules may look at.
//
// The cart line's variant is the catalog fact this workflow holds without asking
// anybody. A category id could be a rule target too and is deliberately NOT here:
// ADR 0048 licensed exactly two more names into this map and said the day a rule
// wants a category is a separate argument with a separate read.
const attrVariantID = "variant_id"

// The product's two merchandising flags, as a catalog field name AND as a line
// attribute a promotion rule names (ADR 0048).
//
// # One string doing two jobs is the decision, not an accident
//
// The value is asked of product's Query provider as a FIELD and put into the
// line's attribute map as a KEY. Translating between the two would mean the
// merchant writing "is_giftcard ne true" against the name the catalog publishes
// while the cart handed the engine some other spelling — the rule would then
// match nothing, no line would be excluded, and a promotion would fall on gift
// cards with no error anywhere.
//
// # Nothing compares these literals with the ones product declares
//
// This package cannot import the product module (ADR 0006), so the names are
// repeated here, which is the accepted price of isolation (ADR 0001). Half of
// the drift is loud: the Query layer refuses a field its provider does not
// publish, so a RENAMED column fails the read. The other half is silent, and
// ADR 0048 wrote it down as a cost rather than hiding it — a shop's gift-card
// rule can stop matching without a single error being raised. The audit that
// would close it is the one ADR 0040 asked for and did not build either.
const (
	// attrIsGiftcard says whether the line is a gift card, which a rule such as
	// "is_giftcard ne true" may then exclude.
	attrIsGiftcard = "is_giftcard"
	// attrDiscountable says whether a promotion may fall on the line at all.
	attrDiscountable = "discountable"
	// attrProductID is the line's PRODUCT, which a rule may name directly.
	//
	// The variant was there from the start and the product was not, which made
	// "20% off this product" impossible to write: a merchant had to name every
	// variant of it and name the new ones as they were added.
	attrProductID = "product_id"
	// attrCollectionID is the collection the line's product belongs to.
	//
	// It is the one taxonomy a product carries as a COLUMN, so it is the one
	// that fits an attribute map holding a single value per key. A category and
	// a tag are lists — see ADR 0103 for why they are not here.
	attrCollectionID = "collection_id"
	// attrTypeID is the product's TYPE, which a tax rate rule may match on.
	//
	// It sits with the other two because it is read the same way — a field of
	// the product record the catalog publishes — even though its consumer is the
	// tax module rather than the promotion engine.
	attrTypeID = "type_id"
	// attrCategoryIDs and attrTagIDs are the line's product's MEMBERSHIPS, and
	// they are lists rather than single values (ADR 0148).
	//
	// A product is in as many categories and carries as many tags as the merchant
	// filed it under, so neither fits the attribute map: they go in the line's
	// LIST map, where the `any_in` operator reads them (ADR 0144). The names are
	// the field names the catalog publishes, for [attrIsGiftcard]'s reason — one
	// string doing two jobs beats two spellings of one concept.
	attrCategoryIDs = "category_ids"
	attrTagIDs      = "tag_ids"
)

// EntityProduct is the entity name of products in the Query layer; the product
// module declares its provider under this name.
//
// It lives beside its consumer rather than with the other cross-module names in
// deps.go because the discount leg is the only reader of the product entity in
// this package: the variant hop that precedes it is a VARIANT read
// ([Workflows.productIDsFor]).
const EntityProduct = "product"

// productFacts are the two merchandising answers a discount rule may look at.
//
// The type carries no "known" flag: a variant with no entry in the map the
// lookup returns is a variant whose product could not be resolved, and the
// caller leaves the attributes OFF the line rather than sending a made-up
// value. Sending false for an unknown gift card would be a lie the engine
// cannot see through.
type productFacts struct {
	// IsGiftcard is the product's is_giftcard column: what the product IS.
	IsGiftcard bool
	// Discountable is the product's discountable column: what may be DONE to it.
	//
	// The two are separate columns because they are separate statements, and a
	// shop running a promotion ON gift cards is a legitimate configuration
	// rather than a contradiction.
	Discountable bool
	// ProductID is the product the line's variant belongs to.
	ProductID string
	// CollectionID is the collection that product is filed under; empty when it
	// is in none.
	CollectionID string
	// CategoryIDs and TagIDs are the product's memberships, in the merchant's
	// rank order (ADR 0148).
	//
	// They are lists because a product is in as many categories as it was filed
	// under, which is why they reach the engine through the line's LIST map rather
	// than its attribute map. They ride with the flags for [productFacts.TypeID]'s
	// reason: the same record, the same read.
	CategoryIDs []string
	TagIDs      []string
	// TypeID is the product's TYPE, and its consumer is the TAX module rather
	// than the promotion engine: a rate rule matches on it (ADR 0101).
	//
	// It rides with the flags because it is read from the same record in the
	// same query, and the totals path reads that record ONCE — two reads of one
	// product row on the path that runs on every cart update is exactly the N+1
	// this file's own tests were written to keep out.
	TypeID string
}

// discountRequest is the JSON schema of the discount request that goes to the
// promotion module.
//
// The field names MUST be EXACTLY the same as promotion's interop schema: the
// other side REJECTS unknown fields, and because the two packages cannot import
// each other the compiler cannot see the match (the accepted price of ADR 0006).
// The match can only be proven by an integration test.
//
// All amounts are INTEGER minor units (plan Section 8).
type discountRequest struct {
	// CurrencyCode is the cart's currency; filtering out fixed-amount
	// promotions rests on it.
	CurrencyCode string `json:"currency_code"`
	// Context holds the fields that context rules will look at.
	Context map[string]string `json:"context"`
	// ContextLists holds the fields a context rule reads as a SET.
	//
	// A sibling of Context and not a replacement: the receiving side rejects
	// unknown fields, so a retype would break it, and a shipped rule's answer has
	// to stay exactly what it was. Only the operator that reads a list looks here
	// (ADR 0144).
	ContextLists map[string][]string `json:"context_lists"`
	// Items are the cart's lines and they go in the cart's ORDER.
	Items []discountRequestItem `json:"items"`
	// ShippingMethods is ALWAYS EMPTY; the rationale is in the
	// [Workflows.discountRequestFor] godoc.
	ShippingMethods []discountRequestShipping `json:"shipping_methods"`
	// Codes are the coupon codes to apply; they come off the CART's own rows
	// (ADR 0109) and never off the call that asked for the calculation.
	Codes []string `json:"codes"`
	// At is the instant of the calculation; it is left empty and promotion uses
	// "now".
	//
	// A cart calculation ALWAYS belongs to now: a backdated calculation would
	// show a campaign that ended today as live in the cart.
	At string `json:"at"`
}

// discountRequestItem is the schema of a single cart line in the request.
type discountRequestItem struct {
	// ID is the cart line's ID; the discount comes back under the same ID.
	ID string `json:"id"`
	// Amount is the line's PRE-DISCOUNT subtotal (unit x quantity).
	Amount int64 `json:"amount"`
	// UnitAmount is the line's unit price, and the other side REQUIRES it to be
	// exactly Amount / Quantity.
	//
	// It is sent rather than left to be derived because the receiver's division
	// would round: a "buy two, get one" reward is measured per UNIT, and this
	// package is the one that already knows the number ([LineTotals.UnitPrice]).
	UnitAmount int64 `json:"unit_amount"`
	// Quantity is the quantity on the line; it determines how many units a "fixed
	// amount per unit" discount applies to.
	Quantity int64 `json:"quantity"`
	// Attributes are the line attributes that target rules will look at.
	Attributes map[string]string `json:"attributes"`
	// Lists are the line attributes a target rule reads as a SET (ADR 0148).
	//
	// A sibling of Attributes for the reason [discountRequest.ContextLists] is a
	// sibling of Context: the receiving side rejects unknown fields, so a retype
	// would break it, and a rule that was shipped against a single value has to
	// keep the answer it has. Only the `any_in` operator looks here.
	Lists map[string][]string `json:"lists"`
}

// discountRequestShipping is the schema of a single shipping method in the
// request.
//
// It carries NO list map, and the absence is a statement rather than an omission:
// a shipping method is in no category and carries no tag, so a field for it would
// be a promise nothing could ever fill. A rule asking a shipping target about a
// category therefore matches nothing, which is the right answer.
//
// The type exists only so that the SCHEMA is COMPLETE; this package never sends a
// shipping method.
type discountRequestShipping struct {
	// ID is the shipping method's ID.
	ID string `json:"id"`
	// Amount is the shipping amount (minor units).
	Amount int64 `json:"amount"`
	// Attributes are the attributes that target rules will look at.
	Attributes map[string]string `json:"attributes"`
}

// discountResponse is the JSON schema of the discount result returned by the
// promotion module.
//
// Unknown fields are SILENTLY SKIPPED (the opposite of the request): when
// promotion grows its schema, this package must not have to be updated in the same
// round. The silence is only for UNRECOGNIZED fields — the invariants that the
// recognized fields carry are VALIDATED one by one inside
// [Workflows.applyDiscounts].
type discountResponse struct {
	// CurrencyCode is the currency of the calculation (UPPERCASE).
	CurrencyCode string `json:"currency_code"`
	// Items are the per-item discounts; it carries one record for EVERY item in
	// the request and is in the SAME order as the request.
	Items []discountLine `json:"items"`
	// ShippingMethods are the per-shipping-method discounts; because this package
	// sends no shipping, it is expected to be EMPTY.
	ShippingMethods []discountLine `json:"shipping_methods"`
	// ItemsDiscountTotal is the total discount falling on the items.
	ItemsDiscountTotal int64 `json:"items_discount_total"`
	// ShippingDiscountTotal is the total discount falling on shipping; zero is
	// expected.
	ShippingDiscountTotal int64 `json:"shipping_discount_total"`
	// DiscountTotal is the total discount.
	DiscountTotal int64 `json:"discount_total"`
	// Applied names the promotions that actually produced a discount, IN THE
	// ORDER they were applied.
	//
	// It is read because the cart has to REMEMBER it: the redemption at order
	// time is addressed per promotion and takes an amount, and it has to be the
	// amount the customer was shown. A consumer that dropped this field would
	// leave the order unable to say which coupon to spend — which is the fault
	// ADR 0102 found one boundary over, in the other direction.
	Applied []discountApplied `json:"applied"`
	// UnmatchedCodes are the codes that could not be tied to a usable
	// promotion.
	//
	// It is read for the record and not for a refusal: a code that stops being
	// usable while it sits in the cart must not make the cart unpriceable. What
	// refuses an unusable code is [Workflows.ApplyPromotionCode], at the moment
	// it is typed.
	UnmatchedCodes []string `json:"unmatched_codes"`
}

// discountApplied is one promotion that produced a discount.
type discountApplied struct {
	// PromotionID is the promotion module's identity; it is kept opaque.
	PromotionID string `json:"promotion_id"`
	// Code is the coupon code; the producer always sends one.
	Code string `json:"code"`
	// IsAutomatic reports whether the promotion needed no code.
	IsAutomatic bool `json:"is_automatic"`
	// Amount is the discount this promotion produced (minor unit).
	Amount int64 `json:"amount"`
}

// codesOrEmpty makes sure the request carries an ARRAY and never a null.
func codesOrEmpty(codes []string) []string {
	if codes == nil {
		return []string{}
	}

	return codes
}

// discountLine is the schema of a single line discount in the response.
type discountLine struct {
	// ID is the line the discount belongs to.
	ID string `json:"id"`
	// Amount is the TOTAL discount falling on the line (minor units).
	Amount int64 `json:"amount"`
}

// applyDiscounts takes the lines' discount from the promotion module and WRITES it
// onto the lines.
//
// The lines' subtotals must already have been calculated; tax, on the other hand,
// has NOT YET been calculated. The order is the contract itself: the tax base is
// POST-DISCOUNT (see the package comment, "Tax contract"), and if the discount
// were not known before tax the base would stay wrong.
//
// # If promotion is NOT registered
//
// The discount stays ZERO and the calculation continues. The same pattern exists
// in the product module's storefront listing (if there is no price/stock provider
// the catalog comes back without prices): modularity itself demands it — the cart
// must work in a deployment that does not install promotion. The direction is safe
// too; a missing discount OVERCHARGES the customer, and the customer sees that and
// says so. The reverse direction (a missing tax) would silently come out of the
// merchant's own pocket, and that is why tax does not fall back to zero (see
// [Workflows.applyTaxes]).
//
// The presence of the surface is logged once at STARTUP ([FromContainer]); no
// per-round warning is produced here.
//
// # The returned result is VALIDATED
//
// promotion's godoc promises three invariants: a response line for every request
// line IN THE SAME ORDER, a line discount that does not exceed the line amount,
// and the identity of the totals. Validating something that was promised may look
// unnecessary, but the compiler does not check the other side of the boundary, and
// if a BROKEN discount passes silently the result is a cart that trips the cart
// module's totals check or, worse, does not trip it and shows the customer a wrong
// amount. A contract violation is an errors.Internal: there is nothing the caller
// can fix.
func (w *Workflows) applyDiscounts(
	ctx context.Context, snap Snapshot, lines []LineTotals, facts map[string]productFacts,
) ([]AppliedPromotion, error) {
	if w.discounts == nil {
		return nil, nil
	}
	if len(lines) != len(snap.Items) {
		return nil, errors.Internal(CodeDiscountInvalid,
			"line count does not match the snapshot: %d calculated, %d lines (%s)",
			len(lines), len(snap.Items), snap.ID)
	}

	// The facts are read ONCE per round by [Workflows.computeTotals] and handed
	// in, because [Workflows.discountRequestFor] has no error return and the
	// read has two ways to fail. A failure is not fatal; the rationale is in
	// [Workflows.lineProductFacts].
	payload, err := json.Marshal(w.discountRequestFor(ctx, snap, lines, facts))
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeDiscountFailed,
			"discount request could not be encoded to JSON: %s", snap.ID)
	}

	raw, err := w.discounts.ComputeDiscountsJSON(ctx, payload)
	if err != nil {
		// The class is PRESERVED: promotion's Invalid is a contract mismatch, and
		// had it been turned into Internal a fixable wiring error would look like
		// a server failure.
		return nil, errors.Wrap(err, errors.KindOf(err), CodeDiscountFailed,
			"cart discount could not be calculated: %s (%d lines)", snap.ID, len(lines))
	}

	var resp discountResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeDiscountInvalid,
			"discount result could not be decoded: %s", snap.ID)
	}
	if err := applyDiscountResponse(snap, lines, resp); err != nil {
		return nil, err
	}

	return appliedPromotionsOf(resp), nil
}

// appliedPromotionsOf turns the response's own record of what applied into the
// shape the cart stores.
//
// The ORDER is kept: the promotion module applies them in a defined order and
// the position is part of what the record means. The position is the INDEX
// rather than a field on the wire, so the two cannot disagree.
func appliedPromotionsOf(resp discountResponse) []AppliedPromotion {
	out := make([]AppliedPromotion, 0, len(resp.Applied))
	for i := range resp.Applied {
		out = append(out, AppliedPromotion{
			PromotionID: resp.Applied[i].PromotionID,
			Code:        resp.Applied[i].Code,
			Amount:      resp.Applied[i].Amount,
		})
	}

	return out
}

// discountRequestFor translates the shape of the cart into promotion's request
// schema.
//
// # Shipping methods are NOT SENT
//
// promotion can apply a discount to shipping too, but the [Totals] schema has NO
// field to carry a shipping discount: the cart's discount is the sum of the line
// discounts, and the cart module applies the "a discount cannot exceed the
// subtotal" rule against the subtotal (which excludes shipping). Folding the
// shipping discount into the cart discount would push the discount above the
// subtotal on a cart with cheap goods but expensive shipping, and cart would reject
// the WHOLE calculation. Subtracting it from [Totals.ShippingTotal] would instead
// hide the discount somewhere invisible — the customer could not read on any line
// why shipping got cheaper.
//
// That is why shipping is NOT DRAWN INTO the calculation; the shipping discount is
// opened up the day [Totals] gains a "shipping_discount_total" field, and the place
// it will be wired to is this empty slice in the request.
//
// # What goes into the context
//
// The region, the cart's own metadata (ADR 0111) and the customer's groups. This
// paragraph said the opposite for a long time — "the customer group is NOT put
// into the context … added here the day the customer surface publishes the group
// list" — and that day had come and gone: [Workflows.ruleContext] has written it
// since ADR 0049 published the ranked list. The sentence survived the change, and
// a later round would have cited it as proof the leg was missing (ADR 0144).
//
// The groups go in TWICE and the two are different questions. The merchant-ranked
// HEAD goes into Context, where `eq` and `in` read it and every shipped rule keeps
// its exact answer. ALL of them go into ContextLists, where only `any_in` reads
// them — which is what lets a rule ask "is this customer in any of these groups"
// about a customer whose head is not the one the rule names.
//
// # The line attributes carry the product's two flags
//
// flags comes from [Workflows.lineProductFacts] and may be nil, which is what a
// failed read looks like from here. What each key does to the calculation is the
// merchant's business: the promotion engine's rule attribute name space is open
// by construction, so "discountable eq true" and "is_giftcard ne true" are rules
// a shop writes, and a shop that writes neither has two keys the engine never
// looks at (ADR 0048).
func (w *Workflows) discountRequestFor(
	ctx context.Context, snap Snapshot, lines []LineTotals, flags map[string]productFacts,
) discountRequest {
	items := make([]discountRequestItem, 0, len(lines))
	for i := range lines {
		items = append(items, discountRequestItem{
			ID:         lines[i].LineItemID,
			Amount:     lines[i].Subtotal,
			UnitAmount: lines[i].UnitPrice,
			Quantity:   snap.Items[i].Quantity,
			Attributes: lineAttributes(snap.Items[i].VariantID, flags),
			Lists:      lineLists(snap.Items[i].VariantID, flags),
		})
	}

	attributes, lists, groupErr := w.ruleContext(ctx, snap)
	if groupErr != nil {
		w.log.WarnContext(ctx, "the customer's groups could not be read; discounting without a segment",
			"error", groupErr, "customer_id", snap.CustomerID)
	}

	return discountRequest{
		CurrencyCode:    snap.CurrencyCode,
		Context:         attributes,
		ContextLists:    lists,
		Items:           items,
		ShippingMethods: []discountRequestShipping{},
		Codes:           codesOrEmpty(snap.PromotionCodes),
	}
}

// applyDiscountResponse VALIDATES the response and writes it onto the lines.
//
// All of the validation stands in one place so that a forgotten rule can be seen
// by eye. The write is done after ALL of the validation passes: half-written lines
// would leave the caller with an inconsistent slice even when an error is
// returned.
func applyDiscountResponse(snap Snapshot, lines []LineTotals, resp discountResponse) error {
	if !strings.EqualFold(resp.CurrencyCode, snap.CurrencyCode) {
		return errors.Internal(CodeDiscountInvalid,
			"discount was calculated in a different currency: cart %q, result %q (%s)",
			snap.CurrencyCode, resp.CurrencyCode, snap.ID)
	}
	if len(resp.Items) != len(lines) {
		return errors.Internal(CodeDiscountInvalid,
			"discount result returned a wrong record count: %d lines, %d records (%s)",
			len(lines), len(resp.Items), snap.ID)
	}
	if resp.ShippingDiscountTotal != 0 || len(resp.ShippingMethods) != 0 {
		return errors.Internal(CodeDiscountInvalid,
			"a shipping discount came back although no shipping method was sent: %d (%s)",
			resp.ShippingDiscountTotal, snap.ID)
	}

	var sum int64
	for i := range resp.Items {
		line := resp.Items[i]
		if line.ID != lines[i].LineItemID {
			return errors.Internal(CodeDiscountInvalid,
				"discount result did not preserve the request order: record %d is %q, expected %q (%s)",
				i, line.ID, lines[i].LineItemID, snap.ID)
		}
		if line.Amount < 0 || line.Amount > lines[i].Subtotal {
			return errors.Internal(CodeDiscountInvalid,
				"line discount must be in the range [0, %d]: %q -> %d (%s)",
				lines[i].Subtotal, line.ID, line.Amount, snap.ID)
		}

		var err error
		if sum, err = addAmount(sum, line.Amount); err != nil {
			return err
		}
	}

	// The cart discount is Σ of the line discounts. promotion reports the same
	// identity with its own total as well; the two diverging means the discount
	// written onto the lines differs from the one written onto the cart, and cart's
	// totals check would only catch that at write time.
	if sum != resp.ItemsDiscountTotal || resp.DiscountTotal != resp.ItemsDiscountTotal {
		return errors.Internal(CodeDiscountInvalid,
			"discount total does not match the line discounts: Σ=%d, items total=%d, grand total=%d (%s)",
			sum, resp.ItemsDiscountTotal, resp.DiscountTotal, snap.ID)
	}

	for i := range resp.Items {
		lines[i].DiscountTotal = resp.Items[i].Amount
	}
	return nil
}

// stringList reads a record value that should be a list of ids.
//
// # Why every other shape becomes an EMPTY list rather than an error
//
// The field is published by product's provider, so a renamed one fails the read
// above — that half is loud. What can still arrive is a value of another shape,
// from a provider that is not the product module's, and the choice there is the
// same one [Workflows.productFactsFor] makes for a flag of the wrong type: the
// line ends up with nothing to say about its categories, so a rule asking about
// them does not match it. The opposite — guessing — would put a discount on a
// line whose membership nobody read.
//
// `[]any` is the shape a record that has been through JSON carries, and `[]string`
// the one an in-process provider hands over. Both are read; anything else is
// dropped.
func stringList(value any) []string {
	switch list := value.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}

		return out
	default:
		return nil
	}
}

// lineAttributes builds one line's attribute map for the discount request.
//
// A variant with no flags is given the variant id ALONE, and the two keys are
// simply absent. That is the safe direction and it is the engine's own rule
// rather than a convention invented here: a promotion rule naming an attribute
// the line does not carry DOES NOT MATCH, even a negative one, so the line drops
// out of that promotion's targets and receives no discount. Sending "false" for
// a product nobody could read would instead hand the engine an answer this
// package does not have.
func lineAttributes(variantID string, flags map[string]productFacts) map[string]string {
	attributes := map[string]string{attrVariantID: variantID}

	flag, known := flags[variantID]
	if !known {
		return attributes
	}

	// strconv rather than a hand-written literal: "true"/"false" is what the
	// merchant types into a rule value, and the two spellings have to be the
	// same string for the comparison in matchRule to succeed.
	attributes[attrIsGiftcard] = strconv.FormatBool(flag.IsGiftcard)
	attributes[attrDiscountable] = strconv.FormatBool(flag.Discountable)

	// The product and its collection are written only when they are KNOWN. An
	// empty string is a value a rule can be written against — a merchant could
	// not type it, but a rule stored with an empty value would match every line
	// whose product is in no collection — and the engine's own rule is that a
	// line missing an attribute simply does not match. Absent is the safe word.
	if flag.ProductID != "" {
		attributes[attrProductID] = flag.ProductID
	}
	if flag.CollectionID != "" {
		attributes[attrCollectionID] = flag.CollectionID
	}

	return attributes
}

// lineLists builds one line's LIST attributes for the discount request.
//
// # An empty membership is sent as an absent key, not an empty list
//
// The engine's `any_in` answers false for an empty list, so the two would behave
// the same today — and they would stop behaving the same the day an operator gets
// an operator meaning "in none of these". Absent says "this line has nothing to
// say about categories", which is the fact; an empty list says "it is in zero
// categories", which is a claim this package cannot make about a product it could
// not read.
//
// A variant with no facts therefore gets NOTHING here, not even a key.
func lineLists(variantID string, flags map[string]productFacts) map[string][]string {
	flag, known := flags[variantID]
	if !known {
		return nil
	}

	lists := map[string][]string{}
	if len(flag.CategoryIDs) > 0 {
		lists[attrCategoryIDs] = flag.CategoryIDs
	}
	if len(flag.TagIDs) > 0 {
		lists[attrTagIDs] = flag.TagIDs
	}
	if len(lists) == 0 {
		return nil
	}

	return lists
}

// lineProductFacts resolves the two product flags of every line of the cart,
// keyed by VARIANT so the caller can look them up line by line.
//
// # It costs two batch reads and neither of them is per line
//
// A cart line knows its variant and the flags live on the PRODUCT, so the hop
// comes first ([Workflows.productIDsFor], one Graph for the whole cart) and the
// flags follow (one Graph for the whole cart). Both are paid once per
// CALCULATION, not once per line — which is the half the old comment on
// [attrVariantID] got wrong when it called this an extra round trip per line.
//
// The tax leg makes the SAME hop on the path where the tax module answers
// ([Workflows.applyModuleTax]). Hoisting the resolution so that both legs share
// one — it would live in [Workflows.computeTotals] and be handed to both — is
// the shape ADR 0048 costed, and it is NOT built: on that path a discounting
// cart therefore resolves the products twice. On the two region-rate paths
// there is no hop to hoist and the discount leg pays it alone, which is the
// price the record already names.
//
// # It is not read at all when nothing would look at it
//
// The only caller is [Workflows.applyDiscounts], which returns before this on an
// installation with no promotion module — so a shop that does not discount pays
// nothing, and neither does a cart with no lines.
//
// # A failure is degradation, not a dropped cart
//
// The error is returned so the caller can log it, and the caller carries on with
// no flags. The direction is the one this package has already chosen twice: a
// missing discount OVERCHARGES the customer, who sees the price and says so,
// whereas a cart that cannot be priced stops the shop (the same argument as the
// customer-group read in [Workflows.ruleContext], and the mirror of why the tax
// does not fall back to zero). What it costs is written down in ADR 0048: a shop
// whose rules name these attributes gives NO discount at all until the read
// recovers.
//
// # The product read carries no sales channel filter
//
// It could not: the product provider does not accept that filter and refuses a
// filter it does not recognize, which would turn every discounting cart into an
// errors.Invalid. It is not needed either — the scope was applied one call
// earlier, on the VARIANT hop, so a variant outside the request's channels
// resolves to no product and its line simply carries no flags.
func (w *Workflows) lineProductFacts(ctx context.Context, snap Snapshot) (map[string]productFacts, error) {
	variantIDs := snap.VariantIDs()
	if len(variantIDs) == 0 {
		return nil, nil
	}

	productIDs, err := w.productIDsFor(ctx, variantIDs)
	if err != nil {
		return nil, err
	}
	if len(productIDs) == 0 {
		return nil, nil
	}

	flags, err := w.productFactsFor(ctx, uniqueProductIDs(productIDs))
	if err != nil {
		return nil, err
	}

	out := make(map[string]productFacts, len(productIDs))
	for variantID, productID := range productIDs {
		if flag, known := flags[productID]; known {
			out[variantID] = flag
		}
	}
	return out, nil
}

// uniqueProductIDs returns the distinct products of a variant-to-product map, in
// a STABLE order.
//
// Two variants of the same product are one row in the catalog, and asking for it
// twice would grow the query for nothing. The order is sorted rather than the
// map's, because a query whose input order changes from call to call produces
// error messages that cannot be compared between two runs.
func uniqueProductIDs(productIDs map[string]string) []string {
	seen := make(map[string]struct{}, len(productIDs))
	out := make([]string, 0, len(productIDs))
	for _, productID := range productIDs {
		if _, dup := seen[productID]; dup {
			continue
		}
		seen[productID] = struct{}{}
		out = append(out, productID)
	}
	slices.Sort(out)
	return out
}

// productFactsFor reads is_giftcard and discountable for the given products in a
// SINGLE catalog query.
//
// # A product missing from the answer is NOT an error
//
// It simply has no entry, and its lines carry no flag attributes. The rationale
// is [Workflows.productIDsFor]'s: refusing to price a whole cart because one
// product is invisible in the current sales channel trades a rule that matches
// nothing for a shopper who cannot check out at all. A read FAILURE is a
// different matter and is returned — a fault is not the same fact as an
// absence.
//
// # A flag that is not a bool is skipped rather than guessed
//
// The provider refuses a field it does not publish, so a renamed column fails
// the read above. A value of the wrong type can still arrive from a provider
// that is not the product module's, and reading it as false would make a gift
// card discountable on a type error. The product is left out of the answer
// instead, which puts its lines on the same footing as a product nobody could
// read: no attributes, so a rule naming them does not match.
func (w *Workflows) productFactsFor(ctx context.Context, productIDs []string) (map[string]productFacts, error) {
	records, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity: EntityProduct,
		Fields: []string{
			query.IDField, attrIsGiftcard, attrDiscountable, attrTypeID, attrCollectionID,
			attrCategoryIDs, attrTagIDs,
		},
		Filters: map[string]any{FilterIDs: productIDs},
		Limit:   len(productIDs),
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"could not read the flags of %d products from the catalog", len(productIDs))
	}

	out := make(map[string]productFacts, len(records))
	for i := range records {
		productID, ok := records[i][query.IDField].(string)
		if !ok || productID == "" {
			continue
		}
		giftcard, giftcardOK := records[i][attrIsGiftcard].(bool)
		discountable, discountableOK := records[i][attrDiscountable].(bool)
		if !giftcardOK || !discountableOK {
			continue
		}
		// The type is OPTIONAL where the two flags are required: a product with
		// no type is the ordinary case, so a missing or non-string value leaves
		// the field empty instead of dropping the product from the answer.
		typeID, _ := records[i][attrTypeID].(string)
		collectionID, _ := records[i][attrCollectionID].(string)

		out[productID] = productFacts{
			ProductID:    productID,
			CollectionID: collectionID,
			IsGiftcard:   giftcard,
			Discountable: discountable,
			TypeID:       typeID,
			CategoryIDs:  stringList(records[i][attrCategoryIDs]),
			TagIDs:       stringList(records[i][attrTagIDs]),
		}
	}
	return out, nil
}
