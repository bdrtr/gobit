# ADR 0462 — The admin surface counts a network and names an origin a browser sends

**Summary:** A route quota counts an IPv6 client by its /64, `ADMIN_CORS_ALLOWED_ORIGINS` refuses an entry no browser sends, and the panel's security headers stand before the API guards, so a refusal at its address carries them.
It costs a /64 shared by several hosts one quota between them, and buys the sign-in quota ADR 0452 promised, a panel origin that matches, and a panel refusal under the panel's policy.

- **Status:** Accepted
- **Date:** 2026-10-20
- **Amends:** [0451](0451-a-panel-on-its-own-host-calls-the-admin-api.md), whose origins a browser never sent were taken; [0452](0452-a-route-takes-a-quota-and-signing-in-has-one.md), whose client was one IPv6 address

## Context

A review of ADR 0451 and 0452 after their release found three faults on the
admin surface. A route quota keyed a client by its full address, so a host
rotating through its IPv6 /64 met a fresh sign-in quota at every address,
and 0452's bound on what one client may guess did not hold (D317). The
origin validator, written to refuse entries that never match, took
`https://panel.example.com:443` and a host in Unicode; a browser sends
neither, the surface compares the Origin as a string, and the panel was
turned away without a word (D318). The panel's security headers stood
after the API guards, so the sign-in quota's 429 on the panel's form came
without its content policy or frame refusal (D319).

## Decision

A route quota counts an IPv6 address by its /64 and every other key as it
is, and `ADMIN_CORS_ALLOWED_ORIGINS` refuses at startup an entry with its
scheme's default port or a host outside ASCII. The panel's security
headers are scoped to its prefix ahead of the API guards.

## Consequences

- Hosts sharing one /64 share a route quota; the whole-API limit still counts an address.
- An IPv4 address and a key that is not an address count as before.
- An operator writes a panel's origin as a browser sends it: no default port, an `xn--` host.
- Every refusal at the panel's address, the whole-API limit's and the sign-in quota's 429 included, carries the panel's headers; the 429's body is still the API's JSON.

## Rejected

- Normalising an origin that no browser sends: the setting would read otherwise than it matches, and a startup refusal names the fix.
- A configurable IPv6 prefix: a /64 is what one subscriber is given, and no fault has asked for another.
- Rendering the panel's login page for a 429: the core has no refusal writer a guard can hand a page to.
