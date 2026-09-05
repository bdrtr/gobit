-- invoice_series queries.
--
-- The series is the only mutable row in this module, and it is mutable in
-- exactly one way: its last_number goes up by one, under a lock, in the same
-- transaction as the invoice that took the number.

-- name: CreateSeries :one
INSERT INTO invoice_series (id, prefix, year)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetSeries :one
SELECT * FROM invoice_series WHERE id = $1;

-- name: GetSeriesByPrefixYear :one
SELECT * FROM invoice_series WHERE prefix = $1 AND year = $2;

-- name: ListSeries :many
SELECT * FROM invoice_series
ORDER BY year DESC, prefix;

-- TakeNextNumber opens the series if it is new and takes the next number, in
-- ONE statement.
--
-- # Why one statement and not create-then-advance
--
-- The obvious arrangement — look for the series, create it if it is missing,
-- then advance it — cannot recover from its own race. Two callers issuing the
-- first document of a year both find nothing and both insert; one gets a unique
-- violation, and in PostgreSQL an error inside a transaction POISONS it
-- (SQLSTATE 25P02). Every statement after that fails too, so the "read the
-- winner's row instead" fallback has nothing left to run in. The concurrency
-- test found exactly that.
--
-- ON CONFLICT never raises. The insert lands or the update runs, the row comes
-- back either way, and the statement takes the row lock it needs on its way
-- through.
--
-- # Why this single statement is the whole concurrency control
--
-- No SELECT ... FOR UPDATE is taken alongside it. An UPDATE acquires the row
-- lock itself and holds it until the transaction ends, and it re-reads the row
-- after acquiring it, so last_number + 1 is computed from the value the other
-- transaction committed rather than from a stale read. A separate lock before
-- it would lock a row this statement is about to lock anyway — protection that
-- looks like protection and adds none.
--
-- The gap-freeness comes from WHERE the statement runs: inside the same
-- transaction as the invoice that takes the number. A rollback takes the
-- increment back with it, so a failed issue leaves no hole.
--
-- name: TakeNextNumber :one
INSERT INTO invoice_series (id, prefix, year, last_number)
VALUES ($1, $2, $3, 1)
ON CONFLICT (prefix, year) DO UPDATE
SET last_number = invoice_series.last_number + 1,
    updated_at  = now()
RETURNING *;
