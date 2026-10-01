# ADR 0297 — A telephone order finds the caller by e-mail

**Summary:** The telephone order's page finds the customer records that hold
the e-mail a caller gives, and its open form offers them by name, the account
chosen. The search is shown only to an operator who may read customers.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0290's form opens a cart for a guest's e-mail or a customer's id. A caller
gives their e-mail, not an id, so a cart for a registered customer meant
finding the id on the customer screen, which lists customers by page and
searches nothing. The customer provider already filtered by an exact e-mail,
normalized as the customer module stores it. One e-mail holds at most one
account and any number of guest records.

## Decision

The page reads the customer records holding the e-mail typed into its search,
and the open form offers them as a list labeled with the name, the kind and
the id, the account first and chosen, beside the guest's choice. The search is
offered only to an operator who holds `customer:read` as well as the cart's
write.

## Consequences

- An operator opens the cart for the caller's account by the address they
  spell, in any case; the order the cart becomes is under that account.
- The e-mail typed into the search is written into the form, and the operator
  may choose a guest record or no customer instead of the account.
- One search reads at most ten records. A search that finds none, that is not
  an e-mail address, or whose read fails says so and leaves the id box.
- An operator without the customers' privilege keeps the id box, and nothing
  of the customers is read for them.
- A refused form draws the search again and keeps the choice made.

## Rejected

- A partial search on names and addresses: the provider matches an address
  exactly, and a caller spells their e-mail whole.
- Choosing the account without asking: a caller may want the order kept off
  the account, and the form says what it will do.
- Searching under `cart:write` alone: the page would read the customer
  module's data under another module's privilege (ADR 0260).
