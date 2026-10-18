-- The order list searched by q (ADR 0456).
--
-- These are ListOrders and CountOrders with the search added, kept apart
-- rather than written into them as "q IS NULL OR ...": a prepared statement's
-- generic plan keeps such an OR as a filter, and the filter here is the
-- document computed for every row walked. The narrowings are ListOrders' and
-- the four statements are changed together.
--
-- An order is a hit by its number, by its e-mail, or by an address it holds
-- now. The three are read through their own indexes (orders_display_id_uniq,
-- orders_search_idx, order_addresses_search_idx) and united, so a q's words
-- are all found in the e-mail or all in one address. The hits are
-- MATERIALIZED before the narrowings, the sort and the page: left to the
-- planner, a page of twenty under ORDER BY created_at walked the listing and
-- computed the document for each row until twenty matched, which for a q
-- matching nothing is the whole table
-- (docs/measurements/0456-what-a-search-read.md). The hits reach
-- orders as an array, which is read through the primary key: joined, the
-- planner, expecting thousands of hits from a prefix it cannot estimate,
-- hashed every order to find three.

-- name: SearchOrders :many
WITH hit AS MATERIALIZED (
    SELECT o.id FROM orders o
    WHERE o.display_id = sqlc.narg('display_id')::bigint
       OR order_search_document(NULL, o.email) @@ order_search_query(sqlc.narg('q')::text)
    UNION
    SELECT a.order_id FROM order_addresses a
    WHERE a.superseded_at IS NULL
      AND order_search_document(a.phone, a.first_name, a.last_name, a.company)
          @@ order_search_query(sqlc.narg('q')::text)
),
page AS (
    SELECT orders.id, orders.created_at FROM orders
    WHERE orders.id = ANY (ARRAY(SELECT hit.id FROM hit))
      AND (sqlc.narg('customer_id')::text IS NULL OR customer_id = sqlc.narg('customer_id')::text)
      AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
      AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
      AND (sqlc.narg('adds_to_order_id')::text IS NULL OR adds_to_order_id = sqlc.narg('adds_to_order_id')::text)
      -- An order awaits its payment when it is not canceled and what was
      -- collected for it falls short of its total less its credits (ADR 0294).
      -- Refunds are not added back: money returned for goods returned or written
      -- off does not make the order owe again.
      AND (sqlc.narg('awaiting_payment')::boolean IS NULL OR (
        orders.status <> 'canceled'
        AND COALESCE((SELECT s.paid_total FROM order_summaries s WHERE s.order_id = orders.id), 0)
          < orders.total - COALESCE((SELECT SUM(c.amount) FROM order_credit_lines c WHERE c.order_id = orders.id), 0)
      ) = sqlc.narg('awaiting_payment')::boolean)
      AND (sqlc.narg('placed_by_operator')::boolean IS NULL
           OR (placed_by IS NOT NULL) = sqlc.narg('placed_by_operator')::boolean)
      AND (sqlc.narg('sales_channel_id')::text IS NULL
           OR sales_channel_id = sqlc.narg('sales_channel_id')::text)
      -- An order's payment status is read from what its summary reports
      -- collected and refunded, against its total less its credits (ADR 0457).
      -- The CASE is written out in the four list statements and changed in all.
      AND (sqlc.narg('payment_status')::text IS NULL OR sqlc.narg('payment_status')::text = (
        SELECT CASE
                 WHEN COALESCE(s.refunded_total, 0) > 0 AND s.refunded_total >= s.paid_total THEN 'refunded'
                 WHEN COALESCE(s.refunded_total, 0) > 0 THEN 'partially_refunded'
                 WHEN COALESCE(s.paid_total, 0) >= orders.total
                      - COALESCE((SELECT SUM(c.amount) FROM order_credit_lines c WHERE c.order_id = orders.id), 0)
                   THEN 'captured'
                 WHEN COALESCE(s.paid_total, 0) = 0 THEN 'not_paid'
                 ELSE 'partially_captured'
               END
        FROM (SELECT 1) AS one LEFT JOIN order_summaries s ON s.order_id = orders.id))
      AND (orders.created_at, orders.id) < (
        COALESCE(sqlc.narg('after_at')::timestamptz, 'infinity'::timestamptz),
        COALESCE(sqlc.narg('after_id')::text, '')
      )
    ORDER BY orders.created_at DESC, orders.id DESC
    LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint
)
SELECT orders.* FROM orders
JOIN page ON page.id = orders.id
ORDER BY page.created_at DESC, page.id DESC;

-- name: CountSearchedOrders :one
WITH hit AS MATERIALIZED (
    SELECT o.id FROM orders o
    WHERE o.display_id = sqlc.narg('display_id')::bigint
       OR order_search_document(NULL, o.email) @@ order_search_query(sqlc.narg('q')::text)
    UNION
    SELECT a.order_id FROM order_addresses a
    WHERE a.superseded_at IS NULL
      AND order_search_document(a.phone, a.first_name, a.last_name, a.company)
          @@ order_search_query(sqlc.narg('q')::text)
)
SELECT COUNT(*) FROM orders
WHERE orders.id = ANY (ARRAY(SELECT hit.id FROM hit))
  AND (sqlc.narg('customer_id')::text IS NULL OR customer_id = sqlc.narg('customer_id')::text)
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('adds_to_order_id')::text IS NULL OR adds_to_order_id = sqlc.narg('adds_to_order_id')::text)
  -- An order awaits its payment when it is not canceled and what was
  -- collected for it falls short of its total less its credits (ADR 0294).
  -- Refunds are not added back: money returned for goods returned or written
  -- off does not make the order owe again.
  AND (sqlc.narg('awaiting_payment')::boolean IS NULL OR (
    orders.status <> 'canceled'
    AND COALESCE((SELECT s.paid_total FROM order_summaries s WHERE s.order_id = orders.id), 0)
      < orders.total - COALESCE((SELECT SUM(c.amount) FROM order_credit_lines c WHERE c.order_id = orders.id), 0)
  ) = sqlc.narg('awaiting_payment')::boolean)
  AND (sqlc.narg('placed_by_operator')::boolean IS NULL
       OR (placed_by IS NOT NULL) = sqlc.narg('placed_by_operator')::boolean)
  AND (sqlc.narg('sales_channel_id')::text IS NULL
       OR sales_channel_id = sqlc.narg('sales_channel_id')::text)
  -- An order's payment status is read from what its summary reports
  -- collected and refunded, against its total less its credits (ADR 0457).
  -- The CASE is written out in the four list statements and changed in all.
  AND (sqlc.narg('payment_status')::text IS NULL OR sqlc.narg('payment_status')::text = (
    SELECT CASE
             WHEN COALESCE(s.refunded_total, 0) > 0 AND s.refunded_total >= s.paid_total THEN 'refunded'
             WHEN COALESCE(s.refunded_total, 0) > 0 THEN 'partially_refunded'
             WHEN COALESCE(s.paid_total, 0) >= orders.total
                  - COALESCE((SELECT SUM(c.amount) FROM order_credit_lines c WHERE c.order_id = orders.id), 0)
               THEN 'captured'
             WHEN COALESCE(s.paid_total, 0) = 0 THEN 'not_paid'
             ELSE 'partially_captured'
           END
    FROM (SELECT 1) AS one LEFT JOIN order_summaries s ON s.order_id = orders.id));
