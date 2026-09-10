# ADR 0120 — An exchange can take its difference

**Summary:** An exchange's positive difference is funded by naming the payment
collection it was collected into, and the row keeps that identifier and the
moment rather than the amount. It costs a fourth status and buys the completion
migration 000008 took away.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

Migration 000008 removed the exchange's completion and named what would bring it
back: goods out, and money in when the difference is not zero. ADR 0090 built
the goods; the money took three records. ADR 0117 kept the sale's link to its own
collection, ADR 0118 made a collection's gate read the capacity left, and ADR
0119 decided that an order row does not mirror an amount the payment module owns.

What was left was where a withdrawal guard goes. Measured against the tree, none
of the shapes that guard the WITHDRAWAL survives: reading the money there needs
a module this one may not ask, and reading a counter instead produces a record
that can never be taken back and an order that can never be forgotten.

The answer moved the question. An exchange that has taken the customer's money
is not a request that can simply be withdrawn, and a vocabulary that cannot say
so leaves every rule about it bolted on somewhere a caller has to remember.

## Decision

An exchange's positive difference is funded by NAMING the payment collection the
operator collected it into; the row keeps that collection's identifier and the
moment, never its amount. A funded exchange has left `requested`: its goods can
complete it, the ordinary withdrawal refuses it, and the exit is one act that
sends the money back and takes the request with it.

## Consequences

The status vocabulary is four words instead of three, and it is published.

The completion's bound stays in the schema, where migration 000017 put it, and
only because the row keeps an IDENTIFIER: a constraint can see a column and
cannot see a binding recorded through the link layer. This supersedes ADR 0117's
second sentence, which named a link because a link was the shape in view; the
tree's own shape for a cross-module identifier written by the flow that holds
both sides is a column (migration 000012), and only a column keeps the rule.

Everything about the MONEY is asked live, by the flow, at the moment it decides
something. The framework opens no collection and moves no money: the operator
collects through the payment module's own published endpoints, and this records
which collection answered.

A funded exchange holds erasure open exactly as a requested one does; adding a
status without adding it there would have opened a hole the older one did not.

The refusal has an exit, one call rather than two so the halves cannot be done
in the wrong order. Without it the refusal would be a trap: an exchange whose
goods turn out to be unsendable would sit funded for ever. The refund happens
before the record follows it and no transaction spans the two modules, so a
crash between them leaves the money back and the record still funded — the
honest direction, and the exposure the return's refund already accepts.

A negative difference reaches none of this. Only a positive one can be funded,
and a completion still needs a zero difference or a funding, so what migration
000017 protected is narrowed rather than loosened.

Measurement: [measurements/0120](../measurements/0120-the-guard-moves-to-the-binding.md)

## Rejected

**Keeping the amount on the row.** ADR 0119's rule: a figure the payment module
owns can be changed by a route this one never hears about.

**A stamp without a status.** The rule about withdrawing would then live beside
the transition table instead of in it, and every caller would have to remember it.

**Clearing the funding so the ordinary withdrawal works.** It is the reopen ADR
0055 refused, and it destroys the only local record that money ever moved.

**Letting the module's own cancel route decide.** It cannot ask what the
collection still holds, and a guard that guesses is worse than one that refuses.
