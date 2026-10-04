# ADR 0390 — An analytics product hears gobit through the bus

**Summary:** `core/provider` gains no analytics contract; an outside analytics product hears gobit from a
`webhook-out` receiver or a plugin that subscribes to the bus. It costs a translator or a plugin in front of every product, and what leaves is decided by the sender rather than by the core.

- **Status:** Accepted
- **Date:** 2026-10-05

## Context

ADR 0153 refused an `Analytics` contract as the first step of the funnel,
because an interface with no implementation and no caller passed every gate. It
wrote no condition under which the contract would come.

Every contract in `core/provider` is called from the core's side of the plugin
boundary: the payment, fulfillment, notification and file modules call their
providers' methods, `core/errorreport` calls the reporter's and the review job
calls the classifier's. An implementation sits in a module, a plugin or both.
What an analytics product would be told is already on the bus, and the published
`Host.Subscribe` hands a plugin every topic with its payload as published.

ADR 0014 put error reporting behind a core contract so that the core decides what
leaves, which works because the core builds the `ErrorEvent`. A subscriber
receives `order.placed` with its `customer_id`; `webhook-out` withholds it by a
list of its own. No installation, example or storefront in this tree names an
analytics product, and no request carries a visitor id.

Measurement: [measurements/0390](../measurements/0390-an-analytics-product-hears-gobit-through-the-bus.md)

## Decision

**`core/provider` gains no analytics contract; an outside analytics product hears
gobit from a `webhook-out` receiver or from a plugin that subscribes with
`Host.Subscribe`. The decision reopens with a module or core function that must
give an outside product a fact no topic carries, and a contract whose methods no
module or core function calls fails the build.**

## Consequences

- **Nothing moves for an installation.** No route, scope, migration, setting or
  published name changes.
- **`webhook-out` is the at-least-once path, behind a translator.** It posts its
  own signed envelope, so an operator runs whatever turns it into the product's
  request; the delivery id, stable across retries, drops a repeat.
- **A plugin that speaks the product's protocol owns what `webhook-out` already
  does.** Every outbox event arrives twice (D236) and a failing handler is called
  three times in about a second (ADR 0240); without a record keyed on the event
  id and a send that outlives an outage, the product counts twice and loses an
  outage's events.
- **What leaves is the sender's decision**, the position ADR 0014 refused for
  errors: confidentiality is the plugin's, audited by reading its source. A
  contract could not prevent it, because the bus already hands a plugin the
  published payload; the line is the payload (cart events carry no identity,
  ADR 0153) and `webhook-out`'s withholding of `customer_id`.
- **A page's events join the server's on a cart and a customer, not a visit.**
  The cart topics carry the cart id, which a page script can read only if the
  storefront keeps it out of an HttpOnly cookie; `order.placed` the customer.
- **`TestEveryProviderContractHasACaller` holds the uncalled shapes.** It fails
  for an exported interface in `core/provider` whose own methods, `ID` and
  `Close` aside, no function outside `core/provider`, `core/providertest`,
  `core/plugin` and `plugins/` calls while naming the contract or a type of the
  method's signature. It reads syntax: it refuses the inert interface and the
  registered slot nothing calls, it admits any caller, and whether that caller's
  fact belonged on the bus instead is the review's.

## Rejected

- **An `Analytics` contract fed by a subscriber in the core.** It sends what
  `webhook-out` sends, and the core would choose which topics a product hears;
  the gate admits it and this record does not.
- **The contract with an in-tree vendor plugin implementing it.** The plugin
  calls `Host.Subscribe` either way, and nothing on the core's side calls it.
- **A vendor plugin now, without the contract.** No installation names a
  product; the bar is a counted consumer (ADR 0018, ADR 0009).
- **Each product's request shape inside `webhook-out`.** gobit would maintain
  translators for products no installation runs.
- **A gate that type-checks the call.** It credits the same callers and is
  passed by the same caller written to pass it.
- **Reopening when an installation asks for a product.** That consumer is
  outside the tree, and the two paths above already serve it.
