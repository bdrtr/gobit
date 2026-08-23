// Package repository product modülünün veri erişim katmanıdır.
//
// SADECE bu modülün tablolarına dokunur (Prensip 2.1): başka bir modülün
// tablosunu okumaz, ona foreign key vermez. Fiyat ve stok gibi başka modüllere
// ait veriler buradan değil, link'ler ve Query katmanı üzerinden gelir.
//
// Katman sınırı: sqlc'nin ürettiği productdb paketi ve pgtype tipleri bu
// paketin İÇİNDE kalır; dışarıya yalnızca models tipleri ve core/errors tipli
// hataları çıkar.
package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// Hata kodları. Çağıran taraf errors.CodeOf ile bunlara bakabilir; mesaj metni
// değişse de kod sözleşmenin parçasıdır.
const (
	// codeMetadataInvalid jsonb alanının çözümlenememesidir.
	codeMetadataInvalid = "product_metadata_invalid"
	// codeNotFound istenen kaydın (silinmemişler arasında) bulunamamasıdır.
	codeNotFound = "product_not_found"
	// codeConflict adlandırılmamış bir benzersizlik ihlalidir.
	codeConflict = "product_conflict"
	// codeHandleTaken ürün/koleksiyon/kategori handle'ının kullanımda olmasıdır.
	codeHandleTaken = "product_handle_taken"
	// codeSKUTaken varyant SKU'sunun kullanımda olmasıdır.
	codeSKUTaken = "product_sku_taken"
	// codeDuplicate aynı kaydın ikinci kez eklenmeye çalışılmasıdır.
	codeDuplicate = "product_duplicate"
	// codeInvalidRef var olmayan bir kayda referans verilmesidir.
	codeInvalidRef = "product_invalid_reference"
	// codeCheckFailed veritabanı CHECK kısıtının ihlalidir.
	codeCheckFailed = "product_check_failed"
	// codeDBFailed sınıflandırılamayan veritabanı hatasıdır.
	codeDBFailed = "product_db_failed"
	// codeCanceled bağlamın iptal edilmesidir.
	codeCanceled = "product_db_canceled"
)

// PostgreSQL SQLSTATE kodları (bkz. errcodes-appendix).
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
)

// DB deponun ihtiyaç duyduğu bağlantı yüzeyidir: sorgu çalıştırabilmeli ve
// işlem açabilmelidir. *pgxpool.Pool bunu karşılar.
//
// Somut havuz yerine arayüz alınmasının sebebi testtir: deponun işlem yönetimi
// gerçek bir havuz olmadan da doğrulanabilmelidir.
type DB interface {
	productdb.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Store product modülünün veri erişim yüzeyidir.
//
// Arayüz sağlayıcının yanında durur (tüketicinin yanında değil): ADR 0001'in
// "tüketici tarafı interface" kuralı MODÜLLER ARASI bağımlılık içindir; burada
// tüketici de sağlayıcı da aynı modüldür ve arayüzün tek amacı servisi
// veritabanından ayırıp sahte (fake) bir depoyla test edilebilir kılmaktır.
//
// Tüm metodlar context.Context alır (plan Bölüm 8) ve tipli core/errors
// hataları döner.
type Store interface {
	// InTx fn'i tek bir veritabanı işleminde çalıştırır.
	//
	// fn'e verilen Store işleme bağlıdır; fn hata dönerse işlem geri alınır.
	// Zaten bir işlemin içindeki bir Store'da çağrılırsa yeni işlem AÇILMAZ,
	// fn aynı işlemde çalışır — iç içe çağrı sessizce ikinci bir bağlantı
	// kapmaz.
	InTx(ctx context.Context, fn func(ctx context.Context, s Store) error) error

	CreateProduct(ctx context.Context, p models.Product) (models.Product, error)
	GetProduct(ctx context.Context, id string) (models.Product, error)
	// GetProductForUpdate ürünü satır kilidiyle okur; yalnızca InTx içinde
	// anlamlıdır (bkz. Repo.GetProductForUpdate).
	GetProductForUpdate(ctx context.Context, id string) (models.Product, error)
	GetProductByHandle(ctx context.Context, handle string) (models.Product, error)
	ListProducts(ctx context.Context, f ProductFilter) ([]models.Product, error)
	CountProducts(ctx context.Context, f ProductFilter) (int, error)
	ListProductsByIDs(ctx context.Context, ids []string) ([]models.Product, error)
	UpdateProduct(ctx context.Context, id string, patch ProductPatch) (models.Product, error)
	SoftDeleteProduct(ctx context.Context, id string) error
	SoftDeleteProductChildren(ctx context.Context, productID string) error
	ListVariantIDsByProduct(ctx context.Context, productID string) ([]string, error)

	CreateVariant(ctx context.Context, v models.Variant) (models.Variant, error)
	GetVariant(ctx context.Context, id string) (models.Variant, error)
	ListVariants(ctx context.Context, f VariantFilter) ([]models.Variant, error)
	CountVariants(ctx context.Context, f VariantFilter) (int, error)
	ListVariantsByProductIDs(ctx context.Context, productIDs []string) ([]models.Variant, error)
	ListVariantsByIDs(ctx context.Context, ids []string) ([]models.Variant, error)
	UpdateVariant(ctx context.Context, id string, patch VariantPatch) (models.Variant, error)
	SoftDeleteVariant(ctx context.Context, id string) error

	CreateOption(ctx context.Context, o models.Option) (models.Option, error)
	GetOption(ctx context.Context, id string) (models.Option, error)
	ListOptionsByProductIDs(ctx context.Context, productIDs []string) ([]models.Option, error)
	SoftDeleteOption(ctx context.Context, id string) error
	CreateOptionValue(ctx context.Context, v models.OptionValue) (models.OptionValue, error)
	ListOptionValuesByOptionIDs(ctx context.Context, optionIDs []string) ([]models.OptionValue, error)
	ListOptionValuesByIDs(ctx context.Context, ids []string) ([]models.OptionValueRef, error)
	SetVariantOptionValue(ctx context.Context, variantID, optionID, valueID string) error
	DeleteVariantOptionValues(ctx context.Context, variantID string) error
	ListVariantOptionValues(ctx context.Context, variantIDs []string) (map[string][]models.OptionValue, error)

	CreateCollection(ctx context.Context, c models.Collection) (models.Collection, error)
	GetCollection(ctx context.Context, id string) (models.Collection, error)
	ListCollections(ctx context.Context, limit, offset int) ([]models.Collection, error)
	CountCollections(ctx context.Context) (int, error)

	CreateCategory(ctx context.Context, c models.Category) (models.Category, error)
	GetCategory(ctx context.Context, id string) (models.Category, error)
	ListCategories(ctx context.Context, parentID *string, limit, offset int) ([]models.Category, error)
	CountCategories(ctx context.Context, parentID *string) (int, error)

	CreateTag(ctx context.Context, t models.Tag) (models.Tag, error)
	GetTagByValue(ctx context.Context, value string) (models.Tag, error)
	ListTags(ctx context.Context, limit, offset int) ([]models.Tag, error)
	CountTags(ctx context.Context) (int, error)

	SetProductTags(ctx context.Context, productID string, tagIDs []string) error
	SetProductCategories(ctx context.Context, productID string, categoryIDs []string) error
	ListTagsByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Tag, error)
	ListCategoriesByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Category, error)

	CreateImage(ctx context.Context, img models.Image) (models.Image, error)
	ListImagesByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Image, error)
	DeleteImagesByProduct(ctx context.Context, productID string) error
}

// Repo [Store]'un PostgreSQL uygulamasıdır.
type Repo struct {
	q *productdb.Queries
	// pool yalnızca işlem açmak için gerekir; işleme bağlı bir Repo'da nil'dir.
	pool DB
}

// Store'un derleme zamanında karşılandığını garantiler: bir metot imzası
// kaymışsa hata testte değil, derlemede çıkar.
var _ Store = (*Repo)(nil)

// New verilen bağlantı havuzu üzerinde çalışan bir depo üretir.
func New(pool DB) *Repo {
	return &Repo{q: productdb.New(pool), pool: pool}
}

// InTx fn'i tek bir veritabanı işleminde çalıştırır.
func (r *Repo) InTx(ctx context.Context, fn func(ctx context.Context, s Store) error) error {
	if r.pool == nil {
		// Zaten işlemin içindeyiz. İç içe bir işlem açmak havuzdan İKİNCİ bir
		// bağlantı kapardı; o bağlantı dıştaki işlemin henüz görünmeyen
		// yazmalarını okuyamaz ve kilit beklerken kendini bekleyen bir
		// çıkmaza (self-deadlock) girebilirdi.
		return fn(ctx, r)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrapDB(err, "veritabanı işlemi açılamadı")
	}
	// Kesinleşmiş bir işlemde Rollback no-op'tur; hata yolunda ise geri alır.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(ctx, &Repo{q: r.q.WithTx(tx)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapDB(err, "veritabanı işlemi kesinleştirilemedi")
	}
	return nil
}

// wrapDB sürücü hatasını tipli hataya çevirir.
//
// Sınıflandırma çağıranın davranışını belirler: benzersizlik ihlali
// KindConflict (409), var olmayan referans KindInvalid (422), bulunamayan satır
// KindNotFound (404) olur. Sınıflandırılamayan hata KindInternal kalır ve HTTP
// katmanı mesajını bastırır — sürücü metni istemciye sızmaz.
func wrapDB(err error, format string, a ...any) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return errors.Wrap(err, errors.KindNotFound, codeNotFound, format, a...)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.Wrap(err, errors.KindUnavailable, codeCanceled,
			format+" (bağlam iptal edildi)", a...)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation:
			code, reason := conflictReason(pgErr.ConstraintName)
			return errors.Wrap(err, errors.KindConflict, code, format+": %s", append(a, reason)...)
		case pgForeignKeyViolation:
			return errors.Wrap(err, errors.KindInvalid, codeInvalidRef,
				format+": başvurulan kayıt bulunamadı (%s)", append(a, pgErr.ConstraintName)...)
		case pgCheckViolation:
			return errors.Wrap(err, errors.KindInvalid, codeCheckFailed,
				format+": değer kısıtı karşılamıyor (%s)", append(a, pgErr.ConstraintName)...)
		}
	}
	return errors.Wrap(err, errors.KindInternal, codeDBFailed, format, a...)
}

// conflictReason benzersizlik ihlaline yol açan kısıttan okunabilir bir sebep
// ve kararlı bir hata kodu üretir.
//
// Kısıt adları şemayla birlikte gelir; burada eşlenmeyen bir ad genel bir
// çakışma mesajına düşer. Adı mesaja yazmak teşhis içindir: hangi benzersizlik
// kuralının çalıştığı üretimde ancak böyle görülür.
func conflictReason(constraint string) (code, reason string) {
	switch constraint {
	case "product_handle_uniq", "product_collection_handle_uniq", "product_category_handle_uniq":
		return codeHandleTaken, "bu handle zaten kullanımda"
	case "product_variant_sku_uniq":
		return codeSKUTaken, "bu SKU zaten kullanımda"
	case "product_tag_value_uniq":
		return codeDuplicate, "bu etiket zaten var"
	case "product_option_title_uniq":
		return codeDuplicate, "bu üründe aynı başlıkta bir seçenek zaten var"
	case "product_option_value_uniq":
		return codeDuplicate, "bu seçenekte aynı değer zaten var"
	case "":
		return codeConflict, "benzersizlik kısıtı ihlal edildi"
	default:
		return codeConflict, "benzersizlik kısıtı ihlal edildi (" + constraint + ")"
	}
}

// notFound bulunamayan kayıt için tipli hata üretir.
func notFound(entity, id string) error {
	return errors.NotFound(codeNotFound, "%s bulunamadı: %s", entity, id)
}
