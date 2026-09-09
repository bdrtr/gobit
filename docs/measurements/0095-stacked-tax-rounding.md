# Measurement 0095 — where a stack's rounding residue goes

Written for [ADR 0095](../adr/0095-a-rate-can-stand-on-another.md). Nobody has
to read this; the record it serves states the rule in one line.

## The rule

Every component of a stack is floored ON ITS OWN, on the base that component is
computed over. There is no floor at the end and no effective rate.

## Why not one floor at the end

Two ways of computing the same stack, on a line of 1000 minor units with two
non-compound components of 3.33% each:

| method | arithmetic | line tax |
|---|---|---|
| per-component floor | ⌊33.3⌋ + ⌊33.3⌋ = 33 + 33 | **66** |
| one floor over 6.66% | ⌊66.6⌋ | 66 |
| one floor over the summed rate, 1234 units | ⌊41.09⌋ + ⌊41.09⌋ = 82 vs ⌊82.18⌋ = 82 | equal here |

They agree often and not always, and the disagreement is not the point. The
point is that an invoice prints EVERY component with its own base and its own
rate, and those printed figures have to add up to the line's tax. A single floor
at the end produces a line total that no set of per-component figures sums to,
so the document would have to invent which component absorbs the difference.

The per-component floor also keeps the module's existing doctrine: the residue
always falls in the customer's favor, one component at a time
(`internal/modules/tax/service/money.go`).

## The residue is bounded, and the naive bound is wrong

A tempting bound is "item count × component count − 1 minor units". It is false
for a compound stack: a compound component's INPUT is itself a floored number,
so each level carries the level below it into its own rounding.

The honest over-bound per line, with `n` components, is `n(n+1)/2` minor units,
always in the customer's favor — hence **at most 10** with the depth cap of 4.
The derivation: component `i` can lose up to one unit of its own, and a compound
component's base is short by at most the sum of the losses below it, which
multiplies the loss by at most `i`.

## The worked example the tests pin

Line base 12345, base component 5% (500 bps), second component 8% (800 bps) and
compound:

```
c1: base = 12345, rate = 500
    whole     = (12345 / 10000) * 500          = 500
    remainder = ((12345 % 10000) * 500) / 10000 = 117
    tax       = 617                              (exact 617.25)

c2: base = 12345 + 617 = 12962, rate = 800
    whole     = (12962 / 10000) * 800          = 800
    remainder = ((12962 % 10000) * 800) / 10000 = 236
    tax       = 1036                             (exact 1036.96)

line tax = 617 + 1036 = 1653
```

The same pair NOT compounding gives 617 + 987 = 1604. The 49-unit difference is
the compounding itself, and it is visible in the second component's base.

## The ceiling guard, and why it rounds the other way

Each rate is bounded to [0, 10000] on its own and nothing bounds their sum: two
legal 6000 bps rates take 1.2× the line. The write-time guard therefore prices
the whole chain on a notional base of 100,000,000 with CEILING division, so it
is strictly more pessimistic than the floored arithmetic that will run. A stack
it accepts can never reach the base in practice; a borderline one it refuses is
the cheaper mistake, because the alternative is the calculation returning a tax
greater than the line and `validateLine` reporting the merchant's own
configuration as a fault of the provider.
