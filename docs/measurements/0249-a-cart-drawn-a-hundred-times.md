# A cart drawn a hundred times — measured 2026-09-30

The evidence behind [ADR 0249](../adr/0249-a-sale-is-checked-as-a-property.md)
and D172.

## 1. What was there

| Layer | State |
|---|---|
| property-based tests | none; no file imported `pgregory.net/rapid`, gopter or `testing/quick` |
| fuzz targets (ADR 0080) | four; the ordinary lane runs their seeds, `make fuzz` generates |
| `pgregory.net/rapid` in the module graph | v1.2.0, required by `github.com/moby/moby/api`, which the three testcontainers modules require |
| the money arithmetic | the multiplication in four packages, the addition in four, the tax in two |

## 2. The properties

| Property | Draws | Holds |
|---|---|---|
| `TestEveryCartAShopperCanBuildIsSoldAsQuoted` (integration) | a market (tax added or included), one to four lines from a pool of six priced variants and a gift card, quantities one to five; half the pool under an automatic 15% promotion | each stored line's subtotal against its sticker, less its tax where the prices include it; the cart's total against the stickers; a card line untaxed; the order accepted and its total and flag; the payment collection's amount; the invoice issued and its total |
| `TestMulAmountIsTheProductOrARefusal`, `TestMultiplyAmountIsTheProductOrARefusal` (the cart and checkout workflows, the cart and order modules) | two amounts from six classes | the product when both factors are not negative and it is within the ceiling, computed in arbitrary precision; a refusal otherwise |
| `TestAddAmountIsTheSumOrARefusal` (the two workflows) | the same | the sum, or a refusal |
| `TestTaxOfIsTheFlooredProduct` (the cart workflow and the tax module) | a base and a rate, two past each end | base x rate / 10000 rounded down, in arbitrary precision, or a refusal |
| `TestTaxingTheExtractedNetIsNeverLessAndAtMostAUnitMore` (the tax module) | a gross and a rate | taxing what is left of a gross the ordinary way gives the tax taken out or one unit more: ADR 0086's measurement, as a property |

The sale's property runs a hundred carts through the cart, the checkout, the
payment and the invoice in 4.9 seconds, 5.6 with the gift card in the pool.
The money properties take under a millisecond each; at 100,000 draws each, and
200,000 for the tax module's, they passed.

## 3. The generator's classes

The first generator drew an amount from four ranges: -3 to 3, the ceiling
plus or minus 3, zero to the ceiling, and any int64. A hundred draws found
unit price 0 times quantity -1 in the order module and passed the cart module,
whose copy has the same shortcut. Drawing zero, a negative, 1 to 3, near the
ceiling, inside it and anything as six equal classes found it in both modules
on each of three runs, shrunk to the same pair.

## 4. Mutations

Each defect of this session put back, with only the property run:

| # | Mutation | Killed by, and the shrunk draw |
|---|---|---|
| R1 | D169: the cart checks every line as exclusive | the sale's property: market AT, one line, the 7 variant, quantity 1 |
| R2 | D170: no line is a gift card | the same: market TR, one line, the gift card |
| R3 | D171: the invoice reads every row as exclusive | the same: market AT, one line |
| R4 | the order checks every line as exclusive | the same |
| R5 | the checkout's plan checks every line as exclusive | the same |
| R6 | D172: the shortcut for zero before the sign | `TestMultiplyAmountIsTheProductOrARefusal`: 0 x -1 |
| R7 | the extraction divides by the scale alone | `TestTaxingTheExtractedNetIsNeverLessAndAtMostAUnitMore`: gross 80 at 125 bps |
| R8 | the cart's tax rounded up | `TestTaxOfIsTheFlooredProduct`: base 1 at 1 bps |

Eight mutants, all killed. R2's run took 35 seconds, most of it shrinking
against the database.
