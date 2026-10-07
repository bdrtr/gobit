# ADR 0423 — A parcel that came back holds only what a return or a replacement speaks for

**Summary:** A parcel that came back undelivered holds its units only as far as a live return or replacement speaks for them, the order owes the rest again, and marking it come back is announced so a write-off made before or after restocks them.
It costs a refund claim, which names no line, whose goods are offered again, and buys a re-ship and a write-off that work.

- **Status:** Accepted; amended by [0432](0432-an-exchange-names-its-return-and-prices-what-it-sends.md), whose exchange that names its return speaks for a line's units once, the more of what it takes back and what it sends
- **Date:** 2026-10-07
- **Amends:** [0409](0409-an-orders-parcel-holds-the-units-it-ships.md) and [0420](0420-every-parcel-waits-for-its-orders-lock.md), whose counts read a parcel that came back as holding its units

Measurement: [measurements/0423](../measurements/0423-a-parcel-that-came-back.md)

## Context

A parcel marked as come back undelivered (`returned`, through
`POST /admin/v1/fulfillments/{id}/returned` or the order page) came back to
the sender on its own waybill. Every count of what an order's parcels hold read
it as still holding its units, on the premise that an order return's receipt
would put them back, and nothing records such a return. So the order owed the
units no more and a write-off put none of them back on the shelf, where they
were (D268). A parcel that came back cannot be canceled. An operator may have
settled its units through a claim's replacement, a claim refunded in money or a
stock adjustment, and marking a parcel come back published nothing. A return
or a replacement does not say which units it speaks for, and the dispatch
ceiling leaves returns out because a delivered parcel already holds the units a
customer sends back.

## Decision

Per line, an order's outgoing parcels hold the units of its pending, shipped
and delivered parcels and, of the units of its parcels that came back, as many
as the line's live returns ask back and its live replacements send again,
while a parcel that had already come back when this shipped holds all of its
units. Marking a parcel come back publishes `fulfillment.returned`, on which
the order cancellation flow recounts the parcel's written-off lines as for a
canceled parcel, so the units nothing speaks for when a count is made are owed
again to a new parcel or put back on the shelf by a write-off, made before or
after the parcel came back.

## Consequences

- A parcel that came back with nothing speaking for it is sent again by opening
  a parcel, whose owed default and the order page's form take its units, or put
  back by writing them off; the page says so under the parcel only when the
  order owes them.
- A return recorded for those units keeps them held and its receipt restocks
  them. A live replacement keeps them held and sends goods in their place, and
  a write-off counted while it is live restocks none of them, as before this
  record; one recorded after that count is not seen.
- On a line whose returns or replacements also cover delivered goods, the
  come-back units are counted as spoken for first: as many of them as are
  spoken for stay held, and a write-off restocks only the rest.
- A claim refunded in money names no line, so the goods it paid for are offered
  to a new parcel; the operator reads the claim before sending them.
- Fulfillment migration 000008 marks every parcel already come back as held
  whole, so nothing settled before is undone; such a parcel goes back on the
  shelf through an order return and its receipt. A stock adjustment made by
  hand for a parcel marked come back after it is counted again by a write-off.
- `fulfillment.returned` is written to the outbox with the status, forwarded by
  the webhook plugin, and recounted to the same target under the order's lock;
  a parcel bringing a return back puts nothing back when it comes back.
- What returns and replacements speak for is read with the ceiling before the
  order's lock, so one written or withdrawn in between is not seen by that
  open, the window ADR 0420 states. Nothing recounts when one is withdrawn: a
  parcel come back, a replacement recorded, the line written off and the
  replacement then withdrawn leaves the units off the books, and a stock
  adjustment is the remedy (docs/known-limits.md).
- The write-off reads the order's lines before its count and is tried again
  when it cannot; the module applies the rule to what the caller passes.
- The backorder claim keeps the plain count by link, so a recovery that runs it
  after one of the order's parcels came back counts that parcel whole.

## Rejected

- A parcel that came back holding nothing: a return then a re-ship would send goods the buyer was refunded for and the receipt restocked.
- A replacement holding the units for a new parcel and not for a write-off: two counts of one parcel where the open and the restock must agree.
- Reading parcels already come back under the new rule: what settled them before is recorded nowhere, so they would be sent or restocked again.
- Verbs on a parcel that came back to re-ship or restock it: opening a parcel and writing a line off already do both.
- Opening an order return when a parcel comes back: a return carries a refund decision the module cannot make.
- Bounding a return by delivered units: the order module does not know a parcel's status.
- Counting what is spoken for against delivered units first: a return recorded for the units that came back would let them ship twice.
