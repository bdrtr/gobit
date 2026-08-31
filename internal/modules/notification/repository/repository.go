// Package repository notification modülünün veritabanı erişimidir.
//
// SADECE bu modülün tablosuna dokunur (plan Bölüm 4). sqlc üretimi kod
// repository/notificationdb altındadır ve elle düzenlenmez; bu paket onun
// üstüne iki şey ekler:
//
//   - Çeviri: pgtype ve üretilmiş satır tipleri BU PAKETİN DIŞINA ÇIKMAZ,
//     models tiplerine çevrilir.
//   - Sınıflandırma: sürücü hataları core/errors tipli hatalarına çevrilir;
//     satır bulunamaması NotFound, benzersizlik ihlali Conflict olur
//     (plan Bölüm 2.7 — status kodunu handler seçmez).
//
// # İŞLEM (transaction) YOKTUR ve gerekmez
//
// Diğer modüllerin deposu WithTx taşır; burada yoktur. Günlük yazma iki tek
// deyimden oluşur (kayıt aç, sonucu yaz) ve ikisinin ARASINDA sağlayıcıya
// gidilir — yani ikisini tek işleme almak, bir ağ çağrısı boyunca açık
// işlem tutmak demekti. İşlem açık kalırken sürecin ölmesi hâlinde kayıt hiç
// yazılmaz ve mükerrer gönderimi durduran benzersizlik anahtarı da hiç
// oluşmazdı; ayrı deyimler bunun tersini garanti eder: kayıt HER ZAMAN
// gönderimden önce kalıcıdır.
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/repository/notificationdb"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeDeliveryNotFound istenen günlük kaydının bulunamadığını bildirir.
	CodeDeliveryNotFound = "notification_delivery_not_found"
	// CodeDeliveryExists aynı (şablon, referans) için ikinci bir kayıt
	// açılmak istendiğini bildirir.
	CodeDeliveryExists = "notification_delivery_already_exists"
	// CodeConstraintViolation veritabanı kısıtının ihlal edildiğini bildirir.
	CodeConstraintViolation = "notification_constraint_violation"
	// CodeQueryFailed beklenmeyen bir veritabanı hatasını bildirir.
	CodeQueryFailed = "notification_query_failed"
	// CodeCanceled bağlam iptalini bildirir.
	CodeCanceled = "notification_canceled"
	// CodeNotReady deponun havuz olmadan kurulduğunu bildirir.
	CodeNotReady = "notification_repository_not_ready"
)

// PostgreSQL SQLSTATE kodları (ihtiyaç duyulanlar).
const (
	sqlstateUniqueViolation      = "23505"
	sqlstateCheckViolation       = "23514"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// constraintTemplateReferenceUniq idempotency anahtarını zorlayan indeksin
// adıdır; migration'daki adla BİREBİR aynıdır.
const constraintTemplateReferenceUniq = "notification_deliveries_template_reference_uniq"

// Repo teslim günlüğüne erişimi sağlar. Eşzamanlı kullanıma güvenlidir.
type Repo struct {
	q *notificationdb.Queries
}

// New verilen havuz üzerinde çalışan bir depo üretir.
//
// pool nil ise bu, kurulumda değil ilk çağrıda tipli bir hata olarak
// bildirilir; kurulum yolu panik üretmez.
func New(pool *pgxpool.Pool) *Repo {
	if pool == nil {
		return &Repo{}
	}
	return &Repo{q: notificationdb.New(pool)}
}

// ready havuzun kullanılabilir olduğunu doğrular.
func (r *Repo) ready() error {
	if r == nil || r.q == nil {
		return errors.Unavailable(CodeNotReady, "notification veritabanı havuzu kurulmamış")
	}
	return nil
}

// ClaimDelivery günlük kaydını yalnızca o (şablon, referans) çifti HENÜZ
// KULLANILMAMIŞSA yazar. İkinci dönüş değeri satırın YAZILIP yazılmadığıdır.
//
// Çakışma bir hata DEĞİLDİR: aynı bildirimin ikinci kez tetiklenmesi beklenen
// bir durumdur (yeniden yayımlanan bir olay, elle tetikleme) ve doğru cevap
// hata değil ATLAMAKTIR. Çağıran, false gördüğünde sağlayıcıya HİÇ gitmez.
func (r *Repo) ClaimDelivery(ctx context.Context, d models.Delivery) (models.Delivery, bool, error) {
	if err := r.ready(); err != nil {
		return models.Delivery{}, false, err
	}

	row, err := r.q.ClaimNotificationDelivery(ctx, notificationdb.ClaimNotificationDeliveryParams{
		ID:         d.ID,
		Template:   d.Template,
		Channel:    d.Channel,
		Reference:  d.Reference,
		ProviderID: d.ProviderID,
		Status:     d.Status.String(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Delivery{}, false, nil
		}
		return models.Delivery{}, false, classify(err, "bildirim günlüğü kaydı açılamadı: %s/%s",
			d.Template, d.Reference)
	}
	return toDelivery(row), true, nil
}

// FinishDelivery gönderim denemesinin sonucunu yazar; kayıt yoksa NotFound.
func (r *Repo) FinishDelivery(
	ctx context.Context,
	id string,
	status models.DeliveryStatus,
	failure string,
) (models.Delivery, error) {
	if err := r.ready(); err != nil {
		return models.Delivery{}, err
	}

	row, err := r.q.FinishNotificationDelivery(ctx, notificationdb.FinishNotificationDeliveryParams{
		ID:     id,
		Status: status.String(),
		Error:  failure,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Delivery{}, deliveryNotFound(id)
		}
		return models.Delivery{}, classify(err, "bildirim günlüğü kaydı güncellenemedi: %s", id)
	}
	return toDelivery(row), nil
}

// GetDelivery kaydı kimliğiyle döner; yoksa NotFound.
//
// Yönetim listesinin yanında ayrıca durmasının sebebi teşhistir: bir teslim
// kaydının son hâlini, listenin süzgeçlerinden geçmeden okumak gerekir.
func (r *Repo) GetDelivery(ctx context.Context, id string) (models.Delivery, error) {
	if err := r.ready(); err != nil {
		return models.Delivery{}, err
	}

	row, err := r.q.GetNotificationDelivery(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Delivery{}, deliveryNotFound(id)
		}
		return models.Delivery{}, classify(err, "bildirim günlüğü kaydı okunamadı: %s", id)
	}
	return toDelivery(row), nil
}

// ListDeliveries kayıtları süzerek ve sayfalayarak döner.
// İkinci dönüş değeri süzgece uyan TÜM satırların sayısıdır.
//
// Toplam AYRI bir sorgudan gelir ve listeyle aynı süzgeçleri uygular; sayfa
// aralık dışında olsa ve hiç satır dönmese de doğrudur.
func (r *Repo) ListDeliveries(
	ctx context.Context,
	filter models.DeliveryFilter,
) ([]models.Delivery, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListNotificationDeliveries(ctx, notificationdb.ListNotificationDeliveriesParams{
		Reference: filter.Reference,
		Status:    filter.Status,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, "bildirim günlüğü listelenemedi")
	}

	total, err := r.q.CountNotificationDeliveries(ctx, notificationdb.CountNotificationDeliveriesParams{
		Reference: filter.Reference,
		Status:    filter.Status,
	})
	if err != nil {
		return nil, 0, classify(err, "bildirim günlüğü sayılamadı")
	}

	out := make([]models.Delivery, 0, len(rows))
	// Dilim İNDEKSLE gezilir: değerle gezmek her yinelemede satır yapısının
	// tamamını kopyalardı.
	for i := range rows {
		out = append(out, toDelivery(rows[i]))
	}
	return out, total, nil
}

// toDelivery üretilen satırı domain modeline çevirir.
func toDelivery(row notificationdb.NotificationDelivery) models.Delivery {
	return models.Delivery{
		ID:         row.ID,
		Template:   row.Template,
		Channel:    row.Channel,
		Reference:  row.Reference,
		ProviderID: row.ProviderID,
		Status:     models.DeliveryStatus(row.Status),
		Error:      row.Error,
		CreatedAt:  toTime(row.CreatedAt),
		UpdatedAt:  toTime(row.UpdatedAt),
	}
}

// toTime NOT NULL bir zaman damgasını UTC time.Time'a çevirir.
//
// Geçersiz (NULL) damga sıfır zaman döner: NOT NULL sütunlarda bu durum
// oluşamaz, oluşursa da sıfır zaman panik üretmeyen ve testte göze batan bir
// değerdir.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// sprintf hata mesajını bir kez biçimlendirir.
//
// Argümansız çağrılarda format DEĞİŞTİRİLMEDEN döner; aksi hâlde mesajdaki bir
// yüzde işareti (örn. "%!d(MISSING)") teşhis metnini bozardı.
func sprintf(format string, a ...any) string {
	if len(a) == 0 {
		return format
	}
	return fmt.Sprintf(format, a...)
}

// deliveryNotFound bulunamayan kayıt için tipli hata üretir.
func deliveryNotFound(id string) error {
	return errors.NotFound(CodeDeliveryNotFound, "bildirim günlüğü kaydı bulunamadı: %s", id)
}

// classify ham bir veritabanı hatasını tipli hataya çevirir.
//
// Sınıflandırma bilinçlidir: benzersizlik ihlali ÇAKIŞMADIR (409) ve
// idempotency anahtarının çiğnendiğini söyler; kısıt ihlali istemci hatasıdır
// (422); iptal geçici erişilemezliktir (503); geri kalan her şey sunucu
// hatasıdır ve mesajı istemciye SIZDIRILMAZ (bkz. core/http).
//
// Benzersizlik ihlali normal akışta BURAYA DÜŞMEZ — kayıt açma ON CONFLICT DO
// NOTHING kullanır ve çakışmayı satırsız dönüşle bildirir. Yine de eşlenir:
// eşlenmeseydi, indeksin elle ya da başka bir yoldan çiğnendiği bir durum
// "sunucu hatası" olarak görünür ve teşhisi zorlaşırdı.
func classify(err error, format string, a ...any) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Wrap(err, errors.KindUnavailable, CodeCanceled, format, a...)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlstateUniqueViolation:
			if pgErr.ConstraintName == constraintTemplateReferenceUniq {
				return errors.Wrap(err, errors.KindConflict, CodeDeliveryExists,
					"bu şablon ve referans için zaten bir bildirim kaydı var")
			}
		case sqlstateCheckViolation, sqlstateNotNullViolation, sqlstateStringDataRightTrunc:
			return errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation,
				"%s (kısıt: %s)", sprintf(format, a...), pgErr.ConstraintName)
		}
	}

	return errors.Wrap(err, errors.KindInternal, CodeQueryFailed, format, a...)
}
