# ADR 0281 — Raising a line asks the channel again

**Summary:** A cart line's quantity can rise only while its variant is in the
request's sales channels, as when it was added; lowering, removing and
completing still ask nothing.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The sales channel scope is enforced at the entry gate: a variant outside the
request's channels can never be added to a cart. The update of a line's
quantity did not ask it again, and the known limits named the consequence: a
product moved to another channel after it entered a cart could be bought in any
quantity by the cart that held it — stock set aside for one channel drained
from another. The reason not to ask was the cart being a snapshot, and its
alternative was asking at completion, which would make a catalog edit turn a
customer's full cart unpayable. A raise is not that alternative: it asks for
more units, which is an entry of more units.

## Decision

`UpdateLineItem` reads the cart's line and, when the new quantity is above the
line's, asks the variant's scope as `AddLineItem` does, refusing an
out-of-scope raise with the same not-found and writing nothing. A quantity that
stays or falls, a removal and the completion ask nothing.

## Consequences

- A customer keeps what their cart holds of a product that left their channel,
  and can lower it or buy it as it is.
- The refusal of a raise reads as the refusal of an add: a hidden product does
  not reveal itself through the code.
- Every update reads the cart once before writing; a cart that cannot be read
  refuses the update rather than raising unasked.
- An administrator's edit of a cart line is out of reach: the admin surface
  writes no quantity (ADR 0146).

## Rejected

- Asking at completion as well: a catalog edit would make a full cart
  unpayable, the cost the original decision named.
- Capping a raise at the quantity the line had when the product left: the cart
  does not record when that was, and the line's current quantity already is the
  cap.
