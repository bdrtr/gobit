-- A customer keeps a wishlist (ADR 0190).
--
-- One row per variant a customer saved. The pair is the key, so saving the
-- same variant twice is one row, and the key is the only index: every read is
-- one customer's rows, at most the cap of them. An index ordered for the
-- listing measured as large as the table and saved 0.03 ms on a full list
-- (ADR 0190's measurement).
--
-- The variant id belongs to the product module and has no foreign key here, as
-- every cross-module id in this repository has none: whether the variant can
-- still be shown is the catalog's answer when the list is read.
CREATE TABLE IF NOT EXISTS customer_wishlist_item (
    customer_id TEXT        NOT NULL REFERENCES customer (id) ON DELETE CASCADE,
    variant_id  TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, variant_id),
    CONSTRAINT customer_wishlist_item_variant_check CHECK (length(btrim(variant_id)) > 0)
);
