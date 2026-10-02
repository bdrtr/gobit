# ADR 0352 — The panel corrects the sales channels

**Summary:** A Sales channels screen lists the auth module's channels
through its channel entity under `auth:read`, and an operator holding admin
corrects each one's name, description and whether it is in use through
`auth.admin`, from the ones its row was drawn with, refused with
`auth_sales_channel_revised` otherwise.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

A publishable key is attached to sales channels (ADR 0351), and a channel
disabled is one its keys stop seeing, but the channels were read and
changed only through the admin API, whose update writes the fields it is
sent over whatever the channel has become.

## Decision

The auth module corrects a channel's name, description and state together,
in one UPDATE that matches the ones the caller read on a live channel, and
refuses with `auth_sales_channel_revised` otherwise. The panel's Sales
channels section lists the channels, the newest first, and offers each row
the correction to an operator holding admin.

## Consequences

- An operator disables a channel, or renames it, where its keys are made.
- Two operators correcting one channel at once write once; the second gets
  back what they typed in that row alone.
- A name another live channel holds is refused, as through the API.
- The channels are read through the read layer, which the telephone order
  already reads them through (ADR 0305); only the correction crosses the
  surface, its terms as JSON under the provider's field names.
- Making and deleting a channel stay the API's.

## Rejected

- Writing through the API's partial update: it writes over a correction
  made meanwhile.
- Matching on the moment the channel was last written: the provider
  publishes it, but a metadata write the panel does not show would refuse
  a correction of what it does.
