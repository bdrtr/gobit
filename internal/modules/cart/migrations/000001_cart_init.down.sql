-- 000001_cart_init'in geri alınması.
--
-- Tablolar bağımlılık sırasının TERSİNDE düşürülür: önce carts'a referans veren
-- tablolar, sonra carts. İndeksler tabloyla birlikte düşer, ayrıca DROP edilmez.

DROP TABLE IF EXISTS cart_shipping_methods;
DROP TABLE IF EXISTS cart_addresses;
DROP TABLE IF EXISTS cart_line_items;
DROP TABLE IF EXISTS carts;
