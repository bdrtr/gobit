package repository

import (
	"encoding/json"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// Bu dosya pgtype <-> domain modeli dönüşümlerinin TEK yeridir.
//
// Sınırın burada olması bilinçlidir: sürücüye özgü tipler (pgtype.Timestamptz,
// jsonb için []byte) repository'nin dışına çıkmaz. Servis ve API katmanı
// time.Time ve map[string]any görür; bir gün sürücü değişirse değişen yer
// yalnızca burasıdır.

// toInt32 sayfalama değerini sorgunun beklediği int32'ye GÜVENLE daraltır.
//
// Negatif değer sıfıra, int32'yi aşan değer üst sınıra çekilir: aksi hâlde
// daraltma sessizce işaret değiştirir ve "LIMIT -2147483648" gibi bir sorgu
// üretirdi. Sınır kontrolü çağıranın doğrulamasına bırakılmaz; burası son
// savunmadır.
func toInt32(n int) int32 {
	switch {
	case n < 0:
		return 0
	case n > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(n)
	}
}

// toTime timestamptz değerini UTC time.Time'a çevirir.
//
// Geçersiz (NULL) değer sıfır zaman döner: NOT NULL sütunlarda bu durum
// oluşmaz, dolayısıyla sıfır zaman görülmesi veri bozukluğunun işaretidir.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toTimePtr nullable timestamptz değerini *time.Time'a çevirir.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// toMetadata jsonb sütununu haritaya çevirir.
//
// Boş ya da JSON null değer nil harita döner; böylece API yanıtında
// "metadata": null yerine alan hiç görünmez (omitempty).
func toMetadata(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeMetadataInvalid,
			"metadata alanı çözümlenemedi")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromMetadata haritayı jsonb sütununa yazılacak bayta çevirir.
//
// nil harita boş nesneye ('{}') çevrilir: sütun NOT NULL'dur ve "metadata yok"
// ile "metadata boş" ayrımı bu modülde bir şey ifade etmez.
func fromMetadata(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, codeMetadataInvalid,
			"metadata alanı JSON'a çevrilemedi")
	}
	return raw, nil
}

// patchMetadata güncelleme için metadata parametresini üretir.
//
// nil harita "değiştirme" demektir ve sorguya NULL gider (COALESCE kalıbı
// eski değeri korur); dolu harita yeni değeri yazar.
func patchMetadata(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return fromMetadata(m)
}

// toProduct veritabanı satırını domain modeline çevirir.
func toProduct(row productdb.Product) (models.Product, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.Product{}, err
	}
	return models.Product{
		ID:            row.ID,
		Handle:        row.Handle,
		Title:         row.Title,
		Subtitle:      row.Subtitle,
		Description:   row.Description,
		Thumbnail:     row.Thumbnail,
		Status:        models.Status(row.Status),
		IsGiftcard:    row.IsGiftcard,
		Discountable:  row.Discountable,
		Weight:        row.Weight,
		Length:        row.Length,
		Height:        row.Height,
		Width:         row.Width,
		Material:      row.Material,
		OriginCountry: row.OriginCountry,
		CollectionID:  row.CollectionID,
		Metadata:      meta,
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
		DeletedAt:     toTimePtr(row.DeletedAt),
	}, nil
}

// toProducts satır dilimini domain modeli dilimine çevirir.
func toProducts(rows []productdb.Product) ([]models.Product, error) {
	out := make([]models.Product, 0, len(rows))
	// Döngü indeksle gezilir: satır yapıları büyüktür ve değerle kopyalamak
	// her tur birkaç yüz baytı boşuna taşır.
	for i := range rows {
		p, err := toProduct(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// toVariant veritabanı satırını domain modeline çevirir.
func toVariant(row productdb.ProductVariant) (models.Variant, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.Variant{}, err
	}
	return models.Variant{
		ID:              row.ID,
		ProductID:       row.ProductID,
		Title:           row.Title,
		SKU:             row.Sku,
		Barcode:         row.Barcode,
		EAN:             row.Ean,
		UPC:             row.Upc,
		ManageInventory: row.ManageInventory,
		AllowBackorder:  row.AllowBackorder,
		Weight:          row.Weight,
		Rank:            row.Rank,
		Metadata:        meta,
		CreatedAt:       toTime(row.CreatedAt),
		UpdatedAt:       toTime(row.UpdatedAt),
		DeletedAt:       toTimePtr(row.DeletedAt),
	}, nil
}

// toVariants satır dilimini domain modeli dilimine çevirir.
func toVariants(rows []productdb.ProductVariant) ([]models.Variant, error) {
	out := make([]models.Variant, 0, len(rows))
	for i := range rows {
		v, err := toVariant(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// toOption seçenek satırını domain modeline çevirir.
func toOption(row productdb.ProductOption) models.Option {
	return models.Option{
		ID:        row.ID,
		ProductID: row.ProductID,
		Title:     row.Title,
		Rank:      row.Rank,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}
}

// toOptionValue seçenek değeri satırını domain modeline çevirir.
func toOptionValue(row productdb.ProductOptionValue) models.OptionValue {
	return models.OptionValue{
		ID:        row.ID,
		OptionID:  row.OptionID,
		Value:     row.Value,
		Rank:      row.Rank,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}
}

// toCollection koleksiyon satırını domain modeline çevirir.
func toCollection(row productdb.ProductCollection) (models.Collection, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.Collection{}, err
	}
	return models.Collection{
		ID:        row.ID,
		Title:     row.Title,
		Handle:    row.Handle,
		Metadata:  meta,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}, nil
}

// toCategory kategori satırını domain modeline çevirir.
func toCategory(row productdb.ProductCategory) models.Category {
	return models.Category{
		ID:          row.ID,
		Name:        row.Name,
		Handle:      row.Handle,
		Description: row.Description,
		ParentID:    row.ParentID,
		IsActive:    row.IsActive,
		IsInternal:  row.IsInternal,
		Rank:        row.Rank,
		CreatedAt:   toTime(row.CreatedAt),
		UpdatedAt:   toTime(row.UpdatedAt),
		DeletedAt:   toTimePtr(row.DeletedAt),
	}
}

// toTag etiket satırını domain modeline çevirir.
func toTag(row productdb.ProductTag) models.Tag {
	return models.Tag{
		ID:        row.ID,
		Value:     row.Value,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}
}

// toImage görsel satırını domain modeline çevirir.
func toImage(row productdb.ProductImage) (models.Image, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.Image{}, err
	}
	return models.Image{
		ID:        row.ID,
		ProductID: row.ProductID,
		URL:       row.Url,
		Rank:      row.Rank,
		Metadata:  meta,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}, nil
}
