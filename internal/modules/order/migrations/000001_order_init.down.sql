-- 000001_order_init'in geri alınması.
--
-- Tablolar bağımlılık sırasının TERSİNDE düşürülür: önce orders'a referans
-- veren tablolar, sonra orders. İndeksler tabloyla birlikte düşer, ayrıca DROP
-- edilmez. display_id'nin IDENTITY sequence'ı da sütuna ait olduğu için
-- orders ile birlikte düşer; ayrı bir DROP SEQUENCE gerekmez.

DROP TABLE IF EXISTS order_claims;
DROP TABLE IF EXISTS order_exchanges;
DROP TABLE IF EXISTS order_returns;
DROP TABLE IF EXISTS order_summaries;
DROP TABLE IF EXISTS order_line_items;
DROP TABLE IF EXISTS orders;
