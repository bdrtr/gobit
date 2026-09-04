-- Rollback of 000001_product_init.
--
-- The order is the REVERSE of the dependency: the dependent tables drop first,
-- then their owners.
-- CASCADE is deliberately not used; if the order is right it is unnecessary,
-- and CASCADE could one day drop another module's (mistakenly installed)
-- object as well.
DROP TABLE IF EXISTS product_category_map;
DROP TABLE IF EXISTS product_tag_map;
DROP TABLE IF EXISTS product_image;
DROP TABLE IF EXISTS product_variant_option_value;
DROP TABLE IF EXISTS product_option_value;
DROP TABLE IF EXISTS product_option;
DROP TABLE IF EXISTS product_variant;
DROP TABLE IF EXISTS product;
DROP TABLE IF EXISTS product_tag;
DROP TABLE IF EXISTS product_category;
DROP TABLE IF EXISTS product_collection;
