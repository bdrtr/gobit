# What a suite can tell about a verifier — measured 2026-09-10

Serves [ADR 0126](../adr/0126-a-customer-identity-passes-a-published-suite.md).

## The interface, and why "reads a header" is not the rule

`corehttp.Identity` has one method and the request is passed whole. Its own
godoc names the sources an implementation may read, and one of them is "a header
written by an upstream proxy". That sentence is right — behind a gateway that
authenticates and STRIPS, the header is proof — and it is why the obvious rule
does not work: a suite forbidding a verifier to read a header would refuse a
shape the contract explicitly allows.

What separates the two implementations is not the header. It is whether anything
strips it, and no test holding a `*http.Request` can see a gateway.

## What is checkable without a key, a store or an upstream

Four rules survived that filter.

| Rule | The implementation it refuses |
|---|---|
| a request carrying nothing proves nobody | one that returns a constant |
| a request cannot name its own customer | one that reads the claim and hands it back |
| an error comes alone | one that returns an identifier it disowns |
| the request survives being read | one that consumes the body looking for a session |

The fourth is the one whose symptom lands furthest from its cause: the body is a
stream, so a verifier reading it leaves the handler nothing, and every storefront
POST then fails to parse a body that was there.

## The declaration, and why it is not an escape hatch

An implementation that really does trust a stripped header says so by
implementing `identitytest.UpstreamTrust` and naming the header. The suite then
probes every other surface and leaves that one alone.

The two implementations in the suite's own tests — `upstreamIdentity` and
`silentUpstreamIdentity` — are the same code. One declares and passes; the other
does not and is told, by a failure that names the interface to implement. That
is the whole value: the declaration costs one method and buys an author who has
looked at the question.

## The probe's surfaces are a NAMED list, and it says so

A request has no schema saying where a claim may ride, so the header list is
written out rather than derived — eleven names a first implementation reaches
for, plus the query string, two cookies and a JSON body. A verifier reading a
header not on the list is not caught by this check, and the package says so where
the list is.

The list was mutation-proved in four directions: removing the probe, emptying the
body, dropping the cookies, and exempting an UNDECLARED header each failed the
test written for that surface and no other.

## What has no in-tree consumer, and why that is not ADR 0063's case

Every `corehttp.Identity` in this repository is a test double — `signedInAs`,
`pathProvingIdentity` — and a double is built to prove whatever its test needs.
Making them pass a compliance suite would be a category error, so no gate walks
the tree the way ADR 0077's does for providers.

The audience is outside the tree, and it became a real audience three records
ago: ADR 0125 made every storefront route naming a customer refuse until an
embedder binds a verifier. The suite is demonstrated by its own fixtures — five
implementations that must fail, one that must pass — which is how a test suite is
shown to work at all.
