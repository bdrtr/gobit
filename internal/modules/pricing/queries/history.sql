-- history queries — what the ladder read, as it stood after every write
-- (ADR 0167).
--
-- There is no UPDATE and no DELETE in this file, and that is the tables' whole
-- design: a snapshot is a fact about a moment that has passed.

-- InsertPriceSetHistory records a set's live prices as they stand after a
-- write, in the same transaction as the write.
-- name: InsertPriceSetHistory :exec
INSERT INTO price_set_history (id, price_set_id, recorded_at, prices)
VALUES ($1, $2, $3, $4);

-- InsertPriceListHistory records a list's header as it stands after a write.
-- name: InsertPriceListHistory :exec
INSERT INTO price_list_history
    (id, price_list_id, recorded_at, type, status, starts_at, ends_at, deleted)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- ListPriceSetHistory returns every snapshot of the given sets, oldest first.
--
-- All of them rather than a window: the question a reader asks — since when has
-- this reduction run, and what was the price in the thirty days before — reaches
-- back as far as the reduction does, and a set is written when an operator
-- edits it, which is rarely.
-- name: ListPriceSetHistory :many
SELECT * FROM price_set_history
WHERE price_set_id = ANY(sqlc.arg('price_set_ids')::text[])
ORDER BY price_set_id, recorded_at, seq;

-- ListPriceListHistory returns every snapshot of the given lists, oldest first.
-- name: ListPriceListHistory :many
SELECT * FROM price_list_history
WHERE price_list_id = ANY(sqlc.arg('price_list_ids')::text[])
ORDER BY price_list_id, recorded_at, seq;
