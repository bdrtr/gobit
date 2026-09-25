# ADR 0178 — The panel schedules a draft

**Summary:** The admin panel's product form takes a publication moment in UTC
and shows it on the product page, so a launch can be scheduled without the API.
The form checks the moment before it writes anything, and the module still
decides.

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

ADR 0177 gave a draft a publication moment over the admin API and recorded that
the panel's product form did not show it. The panel is the merchant's surface,
and a launch nobody can schedule from it is a feature only an integrator can
use. The panel reads a product through the read layer and writes it through the
product module's admin surface, and it knows neither the module's types nor its
rules (ADR 0011, ADR 0013).

## Decision

The product record in the read layer carries `publish_at`, the admin surface
gains `ScheduleProduct` and `UnscheduleProduct`, and the edit form gains a
datetime-local field that the panel reads as UTC, as its label says. A saved
draft with a moment is scheduled and one without is unscheduled; the panel
refuses a moment it can see will not be kept — malformed, past, or on a product
not saved as a draft — before it writes anything.

## Consequences

An edit is never half-saved over its moment. The title and the status are not
written when the moment will be refused afterwards. The module remains the
authority: it checks the draft and the future again on its own.

A product saved as published or archived is not asked about a schedule. The
module's status write already takes it off in the same statement.

The moment is in UTC on the page, in the form and on the label. An HTML
datetime-local input carries no zone, and guessing the operator's would publish
at an hour nobody chose.

The panel's write surface grows from one method to three. Each is a decision
recorded here, which is what its godoc asks of any method added to it.

The read layer now offers the moment to every consumer of the product entity.
Nothing on the storefront reads a product from the read layer, and the
storefront's own bodies still leave it out (ADR 0177).

## Rejected

- **Reading the moment in the browser's zone.** The zone would have to travel
  with the form, and a panel used from two zones would schedule two different
  hours from one typed value.
- **Scheduling from the product page with a separate form.** A second write
  form for one field, and an edit that changes the status could not also set
  the moment it depends on.
