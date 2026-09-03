// Package repository cart modülünün veritabanı erişimidir.
//
// SADECE bu modülün tablolarına dokunur (plan Bölüm 4). sqlc üretimi kod
// repository/cartdb altındadır ve elle düzenlenmez; bu paket onun üstüne iki
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
// [Repository.LockCart] işlem DIŞINDA çağrılırsa hata döner: FOR UPDATE kilidi
// işlem bitince serbest kalacağı için, işlemsiz bir kilit sessizce hiçbir şeyi
// korumazdı.
package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/repository/cartdb"
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

// Repository cart tablolarına erişimdir. Eşzamanlı kullanıma güvenlidir.
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
		return classify(err, "cart_tx_begin_failed", "işlem başlatılamadı")
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
		return classify(err, "cart_tx_commit_failed", "işlem tamamlanamadı")
	}
	committed = true
	return nil
}

// WithReadTx fn'i salt-okunur ve REPEATABLE READ bir işlemde çalıştırır.
//
// Birden çok sorgusu olan bir OKUMA yolu içindir (servisin GetCart'ı sepeti,
// satırları, adresleri ve kargo yöntemlerini ayrı sorgularla getirir):
// sorguların hepsi sepetin AYNI hâlini görsün diye. Kilit alınmaz.
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
		return classify(err, "cart_tx_begin_failed", "salt-okunur işlem başlatılamadı")
	}
	defer func() {
		// Bağlamdan bağımsız kısa ömürlü bir context kullanılır: çağıranın
		// ctx'i iptal edilmişse onunla yapılan geri alma da anında düşerdi.
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
func (r *Repository) queries(ctx context.Context) *cartdb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return cartdb.New(tx)
	}
	return cartdb.New(r.pool)
}

// requireTx kilit alan metotların işlem içinde çağrıldığını doğrular.
func requireTx(ctx context.Context, op string) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"%s işlem (transaction) içinde çağrılmalı; işlemsiz bir FOR UPDATE kilidi hiçbir şeyi korumaz", op)
	}
	return nil
}

// cartNotFound eksik sepet için ortak hatayı üretir.
func cartNotFound(id string) error {
	return errors.NotFound(codeCartNotFound, "sepet bulunamadı: %s", id)
}

// --- sepetler ----------------------------------------------------------------

// CreateCart yeni bir sepet kaydeder.
func (r *Repository) CreateCart(ctx context.Context, cart models.Cart) (models.Cart, error) {
	meta, err := fromJSONMap(cart.Metadata)
	if err != nil {
		return models.Cart{}, err
	}

	row, err := r.queries(ctx).CreateCart(ctx, cartdb.CreateCartParams{
		ID:           cart.ID,
		RegionID:     cart.RegionID,
		CustomerID:   nullString(cart.CustomerID),
		Email:        nullString(cart.Email),
		CurrencyCode: cart.CurrencyCode,
		Metadata:     meta,
	})
	if err != nil {
		return models.Cart{}, classify(err, codeQueryFailed, "sepet oluşturulamadı")
	}
	return toCart(row)
}

// GetCart sepeti kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetCart(ctx context.Context, id string) (models.Cart, error) {
	row, err := r.queries(ctx).GetCart(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, cartNotFound(id)
		}
		return models.Cart{}, classify(err, codeQueryFailed, "sepet okunamadı")
	}
	return toCart(row)
}

// LockCart sepeti işlem boyunca kilitler ve güncel hâlini döner.
//
// Sepeti değiştiren HER akış bununla başlar; kilit sırası tektir ve her akışta
// aynıdır (önce sepet, sonra çocuk satırlar). Sepet yoksa NotFound; işlem
// dışında çağrılırsa hata döner.
func (r *Repository) LockCart(ctx context.Context, id string) (models.Cart, error) {
	if err := requireTx(ctx, "LockCart"); err != nil {
		return models.Cart{}, err
	}
	row, err := r.queries(ctx).LockCart(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, cartNotFound(id)
		}
		return models.Cart{}, classify(err, codeQueryFailed, "sepet kilitlenemedi")
	}
	return toCart(row)
}

// ListCarts sepetleri filtreleyerek ve sayfalayarak döner.
//
// İkinci dönüş değeri sayfaya değil, filtreye uyan TÜM satırlara ait sayıdır.
// Toplam AYRI bir sorgudan gelir ve listeyle aynı filtreleri uygular; sayfa
// aralık dışında olsa ve hiç satır dönmese de doğrudur. İki sorgu arasında
// yazılan bir satır toplamı bir değiştirebilir: toplam, sayfalama zarfının
// bilgilendirici alanıdır, işlem kararı ona dayandırılmaz.
func (r *Repository) ListCarts(ctx context.Context, filter models.CartFilter) ([]models.Cart, int64, error) {
	rows, err := r.queries(ctx).ListCarts(ctx, cartdb.ListCartsParams{
		CustomerID: filter.CustomerID,
		RegionID:   filter.RegionID,
		Completed:  filter.Completed,
		RowLimit:   filter.Limit,
		RowOffset:  filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "sepetler listelenemedi")
	}

	total, err := r.queries(ctx).CountCarts(ctx, cartdb.CountCartsParams{
		CustomerID: filter.CustomerID,
		RegionID:   filter.RegionID,
		Completed:  filter.Completed,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "sepetler sayılamadı")
	}

	carts, err := toCarts(rows)
	if err != nil {
		return nil, 0, err
	}
	return carts, total, nil
}

// CartsByIDs verilen kimliklerin sepetlerini TEK sorguda döner.
// Bulunamayan kimlik için satır dönmez; bu bir hata değildir.
func (r *Repository) CartsByIDs(ctx context.Context, ids []string) ([]models.Cart, error) {
	if len(ids) == 0 {
		return []models.Cart{}, nil
	}
	rows, err := r.queries(ctx).GetCartsByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "sepetler okunamadı")
	}
	return toCarts(rows)
}

// UpdateCartContact sepetin e-posta ve müşteri alanlarını MUTLAK değerle yazar.
//
// Boş dize NULL olarak saklanır: "e-postası yok" ile "e-postası boş metin"
// veritabanında iki ayrı durum olsaydı, aynı sepet iki farklı sorguda farklı
// görünürdü.
//
// Sepet yoksa, silinmişse ya da TAMAMLANMIŞSA satır güncellenmez; sorgunun
// WHERE'i tamamlanmış sepeti dışarıda bırakır ve bu durumda Conflict döner.
func (r *Repository) UpdateCartContact(ctx context.Context, id string, contact models.CartContact) (models.Cart, error) {
	row, err := r.queries(ctx).UpdateCartContact(ctx, cartdb.UpdateCartContactParams{
		ID:         id,
		Email:      nullString(contact.Email),
		CustomerID: nullString(contact.CustomerID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, r.writeBlocked(ctx, id, "sepet güncellenemedi")
		}
		return models.Cart{}, classify(err, codeQueryFailed, "sepetin iletişim alanları güncellenemedi")
	}
	return toCart(row)
}

// UpdateCartTotals sepetin toplam alanlarını yazar ve hangi şekil için
// hesaplandıklarını damgalar.
//
// Sepet yoksa, silinmişse ya da TAMAMLANMIŞSA satır güncellenmez; sorgunun
// WHERE'i tamamlanmış sepeti dışarıda bırakır ve bu durumda Conflict döner.
func (r *Repository) UpdateCartTotals(ctx context.Context, id string, totals models.CartTotals) (models.Cart, error) {
	row, err := r.queries(ctx).UpdateCartTotals(ctx, cartdb.UpdateCartTotalsParams{
		ID:             id,
		Subtotal:       totals.Subtotal,
		DiscountTotal:  totals.DiscountTotal,
		TaxTotal:       totals.TaxTotal,
		ShippingTotal:  totals.ShippingTotal,
		Total:          totals.Total,
		TotalsRevision: totals.Revision,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, r.writeBlocked(ctx, id, "toplamlar yazılamadı")
		}
		return models.Cart{}, classify(err, codeQueryFailed, "sepet toplamları güncellenemedi")
	}
	return toCart(row)
}

// BumpCartRevision sepetin şekil sayacını bir artırır.
//
// Toplamları etkileyen her yapısal değişiklikten sonra AYNI işlemde çağrılır;
// böylece toplamların bayatladığı [models.Cart.TotalsStale] ile okunabilir olur.
func (r *Repository) BumpCartRevision(ctx context.Context, id string) (models.Cart, error) {
	row, err := r.queries(ctx).BumpCartRevision(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, r.writeBlocked(ctx, id, "sepet güncellenemedi")
		}
		return models.Cart{}, classify(err, codeQueryFailed, "sepet sürümü artırılamadı")
	}
	return toCart(row)
}

// MarkCartCompleted sepeti tamamlanmış olarak damgalar.
//
// Sepet zaten tamamlanmışsa satır güncellenmez ve Conflict döner: aynı sepetten
// ikinci bir sipariş doğamaz.
func (r *Repository) MarkCartCompleted(ctx context.Context, id string) (models.Cart, error) {
	row, err := r.queries(ctx).MarkCartCompleted(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, r.writeBlocked(ctx, id, "sepet tamamlanamadı")
		}
		return models.Cart{}, classify(err, codeQueryFailed, "sepet tamamlanamadı")
	}
	return toCart(row)
}

// SoftDeleteCart sepeti yumuşak siler; sepet yoksa ya da zaten silinmişse
// NotFound döner.
func (r *Repository) SoftDeleteCart(ctx context.Context, id string) error {
	affected, err := r.queries(ctx).SoftDeleteCart(ctx, id)
	if err != nil {
		return classify(err, codeQueryFailed, "sepet silinemedi")
	}
	if affected == 0 {
		return cartNotFound(id)
	}
	return nil
}

// writeBlocked yazan bir sorgunun hiç satır etkilememesinin SEBEBİNİ okur.
//
// Sorguların WHERE'i "silinmemiş VE tamamlanmamış" der; sıfır satır iki farklı
// duruma karşılık gelir ve ikisi farklı hata sınıfıdır (404 vs 409). Sebebi
// yazmadan tek bir hata dönmek, çağıranın "sepet yok" ile "sepet kapandı"
// arasında ayrım yapmasını imkânsız kılardı.
func (r *Repository) writeBlocked(ctx context.Context, id, what string) error {
	cart, err := r.GetCart(ctx, id)
	if err != nil {
		return err
	}
	if cart.Completed() {
		return errors.Conflict(codeCartCompleted,
			"%s: sepet tamamlanmış ve değiştirilemez (%s)", what, id)
	}
	// Satır duruyor, silinmemiş ve tamamlanmamış: aradaki tek fark eşzamanlı
	// bir işlemin kaydı değiştirmiş olmasıdır. Yeniden denenebilir.
	return errors.Conflict(codeConcurrentUpdate,
		"%s: sepet eşzamanlı olarak değişti, istek yeniden denenebilir (%s)", what, id)
}

// --- satırlar ----------------------------------------------------------------

// CreateLineItem yeni bir sepet satırı kaydeder.
func (r *Repository) CreateLineItem(ctx context.Context, item models.LineItem) (models.LineItem, error) {
	meta, err := fromJSONMap(item.Metadata)
	if err != nil {
		return models.LineItem{}, err
	}

	row, err := r.queries(ctx).CreateLineItem(ctx, cartdb.CreateLineItemParams{
		ID:        item.ID,
		CartID:    item.CartID,
		VariantID: item.VariantID,
		Title:     item.Title,
		Quantity:  item.Quantity,
		UnitPrice: item.UnitPrice,
		Metadata:  meta,
	})
	if err != nil {
		return models.LineItem{}, classify(err, codeQueryFailed, "sepet satırı oluşturulamadı")
	}
	return toLineItem(row)
}

// GetLineItem satırı kimliğiyle döner; yoksa NotFound.
//
// Sepet kimliği de şarttır: başka bir sepetin satırı, kimliği bilinse bile
// okunamaz.
func (r *Repository) GetLineItem(ctx context.Context, cartID, lineID string) (models.LineItem, error) {
	row, err := r.queries(ctx).GetLineItem(ctx, cartdb.GetLineItemParams{
		ID:     lineID,
		CartID: cartID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.LineItem{}, lineItemNotFound(cartID, lineID)
		}
		return models.LineItem{}, classify(err, codeQueryFailed, "sepet satırı okunamadı")
	}
	return toLineItem(row)
}

// GetLineItemByVariant sepetteki varyantın yaşayan satırını döner; yoksa
// NotFound.
func (r *Repository) GetLineItemByVariant(ctx context.Context, cartID, variantID string) (models.LineItem, error) {
	row, err := r.queries(ctx).GetLineItemByVariant(ctx, cartdb.GetLineItemByVariantParams{
		CartID:    cartID,
		VariantID: variantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.LineItem{}, errors.NotFound(codeLineItemNotFound,
				"sepette bu varyanttan satır yok (sepet: %s, varyant: %s)", cartID, variantID)
		}
		return models.LineItem{}, classify(err, codeQueryFailed, "sepet satırı okunamadı")
	}
	return toLineItem(row)
}

// ListLineItems sepetin satırlarını oluşturulma sırasıyla döner.
func (r *Repository) ListLineItems(ctx context.Context, cartID string) ([]models.LineItem, error) {
	rows, err := r.queries(ctx).ListLineItems(ctx, cartID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "sepet satırları listelenemedi")
	}
	return toLineItems(rows)
}

// LineItemsByCartIDs birden çok sepetin satırlarını TEK sorguda döner (N+1 yok).
func (r *Repository) LineItemsByCartIDs(ctx context.Context, cartIDs []string) ([]models.LineItem, error) {
	if len(cartIDs) == 0 {
		return []models.LineItem{}, nil
	}
	rows, err := r.queries(ctx).ListLineItemsByCartIDs(ctx, cartIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "sepet satırları listelenemedi")
	}
	return toLineItems(rows)
}

// SetLineItemQuantity satırın adedini MUTLAK değerle yazar.
//
// Artımlı (quantity = quantity + n) bir güncelleme kasten kullanılmaz: yeni
// değer, kilit altında okunan değerden hesaplanır ve kararı veren kodun gördüğü
// sayı ile yazılan sayı aynı olur.
func (r *Repository) SetLineItemQuantity(ctx context.Context, cartID, lineID string, quantity int64) (models.LineItem, error) {
	row, err := r.queries(ctx).SetLineItemQuantity(ctx, cartdb.SetLineItemQuantityParams{
		ID:       lineID,
		CartID:   cartID,
		Quantity: quantity,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.LineItem{}, lineItemNotFound(cartID, lineID)
		}
		return models.LineItem{}, classify(err, codeQueryFailed, "sepet satırının adedi güncellenemedi")
	}
	return toLineItem(row)
}

// SetLineItemTotals bir hesap turunun TÜM satır tutarlarını TEK deyimle yazar;
// adede dokunmaz.
//
// # Neden tek deyim
//
// Yazma, sepetin kilidi altında ve tek işlemde olur; satır başına bir UPDATE
// koşmak kilidi satır sayısıyla DOĞRU ORANTILI süre boyunca tutuyordu. Ölçüldü
// (yerel konteyner, TCP gidiş-dönüş ~30 µs, 100 satırlık sepet, kilidin
// alınmasından SON YAZMANIN dönmesine kadar, p50): satır başına UPDATE 8,0 ms,
// buradaki tek deyim 0,55 ms — yazma evresinde 14 kat. Aynı UPDATE'leri tek
// boru hattında göndermek (pgx batch) 3,0 ms'de kalıyordu, yani kazancın %63'ü;
// kalan farkı ancak deyim sayısını 1'e indirmek veriyor.
//
// Sayılar commit'in WAL flush'ını İÇERMEZ (harness fsync=off koşar) ve flush da
// aynı kilidin altındadır: kalıcı bir kümede 6,2 ms ve bu değişiklik ona
// dokunmaz, yani uçtan uca kazanç ~2 kattır. Ayrım
// [github.com/bdrtr/gobit/internal/modules/cart/service.Service.SetTotals]
// godoc'unda ayrıntılı.
//
// Dizi boyutu için AYRI bir tavan YOKTUR ve konmadı: çağıran sepetin bütün
// satırlarını vermek zorunda olduğu için (bkz. service.SetTotals) boyut sepetin
// satır sayısıdır ve onu workflows/cart.MaxLineItems (bugün 100) sınırlar.
//
// # Eksik yazma turu DÜŞÜRÜR
//
// Deyim, kimliği eşleşmeyen satırı sessizce ATLAR: silinmiş bir satır, hiç
// olmayan bir kimlik ya da BAŞKA SEPETİN satırı hiçbir şey yazmaz (cart_id
// WHERE'dedir). Bu yüzden yazılan kimlikler istenenlerle karşılaştırılır ve
// eksik varsa NotFound dönülür — işlem geri alınır, sepet ya tamamen yeni
// tutarları alır ya da hiçbirini. Sessizce eksik yazmak, sepetin ara toplamıyla
// satırlarının toplamını ayırır ve müşteriye yanlış tutar tahsil edilirdi.
//
// Kural bugün İKİNCİ savunmadır: servis satır kümesini kilit altında okuyup tam
// kapsama arar ve sepeti değiştiren her yol aynı kilidi alır, yani okuma ile
// yazma arasında satır kaybolamaz. Buradaki kontrol, kilidi atlayan bir yolun
// (doğrudan SQL, ileride eklenecek bir akış) sessiz kalmasını engeller.
func (r *Repository) SetLineItemTotals(ctx context.Context, cartID string, lines []models.LineItemTotals) error {
	if len(lines) == 0 {
		return nil
	}

	// Diziler TEK döngüde kurulur: uzunlukların eşitliği ve indekslerin
	// hizası burada yapısal olarak garanti edilir. Ayrı döngüler, bir tutarı
	// başka bir satırla eşleştirme ihtimalini geri getirirdi.
	arg := cartdb.SetLineItemTotalsParams{
		CartID:         cartID,
		LineIds:        make([]string, len(lines)),
		UnitPrices:     make([]int64, len(lines)),
		Subtotals:      make([]int64, len(lines)),
		DiscountTotals: make([]int64, len(lines)),
		TaxTotals:      make([]int64, len(lines)),
		Totals:         make([]int64, len(lines)),
	}
	istenen := make(map[string]struct{}, len(lines))
	for i, line := range lines {
		// Aynı kimlik iki kez verilemez: UPDATE ... FROM bir hedef satır
		// birden çok kaynak satırla eşleştiğinde HANGİ tutarın kazandığını
		// tanımlamaz, yani sepet iki tutardan birini rastgele alırdı. Servis
		// bunu zaten eler; burada elenmesi, deyimin tanımsız davranışını
		// depodan çıkarır ve doğrudan çağıran bir testi de korur.
		if _, dup := istenen[line.LineItemID]; dup {
			return errors.Invalid(codeTotalsInconsistent,
				"aynı satır için birden çok tutar verildi: %s", line.LineItemID)
		}
		istenen[line.LineItemID] = struct{}{}

		arg.LineIds[i] = line.LineItemID
		arg.UnitPrices[i] = line.Totals.UnitPrice
		arg.Subtotals[i] = line.Totals.Subtotal
		arg.DiscountTotals[i] = line.Totals.DiscountTotal
		arg.TaxTotals[i] = line.Totals.TaxTotal
		arg.Totals[i] = line.Totals.Total
	}

	written, err := r.queries(ctx).SetLineItemTotals(ctx, arg)
	if err != nil {
		return classify(err, codeQueryFailed, "sepet satırlarının tutarları güncellenemedi")
	}
	if len(written) != len(lines) {
		return lineItemNotFound(cartID, firstUnwritten(lines, written))
	}
	return nil
}

// firstUnwritten yazılmayan İLK satırın kimliğini çağıranın verdiği sırayla
// döner.
//
// Sıra çağıranın dilimindendir, RETURNING'in değil: PostgreSQL RETURNING
// sırasını garanti etmez ve harita üzerinde dönmek aynı girdide farklı hata
// mesajları üretirdi. Mesajın yeniden üretilebilir olması, operatörün iki
// farklı arızayı ayırt edebilmesi demektir.
//
// Kimlikler tekrarsız olduğu için (yukarıda elenir) sayı eşitsizliği en az bir
// kimliğin yazılmadığı anlamına gelir; döngü daima bir kimlik bulur.
func firstUnwritten(lines []models.LineItemTotals, written []string) string {
	yazilan := make(map[string]struct{}, len(written))
	for _, id := range written {
		yazilan[id] = struct{}{}
	}
	for _, line := range lines {
		if _, ok := yazilan[line.LineItemID]; !ok {
			return line.LineItemID
		}
	}
	return ""
}

// SoftDeleteLineItem satırı yumuşak siler; satır yoksa NotFound döner.
func (r *Repository) SoftDeleteLineItem(ctx context.Context, cartID, lineID string) error {
	affected, err := r.queries(ctx).SoftDeleteLineItem(ctx, cartdb.SoftDeleteLineItemParams{
		ID:     lineID,
		CartID: cartID,
	})
	if err != nil {
		return classify(err, codeQueryFailed, "sepet satırı silinemedi")
	}
	if affected == 0 {
		return lineItemNotFound(cartID, lineID)
	}
	return nil
}

// SoftDeleteLineItemsByCart sepetin tüm satırlarını yumuşak siler.
func (r *Repository) SoftDeleteLineItemsByCart(ctx context.Context, cartID string) error {
	if err := r.queries(ctx).SoftDeleteLineItemsByCart(ctx, cartID); err != nil {
		return classify(err, codeQueryFailed, "sepet satırları silinemedi")
	}
	return nil
}

// lineItemNotFound eksik satır için ortak hatayı üretir.
func lineItemNotFound(cartID, lineID string) error {
	return errors.NotFound(codeLineItemNotFound,
		"sepet satırı bulunamadı (sepet: %s, satır: %s)", cartID, lineID)
}

// --- adresler ----------------------------------------------------------------

// UpsertCartAddress sepetin verilen türdeki adresini yazar; varsa üzerine yazar.
//
// Kimlik yalnızca YENİ satır için kullanılır; var olan kayıt güncellenirken
// kimliği KORUNUR. Kimliğin sabit kalması, adrese verilen bir referansın
// (log kaydı, sipariş kopyası) düzeltmeden sonra da geçerli kalması demektir.
func (r *Repository) UpsertCartAddress(ctx context.Context, addr models.CartAddress) (models.CartAddress, error) {
	meta, err := fromJSONMap(addr.Metadata)
	if err != nil {
		return models.CartAddress{}, err
	}

	row, err := r.queries(ctx).UpsertCartAddress(ctx, cartdb.UpsertCartAddressParams{
		ID:              addr.ID,
		CartID:          addr.CartID,
		AddressType:     addr.Type.String(),
		SourceAddressID: nullString(addr.SourceAddressID),
		FirstName:       nullString(addr.FirstName),
		LastName:        nullString(addr.LastName),
		Company:         nullString(addr.Company),
		Address1:        nullString(addr.Address1),
		Address2:        nullString(addr.Address2),
		City:            nullString(addr.City),
		Province:        nullString(addr.Province),
		PostalCode:      nullString(addr.PostalCode),
		CountryCode:     nullString(addr.CountryCode),
		Phone:           nullString(addr.Phone),
		Metadata:        meta,
	})
	if err != nil {
		return models.CartAddress{}, classify(err, codeQueryFailed, "sepet adresi yazılamadı")
	}
	return toCartAddress(row)
}

// ListCartAddresses sepetin adreslerini döner (tür sırasıyla).
func (r *Repository) ListCartAddresses(ctx context.Context, cartID string) ([]models.CartAddress, error) {
	rows, err := r.queries(ctx).ListCartAddresses(ctx, cartID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "sepet adresleri listelenemedi")
	}

	out := make([]models.CartAddress, 0, len(rows))
	for i := range rows {
		addr, convErr := toCartAddress(rows[i])
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, addr)
	}
	return out, nil
}

// SoftDeleteCartAddressesByCart sepetin tüm adreslerini yumuşak siler.
func (r *Repository) SoftDeleteCartAddressesByCart(ctx context.Context, cartID string) error {
	if err := r.queries(ctx).SoftDeleteCartAddressesByCart(ctx, cartID); err != nil {
		return classify(err, codeQueryFailed, "sepet adresleri silinemedi")
	}
	return nil
}

// --- kargo yöntemleri --------------------------------------------------------

// CreateShippingMethod sepete bir kargo yöntemi ekler.
func (r *Repository) CreateShippingMethod(ctx context.Context, method models.ShippingMethod) (models.ShippingMethod, error) {
	data, err := fromJSONMap(method.Data)
	if err != nil {
		return models.ShippingMethod{}, err
	}

	row, err := r.queries(ctx).CreateShippingMethod(ctx, cartdb.CreateShippingMethodParams{
		ID:               method.ID,
		CartID:           method.CartID,
		Name:             method.Name,
		ShippingOptionID: nullString(method.ShippingOptionID),
		Amount:           method.Amount,
		Data:             data,
	})
	if err != nil {
		return models.ShippingMethod{}, classify(err, codeQueryFailed, "kargo yöntemi eklenemedi")
	}
	return toShippingMethod(row)
}

// GetShippingMethod kargo yöntemini kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetShippingMethod(ctx context.Context, cartID, methodID string) (models.ShippingMethod, error) {
	row, err := r.queries(ctx).GetShippingMethod(ctx, cartdb.GetShippingMethodParams{
		ID:     methodID,
		CartID: cartID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingMethod{}, shippingMethodNotFound(cartID, methodID)
		}
		return models.ShippingMethod{}, classify(err, codeQueryFailed, "kargo yöntemi okunamadı")
	}
	return toShippingMethod(row)
}

// ListShippingMethods sepetin kargo yöntemlerini döner.
func (r *Repository) ListShippingMethods(ctx context.Context, cartID string) ([]models.ShippingMethod, error) {
	rows, err := r.queries(ctx).ListShippingMethods(ctx, cartID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "kargo yöntemleri listelenemedi")
	}

	out := make([]models.ShippingMethod, 0, len(rows))
	for i := range rows {
		method, convErr := toShippingMethod(rows[i])
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, method)
	}
	return out, nil
}

// SoftDeleteShippingMethod kargo yöntemini yumuşak siler; yoksa NotFound döner.
func (r *Repository) SoftDeleteShippingMethod(ctx context.Context, cartID, methodID string) error {
	affected, err := r.queries(ctx).SoftDeleteShippingMethod(ctx, cartdb.SoftDeleteShippingMethodParams{
		ID:     methodID,
		CartID: cartID,
	})
	if err != nil {
		return classify(err, codeQueryFailed, "kargo yöntemi kaldırılamadı")
	}
	if affected == 0 {
		return shippingMethodNotFound(cartID, methodID)
	}
	return nil
}

// SoftDeleteShippingMethodsByCart sepetin tüm kargo yöntemlerini yumuşak siler.
func (r *Repository) SoftDeleteShippingMethodsByCart(ctx context.Context, cartID string) error {
	if err := r.queries(ctx).SoftDeleteShippingMethodsByCart(ctx, cartID); err != nil {
		return classify(err, codeQueryFailed, "kargo yöntemleri silinemedi")
	}
	return nil
}

// shippingMethodNotFound eksik kargo yöntemi için ortak hatayı üretir.
func shippingMethodNotFound(cartID, methodID string) error {
	return errors.NotFound(codeShippingNotFound,
		"kargo yöntemi bulunamadı (sepet: %s, yöntem: %s)", cartID, methodID)
}
