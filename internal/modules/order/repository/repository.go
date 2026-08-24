// Package repository order modülünün veritabanı erişimidir.
//
// SADECE bu modülün tablolarına dokunur (plan Bölüm 4). sqlc üretimi kod
// repository/orderdb altındadır ve elle düzenlenmez; bu paket onun üstüne iki
// şey ekler:
//
//   - Çeviri: pgtype ve üretilmiş satır tipleri BU PAKETİN DIŞINA ÇIKMAZ,
//     models tiplerine çevrilir (bkz. convert.go).
//   - Sınıflandırma: sürücü hataları core/errors tipli hatalarına çevrilir;
//     satır bulunamaması NotFound, benzersizlik ihlali Conflict, kimlik
//     ihlali Invalid olur.
//
// # İşlem (transaction) taşınması
//
// [Repository.WithTx] bir işlem açar ve onu CONTEXT'e koyar; işlem boyunca
// çağrılan tüm repository metodları o context'i aldıkları sürece aynı işlemde
// çalışır. Bunun alternatifi, işlem tutamağını taşıyan ayrı bir arayüz tipini
// metot imzalarına koymaktı; o durumda servis kendi paketinde tanımladığı dar
// arayüzle bu paketi YAPISAL OLARAK eşleştiremezdi — Go'da imzadaki adlandırılmış
// tipler birebir aynı olmak zorundadır, yani servis repository'yi import etmek
// zorunda kalırdı (ADR 0001 bunu yasaklar). Context ile taşımak imzaları iki
// tarafın da paylaştığı tiplere (context.Context, models.*) indirger.
//
// [Repository.LockOrder] işlem DIŞINDA çağrılırsa hata döner: FOR UPDATE kilidi
// işlem bitince serbest kalacağı için, işlemsiz bir kilit sessizce hiçbir şeyi
// korumazdı.
//
// # Durum geçişleri neden burada da korunur
//
// CancelOrder / CompleteOrder / ArchiveOrder sorguları WHERE koşuluna BEKLENEN
// DURUMU yazar. Servis aynı kontrolü kilit altında ve okunabilir bir hatayla
// zaten yapar; buradaki koşul, doğrudan SQL ile ya da servisin kilit
// çerçevesini atlayan bir çağrıyla yapılan geçişi de kapsayan ikinci kapıdır.
// Hiç satır etkilenmezse Conflict döner: satır kilit altında OKUNMUŞTUR, yani
// yokluğu değil DURUMUNUN DEĞİŞMİŞ olması tek açıklamadır.
package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// rollbackTimeout iptal edilmiş bir bağlamda geri almaya tanınan süredir.
// Geri alma, çağıranın ctx'i dolmuş olsa da denenmelidir; aksi hâlde işlem
// bağlantı havuza dönene kadar açık kalırdı.
const rollbackTimeout = 5 * time.Second

// txKeyType context anahtarının tipidir; dışarıdan üretilemesin diye dışa
// açık değildir.
type txKeyType struct{}

// txKey işlem tutamağının context'teki anahtarıdır.
var txKey = txKeyType{}

// Repository order tablolarına erişimdir. Eşzamanlı kullanıma güvenlidir.
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
		return classify(err, "order_tx_begin_failed", "işlem başlatılamadı")
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// Bağlamdan bağımsız kısa ömürlü bir context kullanılır: çağıranın
		// ctx'i iptal edilmişse onunla yapılan geri alma da anında düşerdi.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if err := fn(context.WithValue(ctx, txKey, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return classify(err, "order_tx_commit_failed", "işlem tamamlanamadı")
	}
	committed = true
	return nil
}

// WithReadTx fn'i salt-okunur ve REPEATABLE READ bir işlemde çalıştırır.
//
// Birden çok sorgusu olan bir OKUMA yolu içindir (servisin GetOrder'ı siparişi,
// satırları ve özeti ayrı sorgularla getirir): sorguların hepsi siparişin AYNI
// hâlini görsün diye. Kilit alınmaz.
//
// # Neden REPEATABLE READ
//
// PostgreSQL'in varsayılanı READ COMMITTED'dır ve orada anlık görüntü İŞLEM
// başına değil DEYİM başına alınır; sorguları sıradan bir işleme sarmak yırtık
// görünümü engellemezdi. Görüntüyü işlemin ilk deyiminde dondurup sonuna kadar
// koruyan düzey REPEATABLE READ'dir. Salt-okunur işaretlenmesi de bilinçlidir:
// bu yolun yanlışlıkla yazması veritabanı tarafından engellenir ve yazma
// düzeyinde REPEATABLE READ'in getireceği serileştirme hataları hiç doğmaz.
//
// İşlem zaten açıksa yeni bir tane AÇILMAZ, var olan kullanılır: bu yol bir
// yazma işleminin içinden çağrıldığında, o işlemin görüntüsü zaten tutarlıdır
// ve dıştaki işlemin yalıtım düzeyini içeriden değiştirmeye çalışmak hata
// verirdi.
func (r *Repository) WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return classify(err, "order_tx_begin_failed", "salt-okunur işlem başlatılamadı")
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		// Salt-okunur işlemde yazılacak bir şey yoktur; commit ile rollback
		// aynı kapıya çıkar ve rollback iptal edilmiş bir bağlamda da çalışır.
		_ = tx.Rollback(rollbackCtx)
	}()

	return fn(context.WithValue(ctx, txKey, tx))
}

// txFromContext context'teki işlem tutamağını döner.
func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey).(pgx.Tx)
	return tx, ok
}

// queries context'e uygun sorgu kümesini döner: işlem varsa ona, yoksa havuza
// bağlı olanı.
func (r *Repository) queries(ctx context.Context) *orderdb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return orderdb.New(tx)
	}
	return orderdb.New(r.pool)
}

// requireTx kilit alan metotların işlem içinde çağrıldığını doğrular.
func requireTx(ctx context.Context, op string) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"%s işlem (transaction) içinde çağrılmalı; işlemsiz bir FOR UPDATE kilidi hiçbir şeyi korumaz", op)
	}
	return nil
}

// orderNotFound eksik sipariş için ortak hatayı üretir.
func orderNotFound(id string) error {
	return errors.NotFound(codeOrderNotFound, "sipariş bulunamadı: %s", id)
}

// stateChanged durum koşulu tutmayan bir geçiş için ortak hatayı üretir.
func stateChanged(id, op string) error {
	return errors.Conflict(codeStateChanged,
		"%s uygulanamadı: siparişin durumu beklenenden farklı (%s)", op, id)
}

// --- siparişler --------------------------------------------------------------

// CreateOrder yeni bir sipariş kaydeder.
//
// display_id parametre olarak VERİLMEZ; değeri veritabanının IDENTITY sütunu
// üretir ve RETURNING ile geri okunur. Eşzamanlı iki çağrının aynı numarayı
// alması bu yüzden imkânsızdır.
func (r *Repository) CreateOrder(ctx context.Context, order models.Order) (models.Order, error) {
	meta, err := fromJSONMap(order.Metadata)
	if err != nil {
		return models.Order{}, err
	}

	row, err := r.queries(ctx).CreateOrder(ctx, orderdb.CreateOrderParams{
		ID:             order.ID,
		Status:         order.Status.String(),
		RegionID:       order.RegionID,
		CustomerID:     nullString(order.CustomerID),
		Email:          nullString(order.Email),
		CurrencyCode:   order.CurrencyCode,
		CartID:         nullString(order.CartID),
		IdempotencyKey: nullString(order.IdempotencyKey),
		Subtotal:       order.Subtotal,
		DiscountTotal:  order.DiscountTotal,
		TaxTotal:       order.TaxTotal,
		ShippingTotal:  order.ShippingTotal,
		Total:          order.Total,
		Metadata:       meta,
	})
	if err != nil {
		return models.Order{}, classify(err, codeQueryFailed, "sipariş oluşturulamadı")
	}
	return toOrder(row)
}

// GetOrder siparişi kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetOrder(ctx context.Context, id string) (models.Order, error) {
	row, err := r.queries(ctx).GetOrder(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, orderNotFound(id)
		}
		return models.Order{}, classify(err, codeQueryFailed, "sipariş okunamadı")
	}
	return toOrder(row)
}

// GetOrderByDisplayID siparişi insan okunur numarasıyla döner; yoksa NotFound.
func (r *Repository) GetOrderByDisplayID(ctx context.Context, displayID int64) (models.Order, error) {
	row, err := r.queries(ctx).GetOrderByDisplayID(ctx, displayID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, errors.NotFound(codeOrderNotFound,
				"sipariş bulunamadı: #%d", displayID)
		}
		return models.Order{}, classify(err, codeQueryFailed, "sipariş okunamadı")
	}
	return toOrder(row)
}

// GetOrderByIdempotencyKey anahtarla açılmış siparişi döner; yoksa NotFound.
func (r *Repository) GetOrderByIdempotencyKey(ctx context.Context, key string) (models.Order, error) {
	row, err := r.queries(ctx).GetOrderByIdempotencyKey(ctx, &key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, errors.NotFound(codeOrderNotFound,
				"bu idempotency anahtarıyla sipariş bulunamadı")
		}
		return models.Order{}, classify(err, codeQueryFailed, "sipariş okunamadı")
	}
	return toOrder(row)
}

// LockOrder siparişi işlem boyunca kilitler ve güncel hâlini döner.
func (r *Repository) LockOrder(ctx context.Context, id string) (models.Order, error) {
	if err := requireTx(ctx, "LockOrder"); err != nil {
		return models.Order{}, err
	}
	row, err := r.queries(ctx).LockOrder(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, orderNotFound(id)
		}
		return models.Order{}, classify(err, codeQueryFailed, "sipariş kilitlenemedi")
	}
	return toOrder(row)
}

// ListOrders siparişleri filtreleyip sayfalar; ikinci değer toplam sayıdır.
func (r *Repository) ListOrders(ctx context.Context, filter models.OrderFilter) ([]models.Order, int64, error) {
	var status *string
	if filter.Status != nil {
		value := filter.Status.String()
		status = &value
	}

	rows, err := r.queries(ctx).ListOrders(ctx, orderdb.ListOrdersParams{
		CustomerID: filter.CustomerID,
		RegionID:   filter.RegionID,
		Status:     status,
		RowLimit:   filter.Limit,
		RowOffset:  filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "siparişler listelenemedi")
	}

	total, err := r.queries(ctx).CountOrders(ctx, orderdb.CountOrdersParams{
		CustomerID: filter.CustomerID,
		RegionID:   filter.RegionID,
		Status:     status,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "siparişler sayılamadı")
	}

	orders, err := toOrders(rows)
	if err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

// OrdersByIDs kimlik kümesini TEK sorguda getirir (N+1 yok).
func (r *Repository) OrdersByIDs(ctx context.Context, ids []string) ([]models.Order, error) {
	if len(ids) == 0 {
		return []models.Order{}, nil
	}
	rows, err := r.queries(ctx).GetOrdersByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "siparişler okunamadı")
	}
	return toOrders(rows)
}

// CancelOrder siparişi iptal eder ve iptal anını damgalar.
//
// Yalnızca 'pending' durumundaki sipariş iptal edilir; başka durumda hiçbir
// satır etkilenmez ve Conflict döner (bkz. paket belgesi).
func (r *Repository) CancelOrder(ctx context.Context, id, reason string) (models.Order, error) {
	row, err := r.queries(ctx).CancelOrder(ctx, orderdb.CancelOrderParams{
		ID:           id,
		CancelReason: nullString(reason),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, stateChanged(id, "iptal")
		}
		return models.Order{}, classify(err, codeQueryFailed, "sipariş iptal edilemedi")
	}
	return toOrder(row)
}

// CompleteOrder siparişi tamamlanmış olarak damgalar.
func (r *Repository) CompleteOrder(ctx context.Context, id string) (models.Order, error) {
	row, err := r.queries(ctx).CompleteOrder(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, stateChanged(id, "tamamlama")
		}
		return models.Order{}, classify(err, codeQueryFailed, "sipariş tamamlanamadı")
	}
	return toOrder(row)
}

// ArchiveOrder tamamlanmış bir siparişi arşive alır.
func (r *Repository) ArchiveOrder(ctx context.Context, id string) (models.Order, error) {
	row, err := r.queries(ctx).ArchiveOrder(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, stateChanged(id, "arşivleme")
		}
		return models.Order{}, classify(err, codeQueryFailed, "sipariş arşivlenemedi")
	}
	return toOrder(row)
}

// --- sipariş satırları -------------------------------------------------------

// CreateLineItem yeni bir sipariş satırı kaydeder.
func (r *Repository) CreateLineItem(ctx context.Context, item models.OrderLineItem) (models.OrderLineItem, error) {
	meta, err := fromJSONMap(item.Metadata)
	if err != nil {
		return models.OrderLineItem{}, err
	}

	row, err := r.queries(ctx).CreateOrderLineItem(ctx, orderdb.CreateOrderLineItemParams{
		ID:            item.ID,
		OrderID:       item.OrderID,
		VariantID:     item.VariantID,
		Title:         item.Title,
		Quantity:      item.Quantity,
		UnitPrice:     item.UnitPrice,
		Subtotal:      item.Subtotal,
		DiscountTotal: item.DiscountTotal,
		TaxTotal:      item.TaxTotal,
		Total:         item.Total,
		Metadata:      meta,
	})
	if err != nil {
		return models.OrderLineItem{}, classify(err, codeQueryFailed, "sipariş satırı oluşturulamadı")
	}
	return toLineItem(row)
}

// ListLineItems siparişin satırlarını oluşturulma sırasıyla döner.
func (r *Repository) ListLineItems(ctx context.Context, orderID string) ([]models.OrderLineItem, error) {
	rows, err := r.queries(ctx).ListOrderLineItems(ctx, orderID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "sipariş satırları okunamadı")
	}
	return toLineItems(rows)
}

// --- özet --------------------------------------------------------------------

// CreateSummary siparişin özet kaydını sıfırlanmış olarak açar.
func (r *Repository) CreateSummary(ctx context.Context, summary models.OrderSummary) (models.OrderSummary, error) {
	row, err := r.queries(ctx).CreateOrderSummary(ctx, orderdb.CreateOrderSummaryParams{
		ID:      summary.ID,
		OrderID: summary.OrderID,
	})
	if err != nil {
		return models.OrderSummary{}, classify(err, codeQueryFailed, "sipariş özeti oluşturulamadı")
	}
	return toSummary(row), nil
}

// GetSummary siparişin özetini döner; yoksa NotFound.
func (r *Repository) GetSummary(ctx context.Context, orderID string) (models.OrderSummary, error) {
	row, err := r.queries(ctx).GetOrderSummary(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.OrderSummary{}, errors.NotFound(codeSummaryNotFound,
				"sipariş özeti bulunamadı: %s", orderID)
		}
		return models.OrderSummary{}, classify(err, codeQueryFailed, "sipariş özeti okunamadı")
	}
	return toSummary(row), nil
}

// SetSummaryTotals ödenen ve iade edilen kümülatif tutarları BİRLEŞTİRİR.
//
// Birleştirme (GREATEST) sorgunun kendisindedir; gerekçesi için bkz.
// queries/order_summaries.sql. Her iki alan da yalnızca büyür, dolayısıyla
// gecikmiş ya da tekrarlanan bir ödeme olayı kaydedilmiş bir tutarı silemez.
func (r *Repository) SetSummaryTotals(ctx context.Context, orderID string, paid, refunded int64) (models.OrderSummary, error) {
	row, err := r.queries(ctx).SetOrderSummaryTotals(ctx, orderdb.SetOrderSummaryTotalsParams{
		OrderID:       orderID,
		PaidTotal:     paid,
		RefundedTotal: refunded,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.OrderSummary{}, errors.NotFound(codeSummaryNotFound,
				"sipariş özeti bulunamadı: %s", orderID)
		}
		return models.OrderSummary{}, classify(err, codeQueryFailed, "sipariş özeti yazılamadı")
	}
	return toSummary(row), nil
}

// --- iade / değişim / hasar --------------------------------------------------

// CreateReturn yeni bir iade kaydı açar.
func (r *Repository) CreateReturn(ctx context.Context, ret models.Return) (models.Return, error) {
	meta, err := fromJSONMap(ret.Metadata)
	if err != nil {
		return models.Return{}, err
	}

	row, err := r.queries(ctx).CreateOrderReturn(ctx, orderdb.CreateOrderReturnParams{
		ID:           ret.ID,
		OrderID:      ret.OrderID,
		Status:       ret.Status.String(),
		RefundAmount: ret.RefundAmount,
		Reason:       nullString(ret.Reason),
		Note:         nullString(ret.Note),
		Metadata:     meta,
	})
	if err != nil {
		return models.Return{}, classify(err, codeQueryFailed, "iade kaydı oluşturulamadı")
	}
	return toReturn(row)
}

// GetReturn iade kaydını kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetReturn(ctx context.Context, id string) (models.Return, error) {
	row, err := r.queries(ctx).GetOrderReturn(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Return{}, errors.NotFound(codeReturnNotFound, "iade kaydı bulunamadı: %s", id)
		}
		return models.Return{}, classify(err, codeQueryFailed, "iade kaydı okunamadı")
	}
	return toReturn(row)
}

// ListReturns siparişin iade kayıtlarını sayfalar; ikinci değer toplam sayıdır.
func (r *Repository) ListReturns(ctx context.Context, filter models.ChildFilter) ([]models.Return, int64, error) {
	rows, err := r.queries(ctx).ListOrderReturns(ctx, orderdb.ListOrderReturnsParams{
		OrderID:   filter.OrderID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "iade kayıtları listelenemedi")
	}
	total, err := r.queries(ctx).CountOrderReturns(ctx, filter.OrderID)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "iade kayıtları sayılamadı")
	}
	items, err := toReturns(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// CreateExchange yeni bir değişim kaydı açar.
func (r *Repository) CreateExchange(ctx context.Context, exchange models.Exchange) (models.Exchange, error) {
	meta, err := fromJSONMap(exchange.Metadata)
	if err != nil {
		return models.Exchange{}, err
	}

	row, err := r.queries(ctx).CreateOrderExchange(ctx, orderdb.CreateOrderExchangeParams{
		ID:            exchange.ID,
		OrderID:       exchange.OrderID,
		Status:        exchange.Status.String(),
		DifferenceDue: exchange.DifferenceDue,
		Note:          nullString(exchange.Note),
		Metadata:      meta,
	})
	if err != nil {
		return models.Exchange{}, classify(err, codeQueryFailed, "değişim kaydı oluşturulamadı")
	}
	return toExchange(row)
}

// GetExchange değişim kaydını kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetExchange(ctx context.Context, id string) (models.Exchange, error) {
	row, err := r.queries(ctx).GetOrderExchange(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Exchange{}, errors.NotFound(codeExchangeNotFound, "değişim kaydı bulunamadı: %s", id)
		}
		return models.Exchange{}, classify(err, codeQueryFailed, "değişim kaydı okunamadı")
	}
	return toExchange(row)
}

// ListExchanges siparişin değişim kayıtlarını sayfalar; ikinci değer toplam
// sayıdır.
func (r *Repository) ListExchanges(ctx context.Context, filter models.ChildFilter) ([]models.Exchange, int64, error) {
	rows, err := r.queries(ctx).ListOrderExchanges(ctx, orderdb.ListOrderExchangesParams{
		OrderID:   filter.OrderID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "değişim kayıtları listelenemedi")
	}
	total, err := r.queries(ctx).CountOrderExchanges(ctx, filter.OrderID)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "değişim kayıtları sayılamadı")
	}
	items, err := toExchanges(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// CreateClaim yeni bir hasar kaydı açar.
func (r *Repository) CreateClaim(ctx context.Context, claim models.Claim) (models.Claim, error) {
	meta, err := fromJSONMap(claim.Metadata)
	if err != nil {
		return models.Claim{}, err
	}

	row, err := r.queries(ctx).CreateOrderClaim(ctx, orderdb.CreateOrderClaimParams{
		ID:           claim.ID,
		OrderID:      claim.OrderID,
		ClaimType:    claim.Type.String(),
		Status:       claim.Status.String(),
		RefundAmount: claim.RefundAmount,
		Reason:       nullString(claim.Reason),
		Note:         nullString(claim.Note),
		Metadata:     meta,
	})
	if err != nil {
		return models.Claim{}, classify(err, codeQueryFailed, "hasar kaydı oluşturulamadı")
	}
	return toClaim(row)
}

// GetClaim hasar kaydını kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetClaim(ctx context.Context, id string) (models.Claim, error) {
	row, err := r.queries(ctx).GetOrderClaim(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Claim{}, errors.NotFound(codeClaimNotFound, "hasar kaydı bulunamadı: %s", id)
		}
		return models.Claim{}, classify(err, codeQueryFailed, "hasar kaydı okunamadı")
	}
	return toClaim(row)
}

// ListClaims siparişin hasar kayıtlarını sayfalar; ikinci değer toplam sayıdır.
func (r *Repository) ListClaims(ctx context.Context, filter models.ChildFilter) ([]models.Claim, int64, error) {
	rows, err := r.queries(ctx).ListOrderClaims(ctx, orderdb.ListOrderClaimsParams{
		OrderID:   filter.OrderID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "hasar kayıtları listelenemedi")
	}
	total, err := r.queries(ctx).CountOrderClaims(ctx, filter.OrderID)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "hasar kayıtları sayılamadı")
	}
	items, err := toClaims(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
