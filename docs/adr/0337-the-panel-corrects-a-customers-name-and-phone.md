# ADR 0337 — The panel corrects a customer's name and phone

**Summary:** A customer's page corrects their first name, last name and
phone under `customer:write` through `customer.admin`, from the ones the
page was drawn with; the customer module writes them in one conditional
statement only while they are still the customer's. The e-mail is not
corrected here.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel shows a customer's contact details (ADR 0308) and puts them into
groups (ADR 0322), but a misspelt name or a changed phone, which support
corrects daily, took the admin API's partial update, which writes whatever
it is sent over whatever the record has become.

## Decision

The customer module corrects a customer's name and phone together, in one
UPDATE that matches the ones the caller read on a live customer, and
refuses with `customer_contact_revised` otherwise. The customer's page
offers the form to an operator who may write the customers, carrying the
name and phone it was drawn with.

## Consequences

- Support corrects a customer where it reads them.
- Two operators correcting one customer at once write once; the second is
  told what the customer is now and gets back what they typed.
- The read and the written contact cross the surface as JSON, their fields
  named rather than placed.
- The e-mail stays the admin API's to change: an account signs in with it,
  and a change there is a decision about the account, not a correction.

## Rejected

- Writing through the API's partial update: it writes over a correction
  made meanwhile.
- Correcting the e-mail in the same form: it is the account's identity, and
  two accounts may not share one.
