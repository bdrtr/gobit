# ADR 0030 — The panel becomes an admin-API client, and pays for it in one named layer

**Summary:** The panel becomes an ordinary client of `/admin/v1`, served as
static assets from the binary and authenticating with the same cookie. The cost
is named in one layer rather than spread through the panel.

- **Status:** Accepted
- **Date:** 2026-09-06
- **Phase:** after the roadmap
- **Amends:** [ADR 0011](0011-yonetim-paneli-dorduncu-agac.md) — its decision
  that the panel is served from the framework rather than calling it. ADR 0011's
  fourth-tree placement, its identity decision and its refusal of localStorage
  all stand.

## Context

ADR 0011 put the panel in a fourth tree that consumes the framework's own read
paths, and [ADR 0013](0013-panel-write-surface.md) later amended its decision 6
so a module's admin surface may also READ. What neither settled is whether the
panel keeps talking to the framework IN PROCESS or becomes an ordinary client of
`/admin/v1` — and that question blocks the extension-point design, because a
plugin cannot add a screen to a server-rendered panel without linking into it.

**The crux is the cookie, and the repository already wrote down why.** The
panel's session cookie is scoped `Path: /admin/ui`, so `/admin/v1` never
receives it. `internal/adminui/session.go` states the consequence of changing
that in its own words: if the cookie were "also sent to the API prefix, that
immunity would vanish and EVERY admin endpoint would enter a new attack
surface."

The cookie already carries the two guards that would have to stand in for it:

- `HttpOnly` — the token never touches JavaScript, so an XSS in the panel can
  manipulate the page but cannot exfiltrate the session.
- `SameSite=Strict` — which the same godoc calls "CSRF's first and cheapest
  defense" and, in the same sentence, "NOT sufficient alone (it does not cover
  subdomain takeover)".
- `UI.CheckOrigin` — the named second layer, applied to state-changing methods.

## Decision

**The panel becomes a client of `/admin/v1`.** It is a single-page application
served as static assets from the binary; every read and every write goes over
the same admin API an external client would use.

**The token still never enters JavaScript.** ADR 0011's refusal of localStorage
stands unchanged and is the reason this is a cookie decision rather than a token
decision: the SPA authenticates with the same `HttpOnly` cookie, and the only
change is its `Path`, which widens from `/admin/ui` to cover `/admin/v1`.

**CSRF is carried by the two layers that already exist**, extended to the API
prefix: `SameSite=Strict` plus `UI.CheckOrigin` on state-changing methods.
Neither is new machinery; both are today's panel guards applied to a wider path.

## What this costs, stated as a loss rather than as a wash

**The path-scoped cookie is a THIRD layer and this ADR spends it.** Today
`/admin/v1` is immune to a browser-driven CSRF by construction — not because it
checks anything, but because the credential is never sent there. After this ADR
it is immune because two checks say so. That is a normal, industry-standard
posture and it is still defence in depth; it is simply one layer thinner, and
the repository's own comment predicted exactly this ("every admin endpoint would
enter a new attack surface").

**The gap that layer was covering has a name.** `SameSite=Strict` does not cover
a subdomain takeover, and the repository says so where the flag is set. Under
path scoping, a subdomain attacker still could not reach `/admin/v1` with the
session cookie because the cookie was never scoped there. After this ADR they
can, and `UI.CheckOrigin` is what stops them — so that middleware moves from
"second layer" to "the layer", and it must be mounted on the admin API and not
only on the panel tree.

**Whoever implements this owes three things because of the above:**

1. `UI.CheckOrigin`, or an equivalent, mounted on `/admin/v1` — today that
   prefix has neither guard, because it has never needed one.
2. A decision about API-key callers. `sk_` secret keys are accepted on
   `/admin/v1` and are not browser credentials; an Origin check must not refuse
   them, so the guard has to distinguish a cookie-authenticated request from a
   key-authenticated one.
3. The panel's own error page cannot be the response any more. `CheckOrigin`
   renders HTML today; on the API prefix the refusal has to be the JSON error
   envelope every other admin endpoint returns.

## Rejected alternative

**Keep the panel server-side and add extension points to the server renderer.**
Rejected because it makes every plugin screen a Go template compiled into the
binary, which is the fork pressure ADR 0025 exists to remove: a shop wanting one
extra column would either patch the framework's templates or do without. The
panel is part of the product, and this is the one decision in this round that
deliberately spends the "gobit provides mechanism, the embedder provides policy"
backbone rather than following it.

## What this deliberately does NOT do

- **It does not put a token in JavaScript.** That option was rejected by ADR
  0011 and this ADR does not reopen it.
- **It does not make the panel a separate deployment.** The SPA ships in the
  binary; ADR 0011's single-artifact property is untouched, and CORS stays
  unnecessary because the panel and the API remain same-origin.
- **It does not decide the session lifetime.** That is
  [ADR 0031](0031-the-admin-session-is-twelve-hours.md), and the two were
  answered together because each is an input to the other.

## Consequences

**Positive**

- **The extension-point design becomes possible.** A plugin screen is a client
  of an API, which is a contract, rather than a template inside a binary.
- **The panel can no longer do what an API client cannot.** ADR 0011 listed this
  as a real gain of the loopback option it rejected for the read slice; the SPA
  gets it for free.

**Negative, and accepted**

- **One layer of CSRF defence is spent**, as above.
- **Every panel screen becomes a round trip.** ADR 0011 measured that the Query
  layer builds a screen in one pass while the HTTP path costs a call per row;
  the SPA pays that cost, and screens that were one query become several
  requests.

## Reopening the decision

Reopen if the Origin guard turns out not to be expressible on `/admin/v1`
without refusing legitimate `sk_` callers, or if a deployment needs the panel on
a different origin from the API — that would bring CORS back, and ADR 0011's
argument against it was that it had no counterpart, not that it was impossible.

## Related

- [ADR 0011](0011-yonetim-paneli-dorduncu-agac.md) — amended here.
- [ADR 0013](0013-panel-write-surface.md) — amended ADR 0011's decision 6.
- [ADR 0031](0031-the-admin-session-is-twelve-hours.md) — the lifetime this
  decision needed a number from.
