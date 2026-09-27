# One price at one unit — measured 2026-09-27

The evidence behind [ADR 0206](../adr/0206-a-base-price-is-changed-at-one-unit.md).

## 1. The write before

A price set holding two base prices in one currency, 100.00 for one to nine
units and 90.00 for ten or more, given to the admin surface's
`SetBasePriceAmount` with 120.00 through a captured replace:

| Price | Before | Written |
|---|---|---|
| 1 to 9 | 10000 | 12000 |
| 10 or more | 9000 | 12000 |

The write matched every price with no list and no rules in the currency, and
the discount for ten or more was gone without a word.

## 2. The page before

The variant page read the price set's `prices` from the read layer and gave
each a form. Pricing's provider lists a price with rules nowhere and a price on
a list only while the list is open, so the page listed the price at one unit, its
quantity tiers and the open lists' prices. The form carried `price_set_id`,
`currency`, `minor` and `amount`, and nothing that said which price it had been
rendered for. Saving a tier's form or a list price's form therefore changed the
base price in that currency, and saving a tier's also wrote its amount over the
price at one unit.

## 3. What pricing allows

Neither pricing's validation nor its schema compares two prices' ranges: each
range is checked against its own bounds, by `normalizeQuantityRange` and by
`price_quantity_range_check`. A currency can therefore hold two base prices that
both cover one unit. The calculation charges the one whose range is narrower;
the export and the catalog filter (ADR 0041) take any price at one unit.

## 4. On a real installation

`TestThePanelEditsARealVariantsPriceAtOneUnit` builds a price set with a tier
through pricing's service, links it through the product module, reads the page
through the read layer and saves through pricing's admin surface: one form, for
the price at one unit, and after the save the tier keeps its 17990 (a test
currency, so the amounts are in minor units). It is the test that shows the
provider's types are the ones the page's reading assumes: a typed nil for the
list and the upper end, an `int32` for the lower end.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| P1 | a tier counted as the price at one unit | pricing's test, the installation's |
| P2 | the added price left open above | pricing's test |
| P3 | an amount that stands written | pricing's test |
| P4 | the first of two prices at one unit taken | pricing's test |
| P5 | a currency named twice taken | pricing's test |
| P6 | the old write, every base price in the currency | pricing's test, the installation's |
| A1 | a list price given a form | the panel's test |
| A2 | a tier given a form | the panel's test, the installation's |
| A3 | two prices at one unit given forms | the panel's test |
| A4 | a typed nil read as a list | the panel's test |
| A5 | the upper end not read | the panel's test |
| A6 | the lower end not read | the panel's test, the installation's |

P4 and P6 add statements the compiler accepts; both were run again alone and
failed on assertions, not on the build.
