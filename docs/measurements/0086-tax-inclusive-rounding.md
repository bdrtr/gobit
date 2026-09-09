# Extracting a tax and re-taxing the remainder — measured 2026-09-09

The evidence behind [ADR 0086](../adr/0086-a-price-can-include-its-tax.md): why
a tax-inclusive price needs its own arithmetic instead of reusing `TaxOf`.

## The question

To charge a shopper the price on the tag, the net base has to be derived from a
gross amount. The cheapest implementation reuses what already exists: extract
the base, then let the ordinary calculation tax it. If the two agree, no new
arithmetic is needed and no schema has to learn a flag.

They do not agree.

## The reproduction

Both functions as gobit implements them, floor-rounded, in minor units:

```python
def tax_of(base, r):    # the ordinary calculation: tax ON TOP of a net base
    return (base // 10000) * r + ((base % 10000) * r) // 10000

def tax_inc(gross, r):  # the extraction: tax taken OUT of a gross amount
    return (gross * r) // (10000 + r)

# The question asked of every gross amount:
#   extract the tax, then tax the remainder the ordinary way.
#   Does the second answer equal the first?
tax_of(gross - tax_inc(gross, r), r) == tax_inc(gross, r)
```

Run over every gross amount from 0 to 200,000 minor units, per rate.

## The result

| rate (bps) | rate | amounts that DISAGREE | share |
|---|---|---|---|
| 100 | 1% | 1,980 | 0.99% |
| 800 | 8% | 14,814 | 7.41% |
| 1000 | 10% | 18,181 | 9.09% |
| 1800 | 18% | 30,508 | 15.25% |
| 2000 | 20% | 33,333 | **16.67%** |
| 2500 | 25% | 40,000 | 20.00% |

Every disagreement is by exactly one minor unit, and always in the same
direction: re-taxing the extracted base produces MORE than the tax that was
taken out. The shopper would pay above the sticker.

## The share is not a coincidence: it is rate/(10000 + rate)

The aggregate over the six rates above is 138,816 of 1,200,006 — 11.57% — but
that figure is an artefact of WHICH rates were sampled, and quoting it alone
would be misleading. The per-rate share has a closed form and it is the same
expression as the extraction itself:

    share of amounts that disagree = rate / (10000 + rate)

Checked against the measurement at eight rates, agreeing to six decimal places:

| rate (bps) | measured | rate/(10000+rate) | difference |
|---|---|---|---|
| 100 | 0.009900 | 0.009901 | 1.0e-06 |
| 500 | 0.047615 | 0.047619 | 4.3e-06 |
| 800 | 0.074070 | 0.074074 | 4.4e-06 |
| 1000 | 0.090905 | 0.090909 | 4.6e-06 |
| 1800 | 0.152539 | 0.152542 | 3.1e-06 |
| 2000 | 0.166664 | 0.166667 | 2.5e-06 |
| 2500 | 0.199999 | 0.200000 | 1.0e-06 |
| 10000 | 0.499998 | 0.500000 | 2.5e-06 |

The residual difference is the finite sample: the pattern repeats with a period
of `10000 + rate`, and 200,001 amounts do not divide evenly into it.

**What this means for the decision.** At Turkey's 20% VAT the shortcut charges a
kurus too much on **one gross amount in six**. At 25% it is one in five. The
higher the rate, the worse the shortcut behaves — which is the opposite of what
an implementer would guess, and the reason the reuse was refused.

## The split arithmetic

`TaxIncludedIn` divides before it multiplies, because `gross × rate` reaches
10^22 and an int64 ends at 9.22 × 10^18. Writing `gross = q × d + m` with
`d = 10000 + rate`:

    gross × rate / d  =  q × rate + (m × rate) / d

with `q × rate` at most 5 × 10^17 and `m × rate` under 2 × 10^8. Both fit.

Checked against the direct computation on 215,015 pairs — every amount from 0 to
3,000 at five rates, the int64 ceiling, and 200,000 random pairs — with zero
disagreements. The property is also asserted continuously against `math/big` by
`FuzzTaxIncludedIn`, which ran 6.2 million executions without a failure.
