# ADR 0061 — A locale needs a SOURCE before it needs a home

**Summary:** Gap A11 closes as a DECISION rather than a build: gobit builds
neither half of the language axis, and two gates refuse a locale arriving
without a record.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

Gap A11 asks where translated content lives.
[ADR 0050](0050-gobit-stores-one-language-and-the-second-is-the-embedders.md)
fixed gobit's POSITION — one language stored, the second the embedder's — and
said it does NOT close the gap: the storefront has no notion of a shopper's
language at all. **Re-measured, that stands.** Nothing matches accept-language,
no route carries a locale segment, no handler reads a locale parameter, and
`core/http.Principal` is still four fields. The word appears 99 times across the
699 production Go files, and not one of the 35 outside `plugins/webpush` is a
NAME — all are prose about the cluster's collation
([ADR 0015](0015-postgresql-cluster-contract.md)). The one component that made
it a key did so for a DEVICE picking a template
([ADR 0018](0018-web-push-is-a-device-registry-not-a-channel.md)).

**One of ADR 0050's two timing reasons is spent.**
[ADR 0044](0044-the-sales-channel-moves-into-the-catalog-path.md) was unbuilt
then and would move the reads a locale would ride; the paths under
`/store/v1/sales-channels/` are bound today. The SHAPE question is answered by
precedent: what varies the body goes in the PATH, because a header nothing emits
Vary for cannot be cached, and a locale is such a thing.

**What remains is a deadlock held by this repository's own rules.** Storage
cannot go first — a locale key no request supplies is a key nothing can use. Nor
can the request half: a segment whose two values return byte-identical bodies is
a published URL shape with no first consumer, and that map is empty by policy.

Measurement: [measurements/0061](../measurements/0061-a-locale-has-no-source.md).

## Decision

**A11 stops being a gap and becomes a decision: gobit builds NEITHER half, and
the tree refuses a locale arriving without a record.**
`TestOnlyOneComponentNamesALocale` refuses a Go identifier or struct tag naming
a locale outside `plugins/webpush`;
`TestNoTableOutsideTheDeviceRegistryDeclaresALocale` refuses one in SQL. Both
read NAMES, not the word, so prose about a C-locale cluster stays free.

**The trigger is two observable facts, either of which fires it.**

- **A second component names a locale.** Exactly one does today, and the gates
  turn the second into a red test naming the file: a second locale-to-content
  mapping means the axis has begun being decided per component.
- **One shop must serve one catalog in two languages over one stock ledger or
  one document series.** ADR 0050's workaround — one installation per language —
  is not neutral: `inventory_levels` becomes two ledgers over one warehouse,
  order display ids two IDENTITY sequences, `invoice_series` two legal
  numberings. Point at it: two databases, one SKU set, one reconciliation ask.

**When it fires the candidate is still ADR 0050's third** — one translation
module keyed by entity, id, field and locale — with the request half FIRST.

## Consequences

- **gobit stays single-language by decision, not omission**; A11 leaves the list.
- **The deferral is mechanical for the STORAGE half only.** The gates read a Go
  name, a struct tag and a SQL column; a locale arriving as a path segment, a
  query key or a filter key is UNWATCHED and rests on a reader.
- **The gates cost a false alarm.** A `/* */` SQL block naming a locale fails a
  test that is not about it — loud where silence would be worse.

## Rejected

- **Build the request half alone.** Only gobit can build it and its shape is
  settled — but two locales would return byte-identical bodies, so the segment
  is a published URL shape nothing varies on. The first-consumer rule, not taste.
- **Build the translation module now.** Its key has no source — ADR 0050's
  argument, still true — and no request could fill the column.
- **Leave A11 open on ADR 0050's four triggers.** It buys not being wrong and
  loses the finding: the row waits on a FACT, not on work — and leaves nothing
  watching, which is how a column arrives "for later".
