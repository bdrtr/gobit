-- b2b modülünün şeması.
--
-- Tablolar YALNIZCA bu modüle aittir ve adları "b2b_" önekiyle başlar. Prensip
-- 2.2 gereği başka bir modülün tablosuna REFERENCES verilmez: çalışanın MÜŞTERİ
-- kaydına bağlanması core/link ile yapılır ve bu şemada customer_id diye bir
-- sütun BULUNMAZ (gerekçe: internal/modules/b2b/service, Definitions). Modülün
-- KENDİ tabloları arasındaki foreign key serbesttir ve b2b_company_employee ->
-- b2b_company bağında kullanılır.
--
-- Zaman sütunları TIMESTAMPTZ'dir ve daima UTC yazılır; silme SOFT'tur
-- (deleted_at) ve tüm okuma sorguları deleted_at IS NULL süzer. Para TAM SAYI
-- minor unit'tir (plan Bölüm 8).

-- b2b_company alışverişi bir birey adına değil bir TÜZEL KİŞİ adına yapan
-- şirkettir.
--
-- E-posta KÜÇÜK harfe normalize edilerek saklanır (CHECK ile zorlanır) ama
-- BENZERSİZ DEĞİLDİR. Benzersizlik bilinçli olarak konmadı: bu modülde
-- e-postayla bir kimlik kurulmaz (giriş customer/auth tarafındadır), buna
-- karşılık aynı holdingin iki tüzel kişisi pekâlâ aynı muhasebe adresini
-- paylaşır. Benzersiz bir indeks, var olmayan bir kimlik uğruna gerçek bir
-- kaydı reddederdi. Bunun bedeli, e-posta süzgecinin birden çok satır
-- döndürebilmesidir ve bu, süzgecin sözleşmesinde yazılıdır.
--
-- Adres alanları BOŞ BIRAKILABİLİR: bir şirket kaydı çoğu zaman fatura adresi
-- kesinleşmeden önce açılır. Para birimi ise zorunludur — harcama limiti
-- (b2b_company_employee.spending_limit) bir tam sayıdır ve hangi para
-- biriminde olduğu bilinmeden karşılaştırılamaz.
CREATE TABLE IF NOT EXISTS b2b_company (
    id                            TEXT PRIMARY KEY,
    name                          TEXT        NOT NULL,
    email                         TEXT        NOT NULL,
    phone                         TEXT        NOT NULL DEFAULT '',
    address                       TEXT        NOT NULL DEFAULT '',
    city                          TEXT        NOT NULL DEFAULT '',
    postal_code                   TEXT        NOT NULL DEFAULT '',
    country_code                  TEXT        NOT NULL DEFAULT '',
    currency_code                 TEXT        NOT NULL,
    spending_limit_reset_period   TEXT        NOT NULL DEFAULT 'never',
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at                    TIMESTAMPTZ,
    CONSTRAINT b2b_company_name_check     CHECK (name <> '' AND length(name) <= 255),
    CONSTRAINT b2b_company_email_check    CHECK (email <> '' AND email = lower(email) AND length(email) <= 320),
    CONSTRAINT b2b_company_currency_check CHECK (currency_code ~ '^[A-Z]{3}$'),
    -- Boş ülke kodu "adres henüz girilmedi" demektir; girildiyse ISO 3166-1
    -- alpha-2 olmak ZORUNDADIR. İki durumu tek kısıtta ifade etmek, "adres
    -- opsiyonel" kararının veritabanındaki karşılığıdır.
    CONSTRAINT b2b_company_country_check  CHECK (country_code = '' OR country_code ~ '^[A-Z]{2}$'),
    -- Sıfırlama periyodu bir ENUM'dur ve değer kümesi ŞEMADA durur. Uygulama
    -- katmanı da doğrular; ama harcama limitini uygulayacak olan bir sonraki
    -- adım bu sütunu okuyup dallanacak ve tanımadığı bir değer görmesi
    -- "limit hiç uygulanmadı" demek olurdu.
    CONSTRAINT b2b_company_reset_check    CHECK (spending_limit_reset_period IN ('monthly', 'yearly', 'never'))
);

-- E-postaya göre süzme (yönetim listesi) bu indeksi kullanır. Benzersiz
-- DEĞİLDİR; gerekçesi tablonun belgesindedir.
CREATE INDEX IF NOT EXISTS b2b_company_email_idx
    ON b2b_company (email)
    WHERE deleted_at IS NULL;

-- b2b_company_employee şirket adına harcama yapabilen çalışandır.
--
-- Çalışanın MÜŞTERİ kaydına bağı burada DEĞİL, "b2b_employee_customer"
-- linkindedir (core/link). Bir customer_id sütunu, aynı ilişkiyi iki yerde
-- tutmak demek olurdu: sütun ile link arasındaki her ayrışma, vitrinde
-- "kendi çalışan kaydım" sorusuna iki farklı cevap üretirdi. Tek kaynak
-- link tablosudur ve çalışan kaydını müşteriden bulan tek yol odur.
--
-- spending_limit NULL ise çalışan SINIRSIZ harcayabilir; 0 ise hiç
-- harcayamaz. İkisini ayırmak şarttır — tek bir sıfır değeri kullanılsaydı
-- "limit koymadım" ile "limiti sıfırladım" aynı satıra düşerdi.
CREATE TABLE IF NOT EXISTS b2b_company_employee (
    id               TEXT        PRIMARY KEY,
    company_id       TEXT        NOT NULL REFERENCES b2b_company(id) ON DELETE CASCADE,
    spending_limit   BIGINT,
    is_company_admin BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ,
    -- Negatif limit bir sınır değil, anlamsız bir sayıdır: her karşılaştırma
    -- onu aşardı ve çalışan sessizce hiç alışveriş yapamaz hâle gelirdi.
    CONSTRAINT b2b_company_employee_limit_check CHECK (spending_limit IS NULL OR spending_limit >= 0)
);

-- Bir şirketin çalışanlarını listelemek en sık yapılan okumadır; company_id
-- indekssiz kalırsa her liste tablo taramasına düşer.
CREATE INDEX IF NOT EXISTS b2b_company_employee_company_idx
    ON b2b_company_employee (company_id)
    WHERE deleted_at IS NULL;
