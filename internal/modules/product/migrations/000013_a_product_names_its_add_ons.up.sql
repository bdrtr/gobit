-- product_add_on: the variants a product's cart lines may carry as add-ons
-- (ADR 0228).
--
-- An add-on is a variant of another product, an engraving or a gift wrap, with
-- its own price set; a cart line of this product may open a line of it bound to
-- itself. The list is the operator's, in the order the storefront shows it.
--
-- The rows are deleted with either side, as a relation's are (ADR 0180): the
-- product's deletion takes its list and every entry naming one of its variants,
-- and a variant's deletion takes the entries naming it.
CREATE TABLE IF NOT EXISTS product_add_on (
    product_id text        NOT NULL REFERENCES product (id) ON DELETE CASCADE,
    variant_id text        NOT NULL REFERENCES product_variant (id) ON DELETE CASCADE,
    rank       integer     NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (product_id, variant_id),
    CONSTRAINT product_add_on_rank_nonneg CHECK (rank >= 0),
    CONSTRAINT product_add_on_rank_unique UNIQUE (product_id, rank)
);

-- A deleted variant takes the entries naming it; this finds them.
CREATE INDEX IF NOT EXISTS product_add_on_variant_idx ON product_add_on (variant_id);
