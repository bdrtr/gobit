-- A shipping option says how many business days its delivery takes (ADR 0421).
--
-- The days are the carrier's working days, a minimum and a maximum, set
-- together or not at all; gobit turns no day count into a date, since it holds
-- no shop time zone or calendar (ADR 0399), and the storefront renders them.
--
-- They are on the option, not on a warehouse's bond to a region: a bond is a
-- coverage constraint (000002), and a warehouse with no bond serves every
-- region, so a time written on a bond would edit coverage. An option is already
-- a carrier's service in a region, which is where a standard and an express
-- service differ.
--
-- An option written before this migration carries no days and is listed
-- without them.
ALTER TABLE shipping_options
    ADD COLUMN IF NOT EXISTS delivery_min_days INTEGER,
    ADD COLUMN IF NOT EXISTS delivery_max_days INTEGER;

ALTER TABLE shipping_options
    ADD CONSTRAINT shipping_options_delivery_days_paired
        CHECK ((delivery_min_days IS NULL) = (delivery_max_days IS NULL)),
    ADD CONSTRAINT shipping_options_delivery_days_range
        CHECK (delivery_min_days IS NULL
               OR (delivery_min_days >= 0
                   AND delivery_min_days <= delivery_max_days
                   AND delivery_max_days <= 365));
