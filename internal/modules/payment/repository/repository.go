// Package repository payment modülünün veritabanı erişimidir.
//
// SADECE bu modülün tablolarına dokunur (plan Bölüm 4). sqlc üretimi kod
// repository/paymentdb altındadır ve elle düzenlenmez; bu paket onun üstüne
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
// arayüzle bu paketi YAPISAL OLARAK eşleştiremezdi — Go'da imzadaki
// adlandırılmış tipler birebir aynı olmak zorundadır, yani servis repository'yi
// import etmek zorunda kalırdı. Context ile taşımak imzaları iki tarafın da
// paylaştığı tiplere (context.Context, models.*) indirger.
//
// Kilit alan metotlar (Lock...) işlem DIŞINDA çağrılırsa hata döner: FOR UPDATE
// kilidi işlem bitince serbest kalacağı için, işlemsiz bir kilit sessizce
// hiçbir şey korumazdı.
//
// # İki ayrı defter
//
// Bu paket iki farklı sahibin verisine hizmet eder: payment modülünün alan
// tabloları (payment_collections, payment_sessions, payments, refunds) ve
// MANUEL SAĞLAYICININ kendi defteri (payment_manual_sessions). İkincisi
// modülün alan verisi değildir; taklit edilen dış sistemin durumudur ve ona
// yalnızca manual paketi dokunur. Ayrım fiziksel olarak da korunur: servisin
// [github.com/bdrtr/gobit/internal/modules/payment/service.Store] arayüzünde
// manuel defter metotları YOKTUR.
package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
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

// Repository payment tablolarına erişimdir. Eşzamanlı kullanıma güvenlidir.
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
		return classify(err, codeTxBeginFailed, "işlem başlatılamadı")
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
		return classify(err, codeTxCommitFailed, "işlem tamamlanamadı")
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
func (r *Repository) queries(ctx context.Context) *paymentdb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return paymentdb.New(tx)
	}
	return paymentdb.New(r.pool)
}

// requireTx kilit alan metotların işlem içinde çağrıldığını doğrular.
func requireTx(ctx context.Context, op string) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"%s işlem (transaction) içinde çağrılmalı; işlemsiz bir FOR UPDATE kilidi hiçbir şeyi korumaz", op)
	}
	return nil
}

// --- ödeme koleksiyonları ----------------------------------------------------

// CreatePaymentCollection yeni bir ödeme koleksiyonu kaydeder.
func (r *Repository) CreatePaymentCollection(
	ctx context.Context,
	col models.PaymentCollection,
) (models.PaymentCollection, error) {
	meta, err := fromJSONMap(col.Metadata)
	if err != nil {
		return models.PaymentCollection{}, err
	}

	row, err := r.queries(ctx).CreatePaymentCollection(ctx, paymentdb.CreatePaymentCollectionParams{
		ID:           col.ID,
		Reference:    col.Reference,
		Amount:       col.Amount,
		CurrencyCode: col.CurrencyCode,
		Status:       col.Status.String(),
		Metadata:     meta,
	})
	if err != nil {
		return models.PaymentCollection{}, classify(err, codeQueryFailed, "ödeme koleksiyonu oluşturulamadı")
	}
	return toCollection(row)
}

// GetPaymentCollection koleksiyonu kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error) {
	row, err := r.queries(ctx).GetPaymentCollection(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentCollection{}, collectionNotFound(id)
		}
		return models.PaymentCollection{}, classify(err, codeQueryFailed, "ödeme koleksiyonu okunamadı")
	}
	return toCollection(row)
}

// LockPaymentCollection koleksiyonu işlem boyunca kilitler ve güncel hâlini
// döner. Koleksiyona bağlı HER yazma akışı buradan başlar; kilit sırasının ilk
// adımıdır.
func (r *Repository) LockPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error) {
	if err := requireTx(ctx, "LockPaymentCollection"); err != nil {
		return models.PaymentCollection{}, err
	}
	row, err := r.queries(ctx).LockPaymentCollection(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentCollection{}, collectionNotFound(id)
		}
		return models.PaymentCollection{}, classify(err, codeQueryFailed, "ödeme koleksiyonu kilitlenemedi")
	}
	return toCollection(row)
}

// ListPaymentCollections koleksiyonları süzerek ve sayfalayarak döner.
// İkinci dönüş değeri süzgece uyan TÜM satırların sayısıdır.
//
// Toplam AYRI bir sorgudan gelir ve listeyle aynı süzgeçleri uygular; sayfa
// aralık dışında olsa ve hiç satır dönmese de doğrudur. İki sorgu arasında
// yazılan bir satır toplamı bir değiştirebilir: toplam, sayfalama zarfının
// bilgilendirici alanıdır, işlem kararı ona dayandırılmaz.
func (r *Repository) ListPaymentCollections(
	ctx context.Context,
	filter models.CollectionFilter,
) ([]models.PaymentCollection, int64, error) {
	rows, err := r.queries(ctx).ListPaymentCollections(ctx, paymentdb.ListPaymentCollectionsParams{
		Reference: filter.Reference,
		Status:    filter.Status,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "ödeme koleksiyonları listelenemedi")
	}

	total, err := r.queries(ctx).CountPaymentCollections(ctx, paymentdb.CountPaymentCollectionsParams{
		Reference: filter.Reference,
		Status:    filter.Status,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "ödeme koleksiyonları sayılamadı")
	}

	out := make([]models.PaymentCollection, 0, len(rows))
	for i := range rows {
		col, convErr := toCollection(rows[i])
		if convErr != nil {
			return nil, 0, convErr
		}
		out = append(out, col)
	}
	return out, total, nil
}

// PaymentCollectionsByIDs verilen kimliklerin koleksiyonlarını TEK sorguda
// döner. Bulunamayan kimlik için satır dönmez; bu bir hata değildir.
func (r *Repository) PaymentCollectionsByIDs(
	ctx context.Context,
	ids []string,
) ([]models.PaymentCollection, error) {
	if len(ids) == 0 {
		return []models.PaymentCollection{}, nil
	}
	rows, err := r.queries(ctx).GetPaymentCollectionsByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "ödeme koleksiyonları okunamadı")
	}

	out := make([]models.PaymentCollection, 0, len(rows))
	for i := range rows {
		col, convErr := toCollection(rows[i])
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, col)
	}
	return out, nil
}

// UpdatePaymentCollectionTotals koleksiyonun tutarlarını ve türetilmiş
// durumunu MUTLAK değerlerle yazar.
//
// Artımlı güncelleme kasten kullanılmaz: yeni değer, kilit altında okunan
// değerden hesaplanır ve kararı veren kodun gördüğü sayı ile yazılan sayı aynı
// olur.
func (r *Repository) UpdatePaymentCollectionTotals(
	ctx context.Context,
	id string,
	status models.CollectionStatus,
	authorized, captured, refunded int64,
) (models.PaymentCollection, error) {
	row, err := r.queries(ctx).UpdatePaymentCollectionTotals(ctx, paymentdb.UpdatePaymentCollectionTotalsParams{
		ID:               id,
		Status:           status.String(),
		AuthorizedAmount: authorized,
		CapturedAmount:   captured,
		RefundedAmount:   refunded,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentCollection{}, collectionNotFound(id)
		}
		return models.PaymentCollection{}, classify(err, codeQueryFailed, "ödeme koleksiyonu güncellenemedi")
	}
	return toCollection(row)
}

// --- ödeme oturumları --------------------------------------------------------

// CreatePaymentSession yeni bir ödeme oturumu kaydeder.
// Aynı (sağlayıcı, idempotency anahtarı) çifti yaşıyorsa Conflict döner.
func (r *Repository) CreatePaymentSession(
	ctx context.Context,
	ses models.PaymentSession,
) (models.PaymentSession, error) {
	row, err := r.queries(ctx).CreatePaymentSession(ctx, paymentdb.CreatePaymentSessionParams{
		ID:                  ses.ID,
		PaymentCollectionID: ses.PaymentCollectionID,
		ProviderID:          ses.ProviderID,
		ExternalID:          ses.ExternalID,
		Status:              ses.Status.String(),
		Amount:              ses.Amount,
		AuthorizedAmount:    ses.AuthorizedAmount,
		CurrencyCode:        ses.CurrencyCode,
		Data:                jsonOrEmpty(ses.Data),
		IdempotencyKey:      ses.IdempotencyKey,
	})
	if err != nil {
		return models.PaymentSession{}, classify(err, codeQueryFailed, "ödeme oturumu oluşturulamadı")
	}
	return toSession(row), nil
}

// GetPaymentSession oturumu kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetPaymentSession(ctx context.Context, id string) (models.PaymentSession, error) {
	row, err := r.queries(ctx).GetPaymentSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentSession{}, sessionNotFound(id)
		}
		return models.PaymentSession{}, classify(err, codeQueryFailed, "ödeme oturumu okunamadı")
	}
	return toSession(row), nil
}

// LockPaymentSession oturumu işlem boyunca kilitler ve güncel hâlini döner.
//
// Durum geçişleri yalnızca bu kilit altında yapılır: aynı oturumu aynı anda
// yetkilendirmeye çalışan iki çağrıdan ikincisi, birincinin yazdığı durumu
// görür ve sağlayıcıya İKİNCİ KEZ gitmez.
func (r *Repository) LockPaymentSession(ctx context.Context, id string) (models.PaymentSession, error) {
	if err := requireTx(ctx, "LockPaymentSession"); err != nil {
		return models.PaymentSession{}, err
	}
	row, err := r.queries(ctx).LockPaymentSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentSession{}, sessionNotFound(id)
		}
		return models.PaymentSession{}, classify(err, codeQueryFailed, "ödeme oturumu kilitlenemedi")
	}
	return toSession(row), nil
}

// PaymentSessionByIdempotencyKey aynı anahtarla açılmış oturumu döner;
// yoksa NotFound.
func (r *Repository) PaymentSessionByIdempotencyKey(
	ctx context.Context,
	providerID, key string,
) (models.PaymentSession, error) {
	row, err := r.queries(ctx).GetPaymentSessionByIdempotencyKey(ctx,
		paymentdb.GetPaymentSessionByIdempotencyKeyParams{
			ProviderID:     providerID,
			IdempotencyKey: key,
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentSession{}, errors.NotFound(codeSessionNotFound,
				"bu idempotency anahtarıyla açılmış oturum yok: %s/%s", providerID, key)
		}
		return models.PaymentSession{}, classify(err, codeQueryFailed, "ödeme oturumu okunamadı")
	}
	return toSession(row), nil
}

// ListPaymentSessionsByCollection koleksiyonun oturumlarını döner.
func (r *Repository) ListPaymentSessionsByCollection(
	ctx context.Context,
	collectionID string,
) ([]models.PaymentSession, error) {
	rows, err := r.queries(ctx).ListPaymentSessionsByCollection(ctx, collectionID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "ödeme oturumları listelenemedi")
	}

	out := make([]models.PaymentSession, 0, len(rows))
	for i := range rows {
		out = append(out, toSession(rows[i]))
	}
	return out, nil
}

// SessionCounts koleksiyonun oturumlarını duruma göre TEK sorguda sayar.
func (r *Repository) SessionCounts(ctx context.Context, collectionID string) (models.SessionCounts, error) {
	row, err := r.queries(ctx).CountPaymentSessionStates(ctx, collectionID)
	if err != nil {
		return models.SessionCounts{}, classify(err, codeQueryFailed, "ödeme oturumları sayılamadı")
	}
	return models.SessionCounts{
		Live:     row.LiveCount,
		Canceled: row.CanceledCount,
		Failed:   row.FailedCount,
		Total:    row.TotalCount,
	}, nil
}

// LiveSessionAmount koleksiyonun CANLI oturumlarının rezerve ettiği toplam
// tutarı TEK sorguda döner; hiç canlı oturum yoksa 0.
//
// Bekleyen oturum kendi tutarını, yetkilendirilmiş oturum bloke edilen tutarı
// rezerve eder (gerekçe sorgunun yanındadır). Koleksiyon kilidi ALTINDA
// okunmalıdır: kilitsiz okunan bir toplam, araya giren bir oturum açılışıyla
// bayatlar ve koleksiyonun tutarından fazlası rezerve edilebilirdi.
func (r *Repository) LiveSessionAmount(ctx context.Context, collectionID string) (int64, error) {
	reserved, err := r.queries(ctx).SumLiveSessionAmounts(ctx, collectionID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "canlı oturum tutarları toplanamadı")
	}
	return reserved, nil
}

// UpdatePaymentSessionState oturumun durumunu, yetkilendirilen tutarını, ham
// sağlayıcı verisini ve ret sebebini MUTLAK değerlerle yazar.
func (r *Repository) UpdatePaymentSessionState(
	ctx context.Context,
	id string,
	status models.SessionStatus,
	authorizedAmount int64,
	data []byte,
	declineReason string,
) (models.PaymentSession, error) {
	row, err := r.queries(ctx).UpdatePaymentSessionState(ctx, paymentdb.UpdatePaymentSessionStateParams{
		ID:               id,
		Status:           status.String(),
		AuthorizedAmount: authorizedAmount,
		Data:             jsonOrEmpty(data),
		DeclineReason:    nullString(declineReason),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.PaymentSession{}, sessionNotFound(id)
		}
		return models.PaymentSession{}, classify(err, codeQueryFailed, "ödeme oturumu güncellenemedi")
	}
	return toSession(row), nil
}

// --- tahsilatlar -------------------------------------------------------------

// CreatePayment yeni bir tahsilat kaydeder.
// Oturumun zaten bir tahsilatı varsa Conflict döner.
func (r *Repository) CreatePayment(ctx context.Context, pay models.Payment) (models.Payment, error) {
	row, err := r.queries(ctx).CreatePayment(ctx, paymentdb.CreatePaymentParams{
		ID:                  pay.ID,
		PaymentSessionID:    pay.PaymentSessionID,
		PaymentCollectionID: pay.PaymentCollectionID,
		Amount:              pay.Amount,
		CurrencyCode:        pay.CurrencyCode,
		CapturedAt:          fromTime(pay.CapturedAt),
	})
	if err != nil {
		return models.Payment{}, classify(err, codeQueryFailed, "tahsilat oluşturulamadı")
	}
	return toPayment(row), nil
}

// GetPayment tahsilatı kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetPayment(ctx context.Context, id string) (models.Payment, error) {
	row, err := r.queries(ctx).GetPayment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Payment{}, paymentNotFound(id)
		}
		return models.Payment{}, classify(err, codeQueryFailed, "tahsilat okunamadı")
	}
	return toPayment(row), nil
}

// LockPayment tahsilatı işlem boyunca kilitler ve güncel hâlini döner.
// İade edilen tutar yalnızca bu kilit altında güncellenir.
func (r *Repository) LockPayment(ctx context.Context, id string) (models.Payment, error) {
	if err := requireTx(ctx, "LockPayment"); err != nil {
		return models.Payment{}, err
	}
	row, err := r.queries(ctx).LockPayment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Payment{}, paymentNotFound(id)
		}
		return models.Payment{}, classify(err, codeQueryFailed, "tahsilat kilitlenemedi")
	}
	return toPayment(row), nil
}

// PaymentBySession oturumdan doğan tahsilatı döner; yoksa NotFound.
func (r *Repository) PaymentBySession(ctx context.Context, sessionID string) (models.Payment, error) {
	row, err := r.queries(ctx).GetPaymentBySession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Payment{}, errors.NotFound(codePaymentNotFound,
				"oturumdan doğan tahsilat yok: %s", sessionID)
		}
		return models.Payment{}, classify(err, codeQueryFailed, "tahsilat okunamadı")
	}
	return toPayment(row), nil
}

// ListPaymentsByCollection koleksiyonun tahsilatlarını döner.
func (r *Repository) ListPaymentsByCollection(ctx context.Context, collectionID string) ([]models.Payment, error) {
	rows, err := r.queries(ctx).ListPaymentsByCollection(ctx, collectionID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "tahsilatlar listelenemedi")
	}

	out := make([]models.Payment, 0, len(rows))
	for i := range rows {
		out = append(out, toPayment(rows[i]))
	}
	return out, nil
}

// UpdatePaymentRefundedAmount tahsilatın iade edilmiş tutarını MUTLAK değerle
// yazar.
func (r *Repository) UpdatePaymentRefundedAmount(
	ctx context.Context,
	id string,
	refunded int64,
) (models.Payment, error) {
	row, err := r.queries(ctx).UpdatePaymentRefundedAmount(ctx, paymentdb.UpdatePaymentRefundedAmountParams{
		ID:             id,
		RefundedAmount: refunded,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Payment{}, paymentNotFound(id)
		}
		return models.Payment{}, classify(err, codeQueryFailed, "tahsilat güncellenemedi")
	}
	return toPayment(row), nil
}

// --- iadeler -----------------------------------------------------------------

// CreateRefund yeni bir iade kaydeder.
func (r *Repository) CreateRefund(ctx context.Context, ref models.Refund) (models.Refund, error) {
	row, err := r.queries(ctx).CreateRefund(ctx, paymentdb.CreateRefundParams{
		ID:        ref.ID,
		PaymentID: ref.PaymentID,
		Amount:    ref.Amount,
		Reason:    nullString(ref.Reason),
	})
	if err != nil {
		return models.Refund{}, classify(err, codeQueryFailed, "iade oluşturulamadı")
	}
	return toRefund(row), nil
}

// ListRefundsByPayment tahsilatın iadelerini döner.
func (r *Repository) ListRefundsByPayment(ctx context.Context, paymentID string) ([]models.Refund, error) {
	rows, err := r.queries(ctx).ListRefundsByPayment(ctx, paymentID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "iadeler listelenemedi")
	}

	out := make([]models.Refund, 0, len(rows))
	for i := range rows {
		out = append(out, toRefund(rows[i]))
	}
	return out, nil
}

// --- manuel sağlayıcının defteri ---------------------------------------------

// InsertManualSessionIfAbsent oturumu yalnızca idempotency anahtarı henüz
// kullanılmamışsa yazar. İkinci dönüş değeri satırın YAZILIP yazılmadığıdır.
//
// Çakışma bir hata DEĞİLDİR: sağlayıcı sözleşmesi aynı anahtarla ikinci bir
// çağrının mevcut oturumu dönmesini şart koşar. Yazma ile okumayı tek deyimde
// birleştirmek, "önce oku sonra yaz" arasına giren eşzamanlı bir çağrının
// benzersiz indekse çarpmasını da önler.
func (r *Repository) InsertManualSessionIfAbsent(
	ctx context.Context,
	ses models.ManualSession,
) (models.ManualSession, bool, error) {
	row, err := r.queries(ctx).InsertManualSessionIfAbsent(ctx, paymentdb.InsertManualSessionIfAbsentParams{
		ID:             ses.ID,
		IdempotencyKey: ses.IdempotencyKey,
		Reference:      ses.Reference,
		Amount:         ses.Amount,
		CurrencyCode:   ses.CurrencyCode,
		Status:         ses.Status.String(),
		Data:           jsonOrEmpty(ses.Data),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualSession{}, false, nil
		}
		return models.ManualSession{}, false, classify(err, codeQueryFailed,
			"manuel sağlayıcı oturumu oluşturulamadı")
	}
	return toManualSession(row), true, nil
}

// ManualSession sağlayıcı oturumunu kimliğiyle döner; yoksa NotFound.
func (r *Repository) ManualSession(ctx context.Context, id string) (models.ManualSession, error) {
	row, err := r.queries(ctx).GetManualSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualSession{}, manualSessionNotFound(id)
		}
		return models.ManualSession{}, classify(err, codeQueryFailed,
			"manuel sağlayıcı oturumu okunamadı")
	}
	return toManualSession(row), nil
}

// ManualSessionByIdempotencyKey sağlayıcı oturumunu anahtarıyla döner;
// yoksa NotFound.
func (r *Repository) ManualSessionByIdempotencyKey(ctx context.Context, key string) (models.ManualSession, error) {
	row, err := r.queries(ctx).GetManualSessionByIdempotencyKey(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualSession{}, errors.NotFound(codeManualSessionNotFound,
				"bu idempotency anahtarıyla açılmış sağlayıcı oturumu yok: %s", key)
		}
		return models.ManualSession{}, classify(err, codeQueryFailed,
			"manuel sağlayıcı oturumu okunamadı")
	}
	return toManualSession(row), nil
}

// LockManualSession sağlayıcı oturumunu işlem boyunca kilitler ve güncel
// hâlini döner. Sağlayıcının durum geçişleri yalnızca bu kilit altında yapılır.
func (r *Repository) LockManualSession(ctx context.Context, id string) (models.ManualSession, error) {
	if err := requireTx(ctx, "LockManualSession"); err != nil {
		return models.ManualSession{}, err
	}
	row, err := r.queries(ctx).LockManualSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualSession{}, manualSessionNotFound(id)
		}
		return models.ManualSession{}, classify(err, codeQueryFailed,
			"manuel sağlayıcı oturumu kilitlenemedi")
	}
	return toManualSession(row), nil
}

// UpdateManualSessionState sağlayıcı oturumunun durumunu ve tutarlarını MUTLAK
// değerlerle yazar.
func (r *Repository) UpdateManualSessionState(
	ctx context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.ManualSession, error) {
	row, err := r.queries(ctx).UpdateManualSessionState(ctx, paymentdb.UpdateManualSessionStateParams{
		ID:               id,
		Status:           status.String(),
		AuthorizedAmount: authorized,
		CapturedAmount:   captured,
		RefundedAmount:   refunded,
		DeclineReason:    nullString(declineReason),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualSession{}, manualSessionNotFound(id)
		}
		return models.ManualSession{}, classify(err, codeQueryFailed,
			"manuel sağlayıcı oturumu güncellenemedi")
	}
	return toManualSession(row), nil
}
