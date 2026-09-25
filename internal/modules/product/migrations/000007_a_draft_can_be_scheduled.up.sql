-- product gains the moment a draft is to be published (ADR 0177).
--
-- # Why a moment and not a status
--
-- A scheduled product IS a draft until the moment: nothing the storefront, the
-- search index or a webhook reads should treat it differently from any other
-- draft. A fourth status would put a new word in front of every reader of the
-- column, each of which already answers "is this visible" with status alone.
--
-- # Why only a draft may carry one
--
-- The column means "publish this draft then". On a published or archived product
-- it would mean nothing, and a value that means nothing is one a later reader
-- takes for a fact. The constraint keeps the pair honest; the update that
-- changes a product's status clears the column in the same statement, so
-- publishing or archiving a scheduled draft by hand does not trip it.
ALTER TABLE product
    ADD COLUMN publish_at timestamptz,
    ADD CONSTRAINT product_publish_at_draft_only
        CHECK (publish_at IS NULL OR status = 'draft');

-- The job asks, once a minute, for the drafts whose moment has come. The index
-- holds only the scheduled rows, which are few; the rest of the table is never
-- in it.
CREATE INDEX IF NOT EXISTS product_publish_due_idx
    ON product (publish_at) WHERE publish_at IS NOT NULL AND deleted_at IS NULL;
