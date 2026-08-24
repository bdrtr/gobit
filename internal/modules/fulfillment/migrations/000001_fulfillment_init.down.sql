-- 000001_fulfillment_init'in geri alınması.
--
-- Tablolar bağımlılık sırasının TERSİNDE düşürülür: önce referans verenler
-- (fulfillment_items -> fulfillments -> shipping_option_rules ->
-- shipping_options), sonra referans verilen (shipping_profiles). İndeksler
-- tabloyla birlikte düşer, ayrıca DROP edilmez.
--
-- fulfillment_manual_shipments hiçbir tabloya bağlı değildir; sağlayıcının
-- kendi defteridir ve sırası önemsizdir.

DROP TABLE IF EXISTS fulfillment_manual_shipments;
DROP TABLE IF EXISTS fulfillment_items;
DROP TABLE IF EXISTS fulfillments;
DROP TABLE IF EXISTS shipping_option_rules;
DROP TABLE IF EXISTS shipping_options;
DROP TABLE IF EXISTS shipping_profiles;
