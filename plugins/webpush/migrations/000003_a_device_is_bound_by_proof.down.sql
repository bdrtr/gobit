-- Rolling back the move of unproven bindings (ADR 0440).
--
-- The bindings the forward step moved are not put back: a binding the
-- storefront claimed is what the forward step exists to stop believing, and the
-- older schema needs nothing restored to run. A device bound since keeps the
-- customer its subscription proved.
ALTER TABLE webpush_subscription DROP COLUMN IF EXISTS unproven_customer_id;
