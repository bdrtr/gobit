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

-- CountInvoicesByBuyerEmail counts the documents issued to one address.
--
-- It is the ONLY way this module can resolve a person, and that is a property
-- of the table rather than a choice: invoices carries no customer_id and no
-- order_id, so the single handle on a human being is the address printed on the
-- document. The count is what the erasure contract reports as the number of
-- rows retained (ADR 0032); it is a count and not a SELECT because nothing in
-- the answer needs the documents themselves, and reading whole invoices to
-- discard them would be a table's worth of buyer data pulled into memory in
-- order to say "we kept them".
--
-- The comparison is case-insensitive and both sides are lowered. This module,
-- unlike customer and auth, has NO check constraint forcing buyer_email to
-- lower case — an invoice copies what the document said — so a case-sensitive
-- match would report "0 retained" about a person whose invoice is in the table
-- under one capital letter. The lower() on the column is written exactly as
-- 000002 writes the invoices_buyer_email_idx expression, because an index on
-- lower(buyer_email) is only usable by a predicate spelled the same way.
--
-- name: CountInvoicesByBuyerEmail :one
SELECT count(*) FROM invoices
WHERE lower(buyer_email) = lower(sqlc.arg('buyer_email')::text);
