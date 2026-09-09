-- order_claim_evidence binds a claim to the FILES that show what went wrong.
--
-- # Why a table and not a column
--
-- A damaged parcel is rarely one photograph. The claim carries several pieces of
-- evidence or none, and a column could hold exactly one — which would make the
-- second photograph a reason to edit the first.
--
-- # Why the UPLOAD ID alone, and not the address beside it
--
-- product_image carries both, and this table deliberately does not. Two things
-- differ.
--
-- The evidence is read LATE. An operator opens a claim days or months after it
-- was filed, and an object store's address is signed and expires; a stored
-- address would be a record with a rotting half, right about the file and wrong
-- about where to get it. The id resolves through the file module whenever it is
-- asked.
--
-- And nothing renders it on a hot path. product_image's address is written into
-- a storefront page on every product view, which is what makes carrying it worth
-- the staleness; a claim's evidence is opened by one operator, one claim at a
-- time, and can afford the resolve.
--
-- # NO foreign key on upload_id
--
-- The id belongs to the file module and a cross-module foreign key is banned
-- (Principle 2.2): the two modules must stay separable. The id is kept opaque,
-- exactly as product_image.upload_id and shipping_options.region_id are. The
-- CHECK keeps out the empty string, which is the one value that would claim
-- "there is a file" while naming none.
--
-- # NO index on upload_id
--
-- The reverse direction — "which claims use this upload" — is not read from
-- here, and product_image's migration 000002 wrote the rule down: an index with
-- no reader loads a cost onto every write and advertises a use that does not
-- exist. When a query inside this module reads by upload_id, that is the day it
-- is added.
CREATE TABLE IF NOT EXISTS order_claim_evidence (
    id             TEXT        PRIMARY KEY,
    order_claim_id TEXT        NOT NULL
        REFERENCES order_claims (id) ON DELETE CASCADE,
    -- upload_id is the file module's id; there is NO FK (Principle 2.2).
    upload_id      TEXT        NOT NULL,
    -- caption is what the operator says the picture shows; it may be empty,
    -- because a photograph of a crushed box often says it by itself.
    caption        TEXT        NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_claim_evidence_upload_present CHECK (upload_id <> ''),
    -- One upload is evidence of one claim ONCE. Attaching the same file twice is
    -- a double click, not a second piece of evidence.
    CONSTRAINT order_claim_evidence_upload_uniq UNIQUE (order_claim_id, upload_id)
);

-- The listing is per claim and in the order the evidence was attached.
CREATE INDEX IF NOT EXISTS order_claim_evidence_claim_idx
    ON order_claim_evidence (order_claim_id, created_at, id);
