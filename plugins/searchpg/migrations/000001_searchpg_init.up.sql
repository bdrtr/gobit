-- The searchpg plugin's schema: the product search index.
--
-- The schema belongs to the PLUGIN and its version ledger is separate too
-- (searchpg_schema_migrations, see core/db.MigrationsTable): when the plugin is
-- removed, what stays behind is only this table and no module's ledger is
-- touched.
--
-- The conventions (plan Section 8):
--   * The id is prefixed text (prod_...) and belongs to the CATALOG; it is not
--     produced here.
--   * Time is UTC.
--   * There is NO SOFT DELETE and there must not be: this table is not a record
--     but a DERIVED view of the catalog. Keeping the index row of a deleted
--     product would mean search showing a product that does not exist; the row is
--     really deleted and comes back through a reindex when it is needed.

-- There is NO FOREIGN KEY on product_id (Principle 2.2: cross-module FKs are
-- forbidden). A constraint here would bind the catalog table to a plugin, make
-- it impossible to move product into a separate service, and hand the job of
-- updating the index on a product deletion to a database constraint — while that
-- job belongs to the subscriber of the "product.deleted" event.
CREATE TABLE IF NOT EXISTS searchpg_product (
    -- product_id is the catalog's product id; there is ONE row per product.
    product_id text PRIMARY KEY,
    -- document is the weighted search document (A: title, B: handle, subtitle,
    -- tags, variants and SKUs, C: description). The document is produced in SQL
    -- rather than in Go; see upsertSQL in plugins/searchpg/index.go.
    document tsvector NOT NULL,
    -- indexed_at is when the row was last written. A full reindex sweeps the rows
    -- left OLDER than its round by looking at this column.
    indexed_at timestamptz NOT NULL DEFAULT now()
);

-- GIN is the index type for a tsvector match: it is larger than GiST and slower
-- to build, but markedly faster on the search query — and this table is written
-- at most a few times per product and read many times.
CREATE INDEX IF NOT EXISTS searchpg_product_document_idx
    ON searchpg_product USING GIN (document);

-- The sweep (DELETE ... WHERE indexed_at < $1) uses this index; a full table scan
-- would be a cost paid on every reindex, growing with the catalog.
CREATE INDEX IF NOT EXISTS searchpg_product_indexed_at_idx
    ON searchpg_product (indexed_at);
