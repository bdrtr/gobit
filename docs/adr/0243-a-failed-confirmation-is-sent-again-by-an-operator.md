# ADR 0243 — A failed confirmation is sent again by an operator

**Summary:** An operator sends a failed order confirmation again through
`POST /admin/v1/notifications/{id}/resend`, which reopens the record only while
it is failed and rebuilds the message from the order.

- **Status:** Accepted
- **Date:** 2026-09-29
- **Amended by:** [0245](0245-a-delivery-a-dead-attempt-left-pending-is-sent-again.md): a record a dead attempt left pending is sent again too

Measurement: [measurements/0243](../measurements/0243-the-mail-nobody-could-send.md)

## Context

A provider's error does not say a message did not go out, so the notification
module sends once and wrote that sending a failed one again is a person's
deliberate decision; it gave the person no way to make it (D166). Since ADR
0240 the bus calls a failing handler again, and the second call found the
record of the first attempt and skipped it, logging that the notification "has
already been sent". An order whose confirmation the mail server refused stayed
without one.

## Decision

`POST /admin/v1/notifications/{id}/resend`, under `notification:write`, sends a
failed order confirmation again: the record is taken back to pending only while
it is failed, and the message is rebuilt from the order, as the event builds
it. Any other status, and any other template, is refused with 409
`notification_not_resendable`.

## Consequences

- The confirmation keeps its record and its key; the resend is an attempt on
  that record, through the provider configured now, and the record says how it
  ended. Two operators pressing at once reopen it once.
- A sent confirmation is never sent twice by this path; a failure whose mail
  did go out may reach the customer twice, which is the operator's call.
- The invitation, the gift card's code and the stock alert are sent through
  the interop by the modules that hold their content, and are resent by them.
- The handler's second call after a failure now logs that the notification
  was attempted before, whatever that attempt's status.
- The module gains its write scope, `notification:write`.

## Rejected

- **Sending a failed delivery again automatically.** A provider that timed out
  after sending would send a second e-mail for every such failure.
- **A "send a notification" endpoint.** It would do the same job over a second
  path and let a caller choose the idempotency key.
- **Keeping each message to resend any template.** The log holds no recipient
  and no content by design, and a gift card's code or an invitation's token
  would be kept in it.
