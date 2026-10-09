# ADR 0434 — An admin route demands a privilege

**Summary:** gobit owns `/admin/v1` and `/admin/ui`, every route bound under `/admin/v1` demands a privilege through `corehttp.RequireScope` but the person's own auth routes, and every route under `/admin/ui` is the panel's, or the installation does not start; `contrib/identity-session`'s credential route names its customer in the path and demands `customer-credential:write`.
It costs a module or plugin that guards an admin route any other way, binds under the panel's address or claims a prefix's catch-all a startup refusal, and an integration that writes credentials a new grant and a new path.

- **Status:** Accepted
- **Date:** 2026-10-09
- **Amends:** [0127](0127-a-working-identity-ships-outside-the-module.md), whose credential route names its customer in the path and demands `customer-credential:write`; [0379](0379-an-account-is-told-what-changed.md), whose operator's replacement moves to that route and still sends nothing; [0255](0255-a-panel-route-is-its-method-and-its-path.md), whose table now refuses a route anybody else binds under the panel's address; [0263](0263-an-operation-names-the-privilege-its-route-demands.md), whose guard the installation reads at startup as well as in the document

Measurement: [measurements/0434](../measurements/0434-who-may-write-a-password.md)

## Context

`contrib/identity-session` mounted `PUT /admin/v1/customer-credentials` behind
gobit's admin ring and demanded no privilege of the caller. The ring proves
who is calling; what they may do is each route's `corehttp.RequireScope`. Any
admin principal, a panel operator or an API key holding only `product:read`,
could set any customer's password and sign in as that customer (D270). A
marketplace built on gobit found it from outside. The route writes the e-mail
too, so its caller also chooses where the customer's reset goes, and ADR 0379
tells the customer nothing. gobit's own admin routes each demand a privilege
but the person's own auth routes; nothing held a module or plugin from
elsewhere to that. chi matches patterns, so a parameter where `/admin` stands,
a catch-all, a router's not-found handler or a route bound twice answers an
admin path that no literal prefix names, and ADR 0255 held only the routes
the panel binds itself.

## Decision

The credential route becomes `PUT /admin/v1/customer-credentials/{customer_id}`
and demands `customer-credential:write`, a privilege of its own that `admin`
covers, and an installation does not start while a route bound under
`/admin/v1` demands no privilege through `corehttp.RequireScope`, unless it is
one of the person's own auth routes served by the auth module, or while a
route under `/admin/ui` is not one the panel's scope table lists and the panel
serves. gobit binds each of the two prefixes and its catch-all itself, so a
path there that no route of the surface's takes is gobit's 404 or 405 and no
pattern or fallback bound elsewhere answers it.

## Consequences

- Whoever holds `customer-credential:write` can sign in as any customer, read
  what that customer reads, act as them and choose where their reset goes; the
  customer is told nothing (ADR 0379). Every other privilege gets 403.
- It is granted over the API, as `personal-data:*` is; the panel offers it on
  the page of a user who already holds it.
- The audit log's row of a credential write names its customer in the path.
- chi prefers the prefixes' static segments, so a storefront's page router or
  single-page app at the root starts as it is and answers no admin path. A
  route on a prefix's catch-all, a router mounted above a prefix with routes
  under it, a router of a module's own type mounted under one, and a pattern
  carrying a percent-encoded byte stop the installation; a router mounted
  under a prefix answers its unbound paths with gobit's.
- The gate walks the router once everything is bound, wherever an
  installation is assembled, and reads every middleware a route passes
  through, so a guard on a group, a sub-router or a mounted router counts. A
  privilege checked in a handler, or behind a middleware wrapping
  `RequireScope`, does not.
- The exempt routes are signing in, accepting an invitation, reading one's own
  identity, signing out, and those on one's own second factor and sessions,
  each held to its method, its path and the auth module's handler; the
  end-to-end walk reads the same list.
- The gate holds that an admin route demands a privilege, not which one or in
  what position, and never sees a route bound after the assembly.

## Rejected

- `customer:write`: it is the grant of every operator who corrects an address, and the credential reads what that grant never could.
- `admin`, as an operator's password demands: an integration that onboards customers would hold the power to write privileges.
- Telling the old address when a credential is replaced: not decided here, and no fault has asked for it.
- An end-to-end walk instead of a startup refusal: it covers this tree's installation, not an embedder's with a contrib module or a plugin of their own.
- A warning at startup: an open route that logs is still open.
- Exempting `/admin/v1/auth` by prefix, or by path alone: the next route there, or a module's handler bound over one, would inherit the exemption.
- Refusing a route by what its pattern can reach: a storefront's page router and a single-page app at the root would have to move.
- Refusing while a module or plugin mounts: each sees its own routes, and the composition root's routes and the mounting order are the assembly's.
