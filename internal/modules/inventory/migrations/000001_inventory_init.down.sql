-- 000001_inventory_init'in geri alınması.
--
-- Tablolar bağımlılık sırasının TERSİNDE düşürülür: önce inventory_items ve
-- stock_locations'a referans veren tablolar, sonra referans verilenler.
-- İndeksler tabloyla birlikte düşer, ayrıca DROP edilmez.

DROP TABLE IF EXISTS inventory_reservations;
DROP TABLE IF EXISTS inventory_levels;
DROP TABLE IF EXISTS inventory_items;
DROP TABLE IF EXISTS stock_locations;
