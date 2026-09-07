# ADR 0043 — gobit REQUIRES a customer identity at the address book, and still does not issue one

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap
- **Extends:** ADR 0008, which STANDS and is not superseded

## Context

Gap A9 asks one question and it has only two answers: ADR 0008 stands, or it is
superseded. It is the upstream of customer login, of passkeys, of A/B
assignment, and it is the reason defect D3 is open.

**Every premise ADR 0008 rested on is still true, and each was re-checked
against the code rather than against the record.** `Principal` in
`core/http/auth.go` carries four fields — the identifier, the kind, the scopes
and the sales channels — and no customer among them. `RequireStore` in the same
file reads one header, the publishable key, and that key binds a request to a
sales channel. `POST /store/v1/customers` mints a fresh guest record with
nothing but that key. Both tripwires ADR 0008 planted in
`internal/modules/order/service/spending_test.go` are still there and still
green. Nothing about the boundary has moved in six days.

**One word in the record of it was never right, and it matters here.** The
defect ledger calls the address book "unauthenticated", and so does the godoc on
`GuardOptions.Audit`. The store surface IS authenticated: the six address routes
sit under the store prefix in the guard stack built by `core/http/guard.go`, and
a request arriving without a publishable key never reaches a handler — it takes
a 401 from `RequireStore`, which writes `WWW-Authenticate` and a typed
`Unauthorized`. What the address book lacks is not authentication but
AUTHORIZATION: nothing anywhere asks whether the caller is the customer the path
names.

**What that costs, counted.** `internal/modules/customer/api/api.go` binds NINE
store routes. Eight of them read the customer from the path through one
function, `storeCustomerID`, which returns the chi path parameter unexamined.
Six of the eight are the address book, and `toAddressDTO` in
`internal/modules/customer/api/dto.go` hands back first name, last name,
company, both street lines, city, postal code and phone. The other two are the
profile, and `toCustomerDTO` beside it hands back e-mail, both names and phone.
So the chain a reader usually draws through the cart and order response bodies
is one hop longer than it needs to be: a single `GET` against a known id is
already the whole of it. `internal/modules/b2b/api/api.go` has two more store
routes and its OWN copy of `storeCustomerID` over its own path parameter.

**ADR 0008 assigned the closing work to the embedder and named the places to do
it. NOT ONE of them can be reached from outside this repository.** Its
Consequence 3 says the identifier must come from the embedder's session rather
than from the body and that a mismatch must return `errors.Forbidden`, and it
names three places that will change: cart creation in `cart/api/store.go`,
`b2b/api`'s `storeCustomerID` helper and `order`'s `spendingRuleFor` entry
point. A fourth was named afterwards and not by that record: the package godoc
of `internal/modules/customer/api` cites the same consequence and calls its own
`storeCustomerID` the one place to bind. Every one of the four is an unexported
symbol — two package-level functions, plus `cart/api`'s `storeCreateCart` and
`order`'s `spendingRuleFor`, which are methods — and all four live under
`internal/`, which the Go toolchain itself refuses to an outside module. An
embedder that reads ADR 0008 and does exactly what it says has to fork. That is
the sentence this decision exists to fix, and it is not an argument about taste
— it is a rule of the language.

**The obstacle that had kept the question open is false, and it was broken by
compiling rather than by arguing.** The belief was that an out-of-tree program
has no seam through which to supply an implementation, so publishing an
interface would change no request. Measured 2026-09-07: a separate Go module —
its own `go.mod`, a replace pointing at this repository — importing ONLY the
published facade, `core/module` and `core/container`, implementing
`module.Module`, calling `Container.Provide` under a name of its own inside
`Module.Register`, and handed to the facade through `App.Add`. `go build ./...`
exits 0. The seam is two published calls wide and `core/plugin/plugin.go` even
documents the second one, saying that registering your own service is safe.

**The ordering objection is false too, and this repository ships the
counter-example inside the very feature ADR 0008 is about.** In
`internal/app/app.go` the `order` module is added before `b2b`, and the
embedder's own modules are added after everything in the box. `order`
nevertheless reaches b2b's spending policy at REQUEST time, through the
`sync.Once` wrapper in `internal/modules/order/module.go` that resolves the
provider on first use and treats `errors.IsNotFound` as "the concept is not
installed". Registration order forbids replacing a name that is already TAKEN;
it does not forbid resolving a NEW one lazily. `Registry.Modules` in
`core/module/registry.go` returns the embedder's modules along with the rest,
so an out-of-tree module is inside every scan the composition root makes.

**And the idiom is not new here.** A capability that a module may or may not
have is already found three times by exactly this shape: `openapi.Describer` in
`internal/app/setup.go`, `personaldata.Eraser` and its two siblings in
`internal/workflows/datasubject/datasubject.go`, and
`provider.SessionInspector` in `internal/modules/payment/service/reconcile.go`.
`examples/starter/loyalty/loyalty.go` is an out-of-tree module satisfying a
published optional interface today, and `TestTheOutOfTreeExamplesCompile` in
`internal/arch/public_surface_test.go` keeps it honest by building it from
outside.

## Decision

**ADR 0008 stands. gobit does not issue a customer identity, does not verify
one, and will not.** The framework holds no proof about the person behind a
storefront request and nothing in this record gives it one.

**gobit PUBLISHES the interface the embedder implements.** A one-method
interface named `Identity` in `core/http`, beside `Authenticator` and
`Principal`: it takes the request and returns the customer identifier it proves,
or an error. Every type in the signature is stdlib, which is what makes it
satisfiable by a type defined outside this repository — the property the compile
above measured.

**The embedder binds it by name, in the container, from an ordinary module.** It
is resolved under a CORE-owned service name, so the direction of naming runs
module to core — the same direction `internal/modules/customer/module.go`
already uses when it resolves the core pool. The resolve is LAZY, on first use,
in the wrapper shape `order` uses for the spending policy. That is what keeps
the note in that module's `Register` true: customer resolves no other MODULE's
service at registration, so it creates no ordering dependency, and the
embedder's module may register last.

**The address book requires the claim to be BACKED.** `storeCustomerID` stops
returning the path parameter unexamined. It resolves the identity, compares what
the identity proves with what the path says, and returns `errors.Forbidden` on a
mismatch. An error from the identity is passed to the core's error writer, so
the embedder chooses the status by choosing the error's kind.

**With no identity bound, those routes are CLOSED, not open.** The request is
rejected with a code naming the missing binding, the way `DeferredAuthenticator`
already rejects a request that arrives before the authenticator is bound. This
is the row ADR 0007 wrote for an unconfigured authenticator — reject every
request — and not the row it wrote for an unconfigured rate limiter.

**"Mandatory" here means backed, not declared.** The declaration is already
mandatory on all six address routes: the identifier is a required path segment,
so there is no guest path to break. That is what distinguishes this from the
mandatory declaration ADR 0008 rejected, which was about the cart BODY on a
surface whose default path is guest purchasing.

**The contract is written on the function, not on the route list.** The two
profile routes read the identity through the same `storeCustomerID` and get the
same check. Writing a second, weaker helper so that the scope matched the word
"address book" would leave e-mail and phone readable by id in order to keep a
sentence tidy.

### Where it lives, and what that costs on the published surface

`core/http` is already published, and the three audits in
`internal/arch/public_surface_test.go` key on package DIRECTORIES rather than on
symbols — `publishedPackages` is a list of seventeen entries, the facade plus
sixteen under `core/`. Adding a method-bearing interface to a directory already
on that list costs nothing there. A package of its own under `core/` would cost
one line in that list plus an amendment to ADR 0026, a price this repository has
paid twice already, and it would buy a home for a concept that has exactly one
method.

## Rejected alternatives

**Supersede ADR 0008 and build a customer session inside gobit.** It would close
the whole hole in one place and ask the embedder for nothing, which is the only
alternative here that fixes the cart as well as the address book. What kills it
is the list ADR 0008 wrote when it rejected the same thing: who issues the
token, how long it lives, how a guest cart is handed to a registered customer,
and what happens to the admin authorization model when `Principal` starts
carrying a customer. An interface clears three of those by construction — the
embedder issues, the embedder sets the lifetime, and `Principal` is untouched —
and clears the fourth not at all. Issuing a session also means a library owning
a cookie, a signing key and a rotation policy for a storefront it does not
serve, which is the shape ADR 0025 refused when it decided gobit is a library
rather than a template.

**Leave ADR 0008 exactly as it is and add nothing.** It costs nothing and it was
the standing answer. What kills it is the compile: the reason for doing nothing
was that an outside program had no seam to supply an implementation, and that
reason is false. What remains without it is a framework that documents a hole,
assigns the fix to the embedder, and points the embedder at an unexported
function inside `internal/`.

**Fall back to today's behaviour when no identity is bound.** It is the
`SpendingPolicy` precedent and it would break not one installation. It is
rejected because the two absences do not mean the same thing. A missing spending
policy means "this installation has no B2B", which is a complete and correct
answer. A missing identity does not mean "every caller is who they say they
are"; it means nobody looked. ADR 0007 already separated those two rows by
exactly this test, and a mandate that is off by default is the strict-mode flag
ADR 0008 rejected with the switch moved from an environment variable to an
omission.

**Install it as a middleware the embedder adds to the router.** It would need no
container name and no module, and it is the first thing an HTTP author reaches
for. Two things kill it. Mechanically, chi v5.3.2 panics on a `Use` that arrives
after a route is registered, and `core/http/router.go` registers `/health` and
`/ready` before it returns the router — which is why `DeferredAuthenticator`
exists at all. And by scope: a middleware on the store prefix puts a customer in
the request context for the WHOLE surface, where the cart would then read it —
and the cart is the guest-to-registered handover this decision deliberately does
not touch.

**Add a field to `GuardOptions`, or a fifth method on the facade.** It would be
typed, discoverable and impossible to misspell, which is more than the container
name gives. It is rejected because the guard is core middleware running on a
path PREFIX and would have to carry a context key for a concept only one module
reads, and because the facade's own package doc says re-exporting a published
contract there would double the surface without adding a capability. The
container-by-name resolve is the idiom this repository already uses three times
for a capability that may be absent.

**Put the interface in a new `core/identity` package.** It would give the
concept room to grow — a customer principal, scopes, a session — instead of
wedging it beside the admin one. Rejected on ADR 0026's terms: a published
package is a promise that cannot be withdrawn before `1.0.0`, this is one method
with one implementation, and `core/http` already holds `Authenticator` and
`Principal`, so the concept has a home that costs nothing. It is the same trade
ADR 0038 made when it refused to hoist a two-line function into `core/`.

## Consequences

**Positive**

- **The work ADR 0008 assigned becomes satisfiable from outside.** That is not a
  claim about design; it is the compile in the context above, a separate module
  building against the published surface and exiting 0.
- **The address book stops being open BY DEFAULT.** The failure mode of an
  embedder who never read ADR 0008 changes from a silent leak of a person's
  street address to a rejected request naming the binding that is missing. This
  repository's most expensive fault class is the one that produces no error, and
  this moves the address book out of it.
- **`Principal` does not move, so the admin authorization model does not
  either.** One of the four blockers ADR 0008 listed against a signed customer
  token is cleared by construction rather than by decision.
- **The boundary keeps its shape.** gobit still verifies nothing. It requires
  the embedder's verifier and refuses to guess in its absence, which is a
  stronger version of the same sentence rather than a different one.

**Negative, and accepted**

- **An installation that binds no identity loses its address book on upgrade.**
  Six routes, plus the two profile routes, go from answering to rejecting. The
  `0.x` contract in the changelog permits a breaking change in a minor release
  and the current tag is `v0.8.0`, so the permission exists — but the permission
  is not the cost. **How many installations that is cannot be measured from
  here.** This repository contains two out-of-tree Go modules, both of them its
  own examples, and `cmd/server` which is deliberately nothing but the example.
  Counting somebody else's consumers as gobit's is a mistake this project has
  made before and it is not repeated here: the honest statement is that the
  affected population is unknown and the change is loud rather than silent.
- **The container name is a cross-boundary contract held by two string literals
  that no compiler compares.** The embedder spells it; the customer module
  spells it. That is precisely the pairing class ADR 0040 created and named, and
  it is created here for the second time with nothing auditing either one. A
  renamed slot would make every address book request reject — loudly, which is
  the better half of the failure — but nothing would say why.
- **A second copy of the same boundary stays open, and it is the one ADR 0008
  actually named.** `internal/modules/b2b/api` has its own `storeCustomerID`
  over its own path parameter on two store routes, and this decision does not
  move it. One contract, two modules, two functions — and the copy this record
  closes is the one ADR 0008's list left out.
- **The sharpest half of ADR 0008 is untouched.** The measurement in that record
  — a foreign client completing a purchase in an employee's name and burning
  their spending window — reproduces exactly as it did, because the cart still
  takes `customer_id` from the body and still permits the handover. This
  decision closes the address book, not the checkout.
- **The two tripwires stay GREEN, and ADR 0008 says they should have gone
  red.** Its Consequence 2 names both tests in
  `internal/modules/order/service/spending_test.go` and says that when a layer
  that verifies identity is added they are expected to fail, and that their
  failing on that day is the sign that the decision was really taken. Only ONE
  of the two repeats that in its own godoc:
  `TestTrustBoundaryGuestOrderIsNeverAskedForTheSpendingRule` says the day a
  customer session arrives is the day it falls, while
  `TestTheSpendingRuleIsAppliedToTheDeclaredCustomer` says only that the
  argument of its call will then have to come from the session rather than from
  the body. A layer arrives here and neither fails, because the layer lands at
  the address book and they watch the order service. That is correct rather than
  a miss — but it means the sign ADR 0008 designed does not fire, and a reader
  who trusts the sign alone will conclude nothing happened.
- **The customer module gains machinery it was written not to have.** Its
  `Register` godoc explains that it resolves only the core pool, on purpose,
  because resolving another module's service is the one thing that creates an
  ordering dependency. The lazy wrapper keeps that true, and it is not the first
  of its shape in the tree — `order` has one for the spending policy and another
  for the invoicing flow, and `cart` has its own — but a module that had none now
  has one.
- **Two places in the repository call the storefront "unauthenticated" and are
  wrong today, not only after this change.** The defect ledger's D3 entry and
  the godoc on `GuardOptions.Audit` both say it. The store surface is
  authenticated by a publishable key and unauthorized for the customer, and
  fixing those two sentences is not part of this record.

## What this deliberately does NOT do

- **It does not implement any of this.** No interface is added, no wrapper is
  written, `storeCustomerID` still returns the path parameter. This record
  decides the contract; the code is a separate change and the audit the
  container name needs does not exist yet either.
- **It does not close the spending limit escape.** The cart accepts
  `customer_id` in the body on creation and on handover, and `order` applies the
  rule to whatever identifier it is handed. ADR 0008's condition — the limit
  applies to purchases that declare their customer, and gobit does not verify
  the declaration — is unchanged.
- **It does not decide the guest-to-registered handover.** That is the one
  blocker of ADR 0008's four that an interface does not clear, and scoping this
  decision to a surface with no guest path is how it is avoided rather than
  answered.
- **It does not put a customer in `Principal`, in the request context, or in the
  guard stack.** The identity is resolved by the module that needs it, at the
  moment it needs it, and nothing else in the tree can see it.
- **It does not make gobit verify anything.** An embedder who binds an
  `Identity` that returns the path parameter back has changed nothing, and the
  framework cannot tell. What the framework now refuses is to proceed when
  NOBODY has been asked.

## Related

- [ADR 0008](0008-musteri-kimligi-guven-siniri.md) — the trust boundary this
  record extends and does not move, and the source of every premise re-checked
  above.
- [ADR 0007](0007-sertlestirme-arizada-davranis.md) — the per-component failure
  model, whose first row is where the closed-by-default answer comes from.
- [ADR 0026](0026-the-published-surface-is-fourteen-packages.md) — the published
  surface, and why the interface goes into a package that is already on it.
- [ADR 0029](0029-the-embedder-is-the-data-controller.md) — who owes the person
  whose address the address book holds.
- [ADR 0040](0040-in-stock-is-a-catalog-answer-over-an-inventory-fact.md) — the
  first contract held by two unaudited literals; this decision creates the
  second.
