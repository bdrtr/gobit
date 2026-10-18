# What a search read — measured 2026-10-18

Evidence for [ADR 0456](../adr/0456-an-admin-list-is-searched-in-the-database.md),
[ADR 0457](../adr/0457-an-orders-payment-status-is-read-from-its-summary.md)
and gaps D311 and D312.

## 1. The consumer's workaround

depremtek's backend (`dtm/listeler_orders.go`, `listeler_customers.go`,
`listeler_reviews.go`) answered its panel's order, customer and review
search by reading gobit's newest 10,000 orders or customers, or 2,000
reviews, through the admin API and the read layer, matching them in Go, and
flagging `truncated` when the window ended before the table did. A search
was exact only inside that window, and every keystroke after the debounce
read the window again. gobit's lists took no text: the customer list's
`email` is an equality, and the order and review lists had nothing.

## 2. The rig

PostgreSQL 16 (`postgres:16-alpine`, the image CI runs), the customer,
order and review migrations applied as the module runs them, in a database
created with `--locale=C` collation and `C.UTF-8` CTYPE, as the shipped
compose creates it. The data:

- 100,000 customers: thirty Turkish first names and thirty last names
  written with their marks, an address `first.last<n>@<one of five mail
  domains>`, a unique `+90 5…` phone;
- 300,000 orders of those customers, each with a shipping and a billing
  address copied from the customer, one in ten with the company
  `Deprem Teknoloji A.S.`, a summary for each (canceled ones unpaid, one in
  ten of the rest refunded);
- 200,000 reviews over 5,000 products, five Turkish titles, five Turkish
  texts of 60 to 90 characters with a number, a byline `First L.`.

`VACUUM ANALYZE` after the load. Every statement is the one sqlc generated,
prepared and run five times; the figure is the median of the last four
`EXPLAIN ANALYZE` execution times. A page is 21 rows, the 20 the service
asks for and the one it reads to know a next page exists.

## 3. The sizes

| relation | size |
|---|---|
| customer | 12 MB |
| customer_search_idx | 43 MB |
| orders | 60 MB |
| orders_search_idx | 13 MB |
| order_addresses | 93 MB |
| order_addresses_search_idx | 66 MB |
| reviews | 41 MB |
| reviews_search_idx | 15 MB |

Most of a phone-bearing index is the phone's suffixes: every run of four
digits or more that ends the number is a lexeme, so a customer's unique
phone adds about nine. The order addresses repeat their customer's phone,
so their posting lists compress.

Building the four indexes on these rows, as the migrations do: 3.3 s
(customers), 2.6 s (orders), 9.2 s (order addresses), 5.7 s (reviews).
`CREATE INDEX` holds off writes to its table for that time.

Writing 20,000 customers in one statement took 75 ms without the index and
720 ms with it, about 32 µs a row.

## 4. Why plpgsql, and why COST

The same document written as a `LANGUAGE sql` function cost 5,628 ms for
20,000 rows, a plpgsql one 356 ms: a SQL function called from an index
expression is planned on every call, a plpgsql one keeps its plans.

Before the functions carried `COST 1000` and before the hits were
`MATERIALIZED`, a page of twenty under `ORDER BY created_at DESC` was
planned as a walk of the listing index filtering on the document,
expecting twenty hits early. For a q that matched eleven customers of
200,000 (a SQL function, the earlier draft) the walk computed the document
for every row: 46 s. With the plpgsql function and no `MATERIALIZED`, the
same plan took 5.2 s. Materialized, the hits come from the index and only
they are sorted.

## 5. Why a phone's digit groups are not words too, and the array

A first draft read `0503 359 23 98` as the phone `5033592398` OR the words
`0503 & 359 & 23 & 98`. The planner estimated 18,000 hits from the four
short prefixes and hashed all 300,000 orders to join three: 50 to 110 ms.
The phone alone, and the hits handed to `orders` as `id = ANY (ARRAY(...))`
rather than joined, gave 5 ms; a prefix the planner cannot estimate
otherwise still made it hash every order (`123457`: 55 ms joined, 5.5 ms
through the array). The array costs the broad q: one letter took 148 ms
joined and 180 ms through the array.

## 6. The figures

PostgreSQL's default `plan_cache_mode` kept the custom plan for all four
search statements over eight executions (`pg_prepared_statements`:
`generic_plans = 0`); the generic column is what `force_generic_plan`
gives, JIT included.

| list | q | page, custom plan | page, generic plan | count, custom | count, generic |
|---|---|---|---|---|---|
| customers | a full name (Emre Aksoy, ~111 hits) | 0.5 ms | 0.8 ms | 0.4 ms | 0.7 ms |
| customers | an e-mail as typed | 1.7 ms | 1.8 ms | 1.2 ms | 1.7 ms |
| customers | a phone as typed (0503 359 23 98) | 4.2 ms | 0.1 ms | 4.7 ms | 0.1 ms |
| customers | a phone by its last four digits | 4.6 ms | 0.2 ms | 4.7 ms | 0.1 ms |
| customers | a first name folded (ozlem, ~3,300 hits) | 4.7 ms | 0.9 ms | 4.6 ms | 0.5 ms |
| customers | a mail domain (gmail, 20,000 hits) | 6.4 ms | 5.1 ms | 4.2 ms | 2.4 ms |
| customers | one letter (a, most rows) | 8.1 ms | 8.4 ms | 5.5 ms | 4.5 ms |
| customers | nothing matches (zzzz) | 4.6 ms | 0.1 ms | 4.7 ms | 0.1 ms |
| orders | #123457 (the number alone) | 0.1 ms | 5.1 ms | 0.1 ms | 5.3 ms |
| orders | 123457 (the number and the words) | 5.3 ms | 5.0 ms | 5.1 ms | 5.1 ms |
| orders | a full name (Emre Aksoy, ~333 hits) | 9.8 ms | 12.2 ms | 8.6 ms | 12.5 ms |
| orders | an e-mail as typed | 8.4 ms | 9.2 ms | 8.0 ms | 9.8 ms |
| orders | a phone as typed | 5.0 ms | 4.9 ms | 5.3 ms | 5.2 ms |
| orders | a company word (deprem, 30,000 hits) | 98.9 ms | 91.5 ms | 54.5 ms | 93.0 ms |
| orders | one letter (a, most rows) | 176.2 ms | 184.8 ms | 147.9 ms | 180.1 ms |
| orders | a name and a status (emre aksoy, completed) | 9.1 ms | 12.1 ms | 8.6 ms | 11.7 ms |
| orders | a name and payment_status=refunded | 10.1 ms | 13.5 ms | 9.6 ms | 13.6 ms |
| orders | nothing matches (zzzz) | 5.2 ms | 5.1 ms | 4.7 ms | 4.8 ms |
| orders | no q, payment_status=captured (plain list) | 0.2 ms | 208.5 ms | 821.1 ms | 916.6 ms |
| orders | no q, payment_status=partially_refunded (no row has it) | 772.1 ms | 967.5 ms | 809.1 ms | 898.6 ms |
| orders | no q, no filter (the list as before) | 0.0 ms | 209.3 ms | 6.3 ms | 189.0 ms |
| reviews | two words of the text (kargo hizli, 40,000 hits) | 18.8 ms | 20.0 ms | 9.3 ms | 14.3 ms |
| reviews | a rare word of one text, in the queue | 5.8 ms | 12.5 ms | 4.2 ms | 11.7 ms |
| reviews | an author (zeynep k) | 5.0 ms | 5.2 ms | 3.9 ms | 4.6 ms |
| reviews | one letter (a, most rows) | 37.0 ms | 49.7 ms | 19.5 ms | 32.9 ms |
| reviews | nothing matches (zzzz) | 3.8 ms | 0.1 ms | 4.1 ms | 0.0 ms |

The last three order rows are the plain list without q. A plain count
under any `payment_status`, and a page asking for a status few orders hold,
reads every order's summary, as `awaiting_payment` does since ADR 0294:
measured on the same rows, `awaiting_payment=true` took 624 ms (count) and
579 ms (page). The generic plan of the plain list took 150 ms before this
change and 212 ms after it; PostgreSQL kept the custom plan for it too.

## 7. A cluster that folds ASCII only

On a database created with `--locale=C` (encoding UTF8), the customer
document of `U&'\0130smail'`, `U&'\00C7EL\0130K'` and `a.b@x.com` with
phone `+90 532 111 22 33` is `'05321112233' '1112233' … 'a' 'b' 'celik'
'com' 'ismail' 'x'`: the Turkish capitals fold through translate(), which
does not consult the CTYPE.
