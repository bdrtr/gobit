# The embedded admin panel — measured against the brief, 2026-09-05

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

The brief: the panel must ship, but as a CLIENT of the admin API rather than as
part of the core — `/admin/api/*` with RBAC first, then an SPA over it, embedded
in the binary with `embed.FS`. Extension points so a project can add its own
page, a JSON-schema-driven form for metadata fields, and slots on the critical
screens.

**The panel exists and its structure is the opposite of the brief's, by a
written decision.**

Today the panel resolves `core.query` and the three narrow admin surfaces
(`product.admin`, `pricing.admin`, `inventory.admin`) **from the container, in
process. It makes no HTTP call to the admin API at all.** It is a fourth tree
(ADR 0011) of server-rendered Go templates.

ADR 0011's reasons are on the record and they are not incidental:

- The panel's cookie stays inside the panel's tree and is deliberately NOT
  accepted by the admin API, **so the admin API's CSRF immunity survives** — it
  takes identity from a header only, and a browser cannot be made to send one
  cross-site.
- "The panel ships in one binary: **no separate toolchain, no separate
  deployment and no CORS surface.**"

So the brief's structure is not a refinement of what exists; **adopting it means
superseding ADR 0011, and the crux is authentication.** An SPA calling
`/admin/v1` needs either the cookie accepted there — which is exactly what the
ADR refused, because it destroys the CSRF property — or a bearer token held in
JavaScript, which is the XSS exposure the cookie avoids. Both are solvable
(same-origin with a short-lived token, or cookie plus a CSRF token) but the
choice is an ADR, not a detail.

The brief's strongest point stands regardless: **an SPA that is an API client is
a test of whether the admin API is complete.** Today three of the panel's writes
go through in-process surfaces rather than HTTP, so that test would fail before
it began — and finding out exactly where is worth doing whether or not the SPA
follows.

What is already true of the brief's other asks:

- **Embedding is already the posture.** Templates and the stylesheet are in the
  binary via `embed.FS`, with the stylesheet's ETag derived from its own bytes.
  An SPA would use the same mechanism.
- **RBAC exists**: the admin API is scope-guarded per endpoint.
- **The metadata slot exists**: a `metadata` jsonb sits on ~~eleven~~ **ten
  (corrected 2026-09-06)** modules' models — auth, cart, customer, fulfillment,
  invoice, order, payment, product, promotion and tax — and inside product it
  reaches the variant and taxonomy tables as well, which are that module's own
  and were being counted as two more modules. What is missing is the form
  generator, not the field.
- **The extension points do not exist.** There is no `AddPage`, no widget slot,
  and the plugin host cannot add a panel page.
- **Coverage is five sections over four of sixteen modules** — catalog, orders,
  sales, customers, inventory — which is what an operator looks at daily. Sales
  (2026-09-05) is the first section that is not a module's own screen: it reads
  the order module's line entity, so it adds a section without adding a module,
  and the two counts have to be read separately from here on. The remaining
  twelve modules are configuration.

One precedent worth carrying into the extension design: ADR 0011 deferred
WRITING entirely, because "no module has a narrow surface open to its admin
side — the price module has none at all — so writing means opening new contracts
to three modules, and every contract is a commitment without a compiler." That
gap was later closed by opening exactly three narrow `*.admin` surfaces. **That
is the pattern a project-added page should follow**: a named narrow surface, not
access to the module.
