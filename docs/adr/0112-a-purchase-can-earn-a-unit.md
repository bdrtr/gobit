# ADR 0112 — A purchase can earn a unit

**Summary:** A buyget promotion counts the units its buy rules select and gives
away the cheapest units the purchase did not consume. It costs a required unit
price on the discount request and buys the one mechanic every competitor ships.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

`buyget` existed as a word in an enum and nothing else. The type could be
written, the promotion could not be activated, and the computation skipped it —
three refusals standing in for a mechanic that was never built.

Three things were missing, and each was a different kind of gap. Rules were
`context` and `target`, so nothing could say WHICH lines are bought as opposed
to rewarded. The application method measured an amount and never a count, so
nothing could say HOW MANY units the reward pays for. And the computation input
carried the line amount but not the unit price, so the reward — which is priced
per unit — could only have been derived by a division that rounds.

"Buy two, get one" is the promotion a merchant asks for first.

## Decision

A buyget promotion counts the units its `buy` rules select against
`buy_quantity`, and then discounts `apply_to_quantity` of the cheapest units the
target rules select that the purchase did not already consume. The discount
request carries `unit_amount` on every line, required to satisfy
`unit_amount × quantity = amount`.

## Consequences

A bought unit is not also a rewarded unit, so "buy two, get one" needs THREE
units in the cart. The other reading gives the shopper two for the price of one
under the same wording, and no merchant means that.

The purchase consumes the MOST EXPENSIVE units and the reward lands on the
cheapest ones left: the supermarket's own rule, and the merchant-safe direction.

The reward is granted ONCE per computation; the repeating ladder is a different
promise and its trigger is a merchant writing one down.

The unit price is REQUIRED of both callers — the cart flow and the admin
computation endpoint — and the identity is enforced. An optional field would
have made the mechanic work for one caller and silently not for the other.

The mechanic and the method must AGREE: a buyget with no counts, and a standard
promotion carrying them, are both refused with `reward_mismatch` and reported to
the operator (ADR 0110). Not applying is the safe direction, the pairing is a
CHECK so a row written by hand cannot carry half of it, and the `not_standard`
skip word is gone because the engine now applies both mechanics.

The storefront's coupon lookup answers with the MECHANIC and the two counts, and
refuses a coupon the computation would refuse. A buyget carries ten thousand
basis points, so a body without the mechanic would read as "100% off".

`allocation` and `max_quantity` are not read on this path. The reward counts
units, and how many is what the method's own pair says.

## Rejected

**Deriving the unit price by dividing the line amount.** Three units of 100 read
back as 33 and the shopper is given a cent less than the promise. The producer
knows the number.

**Reusing `max_quantity` as the reward count.** It bounds units of a SINGLE line
a fixed discount repeats over; the reward bounds units across every target line.
One field, two meanings, is how a merchant configures the wrong one.

**One rule type for both sets.** "Buy two shirts, get a tie" cannot be written
with a single selection.

**A repeating ladder.** It is a second promise ("every third one free") and
needs a record of its own.

**Refusing the mismatch when the method is written.** The type lives on another
table; the check would need a second read and would still be a snapshot, while
the computation reads both together and answers with a word.
