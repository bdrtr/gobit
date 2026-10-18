-- The admin review list searched by q (ADR 0456).
--
-- ListReviews and CountReviews with the search added, kept apart for the
-- reason customer_search.sql gives in the customer module: a generic plan
-- keeps "q IS NULL OR ..." as a filter that computes the document per row.
-- The narrowings are theirs and the four statements are changed together.
-- The hits are materialized before they are sorted and paged, for the same
-- reason.

-- name: SearchReviews :many
WITH hit AS MATERIALIZED (
    SELECT r.id, r.created_at FROM reviews r
    WHERE review_search_document(NULL, r.title, r.body, r.author_name)
          @@ review_search_query(sqlc.arg('q')::text)
      AND (sqlc.narg('status')::text IS NULL OR r.status = sqlc.narg('status')::text)
      AND (sqlc.narg('product_id')::text IS NULL OR r.product_id = sqlc.narg('product_id')::text)
      AND (sqlc.narg('suggested')::text IS NULL
           OR r.suggested_status = sqlc.narg('suggested')::text)
      AND (NOT sqlc.arg('unsuggested')::boolean OR r.suggested_status IS NULL)
      AND (r.created_at, r.id) < (
        COALESCE(sqlc.narg('after_at')::timestamptz, 'infinity'::timestamptz),
        COALESCE(sqlc.narg('after_id')::text, '')
      )
),
page AS (
    SELECT hit.id, hit.created_at FROM hit
    ORDER BY hit.created_at DESC, hit.id DESC
    LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint
)
SELECT reviews.* FROM reviews
JOIN page ON page.id = reviews.id
ORDER BY page.created_at DESC, page.id DESC;

-- name: CountSearchedReviews :one
SELECT count(*) FROM reviews r
WHERE review_search_document(NULL, r.title, r.body, r.author_name)
      @@ review_search_query(sqlc.arg('q')::text)
  AND (sqlc.narg('status')::text IS NULL OR r.status = sqlc.narg('status')::text)
  AND (sqlc.narg('product_id')::text IS NULL OR r.product_id = sqlc.narg('product_id')::text)
  AND (sqlc.narg('suggested')::text IS NULL
       OR r.suggested_status = sqlc.narg('suggested')::text)
  AND (NOT sqlc.arg('unsuggested')::boolean OR r.suggested_status IS NULL);
