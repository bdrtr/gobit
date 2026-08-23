package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// CreateCollection yeni bir koleksiyon yazar.
func (r *Repo) CreateCollection(ctx context.Context, c models.Collection) (models.Collection, error) {
	meta, err := fromMetadata(c.Metadata)
	if err != nil {
		return models.Collection{}, err
	}

	row, err := r.q.CreateCollection(ctx, productdb.CreateCollectionParams{
		ID:       c.ID,
		Title:    c.Title,
		Handle:   c.Handle,
		Metadata: meta,
	})
	if err != nil {
		return models.Collection{}, wrapDB(err, "koleksiyon oluşturulamadı (%s)", c.Handle)
	}
	return toCollection(row)
}

// GetCollection kimliğe göre koleksiyonu döner.
func (r *Repo) GetCollection(ctx context.Context, id string) (models.Collection, error) {
	row, err := r.q.GetCollection(ctx, id)
	if err != nil {
		return models.Collection{}, wrapDB(err, "koleksiyon bulunamadı: %s", id)
	}
	return toCollection(row)
}

// ListCollections koleksiyonları sayfalı döner.
func (r *Repo) ListCollections(ctx context.Context, limit, offset int) ([]models.Collection, error) {
	rows, err := r.q.ListCollections(ctx, productdb.ListCollectionsParams{
		Lim: toInt32(limit),
		Off: toInt32(offset),
	})
	if err != nil {
		return nil, wrapDB(err, "koleksiyonlar listelenemedi")
	}

	out := make([]models.Collection, 0, len(rows))
	for i := range rows {
		c, err := toCollection(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// CountCollections toplam koleksiyon sayısını döner.
func (r *Repo) CountCollections(ctx context.Context) (int, error) {
	n, err := r.q.CountCollections(ctx)
	if err != nil {
		return 0, wrapDB(err, "koleksiyon sayısı okunamadı")
	}
	return int(n), nil
}

// CreateCategory yeni bir kategori yazar.
func (r *Repo) CreateCategory(ctx context.Context, c models.Category) (models.Category, error) {
	row, err := r.q.CreateCategory(ctx, productdb.CreateCategoryParams{
		ID:          c.ID,
		Name:        c.Name,
		Handle:      c.Handle,
		Description: c.Description,
		ParentID:    c.ParentID,
		IsActive:    c.IsActive,
		IsInternal:  c.IsInternal,
		Rank:        c.Rank,
	})
	if err != nil {
		return models.Category{}, wrapDB(err, "kategori oluşturulamadı (%s)", c.Handle)
	}
	return toCategory(row), nil
}

// GetCategory kimliğe göre kategoriyi döner.
func (r *Repo) GetCategory(ctx context.Context, id string) (models.Category, error) {
	row, err := r.q.GetCategory(ctx, id)
	if err != nil {
		return models.Category{}, wrapDB(err, "kategori bulunamadı: %s", id)
	}
	return toCategory(row), nil
}

// ListCategories kategorileri sayfalı döner; parentID verilirse yalnızca o
// düğümün çocukları listelenir.
func (r *Repo) ListCategories(ctx context.Context, parentID *string, limit, offset int) ([]models.Category, error) {
	rows, err := r.q.ListCategories(ctx, productdb.ListCategoriesParams{
		ParentID: parentID,
		Lim:      toInt32(limit),
		Off:      toInt32(offset),
	})
	if err != nil {
		return nil, wrapDB(err, "kategoriler listelenemedi")
	}

	out := make([]models.Category, 0, len(rows))
	for i := range rows {
		out = append(out, toCategory(rows[i]))
	}
	return out, nil
}

// CountCategories toplam kategori sayısını döner.
func (r *Repo) CountCategories(ctx context.Context, parentID *string) (int, error) {
	n, err := r.q.CountCategories(ctx, parentID)
	if err != nil {
		return 0, wrapDB(err, "kategori sayısı okunamadı")
	}
	return int(n), nil
}

// CreateTag yeni bir etiket yazar.
func (r *Repo) CreateTag(ctx context.Context, t models.Tag) (models.Tag, error) {
	row, err := r.q.CreateTag(ctx, productdb.CreateTagParams{ID: t.ID, Value: t.Value})
	if err != nil {
		return models.Tag{}, wrapDB(err, "etiket oluşturulamadı (%s)", t.Value)
	}
	return toTag(row), nil
}

// GetTagByValue değere göre etiketi döner.
func (r *Repo) GetTagByValue(ctx context.Context, value string) (models.Tag, error) {
	row, err := r.q.GetTagByValue(ctx, value)
	if err != nil {
		return models.Tag{}, wrapDB(err, "etiket bulunamadı: %s", value)
	}
	return toTag(row), nil
}

// ListTags etiketleri sayfalı döner.
func (r *Repo) ListTags(ctx context.Context, limit, offset int) ([]models.Tag, error) {
	rows, err := r.q.ListTags(ctx, productdb.ListTagsParams{
		Lim: toInt32(limit),
		Off: toInt32(offset),
	})
	if err != nil {
		return nil, wrapDB(err, "etiketler listelenemedi")
	}

	out := make([]models.Tag, 0, len(rows))
	for i := range rows {
		out = append(out, toTag(rows[i]))
	}
	return out, nil
}

// CountTags toplam etiket sayısını döner.
func (r *Repo) CountTags(ctx context.Context) (int, error) {
	n, err := r.q.CountTags(ctx)
	if err != nil {
		return 0, wrapDB(err, "etiket sayısı okunamadı")
	}
	return int(n), nil
}

// SetProductTags ürünün etiketlerini verilen kümeyle DEĞİŞTİRİR.
//
// Önce mevcut bağlar silinir, sonra yenileri yazılır; çağıran bunu bir işlemin
// (InTx) içinde sarmalıdır, aksi hâlde arada ürün etiketsiz görünebilir.
func (r *Repo) SetProductTags(ctx context.Context, productID string, tagIDs []string) error {
	if err := r.q.DeleteProductTags(ctx, productID); err != nil {
		return wrapDB(err, "ürünün etiketleri temizlenemedi: %s", productID)
	}
	for _, tagID := range tagIDs {
		err := r.q.AddProductTag(ctx, productdb.AddProductTagParams{ProductID: productID, TagID: tagID})
		if err != nil {
			return wrapDB(err, "ürüne etiket bağlanamadı (%s -> %s)", productID, tagID)
		}
	}
	return nil
}

// SetProductCategories ürünün kategorilerini verilen kümeyle değiştirir.
func (r *Repo) SetProductCategories(ctx context.Context, productID string, categoryIDs []string) error {
	if err := r.q.DeleteProductCategories(ctx, productID); err != nil {
		return wrapDB(err, "ürünün kategorileri temizlenemedi: %s", productID)
	}
	for _, categoryID := range categoryIDs {
		err := r.q.AddProductCategory(ctx, productdb.AddProductCategoryParams{
			ProductID:  productID,
			CategoryID: categoryID,
		})
		if err != nil {
			return wrapDB(err, "ürüne kategori bağlanamadı (%s -> %s)", productID, categoryID)
		}
	}
	return nil
}

// ListTagsByProductIDs verilen ürünlerin etiketlerini TEK sorguda döner.
func (r *Repo) ListTagsByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Tag, error) {
	if len(productIDs) == 0 {
		return map[string][]models.Tag{}, nil
	}
	rows, err := r.q.ListTagsByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "ürünlerin etiketleri okunamadı (%d ürün)", len(productIDs))
	}

	out := make(map[string][]models.Tag, len(productIDs))
	for _, row := range rows {
		out[row.ProductID] = append(out[row.ProductID], models.Tag{ID: row.ID, Value: row.Value})
	}
	return out, nil
}

// ListCategoriesByProductIDs verilen ürünlerin kategorilerini TEK sorguda döner.
func (r *Repo) ListCategoriesByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Category, error) {
	if len(productIDs) == 0 {
		return map[string][]models.Category{}, nil
	}
	rows, err := r.q.ListCategoriesByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "ürünlerin kategorileri okunamadı (%d ürün)", len(productIDs))
	}

	out := make(map[string][]models.Category, len(productIDs))
	for _, row := range rows {
		out[row.ProductID] = append(out[row.ProductID], models.Category{
			ID:       row.ID,
			Name:     row.Name,
			Handle:   row.Handle,
			ParentID: row.ParentID,
			Rank:     row.Rank,
		})
	}
	return out, nil
}
