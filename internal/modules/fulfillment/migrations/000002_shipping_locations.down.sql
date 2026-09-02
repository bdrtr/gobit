-- 000002_shipping_locations'ın geri alınması.
--
-- Tablolar bağımlılık sırasının TERSİNDE düşürülür: önce referans veren
-- (shipping_location_regions), sonra referans verilen (shipping_locations).
-- İndeksler tabloyla birlikte düşer, ayrıca DROP edilmez.
--
-- Geri alma politikanın VERİSİNİ de siler ve bu kaçınılmazdır: veri yalnızca
-- bu iki tabloda durur. Şemayı 000001'deki hâline döndürmek, seçimi de o
-- günkü kuralına ("kimliği en küçük aday") döndürür — kod tarafında geri
-- düşüş zaten aynı yolu izler, çünkü politika satırı olmayan depo varsayılan
-- öncelikte ve tüm bölgelere hizmet ediyor sayılır.

DROP TABLE IF EXISTS shipping_location_regions;
DROP TABLE IF EXISTS shipping_locations;
