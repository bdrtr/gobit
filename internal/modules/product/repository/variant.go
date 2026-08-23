package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// VariantFilter varyant listelemesinin ölçütleridir.
type VariantFilter struct {
	ProductID *string
	Limit     int
	Offset    int
}

// VariantPatch bir varyantın kısmi güncellemesidir; nil alan değişmez.
type VariantPatch struct {
	Title           *string
	SKU             *string
	Barcode         *string
	EAN             *string
	UPC             *string
	ManageInventory *bool
	AllowBackorder  *bool
	Weight          *int32
	Rank            *int32
	Metadata        map[string]any
}

// CreateVariant yeni bir varyant yazar ve saklanan hâlini döner.
func (r *Repo) CreateVariant(ctx context.Context, v models.Variant) (models.Variant, error) {
	meta, err := fromMetadata(v.Metadata)
	if err != nil {
		return models.Variant{}, err
	}

	row, err := r.q.CreateVariant(ctx, productdb.CreateVariantParams{
		ID:              v.ID,
		ProductID:       v.ProductID,
		Title:           v.Title,
		Sku:             v.SKU,
		Barcode:         v.Barcode,
		Ean:             v.EAN,
		Upc:             v.UPC,
		ManageInventory: v.ManageInventory,
		AllowBackorder:  v.AllowBackorder,
		Weight:          v.Weight,
		Rank:            v.Rank,
		Metadata:        meta,
	})
	if err != nil {
		return models.Variant{}, wrapDB(err, "varyant oluşturulamadı (ürün: %s)", v.ProductID)
	}
	return toVariant(row)
}

// GetVariant kimliğe göre varyantı döner.
func (r *Repo) GetVariant(ctx context.Context, id string) (models.Variant, error) {
	row, err := r.q.GetVariant(ctx, id)
	if err != nil {
		return models.Variant{}, wrapDB(err, "varyant bulunamadı: %s", id)
	}
	return toVariant(row)
}

// ListVariants ölçütlere uyan varyantları sayfalı döner.
func (r *Repo) ListVariants(ctx context.Context, f VariantFilter) ([]models.Variant, error) {
	rows, err := r.q.ListVariants(ctx, productdb.ListVariantsParams{
		ProductID: f.ProductID,
		Lim:       toInt32(f.Limit),
		Off:       toInt32(f.Offset),
	})
	if err != nil {
		return nil, wrapDB(err, "varyantlar listelenemedi")
	}
	return toVariants(rows)
}

// CountVariants ölçütlere uyan toplam varyant sayısını döner.
func (r *Repo) CountVariants(ctx context.Context, f VariantFilter) (int, error) {
	n, err := r.q.CountVariants(ctx, f.ProductID)
	if err != nil {
		return 0, wrapDB(err, "varyant sayısı okunamadı")
	}
	return int(n), nil
}

// ListVariantsByProductIDs verilen ürünlerin varyantlarını TEK sorguda döner.
//
// Ürün listelemesinin varyantları doldurması bunun üzerinden yapılır; ürün
// başına sorgu N+1 demek olurdu.
func (r *Repo) ListVariantsByProductIDs(ctx context.Context, productIDs []string) ([]models.Variant, error) {
	if len(productIDs) == 0 {
		return []models.Variant{}, nil
	}
	rows, err := r.q.ListVariantsByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "ürünlerin varyantları okunamadı (%d ürün)", len(productIDs))
	}
	return toVariants(rows)
}

// ListVariantsByIDs verilen kimliklerin varyantlarını TEK sorguda döner.
func (r *Repo) ListVariantsByIDs(ctx context.Context, ids []string) ([]models.Variant, error) {
	if len(ids) == 0 {
		return []models.Variant{}, nil
	}
	rows, err := r.q.ListVariantsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "varyantlar kimliğe göre okunamadı (%d kimlik)", len(ids))
	}
	return toVariants(rows)
}

// UpdateVariant varyantı kısmi olarak günceller.
func (r *Repo) UpdateVariant(ctx context.Context, id string, patch VariantPatch) (models.Variant, error) {
	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.Variant{}, err
	}

	row, err := r.q.UpdateVariant(ctx, productdb.UpdateVariantParams{
		ID:              id,
		Title:           patch.Title,
		Sku:             patch.SKU,
		Barcode:         patch.Barcode,
		Ean:             patch.EAN,
		Upc:             patch.UPC,
		ManageInventory: patch.ManageInventory,
		AllowBackorder:  patch.AllowBackorder,
		Weight:          patch.Weight,
		Rank:            patch.Rank,
		Metadata:        meta,
	})
	if err != nil {
		return models.Variant{}, wrapDB(err, "varyant güncellenemedi: %s", id)
	}
	return toVariant(row)
}

// SoftDeleteVariant varyantı siler; kayıt yoksa errors.NotFound döner.
func (r *Repo) SoftDeleteVariant(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteVariant(ctx, id)
	if err != nil {
		return wrapDB(err, "varyant silinemedi: %s", id)
	}
	if n == 0 {
		return notFound("varyant", id)
	}
	return nil
}

// CreateOption ürüne seçenek ekler.
func (r *Repo) CreateOption(ctx context.Context, o models.Option) (models.Option, error) {
	row, err := r.q.CreateOption(ctx, productdb.CreateOptionParams{
		ID:        o.ID,
		ProductID: o.ProductID,
		Title:     o.Title,
		Rank:      o.Rank,
	})
	if err != nil {
		return models.Option{}, wrapDB(err, "seçenek oluşturulamadı (ürün: %s)", o.ProductID)
	}
	return toOption(row), nil
}

// GetOption kimliğe göre seçeneği döner.
func (r *Repo) GetOption(ctx context.Context, id string) (models.Option, error) {
	row, err := r.q.GetOption(ctx, id)
	if err != nil {
		return models.Option{}, wrapDB(err, "seçenek bulunamadı: %s", id)
	}
	return toOption(row), nil
}

// ListOptionsByProductIDs verilen ürünlerin seçeneklerini TEK sorguda döner.
func (r *Repo) ListOptionsByProductIDs(ctx context.Context, productIDs []string) ([]models.Option, error) {
	if len(productIDs) == 0 {
		return []models.Option{}, nil
	}
	rows, err := r.q.ListOptionsByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "ürünlerin seçenekleri okunamadı (%d ürün)", len(productIDs))
	}

	out := make([]models.Option, 0, len(rows))
	for i := range rows {
		out = append(out, toOption(rows[i]))
	}
	return out, nil
}

// SoftDeleteOption seçeneği siler; kayıt yoksa errors.NotFound döner.
func (r *Repo) SoftDeleteOption(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteOption(ctx, id)
	if err != nil {
		return wrapDB(err, "seçenek silinemedi: %s", id)
	}
	if n == 0 {
		return notFound("seçenek", id)
	}
	return nil
}

// CreateOptionValue seçeneğe değer ekler.
func (r *Repo) CreateOptionValue(ctx context.Context, v models.OptionValue) (models.OptionValue, error) {
	row, err := r.q.CreateOptionValue(ctx, productdb.CreateOptionValueParams{
		ID:       v.ID,
		OptionID: v.OptionID,
		Value:    v.Value,
		Rank:     v.Rank,
	})
	if err != nil {
		return models.OptionValue{}, wrapDB(err, "seçenek değeri oluşturulamadı (seçenek: %s)", v.OptionID)
	}
	return toOptionValue(row), nil
}

// ListOptionValuesByOptionIDs verilen seçeneklerin değerlerini TEK sorguda döner.
func (r *Repo) ListOptionValuesByOptionIDs(ctx context.Context, optionIDs []string) ([]models.OptionValue, error) {
	if len(optionIDs) == 0 {
		return []models.OptionValue{}, nil
	}
	rows, err := r.q.ListOptionValuesByOptionIDs(ctx, optionIDs)
	if err != nil {
		return nil, wrapDB(err, "seçenek değerleri okunamadı (%d seçenek)", len(optionIDs))
	}

	out := make([]models.OptionValue, 0, len(rows))
	for i := range rows {
		out = append(out, toOptionValue(rows[i]))
	}
	return out, nil
}

// ListOptionValuesByIDs verilen değerleri, ait oldukları ÜRÜN kimliğiyle döner.
//
// Ürün kimliği doğrulama içindir: bir varyanta yalnızca kendi ürününün
// seçenek değerleri bağlanabilir.
func (r *Repo) ListOptionValuesByIDs(ctx context.Context, ids []string) ([]models.OptionValueRef, error) {
	if len(ids) == 0 {
		return []models.OptionValueRef{}, nil
	}
	rows, err := r.q.ListOptionValuesByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "seçenek değerleri okunamadı (%d kimlik)", len(ids))
	}

	out := make([]models.OptionValueRef, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.OptionValueRef{
			OptionValue: models.OptionValue{
				ID:          row.ID,
				OptionID:    row.OptionID,
				Value:       row.Value,
				Rank:        row.Rank,
				OptionTitle: row.OptionTitle,
			},
			ProductID: row.ProductID,
		})
	}
	return out, nil
}

// SetVariantOptionValue varyantın bir seçenekteki değerini yazar.
//
// Aynı seçenek için ikinci çağrı yeni satır değil GÜNCELLEME üretir; kural
// birincil anahtarla (variant_id, option_id) şemada durur.
func (r *Repo) SetVariantOptionValue(ctx context.Context, variantID, optionID, valueID string) error {
	err := r.q.SetVariantOptionValue(ctx, productdb.SetVariantOptionValueParams{
		VariantID: variantID,
		OptionID:  optionID,
		ValueID:   valueID,
	})
	if err != nil {
		return wrapDB(err, "varyantın seçenek değeri yazılamadı (varyant: %s)", variantID)
	}
	return nil
}

// DeleteVariantOptionValues varyantın tüm seçenek değerlerini kaldırır.
//
// Bu bağ satırlarının soft delete'i yoktur: satırın kendisi bir ilişkidir,
// tarihçesi değil.
func (r *Repo) DeleteVariantOptionValues(ctx context.Context, variantID string) error {
	if err := r.q.DeleteVariantOptionValues(ctx, variantID); err != nil {
		return wrapDB(err, "varyantın seçenek değerleri kaldırılamadı: %s", variantID)
	}
	return nil
}

// ListVariantOptionValues verilen varyantların seçenek değerlerini TEK sorguda
// döner; sonuç varyant kimliğine göre gruplanmıştır.
func (r *Repo) ListVariantOptionValues(ctx context.Context, variantIDs []string) (map[string][]models.OptionValue, error) {
	if len(variantIDs) == 0 {
		return map[string][]models.OptionValue{}, nil
	}
	rows, err := r.q.ListVariantOptionValuesByVariantIDs(ctx, variantIDs)
	if err != nil {
		return nil, wrapDB(err, "varyantların seçenek değerleri okunamadı (%d varyant)", len(variantIDs))
	}

	out := make(map[string][]models.OptionValue, len(variantIDs))
	for _, row := range rows {
		out[row.VariantID] = append(out[row.VariantID], models.OptionValue{
			ID:          row.ID,
			OptionID:    row.OptionID,
			Value:       row.Value,
			Rank:        row.Rank,
			OptionTitle: row.OptionTitle,
		})
	}
	return out, nil
}
