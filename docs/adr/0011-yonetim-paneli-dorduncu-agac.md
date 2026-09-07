# ADR 0011 — The admin panel: a fourth tree, its own identity, the core's writer

- **Status:** Accepted
- **Date:** 2026-09-03
- **Phase:** after Phase 10 (the admin panel round)
- **Amended:** 2026-09-03 — the write question Decision 6 deferred was closed by
  [ADR 0013](0013-panel-write-surface.md). The fourth tree, the identity and the
  writer decisions did not change.
- **Amended:** 2026-09-06 — the decision that the panel is SERVED from the
  framework was changed by
  [ADR 0030](0030-the-panel-becomes-an-admin-api-client.md): the panel is now a
  client of `/admin/v1`. The fourth-tree placement, the identity decision and
  the refusal of localStorage stand exactly as they are; the only thing that
  changes is the cookie's path scope, and that ADR names what it costs (the
  path-scoped cookie was CSRF's third layer).

## Context

The plan document puts the admin panel UI out of scope in two places and does
not say **why**. What surfaced once the panel was started is not an interface
design problem: it is a **placement and identity** problem that touches three of
the framework's written decisions directly.

Before the decision the repository was in this state, and all of it was
measured:

- Admin identity is resolved **only** from the `Authorization: Bearer` header.
  There is not one line of production code that reads or writes a cookie; there
  is no CSRF, CORS or security-header middleware either.
- The guard stack is scoped by path **prefix** and the match is at a segment
  boundary. A tree outside `/admin/v1` does **not** enter identity, the rate
  limit or the idempotency ring **at all**.
- Writing a response body directly in a package outside a module is a structural
  violation; `w.Write`, `w.WriteHeader` and `http.Redirect` are caught.
- There is no `html/template`, no embedded static asset and no template
  precedent in the repository.

One consequence of this is direct: **a browser cannot send an `Authorization`
header while navigating to a page.** The moment server-rendered HTML is
preferred, how identity arrives from the browser stops being a design question
and becomes a framework question.

## Decision

### 1. The panel lives in a fourth tree: `internal/adminui`

Neither core nor module; a sibling of `internal/workflows`.

Put inside a module it would hit three walls at once, and all three were
measured: it could not import any other module (this check has **no**
justified-exemption gate), it could not hand a template to the writer in the api
package, and the natural Go spelling that runs a template through a field could
**not even be written** into the exemption list — because the call's name cannot
be resolved.

It cannot go under core: core does not know the modules. It cannot go under the
composition root: that place is wiring only.

**The price of this tree is the same price ADR 0006 wrote down for
`internal/workflows`** and it is paid the same way — see Decision 5.

### 2. The panel lives under `/admin/ui` and is added to the guard stack EXPLICITLY

`/admin/v1/ui/...` would break three places at once: every page arriving from
the address bar would get a `401`; the HTML endpoints would leak into the
OpenAPI document and into the generated clients; and the authorization test that
walks the router tree would expect a `403` from every page.

`/admin/ui` solves all three, but it arrives **open by default** — no identity,
no quota. That is the exact opposite of ADR 0007's identity line and is not
acceptable. So the panel's own guard ring is **appended**, in the composition
root, to the slice the guard stack returns; for the rate limit its prefix goes
into `OpenPrefixes` — the same class as file serving and OpenAPI.

The ring is attached at the composition root and not in the panel's `Routes`
method: the router refuses with a panic when a ring is added after a route has
been registered.

### 3. Identity: an HttpOnly cookie, ONLY in the panel tree

Login writes the token into an `HttpOnly · Secure · SameSite=Strict` cookie and
the cookie's path is confined to the panel tree. **`/admin/v1` does not accept
this cookie and will not.**

This is the backbone of the decision. The admin API's CSRF immunity today does
not come from a defense; it comes from the token sitting in a header the browser
does not attach on its own. Opening the cookie to `/admin/v1` destroys that
immunity in a single line and would put **every** admin endpoint into a new
attack surface.

And this can be done **without touching core at all**: the helper that puts
identity into the context is exported, and the authorization check reads
identity only from the context. The panel's ring reads the cookie, asks the
**same** authenticator with the "Bearer" scheme, and puts the result into the
context. Neither the header-reading code nor the admin guard changes.

The CSRF defense is `SameSite=Strict` **plus** an `Origin` header check on
state-changing methods; the second is needed because `SameSite` does not close
subdomain scenarios. Both live in the panel's own ring.

### 4. HTML goes through the core's writer

`WriteHTML`, `WriteRedirect` and `WriteAsset` are added beside the core's
response writers. That file is already exempt from the scan as a "core writer
definition", and the exemption's validity looks only at the two existing writers
being defined there — a third writer breaks no check.

The body is produced **into memory first**, and the headers and status are
written after. In a design that streams straight into the writer, an error born
in the middle of a template leaves a **half** page carrying a `200` status code,
and the panic recoverer can do nothing once the header has been written; the
fault goes silent at the client.

A measured side effect: the 2xx requirement belongs to the JSON writer alone. So
the HTML writer can carry `401` and `403` — **a request without identity gets
the login page with the correct status code** — and no redirect is needed. Where
a redirect is required anyway (after login) it is done with a `Location` header
and a `303`; `http.Redirect` is forbidden.

### 5. The tree's wiring gap is closed THE MOMENT it is opened

The registration checks scope themselves down to the module tree. In the panel
tree a capability that is "registered but installed on no ground" would leave
the arch tests **green** — measured. This is this repository's most expensive
class of bug: the capability nothing keeps a record of.

Nothing has to be invented. The same gap has already been closed for
`internal/workflows` and the pattern is ready: the tree's name, a conventional
constructor name as the installation marker, and a staleness gate that keeps the
check from going blind. The panel tree takes the same pattern.

Likewise, the non-module arm of the body-write scan does **not see** template
writing — because the call's receiver is not an import name. This is not a
permission, it is the negative of how the scan measures; since the whole panel
will live in that blind spot, the scan is widened so that it does see template
writing.

### 6. The panel uses the framework's READ paths

Catalog screens take their data from the Query layer (ADR 0004), through a
narrow interface resolved from the container by name (ADR 0006). No module is
imported; the cart flow is the proven example of the same pattern.

**Writing is NOT in the scope of this ADR** and is deliberately deferred: the
modules have no narrow surface opened towards the admin side — the pricing
module has no such surface at all — so writing would require opening new
contracts to three modules, and every contract is a commitment without a
compiler. The decision will be made with real screens in hand.

**Decided on 2026-09-03:** The deferral closed the same day.
[ADR 0013](0013-panel-write-surface.md) tied writing to a narrow,
**primitively typed** surface that each module publishes itself, and registered
that surface under a name SEPARATE from interop — three of them today:
`product.admin`, `pricing.admin`, `inventory.admin`. Writing goes through the
SERVICE and not the repository, so handle uniqueness is checked and
`product.updated` is published. The panel resolves these names **optionally**:
an installation without the product module still gets the panel, and the edit
form returns a `503` that says why. Decision 6's read path stood in outline —
catalog screens still use the Query layer's `Graph` call through a narrow
interface resolved from the container by name — but it did not stand UNCHANGED:
ADR 0013 says in its title that it amends this decision, and it grants the admin
surface a narrow READ right as well, but only where the cross-module read
layer's audience is wrong for that data (the per-location breakdown carries
reserved quantities and internal warehouse names, and the storefront is among
that layer's consumers). A read done to save a round trip is outside this right.

## Consequences

**Positive**

- The framework's identity surface **does not change**; the cookie stays in the
  panel's own tree and the admin API's CSRF immunity is preserved.
- HTML writing gets a single, nameable gate; the check is widened instead of the
  blind spot being exploited.
- The panel ships in the single binary: no separate toolchain, no separate
  deployment and no CORS surface.
- The fourth tree's wiring gap closes in the round that opens it.

**Negative — accepted costs**

- **The repository gains a fourth tree.** The same price ADR 0006 paid for
  `internal/workflows`: because the rules are written against tree names, every
  new tree makes every rule's scope a question again.
- **The framework learns about HTML.** The core response writers now carry a
  browser concept. The price is small and in one file, but it wears away at the
  edge of the phrase "headless framework".
- **A new attack surface opens.** Publishing an HTML panel opens XSS and framing
  surfaces for the first time; there has never been a security header in this
  repository. The defense is attached to the panel prefix, API behavior does not
  change.
- **The panel is compiled and served by default.** The way to turn it off is NOT
  an environment variable, it is deleting a line from the composition root — the
  same class of flag ADR 0007 and ADR 0009 rejected is rejected here too: a
  switch accidentally set to `false` could leave the panel open without
  producing a single error.
- **The session lasts 12 hours and there is no renewal.** The panel shows the
  expiry in advance; a token can die in the middle of a long editing session.
- **Logout is WHOLESALE.** Logging out of the panel drops all of the user's
  sessions; the interface does not hide this, the button says so.

## Rejected options

**Putting the panel in the module tree.** The gain was real: the registration
and ground checks would come for free. Rejected because its price is three
measured walls — the module import ban, the write ban in the api package, and a
call form that cannot be written into the exemption list — which make the panel
effectively impossible. The gain is taken back by hand with Decision 5; the
price cannot be taken back.

**Keeping identity in `localStorage` and attaching it to every request with
JavaScript.** It would not touch the framework at all. Rejected because it
contradicts the server-rendered HTML decision: a navigation typed into the
address bar, and F5, carry no identity, so the first paint becomes dependent on
JavaScript. It also exposes the admin token to XSS, and since there is no
content security policy anywhere in the repository, that is a real price.

**Adding a cookie fallback to the header-reading core code.** This was the path
that asked for the least code. Rejected because it affects the store guard as
well and opens the entire admin API to CSRF; the only thing protecting it today
is that the token sits in a header that is not sent automatically.

**Exploiting the blind spot** — handing the template straight to the writer. It
passes the check today. Rejected because this is not a permission, it is the
negative of how the scan measures; the day the boundary is closed, the whole
panel would fall into violation. The repository has produced this class of bug
(the surface with no checker) with its own hands before, and closed it.

**Writing a justified exemption for every page.** Exemptions are per call, and
an unused exemption fails a test; every new page would grow the list. It does
not scale, and the exemption list would end up longer than the rule itself.

**Writing the panel as a separate application and adding CORS to the
framework.** It would let the panel be versioned separately from the framework.
Rejected because CORS is a security surface (origin list, credential carrying,
preflight) and it would add a new item to the framework's hardening decisions in
return for nothing; the token would also have to be stored in the browser.

**The panel calling its own HTTP API.** It would guarantee that the panel can do
nothing an API client could not do, and that is a real gain. Rejected for the
read slice: the Query layer builds the same screen in a single round trip,
whereas the HTTP path means an extra call per row and a second serialization.
~~The decision **is open again for the write slice**, and there the gain may
weigh heavier.~~ **Corrected on 2026-09-03:** Rejected for the write slice too.
[ADR 0013](0013-panel-write-surface.md) closed the same option with three
prices: the panel would have to mint and carry an admin token of its own —
exactly the hazard the cookie's path scope avoids —, every edit would pay two
serializations, and a process calling itself over its own connection pool would
deadlock under saturation instead of slowing down.

## Reopening the decision

Three pieces of data reopen this decision:

1. **The first moment a screen of the panel can do a job the framework's API
   does not offer.** At that moment the panel stops being a reference consumer
   and becomes a privileged second path, and Decision 6 must be reconsidered.
2. **When the write slice's real cost is measured.** If opening a new narrow
   surface to three modules turns out to be more expensive than the panel making
   HTTP calls to its own API, that rejected option comes back.
   **2026-09-03:** this trigger fell without the measurement it asked for ever
   being made. [ADR 0013](0013-panel-write-surface.md) closed loopback not with
   a cost comparison but with three STRUCTURAL prices — the panel minting an
   admin token for itself, every edit paying two serializations, and the process
   deadlocking rather than slowing down under saturation while calling itself
   over its own connection pool. The triggers that come after this one are
   written in that ADR.
3. **When separate versioning of the panel is wanted.** That day the CORS
   decision is weighed again; the reason it is rejected today is that it buys
   nothing, not that it is impossible.

## Related

- [ADR 0001](0001-modul-arasi-iletisim.md) — narrow interface + resolution by
  name.
- [ADR 0004](0004-query-veri-erisimi.md) — the panel's read path.
- [ADR 0006](0006-workflow-modul-erisimi.md) — the fourth tree's precedent; this
  ADR applies the pattern it established a second time.
- [ADR 0007](0007-sertlestirme-arizada-davranis.md) — behavior on failure; the
  panel's guard ring and its refusal of a flag are fed from there.
- [ADR 0013](0013-panel-write-surface.md) — the answer to the write question
  Decision 6 deferred; it amends this ADR.
