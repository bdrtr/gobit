# ADR 0086 — A price can include its tax, and the reverse computation is not the ordinary one run backwards

**Summary:** A market can quote prices with the tax inside them; the tax is
extracted by its own arithmetic, because reusing the ordinary one is wrong.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

gobit could only compute tax one way: a price was NET and the tax was added on
top. In Turkey — and across most of Europe — retail prices are quoted the other
way: the shopper sees 199,00 and pays 199,00. A merchant who wanted the sticker
to be the amount charged had no way to say so, and nothing in the tree recorded
that as a decision.

**The measurement that decided the shape.** The obvious implementation is to
extract the net and let the existing calculation tax it. It is wrong, always by
one minor unit and always against the shopper, and the share of amounts it is
wrong on has a closed form: `rate / (10000 + rate)`. At Turkey's 20% VAT that is
**one gross amount in six**; at 25% it is one in five. The higher the rate the
worse the shortcut behaves, which is the opposite of what an implementer would
guess. The two computations are not inverses of each other.

Measurement: [measurements/0086](../measurements/0086-tax-inclusive-rounding.md)

## Decision

**`TaxIncludedIn` is its own arithmetic**: `tax = gross × rate / (10000 + rate)`
and the base is what is left. The base is derived by SUBTRACTION, so base plus
tax is the gross by construction rather than by luck. It divides before it
multiplies for the reason `TaxOf` does — the product reaches 10^22 and an int64
ends at 9.22 × 10^18 — and the split was checked against arbitrary precision.

**The flag lives on the tax REGION**, nullable, NULL meaning INHERIT along the
same chain the provider is inherited on. A chain that says nothing is
tax-exclusive, which is what every installation did before the column existed;
not inventing a second inheritance rule was part of the decision.

**The provider is told, and reports the base it used.** Extraction needs the
rate and the rate is the provider's to choose, so the flag travels in
`ProviderInput`. The base is only READ in inclusive mode, which is what lets a
provider that never heard of the flag keep working unchanged.

**The cart's check becomes an EQUALITY, and it is not a relaxation.** Adding on
top, the returned base must be exactly what was sent. Taking out, base plus tax
must equal what was sent. A check that merely allowed a smaller base would pass
a line that is one unit off — the very thing the feature exists to prevent.

**The line's subtotal becomes the extracted base plus its discount**, which
lands `Subtotal − Discount + Tax` exactly on the discounted gross.

## Consequences

- **The sticker is what is charged**, asserted at the cart total: 19,900 in, and
  19,900 out. Leaving the subtotal gross makes the same cart charge 23,216, and
  that is what the test prints when the rewrite is removed.
- **Six mutations, all fired**: the divisor, the remainder term, the choice of
  computation, the direction of the inheritance walk, the subtotal rewrite, and
  equality-versus-range in the cart.
- **Nothing changes for an existing installation.** The column is NULL
  everywhere, and NULL is the old behaviour.
- **The discount stays a GROSS figure beside a NET subtotal.** That hybrid is
  the accepted cost of keeping the identity exact: re-deriving a net discount
  needs a second extraction, and two roundings that must cancel are two
  roundings that eventually will not.
- **A test that pinned an unread field had to change**: the local provider now
  reports the base even with no rate matched, because the equality holds there too.

## Rejected

- **Extracting in the cart and re-taxing with `TaxOf`.** Wrong on one amount in
  six at 20%. This is the whole reason the record exists.
- **The flag on the price.** Whether the tag includes tax is decided by the
  market, not by which price row matched; on the price it would have to be
  restated on every row, and one row set the other way would quietly charge a
  different total than its neighbours.
- **The flag on the rate.** A region can carry several rates and they would all
  have to agree; the flag would then be a region property stored N times.
- **Deriving a net discount as well.** It reads better and it does not add up.
