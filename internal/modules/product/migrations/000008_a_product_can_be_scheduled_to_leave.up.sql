-- product gains the moment it is to be archived (ADR 0179).
--
-- A limited-time product is published on one day and taken off on another. The
-- second moment is a column beside publish_at rather than a row of its own,
-- because a product has at most one of each and the pair is read together.
--
-- # What may carry it
--
-- A draft or a published product: a draft may be scheduled to go live and to
-- leave again, and a published one to leave. An archived product has nothing
-- left to leave, and the update that archives a product clears the column in
-- the same statement.
--
-- # The order of the two moments
--
-- When both are set, the product leaves after it arrives. A pair the other way
-- round would publish an archived product or archive one that never went live,
-- and neither is a schedule anybody meant.
ALTER TABLE product
    ADD COLUMN archive_at timestamptz,
    ADD CONSTRAINT product_archive_at_live_only
        CHECK (archive_at IS NULL OR status IN ('draft', 'published')),
    ADD CONSTRAINT product_archive_after_publish
        CHECK (archive_at IS NULL OR publish_at IS NULL OR archive_at > publish_at);

-- The same pass that publishes asks for the products due to leave.
CREATE INDEX IF NOT EXISTS product_archive_due_idx
    ON product (archive_at) WHERE archive_at IS NOT NULL AND deleted_at IS NULL;
