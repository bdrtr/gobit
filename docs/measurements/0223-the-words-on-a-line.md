# The words on a line — measured 2026-09-28

The evidence behind [ADR 0223](../adr/0223-a-cart-line-carries-what-the-shopper-wrote.md).

## 1. What there was

| Part | State |
|---|---|
| the add-line body | `variant_id`, `quantity`, `metadata` "(gift note, personalization)", carried to the flow unread |
| one variant twice | the quantity of the line already there rises; the second note is dropped (`cart_line_items_cart_variant_uniq`) |
| the cart's snapshot line | id, variant, quantity |
| the checkout's order snapshot line | amounts, tax, price origin, gift card flag; no metadata |
| the order's interop line | takes `metadata`; received none (D152) |
| the order read layer | leaves line metadata out on purpose, asking for a column where a key is needed |
| reservation | one per line; the step already allows two lines of one variant |
| personal data | line metadata declared Open and kept on erasure in both modules |

## 2. The cart

`TestLinePropertiesAreTrimmedAndBounded`: none is nil, names and texts trimmed,
ten properties and the longest name and text (counted in characters, an
accented text of 500 characters being 1,000 bytes) taken, and eleven, an empty
name, a name of 65, an empty text, a text of 501, a line break and a name given
twice once trimmed refused. `TestAVariantWithOtherPropertiesIsAnotherLine`: the
same words trimmed raise the line, other words and none open two more, and an
empty text writes nothing. `TestAMergeKeepsLinesApartByTheirProperties`: Ada
summed to 3 and Bo kept apart. The fake store's uniqueness and lookup gained the
properties with the index.

## 3. The surfaces and the flows

`TestAddLineItemReturns201` and `TestAnAdminLineIsPricedUnderTheChannelTheRequestNames`
carry the storefront's and the admin's properties to the flow;
`TestAddLineItemCarriesThePropertiesToTheCart` carries them to the cart module;
`TestTheOrderReceivesEachLinesNoteAndWords` holds the checkout's two hops, from
the cart's snapshot to the plan and from the plan to the order's wire names;
`TestPlaceOrderJSONReadsTheSchema` reads them on the order's side.

## 4. On a real PostgreSQL

`TestDatabaseEnforcesLineUniqueness`: a line with no words and one with two
words of the same variant stand together; a raw insert of the same variant
without words, and one of the same words in the other key order, are refused by
`cart_line_items_cart_variant_properties_uniq`; the service finds the line by
its words written in the other order; an array is refused by the object check.
`TestTheRollbackOfPropertiesRefusesToChooseALine`: the down file raises, the
column and both lines stay. The disclosure tests gained the words on their
filled line. `TestAnOrderLineKeepsItsPropertiesOnTheRealSchema`: the words and
the note written and read back, the read layer offering the words, a line
without words reading none.

## 5. On the production wiring

`TestAnEngravingReachesTheOrder`: Ada once, Ada twice and Bo once over the
storefront are two lines, an empty text is refused 422, the cart answers each
line's words, and the order placed from it holds Ada 3 and Bo 1, each with the
note, the admin order body naming Bo.
`TestTwoEngravingsOfOneVariantHaveToFitItsStockTogether`: with two in stock,
one Ada and two Bo each fit alone; the completion is refused 409 and the stock
reads two again, the first line's reservation released.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| L1 | the lookup ignoring the properties in SQL | the uniqueness and rollback tests, the end-to-end test |
| L2 | the service looking up without them | the line test, the end-to-end test |
| L3 | the properties not stored | the line and disclosure tests, the end-to-end test |
| L4 | the properties not normalized | the line test, the end-to-end test |
| L5 | one property too many taken | the bounds test |
| L6 | an empty name taken | the bounds and line tests, the end-to-end test |
| L7 | a control character taken | the bounds test |
| L8 | a name given twice taken | the bounds test |
| L9 | bytes counted as characters | the bounds test, once rewritten |
| L10 | a merge looking up without the properties | the merge test |
| L11 | a merge dropping them | the merge test |
| L12 | the index without the properties | the uniqueness and rollback tests, the end-to-end test |
| L13, L14 | the cart's snapshot dropping the words or the note | the end-to-end test |
| L15 | the cart's interop dropping the words | the end-to-end test |
| L16 | the cart's disclosure without the words | the disclosure tests |
| L17, L18 | the storefront or admin body dropping them | the API tests, the end-to-end test |
| L19 | the cart's answer dropping them | the end-to-end test |
| W1, W2 | the flow or its interop dropping them | the flow test, the end-to-end test |
| W3–W5 | the plan dropping the words, the order snapshot the note or the words | the checkout test, the end-to-end test |
| O1–O3 | the order's interop, service or repository dropping them | the schema, disclosure and integration tests, the end-to-end test |
| O4 | the order's answer dropping them | the end-to-end test |
| O5 | the read layer dropping them | the integration test |
| O6 | the order's disclosure dropping them | the disclosure test |

Thirty mutants, all killed; L9 and O3 did not compile in their first form and
were rewritten.
