# ADR 0013 — The panel writes through a per-module admin surface, not through interop

- **Status:** Accepted
- **Date:** 2026-09-03
- **Amends:** ADR 0011 (decision 6, "the panel uses the framework's read paths")
- **Related:** ADR 0001, ADR 0004, ADR 0006

## Context

ADR 0011 built the admin panel as a fourth tree that imports no module and
reads everything through the cross-module read layer. That worked because the
read layer is GENERIC: one `Graph` call serves any entity, and the panel only
had to spell entity and link names.

There is no equivalent for writing. Every write is module-specific, and the
product service's own method cannot be reached from the panel:

```go
func (s *Service) UpdateProduct(ctx context.Context, id string, in UpdateProductInput) (models.Product, error)
```

`UpdateProductInput` and `models.Status` are the product module's types. The
panel cannot name them, because Go's structural typing means a type it declared
itself under the same name would be a DIFFERENT type and the concrete service
would stop satisfying the panel's interface. This is the same wall ADR 0006 hit
for workflows and ADR 0001 for modules.

So the panel could read and not fix. An operator could see that a product's
title was wrong and had to leave the panel, find a token and use `curl`.

## Decision

### 1. The owning module publishes a narrow, PRIMITIVE-typed admin write surface

`internal/modules/product/service/admin.go` declares `AdminSurface` with one
method:

```go
UpdateProductBasics(ctx context.Context, id, title, handle, status string) error
```

Only primitives cross the boundary, for the reason above. The panel declares its
own consumer-side interface (`adminui.ProductWriter`) and the concrete type
satisfies it structurally — ADR 0001's pattern, unchanged.

### 2. The surface is registered under its OWN name, apart from interop

`product.admin`, not `product.interop`, and the split is the decision rather
than a filing convenience.

`interop.go`'s own godoc promises to stay narrow and names its audience: other
modules, workflows and plugins reading the catalog. A write method added there
would hand every plugin the ability to rewrite the catalog — a capability
nobody asked for, granted as a side effect of giving the panel an edit form.

The name says who it is for, and **[TestAdminSurfaceHasOneAudience] makes the
name true**: no production file outside the owning module and `internal/adminui`
may spell a name ending in `.admin`. Without that check the separation would be
a sentence in a godoc, and the first workflow that found the name convenient
would make it false — silently, because resolving a registered name succeeds.

### 3. Everything goes through the SERVICE, never the repository

The service is where the handle uniqueness check runs and where
`product.updated` is published. A surface that reached the repository would
write a product no subscriber ever hears about: a search index would keep
serving the old title, and nothing in the response would say so.

That is asserted, not assumed — the silent half (the event) explicitly, because
the loud half (the handle conflict) would otherwise be taken as proof of both.

### 4. The panel resolves the write surface OPTIONALLY

An installation without the product module still gets a panel; the edit form
answers 503 with a sentence naming the reason. Treating the surface like the
others would turn a removable module into a requirement of the panel itself,
which is exactly the coupling the fourth tree exists to avoid.

A name that IS registered but whose surface does not match still fails, and
fails at startup: that is a wiring mistake, and degrading it silently would
leave the panel showing "editing unavailable" while the module sat right there.

### 5. CSRF is already covered, and the coverage is by PREFIX

The origin ring installed for the login form is scoped to the panel prefix and
exempts nothing state-changing. A write route added under that prefix is
therefore protected the moment it exists — no list to update, nothing to
forget. Because that is a sentence about scope rather than about a route, it is
asserted at the composition root with a cross-site POST to the edit path.

### 6. A rejection re-renders the form; anything else is the panel's error page

An `Invalid` or `Conflict` message is written by a service author and is
client-safe by the framework's own rule, so it is shown on the form next to the
values the operator typed. Redirecting instead would throw those values away and
leave a message with no field to fix.

Every other failure becomes the panel's own HTML error page with a generic
sentence, and the real cause goes to the log. Two things were wrong with the
obvious alternative of calling `corehttp.WriteError`:

- It writes the framework's JSON envelope, which is right for an API client and
  unreadable to the BROWSER that navigated to this path.
- It passes a non-Internal message through untouched. That promise was made
  about API clients; an operator reading a panel page cannot tell a leaked
  connection string from a diagnosis.

The same flaw existed on the sign-in path since ADR 0011 and is fixed here.

## Consequences

- The panel stops being read-only. Today it edits a product's title, handle and
  status; every further write is a new method on a module's admin surface and a
  new form, both visible in a diff.
- `product` grows a second cross-module surface. That cost is real and is what
  its interop godoc warns about; it is paid deliberately and bounded by the
  audience check.
- Adding a fourth editable field changes the surface's signature. That failure
  is NOT silent: the panel resolves through its own interface, and a signature
  that no longer matches makes `container.Resolve` fail AT STARTUP with a
  message naming the missing method.
- The panel now repeats the module's status values. A value the module REMOVED
  fails loudly at the surface; a value the module ADDED would simply never
  appear in the form and no error would report it, so the list is pinned against
  the module's constants in `internal/arch`.

## What this cannot express

- **Whether the operator was allowed to make this edit.** The panel's identity
  ring proves who they are; it does not check a scope per action. Every signed-in
  admin can edit every product, exactly as on `/admin/v1` today.
- **Concurrent edits.** Two operators saving the same product overwrite each
  other, last write wins. There is no version field on the form and no
  optimistic check underneath it.
- **An audit trail.** `product.updated` says the product changed, not who
  changed it. The panel's principal is in the request context and does not reach
  the event.

## Rejected options

**Add the write method to `product.interop`.** It is one line and no new name.
It also hands every plugin and every workflow the ability to rewrite the
catalog, because the container has no audience: any holder can resolve any name.
The capability would be granted as a side effect of an edit form, which is
exactly the kind of quiet widening this repository writes ADRs to prevent.

**Let the panel import the modules.** The coupling would become compile-time,
which this repository generally prefers — a renamed field would break the build
instead of the screen. It would also make every module a build requirement of
the panel: delete the b2b module and the panel stops compiling. The framework's
promise that modules are independent and removable is worth more than the
panel's convenience, and the runtime coupling that remains is pinned by name in
`internal/arch`.

**Have the panel call the admin API over loopback HTTP.** No new module surface
at all. But the panel would have to mint and carry an admin token, which
recreates the exact hazard the cookie's scope was designed to avoid; every edit
would pay two serializations; and a process calling itself through its own
connection pool deadlocks under saturation rather than degrading.

**Have the browser POST straight to `/admin/v1` with the token in a header.**
This needs JavaScript and a token readable by JavaScript — which means the
session cookie stops being HttpOnly. In a panel that runs inside an
administrator's session, that trade turns an XSS from an annoyance into a full
account takeover.

**Keep the panel read-only and link to the API documentation.** Honest, and it
leaves the operator exactly where they were: looking at a wrong title they
cannot fix.

## Reopening the decision

Reopen when a second module needs an admin surface. Two is a pattern and three
is a framework: at that point the question is whether the surfaces should be
generated from the modules' own service methods rather than written by hand,
and whether `.admin` should become a suffix the container itself understands.

Reopen decision 4 if the panel ever needs a write that has no meaningful
degraded state. Today "editing unavailable" is a complete answer; a panel whose
main job was writing would be lying by opening at all.
