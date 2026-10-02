# ADR 0353 — The panel makes a sales channel

**Summary:** The Sales channels screen makes a channel under `admin`
through `auth.admin`, with its name, description and whether it starts
disabled, as the API's create makes one; a name another live channel holds
is refused on the screen with what was typed.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel corrects the sales channels (ADR 0352) and attaches a new
publishable key to them (ADR 0351), but a new storefront's channel was
made only through the admin API, so opening a storefront from the panel
stopped at its first step.

## Decision

The auth module's panel surface makes a channel through the service the
API's create calls, and the Sales channels screen offers the form to an
operator holding admin.

## Consequences

- A storefront is opened from the panel: its channel made here, its key
  made on the API keys screen.
- A channel may start disabled, so it is made before the storefront it
  serves is ready and no key sees it until it is enabled.
- A name another live channel holds is refused as through the API, the
  form kept as typed; a correction refused in a row leaves the form empty.
- Which products a channel sells is still set where the products are.

## Rejected

- Making a channel and its key in one form: the key's token is shown once
  on its own answer, and a channel serves more than one key.
