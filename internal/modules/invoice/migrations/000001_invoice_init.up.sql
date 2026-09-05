-- The invoice module's schema.
--
-- # An invoice is a SNAPSHOT
--
-- Every column here is a copy taken at the moment the document was issued: the
-- two parties, the lines, the amounts. Nothing is a foreign key into another
-- module's table, and not only because Principle 2.2 forbids it — a document
-- that changed when the catalog or the customer record was edited would stop
-- being the permanent answer it exists to be.
--
-- # An issued document is IMMUTABLE
--
-- There is no UPDATE path for the amounts, the parties or the lines. What can
-- change is the STATUS and the fields that describe a transmission
-- (provider_id, external_id, status_reason). A mistake is corrected with a
-- cancellation and a new document, which is also how the law treats it.

-- invoice_series is the source of invoice numbers.
--
-- # Why a table and not a sequence
--
-- The order module numbers its orders with an IDENTITY sequence and its
-- migration argues the case well: a sequence advances atomically and two
-- concurrent inserts cannot collide. The invoice needs the opposite answer for
-- a legal reason: a sequence is NOT gap-free. It advances outside the
-- transaction, so a rolled-back transaction burns its number and leaves a hole,
-- and a hole in an invoice series reads as a document that was issued and then
-- made to disappear.
--
-- A row can be locked. Every invoice in a series takes SELECT ... FOR UPDATE on
-- this row, reads last_number, writes last_number + 1, and commits the two
-- together; a rollback takes the number back with it. The cost is that invoices
-- in the same series serialize, and that is what the guarantee is worth.
--
-- The uniqueness is on (prefix, year) rather than on prefix alone because the
-- numbering restarts every year.
CREATE TABLE invoice_series (
    id          text PRIMARY KEY,
    prefix      text        NOT NULL,
    year        integer     NOT NULL,
    -- last_number starts at 0 and the first invoice takes 1: the number a
    -- reader sees is always one that was actually handed out.
    last_number bigint      NOT NULL DEFAULT 0 CHECK (last_number >= 0),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX invoice_series_prefix_year_uniq
    ON invoice_series (prefix, year);

-- invoices holds the documents.
CREATE TABLE invoices (
    id             text PRIMARY KEY,
    -- number is the legal serial. It is UNIQUE as the last line of defence: the
    -- row lock is what prevents a collision, and this index is what catches one
    -- if the lock is ever bypassed.
    number         text        NOT NULL,
    series_id      text        NOT NULL REFERENCES invoice_series (id),
    kind           text        NOT NULL,
    status         text        NOT NULL,
    currency_code  text        NOT NULL,
    -- The two parties are copied in full. They are separate columns rather than
    -- a JSON blob because they are printed fields with fixed meanings, and a
    -- blob would let a typo in a key produce a document with a missing name and
    -- no error anywhere.
    seller_name         text NOT NULL,
    seller_tax_number   text NOT NULL DEFAULT '',
    seller_tax_office   text NOT NULL DEFAULT '',
    seller_email        text NOT NULL DEFAULT '',
    seller_address      text NOT NULL DEFAULT '',
    seller_country_code text NOT NULL DEFAULT '',
    buyer_name          text NOT NULL,
    buyer_tax_number    text NOT NULL DEFAULT '',
    buyer_tax_office    text NOT NULL DEFAULT '',
    buyer_email         text NOT NULL DEFAULT '',
    buyer_address       text NOT NULL DEFAULT '',
    buyer_country_code  text NOT NULL DEFAULT '',
    -- The totals are STORED rather than summed on read: the document is what
    -- was issued, and a total recomputed later could differ from the printed
    -- one if the summing rule ever changed.
    subtotal       bigint      NOT NULL,
    discount_total bigint      NOT NULL DEFAULT 0,
    tax_total      bigint      NOT NULL DEFAULT 0,
    total          bigint      NOT NULL,
    issued_at      timestamptz NOT NULL,
    provider_id    text        NOT NULL DEFAULT '',
    external_id    text        NOT NULL DEFAULT '',
    status_reason  text        NOT NULL DEFAULT '',
    metadata       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    -- The totals identity is enforced by the DATABASE and not only by the
    -- service: a document whose total does not follow from its own parts is
    -- unprintable, and the check costs nothing on a table that is written once.
    CONSTRAINT invoices_totals_identity
        CHECK (total = subtotal - discount_total + tax_total)
);

CREATE UNIQUE INDEX invoices_number_uniq ON invoices (number);

-- The listing order, so a cursor walk becomes an index seek rather than a scan
-- (see internal/core/page).
CREATE INDEX invoices_listing_idx ON invoices (created_at DESC, id DESC);

CREATE INDEX invoices_series_idx ON invoices (series_id);

-- invoice_lines holds the rows of a document.
CREATE TABLE invoice_lines (
    id             text PRIMARY KEY,
    invoice_id     text    NOT NULL REFERENCES invoices (id) ON DELETE CASCADE,
    -- position is the printed order. It is stored rather than derived from the
    -- insertion order, because the order of the rows is part of the document
    -- and sorting by id would rearrange it the day the id format changes.
    position       integer NOT NULL CHECK (position >= 1),
    description    text    NOT NULL,
    quantity       bigint  NOT NULL CHECK (quantity > 0),
    unit_price     bigint  NOT NULL,
    subtotal       bigint  NOT NULL,
    discount_total bigint  NOT NULL DEFAULT 0,
    -- tax_rate_bps is the rate the line was charged at, in basis points. It is
    -- copied rather than recomputed: rounding down per line maps a range of
    -- rates onto one amount, and the document prints the rate that was charged.
    tax_rate_bps   integer NOT NULL DEFAULT 0 CHECK (tax_rate_bps >= 0),
    tax_total      bigint  NOT NULL DEFAULT 0,
    total          bigint  NOT NULL,
    CONSTRAINT invoice_lines_totals_identity
        CHECK (total = subtotal - discount_total + tax_total)
);

CREATE UNIQUE INDEX invoice_lines_position_uniq
    ON invoice_lines (invoice_id, position);
