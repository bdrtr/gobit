-- A catalog can be imported (ADR 0205).
--
-- An import is a CSV file in the export's columns (ADR 0204), kept until a job
-- has worked through it. The job applies one row at a time and moves
-- rows_done after each, so a job cut off in the middle resumes where it
-- stopped; a row applied twice finds what it made the first time, because a
-- row is matched by the product's id or handle and the variant's id, SKU or
-- options (the service's rule).
--
-- The file is dropped when the import ends. What stays is what an operator
-- asks afterwards: how many rows, what they did, and which failed and why.
CREATE TABLE IF NOT EXISTS product_import (
    id           TEXT        PRIMARY KEY,
    status       TEXT        NOT NULL DEFAULT 'pending',
    file         BYTEA,
    rows_total   INTEGER     NOT NULL,
    rows_done    INTEGER     NOT NULL DEFAULT 0,
    rows_created INTEGER     NOT NULL DEFAULT 0,
    rows_updated INTEGER     NOT NULL DEFAULT 0,
    rows_failed  INTEGER     NOT NULL DEFAULT 0,
    -- errors is the first rows that failed, each {row, message}; the count of
    -- all of them is rows_failed.
    errors       JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,

    CONSTRAINT product_import_status_valid
        CHECK (status IN ('pending', 'running', 'completed')),
    CONSTRAINT product_import_rows_counted
        CHECK (rows_total >= 0 AND rows_done BETWEEN 0 AND rows_total
               AND rows_created >= 0 AND rows_updated >= 0 AND rows_failed >= 0
               AND rows_created + rows_updated + rows_failed <= rows_done),
    -- The file is kept exactly while the import has rows left to apply.
    CONSTRAINT product_import_file_while_open
        CHECK ((status = 'completed') = (file IS NULL)),
    CONSTRAINT product_import_finished_when_completed
        CHECK ((status = 'completed') = (finished_at IS NOT NULL))
);

-- The job takes the oldest import with rows left.
CREATE INDEX IF NOT EXISTS product_import_open_idx
    ON product_import (created_at, id)
    WHERE status <> 'completed';
