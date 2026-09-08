# Commerce models — measured against the brief, 2026-09-05

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

The brief: subscriptions and recurring orders, multi-vendor marketplace, B2B
(group price lists, quotes, terms, minimum order), pre-order with a
back-in-stock waitlist, and digital products with licence delivery. With one
claim attached — a subscription dimension is cheap in the core and expensive
later.

Measured, the five are in three different states.

### Nothing at all: subscriptions and multi-vendor

**Subscriptions and recurring orders: zero.** No schedule, no renewal, no
recurring capture. The order state machine is four states — pending, completed,
archived, canceled — and every one of them describes a SINGLE sale that
happened once.

The brief's claim about cost is right, and it is worth being precise about why.
A subscription is not a fifth status; it is a second axis. The order keeps
`placed_at`, `completed_at` and `canceled_at` — moments, not a period — and a
recurring sale needs a next-run, a cadence, a pause and an end. That axis
touches the checkout saga (a renewal is a capture with no cart), payment (a
stored mandate rather than a session), fulfillment (a shipment per period) and
invoicing (a document per period, from a series that must stay gap-free).

The one piece that is genuinely ready is the scheduler: `internal/core/job`
already runs recurring work with an occurrence elected by a row and liveness by
an advisory lock (ADR 0019), which is exactly the shape a renewal run needs.

**Multi-vendor: zero.** No vendor, no commission, no payout. This is the
largest of the five by some distance, because it is not a feature but a change
to what a line means: every money path assumes one seller. The order totals, the
payment collection, the refund spread and the invoice all resolve to one party
today.

The nearest existing concept is the SALES CHANNEL, which already scopes what a
storefront may see and is enforced in SQL rather than in Go. A marketplace needs
the same discipline applied to money instead of visibility.

### Partly there, with the blocker already named: B2B

The b2b module exists and holds **companies and their employees**, plus a
spending rule the order module resolves by name at request time — so an
employee's order can be refused against a company limit, and a shop without the
module counts every customer as unlimited.

What the brief asks for beyond that:

- **Group price lists: the engine supports it, the cart deliberately does not
  send the group.** `pricing`'s rule matcher takes an arbitrary
  `map[string]string` context, so a rule keyed on `customer_group_id` would
  already work. The cart sends only `region_id`, and the reason is written down
  rather than forgotten: the rule context carries ONE value per attribute, a
  customer may belong to more than one group, and picking one silently would tie
  the price to map iteration order. The unfilled decision is pricing's — "the
  best price the customer is entitled to" — and the same gap is left open in the
  discount context for the same reason.

  So this item is not "build group pricing". It is "decide the selection rule
  for a customer in several groups", after which the plumbing is a context key.

- **Quotes, terms/deferred payment, minimum order: nothing.** Deferred payment
  is the interesting one, because it is the first case where an order is
  completed with money NOT captured — and the repository has just spent a whole
  round making the order say what was actually collected (ADR 0022) and
  reconciling it against the provider (ADR 0020). Terms fit that machinery
  rather than fighting it.

### A flag with no reader: pre-order

**`allow_backorder` exists on the variant, is editable through the admin API,
and is published by the query provider — and nothing reads it.** Every reference
outside the product module's own CRUD and DTO is absent; the inventory
reservation refuses on `CodeInsufficientStock` without consulting it.

That is this repository's named second error class (ADR 0009) in its purest
form: a capability whose consumer was never written, sitting in a published API
where a client can set it and reasonably expect it to mean something. A shop
that ticks "allow backorder" today gets the same refusal it got before.

Pre-order proper needs a little more — a promised date, and stock that may go
negative in a controlled way — but the first move is smaller than the feature:
either the flag gets its reader or it stops being published.

**Swept on 2026-09-06, and this flag has three siblings.** The sweep was an
instrument rather than a grep: a `go/ast` pass collecting side A — every `bool`
and `*bool` struct field carrying a json tag, plus the read layer's record keys
— and side B, the same name appearing in a CONDITION (an if, a for, a switch, a
case, or an operand of not/and/or), with the `x != nil` patch-application shape
excluded because it tests whether a field was SENT and not what it says. Every
SQL predicate was scanned for the snake_case column alongside. Generated sqlc
packages are skipped: their row structs mirror the column rather than publish it
independently, and counting them would double every finding.

Three numbers came back:

- **35 published boolean flags; 17 boolean columns, across ten modules.**
- **Never written: zero.** Every one of the 17 columns has an INSERT that names
  it or an UPDATE that sets it. That is `TestEveryColumnIsWrittenBySomething`
  (B18) doing its job across the whole boolean population, checked from the
  outside rather than trusted.
- **Carried but never decided upon: four** — `manage_inventory`,
  `allow_backorder`, `discountable`, `is_giftcard`. Every other stored boolean
  has a reader that changes what the system does: `is_active` and `is_internal`
  cut the category listing, `is_disabled` is applied inside the key-to-channel
  resolution itself, `admin_only` and `is_return` decide which shipping options
  a cart may see, `requires_shipping` is read in the inventory queries, and so
  on down the list.

**The four are not scattered, and that is the finding.** One module publishes
every unread boolean column in the repository; the other sixteen publish none.
A6 therefore is not "a flag needs a reader" — it is the product module's DTO
making four promises it does not keep, and answering it one flag at a time would
leave three behind. Two of the four cannot even acquire a reader here: the
storefront hands the inventory record through as a loosely typed record on
purpose (the accepted price of ADR 0004, written on `StoreVariant`), so nothing
in this module may interpret the stock pair and the only place that could is the
checkout saga.

**The sweep is deliberately NOT an arch test, and the reason is the more useful
half of the measurement.** Run naively over Go field names it reports 15 flags
and 11 of them are wrong — a 73% false-positive rate — because most booleans
here are not stored flags at all but RESULT fields on a response DTO:
`already_issued`, `already_open`, `released`, `cart_completed`,
`summary_recorded`, `reservations_confirmed`. Nothing in this repository should
read those; the CLIENT reads them. Anchoring the gate to a boolean COLUMN
removes ten of the eleven and takes the report down to five.

The eleventh survives the anchor and is still wrong, and it is the instructive
one. `automatic_taxes` is a real column, published on the region DTO, and no
branch in the region module reads it — but the cart does. The region module
hands it across the boundary through a primitive interop method that returns it
as an UNNAMED bool (`RegionTax` gives back a rate and an "automatic" flag), and
the cart's tax step branches on it: not automatic, no tax line. The value flows;
the NAME does not survive the crossing. That is ADR 0001's interop rule working
exactly as designed, which means a name-based reader audit cannot be made sound
against this repository's own house style — a gate failing the build on
`automatic_taxes` would be teaching somebody to widen a published contract in
order to satisfy a scanner. The finding is recorded here instead, which is the
honest place for something a test cannot hold.

Region is also the contrast that shows what pinning looks like: its service
tests already assert both that a flag left out of an update is unchanged and
that an explicit false is WRITTEN rather than read as "do not touch". Inventory
pins its equivalent default too —
`TestCreateInventoryItemVarsayilanSevkiyatGerektirir` fails at once when
`requires_shipping` is flipped. Product pinned none of its four until
2026-09-06, and that contrast is what makes this a gap rather than a house
convention. What was pinned, and what it cost to find out it was not, is D2.

**The waitlist ("tell me when it is back") is nothing today, and it is NOT the
cheapest item on this list.** That claim stood in this file until 2026-09-05,
when it was measured and found wrong twice over. The correction is recorded
here as well as in the C1 row, because the row was corrected on its own once
and this passage went on repeating the disproved sentence.

The first count was wrong. Three parts are missing, not one: the table, an
inventory EVENT — the module publishes nothing at all, so there is no "it is
back" to react to — and a subscriber to turn that event into a message. Nor can
the event be landed by itself: the topic gate refuses a name no production file
subscribes to, its exemption map is empty as a matter of policy, and the one
plugin that reads the catalog indexes no stock and so would not want the event.
The event and its first consumer are one package or neither.

The second is larger, and it is a DECISION rather than a gap — A15. The table is
not the hard part; the ADDRESS in it is. The storefront has no customer
identity: the only principal it carries is a publishable key that names a sales
channel, and every storefront write endpoint takes its subject from a path
identifier the client chooses, which is D3. (**2026-09-08:** D3 itself is closed
— the customer module's eight routes now require the claim to be BACKED — but
nothing above changes for the waitlist, which has no customer to prove and would
still hold an address typed into a box.) ADR 0008 already measured the cart's
email as an identity anchor and rejected it. So the only thing a "notify me"
form could hold is an address typed into a box, unverified — and this repository
has no verification, no double opt-in, no unsubscribe and no CAPTCHA, while the
notification module deliberately stores no recipient address at all, because a
second copy of one raises the number of places an erasure has to reach. That
table would be the first row in this repository holding an unverified address
for the purpose of mailing it.

Two smaller consequences follow from the same measurement. The subscribe
endpoint's own answer leaks: a success that differs from a conflict tells the
caller whether an address is already waiting, which is the enumeration hazard
ADR 0008 names for the order endpoint. And nothing slows that down per address —
the storefront's quota is one bucket over the whole prefix, reads and writes
alike, keyed by default on the connection rather than on the client, so behind a
proxy it is one shared bucket for every visitor.

None of this makes the feature wrong to build. It makes the FIRST step a
sentence rather than a table, and A2 sits upstream of that sentence: if the
embedder is the controller, the answer may be "publish the hooks and the
erasure contract" rather than "implement consent", and those are different
builds. Writing the table first is what would make it expensive — an address
stored before the question is answered is an address that has to be migrated, or
erased, once it is.

### Two flags and no delivery: digital products

`inventory_items.requires_shipping` and `products.is_giftcard` both exist, so
the model can already say "this does not ship". Nothing delivers anything: no
entitlement, no download token, no licence.

The storage half is built — the `file` module has an S3 provider — so what is
missing is the commerce half: what a paid order entitles the buyer to, a link
that expires and is bound to that entitlement, and a re-download policy. It is
also the one item on this list with a tax dimension in Turkey that the framework
would not decide for the shop.

### The order the measurement suggests

1. **The waitlist's DECISION** (A15), because it is a sentence rather than a
   build, and because it is the step that gets more expensive the longer it
   waits: every row written before it is answered is a row that has to be
   migrated or erased afterwards. The build behind it is not one part but
   three, and the event half is blocked separately by B7.
2. **The backorder flag's reader**, because a published flag that does nothing
   is worse than an absent one.
3. **The group-price selection rule**, because it is a decision rather than a
   build, and B2B is the segment the brief calls a real gap in Turkey.
4. **Subscriptions**, because the brief is right that the second axis is cheaper
   before the order model has more consumers, and the scheduler is ready.
5. **Multi-vendor**, last, because it changes what every money path means and
   should not be attempted while any of the above is still moving.

---

---
