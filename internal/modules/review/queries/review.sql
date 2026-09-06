-- reviews queries.
--
-- The storefront and the admin read the SAME table through different queries,
-- and the difference is the point of the module: the storefront's listing and
-- its summary both carry status = 'approved' as a LITERAL rather than as a
-- parameter, so no request, no filter and no future refactor can widen them.

-- name: CreateReview :one
INSERT INTO reviews (
    id, product_id, rating, title, body, author_name, status
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetReview :one
SELECT * FROM reviews WHERE id = $1;

-- ModerateReview moves a review and records why.
--
-- The WHERE carries the CURRENT status as well, so the move is decided by the
-- database rather than by what the caller read a moment ago: two operators
-- approving and rejecting at the same instant cannot both win, and the loser is
-- told the review moved under them.
--
-- moderated_at is stamped from now() and not from the application clock, so the
-- moment on the row comes from the same clock as created_at. It is never
-- cleared, because no transition returns a review to 'submitted'.
--
-- name: ModerateReview :one
UPDATE reviews
SET status          = sqlc.arg('next_status')::text,
    moderation_note = sqlc.arg('moderation_note')::text,
    moderated_at    = now(),
    updated_at      = now()
WHERE id = sqlc.arg('id')::text
  AND status = sqlc.arg('current_status')::text
RETURNING *;

-- ListReviews pages the reviews for the ADMIN surface.
--
-- The keyset bound is written with COALESCE sentinels rather than
-- "@after IS NULL OR ...": the OR form measures perfectly and then degrades,
-- because Postgres folds it away in a custom plan and keeps it as a Filter in a
-- generic one, turning the seek into a full index walk. See internal/core/page.
--
-- name: ListReviews :many
SELECT * FROM reviews
WHERE (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('product_id')::text IS NULL OR product_id = sqlc.narg('product_id')::text)
  AND (created_at, id) < (
    COALESCE(sqlc.narg('after_at')::timestamptz, 'infinity'::timestamptz),
    COALESCE(sqlc.narg('after_id')::text, '')
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountReviews :one
SELECT count(*) FROM reviews
WHERE (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('product_id')::text IS NULL OR product_id = sqlc.narg('product_id')::text);

-- ListApprovedReviews pages the reviews a STOREFRONT may see.
--
-- It is a separate query from ListReviews rather than the same one called with
-- status = 'approved', and that is the module's central safety property written
-- in SQL: here the status is a literal the caller cannot supply, so the one
-- listing a shopper reaches cannot be widened by a parameter, a nil pointer or
-- a filter that a later change forgets to set. A shared query with a status
-- argument would put the whole design one missing assignment away from
-- publishing everything ever submitted.
--
-- name: ListApprovedReviews :many
SELECT * FROM reviews
WHERE product_id = sqlc.arg('product_id')::text
  AND status = 'approved'
  AND (created_at, id) < (
    COALESCE(sqlc.narg('after_at')::timestamptz, 'infinity'::timestamptz),
    COALESCE(sqlc.narg('after_id')::text, '')
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountApprovedReviews :one
SELECT count(*) FROM reviews
WHERE product_id = sqlc.arg('product_id')::text AND status = 'approved';

-- SummarizeApprovedReviews is the count and the average a product page shows.
--
-- The average is COMPUTED HERE and stored nowhere. Measured against PostgreSQL
-- 16 on 505,000 reviews over 20,001 products, with the module's partial index:
-- 0.2 ms at 19 approved reviews, 1.3-2.0 ms at 5,000, 9.3 ms at 50,000 — and
-- 33-38 ms with no index at all, where it is a full sequential scan whatever the
-- product's size. A stored counter would save those milliseconds and buy an
-- obligation on every path that writes a review; the trade is argued in full on
-- the Summary type in the models package.
--
-- The average comes back in HUNDREDTHS as a bigint rather than as a numeric:
-- avg() returns numeric, the driver would hand it over as a decimal type this
-- module would then have to round somewhere, and the rounding decision belongs
-- in the one place that can state it. COALESCE covers the product with no
-- approved review at all, where avg() is NULL — count is 0 in the same row, so
-- the client can tell that zero apart from a genuine average of zero, which
-- cannot occur anyway because the rating floor is 1.
--
-- name: SummarizeApprovedReviews :one
SELECT count(*)::bigint AS review_count,
       COALESCE(round(avg(rating) * 100), 0)::bigint AS average_hundredths
FROM reviews
WHERE product_id = sqlc.arg('product_id')::text AND status = 'approved';
