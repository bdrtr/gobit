# ADR 0102 — The order hands the document every rate it charged

**Summary:** The order's invoice surface emits `tax_components`, which ADR 0097
said it already did. It costs one field and the two tests that bind a producer
to its reader.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0097 states that the breakdown reaches the document, and it does not. The
invoicing flow was taught to read `tax_components`, the invoice module to
validate and store it, and the flow's test passed — while
`interopInvoiceItem`, the surface the order actually encodes an order into,
carried no such field and never has.

The reader read a key the producer never wrote. Every total still added up, the
document still printed the stack's base rate, and the record said the limit had
closed — so `known-limits.md` lost the entry that was the only remaining
statement of the truth.

What hid it was the flow's own test fake. `fakes_test.go` marshals an order
shape written BY HAND in the consumer's package, and that hand-written shape had
the field. A fake that says what the real producer cannot is worse than no test:
it reports a green chain that does not exist.

## Decision

`interopInvoiceItem` carries `tax_components`, and two tests bind the hop: one in
the order module asserting the WIRE the producer emits, and one end-to-end that
walks order → invoicing flow → invoice module → HTTP read with no fake in it.

## Consequences

The rule this leaves is about tests rather than fields: a consumer's fake proves
the consumer, never the contract. Where two modules meet by JSON name, the
PRODUCER needs its own assertion on the wire, because the compiler cannot see
across the boundary and a hand-written fake will happily invent the other half.

The limit ADR 0095 opened is closed now, for the reason ADR 0097 gave. It stays
out of `known-limits.md`, and the sentence in ADR 0096 that said it would be
"narrowed to that hop" is superseded by this record rather than edited.

The end-to-end test builds its order through the order service rather than
through a cart, because a stacked line from a real cart needs a tax region with
a stacked rate. What it gives up is that the tax module is not in the chain;
what it keeps is every hop the breakdown was actually lost on.

## Rejected

- **Fixing the field and leaving the tests as they were** — the same fake would
  keep proving the same thing, and the next field crosses the same gap.
- **Making the flow's fake import the order module's type** — a workflow that
  imported a module's package would break the isolation the boundary exists for
  (ADR 0001).
- **A gate comparing the two structs by reflection** — they are in different
  packages and both unexported; the comparison would have to be written in a
  third place that neither side changes when it renames a field.
