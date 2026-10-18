# ADR 0449 — A listing reads its ratings in one request

**Summary:** `GET /store/v1/review-summaries?product_id=…` answers the count and the average of the approved reviews of up to a hundred named products, in the order named, from one statement.
It costs one storefront route and one query, and buys a page of product cards its ratings without one request per card or a read of the reviews table.

- **Status:** Accepted
- **Date:** 2026-10-17

## Context

depremtek's backend, a consumer, shows a rating on every product card of its
storefront listings (D304). gobit answered one product's summary at
`GET /store/v1/products/{product_id}/review-summary`; a page of twenty-four
cards was twenty-four requests, so the consumer read gobit's `reviews` table
in its own SQL and computed each product's average and count, a table and a
status value no record publishes.

## Decision

`GET /store/v1/review-summaries` takes the repeated `product_id`, one to a
hundred distinct non-empty ids, and answers each product's summary as the
single read does — approved reviews only, the average in hundredths, a
product with none counted zero — in the order named, from one grouped
statement over the module's partial index.

## Consequences

- A product named twice is answered once; an empty id, none or more than a hundred is a 400.
- The store product listing is unchanged: the catalog and the reviews are two modules, and a card reads both.
- The route answers the publishable key and reads nothing it does not already answer one product at a time, so it opens nothing new; its quota is the store prefix's.
- The status stays a literal in the statement, so no parameter can widen what is counted.

## Rejected

- The rating on the store product list and read: the product module would read the review module's table or join a link nobody declared, and every listing would pay for it.
- An admin route: the storefront is the reader.
- A stored average per product: the single read's measured cost stands, and a stored counter owes every review write.
