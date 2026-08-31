-- searchpg eklentisinin şeması: ürün arama indeksi.
--
-- Şema EKLENTİYE aittir ve sürüm defteri de ayrıdır
-- (searchpg_schema_migrations, bkz. core/db.MigrationsTable): eklenti
-- kaldırıldığında geriye yalnızca bu tablo kalır ve hiçbir modülün defterine
-- dokunulmaz.
--
-- Konvansiyonlar (plan Bölüm 8):
--   * Kimlik önekli metindir (prod_...) ve KATALOĞA aittir; burada üretilmez.
--   * Zaman UTC'dir.
--   * SOFT DELETE YOKTUR ve olmamalıdır: bu tablo bir kayıt değil, kataloğun
--     TÜRETİLMİŞ bir görünümüdür. Silinen bir ürünün indeks satırını saklamak,
--     aramanın var olmayan ürünü göstermesi demek olurdu; satır gerçekten
--     silinir ve gerektiğinde yeniden indekslemeyle geri gelir.

-- product_id'ye FOREIGN KEY VERİLMEZ (Prensip 2.2: cross-module FK yasak).
-- Kısıt konsaydı katalog tablosunu bir eklentiye bağlar, product'ı ayrı bir
-- servise çıkarmayı imkânsız kılar ve bir ürün silindiğinde indeksin
-- güncellenmesini veritabanı kısıtına devrederdi — oysa o iş "product.deleted"
-- olayının abonesine aittir.
CREATE TABLE IF NOT EXISTS searchpg_product (
    -- product_id kataloğun ürün kimliğidir; ürün başına TEK satır olur.
    product_id text PRIMARY KEY,
    -- document ağırlıklandırılmış arama belgesidir (A: başlık, B: handle,
    -- alt başlık, etiket, varyant ve SKU, C: açıklama). Belge Go'da değil
    -- SQL'de üretilir; bkz. plugins/searchpg/index.go upsertSQL.
    document tsvector NOT NULL,
    -- indexed_at satırın son yazıldığı andır. Tam yeniden indeksleme, turdan
    -- ESKİ kalan satırları bu sütuna bakarak süpürür.
    indexed_at timestamptz NOT NULL DEFAULT now()
);

-- GIN, tsvector eşleşmesinin indeks türüdür: GiST'e göre daha büyük ve daha
-- yavaş kurulur ama arama sorgusunda belirgin biçimde hızlıdır ve bu tablo
-- ürün başına en fazla birkaç kez yazılıp çok kez okunur.
CREATE INDEX IF NOT EXISTS searchpg_product_document_idx
    ON searchpg_product USING GIN (document);

-- Süpürme (DELETE ... WHERE indexed_at < $1) bu indeksi kullanır; tam tablo
-- taraması, katalog büyüdükçe her yeniden indekslemede ödenen bir maliyet
-- olurdu.
CREATE INDEX IF NOT EXISTS searchpg_product_indexed_at_idx
    ON searchpg_product (indexed_at);
