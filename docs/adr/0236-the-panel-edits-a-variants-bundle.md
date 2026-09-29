# ADR 0236 — The panel edits a variant's bundle

**Summary:** The variant page lists what a bundle is made of, and a form
replaces its parts, one per line by SKU or id with the units one bundle holds,
on the product's version.

- **Status:** Accepted
- **Date:** 2026-09-29

Measurement: [measurements/0236](../measurements/0236-an-operator-fills-a-box.md)

## Context

ADR 0234 wrote a variant's composition over the admin API only, and ADR 0235
made a bundle sellable; in the panel an operator could neither see what a box
holds nor change it. The add-ons form (ADR 0232) settled how an operator names a
variant: by the SKU they know. A composition differs twice: each part carries
units, and the write is a revision of the product (ADR 0221), so a form opened
before another operator's save must not write over it (ADR 0222).

## Decision

The variant page lists a bundle's parts with their units, each linked to its
own page, and a form at `/admin/ui/products/{id}/variants/{variantID}/bundle`
replaces them, one part per line as a SKU or an id followed by its units, one
when the line gives none. The form carries the product's version, and the
product module's admin surface resolves the references, applies ADR 0234's
rules and refuses a stale version.

## Consequences

- The page reads the parts in one call and their products in another, with the
  reader the add-ons use; a variant that is no bundle costs neither. A bundle's
  page says it counts no stock of its own in place of the stock table.
- A line that does not read — units that are not a whole number, a third word —
  is refused by the panel naming the line, before the module is asked; every
  other refusal is the module's sentence, and the form comes back with what was
  typed.
- A save on a moved version comes back at the version now stored, as the
  product's own form does.
- The form demands the product write privilege. The limits it prints, the field
  the page reads and the keys of a part are bound to the module's at compile
  time.
- The admin surface takes the units beside the references, two lists refused
  unless they pair, because the panel cannot name the module's types; a unit
  past ADR 0234's bound is refused before it is narrowed to the column.

## Rejected

- **A picker over the catalog's variants.** Fifty thousand do not fit a select,
  and an operator knows the SKU.
- **Units written by repeating a part.** A box of twelve would be twelve lines,
  and the module refuses a part named twice.
- **The form without the product's version.** The composition is a revision,
  and a stale form would undo another operator's parts without a word.
