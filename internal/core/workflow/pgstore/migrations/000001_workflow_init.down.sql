-- Rolls the schema above back completely (plan Section 8: up/down pairs).
--
-- The order matters: the steps table is bound to the executions by an FK, so it
-- goes first. The indexes drop with the table on their own; they are still
-- written out explicitly so they can be rolled back one by one when the table
-- itself is to be kept by hand.
DROP TABLE IF EXISTS workflow_execution_steps;

DROP INDEX IF EXISTS workflow_executions_workflow_created_at_idx;
DROP INDEX IF EXISTS workflow_executions_idempotency_key_uniq;

DROP TABLE IF EXISTS workflow_executions;
