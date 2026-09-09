# ADR 0093 — The badge counts the channel's warehouses

**Summary:** The storefront's in-stock badge counts only the warehouses the
request's sales channel ships from, so it agrees with the checkout that refuses
an order it cannot serve.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0092 narrowed the CHECKOUT to a channel's warehouses and left the badge
counting every warehouse. The gap was written down the same day as a known
limit, and it is the worst-shaped kind: the shopper is shown "in stock", adds
the units, pays attention all the way to the last step, and is refused there.

The badge could not be narrowed where it is computed. It reads inventory's
`available_quantity` through a Query expansion (ADR 0004), and an expansion
carries no filter: the provider is handed ids and field names, so it cannot be
told which warehouses the caller may count.

## Decision

Inventory publishes the same total BROKEN DOWN by warehouse, as a second field
computed only when it is asked for. The storefront asks for it through a SECOND
expansion over the same link — the Query layer allows that when the output keys
differ — and sums the warehouses the request's channels ship from.

The breakdown lands under a key of its own and never enters the record the
storefront publishes.

## Consequences

The badge and the checkout now read the same binding, and an end-to-end test
holds them together: the same variant reads out of stock, is refused at
checkout, and both turn over when the merchant binds the warehouse.

A shop's warehouse topology stays inside the module. `StoreVariant.InventoryItem`
is published to shoppers, so the breakdown is deliberately not in it — keeping
it out of that record is cheaper to prove than remembering to delete it from it.

An unnarrowed read pays nothing: no channel means no second expansion, no second
query, and the response is byte for byte what it was.

A binding that cannot be READ degrades the badge to the unnarrowed total, with a
line in the log, instead of failing the catalog. The checkout makes the opposite
choice on the same failure, and the asymmetry is the point: there the answer
decides where goods come from, here it decides how honest a badge is — and the
order that follows is still refused.

A missing breakdown answers ZERO rather than falling back to the total. The
fallback is the one answer this must never give: it would show the stock of a
warehouse the storefront cannot ship from, which is the thing being fixed, and
it would look right while doing it.

`known-limits.md` loses the item ADR 0092 added.

Measurement: none. The shape was read off the contract — an expansion carries no
filter — and the cost was made conditional rather than measured away.

## Rejected

**A filter on the expansion.** The general fix, and it changes a PUBLISHED
contract (ADR 0026): `Provider.FetchByIDs` takes ids and fields, fifteen
implementations satisfy it, and one consumer needs the filter. The day a second
one does, this is the decision to reopen.

**Reading the channel from the request context inside inventory.** The
narrowing would be invisible at the call site and would apply to callers that
never asked for it, including flows with no request at all.

**Asking for the breakdown always and deleting it before the response.** One
expansion instead of two, and a second query on every storefront read that does
not narrow — paid by every shop that never binds a warehouse. It also makes the
leak a matter of remembering to delete.

**Storing a per-channel availability.** A number that is stale the moment stock
moves, and keeping it fresh means the catalog subscribing to inventory's events
for something it can work out on read.
