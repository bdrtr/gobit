-- The table goes and takes the cancellations with it. What comes back is the
-- state before this migration: every bought unit is returnable again, because
-- the return ceiling reads these rows, and a rolled-back schema therefore allows
-- a return of goods somebody had already written off.
DROP INDEX IF EXISTS order_line_cancellations_line_idx;
DROP TABLE IF EXISTS order_line_cancellations;
