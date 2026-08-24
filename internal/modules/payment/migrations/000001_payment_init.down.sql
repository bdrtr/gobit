-- 000001_payment_init'in geri alınması.
--
-- Tablolar bağımlılık sırasının TERSİNDE düşürülür: önce referans verenler
-- (refunds -> payments -> payment_sessions), sonra referans verilen
-- (payment_collections). İndeksler tabloyla birlikte düşer, ayrıca DROP
-- edilmez.
--
-- payment_manual_sessions hiçbir tabloya bağlı değildir; sağlayıcının kendi
-- defteridir ve sırası önemsizdir.

DROP TABLE IF EXISTS payment_manual_sessions;
DROP TABLE IF EXISTS refunds;
DROP TABLE IF EXISTS payments;
DROP TABLE IF EXISTS payment_sessions;
DROP TABLE IF EXISTS payment_collections;
