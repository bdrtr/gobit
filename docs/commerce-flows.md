# The commerce flows: cart, warehouse, and the employee buyer

What this file covers: who calls what between the cart and the order, who
decides the price, how the warehouse a line ships from is chosen, and what
changes when the buyer is not an individual but an employee spending a
company's money.

Who it is for: anyone writing a storefront against `/store/v1`, and anyone
adding a module that has to take part in one of these flows. It is not a field
reference — the request and response shapes live in `/openapi.json`. What it
carries is the ARGUMENT behind each shape: why a field is absent is as
load-bearing here as why one is present, and several of the sentences below are
the only written record of something that was measured.

These sections were moved out of [`README.md`](../README.md), which was serving
as an entry point, a reference manual and a notebook of measurements at the same
time; the README keeps the first job. This file is in English because ADR 0012
makes language a property of the file and every new file is English.

## What the reader is assumed to know

Four facts from the README that everything below rests on:

- The storefront surface `/store/v1/**` has exactly ONE identity: a publishable
  key (`pk_…`) sent in `x-publishable-api-key`. It is not a secret — it sits in
  the browser — and it carries no authorization. The only thing it carries is
  the sales channel the request is bound to; `corehttp.Principal` carries no
  customer identity.
- No module may import another. A module that needs another declares a NARROW
  interface in its own package and resolves the concrete type from the container
  BY NAME ([ADR 0001](adr/0001-modul-arasi-iletisim.md)); the flows under
  `internal/workflows` obey the same rule in both directions
  ([ADR 0006](adr/0006-workflow-modul-erisimi.md)).
- Every error body is written in ONE place, `corehttp.WriteError`. It maps a
  typed `Kind` onto a status code, and it MASKS the body of a `KindInternal`
  error — the client is left with the code, the text goes to the log.
- `complete_cart` is a saga: its steps run in order and compensate in reverse.

---

## From cart to order: who calls the flows, who decides the price

The cart's endpoints are in the `cart` module, but neither the cart's REGION nor
its TOTAL is: the region belongs to `region`, the price to `pricing`, the title
to the catalog, the tax to `tax`/`region`, and the order to
`order` + `payment` + `inventory`. This is why every WRITING endpoint of the
storefront does its work not with its own service but with a cross-module FLOW:

| Endpoint | What it does | Flow |
|---|---|---|
| `POST /store/v1/carts` | the SERVER derives the region and the currency from `country_code`, validates the customer, opens the cart | `workflows/cart` create_cart |
| `POST /store/v1/carts/{id}/line-items` | the SERVER decides the price and the title, adds the line, refreshes the totals | `workflows/cart` add_line_item |
| `PATCH /store/v1/carts/{id}/line-items/{line_item_id}` | writes the quantity and REPRICES the line; a quantity of zero removes the line (204) | `workflows/cart` update_line_item |
| `POST /store/v1/carts/{id}/complete` | reserves stock, opens the order, captures the payment, closes the cart | `workflows/checkout` complete_cart |

### The HTTP owner of a flow is the module

The module does NOT know the concrete flow: it declares a narrow interface in
its own package (`api.CartOpening`, `api.LinePricing`, `api.CartCompletion`) and
resolves the concrete type from the container under the name
`workflows.cart.interop` / `workflows.checkout.interop`
([ADR 0001](adr/0001-modul-arasi-iletisim.md)).
`internal/app` only SETS UP and REGISTERS the flows; no handler code enters the
composition root. This is the very same pattern already in use in the
`order` → `b2b` spending rule.

The registration order is CIRCULAR and the circle is broken in two places: the
flow resolves the surfaces of every module and can therefore only be built AFTER
`Bootstrap`, while the module's handler is built during `Register` and needs the
flow. The resolution on the module's side is therefore LAZY — it happens on the
first request and its result is kept.

### Price authority is on the server, and the path fails CLOSED

The line-item body once took a `unit_price` and the cart service wrote it down as
it came; only its range was checked, never its CORRECTNESS. The field's godoc
said "the final price is written by `calculate_totals`", but that flow was wired
up in no installation at all — meaning the amount the client sent WAS the final
amount, and because the storefront's identity (the publishable key) sits in the
browser, this was an endpoint EVERYONE could reach. `title` was in the same
class: the line's name is the catalog's data and it is what shows up in the
cart, in the order and on the invoice.

Both were REMOVED from the storefront body (breaking; `0.x`). Because the body
rejects an unknown field, an old client does not silently fall back to the old
behavior — it gets a `422`. There is no counterpart on the admin side: `cart`'s
`/admin/v1` surface is by definition read-only (the only party that changes a
cart is the customer), so there is no endpoint to open for "let the administrator
enter a price" either.

If the pricer cannot be resolved, the line is NOT ADDED AT ALL. This is the
deliberate opposite of the `b2b` spending rule: if `b2b` is not installed, "no
limit" is the right answer, but if there is no pricer, writing the line with "no
price" — with the client's price or with zero — is silently giving the goods
away.

The failure is reported with **`500`**, not with `404` or `422`. The distinction
is not cosmetic: the container treats an unregistered name as `not_found` and a
registration of the wrong type as `invalid`, and had those classes been passed
through as they are, the endpoint would have told the client "there is no such
endpoint" or "your body is invalid" — when the failure is in the SERVER'S
CONFIGURATION. The `5xx` alerting chain would then never ring, and intermediaries
could cache the `404` and keep the failure alive after the installation is fixed.
The text the operator needs (which name failed to resolve) is preserved in the
error but does not leak to the client: `KindInternal` bodies are masked and only
the code `cart_module_setup_failed` remains.

### A cart's region and currency are DERIVED from the country

The body of `POST /store/v1/carts` once took `currency_code`, and later
`region_id`, and both were in the SAME class as `unit_price`: the server's data
was coming from the client.

The currency is a SINGLE COLUMN per region in the `region` schema
(`region.currency_code`, an FK to the `currency` table), so a region cannot have
two currencies — the cart's currency is not a choice but a DERIVATION. Nor was
divergence rejected: because the `cart` service does not know `region`
([ADR 0006](adr/0006-workflow-modul-erisimi.md)), it only validated the shape of
the code and never compared it with the region's. A client writing `EUR` into a
cart opened in a TRY region really did get that cart in EUR. The consequence was
not cosmetic, because the currency SELECTS THE PRICE: the line flow reads the
unit price out of the variant's price set "in the cart's currency".

`region_id` survived one more round and fell for two reasons. The first is the
same class: the region selects the cart's TAX RATE. The second is more
fundamental — `region_id` is NOT what the customer wants to express. The customer
picks a COUNTRY (or their browser says it); the region is that country's
counterpart on the server, and the mapping is set up by the operator. Making the
client write an internal entity id was a softer form of the class that had just
been closed. On top of that, a flow that already did the derivation existed —
`create_cart` resolves both the region and the currency from the country code —
and THE STOREFRONT ENDPOINT WAS BYPASSING IT: two contracts for the same
operation, and the one the merchant saw was the raw one.

Today the body takes only `country_code` (mandatory), `customer_id`, `email` and
`metadata`. The pattern is the same as the price's: `cart` does not import the
flow; it declares a narrow interface in its own package (`api.CartOpening`) and
resolves the concrete type from the container under the name
`workflows.cart.interop`, LAZILY. The path fails CLOSED in the same way too — if
the flow cannot be resolved the cart is not opened at all, because falling back
to a default (the shop's first region, or whatever the client said) would reopen
the door that was just closed.

Its side effect is architectural: the ONLY place where the `cart` module resolved
another module by name is closed as well. The `region.service` binding it kept in
order to read the currency, and the `api.RegionCurrencyReader` interface, were
removed; the party that knows about regions is now the flow.

The error surface moved to the country as well: a valid country with no region is
a `404` (the operator has not opened sales to that country — the client can pick
another one), and a malformed or empty code is a `422`. `metadata` STAYED in the
body and is carried into the flow as it is; it really is the client's data and it
enters no calculation — the same decision was taken for line metadata.

On the admin side the same field is LEGITIMATE and was not removed: the
`currency_code` in the body of `POST /admin/v1/regions` DEFINES the region — there
the operator writes the original, not a copy, and there is no source to copy from.
The criterion is not "is the field in the body" but "is this value the caller's
own data". On `cart`'s own `/admin/v1` surface the question never arises: that
side only reads.

### The reason for a refusal reaches the storefront

While the saga engine wrapped a failing step it inherited the error's KIND from
the underlying error but OVERWROTE its CODE with its own constant
(`workflow_step_failed`). Because the only machine-readable field in the body is
`error.code`, every saga failure flattened into a single value for the client: a
purchase exceeding the B2B spending limit got a `409` with `spending_limit`
appearing nowhere in the response, and the storefront could not tell "your limit
was not enough" from "temporary conflict, try again" — while `409` is exactly the
class that retrying does NOT solve.

The code is preserved now (`order_spending_limit_exceeded` travels all the way to
the body); a step failure with no code takes the engine's own constant. Only the
CODE travels: the message and `Details` stay in the chain, and are still masked
for `KindInternal` errors. The boundary of the change is drawn by a test as well —
when a COMPENSATION blows up, the outer code REMAINS `workflow_compensation_failed`,
because what has to be read there is not why the step failed but that the system
has been left inconsistent.

### Every field in the completion body is a question of authority

- `payment_provider_id` IS THERE: which provider the payment is made with is the
  customer's choice. The name must be registered on the server.
- `payment_data` IS THERE: free-form data passed to the provider as it comes.
- `expected_total` IS THERE AND IS MANDATORY: the total the customer approved.
  The calculation is refreshed at the start of completion; a divergence produces
  a `409` and NO SIDE EFFECT is applied (the check runs before the saga's first
  step). Had it been optional, every client that forgot the field would have
  switched the protection off silently.
- `email` IS NOT THERE: the cart's contact address is already on the cart and the
  handler reads it from its own service; opening it to the body would let the
  order be bound to an address other than the one visible on the cart.
- `location_id` IS NOT THERE: which warehouse it ships from is a shipping
  decision, and the flow makes it by asking `inventory` + `fulfillment` PER LINE.
  Letting the customer pick a warehouse would both leak the stock topology and
  leave it to them to decide where the order ships from.

The response carries the order's identity and the amount captured; the payment
session, collection and reservation ids and the operator's warnings are NOT
PUBLISHED.

### Where the same criterion is not applied yet

A gap that has not been written down is a gap nobody has closed. These were
investigated, decided on, and left open deliberately; the reasoning is in the
code's godoc.

- **There is no ownership check on storefront carts.** The model is a CAPABILITY
  URL: the cart id is produced from a 48-bit timestamp plus 80 bits of
  cryptographic randomness, it cannot be guessed, and knowing it carries the
  right of access. It also arises out of necessity — the store surface's only
  identity is the publishable key and that is not a secret; there is no customer
  session. The model's own rule is enforced too: there is NO LIST ENDPOINT on the
  storefront side, because a list endpoint would turn knowing one id into reading
  every cart. What the model does NOT cover is the `customer_id` in the bodies:
  a capability says "I may reach the id I hold", not "I am that customer" — and
  the cart's customer determines which company's window a b2b spending limit is
  drawn from. The only correct closure is a customer session and there is NONE
  YET: phase 8 in the README's phase table is ADMIN identity (admin user, API
  key, RBAC) and it is complete; a customer session is in no phase's scope.

- **The customer's identity is not verified, and therefore the spending limit is
  applied CONDITIONALLY.** This is a direct consequence of the item above, but it
  deserves to be written separately, because the measured behavior can be
  expressed in three distinct forms: not sending the `customer_id` field at all
  (a guest cart, no limit applied), sending SOMEBODY ELSE'S id (the spend is
  drawn from their window), and opening a fresh guest record with
  `POST /store/v1/customers` and sending that (the new record belongs to no
  company, so it is unruled). All three were measured on the real binary with a
  single publishable key; the numbers are in the B2B section below, under "The
  condition of the rule", and the decision is in
  [ADR 0008](adr/0008-musteri-kimligi-guven-siniri.md). The framework OFFERS no
  surface that verifies the identity; the party that should offer one is the
  embedding application.

---

## Which warehouse it ships from

The lines of one order may be reserved from different warehouses, and the
decision is split across two modules: **which warehouses hold enough stock** is a
FACT and comes from the inventory module, **which one we ship from** is a
DECISION and belongs to the shipping module. The cart flow makes neither of them
itself.

The shipping module returns not a single warehouse but a PREFERENCE ORDER; the
cart flow tries to reserve at the first warehouse, moves to the next one if that
warehouse was exhausted in the race, and does not ask the shipping module again.
The order is computed ONCE per line.

### The policy: filter, sort, break ties

The policy is written under `/admin/v1/shipping-locations` and carries two things
per warehouse: the shipping regions it serves, and its preference order
(`priority`).

```bash
# Let the Ankara warehouse serve only reg_tr and come ahead of the defaults
curl -X PUT http://localhost:9000/admin/v1/shipping-locations/sloc_ankara \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"priority": -1, "region_ids": ["reg_tr"]}'
```

It is applied in order:

1. **Filtering** — if at least one region is bound to a warehouse and the cart's
   region is not among them, the candidate drops out. A warehouse with no
   bindings at all serves ALL regions.
2. **Sorting** — the survivors are ordered by `priority`, smallest first. A
   warehouse with no record is at priority zero; to put a warehouse ahead of the
   defaults, give it a negative value.
3. **Tie-breaking** — at equal priority the smaller id comes first.

The body is not a correction but the WHOLE policy (which is why it is `PUT` and
not `PATCH`): if `region_ids` is not given, the warehouse's bindings are deleted
and the warehouse opens to all regions. `DELETE` removes the record, which means
it does not CLOSE the warehouse, it RETURNS IT TO THE DEFAULT — taking a
warehouse out of candidacy is not within the shipping module's authority; the
candidate list is produced by the stock fact.

**With no policy record, the WAREHOUSE CHOSEN is the same as before**: filtering
and sorting fall away and what remains is the tie-breaking rule. The strict
alternative (a warehouse with no record cannot be a candidate) would, on the day
it was turned on, have stopped every order in every existing installation.

Two things do not stay the same, and both affect the record-less installation
too: ONE SQL QUERY per line was added (the old selection never touched the
database at all, so a new 500 path was opened as well) and the CODE of stock
reservation errors changed (see `CHANGELOG.md`, breaking changes). The code
change affects calls that declare a warehouse too; that path never enters the
policy but goes through the same wrapping.

### Scope is a CONSTRAINT; preference is written separately

`region_ids` means "this warehouse CANNOT ship there", not "I would rather not
ship there". If you bind two warehouses to separate regions, the order FAILS when
one of them runs out of stock — even if there are goods in the other. If what you
want is "Ankara first, Istanbul when it runs out", give no bindings, give
`priority`.

There is a trap in this, and its measured form is written in the README's
known-limits section: binding a region id that does not exist filters that
warehouse out of every cart and, in a single-warehouse installation, closes the
shop.

### The reason for a refusal is distinguishable

An order that falls because of the filtering carries a different code from one
that falls for lack of stock:

| Situation | Code |
|---|---|
| No warehouse holds enough stock | `checkout_workflow_reservation_failed` |
| The selected warehouses ran out | `inventory_insufficient_stock` |
| No candidate serves the cart's region | `fulfillment_no_serviceable_location` |

All three are `409` — there is nothing in the input to correct — but the work the
merchant has to do is different in each. The only distinction that reaches the
storefront is the CODE: the step failure preserves the underlying error's code,
while the message is the same in all three cases (the transport layer writes only
the outermost message into the body).

The third situation's message also writes out which regions the candidates are
actually bound to — the dead id of a region that was deleted and reopened can be
seen no other way — but that text is IN THE SERVER LOG AND IN THE
`workflow_executions` RECORD, not in the HTTP body. The code goes to the client,
the dump goes to the operator.

The decision and the rejected options:
[ADR 0010](adr/0010-depo-secim-politikasi.md).

---

## B2B: the buyer is not an individual but an employee with limited authority

The `b2b` module adds the concepts of a **company** and a **company employee**;
an employee may have an upper limit on what they can spend per period. The module
imports no other module and does not touch the core — this was also the exam this
repository's "modular monolith" claim had to sit: can a new domain module be
added by following the existing pattern.

The employee → customer binding is **only in `core/link`**; there is NO
`customer_id` column in the `b2b_company_employee` table. Keeping the same
relationship both in a column and in a link would have opened a place where the
two could diverge.

### The rule is split across two modules and that is deliberate

The spending limit combines two pieces of information: the **limit** (`b2b`'s
data) and the **spend** (`order`'s data — the total of the orders placed).
Neither can import the other, so the contract is JSON: `order` declares its own
narrow interface (`service.SpendingPolicy`) in its own package and resolves the
concrete type from the container under the name `b2b.interop`
([ADR 0001](adr/0001-modul-arasi-iletisim.md)).

The accepted price of this is the following: **the compiler does not check this
contract.** Had one of the field names diverged, the unit tests of both packages
would have stayed green, while in production the limit would have LIFTED SILENTLY
because the `"limited"` field could not be decoded. The two ends of the contract
are therefore brought together over the real container, in e2e
(`internal/e2e/b2b_test.go`).

### Where the check lives and why there

The rule is applied inside `order.CreateOrder`, INSIDE THE TRANSACTION the order
is written in and under the customer's lock. That has two consequences:

- **The money is never authorized at all.** In the `complete_cart` saga,
  `create_order` runs BEFORE `authorize_payment`. Taking the money for a purchase
  that is going to be refused and then refunding it would have been wrong.
- **Two concurrent orders cannot exceed the limit together.** Doing the check on
  the calling side (in the saga, for instance) was possible, but then the check
  and the write fall into two separate transactions and both would look to be
  under the limit.

The second reason is an ESCAPE HATCH: in this module the only way to create an
order is `CreateOrder`. Had the rule been put in the saga, a second caller added
later would have skipped it silently — and that is exactly the class of fault
this repository has found over and over again.

### The condition of the rule: the limit applies to a purchase that DECLARES its customer

Every sentence above is true, but read on its own it says something false. The
rule works through `CreateOrderInput.CustomerID`, and that identity enters the
chain from the BODY of the storefront cart. The store surface's only identity is
the publishable key, and that represents a sales channel, not a customer
(`corehttp.Principal` carries no customer identity). So `customer_id` is not a
fact but a CLAIM that demands no proof at all.

Measured on the real binary with a single publishable key — same cart, same
client, the only difference being the field in the body (limit `50_000`, cart
total `76_800`):

| `POST /store/v1/carts` body | Completion result |
|---|---|
| `{"country_code":"TR","customer_id":"cus_…"}` | **`409`** `order_spending_limit_exceeded` |
| `{"country_code":"TR"}` | **`200`**, the order is opened (`customer_id: ""`) |

The second half of the same measurement: a purchase completed with somebody
else's `customer_id` was written in THAT customer's name and the spend was drawn
from THEIR window — after which the employee's own purchase got a `409`. So the
claim is not only an escape route, it is a way of BURNING the spending
entitlement of an employee whose id is known.

Making the declaration mandatory does not close it either:
`POST /store/v1/customers` opens a new guest record with the publishable key, and
that record belongs to no company, so it is unruled.

The fourth door is that the attribution can be made LATER: a cart opened as a
guest is handed over to somebody else's `customer_id` with
`POST /store/v1/carts/{id}` and the order is written to that identity. So the
attribution rests on a declaration not only when the cart is opened but
throughout the cart's life (measured: the handover returns `200`, the order is in
the victim's name).

This is not a gap but a BOUNDARY THAT HAS BEEN DRAWN: gobit offers no surface
that verifies a customer's identity; the party that should offer one is the
embedding application. The whole decision, its rejected options and the list of
work that falls to the embedding application are in
[ADR 0008](adr/0008-musteri-kimligi-guven-siniri.md). The boundary's present
position is pinned in `order` by two tests
(`TestTrustBoundaryGuestOrderIsNeverAskedForTheSpendingRule`,
`TestTheSpendingRuleIsAppliedToTheDeclaredCustomer`); both protect a decision
rather than a capability, and they are EXPECTED TO FAIL when identity
verification arrives.

What the rule is good for has to be read together with this condition: in a
storefront where identity is verified, the limit ENFORCES accounting discipline;
in one where it is not, it only catches the honest client's mistake.

### Limits

- A `nil` limit means **unlimited**, a `0` limit is **a real limit of zero**.
  They are two separate sentences; had they been conflated, every employee whose
  limit was not entered would have been unable to buy anything.
- The window comes **from the calendar** (monthly: the 1st of the month, yearly:
  1 January, UTC), not from the moment the employee was hired. An employee who
  changes company mid-period carries their spend at the old company with them;
  the deviation is one-directional and **restrictive** (they spend less than they
  are entitled to, never more).
- If the company's currency differs from the cart's, the order is refused;
  converting would require a rate source and that decision does not belong to
  this module.
- **When `b2b` is not registered**, the behavior is as if b2b did not exist at
  all: no reads, no locks. A pure B2C installation is obtained by deleting a
  single line in `internal/app` — that is, with a CODE change. An environment
  variable that switches it off is **deliberately absent**: a flag accidentally
  set to `false` removes the spending limit without producing any error, and that
  is precisely the class of silent failure being guarded against. The code path,
  on the other hand, cannot be left half done —
  `TestEveryModuleIsRegisteredInTheCompositionRoot` asks whoever deletes the line
  to write the decision down together with its reasoning. The price of LEAVING
  the module in a B2C installation is small and visible too: two empty tables and
  a rule that never fires because there is no company record.
