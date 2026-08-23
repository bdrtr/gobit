package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// ProductFilter ürün listelemesinin ölçütleridir.
//
// nil alan "bu ölçütü uygulama" demektir; sıfır değer ile "verilmedi" ayrımı
// işaretçiyle korunur (boş dizge geçerli bir handle değildir ama arama
// metninde boş dizge her şeyi eşleştirirdi).
type ProductFilter struct {
	Status       *string
	CollectionID *string
	Handle       *string
	Search       *string
	Limit        int
	Offset       int
}

// ProductPatch bir ürünün kısmi güncellemesidir.
//
// nil alan DEĞİŞTİRİLMEZ. Bu, PATCH sözleşmesinin doğrudan karşılığıdır:
// gövdede bulunmayan alan korunur.
type ProductPatch struct {
	Handle        *string
	Title         *string
	Subtitle      *string
	Description   *string
	Thumbnail     *string
	Status        *string
	Discountable  *bool
	Weight        *int32
	Length        *int32
	Height        *int32
	Width         *int32
	Material      *string
	OriginCountry *string
	CollectionID  *string
	Metadata      map[string]any
}

// CreateProduct yeni bir ürün satırı yazar ve yazılan hâlini döner.
//
// Zaman damgalarını veritabanı üretir; dönen kayıt bu yüzden çağıranın
// gönderdiği değil, SAKLANAN hâlidir.
func (r *Repo) CreateProduct(ctx context.Context, p models.Product) (models.Product, error) {
	meta, err := fromMetadata(p.Metadata)
	if err != nil {
		return models.Product{}, err
	}

	row, err := r.q.CreateProduct(ctx, productdb.CreateProductParams{
		ID:            p.ID,
		Handle:        p.Handle,
		Title:         p.Title,
		Subtitle:      p.Subtitle,
		Description:   p.Description,
		Thumbnail:     p.Thumbnail,
		Status:        string(p.Status),
		IsGiftcard:    p.IsGiftcard,
		Discountable:  p.Discountable,
		Weight:        p.Weight,
		Length:        p.Length,
		Height:        p.Height,
		Width:         p.Width,
		Material:      p.Material,
		OriginCountry: p.OriginCountry,
		CollectionID:  p.CollectionID,
		Metadata:      meta,
	})
	if err != nil {
		return models.Product{}, wrapDB(err, "ürün oluşturulamadı (%s)", p.Handle)
	}
	return toProduct(row)
}

// GetProduct kimliğe göre ürünü döner; silinmiş kayıt bulunamaz sayılır.
func (r *Repo) GetProduct(ctx context.Context, id string) (models.Product, error) {
	row, err := r.q.GetProduct(ctx, id)
	if err != nil {
		return models.Product{}, wrapDB(err, "ürün bulunamadı: %s", id)
	}
	return toProduct(row)
}

// GetProductByHandle handle'a göre ürünü döner.
func (r *Repo) GetProductByHandle(ctx context.Context, handle string) (models.Product, error) {
	row, err := r.q.GetProductByHandle(ctx, handle)
	if err != nil {
		return models.Product{}, wrapDB(err, "ürün bulunamadı (handle: %s)", handle)
	}
	return toProduct(row)
}

// ListProducts ölçütlere uyan ürünleri sayfalı döner.
func (r *Repo) ListProducts(ctx context.Context, f ProductFilter) ([]models.Product, error) {
	rows, err := r.q.ListProducts(ctx, productdb.ListProductsParams{
		Status:       f.Status,
		CollectionID: f.CollectionID,
		Handle:       f.Handle,
		Search:       f.Search,
		Lim:          toInt32(f.Limit),
		Off:          toInt32(f.Offset),
	})
	if err != nil {
		return nil, wrapDB(err, "ürünler listelenemedi")
	}
	return toProducts(rows)
}

// CountProducts ölçütlere uyan TOPLAM ürün sayısını döner.
//
// Sayı, sayfalama zarfının ("count") kaynağıdır ve limit/offset'ten
// BAĞIMSIZDIR; istemci kaç sayfa olduğunu ancak böyle bilebilir.
func (r *Repo) CountProducts(ctx context.Context, f ProductFilter) (int, error) {
	n, err := r.q.CountProducts(ctx, productdb.CountProductsParams{
		Status:       f.Status,
		CollectionID: f.CollectionID,
		Handle:       f.Handle,
		Search:       f.Search,
	})
	if err != nil {
		return 0, wrapDB(err, "ürün sayısı okunamadı")
	}
	return int(n), nil
}

// ListProductsByIDs verilen kimliklerin ürünlerini TEK sorguda döner.
//
// Query sağlayıcısının FetchByIDs'i bunu kullanır; bulunamayan kimlik için
// kayıt dönmez ve bu bir hata değildir (ADR 0004).
func (r *Repo) ListProductsByIDs(ctx context.Context, ids []string) ([]models.Product, error) {
	if len(ids) == 0 {
		return []models.Product{}, nil
	}
	rows, err := r.q.ListProductsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "ürünler kimliğe göre okunamadı (%d kimlik)", len(ids))
	}
	return toProducts(rows)
}

// UpdateProduct ürünü kısmi olarak günceller ve güncel hâlini döner.
func (r *Repo) UpdateProduct(ctx context.Context, id string, patch ProductPatch) (models.Product, error) {
	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.Product{}, err
	}

	row, err := r.q.UpdateProduct(ctx, productdb.UpdateProductParams{
		ID:            id,
		Handle:        patch.Handle,
		Title:         patch.Title,
		Subtitle:      patch.Subtitle,
		Description:   patch.Description,
		Thumbnail:     patch.Thumbnail,
		Status:        patch.Status,
		Discountable:  patch.Discountable,
		Weight:        patch.Weight,
		Length:        patch.Length,
		Height:        patch.Height,
		Width:         patch.Width,
		Material:      patch.Material,
		OriginCountry: patch.OriginCountry,
		CollectionID:  patch.CollectionID,
		Metadata:      meta,
	})
	if err != nil {
		return models.Product{}, wrapDB(err, "ürün güncellenemedi: %s", id)
	}
	return toProduct(row)
}

// SoftDeleteProduct ürünü siler (deleted_at damgalar).
//
// Zaten silinmiş ya da hiç var olmamış bir kayıt errors.NotFound döner: silme
// çağrısının sessizce başarılı görünmesi, istemcinin yanlış kimlikle çalıştığını
// gizlerdi.
func (r *Repo) SoftDeleteProduct(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteProduct(ctx, id)
	if err != nil {
		return wrapDB(err, "ürün silinemedi: %s", id)
	}
	if n == 0 {
		return notFound("ürün", id)
	}
	return nil
}

// SoftDeleteProductChildren ürünün varyantlarını, seçeneklerini ve
// görsellerini siler.
//
// Silme neden veritabanı CASCADE'ine bırakılmıyor: CASCADE satırı GERÇEKTEN
// siler, oysa buradaki silme SOFT'tur ve kaydın izini bırakır. Ayrıca sıra
// önemlidir; bu yüzden çağıran onu tek işlemde (InTx) sarar.
func (r *Repo) SoftDeleteProductChildren(ctx context.Context, productID string) error {
	if _, err := r.q.SoftDeleteVariantsByProduct(ctx, productID); err != nil {
		return wrapDB(err, "ürünün varyantları silinemedi: %s", productID)
	}
	if _, err := r.q.SoftDeleteOptionsByProduct(ctx, productID); err != nil {
		return wrapDB(err, "ürünün seçenekleri silinemedi: %s", productID)
	}
	if err := r.q.DeleteImagesByProduct(ctx, productID); err != nil {
		return wrapDB(err, "ürünün görselleri silinemedi: %s", productID)
	}
	return nil
}

// ListVariantIDsByProduct ürünün silinmemiş varyant kimliklerini döner.
func (r *Repo) ListVariantIDsByProduct(ctx context.Context, productID string) ([]string, error) {
	ids, err := r.q.ListVariantIDsByProduct(ctx, productID)
	if err != nil {
		return nil, wrapDB(err, "ürünün varyant kimlikleri okunamadı: %s", productID)
	}
	return ids, nil
}

// CreateImage ürüne görsel ekler.
func (r *Repo) CreateImage(ctx context.Context, img models.Image) (models.Image, error) {
	meta, err := fromMetadata(img.Metadata)
	if err != nil {
		return models.Image{}, err
	}

	row, err := r.q.CreateImage(ctx, productdb.CreateImageParams{
		ID:        img.ID,
		ProductID: img.ProductID,
		Url:       img.URL,
		Rank:      img.Rank,
		Metadata:  meta,
	})
	if err != nil {
		return models.Image{}, wrapDB(err, "ürün görseli eklenemedi: %s", img.ProductID)
	}
	return toImage(row)
}

// ListImagesByProductIDs verilen ürünlerin görsellerini TEK sorguda döner.
func (r *Repo) ListImagesByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Image, error) {
	if len(productIDs) == 0 {
		return map[string][]models.Image{}, nil
	}
	rows, err := r.q.ListImagesByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "ürün görselleri okunamadı (%d ürün)", len(productIDs))
	}

	out := make(map[string][]models.Image, len(productIDs))
	for i := range rows {
		img, err := toImage(rows[i])
		if err != nil {
			return nil, err
		}
		out[rows[i].ProductID] = append(out[rows[i].ProductID], img)
	}
	return out, nil
}

// DeleteImagesByProduct ürünün görsellerini siler.
func (r *Repo) DeleteImagesByProduct(ctx context.Context, productID string) error {
	if err := r.q.DeleteImagesByProduct(ctx, productID); err != nil {
		return wrapDB(err, "ürünün görselleri silinemedi: %s", productID)
	}
	return nil
}
