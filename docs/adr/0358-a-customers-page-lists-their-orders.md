# ADR 0358 — A customer's page lists their orders

**Summary:** A customer's page lists their five newest orders, read through
the order entity's customer filter only for an operator who may read the
orders too, and links to the order list asked for that customer, which
lists every one of their orders.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The customer's page shows who they are, their addresses and their groups
(ADR 0308, ADR 0322), but support's first question about a customer, what
they ordered, was answered by searching the order list, which could not be
narrowed to one customer. The order entity filters by customer.

## Decision

The customer's page reads the customer's newest orders through the order
entity for an operator who may read the orders, and the order list takes a
customer to list the orders of. The page lists five and links to the list
when there are more.

## Consequences

- Support reads a customer's orders where it reads the customer.
- An operator who may read the customers and not the orders is read none
  of them (ADR 0251).
- An order read that fails leaves the customer on screen and says so.
- The order list's boxes narrow one customer's orders as they narrow
  every order.
- A guest's orders carry no customer and are not on any customer's page.

## Rejected

- Reading the orders through the customer module: a customer's orders are
  the order module's records, filtered by the customer they name.
