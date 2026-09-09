-- The rollback dissolves every stack and takes the two columns out.
--
-- The rates themselves STAY: each one is a real rate a shop configured, and the
-- older schema can hold all of them — what it cannot hold is the relation
-- between them. Dissolving the relation leaves rates that apply on their own,
-- which is the older shape's honest reading of the same configuration.
--
-- What this cannot do is put back the tax a stacked line would have been
-- charged. That is not a loss: a placed order stores the tax it was charged on
-- its own line and reads nothing from here.
ALTER TABLE tax_rate DROP CONSTRAINT IF EXISTS tax_rate_stacks_on_fk;
ALTER TABLE tax_rate DROP CONSTRAINT IF EXISTS tax_rate_compound_check;
ALTER TABLE tax_rate DROP CONSTRAINT IF EXISTS tax_rate_stacks_on_self_check;

DROP INDEX IF EXISTS tax_rate_stacks_on_uniq;

ALTER TABLE tax_rate
    DROP COLUMN IF EXISTS compound,
    DROP COLUMN IF EXISTS stacks_on_id;

ALTER TABLE tax_rate DROP CONSTRAINT IF EXISTS tax_rate_id_region_uniq;
