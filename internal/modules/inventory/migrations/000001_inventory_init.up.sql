-- inventory modülünün şeması (plan Faz 4).
--
-- Sahiplik: bu dosyadaki dört tablo YALNIZCA inventory modülüne aittir.
-- Modül içi foreign key'ler serbesttir ve kullanılır; başka bir modülün
-- tablosuna REFERENCES verilmez (Prensip 2.2 — cross-module FK yasağı).
-- Bu yüzden inventory_reservations.line_item_id (cart modülünün satırı) ve
-- inventory_items ile ürün varyantı arasındaki bağ FK DEĞİLDİR: ikincisi
-- Module Links üzerinden kurulur.
--
-- Para birimi yoktur; bu modül yalnızca ADET taşır ve adetler BIGINT'tir.
-- Zaman: tüm damgalar timestamptz (UTC). Silme yumuşaktır (deleted_at) ve
-- tüm okuma sorguları deleted_at IS NULL filtresi uygular.
--
-- BİR İSTİSNA VARDIR ve bu dosyadan sonra eklenmiştir: 000002,
-- inventory_reservations'ın deleted_at sütununu DÜŞÜRÜR. O sütunu hiçbir zaman
-- hiçbir şey yazmadı; bir rezervasyon silinmez, durumu değişir (aşağıdaki
-- tablo yorumunun kendisi bunu söyler). Gerekçe 000002'nin başındadır, yani
-- aşağıdaki CREATE TABLE o tablo için TARİHTİR, güncel şema değil.

-- stock_locations stoğun fiziksel olarak durduğu yerdir (depo, mağaza).
CREATE TABLE IF NOT EXISTS stock_locations (
    id           TEXT        PRIMARY KEY,
    name         TEXT        NOT NULL,
    address_1    TEXT,
    address_2    TEXT,
    city         TEXT,
    province     TEXT,
    postal_code  TEXT,
    country_code TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS stock_locations_alive_idx
    ON stock_locations (created_at DESC)
    WHERE deleted_at IS NULL;

-- inventory_items stok takibi yapılan kalemdir. Ürün varyantı ile bağı
-- "product_variant_inventory" link'i üzerinden kurulur; bu modül product'ı
-- bilmez.
CREATE TABLE IF NOT EXISTS inventory_items (
    id                TEXT        PRIMARY KEY,
    sku               TEXT        NOT NULL,
    title             TEXT,
    description       TEXT,
    requires_shipping BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ
);

-- SKU yalnızca YAŞAYAN kalemler arasında benzersizdir; silinen bir kalemin
-- SKU'su yeniden kullanılabilir.
CREATE UNIQUE INDEX IF NOT EXISTS inventory_items_sku_uniq
    ON inventory_items (sku)
    WHERE deleted_at IS NULL;

-- inventory_levels bir kalemin bir lokasyondaki stok durumudur.
--
-- available (satılabilir adet) SAKLANMAZ, stocked_quantity - reserved_quantity
-- olarak TÜRETİLİR. Türetilmiş değerin saklanması, iki sütunun birbirinden
-- ayrı düşebileceği bir tutarsızlık kaynağı olurdu; kısıt da bu yüzden
-- türetme üzerine kurulur.
CREATE TABLE IF NOT EXISTS inventory_levels (
    id                TEXT        PRIMARY KEY,
    inventory_item_id TEXT        NOT NULL REFERENCES inventory_items (id) ON DELETE CASCADE,
    location_id       TEXT        NOT NULL REFERENCES stock_locations (id) ON DELETE CASCADE,
    stocked_quantity  BIGINT      NOT NULL DEFAULT 0,
    reserved_quantity BIGINT      NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,

    CONSTRAINT inventory_levels_stocked_nonneg  CHECK (stocked_quantity >= 0),
    CONSTRAINT inventory_levels_reserved_nonneg CHECK (reserved_quantity >= 0),
    -- Satılabilir adet negatife DÜŞEMEZ. Servis katmanı bunu zaten reddeder;
    -- buradaki kısıt son savunmadır: doğrudan SQL ile yapılan bir müdahale de
    -- stoğu negatife düşüremez.
    CONSTRAINT inventory_levels_available_nonneg CHECK (reserved_quantity <= stocked_quantity)
);

-- (kalem, lokasyon) çifti YAŞAYAN satırlar arasında tektir.
CREATE UNIQUE INDEX IF NOT EXISTS inventory_levels_item_location_uniq
    ON inventory_levels (inventory_item_id, location_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS inventory_levels_location_idx
    ON inventory_levels (location_id)
    WHERE deleted_at IS NULL;

-- inventory_reservations satılabilir stoktan ayrılmış adetlerdir.
--
-- Durum makinesi: active -> released | confirmed. Kayıt SİLİNMEZ; telafinin
-- (ReleaseReservation) idempotent olabilmesi kaydın durumunun okunabilir
-- kalmasına bağlıdır — silinmiş bir rezervasyon ile hiç var olmamış bir
-- rezervasyon birbirinden ayırt edilemezdi.
CREATE TABLE IF NOT EXISTS inventory_reservations (
    id                TEXT        PRIMARY KEY,
    inventory_item_id TEXT        NOT NULL REFERENCES inventory_items (id) ON DELETE CASCADE,
    location_id       TEXT        NOT NULL REFERENCES stock_locations (id) ON DELETE CASCADE,
    quantity          BIGINT      NOT NULL,
    -- line_item_id cart modülünün satır kimliğidir. FK YOKTUR (Prensip 2.2).
    line_item_id      TEXT,
    status            TEXT        NOT NULL DEFAULT 'active',
    description       TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,

    CONSTRAINT inventory_reservations_quantity_positive CHECK (quantity > 0),
    CONSTRAINT inventory_reservations_status_valid
        CHECK (status IN ('active', 'released', 'confirmed'))
);

CREATE INDEX IF NOT EXISTS inventory_reservations_item_idx
    ON inventory_reservations (inventory_item_id, location_id)
    WHERE status = 'active' AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS inventory_reservations_line_item_idx
    ON inventory_reservations (line_item_id)
    WHERE line_item_id IS NOT NULL AND deleted_at IS NULL;
