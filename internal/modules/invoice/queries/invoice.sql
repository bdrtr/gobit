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
    buyer_email, buyer_email_folded, buyer_address, buyer_country_code,
    subtotal, discount_total, tax_total, total,
    issued_at, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17, $18, $19,
    $20, $21, $22, $23,
    $24, $25
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
-- The predicate matches buyer_email_folded, which is written by Go, and it does
-- NOT fold anything itself. It used to read "lower(buyer_email) = lower($1)",
-- and the reason that changed is in migration 000003: lower() is the CLUSTER's
-- fold, on a --locale=C database it folds ASCII only, and this count is what
-- decides whether a data subject is told their documents are held or that they
-- are not here. The caller passes an address already folded by
-- models.NormalizeEmail — the same function the other five holders of an
-- address fold with, compared against them by an audit in internal/arch.
--
-- An equality on a plain column, so invoices_buyer_email_folded_idx serves it as
-- a seek rather than a scan of every invoice ever issued.
--
-- name: CountInvoicesByBuyerEmail :one
SELECT count(*) FROM invoices
WHERE buyer_email_folded = sqlc.arg('buyer_email_folded')::text;

-- ListInvoiceBuyerEmailsForRefold pages the documents that carry a buyer
-- address, for the one-time Go re-fold migration 000003 describes.
--
-- It reads ALL of them rather than only the ones that look non-ASCII, and that
-- is deliberate. The obvious narrowing -- "where the address is not pure ASCII"
-- -- would miss a second way the SQL backfill and the Go fold disagree:
-- btrim() strips SPACES, while Go's strings.TrimSpace strips tabs, newlines and
-- the rest of Unicode's whitespace too, so a plain ASCII address stored with a
-- trailing tab folds differently on the two sides. Deciding in Go which rows are
-- wrong needs the Go function, so every row with an address is handed to it.
--
-- Keyset by id, because this runs against a table that is only ever appended to
-- and may be large; an OFFSET walk would re-read what it has already passed.
--
-- name: ListInvoiceBuyerEmailsForRefold :many
SELECT id, buyer_email, buyer_email_folded
FROM invoices
WHERE buyer_email <> ''
  AND id > sqlc.arg('after_id')::text
ORDER BY id
LIMIT sqlc.arg('row_limit')::bigint;

-- SetInvoiceBuyerEmailFolded rewrites one document's erasure handle.
--
-- # This is not an edit to the document, and the columns it does not touch say so
--
-- An issued invoice is immutable (ADR 0024) and the queries above honor that:
-- there is no UPDATE for its amounts, its parties or its lines. This statement
-- writes buyer_email_folded and nothing else. It leaves buyer_email — what the
-- document PRINTS — exactly as it was, and it deliberately does not touch
-- updated_at: that column records when the document's state last moved, and a
-- maintenance pass correcting an index handle did not move it. Bumping it would
-- make every invoice in the table look freshly transmitted to whoever reads
-- that column next.
--
-- name: SetInvoiceBuyerEmailFolded :execrows
UPDATE invoices
SET buyer_email_folded = sqlc.arg('buyer_email_folded')::text
WHERE id = sqlc.arg('id')::text;

-- ListNonAsciiBuyerEmailsForRefold pages only the documents whose buyer address
-- is not pure ASCII.
--
-- It exists for the STARTUP gate, where the full pass ListInvoiceBuyerEmailsForRefold
-- performs would be wrong: that one reads every document carrying an address,
-- which is the right scope for a maintenance command run once by a human and the
-- wrong scope for something that runs on every boot.
--
-- The narrowing is sound because of WHERE the defect comes from. Migration 000003's
-- backfill folded with lower(btrim()), and on a cluster whose ctype is C that
-- differs from the Go fold ONLY where the address carries a letter outside ASCII.
-- A pure-ASCII address folds identically under both, on every cluster, so a row
-- the gate skips cannot be one of the rows the gate exists to find.
--
-- What this scope does NOT cover is the second, rarer disagreement the full pass
-- was widened for: btrim() strips spaces where Go's TrimSpace also strips tabs and
-- newlines, so a pure-ASCII address stored with a trailing tab folds differently
-- and is invisible here. That is why `gobit refold-invoices` still exists and still
-- reads everything — the gate is the floor, not the ceiling.
--
-- name: ListNonAsciiBuyerEmailsForRefold :many
SELECT id, buyer_email, buyer_email_folded
FROM invoices
WHERE buyer_email <> ''
  AND buyer_email !~ '^[[:ascii:]]*$'
  AND id > sqlc.arg('after_id')::text
ORDER BY id
LIMIT sqlc.arg('row_limit')::bigint;
