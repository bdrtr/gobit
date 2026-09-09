# ADR 0104 — An image says what it shows

**Summary:** `product_image` gains `alt_text`, published on the storefront and in
the GraphQL type. It costs one column and settles that an empty value is an
ANSWER rather than a gap.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

A product image carried a URL, a rank and a free-form `metadata` bag. The text a
screen reader reads out had nowhere to go but that bag, and the bag is the wrong
place for it: nothing validates a key nobody declared, so two installations
spell it two ways; no reader can rely on it, so neither the storefront nor the
GraphQL type could publish it; and an accessibility attribute that is present
only when somebody remembered a convention is one nobody can audit.

## Decision

`product_image.alt_text` is a `text NOT NULL DEFAULT ''` column, written when an
image is created and published everywhere the image is.

## Consequences

The empty string is a real answer, not a missing one. HTML gives `alt=""` the
meaning "this image carries no information", which is exactly what a decorative
picture is — so the column is not nullable, and no reader is made to tell NULL
from empty for a distinction nobody acts on. Every image written before the
column existed reads back as empty, which is what is true of them.

It is TRIMMED like every other text this module stores, and that matters more
here than elsewhere: an alt text of one space is not a description, it is a
description somebody thought they gave.

The field is written when the image is CREATED and cannot be changed
afterwards, because no path in this module can change an image at all — the
product update carries no images. That is a wider gap than this record closes
and it stays open.

## Rejected

- **A key in `metadata`** — undeclared, unvalidated, unpublishable, unauditable.
- **A nullable column** — a third state for a distinction no reader acts on,
  and the empty string already carries HTML's meaning for the case.
- **Requiring a non-empty value** — it would refuse the decorative image the
  standard has a word for, and merchants would type "image" to get past it.
