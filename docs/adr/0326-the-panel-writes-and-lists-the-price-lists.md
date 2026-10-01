# ADR 0326 — The panel writes and lists the price lists

**Summary:** A Price lists screen lists the pricing module's lists with their
type, status and window through `pricing.admin` under `pricing:read`, and
writes one under `pricing:write`.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

A segment's contract prices and a seasonal sale sit on price lists: a sale
list's price is chosen over a variant's base price and an override list's
over both, while the list is active and inside its window. The panel showed
which list a variant's price belonged to, but a list could be written and
read only through the admin API, and the module refused bad input in Turkish.

## Decision

The pricing module's panel surface lists the price lists a page at a time in
the module's order and writes one with its title, type, status and window.
The panel's Price lists screen prints them and offers the form to an
operator holding `pricing:write`.

## Consequences

- A merchant opens a wholesale list or a sale where the catalog is managed;
  its prices are written on a variant's page, a decision of its own.
- The form offers the two types and the two starting statuses the module
  accepts; a list is expired by the API until a screen needs it.
- The window is read in UTC, as a campaign's is (ADR 0319), through one
  reader the two screens share.
- The price list service, repository and input checks are written in English
  now and have left the language ledger.
- The list keeps the module's order, oldest first; the list just written is
  named in the address the form lands on.

## Rejected

- Reading the lists through a read provider: none publishes them, and a draft
  list is not the storefront's to see.
- Editing a list on the same screen: the module replaces a list's whole
  definition, and a form would have to carry what it read.
