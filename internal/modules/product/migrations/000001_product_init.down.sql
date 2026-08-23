-- 000001_product_init'in geri alınması.
--
-- Sıra bağımlılığın TERSİDİR: önce bağımlı tablolar, sonra sahipleri düşer.
-- CASCADE bilinçli olarak kullanılmaz; sıra doğruysa gerek yoktur ve CASCADE
-- bir gün başka bir modülün (yanlışlıkla kurulmuş) nesnesini de düşürebilirdi.
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
