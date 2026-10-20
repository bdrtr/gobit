# ADR 0456 — An admin list is searched in the database

**Summary:** `GET /admin/v1/orders`, `/admin/v1/customers` and `/admin/v1/reviews` take `q`, matched in the database through a GIN index on each module's search document, with the list's other filters, paging and count applied to the hits.
It costs a GIN index per searched table and its upkeep on every write, and buys a search that is exact over every row instead of a consumer's window.

- **Status:** Accepted; amended by [0460](0460-an-ascii-capital-i-folds-on-every-cluster.md): the ASCII capital I is translated before `lower()`
- **Date:** 2026-10-18

## Context

depremtek's backend, a consumer, needs its panel to find an order by its
number, e-mail, a name on it or its phone, a customer by a part of their
name, e-mail or phone, and a review by its words or its author (D311). The
three admin lists took no text, so it read the newest 10,000 orders or
customers, or 2,000 reviews, through the admin API, matched them in Go and
flagged `truncated`: exact only inside that window, and the whole window
read again for every search.

## Decision

Each of the customer, order and review modules carries `<module>_search_document`,
the words of the named fields folded by `lower(translate(...))` to
`internal/core/textfold`'s table and split at every ASCII sign, plus every
run of four or more digits ending the phone, and `<module>_search_query`,
which reads a q of digits and phone signs with four digits or more as a
phone and any other q as its words, each the start of a word, all required.
A GIN expression index serves each searched table (`customer`, `orders` by
e-mail, `order_addresses` while current, `reviews`), and the list's q runs
a statement of its own whose hits are materialized before the list's
narrowings, order, page and count apply.

## Consequences

- An order is found by its number (`#1042` alone, `1042` also as words), by every word in its e-mail, or by every word in one address it holds now; words split over the e-mail and an address, or over two addresses, find nothing.
- A customer is found by every word in their first name, last name and e-mail together; a review by its title, text and byline. The account's name is not searched on an order, nor a product's title on a review: neither module holds them.
- A word is matched by its start (`kay` finds `Kaya`, `aya` does not); a phone by any run of its digits, a leading 0 or 90 of the q ignored.
- The Turkish and Latin letters of the table fold on every cluster, a C-locale one included; any other letter folds as the cluster's `lower()` does (ADR 0015), on both sides alike.
- An erasure's own UPDATE takes a person out of the index; no column holds a copy of their data, so no declaration changes.
- `q` over 200 characters is refused with 422; a q of only signs finds nothing.
- A selective q is answered from the index and the hits alone; a q of one letter sorts most of the table. The migrations build the indexes, holding off writes to each table while they do.
- The panel's own screens do not take q yet.

Measurement: [measurements/0456](../measurements/0456-what-a-search-read.md)

## Rejected

- `pg_trgm`: true substring search, but an extension the cluster contract (ADR 0015) does not require.
- A Go-folded search column per row: every write path, erasure included, would have to keep it, and the review and customer declarations would grow a column.
- `ILIKE '%q%'` as the product title filter does: a scan of every row, and the cluster's fold.
- A `LANGUAGE sql` document: planned on every call.
- Matching words in the phone too: the planner expected most of the table and hashed every order.
- The q written into the list statements as `q IS NULL OR …`: a generic plan keeps it as a per-row filter.
