-- product_import queries (ADR 0205).

-- name: CreateImport :one
INSERT INTO product_import (id, file, rows_total)
VALUES ($1, $2, $3)
RETURNING id, status, rows_total, rows_done, rows_created, rows_updated, rows_failed,
          errors, created_at, started_at, finished_at;

-- name: GetImport :one
SELECT id, status, rows_total, rows_done, rows_created, rows_updated, rows_failed,
       errors, created_at, started_at, finished_at
FROM product_import
WHERE id = $1;

-- ClaimImport takes the oldest import with rows left, so two runners never
-- work through the same one, and marks it running.
-- name: ClaimImport :one
UPDATE product_import
SET status = 'running', started_at = COALESCE(started_at, now())
WHERE id = (
    SELECT id FROM product_import
    WHERE status <> 'completed'
    ORDER BY created_at, id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, file, rows_total, rows_done;

-- RecordImportRow moves the import past one row, counting what the row did
-- and, for a failed row, appending its error while fewer than the cap are kept.
-- name: RecordImportRow :exec
UPDATE product_import
SET rows_done    = rows_done + 1,
    rows_created = rows_created + sqlc.arg('created')::int,
    rows_updated = rows_updated + sqlc.arg('updated')::int,
    rows_failed  = rows_failed + sqlc.arg('failed')::int,
    errors       = CASE
                       WHEN sqlc.narg('error')::jsonb IS NULL
                            OR jsonb_array_length(errors) >= sqlc.arg('error_cap')::int
                           THEN errors
                       ELSE errors || jsonb_build_array(sqlc.narg('error')::jsonb)
                   END
WHERE id = sqlc.arg('id') AND rows_done = sqlc.arg('row_index');

-- FinishImport closes an import whose rows are all done and drops its file.
-- name: FinishImport :exec
UPDATE product_import
SET status = 'completed', finished_at = now(), file = NULL
WHERE id = $1 AND rows_done = rows_total AND status <> 'completed';
