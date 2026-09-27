ALTER TABLE webhook_endpoint
    DROP CONSTRAINT IF EXISTS webhook_endpoint_fields_object,
    DROP CONSTRAINT IF EXISTS webhook_endpoint_filters_object,
    DROP COLUMN IF EXISTS fields,
    DROP COLUMN IF EXISTS filters;
