package service

import (
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// This file holds the two catalog answers that are computed OVER ANOTHER
// MODULE'S data: whether a product is in stock (ADR 0040) and whether its price
// falls inside a bracket (ADR 0041).
//
// # Why they live in the catalog at all
//
// Measured while ADR 0040 was written, and it is the finding that decided where
// the code goes. The obvious split -- inventory answers for the variant, the
// catalog aggregates to the product -- CANNOT be implemented: manage_inventory
// and allow_backorder are columns of product_variant, inventory_items carries
// neither, and under Principle 2.1 inventory may not read product's models to
// get them. Inventory cannot answer the variant question at all. The catalog
// can: it holds both flags and it already receives the linked inventory record
// through the Query layer, so the three inputs meet in exactly one place and
// that place is [StoreVariant].
//
// The price side lands here for a different reason with the same shape. Pricing
// owns the amounts and publishes them, and its Query provider takes ONE filter
// ("id"): there is no predicate a caller can push down to it. The catalog
// therefore receives the prices it was already receiving for the storefront body
// and applies ADR 0041's five points to them.
//
// # The price of that, stated rather than hidden
//
// [StoreVariant]'s godoc says the enriched records are carried "exactly as they
// came from the Query layer (as loosely typed records)" and that interpreting
// their fields would mean copying another module's schema into this one. The
// constants below are that sentence's exceptions, and they are gathered here so
// that the whole of the cost is one block a reader can count.
//
// It is a real cost because a compiler cannot pay it: a name spelled here and a
// name spelled in another module are two literals with nothing between them, and
// a rename on the far side leaves both trees compiling, both suites green, and
// every product reading as out of stock. That is why the pairing is audited in
// internal/arch/provider_fields_test.go rather than merely commented on -- the
// same shape the admin panel's field names have carried since ADR 0030 made the
// panel an API client.

// The field names this module reads out of ANOTHER MODULE'S loosely typed
// record.
//
// Every one of them carries the prefix "foreign" on purpose, and the prefix is
// load-bearing twice.
//
// It is what the arch audit finds them by, and it takes TWO tests rather than
// one. TestEveryForeignFieldTheCatalogReadsIsDeclared reads this package's
// source, collects every "foreign<Name>" constant and refuses one that is not
// declared in its map; on its own that audits only the names somebody chose to
// write down, so an inline "reserved_quantity" at the read site would slip past
// it -- verified by mutation on 2026-09-08, with the whole suite green.
// TestEveryForeignFieldTheCatalogReadsIsNamedByAConstant closes that: it walks
// the READ SITES and refuses a field name that is not one of these constants.
//
// So a SEVENTH field read cannot arrive through either of the two shapes those
// audits read -- a call to a loose-record reader ([recordInt], [recordString],
// [recordSlice], or any future function taking exactly (query.Record, string)),
// and an index into a query.Record PARAMETER, which is every foreign read this
// package makes today. That is how ADR 0040's sentence -- the catalog gets one
// inventory field and the decision "does not license a second" -- is kept by
// something other than good intentions.
//
// What is NOT held, stated so the edge is where a reader can see it: a foreign
// field reached through a LOCAL or RANGE variable rather than a parameter is
// outside the audit (the one such loop, [Service.enrichVariants], indexes by
// [keyPriceSet] and [keyInventory], which are this module's OWN aliases), and so
// is a name that arrives by reflection or by a JSON round trip into a tagged
// struct. Nothing here does either today.
//
// It also keeps them OUT of the other half of that audit. The scan that collects
// what a module PUBLISHES looks for "Field<Name>" constants, and a name spelled
// "foreignFieldAmount" here would be read back as product publishing "amount" to
// the Query layer -- an audit answering a question with its own input.
const (
	// foreignAvailableQuantity is inventory's sellable total across ALL
	// locations, published as service.FieldAvailableQuantity beside the
	// provider that fills it.
	//
	// It is the ONLY thing this module reads about inventory's shape. Region
	// does not enter the answer (ADR 0040 leaves that to a decision that would
	// first have to say which locations serve which region), and no second
	// inventory field is read here or anywhere else in this package.
	foreignAvailableQuantity = "available_quantity"

	// foreignAvailableByLocation is the same total BROKEN DOWN by warehouse,
	// published by inventory as service.FieldAvailableByLocation.
	//
	// It is asked for ONLY when the read is narrowed to a sales channel's
	// warehouses (ADR 0092), and it NEVER reaches the response: the badge is
	// computed from it and the key is removed from the record before it becomes
	// [StoreVariant.InventoryItem]. A shop's warehouse topology is not a
	// shopper's business, and the record IS published.
	foreignAvailableByLocation = "available_by_location"

	// foreignPrices is the list of price sub-records pricing writes on a
	// price set record. Only the prices that are unconditional and valid at the
	// moment of the read are in it; pricing eliminates the rest before it
	// answers, because a provider carries no rule context and cannot evaluate a
	// condition (see its listablePrices).
	foreignPrices = "prices"
	// foreignPriceListID is nil on a BASE price, and ADR 0041's second
	// point is exactly that absence. There is no default price list in that
	// schema and no column that could mark one -- price_list.type admits only
	// "sale" and "override" -- so "the shop's ordinary price" IS the price that
	// belongs to no list.
	foreignPriceListID = "price_list_id"
	// foreignCurrencyCode is the ISO 4217 code the amount is denominated
	// in, stored uppercase. ADR 0041's first point compares within ONE currency
	// and never converts: a rate is a fact about a moment this repository does
	// not store.
	foreignCurrencyCode = "currency_code"
	// foreignAmount is the amount in minor units, which is the unit the
	// bracket is given in too.
	foreignAmount = "amount"
	// foreignMinQuantity and foreignMaxQuantity bound the quantity
	// tier a price applies to. ADR 0041's third point takes tier ONE: a
	// wholesale price that starts at fifty units is not the number a shopper
	// filtering a catalog is thinking of.
	foreignMinQuantity = "min_quantity"
	foreignMaxQuantity = "max_quantity"
)

// filterQuantity is the quantity ADR 0041 compares at: one unit.
//
// It is written as a constant rather than inlined because it appears in the two
// halves of the tier test and they have to mean the same number.
const filterQuantity int64 = 1

// variantInStock is ADR 0040's definition, for ONE variant.
//
// A variant is in stock when ANY of three things is true, and the order below
// is the order the decision lists them in:
//
//  1. it is not counted (manage_inventory false) -- the merchant is not keeping
//     a number for it, so it is always sellable;
//  2. it may be sold past zero (allow_backorder);
//  3. its available quantity is greater than zero.
//
// # A counted variant with no inventory link is NOT in stock
//
// The link is optional -- [Service.SetVariantInventoryItem] is a call of its own
// and nothing makes it -- so a variant can say it is counted while nothing
// counts it. That case arrives here as a nil or
// unreadable record and the answer is false. Answering "in stock" would be a
// guess in the direction of selling something the shop may not have, and the
// merchant who has not linked the variant yet is the one who can see and fix the
// surprise -- the shopper who bought it cannot.
//
// The same answer is given when the record IS present but carries no readable
// quantity, which is the shape a renamed inventory field would produce. That is
// deliberate and it is why the pairing is audited: the failure is silent in the
// direction of hiding stock, never of selling it.
func variantInStock(variant models.Variant, extra enrichment, served map[string]bool) bool {
	if !variant.ManageInventory || variant.AllowBackorder {
		return true
	}

	if len(served) > 0 {
		return sellableAt(extra.sellableByLocation, served) > 0
	}

	quantity, ok := recordInt(extra.inventory, foreignAvailableQuantity)

	return ok && quantity > 0
}

// sellableAt sums the warehouses the read is allowed to count.
//
// # Why a missing breakdown answers ZERO
//
// The alternative is falling back to the unnarrowed total, and that is the one
// answer this function must never give: it would show a shopper the stock of a
// warehouse their storefront cannot ship from — the very thing the narrowing
// exists to prevent, and the failure would be invisible because the badge would
// look right. Zero is visible: the product reads as out of stock and somebody
// asks why.
//
// The map is nil for a variant with no inventory link at all, which
// [variantInStock] already answers false for, and nil when the breakdown was
// not requested — which cannot happen while a channel narrows the read, because
// that is what makes it requested.
func sellableAt(byLocation map[string]int64, served map[string]bool) int64 {
	var total int64
	for locationID, quantity := range byLocation {
		if served[locationID] {
			total += quantity
		}
	}

	return total
}

// productInStock aggregates the variant answers to the product.
//
// A product is in stock when AT LEAST ONE of its variants is, and a product with
// NO variants is not: there is nothing under it to sell. That is the same
// any-variant rule ADR 0041 gives the price filter, for the same reason -- a
// product is offered if something under it is offered.
func productInStock(variants []StoreVariant) bool {
	for i := range variants {
		if variants[i].InStock {
			return true
		}
	}

	return false
}

// PriceBracket is ADR 0041's filter: a currency and an open or closed interval
// over the BASE price at quantity one.
//
// # Why the currency is required and is not a default
//
// Comparing amounts across currencies is arithmetic on numbers that are not
// comparable, and converting them would make the answer depend on a rate this
// repository does not store -- two consecutive requests could then answer
// differently for a reason no merchant configured. So the bracket carries the
// currency the caller is shopping in, and a bound given without one is refused
// rather than compared against whatever the catalog happens to hold.
//
// # Why the bounds are minor units
//
// Because that is what pricing stores and what the storefront body already
// shows. A decimal here would mean this module deciding how many digits a
// currency has, which is region's answer and not the catalog's.
type PriceBracket struct {
	// CurrencyCode is the ISO 4217 code, uppercase.
	CurrencyCode string
	// Min and Max are inclusive bounds in minor units; nil is an open end.
	Min *int64
	Max *int64
}

// currencyCodeLength is the length of an ISO 4217 code.
const currencyCodeLength = 3

// normalized returns the bracket as it is compared: the currency trimmed and
// UPPER-CASED.
//
// It exists because the two read surfaces hand the code over differently and
// only one of them could reasonably clean it up. REST reads it off a query
// string, where it has to trim anyway to tell "not given" from "given empty";
// GraphQL receives it inside an input object and hands it straight on. Pricing
// stores the code uppercase, so a GraphQL client sending "try" would match
// nothing at all and get an empty catalog with no error -- a filter that fails
// in the direction of returning less, which is the direction nobody diagnoses.
//
// The case fold here is [strings.ToUpper] and not a locale one, and an ISO 4217
// code is three ASCII letters, so there is no Turkish-i hazard to inherit
// (ADR 0038): the alphabet the value may be drawn from does not contain one.
func (b PriceBracket) normalized() PriceBracket {
	b.CurrencyCode = strings.ToUpper(strings.TrimSpace(b.CurrencyCode))

	return b
}

// Validate refuses a bracket that cannot mean anything.
//
// It lives on the TYPE rather than in either read surface, and that placement is
// the point: REST spells the bracket as three flat query parameters and GraphQL
// as one input object, so a rule written at the edge would have to be written
// twice and the two surfaces of one installation would drift into refusing
// different requests. The parsing stays at the edge -- only a query string has
// to turn "1000" into a number -- and every judgement is here.
//
// # A bound without a currency
//
// Refused, because there is no shop default to fall back on and inventing one
// would be a decision ADR 0041 does not take. An amount is comparable only
// within one currency: comparing 500 against every price a catalog holds would
// mix them, and the same product would fall in and out of a bracket depending on
// which currencies a merchant happens to have priced it in.
//
// # A currency without a bound
//
// Refused too. "Priced in TRY" reads like a filter and is a different question
// from "priced between two amounts"; answering it would be building a filter no
// record decides. It also closes a silent failure: a typo in "min_price" would
// otherwise widen the answer to the whole catalog while the client believed it
// had narrowed it.
//
// # A negative bound
//
// Refused rather than clamped. The price table's own constraint is amount >= 0,
// so a negative bound is a client that meant something else, and reading it as
// zero would answer a question nobody asked.
//
// # A reversed pair
//
// Refused, because "min 500, max 100" describes the empty set by construction --
// and an empty answer is exactly what a client sees when its filter is right and
// the catalog is empty, so the one case it could not diagnose is the one it
// caused.
func (b PriceBracket) Validate() error {
	if len(b.CurrencyCode) != currencyCodeLength {
		return invalid(
			"a price filter needs a three-letter ISO 4217 currency code (given: %q); "+
				"prices in different currencies are not comparable and this listing never "+
				"converts them", b.CurrencyCode)
	}
	if b.Min == nil && b.Max == nil {
		return invalid("a price filter needs a lower bound, an upper bound or both; " +
			"a currency on its own narrows nothing")
	}
	if b.Min != nil && *b.Min < 0 {
		return invalid("the lower price bound cannot be negative (given: %d)", *b.Min)
	}
	if b.Max != nil && *b.Max < 0 {
		return invalid("the upper price bound cannot be negative (given: %d)", *b.Max)
	}
	if b.Min != nil && b.Max != nil && *b.Min > *b.Max {
		return invalid("the lower price bound (%d) cannot be greater than the upper one (%d)",
			*b.Min, *b.Max)
	}

	return nil
}

// matchesVariant reports whether a variant's BASE price falls inside the
// bracket.
//
// Every one of ADR 0041's five points is tested here and nowhere else:
//
//   - the currency is the request's, compared exactly against the stored
//     uppercase code;
//   - the price belongs to NO list (price_list_id is null), which is what "the
//     base price" means in a schema that has no default list;
//   - the quantity tier covers one unit;
//   - no customer-group context is consulted, which is visible as the ABSENCE of
//     any rule handling: pricing has already dropped every conditional price
//     before it answers, so a group price cannot reach this comparison;
//   - the comparison runs at the moment of the query, over records fetched for
//     this request.
//
// # A variant with no base price never matches
//
// An override-only variant -- priced solely on a list -- has nothing here to
// compare, and it is left out rather than falling back to its list price. A
// fallback would reintroduce every objection to "the lowest price across lists":
// the product would move in and out of a bracket when a sale opens, with nobody
// having edited anything.
func (b PriceBracket) matchesVariant(priceSet query.Record) bool {
	for _, price := range recordSlice(priceSet, foreignPrices) {
		if b.matchesPrice(price) {
			return true
		}
	}

	return false
}

// matchesPrice tests one price sub-record against the bracket.
func (b PriceBracket) matchesPrice(price query.Record) bool {
	if !isBasePrice(price) {
		return false
	}
	if code, ok := recordString(price, foreignCurrencyCode); !ok || code != b.CurrencyCode {
		return false
	}
	if !coversQuantityOne(price) {
		return false
	}

	amount, ok := recordInt(price, foreignAmount)
	if !ok {
		return false
	}
	if b.Min != nil && amount < *b.Min {
		return false
	}
	if b.Max != nil && amount > *b.Max {
		return false
	}

	return true
}

// isBasePrice reports whether a price belongs to no price list.
//
// The absence is checked in three shapes because the record can arrive by three
// roads: pricing writes a *string that may be a TYPED NIL (which is not equal to
// a nil interface and is the trap this function exists for), a provider that
// round-trips through JSON writes a literal null, and a fake may leave the key
// out altogether.
func isBasePrice(price query.Record) bool {
	raw, present := price[foreignPriceListID]
	if !present || raw == nil {
		return true
	}

	switch value := raw.(type) {
	case *string:
		return value == nil
	case string:
		return value == ""
	default:
		return false
	}
}

// coversQuantityOne reports whether a price's quantity tier includes one unit.
//
// A missing min_quantity is read as "from one", which is what pricing's own
// schema default says; a missing or null max_quantity is an open upper end.
func coversQuantityOne(price query.Record) bool {
	if from, ok := recordInt(price, foreignMinQuantity); ok && from > filterQuantity {
		return false
	}
	if upTo, ok := recordInt(price, foreignMaxQuantity); ok && upTo < filterQuantity {
		return false
	}

	return true
}

// recordSlice reads a list of sub-records off a loose record.
//
// Both shapes a provider can produce are accepted, for the reason [asRecord]
// accepts two: the core writes what the provider returned, so a slice of maps
// and a slice of any both occur, and a type assertion that knew only one of them
// would silently read an empty catalog rather than fail.
func recordSlice(record query.Record, field string) []query.Record {
	if record == nil {
		return nil
	}

	switch value := record[field].(type) {
	case []map[string]any:
		out := make([]query.Record, 0, len(value))
		for _, item := range value {
			out = append(out, item)
		}

		return out
	case []query.Record:
		return value
	case []any:
		out := make([]query.Record, 0, len(value))
		for _, item := range value {
			if converted := asRecord(item); converted != nil {
				out = append(out, converted)
			}
		}

		return out
	default:
		return nil
	}
}

// recordString reads a text field off a loose record.
func recordString(record query.Record, field string) (string, bool) {
	if record == nil {
		return "", false
	}

	switch value := record[field].(type) {
	case string:
		return value, true
	case *string:
		if value == nil {
			return "", false
		}

		return *value, true
	default:
		return "", false
	}
}

// recordInt reads a whole number off a loose record.
//
// # Why so many cases
//
// The record is another module's and this module does not know which road it
// took. In process, inventory writes an int64 and pricing an int64 and an int32;
// through JSON every one of them arrives as a float64, and a decoder set to
// UseNumber writes a json.Number. A type switch that knew only int64 would
// answer "no quantity" for a record that plainly carries one, and ADR 0040's
// definition would silently report the whole catalog as out of stock -- a wrong
// answer with no error, which is the failure class this pairing is audited for.
//
// A missing field, a null and an unreadable type are all reported as NOT READ
// rather than as zero. Zero is a meaningful quantity and a meaningful amount, so
// collapsing "absent" into it would make an unlinked variant indistinguishable
// from a sold-out one.
func recordInt(record query.Record, field string) (int64, bool) {
	if record == nil {
		return 0, false
	}

	switch value := record[field].(type) {
	case int64:
		return value, true
	case int32:
		return int64(value), true
	case int:
		return int64(value), true
	case *int32:
		if value == nil {
			return 0, false
		}

		return int64(*value), true
	case *int64:
		if value == nil {
			return 0, false
		}

		return *value, true
	case float64:
		return int64(value), true
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, false
		}

		return parsed, true
	default:
		return 0, false
	}
}
