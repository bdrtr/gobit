DROP INDEX IF EXISTS fulfillments_return_idx;
ALTER TABLE fulfillments DROP CONSTRAINT IF EXISTS fulfillments_return_id_check;
ALTER TABLE fulfillments DROP COLUMN IF EXISTS return_id;
