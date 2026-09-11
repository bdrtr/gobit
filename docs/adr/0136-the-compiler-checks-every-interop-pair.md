# ADR 0136 — The compiler checks every interop pair

**Summary:** One file in `internal/arch` assigns every container-resolved
producer to the interface its consumer declares, and a gate keeps that list
complete. It costs a test package that imports the whole tree, and closes a class
whose only other alarm was a request that had already failed.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

A consumer declares the narrow interface it needs in its OWN package and resolves
the concrete value by name (Principle 2.1/2.4, ADR 0001). Neither side imports the
other, so a drift compiles on both and fails at RESOLUTION — on a request path,
wrapped by a `sync.Once` that caches the failure for the life of the process,
with startup green and the route described.

It had already happened. `*cart.Interop` carried neither `ApplyPromotionCode` nor
`RemovePromotionCode`, so `POST /store/v1/carts/{id}/promotions` and its DELETE sibling
answered 500 to the first customer who typed a coupon code — every coupon test called
the flow's method directly, and the module's nil guard passed because the WRAPPER was
present. Gap D73.

The producer of another such surface explained why nobody had checked, in its own
godoc: "the compiler never sees them together". That sentence is false, and it is
the reason this file did not exist.

## Decision

`internal/arch/interop_pins_test.go` holds `var _ <consumer interface> =
(*<producer>)(nil)` for every interop pair, and
`TestEveryConsumedInteropNameIsPinned` derives the consumed-interop population the
way `TestTheInteropSurfacesHaveAConsumer` already does and requires each name to be
pinned or exempted with a reason.

## Consequences

A third package in the same Go module may import both sides. Module isolation
forbids the two participants importing each other; it says nothing about a test
package that imports everything, and that is all the compiler needs.

The pins live in `internal/arch` rather than `internal/e2e`: both can import the whole
tree, but e2e is Docker-gated, and these are compile-time facts that belong in the lane
that compiles — which is all of them.

The gate prices NAMES; the compiler checks SHAPES. A pin written against the wrong
interface passes the gate and fails `go build`, which is the division of labor
rather than a hole: the list being complete and the shapes being right are two
questions and each has its own answer.

It found two gaps in the hand-written list on its first run — `auth.interop` and
`product.interop` — which is the argument for deriving the population rather than
trusting the list. It is the fifth such list checked against the world here.

A pin is not a test of behaviour. The coupon endpoints also gained an e2e test that
drives the ROUTES, because a pin cannot say a request reaches the flow — proven by
mutation: removing the bridge fails the build, and delegating to the wrong method
passes the build and fails the route.

Two exemptions, each with its reason in the file: `core.identity` is filled by the
embedding application and has no producer here (ADR 0008/0043, with
`core/identitytest.Contract` holding the shape instead), and `pricing.service` is
pinned but does not end in `.interop`, which is what the derivation prices.

Measurement: [measurements/0136](../measurements/0136-what-the-compiler-was-never-shown.md)

## Rejected

**An AST gate comparing method sets as source text.** It would re-implement the
type checker to answer a question `go build` answers exactly, and it would have to
resolve aliases, embedded methods and third-package parameter types.

**Pinning inside each producer's own test.** Four producers already do, and the
other eleven did not — which is precisely the shape that fails: a per-package
convention with no derived population.

**Leaving it to the e2e lane.** That lane caught the drift only where a test drove
the path; the coupon endpoints had none, which is why the defect shipped.
