-- product_type is the shape of a product, and it exists because something is
-- already ASKING for it.
--
-- # The consumer was built first, and it has been starving
--
-- The tax module matches a rate rule on three references and one of them is
-- `product_type`: `models.ReferenceProductType` is a defined reference, the
-- cross-module request carries `product_type_id`
-- (internal/modules/tax/service/interop.go), and the local calculation turns it
-- into a match key (local.go). Every one of those lines is live, and the field
-- has been EMPTY on every request ever made, because nothing in the tree could
-- name a type. A merchant could not say "books are taxed at 1%" — only "this
-- product is", one product at a time.
--
-- That is the reverse of the error ADR 0009 and ADR 0063 refuse. There the
-- capability had no consumer; here the consumer was published and had no
-- capability, which is the same divergence seen from the other end.
--
-- # Why it is a column on the product and not a map table
--
-- A product has ONE type and several categories, and the tax rule that made
-- this necessary matches a single value. The taxonomy already draws that
-- distinction: `collection_id` is a column because a product belongs to one
-- collection, while categories and tags go through map tables. A type follows
-- the collection.
--
-- # Why the delete has to release the products, in SQL of our own
--
-- ON DELETE SET NULL cannot fire against a SOFT delete: the row stays
-- physically in place, so the database never runs the action the schema
-- promises. `ClearCollectionProducts` answers the identical question for the
-- collection and the service does the two writes in ONE transaction; the type
-- takes the same answer for the same reason.
CREATE TABLE IF NOT EXISTS product_type (
    id         text PRIMARY KEY,
    value      text        NOT NULL,
    handle     text        NOT NULL,
    metadata   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,

    CONSTRAINT product_type_value_check  CHECK (value <> ''),
    CONSTRAINT product_type_handle_check CHECK (handle <> '')
);

-- The handle is unique among the LIVE rows, so deleting a type frees its handle
-- for a new one. It is the shape the collection, the category and the tag all
-- take.
CREATE UNIQUE INDEX IF NOT EXISTS product_type_handle_uniq
    ON product_type (handle) WHERE deleted_at IS NULL;

ALTER TABLE product
    ADD COLUMN type_id text REFERENCES product_type (id) ON DELETE SET NULL;

-- The listing filters by type the way it filters by collection, and a product
-- with no type is the common case, so the index covers only the rows that have
-- one.
CREATE INDEX IF NOT EXISTS product_type_idx
    ON product (type_id) WHERE deleted_at IS NULL AND type_id IS NOT NULL;
