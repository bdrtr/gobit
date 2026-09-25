-- product_relation: the products a product points a shopper to (ADR 0180).
--
-- # A closed set of kinds
--
-- Three, each a question a product page asks: what goes WITH this
-- (cross_sell), what is the BETTER one (up_sell), and what to buy INSTEAD
-- (substitute). A free-text kind would let two installations, or two operators
-- of one, spell the same widget two ways, and a storefront could not know which
-- word to ask for. A fourth kind is a migration and a decision.
--
-- # Directed and ranked
--
-- A relation is read from the product it belongs to: a charger is a cross-sell
-- of a phone without the phone being one of the charger's. The rank is the
-- order the operator put them in, which is the order the storefront shows.
--
-- # Why the rows are deleted with the product, and not soft deleted
--
-- A relation is not a record anybody reads after the fact; it is a pointer. A
-- product that is deleted takes the pointers to and from it with it, in the
-- same transaction as the product's other children, so no list ever names a
-- product that is gone.
CREATE TABLE IF NOT EXISTS product_relation (
    product_id         text        NOT NULL REFERENCES product (id) ON DELETE CASCADE,
    type               text        NOT NULL,
    related_product_id text        NOT NULL REFERENCES product (id) ON DELETE CASCADE,
    rank               integer     NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (product_id, type, related_product_id),
    CONSTRAINT product_relation_type_check
        CHECK (type IN ('cross_sell', 'up_sell', 'substitute')),
    CONSTRAINT product_relation_not_itself
        CHECK (product_id <> related_product_id),
    CONSTRAINT product_relation_rank_nonneg
        CHECK (rank >= 0),
    -- One product per place: the order is total, so two reads of one list can
    -- never disagree on it. Its index is also the one the storefront reads a
    -- kind through, in the operator's order.
    CONSTRAINT product_relation_rank_unique
        UNIQUE (product_id, type, rank)
);

-- A deleted product takes the relations pointing AT it too; this finds them.
CREATE INDEX IF NOT EXISTS product_relation_target_idx
    ON product_relation (related_product_id);
