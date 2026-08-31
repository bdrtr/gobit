-- searchpg şemasının geri alınması.
--
-- İndeksler tabloyla birlikte düşer; yine de AÇIKÇA silinir çünkü ad uzayı
-- tablolarla paylaşılır ve bir gün indekslerden biri başka bir tabloya
-- taşınırsa bu dosya sessizce eksik kalırdı.
--
-- Veri kaybı burada KABUL EDİLİR ve tehlikesizdir: tablo kataloğun türetilmiş
-- bir görünümüdür, kaynağı product modülündedir ve tamamı
-- POST /admin/v1/search/reindex ile yeniden kurulabilir.
DROP INDEX IF EXISTS searchpg_product_indexed_at_idx;
DROP INDEX IF EXISTS searchpg_product_document_idx;
DROP TABLE IF EXISTS searchpg_product;
