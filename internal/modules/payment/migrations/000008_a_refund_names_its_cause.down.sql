ALTER TABLE refunds DROP CONSTRAINT IF EXISTS refunds_reference_trimmed;
ALTER TABLE refunds DROP COLUMN IF EXISTS reference;
