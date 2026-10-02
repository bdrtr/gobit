# ADR 0333 — The panel lists and revises the shipping options

**Summary:** A Shipping options screen lists the fulfillment module's options
through its option entity under `fulfillment:read`, and each row renames its
option, sets a flat option's fee and says whether the storefront offers it
under `fulfillment:write`, from the terms the row was drawn with.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

A shop's shipping options are how its parcels go out and what a shopper
pays for them, and the panel had no screen for them: a fee that changed at
the carrier, or an option to withdraw from the storefront for a week, took
the admin API's update, which reads the option and writes every field back
with no guard against a write made in between.

## Decision

The panel lists the shipping options newest first through the fulfillment
module's option entity, and the module revises an option's name, fee and
storefront visibility in one UPDATE that matches the terms the caller read
and refuses with `fulfillment_shipping_option_revised` otherwise. Each row
of the screen offers the form that does it, carrying the terms it was drawn
with and its fee in minor units.

## Consequences

- A merchant follows a carrier's new rate, or keeps an option for the panel
  and the telephone order alone, where the options are listed.
- Two operators revising one option at once write it once; the second is
  told what the option is now and to draw the list again, and the row comes
  back with what they typed.
- A calculated option's fee stays its provider's: its row offers no fee,
  and a fee sent for one is refused as on a new option.
- The provider, the profile, the price type and the region stay the
  option's identity; a parcel opened on it before keeps meaning what it did.
- Writing an option, its rules and its profiles stays on the admin API.

## Rejected

- Writing through the API's update: it writes back the fields it read, so a
  revision made in between is lost.
- Revising the price type or the region in the same form: a calculated
  option asks its provider, and moving an option's region changes which
  carts are offered it, each a decision of its own.
