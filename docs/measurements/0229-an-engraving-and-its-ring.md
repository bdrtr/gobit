# An engraving and its ring — measured 2026-09-29

The evidence behind [ADR 0229](../adr/0229-an-add-on-is-a-line-of-its-own.md).

## 1. What there was

| Part | State |
|---|---|
| a product's add-ons | the list of ADR 0228, read by nothing on the cart |
| a cart line's identity | the variant and its properties: the unique index on `(cart_id, variant_id, md5(properties::text))`, `GetLineItemByVariant` |
| a line bound to a line | none, on the cart or on the order |
| the paths that change a line | the add and the merge open or raise, `UpdateLineItemQuantity` sets, `RemoveLineItem` soft deletes, all under the cart's lock; the storefront's DELETE goes straight to the service |
| the checkout's order snapshot | no line id; the order makes its ids in its write loop, and lines share one `created_at`, so their read order is the random tail of their ids |
| an order line pointing at another | none |
| the per-line arithmetic | each line priced by its own price set at its own quantity, discounted by its own product's facts and taxed by its own product's class |

## 2. The cart

`TestAnAddedLineOpensItsAddOns`: a ring added twice over with an engraving and a
wrap opens three lines, the two add-ons bound to the ring at its quantity and
their own prices. `TestTheAddOnsArePartOfTheLine`: the same add-ons in another
order and spacing raise the ring and both add-ons to three; the ring without
add-ons, with one engraving of other words, and with one engraving of the first
words are each another line. `TestAnAddOnFollowsItsLine`: the ring set to four
sets its engraving to four; the engraving written or removed alone is refused
with `cart_line_is_an_add_on` and nothing changes; the ring removed takes it.
`TestAddOnsAreRefusedBeforeAnythingIsWritten`: eleven add-ons, one variant
twice, one without a variant, one with empty words, each refused, no line
written. `TestAnAddWithAddOnsCountsEveryLine`: on 98 lines a ring with two
add-ons is refused whole, with one it fills the cart to 100.
`TestAMergeCarriesAddOnsWithTheirLine`: a wrapped ring the target holds is
raised with its wrap, an engraved ring it lacks opens with its engraving bound
to the new line.

On the database, `TestTheDatabaseKeepsAddOnsWithTheirLine`: the same ring with
a wrap is a second line; an add-on under another cart's line, an add-on
carrying add-ons, and one add-on twice under one line are refused by the
composite key, the check and the identity index.

## 3. The workflow, the checkout and the order

`TestAnAddOnIsPricedAsTheLineIs`: an add-on on the ring's list reaches the cart
with its catalog title, its own price set's amount and the shopper's words,
priced at the line's quantity. `TestAnAddOnTheProductDoesNotTakeIsRefused`: one
off the list and one named twice are refused before anything is priced or
written. `TestTheOrderMeetsEveryParentBeforeItsAddOns`: an add-on listed before
its parent reaches the order after it, each keyed by its cart line id.
`TestAnAddOnWithoutItsLineIsRefused`: an add-on naming a line the cart does not
hold, or an add-on, is refused before any reservation or payment call.
`TestAnOrderLineKeepsItsParent`: the key is mapped to the id the order made.
`TestAnAddOnNamesAParentItCanHave`: an unknown parent, a later one, an add-on
parent and a key named twice are refused and nothing is written.

## 4. On the production wiring

`TestAnEngravingIsALineOfItsRingsOwn`: a ring whose product accepts an
engraving; the storefront is refused a wrap the ring does not take, adds the
ring with the engraving, reads the engraving bound to the ring at 5,000, raises
the ring to two and reads the engraving at two, is refused a write to the
engraving alone, and completes the cart; the order's engraving line names the
ring's order line, at two and 5,000, and the admin order read shows it.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| C1 | the add-ons not part of the identity | the identity test |
| C2 | a raised line leaving its add-ons | the identity test |
| C3 | a quantity write leaving the add-ons | the follow test, the end-to-end test |
| C4 | an add-on's quantity written alone | the follow test, the end-to-end test |
| C5 | an add-on removed alone | the follow test |
| C6 | a removed line leaving its add-ons | the follow test |
| C7 | an add-on opened at one | the opening test |
| C8 | the key blind to an add-on's words | the identity test |
| C9 | the merge opening no add-ons | the merge test |
| C10 | a merged line leaving its add-ons | the merge test |
| C11 | the order meeting add-ons in the cart's order | the checkout order test |
| C12 | the parent key not sent | the checkout order test, the end-to-end test |
| C13 | the order mapping no parent | the order parent test, the end-to-end test |
| C14 | the order checking no key | the order refusal test |
| C15 | the workflow checking no list | the workflow refusal test, the end-to-end test |
| C16 | an add-on priced at one | the workflow pricing test |
| C17 | the plan checking no parent | the checkout refusal test |
| C18 | the cart snapshot dropping the parent | the end-to-end test |
| C19 | the order read dropping the parent | the end-to-end test |
| C20 | the product read filling no add-on list | the end-to-end test |
| C21 | the identity index blind to the add-ons | the database test |

Twenty-one mutants, all killed on the first run. C8 was written after the
identity test was given a case its first version lacked: one engraving told
apart by its words alone, since two sets of different add-ons would have
parted two lines even with the words ignored.
