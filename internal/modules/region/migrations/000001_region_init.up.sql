-- region modülünün şeması (plan Faz 5, Bölüm 6).
--
-- Tablolar YALNIZCA bu modüle aittir. Prensip 2.2 gereği başka bir modülün
-- tablosuna REFERENCES verilmez; sepetin ya da siparişin bir bölgeye bağlanması
-- Module Links üzerinden yapılır ve region o bağı hiç görmez. Modülün KENDİ
-- tabloları arasındaki foreign key'ler serbesttir ve kullanılır.
--
-- Zaman sütunları TIMESTAMPTZ'dir ve daima UTC yazılır; silme SOFT'tur
-- (deleted_at) ve tüm okuma sorguları deleted_at IS NULL filtresi uygular.

-- currency ISO 4217 para birimidir ve REFERANS VERİDİR.
--
-- Birincil anahtar üretilmiş bir kimlik değil, kodun KENDİSİDİR: ISO 4217 kodu
-- küresel ve değişmez bir tanımlayıcıdır, ona ikinci bir kimlik uydurmak her
-- okumada fazladan bir çeviri adımı getirirdi. Kod daima BÜYÜK harf saklanır;
-- normalleştirmeyi servis yapar, CHECK ikinci kapıdır.
--
-- decimal_digits bu tablonun VAROLUŞ SEBEBİDİR: para minor unit (kuruş/cent)
-- tam sayı olarak saklandığı için (plan Bölüm 8), sunum katmanı bölme
-- çarpanını buradan öğrenir. TRY/USD 2, JPY 0, KWD 3 basamaklıdır; sabit bir
-- 100 çarpanı varsaymak yen tutarlarını yüz kat küçük gösterirdi.
CREATE TABLE IF NOT EXISTS currency (
    code           TEXT PRIMARY KEY,
    symbol         TEXT        NOT NULL,
    name           TEXT        NOT NULL,
    decimal_digits INTEGER     NOT NULL DEFAULT 2,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,
    CONSTRAINT currency_code_check   CHECK (code ~ '^[A-Z]{3}$'),
    CONSTRAINT currency_name_check   CHECK (name <> ''),
    CONSTRAINT currency_symbol_check CHECK (symbol <> ''),
    CONSTRAINT currency_digits_check CHECK (decimal_digits >= 0 AND decimal_digits <= 4)
);

-- region bir satış bölgesidir: para birimi ve (geçici olarak) vergi oranı.
--
-- currency_code foreign key'i modül İÇİNDEDİR ve bilinçlidir: tanımsız bir para
-- biriminde bölge açmak, o bölgeye düşen her sepetin para birimini çözümsüz
-- bırakırdı. Silme kısıtlıdır (varsayılan NO ACTION): kullanımdaki bir para
-- birimi satırı silinemez.
--
-- tax_rate YEDEK orandır (tax modülü devraldı, bu geri düşüş yolu) ve BAZ PUAN olarak
-- saklanır: 2000 = %20. Oranın tam sayı olması bilinçlidir — plan Bölüm 8 para
-- ve türevlerinde float yasaklar, ve %20'lik bir oranın float karşılığı
-- (0.2) tutarla çarpıldığında kuruş düzeyinde sessiz yuvarlama üretirdi.
CREATE TABLE IF NOT EXISTS region (
    id              TEXT PRIMARY KEY,
    name            TEXT        NOT NULL,
    currency_code   TEXT        NOT NULL,
    automatic_taxes BOOLEAN     NOT NULL DEFAULT TRUE,
    tax_rate        INTEGER     NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,
    CONSTRAINT region_name_check     CHECK (name <> ''),
    CONSTRAINT region_tax_rate_check CHECK (tax_rate >= 0 AND tax_rate <= 10000),
    CONSTRAINT region_currency_fk    FOREIGN KEY (currency_code) REFERENCES currency (code)
);

CREATE INDEX IF NOT EXISTS region_currency_code_idx ON region (currency_code) WHERE deleted_at IS NULL;

-- country ISO 3166-1 alpha-2 ülkesidir ve REFERANS VERİDİR.
--
-- Bir ülkenin EN FAZLA bir bölgeye ait olması kuralı YAPISALDIR: bağ, ülke
-- satırındaki tek bir region_id sütunudur. Ara tablo kullanılsaydı aynı kuralı
-- ayrıca bir benzersiz indeksle zorlamak gerekirdi; tek sütun onu şemanın
-- kendisiyle imkânsız kılar.
--
-- region_id NULL olabilir: ISO 3166'nın tamamı tohumlanır ama bir kurulum
-- bunların yalnızca birkaçına satış yapar. Bölge silindiğinde sütun NULL'a
-- çekilir (bkz. repository.DeleteRegion); aksi hâlde ülke ölü bir bölgeye
-- bağlı kalır ve bir daha hiçbir bölgeye eklenemezdi.
CREATE TABLE IF NOT EXISTS country (
    iso_2      TEXT PRIMARY KEY,
    name       TEXT        NOT NULL,
    region_id  TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT country_iso_2_check CHECK (iso_2 ~ '^[A-Z]{2}$'),
    CONSTRAINT country_name_check  CHECK (name <> ''),
    CONSTRAINT country_region_fk   FOREIGN KEY (region_id) REFERENCES region (id)
);

CREATE INDEX IF NOT EXISTS country_region_id_idx ON country (region_id) WHERE deleted_at IS NULL;
