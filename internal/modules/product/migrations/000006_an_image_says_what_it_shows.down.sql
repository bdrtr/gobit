-- The column goes and takes the alt texts with it. Nothing else in this module
-- reads the field, so a rolled-back schema serves and lists exactly as it did.
ALTER TABLE product_image
    DROP COLUMN IF EXISTS alt_text;
