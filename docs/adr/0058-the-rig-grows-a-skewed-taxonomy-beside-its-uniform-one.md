# ADR 0058 — The rig grows a skewed taxonomy beside its uniform one

**Summary:** The catalog rig builds two small categories that differ only in where
their members sit in the listing, so the case that decided the product filter is
reproducible by command instead of by hand on a scratch database.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

The rig's taxonomy is uniform by construction: product n lands in category
(n-1) mod C, so twenty categories over 52,004 products hold exactly 2,600 each,
the largest and the smallest alike. The case that decided how the product filter
is written lives at the other end — a category holding a twentieth of a percent
of the catalog — and `rig.Spec` took a category COUNT with no way to ask for one.

The selective categories were therefore built by hand on a scratch database that
is gone, and the gap ledger has carried the consequence ever since: the rig
cannot reproduce the case that motivated the change it paid for.

The same measurement said something sharper: two categories of NEARLY the same size
produced two different plans, separated by where their members sat in that
listing order.

Measurement: [catalog-search-cost](../measurements/catalog-search-cost.md).

## Decision

**`Spec.SkewedCategorySize` builds TWO extra categories of that size, and zero
builds neither.** One holds the head of the listing; the other takes every
stride-th row of it, at the stride that walks the whole catalog in exactly as
many steps. Both sets are picked by the storefront's own ordering rather than by
the shape of an id, because consecutive numbers are not consecutive in it.

**The uniform taxonomy keeps every member it had.** The skewed rows are extra
memberships on products that already carry one, so the figures taken at 5%
selectivity stay measurable on the same rig.

## Consequences

- **The small-category case is reproducible by command**, on a rebuilt database
  instead of a scratch one that no longer exists.
- **A product belongs to two categories for the first time** — the shape the
  filter's `EXISTS` was written for and the generator could not produce.
- **The skewed memberships are POSITIONAL.** Every other row the rig writes is
  derived from its own number, so re-seeding larger leaves the smaller rows
  correct; these are picked by place in the listing, so a rerun at a different
  family size adds a second set beside the first. Changing the size wants a
  reset first.
- **`DefaultSpec` is unchanged**, so no recorded figure becomes a claim about a
  catalog nobody measured.
- **The SQL rule gains its first standing exemption, and it is per TABLE.** The
  rig names eleven tables it does not own; each is listed, an unlisted twelfth
  is a finding, and a listed table nobody names is a finding too. Retiring the
  exemption means the rig filling those tables through the owning modules'
  services instead of by statement.
- **The integration suites ask for a skew although the default is none**, or
  three statements would be run by no test and free to rot.

## Rejected

- **A power-law spread over the existing categories.** It reshapes the rig
  instead of adding to it: every recorded 5% figure would describe a catalog
  that no longer exists, and none of them can be re-measured without the
  database they were taken on.
- **One small category.** It reproduces a number and hides the finding. The cost
  of a small category was measured as not being a figure at all but which of two
  legal plans the statistics led the planner to, and a rig carrying one sample
  invites the next reader to take that sample for a law.
- **Turning the skew on by default.** The rig's promise is that a rebuild is the
  same rig; a default that quietly added two categories and their memberships
  would break it for a convenience.
- **Pinning the plan difference in the rebuild's acceptance check.** The shipped
  statement no longer carries the disjunction that collapsed, the two plans were
  measured as a coin the planner flips so an assertion on either would go red on
  a legal plan, and the check is four numbers on a query the taxonomy does not
  touch.
- **The same skew on the tag path.** The measurement records it mirroring the
  category path within noise; a second pair buys an index name.
