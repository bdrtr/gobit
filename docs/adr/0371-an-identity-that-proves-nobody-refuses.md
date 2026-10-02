# ADR 0371 — An identity that proves nobody refuses

**Summary:** An error a customer identity does not classify refuses the
request as Unauthorized, as an authenticator's does under ADR 0364, and a
surface open to anonymous callers asks who a request proves, if anybody.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

`corehttp.ProvenCustomer` passed an identity's error through unwrapped, so the
embedder chose the status by choosing the error's kind. An error that chose no
kind reached the core's default, an internal fault. The identity gobit ships,
`contrib/identity-session`, refuses a request with no session, an expired one
or a forged one with exactly such an error: every storefront route naming a
customer answered an anonymous caller 500 `internal_error` (D220). The e2e
harness never saw it, because its own verifier classified its refusal. A review
written by a buyer needs the opposite door as well: who a request proves, when
proving nobody is an answer rather than a refusal.

## Decision

An identity's unclassified error refuses the request as Unauthorized
`identity_refused`; a classified one passes as the embedder chose it, and only a
classified unavailable or internal error is a failure to check, as for an
authenticator. `corehttp.ProvenCustomerIfAny` answers the customer a request
proves, or nobody on any refusal, and fails only when the proof could not be
read.

## Consequences

- An anonymous or forged request to the fifteen storefront routes naming a
  customer answers 401 where it answered 500, with every identity that refuses
  with a bare error, the shipped one included.
- The identity's own error stays in the chain, so the log still says why.
- `core/identitytest.Contract` fails an identity that answers an empty request
  as unavailable or internal: nothing was there to check.
- The e2e harness's verifier refuses with a bare error, as the shipped one
  does, so the production wiring is exercised on the path that failed.
- `CodeIdentityRefused` and `ProvenCustomerIfAny` join the published names of
  `core/http`.

## Rejected

- Classifying the error in `contrib/identity-session` alone: every other
  identity returning a bare error would still answer 500, and the contract
  would still pass it.
- Mapping every identity error to Unauthorized: an identity whose session store
  is down would sign every shopper out, the failure ADR 0364 refused for
  authenticators.
