# A guest storefront, in gobit's own process

Three pages — a catalog, a product, a cart — served by a module this example
wrote, filled by a script this example wrote, reading `/store/v1` with the shop's
publishable key. No framework, no build step, no second process.

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

## What it does not do

- **No payment.** The cart page shows the totals and stops. `docs/first-run.md`
  completes a cart with the `manual` provider, which is one more call.
- **No customer.** The cart is a guest's, which is the storefront's default path;
  gobit issues no customer identity of its own. `examples/starter` is where
  sign-in lives.
- **No address, no shipping.** Both are endpoints this example does not call.

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
