-- The customer list searched by q (ADR 0456).
--
-- These are ListCustomers and CountCustomers with the search added, kept apart
-- rather than written into them as "q IS NULL OR ...": a prepared statement's
-- generic plan keeps such an OR as a filter, and the filter here is the
-- document computed for every row walked. The narrowings are theirs and the
-- four statements are changed together.
--
-- The hits are MATERIALIZED before they are sorted and paged. Left to the
-- planner, a page of twenty under ORDER BY created_at walked the listing index
-- and computed the document for each row until twenty matched, which for a q
-- matching nothing is the whole table
-- (docs/measurements/0456-what-a-search-read.md).

-- name: SearchCustomers :many
WITH hit AS MATERIALIZED (
    SELECT c.id, c.created_at FROM customer c
    WHERE c.deleted_at IS NULL
      AND customer_search_document(c.phone, c.first_name, c.last_name, c.email)
          @@ customer_search_query(sqlc.arg('q')::text)
      AND (sqlc.narg('email')::text IS NULL OR c.email = sqlc.narg('email')::text)
      AND (sqlc.narg('has_account')::boolean IS NULL OR c.has_account = sqlc.narg('has_account')::boolean)
      AND (sqlc.narg('group_id')::text IS NULL OR EXISTS (
            SELECT 1 FROM customer_group_customer m
            JOIN customer_group g ON g.id = m.customer_group_id AND g.deleted_at IS NULL
            WHERE m.customer_id = c.id AND m.customer_group_id = sqlc.narg('group_id')::text))
      AND (c.created_at, c.id) < (
        COALESCE(sqlc.narg('after_at')::timestamptz, 'infinity'::timestamptz),
        COALESCE(sqlc.narg('after_id')::text, '')
      )
),
page AS (
    SELECT hit.id, hit.created_at FROM hit
    ORDER BY hit.created_at DESC, hit.id DESC
    LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int
)
SELECT customer.* FROM customer
JOIN page ON page.id = customer.id
ORDER BY page.created_at DESC, page.id DESC;

-- name: CountSearchedCustomers :one
SELECT count(*) FROM customer c
WHERE c.deleted_at IS NULL
  AND customer_search_document(c.phone, c.first_name, c.last_name, c.email)
      @@ customer_search_query(sqlc.arg('q')::text)
  AND (sqlc.narg('email')::text IS NULL OR c.email = sqlc.narg('email')::text)
  AND (sqlc.narg('has_account')::boolean IS NULL OR c.has_account = sqlc.narg('has_account')::boolean)
  AND (sqlc.narg('group_id')::text IS NULL OR EXISTS (
        SELECT 1 FROM customer_group_customer m
        JOIN customer_group g ON g.id = m.customer_group_id AND g.deleted_at IS NULL
        WHERE m.customer_id = c.id AND m.customer_group_id = sqlc.narg('group_id')::text));
