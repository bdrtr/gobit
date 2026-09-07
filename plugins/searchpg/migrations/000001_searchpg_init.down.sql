-- Rolling the searchpg schema back.
--
-- The indexes fall with the table; they are still dropped EXPLICITLY, because
-- the namespace is shared with the tables and if one of the indexes were moved
-- to another table one day, this file would quietly be incomplete.
--
-- Data loss is ACCEPTED here and is harmless: the table is a derived view of the
-- catalog, its source is in the product module, and all of it can be rebuilt
-- with POST /admin/v1/search/reindex.
DROP INDEX IF EXISTS searchpg_product_indexed_at_idx;
DROP INDEX IF EXISTS searchpg_product_document_idx;
DROP TABLE IF EXISTS searchpg_product;
