# ADR 0305 — A telephone order chooses its channel by name

**Summary:** The telephone order's cart page offers the enabled sales channels
by name to the forms that claim one — the line and the completion — for an
operator who holds `auth:read`. An operator without it types the channel's id
as before.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0146 made every operator's line write and completion name a sales channel,
and the panel's telephone order (ADR 0290, ADR 0291) asked for it in a box
that takes the channel's id. Operators know their shopfronts by name. The auth
module owns the channels and offers them to the read layer as
`sales_channel`; the admin API lists them under `auth:read`. Each module's
data on a panel page is read under that module's privilege (ADR 0260).

## Decision

The cart page reads the enabled sales channels, at most fifty, when the
operator holds `auth:read` as well as the cart's write, and the line and the
completion forms offer them as a list sorted by name. Without the privilege,
or when the read fails, the forms keep the id box.

## Consequences

- An operator claims a sale for the shopfront by its name, and a mistyped id
  stops reaching the write.
- The channel chosen in a refused form is chosen again when the page is drawn
  with the refusal.
- `auth:read` also reads the shop's users and keys on the admin API; a shop
  that does not grant it to its telephone operators keeps the id box.
- A disabled channel is not offered, and a shop with more than fifty channels
  sees the first fifty by the read layer's order, sorted by name.

## Rejected

- Listing the channels through the cart module's surface: the channels are the
  auth module's data, and the cart module would read another module's records.
- A privilege of the channels' own: the admin API reads them under
  `auth:read`, and the two doors keep one policy (ADR 0260).
