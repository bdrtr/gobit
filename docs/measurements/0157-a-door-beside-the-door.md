# A door beside the door — measured 2026-09-12

What a route bound at the panel's address carried, and what it did not, when
something other than the panel bound it.

## 1. The probe

Not an argument. A router built the way the composition root builds one, the
panel mounted on it, and then one route bound afterwards — which is exactly what
a plugin's `AddRoutes` does, on the same router, after `registerPanel`:

```go
panel.Routes(router)

router.Get(adminui.URLPrefix+"/rogue", func(w http.ResponseWriter, _ *http.Request) {
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte("<h1>rogue</h1>"))
})
```

Two requests, the same router:

| requested | status | Content-Security-Policy | X-Frame-Options |
|---|---|---|---|
| the route bound after the group | 200 | *empty* | *empty* |
| the panel's catalog screen | 403 | `default-src 'none'; script-src 'self'; …` | `DENY` |

The panel's own route carries the policy. The route bound beside it carries
nothing.

## 2. Why

ADR 0155 installed the policy with `r.Use` inside the group `UI.Routes` opens:

```go
func (u *UI) Routes(r chi.Router) {
    r.Group(u.routes)      // and u.routes started with r.Use(withSecurityHeaders)
}
```

chi's `Group` starts its own middleware stack. Everything registered INSIDE the
callback is wrapped; anything bound on the parent afterwards is not. The panel
binds twenty routes in that callback and a registered screen's two more — and
the address `/admin/ui` is open to anything else that reaches the router.

The reasoning ADR 0155 gave for the group was about a call per handler:

> a call per handler is a rule that holds until somebody adds a handler … the
> twenty-first would be written by copying a neighbour

That reasoning is correct. What it protected was the panel's own route list,
and the sentence the policy states is about an address.

## 3. What the route DID carry

The hole is one ring deep, not three. The composition root scopes the other two
panel rings to the PREFIX, not to the panel's routes:

```go
corehttp.Scoped(adminui.URLPrefix, nil, panel.CheckOrigin),
corehttp.Scoped(adminui.URLPrefix, adminui.ExemptPaths(), panel.Protect),
```

So a route bound beside the panel is inside the operator's session and behind
the origin check. It is a page an operator reaches, signed in, with no content
policy — which is the one rule that says which scripts may run there.

That shape is also the fix: the policy moves to where those two already are.

## 4. The second hole, and it is not the policy's

ADR 0156 prices every panel path in one table and wraps each route with the
privilege found there. A route the panel did not bind is in no table, so it is
wrapped by nothing. An operator who can sign in reaches it whatever their grants
are — the door ADR 0156 shut, reopened from the side.

No policy header fixes that, which is why the decision has two halves.

## 5. Who binds there today

```
$ grep -rl "admin/ui" --include='*.go' plugins/ contrib/ examples/ | grep -v _test
plugins/analytics/plugin.go
```

One file, and the name in it is `PagePath = "/admin/ui/analytics/funnel"` — not
a route binding but the `Path` of a
`RegisterAdminPage` registration, bound BY THE PANEL, inside the group, under a
declared privilege. So the refusal takes nothing away from any installation, and
the plugin that reaches the panel is already using the sanctioned path.

## 6. The refusal's subject, and how it was wrong first

`MountRoutes` compares patterns collected by `collectPatterns`, which keys on
the method and the path together. The first version of the check tested that key
against the panel's prefix:

```go
strings.HasPrefix(pattern, adminPanelPrefix+"/")   // the key carries a VERB first
```

It never matched, and the refusal never fired. Its own gate caught it on the
first run — three refusal cases failed with "An error is expected but got nil".

That is this repository's most frequent defect and the fifth instance in three
slices: a check whose SUBJECT is not the thing it claims. What makes the
difference is not care; it is that the gate was written to fail, and run.

## 7. Mutations

Four, all of which bit:

| Mutation | What failed |
|---|---|
| the policy removed from the guard stack | the prefix walk — on the route bound after the group, and on the 404 |
| the refusal removed from `MountRoutes` | the refusal's own table AND the arch check that feeds it the panel's real constant |
| `core/plugin`'s copy of the prefix changed to `/admin/panel` | both of the above — the drift is what the behavioural check exists for |
| the boundary match replaced with a plain prefix test | the OTHER direction: `/admin/uipload` was refused with a message about screens |

The third is the one worth keeping. A comparison of two literals would also have
failed there, and it would have needed the constant exported to be written at
all. The behavioural check needs nothing exported and asserts the thing that
matters: that the refusal covers the address the panel is actually served at.

## 8. What this did not close

A plugin's routes still run on the same router as everything else, and the
refusal covers ONE address. `/admin/v1` is shared by construction — plugins
publish endpoints there on purpose — and a plugin binding `/admin/v1/…` gets the
API's rings, which is correct and different: that surface's rules are applied by
prefix to everyone, and a plugin route there is guarded exactly like a module's.
