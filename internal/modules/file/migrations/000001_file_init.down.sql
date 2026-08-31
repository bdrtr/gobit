-- 000001_file_init'in geri alınması.
--
-- Tek tablo vardır ve hiçbir tabloya bağlı değildir; indeksler tabloyla
-- birlikte düşer, ayrıca DROP edilmez.
--
-- DİKKAT: Bu, DEPODAKİ DOSYALARI silmez. Migration veritabanını geri alır;
-- yüklenen baytlar kök dizinde (ya da nesne deposunda) kalır. Kasıtlıdır —
-- bir şema geri alması, geri alınamayacak bir veri silmeyi tetiklememelidir.
-- Dosyaların temizliği, kayıtları hâlâ okunabilirken yapılmalıdır.

DROP TABLE IF EXISTS file_uploads;
