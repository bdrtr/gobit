-- The table goes and takes the bindings with it. The FILES are untouched: they
-- belong to the file module, which knows nothing about this table, so a
-- rolled-back schema leaves every upload where it was and only forgets which
-- claim it was evidence of.
DROP INDEX IF EXISTS order_claim_evidence_claim_idx;
DROP TABLE IF EXISTS order_claim_evidence;
