# ADR 0041 — A price filter compares the BASE price, in the request's currency, at quantity one

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Gap A16 asks what amount a price filter would compare against. A product has no
single price: it has a price set per variant, prices in several currencies,
prices that belong to a list, and prices that apply from a minimum quantity. A
filter that does not say which of those it means is a filter whose results nobody
can predict.

## Decision

**The amount compared is the price that satisfies all of the following.**

1. **The request's currency.** Not a shop default and not a conversion — ~~the
   storefront already carries a currency per request~~, and comparing across
   currencies would be arithmetic on numbers that are not comparable. **Measured
   on 2026-09-08 while building it: the catalog reads carried NO currency at
   all.** A cart has one and an order has one; the three storefront catalog
   reads had none, so "the request's currency" had to become an input the
   request states — `currency_code`, required beside a bound and refused on its
   own. Nothing was defaulted: a shop default is the thing this point rejects.
2. **The BASE price: `price_list_id IS NULL`.** See below — this is what "the
   default price list" turns out to mean in this schema.
3. **Quantity tier one:** the price whose `min_quantity <= 1` and whose
   `max_quantity` is null or `>= 1`. A wholesale tier that starts at 50 units is
   not the price a shopper filtering a catalog is thinking of.
4. **No customer-group context.** The filter answers the same for everybody. A
   group-specific price is a price for a person, and a catalog filter runs before
   this framework knows who is asking (ADR 0008's trust boundary is exactly that).
5. **Evaluated at the moment of the query.** Not cached, not precomputed — a list
   with a window can open or close between two requests and the filter must agree
   with what the shopper is then shown.

**Where a product has several variants, the product matches the filter when ANY
of its variants' base prices match.** It is the same rule ADR 0040 gives for
stock, for the same reason: a product is offered if something under it is.

## "The default price list" is not a list

Measured before this was written: there is no default price list in this schema
and no way to mark one. `price_list.type` is constrained to `'sale'` or
`'override'`, and there is no `is_default` column anywhere in the pricing module.

What DOES exist is `price.price_list_id`, which is **nullable**. A price that
belongs to no list is the shop's ordinary price for that variant and currency,
and that is the thing "default" names.

This is written down so that nobody implements A16 by adding an `is_default`
flag to `price_list`. The base price is the ABSENCE of a list, and the filter's
predicate is `price_list_id IS NULL` rather than a join.

## Rejected alternatives

**The lowest price across all lists.** It is what a shopper arguably wants — the
price they would actually pay — but it makes the filter's answer depend on which
sale is running, so a product moves in and out of a price bracket without anybody
editing it. It also cannot be indexed against a stable column, because the
minimum is over a set that changes with time windows.

**The price after promotions.** Promotions are computed against a CART (they can
depend on quantity, on combinations, on a code that has been entered). There is no
promoted price for a product outside a cart, so this is not merely expensive — it
is undefined.

**A precomputed `filter_price` column on the variant.** It would make the filter a
plain index scan. Rejected because point 5 is the requirement it breaks: a
window-bounded list opening at midnight would leave every stored amount wrong
until something recomputed them, and the recomputation is a job that must run
against the whole catalog for a value that is one join away.

**Converting currencies.** It would let one filter serve every storefront.
Rejected because a rate is a fact about a moment that this repository does not
store, and a filter that quietly converts would answer differently on two
consecutive requests for reasons no merchant configured.

## Consequences

**Positive**

- **The filter is predictable and explainable**: it is the shop's ordinary price,
  in the currency being shopped in, for one unit.
- **It is indexable.** All five points reduce to a predicate over `price` columns
  the module already has, with no join to `price_list` and no time arithmetic.
  **Measured while building it, 2026-09-08: indexable BY PRICING and not by the
  caller.** The filter lives in the catalog, which may not join those columns and
  cannot push a predicate down to a provider that accepts only `id`, so the
  comparison happens in Go over records the request already fetched. See "What
  this deliberately does not do".
- **It agrees with the vocabulary a merchant edits.** The base price is the number
  on the variant's form, which is what a merchant expects a price filter to mean.

**Negative, and accepted**

- **The filter can disagree with the displayed price.** A product on sale shows
  the sale price and filters by its base price, so a shopper filtering "under
  100" may not see something currently selling for 90. That is the cost of a
  filter that does not move on its own, and it should be said in the storefront's
  documentation rather than discovered.
- **A variant priced ONLY in a list has no base price and therefore never
  matches.** That is a real catalog shape — an override-only variant — and this
  decision leaves it out rather than falling back to a list price, because a
  fallback would reintroduce every objection to "the lowest price".
- **Quantity tiers above one are invisible to the filter**, which is correct for a
  catalog and wrong for a wholesale storefront. A B2B catalog filtering at its
  contract quantity is a different decision, and the b2b module's group prices sit
  behind point 4 as well.

## What this deliberately does NOT do

- ~~**It does not build the filter**, its index, or its query parameter. It
  settles what the number is.~~ **The filter and its query parameters were built
  2026-09-08. THE INDEX WAS NOT, and the reason is a measurement that overturns
  one sentence of this record.**

  What shipped: `currency_code`, `min_price` and `max_price` on the REST
  storefront listing (minor units, inclusive bounds, an absent bound is an open
  end) and one `price: PriceFilter` input object on the GraphQL one. The three
  are one criterion, so a bound without a currency, a currency without a bound, a
  negative amount and a reversed pair are all REFUSED — and refused in
  `service.PriceBracket.Validate`, once, so the two surfaces cannot drift into
  accepting different requests. THREE of the decision's five points — the
  currency, the base price and quantity tier one — are tested by a case whose
  amount is INSIDE the bracket and which must still not match, so a build that
  compared amounts and ignored those three fails every one of them.

  Points 4 and 5 have no such case and cannot have one from here: no
  customer-group context is a property of what pricing HANDS OVER (its provider
  has already dropped every conditional price, so there is no group price for a
  test at this level to smuggle in), and "evaluated at the moment of the query"
  is the absence of a cache. Neither is falsifiable by a table of prices, so
  neither is claimed to be tested.

  **Why there is no index.** "It is indexable. All five points reduce to a
  predicate over `price` columns the module already has" is true — of PRICING.
  The filter runs in the CATALOG, which may not join pricing's tables
  (Principle 2.2) and has no predicate to push down: pricing's Query provider
  accepts exactly one filter, `id`. So the catalog compares the prices it was
  already receiving for the storefront body, in Go, after the rows are read —
  and pays exactly what ADR 0040's filter pays, because it rides the same scan:
  a page walks up to five hundred catalog rows, an offset is refused beside it,
  and the listing is not counted. The indexable predicate this record describes
  is still the right one; reaching it would take a base-amount surface on
  pricing's side that does not exist, and that is a change to a module this
  build did not own.

  **The second cost, and it is the one a reader should carry away.** Evaluating
  the predicate means reading SIX names out of pricing's loosely typed record —
  `prices`, `price_list_id`, `currency_code`, `amount`, `min_quantity`,
  `max_quantity` — the road ADR 0040 opened for ONE inventory field and
  explicitly refused to widen. Pricing declares all six as UNEXPORTED constants,
  so the audit ADR 0040 owes cannot bind them to anything: they are registered in
  `catalogUnboundForeignFields` with a test that fails the day pricing exports
  them. Until then a rename inside pricing silently empties a price-filtered
  catalog. The end-to-end proof in `internal/e2e` is what stands in for the
  missing audit: it prices its fixture through the real pricing module and would
  fail on such a rename.
- **It does not decide the DISPLAY price**, which already has an answer in the
  storefront's price set and is not what this is about.
- **It does not touch group pricing.** Point 4 says the filter ignores it; whether
  a logged-in B2B buyer should see a group-filtered catalog is A5's question and
  is left where it is.

## Related

- [ADR 0040](0040-in-stock-is-a-catalog-answer-over-an-inventory-fact.md) — the
  other storefront definition decided in the same round, with the same
  any-variant rule.
- [ADR 0008](0008-musteri-kimligi-guven-siniri.md) — the trust boundary that
  makes a catalog filter group-contextless.
