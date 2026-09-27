-- A product keeps its revisions (ADR 0221).
--
-- product.version counts the revisions a product has; 0 is a product written
-- before this migration and not since. product_revision holds, per version, the
-- product's admin view as it stood after the write that made it: its own
-- fields, variants, options, images, tags, categories and attribute values,
-- without their timestamps. changed names the top-level fields that differ from
-- the revision before; the first revision names none.
--
-- # Why the history starts at the first write, not here
--
-- The admin view is assembled by the service from eight reads, and a migration
-- cannot build it. A product written before this migration gets its first
-- revision, the view before the write, from the first write after it.
--
-- # Append only
--
-- A revision is inserted and never updated or deleted; an arch gate reads the
-- query files to hold that. It outlives a deleted product, whose row is soft
-- deleted and still answers the foreign key; a product row removed outright
-- takes its revisions with it, as it takes every other row of its own.
--
-- request_id is the HTTP request that made the revision, the key the admin
-- audit log records its caller under; a revision a job made has none.
ALTER TABLE product ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 0;

ALTER TABLE product DROP CONSTRAINT IF EXISTS product_version_not_negative;
ALTER TABLE product ADD CONSTRAINT product_version_not_negative CHECK (version >= 0);

CREATE TABLE IF NOT EXISTS product_revision (
    id          TEXT        PRIMARY KEY,
    product_id  TEXT        NOT NULL REFERENCES product (id) ON DELETE CASCADE,
    version     BIGINT      NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    changed     TEXT[]      NOT NULL,
    request_id  TEXT        NULL,
    snapshot    JSONB       NOT NULL,
    CONSTRAINT product_revision_version_positive CHECK (version > 0),
    CONSTRAINT product_revision_snapshot_is_object CHECK (jsonb_typeof(snapshot) = 'object'),
    CONSTRAINT product_revision_request_not_blank CHECK (request_id IS NULL OR btrim(request_id) <> '')
);

CREATE UNIQUE INDEX IF NOT EXISTS product_revision_version_key
    ON product_revision (product_id, version);
