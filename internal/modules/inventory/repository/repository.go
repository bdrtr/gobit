// Package repository inventory modülünün veritabanı erişimidir.
//
// SADECE bu modülün tablolarına dokunur (plan Bölüm 4). sqlc üretimi kod
// repository/inventorydb altındadır ve elle düzenlenmez; bu paket onun üstüne
// iki şey ekler:
//
//   - Çeviri: pgtype ve üretilmiş satır tipleri BU PAKETİN DIŞINA ÇIKMAZ,
//     models tiplerine çevrilir.
//   - Sınıflandırma: sürücü hataları core/errors tipli hatalarına çevrilir;
//     satır bulunamaması NotFound, benzersizlik ihlali Conflict olur.
//
// # İşlem (transaction) taşınması
//
// [Repository.WithTx] bir işlem açar ve onu CONTEXT'e koyar; işlem boyunca
// çağrılan tüm repository metodları o context'i aldıkları sürece aynı işlemde
// çalışır. Bunun alternatifi, işlem tutamağını taşıyan ayrı bir arayüz tipini
// metot imzalarına koymaktı; o durumda servis kendi paketinde tanımladığı dar
// arayüzle bu paketi YAPISAL OLARAK eşleştiremezdi — Go'da imzadaki adlandırılmış
// tipler birebir aynı olmak zorundadır, yani servis repository'yi import etmek
// zorunda kalırdı. Context ile taşımak imzaları iki tarafın da paylaştığı
// tiplere (context.Context, models.*) indirger.
//
// Kilit alan metotlar (Lock...) işlem DIŞINDA çağrılırsa hata döner: FOR UPDATE
// kilidi işlem bitince serbest kalacağı için, işlemsiz bir kilit sessizce
// hiçbir şey korumazdı.
package repository

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository/inventorydb"
)

// Hata kodları. Çağıran taraf errors.CodeOf ile bunlara bakabilir; API
// katmanı da aynı kodları istemciye geçirir.
const (
	codeItemNotFound        = "inventory_item_not_found"
	codeLocationNotFound    = "inventory_location_not_found"
	codeLevelNotFound       = "inventory_level_not_found"
	codeReservationNotFound = "inventory_reservation_not_found"
	codeSKUExists           = "inventory_sku_exists"
	codeLevelExists         = "inventory_level_exists"
	codeInsufficientStock   = "inventory_insufficient_stock"
	codeTxRequired          = "inventory_tx_required"
	codeQueryFailed         = "inventory_query_failed"
	codeConcurrentUpdate    = "inventory_concurrent_update"
)

// Kısıt adları; sürücü hatasını anlamlı bir tipli hataya çevirmek için
// kullanılır. Adlar migration'daki adlarla birebir aynıdır.
const (
	constraintSKUUniq       = "inventory_items_sku_uniq"
	constraintLevelUniq     = "inventory_levels_item_location_uniq"
	constraintAvailable     = "inventory_levels_available_nonneg"
	constraintStockedNonneg = "inventory_levels_stocked_nonneg"
)

// PostgreSQL SQLSTATE kodları.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	sqlStateDeadlockDetected    = "40P01"
)

// rollbackTimeout iptal edilmiş bir bağlamda geri almaya tanınan süredir.
// Geri alma, çağıranın ctx'i dolmuş olsa da denenmelidir; aksi hâlde işlem
// bağlantı havuza dönene kadar açık kalırdı.
const rollbackTimeout = 5 * time.Second

// txKeyType context anahtarının tipidir; dışarıdan üretilemesin diye
// dışa açık değildir.
type txKeyType struct{}

// txKey işlem tutamağının context'teki anahtarıdır.
var txKey = txKeyType{}

// Repository inventory tablolarına erişimdir. Eşzamanlı kullanıma güvenlidir.
type Repository struct {
	pool *pgxpool.Pool
}

// New verilen havuz üzerinde çalışan bir Repository üretir.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// WithTx fn'i tek bir veritabanı işleminde çalıştırır.
//
// fn'e verilen context işlemi taşır; o context ile çağrılan tüm repository
// metodları aynı işlemde koşar. fn hata dönerse ya da panikler ise işlem geri
// alınır, hata (panikte panik) yukarı verilir.
//
// Çağrı iç içe gelirse yeni bir işlem AÇILMAZ, var olan kullanılır: iç içe
// işlem açmak PostgreSQL'de savepoint demektir ve dıştaki işlemin atomikliği
// konusunda yanıltıcı bir güven verirdi.
func (r *Repository) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return classify(err, "inventory_tx_begin_failed", "işlem başlatılamadı")
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// Bağlamdan bağımsız kısa ömürlü bir context kullanılır: çağıranın ctx'i
		// iptal edilmişse onunla yapılan geri alma da anında düşerdi.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if err := fn(context.WithValue(ctx, txKey, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return classify(err, "inventory_tx_commit_failed", "işlem tamamlanamadı")
	}
	committed = true
	return nil
}

// txFromContext context'teki işlem tutamağını döner.
func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey).(pgx.Tx)
	return tx, ok
}

// queries context'e uygun sorgu kümesini döner: işlem varsa ona, yoksa havuza
// bağlı olanı.
func (r *Repository) queries(ctx context.Context) *inventorydb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return inventorydb.New(tx)
	}
	return inventorydb.New(r.pool)
}

// requireTx kilit alan metotların işlem içinde çağrıldığını doğrular.
func requireTx(ctx context.Context, op string) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"%s işlem (transaction) içinde çağrılmalı; işlemsiz bir FOR UPDATE kilidi hiçbir şeyi korumaz", op)
	}
	return nil
}

// --- stok lokasyonları -------------------------------------------------------

// CreateStockLocation yeni bir stok lokasyonu kaydeder.
func (r *Repository) CreateStockLocation(ctx context.Context, loc models.StockLocation) (models.StockLocation, error) {
	row, err := r.queries(ctx).CreateStockLocation(ctx, inventorydb.CreateStockLocationParams{
		ID:          loc.ID,
		Name:        loc.Name,
		Address1:    nullString(loc.Address1),
		Address2:    nullString(loc.Address2),
		City:        nullString(loc.City),
		Province:    nullString(loc.Province),
		PostalCode:  nullString(loc.PostalCode),
		CountryCode: nullString(loc.CountryCode),
	})
	if err != nil {
		return models.StockLocation{}, classify(err, codeQueryFailed, "stok lokasyonu oluşturulamadı")
	}
	return toStockLocation(row), nil
}

// GetStockLocation lokasyonu kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetStockLocation(ctx context.Context, id string) (models.StockLocation, error) {
	row, err := r.queries(ctx).GetStockLocation(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.StockLocation{}, errors.NotFound(codeLocationNotFound,
				"stok lokasyonu bulunamadı: %s", id)
		}
		return models.StockLocation{}, classify(err, codeQueryFailed, "stok lokasyonu okunamadı")
	}
	return toStockLocation(row), nil
}

// ListStockLocations lokasyonları sayfalayarak döner. İkinci dönüş değeri
// sayfaya değil, filtreye uyan TÜM satırlara ait toplam sayıdır.
//
// Toplam AYRI bir sorgudan gelir; sayfa aralık dışında olsa ve hiç satır
// dönmese de doğrudur (bkz. queries/stock_locations.sql).
func (r *Repository) ListStockLocations(ctx context.Context, limit, offset int64) ([]models.StockLocation, int64, error) {
	rows, err := r.queries(ctx).ListStockLocations(ctx, inventorydb.ListStockLocationsParams{
		RowLimit:  limit,
		RowOffset: offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "stok lokasyonları listelenemedi")
	}

	total, err := r.queries(ctx).CountStockLocations(ctx)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "stok lokasyonları sayılamadı")
	}

	out := make([]models.StockLocation, 0, len(rows))
	for i := range rows {
		out = append(out, toStockLocation(rows[i]))
	}
	return out, total, nil
}

// --- stok kalemleri ----------------------------------------------------------

// CreateInventoryItem yeni bir stok kalemi kaydeder.
// Aynı SKU yaşayan bir kalemde varsa Conflict döner.
func (r *Repository) CreateInventoryItem(ctx context.Context, item models.InventoryItem) (models.InventoryItem, error) {
	row, err := r.queries(ctx).CreateInventoryItem(ctx, inventorydb.CreateInventoryItemParams{
		ID:               item.ID,
		Sku:              item.SKU,
		Title:            nullString(item.Title),
		Description:      nullString(item.Description),
		RequiresShipping: item.RequiresShipping,
	})
	if err != nil {
		return models.InventoryItem{}, classify(err, codeQueryFailed, "stok kalemi oluşturulamadı")
	}
	return toInventoryItem(row), nil
}

// GetInventoryItem kalemi kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetInventoryItem(ctx context.Context, id string) (models.InventoryItem, error) {
	row, err := r.queries(ctx).GetInventoryItem(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.InventoryItem{}, errors.NotFound(codeItemNotFound, "stok kalemi bulunamadı: %s", id)
		}
		return models.InventoryItem{}, classify(err, codeQueryFailed, "stok kalemi okunamadı")
	}
	return toInventoryItem(row), nil
}

// LockInventoryItem kalemi işlem boyunca kilitler ve var olduğunu doğrular.
// İşlem dışında çağrılırsa hata döner.
func (r *Repository) LockInventoryItem(ctx context.Context, id string) error {
	if err := requireTx(ctx, "LockInventoryItem"); err != nil {
		return err
	}
	if _, err := r.queries(ctx).LockInventoryItem(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.NotFound(codeItemNotFound, "stok kalemi bulunamadı: %s", id)
		}
		return classify(err, codeQueryFailed, "stok kalemi kilitlenemedi")
	}
	return nil
}

// LockInventoryItemShared kalemi işlem boyunca PAYLAŞIMLI kilitler ve var
// olduğunu doğrular. İşlem dışında çağrılırsa hata döner.
//
// Seviye ya da rezervasyon satırına dokunan akışlar kilit sırasının ilk adımı
// olarak bunu alır; paylaşımlı olduğu için eşzamanlı rezervasyonları birbirine
// beklettirmez ama [Repository.LockInventoryItem]'ın dışlayıcı kilidiyle
// çakışır (bkz. queries/inventory_items.sql).
func (r *Repository) LockInventoryItemShared(ctx context.Context, id string) error {
	if err := requireTx(ctx, "LockInventoryItemShared"); err != nil {
		return err
	}
	if _, err := r.queries(ctx).LockInventoryItemShared(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.NotFound(codeItemNotFound, "stok kalemi bulunamadı: %s", id)
		}
		return classify(err, codeQueryFailed, "stok kalemi kilitlenemedi")
	}
	return nil
}

// ListInventoryItems kalemleri filtreleyerek ve sayfalayarak döner.
// İkinci dönüş değeri filtreye uyan TÜM satırların sayısıdır.
//
// Toplam AYRI bir sorgudan gelir ve listeyle aynı filtreleri uygular; sayfa
// aralık dışında olsa ve hiç satır dönmese de doğrudur. İki sorgu arasında
// yazılan bir satır toplamı bir değiştirebilir: toplam, sayfalama zarfının
// bilgilendirici alanıdır, işlem kararı ona dayandırılmaz.
func (r *Repository) ListInventoryItems(ctx context.Context, filter models.InventoryItemFilter) ([]models.InventoryItem, int64, error) {
	rows, err := r.queries(ctx).ListInventoryItems(ctx, inventorydb.ListInventoryItemsParams{
		Sku:              filter.SKU,
		RequiresShipping: filter.RequiresShipping,
		RowLimit:         filter.Limit,
		RowOffset:        filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "stok kalemleri listelenemedi")
	}

	total, err := r.queries(ctx).CountInventoryItems(ctx, inventorydb.CountInventoryItemsParams{
		Sku:              filter.SKU,
		RequiresShipping: filter.RequiresShipping,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "stok kalemleri sayılamadı")
	}

	out := make([]models.InventoryItem, 0, len(rows))
	for i := range rows {
		out = append(out, toInventoryItem(rows[i]))
	}
	return out, total, nil
}

// InventoryItemsByIDs verilen kimliklerin kalemlerini TEK sorguda döner.
// Bulunamayan kimlik için satır dönmez; bu bir hata değildir.
func (r *Repository) InventoryItemsByIDs(ctx context.Context, ids []string) ([]models.InventoryItem, error) {
	if len(ids) == 0 {
		return []models.InventoryItem{}, nil
	}
	rows, err := r.queries(ctx).GetInventoryItemsByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "stok kalemleri okunamadı")
	}

	out := make([]models.InventoryItem, 0, len(rows))
	for i := range rows {
		out = append(out, toInventoryItem(rows[i]))
	}
	return out, nil
}

// SoftDeleteInventoryItem kalemi yumuşak siler (deleted_at). Kalem yoksa ya da
// zaten silinmişse NotFound döner.
func (r *Repository) SoftDeleteInventoryItem(ctx context.Context, id string) error {
	affected, err := r.queries(ctx).SoftDeleteInventoryItem(ctx, id)
	if err != nil {
		return classify(err, codeQueryFailed, "stok kalemi silinemedi")
	}
	if affected == 0 {
		return errors.NotFound(codeItemNotFound, "stok kalemi bulunamadı: %s", id)
	}
	return nil
}

// SoftDeleteInventoryLevelsByItem kalemin tüm stok seviyelerini yumuşak siler.
func (r *Repository) SoftDeleteInventoryLevelsByItem(ctx context.Context, itemID string) error {
	if err := r.queries(ctx).SoftDeleteInventoryLevelsByItem(ctx, itemID); err != nil {
		return classify(err, codeQueryFailed, "stok seviyeleri silinemedi")
	}
	return nil
}

// --- stok seviyeleri ---------------------------------------------------------

// LockInventoryLevel (kalem, lokasyon) seviyesini işlem boyunca kilitler ve
// güncel hâlini döner. Seviye yoksa NotFound; işlem dışında çağrılırsa hata.
//
// Stok adedini değiştiren her akış okumasını BU metotla yapar: kilit
// alınmadan okunan bir adet, yazma anında bayat olabilirdi.
func (r *Repository) LockInventoryLevel(ctx context.Context, itemID, locationID string) (models.InventoryLevel, error) {
	if err := requireTx(ctx, "LockInventoryLevel"); err != nil {
		return models.InventoryLevel{}, err
	}
	row, err := r.queries(ctx).LockInventoryLevel(ctx, inventorydb.LockInventoryLevelParams{
		InventoryItemID: itemID,
		LocationID:      locationID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.InventoryLevel{}, levelNotFound(itemID, locationID)
		}
		return models.InventoryLevel{}, classify(err, codeQueryFailed, "stok seviyesi kilitlenemedi")
	}
	return toInventoryLevel(row), nil
}

// CreateInventoryLevel yeni bir stok seviyesi kaydeder.
func (r *Repository) CreateInventoryLevel(ctx context.Context, level models.InventoryLevel) (models.InventoryLevel, error) {
	row, err := r.queries(ctx).CreateInventoryLevel(ctx, inventorydb.CreateInventoryLevelParams{
		ID:               level.ID,
		InventoryItemID:  level.InventoryItemID,
		LocationID:       level.LocationID,
		StockedQuantity:  level.StockedQuantity,
		ReservedQuantity: level.ReservedQuantity,
	})
	if err != nil {
		return models.InventoryLevel{}, classify(err, codeQueryFailed, "stok seviyesi oluşturulamadı")
	}
	return toInventoryLevel(row), nil
}

// UpdateInventoryLevelQuantities seviyenin adetlerini MUTLAK değerlerle yazar.
//
// Artımlı (quantity = quantity + n) bir güncelleme kasten kullanılmaz: yeni
// değer, kilit altında okunan değerden hesaplanır ve kararı veren kodun
// gördüğü sayı ile yazılan sayı aynı olur.
func (r *Repository) UpdateInventoryLevelQuantities(ctx context.Context, levelID string, stocked, reserved int64) (models.InventoryLevel, error) {
	row, err := r.queries(ctx).UpdateInventoryLevelQuantities(ctx, inventorydb.UpdateInventoryLevelQuantitiesParams{
		ID:               levelID,
		StockedQuantity:  stocked,
		ReservedQuantity: reserved,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.InventoryLevel{}, errors.NotFound(codeLevelNotFound,
				"stok seviyesi bulunamadı: %s", levelID)
		}
		return models.InventoryLevel{}, classify(err, codeQueryFailed, "stok seviyesi güncellenemedi")
	}
	return toInventoryLevel(row), nil
}

// ListInventoryLevels kalemin tüm lokasyonlardaki seviyelerini döner.
func (r *Repository) ListInventoryLevels(ctx context.Context, itemID string) ([]models.InventoryLevel, error) {
	rows, err := r.queries(ctx).ListInventoryLevels(ctx, itemID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "stok seviyeleri listelenemedi")
	}

	out := make([]models.InventoryLevel, 0, len(rows))
	for i := range rows {
		out = append(out, toInventoryLevel(rows[i]))
	}
	return out, nil
}

// AvailableByItemIDs kalem başına TÜM lokasyonların satılabilir toplamını
// TEK sorguda döner. Hiç seviyesi olmayan kalem sonuçta yer almaz; çağıran
// onu sıfır saymalıdır.
func (r *Repository) AvailableByItemIDs(ctx context.Context, ids []string) (map[string]int64, error) {
	if len(ids) == 0 {
		return map[string]int64{}, nil
	}
	rows, err := r.queries(ctx).AvailableQuantityByItemIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "satılabilir adet hesaplanamadı")
	}

	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.InventoryItemID] = row.AvailableQuantity
	}
	return out, nil
}

// --- rezervasyonlar ----------------------------------------------------------

// CreateReservation yeni bir rezervasyon kaydeder.
func (r *Repository) CreateReservation(ctx context.Context, res models.Reservation) (models.Reservation, error) {
	row, err := r.queries(ctx).CreateReservation(ctx, inventorydb.CreateReservationParams{
		ID:              res.ID,
		InventoryItemID: res.InventoryItemID,
		LocationID:      res.LocationID,
		Quantity:        res.Quantity,
		LineItemID:      nullString(res.LineItemID),
		Status:          res.Status.String(),
		Description:     nullString(res.Description),
	})
	if err != nil {
		return models.Reservation{}, classify(err, codeQueryFailed, "rezervasyon oluşturulamadı")
	}
	return toReservation(row), nil
}

// LockReservation rezervasyonu işlem boyunca kilitler ve güncel hâlini döner.
//
// Durum geçişleri yalnızca bu kilit altında yapılır: aynı rezervasyonu aynı
// anda serbest bırakmaya çalışan iki çağrıdan ikincisi, birincinin yazdığı
// durumu görür ve stoğu ikinci kez iade etmez.
func (r *Repository) LockReservation(ctx context.Context, id string) (models.Reservation, error) {
	if err := requireTx(ctx, "LockReservation"); err != nil {
		return models.Reservation{}, err
	}
	row, err := r.queries(ctx).LockReservation(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Reservation{}, errors.NotFound(codeReservationNotFound,
				"rezervasyon bulunamadı: %s", id)
		}
		return models.Reservation{}, classify(err, codeQueryFailed, "rezervasyon kilitlenemedi")
	}
	return toReservation(row), nil
}

// GetReservation rezervasyonu kilitlemeden döner; yoksa NotFound.
func (r *Repository) GetReservation(ctx context.Context, id string) (models.Reservation, error) {
	row, err := r.queries(ctx).GetReservation(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Reservation{}, errors.NotFound(codeReservationNotFound,
				"rezervasyon bulunamadı: %s", id)
		}
		return models.Reservation{}, classify(err, codeQueryFailed, "rezervasyon okunamadı")
	}
	return toReservation(row), nil
}

// SetReservationStatus rezervasyonun durumunu yazar; kayıt yoksa NotFound.
func (r *Repository) SetReservationStatus(ctx context.Context, id string, status models.ReservationStatus) error {
	affected, err := r.queries(ctx).SetReservationStatus(ctx, inventorydb.SetReservationStatusParams{
		ID:     id,
		Status: status.String(),
	})
	if err != nil {
		return classify(err, codeQueryFailed, "rezervasyon durumu güncellenemedi")
	}
	if affected == 0 {
		return errors.NotFound(codeReservationNotFound, "rezervasyon bulunamadı: %s", id)
	}
	return nil
}

// CountActiveReservations kalemin aktif rezervasyon sayısını döner.
func (r *Repository) CountActiveReservations(ctx context.Context, itemID string) (int64, error) {
	count, err := r.queries(ctx).CountActiveReservationsByItem(ctx, itemID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "aktif rezervasyonlar sayılamadı")
	}
	return count, nil
}

// --- çeviri ve hata sınıflandırma --------------------------------------------

// levelNotFound eksik seviye için ortak hatayı üretir.
func levelNotFound(itemID, locationID string) error {
	return errors.NotFound(codeLevelNotFound,
		"stok seviyesi bulunamadı (kalem: %s, lokasyon: %s)", itemID, locationID)
}

// classify sürücü hatasını tipli hataya çevirir.
//
// Benzersizlik, foreign key ve CHECK ihlalleri istemcinin düzeltebileceği
// durumlardır; sınıflandırılmazsa hepsi 500 olarak görünür ve gerçek sebep
// yalnızca logda kalırdı. Kilitlenme (deadlock) de aynı sebeple ayrı ele
// alınır: işlemin kendisinde bir yanlışlık yoktur, YENİDEN DENENEBİLİR.
func classify(err error, code, format string, a ...any) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}

	switch pgErr.Code {
	case sqlStateUniqueViolation:
		switch pgErr.ConstraintName {
		case constraintSKUUniq:
			return errors.Wrap(err, errors.KindConflict, codeSKUExists,
				"bu SKU zaten kullanılıyor")
		case constraintLevelUniq:
			return errors.Wrap(err, errors.KindConflict, codeLevelExists,
				"bu kalem için bu lokasyonda stok seviyesi zaten var")
		}
	case sqlStateForeignKeyViolation:
		// Seviye/rezervasyon satırı, olmayan bir kaleme ya da lokasyona
		// bağlanamaz. Hangisi olduğunu kısıt adı söyler.
		if strings.Contains(pgErr.ConstraintName, "location_id") {
			return errors.Wrap(err, errors.KindNotFound, codeLocationNotFound,
				"stok lokasyonu bulunamadı")
		}
		return errors.Wrap(err, errors.KindNotFound, codeItemNotFound,
			"stok kalemi bulunamadı")
	case sqlStateCheckViolation:
		switch pgErr.ConstraintName {
		case constraintAvailable, constraintStockedNonneg:
			return errors.Wrap(err, errors.KindConflict, codeInsufficientStock,
				"stok yetersiz: satılabilir adet negatife düşemez")
		}
	case sqlStateDeadlockDetected:
		// Kilit sırası tekleştirildiği için normal akışlarda oluşmaz; burası
		// son savunmadır. İşlem geri alınmıştır, aynı istek olduğu gibi
		// yeniden denenebilir — bu yüzden Internal (500) değil Conflict.
		return errors.Wrap(err, errors.KindConflict, codeConcurrentUpdate,
			"eşzamanlı bir işlemle çakışıldı; istek yeniden denenebilir")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// nullString boş dizeyi SQL NULL'a çevirir.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// stringValue SQL NULL'ı boş dizeye çevirir.
func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// timeValue pgtype damgasını UTC time.Time'a çevirir.
func timeValue(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toStockLocation üretilmiş satırı alan modeline çevirir.
func toStockLocation(row inventorydb.StockLocation) models.StockLocation {
	return models.StockLocation{
		ID:          row.ID,
		Name:        row.Name,
		Address1:    stringValue(row.Address1),
		Address2:    stringValue(row.Address2),
		City:        stringValue(row.City),
		Province:    stringValue(row.Province),
		PostalCode:  stringValue(row.PostalCode),
		CountryCode: stringValue(row.CountryCode),
		CreatedAt:   timeValue(row.CreatedAt),
		UpdatedAt:   timeValue(row.UpdatedAt),
	}
}

// toInventoryItem üretilmiş satırı alan modeline çevirir.
func toInventoryItem(row inventorydb.InventoryItem) models.InventoryItem {
	return models.InventoryItem{
		ID:               row.ID,
		SKU:              row.Sku,
		Title:            stringValue(row.Title),
		Description:      stringValue(row.Description),
		RequiresShipping: row.RequiresShipping,
		CreatedAt:        timeValue(row.CreatedAt),
		UpdatedAt:        timeValue(row.UpdatedAt),
	}
}

// toInventoryLevel üretilmiş satırı alan modeline çevirir.
func toInventoryLevel(row inventorydb.InventoryLevel) models.InventoryLevel {
	return models.InventoryLevel{
		ID:               row.ID,
		InventoryItemID:  row.InventoryItemID,
		LocationID:       row.LocationID,
		StockedQuantity:  row.StockedQuantity,
		ReservedQuantity: row.ReservedQuantity,
		CreatedAt:        timeValue(row.CreatedAt),
		UpdatedAt:        timeValue(row.UpdatedAt),
	}
}

// toReservation üretilmiş satırı alan modeline çevirir.
func toReservation(row inventorydb.InventoryReservation) models.Reservation {
	return models.Reservation{
		ID:              row.ID,
		InventoryItemID: row.InventoryItemID,
		LocationID:      row.LocationID,
		Quantity:        row.Quantity,
		LineItemID:      stringValue(row.LineItemID),
		Description:     stringValue(row.Description),
		Status:          models.ReservationStatus(row.Status),
		CreatedAt:       timeValue(row.CreatedAt),
		UpdatedAt:       timeValue(row.UpdatedAt),
	}
}
