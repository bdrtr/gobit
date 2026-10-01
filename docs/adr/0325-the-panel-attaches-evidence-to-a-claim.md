# ADR 0325 — The panel attaches evidence to a claim

**Summary:** A claim on the order's page lists its evidence, and a file sent
from the page is stored through a new `file.admin` surface under `file:write`
and bound to the claim through `order.admin` under `order:write`; a file that
cannot be bound is removed again.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

A claim for goods that arrived damaged is decided on a photograph, and the
order module binds uploads to a claim (ADR 0106). The panel acted on claims
(ADR 0271) but had never taken a file: the evidence was uploaded and bound
through two admin API calls, outside the page where the claim is settled.

## Decision

The order's page lists each claim's evidence with its caption, linked to the
file for an operator holding `file:read`. Its form streams the file through
the file module's panel surface and binds the upload through the order
module's, asking `order:write` and `file:write` both, and removes the upload
when the binding is refused.

## Consequences

- An operator records the damage where the claim is settled.
- The panel makes the API's two calls, each behind its own module's
  privilege; the owner walk lets a write reach the module whose privilege its
  handler asks for, and nothing else.
- The file module keeps its allow list and bound; the panel reads the body as
  a stream, cuts it at that bound and the form's envelope, and detects the
  type from the first bytes as the module's own endpoint does.
- A caption is read before the file, in the form's order.
- Removing evidence unbinds it and keeps the file, as the API does.
- The panel's content policy still loads no image; evidence is a link.

## Rejected

- Uploading through the order module, which would reach the file module: the
  file module's cross-module surface is a read by design.
- An uploads screen whose ids are then typed into the claim: two screens for
  one act, and an id an operator copies by hand.
