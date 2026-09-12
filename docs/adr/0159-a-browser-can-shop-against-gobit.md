# ADR 0159 — A browser can shop against gobit

**Summary:** A fifth out-of-tree module serves three pages and one hand-written
script from gobit's own process; the browser reads `/store/v1` same-origin with
the shop's publishable key.

- **Status:** Accepted
- **Date:** 2026-09-12

Measurement: [measurements/0159](../measurements/0159-a-shop-with-no-toolchain.md)

## Context

Nothing in this repository put a browser in front of gobit. The storefront API
is forty-eight routes, thirty-seven of them usable by a guest, and the only
proof any of them work is a Go harness driving them over HTTP. An embedder
asking "can I build a shop on this" had two answers available: read the tests,
or believe the README.

The row asked for a Next.js or SvelteKit starter. Measured, that shape is
reached by exactly ONE gate in the whole repository — the path-language check —
so a node toolchain would enter the tree unaudited, with its own lockfile, its
own vulnerability surface and no lane that opens it.

## Decision

The example is a Go module like the other four: `examples/storefront`, whose
`main.go` is the published facade plus one module of its own, and whose module
serves three shells and one framework-free script. The pages run in gobit's
process, so the browser is SAME-ORIGIN with `/store/v1` and no installation has
to open CORS for the example to work.

## Consequences

The shop holds no service and reads no database. Every figure on every page is
fetched by the browser from the endpoints a storefront on another host would
use, which is what makes the example a claim about the SURFACE rather than about
the example.

It needs two values it cannot discover: the publishable key and the sales
channel. No `/store/v1` endpoint lists either, and the catalog address carries
the channel in its path. Both are refused at startup rather than defaulted — a
shop that began without them would answer 401 to every request the page makes
and look like an empty catalog.

The shop carries its OWN content policy, and it cannot be the panel's: a
catalog shows product images and the panel's policy starts at `default-src
'none'` with no `img-src`. Nothing published carries a policy for an embedder's
pages, so an embedder writes the three headers themselves — which this example
now demonstrates rather than hides.

The cart id lives in `localStorage`. A gobit cart is a capability URL, so a
shared browser keeps the cart and clearing site data loses it. That is the
storefront's model rather than the example's choice, and the README says so.

`.js` joined the language scan in the same change. The repository was already
shipping two hand-written scripts the content scan had never read, and this
example would have been the third — a file whose prose an operator reads inside
the page, audited by nothing.

## Rejected

**Next.js or SvelteKit.** One gate would have seen it. The toolchain, the
lockfile and the second language are a cost the example does not need to make
its point, and the point is the API surface.

**A storefront on its own port.** Legitimate and a different lesson: it begins
with CORS configuration, which has nothing to do with shopping, and it would
hide the one thing this example is for.

**Rendering the catalog on the server.** The module would need a channel-scoped
read, and the scoping lives on the store surface rather than in the read layer.
Fetching from the browser is the path a real storefront takes.

**Adding the pages to `examples/starter`.** That program answers "what does a
project's main.go look like"; a shop is a second lesson, and a separate module
is also a second proof — that the surface is enough for a program that is ONLY
a storefront.
