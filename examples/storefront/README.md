# A guest storefront, in gobit's own process

Four pages — a catalog, a product, a cart and a checkout — served by a module
this example wrote, filled by a script this example wrote, reading `/store/v1`
with the shop's publishable key. No framework, no build step, no second process.

## What it proves

That the published surface carries enough to put a browser in front of gobit.
The module imports `core/http`, `core/module` and `core/container` and nothing
else from the framework; it reads no database and holds no service. Everything on
every page is fetched by the browser from the same endpoints a storefront on
another host would use.

## Running it

The shop needs two values the operator mints, and neither is readable from the
store surface: no `/store/v1` endpoint lists sales channels, and a publishable key
is returned once, by the admin endpoint that creates it.

[`docs/first-run.md`](../../docs/first-run.md) produces both — and everything a
catalog needs to be worth looking at. Run its block first, then:

```bash
STOREFRONT_PUBLISHABLE_KEY=pk_… \
STOREFRONT_SALES_CHANNEL_ID=sc_… \
DATABASE_URL=postgres://… go run .
```

Then open `/shop`.

Both are refused at STARTUP when absent. A shop that began without them would
answer 401 to every request the page makes and look like an empty catalog, which
is the failure this repository keeps naming: a screen that shows nothing and does
not say why.

## The checkout

The checkout takes an e-mail and a shipping address, lists the shipping options
the cart's region serves, puts the chosen one on the cart, and shows the total
with the delivery and the tax in it. The shopper picks a payment provider and the
page completes the cart, sending that total back as `expected_total`: a price
that moved while the page was open refuses the order rather than charging a
figure nobody saw (ADR 0174).

Every figure on it is the cart's own. The page never adds a delivery to a total;
it reads the total after the delivery was added, which the server reprices on
every write (ADR 0173).

A shopper who comes back — after a refused payment, or to change the delivery —
finds the cart still holding the method they chose, and choosing again replaces
it rather than charging two deliveries.

## What it does not do

- **No customer.** The cart is a guest's, which is the storefront's default path;
  gobit issues no customer identity of its own. `examples/starter` is where
  sign-in lives.
- **No real payment.** Every provider the installation registers is listed. A
  stock installation registers `manual`, which authorizes whatever it is given,
  and the two tenders below; a real provider's card form or redirect is the
  provider's, not the shop's.
- **The provider list is not the guest's.** `GET /store/v1/payment-providers`
  lists every provider, including the two that belong to a person — store credit
  and loyalty points. A guest who picks one is refused at the payment step, after
  the order was opened and canceled, and the page shows the refusal.

## Two costs worth knowing

**The cart id lives in `localStorage`.** A gobit cart is a capability: knowing its
id is the right to it. A shared browser therefore keeps the cart, and clearing
site data loses it. A shop with accounts would hang the cart off the customer
instead — which is what `examples/starter`'s identity module makes possible.

**The catalog is the CHANNEL's.** A product that is not published, or that the
channel does not carry, is not here — and the page then shows an empty list
rather than an error, because an empty channel and an empty catalog look the same
from the store surface. `docs/security.md` explains the scoping and its one
surprise: a product assigned to NO channel is visible in every channel.
