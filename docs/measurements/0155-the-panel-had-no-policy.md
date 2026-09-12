# The panel had no policy

Evidence for [ADR 0155](../adr/0155-a-plugin-can-put-a-screen-in-the-panel.md).

Measured 2026-09-12, while choosing the next item from the feature list's A
section. Two of the findings below came out of an adversarial reading pass over an
earlier measurement of this row; both changed the slice.

## The row was right, and it was pointing at a locked door

`sections()` is a six-element package-private slice and `internal/adminui` is
under `internal/`, so nothing outside this module can name it. The `Host` a plugin
is handed had fifteen methods and none of them was about a screen.

So a plugin could open an admin ENDPOINT and stop there. `plugins/analytics`,
written one slice earlier, is exactly that shape: a funnel an operator could read
only with curl.

## What was absent besides the registry

Two absences, both verified by searching rather than asserted, and the second one
is why this slice is one decision rather than two.

**No lane boots the panel.** The e2e harness says so in its own words — the panel
tree is not mounted there — and nothing under `internal/smoke` mentions the panel
at all. What DOES exist, and the earlier measurement missed it, is a harness in the
composition root's own tests: a container of fakes, the real guard stack, a real
router and `panel.Routes`. That is where the gates in this slice live, and it is
why the slice needed no new lane.

**No Content-Security-Policy, anywhere.**

```
$ grep -rn 'Content-Security' --include='*.go' core/ internal/
(no match)
```

Nor `X-Frame-Options`, nor `Referrer-Policy`. The only security header in the tree
is `X-Content-Type-Options`, on asset and file responses.

That is what makes the row's own proposal — a registry carrying a SCRIPT —
something that could not ship on its own. A script served into the panel runs
inside an administrator's session.

## Why the policy can be strict, measured rather than assumed

The panel earned it. Measured across its fourteen templates and its stylesheet:

| Looked for | Found |
|---|---|
| `<script` | one, `src=` with `defer`, on the review screen |
| `<style`, `style=`, `onclick`, `onload` | none |
| `<img`, `url(` in the CSS | none |
| `template.HTML`, `template.JS`, `template.URL` | none (its layout says so too) |

So `'self'` covers everything the panel does, with no nonce. `default-src 'none'`
is the part that keeps it true: anything added later — a font, an image, a worker
— fails loudly in the console instead of quietly working.

And it is why the registry carries BYTES rather than a URL. The panel serves a
plugin's script from its own origin, so the policy stays closed for every
installation, including the ones with no plugin installed.

## The trap the measurement named first, and it was real

The likely inert shape, named before anything was written: the menu entry and the
route come from two mechanisms and only one is wired, so the link 404s — or the
reverse, a screen only somebody who knows the URL can open. The panel has already
been bitten by the second: `TestTheReviewScreenIsInTheMenu` exists because of it.

Nothing in the tree would catch either for a plugin's screen. The route audits
resolve addresses that appear in PROSE, and a plugin's path appears in none.

Both are bound from one list now, in one construction.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 116 | the route is bound and the menu entry is not | **survived**, then bit |
| 117 | the menu entry is there and the route is not | **bit** (2) |
| 118 | a path outside the panel's prefix is accepted | **bit** (1) |
| 119 | the script's address is not derived from the page's | **bit** (3) |
| 120 | the policy allows any script origin | **bit** (1) |
| 121 | `default-src` becomes permissive | **bit** (1) |
| 122 | the plugin stops registering its screen | **survived**, then bit |
| 123 | the composition root stops handing the pages over | **survived**, then bit |
| — | the policy middleware is not installed | **bit** (20 routes) |

Three survivals, one class, and it is the one this repository keeps producing: a
check whose subject is not the thing it claims.

**116** is the trap above, and my own first test did not catch it. It called
`navItemsOf` and asserted the answer — which proves that function works and says
nothing about whether the panel uses it. Its subject is the RENDERED page now:
the panel built the way the composition root builds it, a screen requested, and
the assertion on the HTML a browser would receive.

**122** is the capability-with-no-consumer shape at the plugin's end. Deleting the
registration left every lane green: the panel validated an empty list correctly and
an operator simply never saw the page.

**123** is the same shape one layer out, and the most interesting of the three. The
capability HAS a consumer and the WIRE between them had none — `panelPages(host)`
is one expression in the composition root, read by nothing. Replaced with nil,
every lane stayed green.

## What is NOT closed

- **The panel still has no lane that boots a real server.** The gates here run
  over a real router with fake services; `make smoke` starts the binary and never
  opens `/admin/ui`. A policy header is a property of the response, so the router
  is the honest subject — but "the panel renders against a real database" remains
  unproven by anything.
- **A plugin's script is not sandboxed from the panel.** It runs on the panel's
  origin with the operator's cookie, which is what makes it work and also what
  makes installing a plugin a decision about trust. The policy bounds where code
  may come FROM, not what it may do once it is there.
- **One shell, no slots.** A registered screen gets the panel's frame and a
  script. There is no way to add a column to an existing screen, and ADR 0030's
  refusal of server-renderer extension points is why.
- **No page-level scope.** A registered screen is visible to anyone the panel
  lets in; a plugin cannot ask for a narrower audience.

## What was not measured

Whether a browser actually refuses the things the policy forbids. The header is
asserted; enforcing it is the browser's, and nothing in this repository drives one.
