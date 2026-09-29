# Twelve lines in a row — measured 2026-09-29

The evidence behind [ADR 0233](../adr/0233-an-order-keeps-the-order-of-its-lines.md).

## 1. What there was

| Part | State |
|---|---|
| the order's line read | `ORDER BY created_at, id`; documented as "in creation order" in the service's store port and the order model |
| the cart's line reads | the same order, the same claim, for the lines, the add-ons and the batch by cart |
| the rows written together | an order's lines in `writeOrder`; a cart line and its add-ons (ADR 0229); the lines a merge opens |
| `created_at` | the transaction's `now()`, the same for every row of one write |
| the ids | 48 bits of milliseconds and 80 random bits, not monotonic within a millisecond |

## 2. The reproduction

`TestAnOrderKeepsTheOrderOfItsLines`, before the fix: twelve lines given in
order came back as `variant_11, variant_10, variant_07, variant_09, variant_06,
variant_08, variant_05, variant_00, variant_01, variant_03, variant_04,
variant_02`. After it, in the order given. `TestACartKeepsTheOrderOfItsLines`: a
ring and five add-ons added in one call come back ring first and the add-ons in
the order given. `TestAnEngravingIsALineOfItsRingsOwn` reads the ring as the
order's first line.

## 3. Mutations

| # | Mutation | Killed by |
|---|---|---|
| S1 | the order's lines read by `created_at, id` again, regenerated | the order test |
| S2 | the cart's lines read by `created_at, id` again, regenerated | the cart test |

Two mutants, both killed. The end-to-end assertion on two lines passes by chance
half the time under S1 and is a witness of the production wiring, not the gate.
