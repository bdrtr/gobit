# ADR 0039 — An option value is matched by a folded form, and the fold keeps what it cannot transliterate

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

A shopper filtering by "Color: red" has to be told which products have it, and
gap A18 asks what counts as a match: the value EXACTLY as the catalog stores it,
the same value case-insensitively, or the value folded the way a handle already
is. The filter itself is not built — this decision sits at its head, and it also
decides the index, so the index was never a separate question.

The three candidates and their costs were measured on 2026-09-06:

- **Exact** (`value = $1`). No machinery, and the ordinary client never notices,
  because the vocabulary endpoint hands the value back verbatim and a client
  filtering by what it was just given always matches. The cost lands on a
  hand-typed URL, where one spelling finds everything and another finds nothing,
  with no way for the caller to tell that from an empty catalog.
- **Case-insensitive in SQL.** The shape the catalog's `q` search already uses.
  It needs an expression index, and it inherits ADR 0015's live hazard: `lower()`
  is the CLUSTER's fold, and on a database created with `--locale=C` it folds
  ASCII and nothing else. ADR 0038 has just finished removing the last such
  predicate from this repository for exactly that reason.
- **Fold to ASCII**, the normalization `slugify` already performs for handles.
  Locale-INDEPENDENT, which is the property the second lacks, and a rule this
  repository already trusts.

## Decision

**An option value is stored twice: `value` as the merchant typed it, and
`value_folded` as it is matched.** The fold is Go's, never the cluster's, and
`product_option_value_folded_uniq` is UNIQUE per option on the folded form.

**The fold is [models.FoldOptionValue] and it is NOT `slugify`.** It trims,
lower-cases with Go's Unicode rules, and replaces each Latin letter carrying a
mark with its ASCII base. **Every rune it has no ASCII base for is KEPT.**

## Why the fold is not slugify, which is the part that was measured

A18 proposed the third candidate as "the text folded to ASCII the way a handle
already is", and building it that way was the plan. Measured before it was built:

| value | `slugify` | `FoldOptionValue` |
|---|---|---|
| the dotted and dotless spellings of one Turkish color | one form | one form |
| two different Cyrillic colors | `""` and `""` | kept, and kept apart |
| a CJK color | `""` | kept |
| a Danish o-slash color | `r-d` | `rod` with the slashed letter kept |

`slugify` DROPS a rune it cannot transliterate, and it is right to: a URL handle
must be ASCII, and a handle that loses a word is merely ugly. A MATCHING form
that loses a word is a different object. Every Cyrillic value in a catalog folds
to the empty string, as does every CJK one — so they are all equal to one
another, and because the folded form carries a unique index, the second such
value is not mismatched but REFUSED.

That was reproduced on the real schema rather than argued: inserting two Cyrillic
colors into one option, with `slugify`'s result as the folded form, fails on
`product_option_value_folded_uniq` with `Key (option_id, value_folded)=(o2, )
already exists`. **A shop whose catalog is written in Cyrillic could not run this
migration at all.**

The rule is flawless on Turkish, which is exactly why it read as safe. The
requirement it fails is not "fold as far as possible" but: *two spellings of one
value must meet, and two different values must not.*

Keeping the rune costs the case the fold was for — a shopper typing an
unaccented approximation of a Cyrillic word still misses — but that is a MISS,
and the alternative is a MERGE. A filter that finds nothing is a filter somebody
retypes. A filter that reports one value's products under another value's name
is a wrong answer nobody can see. Case folding still applies to those scripts, so
they gain the case-insensitivity the second candidate offered, without its
cluster dependency.

## Rejected alternatives

**Exact match.** Rejected because the failure is silent and lands on precisely the
caller who cannot diagnose it. It is also the only candidate that would make the
vocabulary endpoint's verbatim value load-bearing forever.

**An expression index on `lower(value)`.** Rejected on ADR 0038's finding, which
is one day old: `lower()` folds what the cluster's ctype knows, so the filter
would match different things on different installations and the difference would
never surface as an error. The fold belongs to Go.

**`golang.org/x/text` normalization (NFD, drop the marks).** It would fold most
of the Latin table automatically. Rejected for two reasons: it makes an indirect
dependency direct, which ADR 0010 treats as a decision rather than a convenience;
and it misses the letter this repository cares about most — the Turkish dotless i
has no decomposition, so NFD leaves it alone and the two spellings of the
commonest Turkish color would never meet. A written table gets that letter right
and can be read end to end by somebody deciding whether their catalog is safe.

**Folding the stored `value` itself.** Rejected: the vocabulary endpoint hands
back what the merchant typed, and a merchant who typed a capital letter should see
it. The folded form is a handle beside the value, not a replacement for it — the
same shape ADR 0038 gave the invoice buyer address.

## Consequences

**Positive**

- **A filter answers the same way on every installation.** The fold consults no
  locale, so the storefront cannot behave differently on two clusters.
- **Two spellings of one value cannot coexist in an option.** The unique index
  refuses the second, which is a merchant seeing an error at the moment they
  create a duplicate rather than a shopper seeing split stock.
- **Non-Latin catalogs work.** They gain case-insensitive matching and lose
  nothing.

**Negative, and accepted**

- **The migration can REFUSE to apply.** A merchant who already has two spellings
  of one color in one option has two rows that fold to one value, and 000003
  stops until one is removed. That is the intended reading of those rows — the
  same color typed twice — but it is a migration that can fail on live data, and
  the operator meets it without warning.
- **The backfill is correct for ASCII and only for ASCII**, for the reason it was
  in ADR 0038: a migration is SQL and `lower()` is the fold that cannot be
  trusted. The convergence belongs at startup, the way the invoice module's does,
  and that step is NOT yet built — see "What this deliberately does not do".
- **The Latin table is hand-written and therefore incomplete.** It covers the
  Turkish letters and the common Western European ones. A letter outside it is
  kept rather than folded, which is a miss and not a merge, but a Polish or Czech
  catalog gets less folding than a French one.

## What this deliberately does NOT do

- **It does not build the filter.** That is B2's, and it now has its rule and its
  index.
- ~~**It does not add the startup convergence pass.**~~ **Built 2026-09-07, in the
  same round.** `refoldOptionValues` runs beside `refoldInvoiceHandles` after
  `registry.Bootstrap`, reads only the non-ASCII values — the only shape the SQL
  backfill can have got wrong — and writes only where the stored form differs from
  the Go fold.

  It also had to answer a case the decision above did not anticipate. **The
  convergence can be REFUSED by the very index this ADR adds**, and the case is
  ordinary: on a `--locale=C` cluster the SQL backfill leaves the dotted and
  dotless spellings of one Turkish word at DIFFERENT folded forms, so 000003's
  unique index accepts both rows; the Go fold brings them together and the update
  violates it. So the migration succeeds and the convergence collides, which is
  the opposite order from the one the Consequences above describe.

  The pass therefore does not stop and does not choose. It converges what it can,
  and logs each collision at ERROR with the option and both forms, because those
  two rows are one value typed twice and only the merchant knows which spelling to
  keep. Nothing is deleted and nothing is renamed.
- **It does not touch the `q` search.** ADR 0015's hazard stands for `ILIKE` and
  `to_tsvector`; this decision is about matching a value a client chose from a
  vocabulary, not text a shopper typed into a search box.

## Related

- [ADR 0038](0038-the-storage-form-of-an-e-mail-is-one-rule.md) — the fold
  belongs to Go, and the stored-beside-the-original shape this reuses.
- [ADR 0015](0015-postgresql-cluster-contract.md) — the cluster's case folding,
  which is what makes the SQL candidates unsafe.
- [ADR 0010](0010-depo-secim-politikasi.md) — the dependency policy that makes
  x/text a decision.
