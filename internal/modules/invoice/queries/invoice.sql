-- invoices queries.
--
-- An issued document is IMMUTABLE: there is no UPDATE for its amounts, its
-- parties or its lines. The only mutable part is where it stands — the status
-- and the fields describing a transmission.

-- name: CreateInvoice :one
INSERT INTO invoices (
    id, number, series_id, kind, status, currency_code,
    seller_name, seller_tax_number, seller_tax_office,
    seller_email, seller_address, seller_country_code,
    buyer_name, buyer_tax_number, buyer_tax_office,
    buyer_email, buyer_address, buyer_country_code,
    subtotal, discount_total, tax_total, total,
    issued_at, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17, $18,
    $19, $20, $21, $22,
    $23, $24
)
RETURNING *;

-- name: CreateInvoiceLine :one
INSERT INTO invoice_lines (
    id, invoice_id, position, description, quantity,
    unit_price, subtotal, discount_total, tax_rate_bps, tax_total, total
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetInvoice :one
SELECT * FROM invoices WHERE id = $1;

-- name: GetInvoiceByNumber :one
SELECT * FROM invoices WHERE number = $1;

-- name: ListInvoiceLines :many
SELECT * FROM invoice_lines
WHERE invoice_id = $1
ORDER BY position;

-- name: ListInvoiceLinesForInvoices :many
SELECT * FROM invoice_lines
WHERE invoice_id = ANY(sqlc.arg('invoice_ids')::text[])
ORDER BY invoice_id, position;

-- ListInvoices pages the documents.
--
-- The keyset bound is written with COALESCE sentinels rather than
-- "@after IS NULL OR ...": the OR form measures perfectly and then degrades,
-- because Postgres folds it away in a custom plan and keeps it as a Filter in a
-- generic one, turning the seek into a full index walk. See internal/core/page.
--
-- name: ListInvoices :many
SELECT * FROM invoices
WHERE (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('kind')::text IS NULL OR kind = sqlc.narg('kind')::text)
  AND (created_at, id) < (
    COALESCE(sqlc.narg('after_at')::timestamptz, 'infinity'::timestamptz),
    COALESCE(sqlc.narg('after_id')::text, '')
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountInvoices :one
SELECT count(*) FROM invoices
WHERE (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('kind')::text IS NULL OR kind = sqlc.narg('kind')::text);

-- SetInvoiceStatus moves the document and records why.
--
-- The WHERE carries the CURRENT status as well, so the move is decided by the
-- database rather than by what the caller read a moment ago: two operators
-- cancelling and sending at the same time cannot both win.
--
-- name: SetInvoiceStatus :one
UPDATE invoices
SET status        = sqlc.arg('next_status')::text,
    status_reason = sqlc.arg('status_reason')::text,
    provider_id   = COALESCE(sqlc.narg('provider_id')::text, provider_id),
    external_id   = COALESCE(sqlc.narg('external_id')::text, external_id),
    updated_at    = now()
WHERE id = sqlc.arg('id')::text
  AND status = sqlc.arg('current_status')::text
RETURNING *;
