-- customer modülünün şeması (plan Faz 5, Bölüm 6).
--
-- Tablolar YALNIZCA bu modüle aittir. Prensip 2.2 gereği başka bir modülün
-- tablosuna REFERENCES verilmez: sepetin ya da siparişin müşteriye bağlanması
-- Module Links üzerinden yapılır ve customer o bağı hiç görmez. Modülün KENDİ
-- tabloları arasındaki foreign key'ler serbesttir ve kullanılır.
--
-- Zaman sütunları TIMESTAMPTZ'dir ve daima UTC yazılır; silme SOFT'tur
-- (deleted_at) ve tüm okuma sorguları deleted_at IS NULL filtresi uygular.

-- customer hem misafir hem kayıtlı müşteriyi tutar; ikisini has_account ayırır.
--
-- E-posta daima KÜÇÜK harfe normalize edilerek saklanır. Normalizasyon
-- saklamadadır çünkü benzersizlik indeksi ham sütun üzerindedir: "Ali@X.com"
-- ile "ali@x.com" aynı hesabı göstermeliyse ikisinin de aynı baytlara inmesi
-- gerekir. CHECK kısıtı, servis normalizasyonu atlansa bile büyük harfli bir
-- e-postanın tabloya girmesini engeller.
CREATE TABLE IF NOT EXISTS customer (
    id          TEXT PRIMARY KEY,
    email       TEXT        NOT NULL,
    first_name  TEXT        NOT NULL DEFAULT '',
    last_name   TEXT        NOT NULL DEFAULT '',
    phone       TEXT        NOT NULL DEFAULT '',
    has_account BOOLEAN     NOT NULL DEFAULT FALSE,
    metadata    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT customer_email_check       CHECK (email <> '' AND email = lower(email)),
    CONSTRAINT customer_email_shape_check CHECK (email ~ '^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$'),
    CONSTRAINT customer_email_len_check   CHECK (length(email) <= 320)
);

-- Kayıtlı HESAPLARIN e-postası benzersizdir; misafirlerinki DEĞİLDİR.
--
-- Kısmi indeks bu modülün en önemli kararıdır. Aynı e-postayla defalarca
-- misafir siparişi verilebilmelidir: vitrinde adresini yazan bir müşteri,
-- aylar önce aynı adresle alışveriş yapmış olduğu için reddedilemez. Buna
-- karşılık aynı e-postayla iki KAYITLI hesap olamaz, aksi hâlde Faz 8'de
-- gelecek "e-posta ile giriş" hangi kaydı seçeceğini bilemezdi. WHERE koşulu
-- iki gereksinimi tek kısıtta ifade eder ve kuralı uygulamaya değil
-- veritabanına bağlar.
--
-- deleted_at IS NULL koşulu ikinci bir işi görür: yumuşak silinmiş bir hesabın
-- e-postası yeniden kullanılabilir kalır.
CREATE UNIQUE INDEX IF NOT EXISTS customer_account_email_uniq
    ON customer (email)
    WHERE has_account AND deleted_at IS NULL;

-- E-postaya göre arama (GetCustomerByEmail, misafir eşleştirme) bu indeksi
-- kullanır; kısmi benzersiz indeks yalnızca hesapları kapsadığı için
-- misafir aramalarına yetmez.
CREATE INDEX IF NOT EXISTS customer_email_idx
    ON customer (email)
    WHERE deleted_at IS NULL;

-- customer_group müşteri segmentidir. pricing'in kural bağlamındaki
-- "customer_group_id" özniteliği buradaki kimliğe karşılık gelir; bağ
-- veritabanı düzeyinde DEĞİL, hesaplama bağlamı üzerinden kurulur.
CREATE TABLE IF NOT EXISTS customer_group (
    id         TEXT PRIMARY KEY,
    name       TEXT        NOT NULL,
    metadata   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT customer_group_name_check     CHECK (name <> ''),
    CONSTRAINT customer_group_name_len_check CHECK (length(name) <= 255)
);

-- Grup adı CANLI gruplar arasında benzersizdir.
--
-- WHERE koşulu yumuşak silmenin karşılığıdır: silinen bir grubun adı indeksin
-- kapsamından çıkar ve yeniden kullanılabilir hâle gelir. Koşul olmasaydı bir
-- kez kullanılmış her ad sonsuza dek işgal edilirdi — grup silinebildiği için
-- bu somut bir kısıttır (bkz. queries/customer_group.sql,
-- SoftDeleteCustomerGroup).
CREATE UNIQUE INDEX IF NOT EXISTS customer_group_name_uniq
    ON customer_group (name)
    WHERE deleted_at IS NULL;

-- customer_group_customer müşteri ile grup arasındaki ÇOKA-ÇOK bağdır.
--
-- Bileşik birincil anahtar aynı müşterinin aynı gruba iki kez eklenmesini
-- engeller; üyelik kümedir, çokluk taşımaz.
CREATE TABLE IF NOT EXISTS customer_group_customer (
    customer_id       TEXT        NOT NULL REFERENCES customer(id) ON DELETE CASCADE,
    customer_group_id TEXT        NOT NULL REFERENCES customer_group(id) ON DELETE CASCADE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, customer_group_id)
);

-- Bir grubun üyelerini listelemek birincil anahtarın ÖNEKİNİ kullanamaz;
-- ters yön için ayrı indeks gerekir.
CREATE INDEX IF NOT EXISTS customer_group_customer_group_idx
    ON customer_group_customer (customer_group_id);

-- customer_address müşterinin kayıtlı adresidir.
--
-- Adres customer'a foreign key ile bağlıdır: ikisi de bu modülün verisidir ve
-- sahipsiz bir adres kaydının anlamı yoktur.
CREATE TABLE IF NOT EXISTS customer_address (
    id                  TEXT        PRIMARY KEY,
    customer_id         TEXT        NOT NULL REFERENCES customer(id) ON DELETE CASCADE,
    first_name          TEXT        NOT NULL DEFAULT '',
    last_name           TEXT        NOT NULL DEFAULT '',
    company             TEXT        NOT NULL DEFAULT '',
    address_1           TEXT        NOT NULL,
    address_2           TEXT        NOT NULL DEFAULT '',
    city                TEXT        NOT NULL,
    country_code        TEXT        NOT NULL,
    postal_code         TEXT        NOT NULL DEFAULT '',
    phone               TEXT        NOT NULL DEFAULT '',
    is_default_shipping BOOLEAN     NOT NULL DEFAULT FALSE,
    is_default_billing  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ,
    CONSTRAINT customer_address_address1_check CHECK (address_1 <> ''),
    CONSTRAINT customer_address_city_check     CHECK (city <> ''),
    CONSTRAINT customer_address_country_check  CHECK (country_code ~ '^[A-Z]{2}$')
);

CREATE INDEX IF NOT EXISTS customer_address_customer_idx
    ON customer_address (customer_id)
    WHERE deleted_at IS NULL;

-- Varsayılan kargo/fatura adresi MÜŞTERİ BAŞINA TEKTİR ve bunu veritabanı
-- zorlar.
--
-- Kural uygulamada da tutulabilirdi ("yenisini yazmadan önce eskisini temizle")
-- ama iki eşzamanlı istek arasında tutmazdı: ikisi de eski varsayılanı temizler,
-- ikisi de kendi adresini işaretler ve müşteri iki varsayılan kargo adresiyle
-- kalırdı. Kısmi benzersiz indeks bu yarışı imkânsız kılar; ikinci yazım
-- benzersizlik ihlaliyle döner. Uygulama tarafındaki temizleme adımı hâlâ
-- gereklidir, ama artık DOĞRULUĞUN kaynağı değil, kısıtı sağlama yoludur.
CREATE UNIQUE INDEX IF NOT EXISTS customer_address_default_shipping_uniq
    ON customer_address (customer_id)
    WHERE is_default_shipping AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS customer_address_default_billing_uniq
    ON customer_address (customer_id)
    WHERE is_default_billing AND deleted_at IS NULL;
