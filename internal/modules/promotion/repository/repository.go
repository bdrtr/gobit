// Package repository promotion modülünün veritabanı erişim katmanıdır.
//
// sqlc'nin ürettiği promotiondb paketi bu paketin İÇİNDE kalır: dışarıya
// yalnızca [models] domain tipleri verilir, pgtype hiçbir imzada görünmez. Bu
// sınır bilinçlidir — servis ve API katmanları depolama ayrıntısına bağlanmaz
// ve üretilen kod yeniden üretildiğinde yalnızca bu paket etkilenir.
//
// Ham hatalar da sınırı geçmez: pgx.ErrNoRows ve PostgreSQL kısıt ihlalleri
// burada core/errors'ın tipli hatalarına çevrilir, böylece HTTP
// katmanı status kodunu doğru seçer (plan Bölüm 2.7).
//
// # Kilit sırası
//
// Kullanım akışı (Redeem/Release) satır kilitleri alır ve sıra HER YERDE
// AYNIDIR: ÖNCE promosyon, SONRA kampanya. Aynı kampanyaya bağlı iki promosyon
// eşzamanlı kullanıldığında ikisi de aynı kampanya satırını ister; sıra ancak
// böyle sabitlenirse kilitlenme (deadlock) oluşmaz.
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeCampaignNotFound istenen kampanyanın bulunamadığını bildirir.
	CodeCampaignNotFound = "campaign_not_found"
	// CodePromotionNotFound istenen promosyonun bulunamadığını bildirir.
	CodePromotionNotFound = "promotion_not_found"
	// CodeApplicationMethodNotFound promosyonun uygulama yöntemi olmadığını bildirir.
	CodeApplicationMethodNotFound = "promotion_application_method_not_found"
	// CodePromotionRuleNotFound istenen promosyon kuralının bulunamadığını bildirir.
	CodePromotionRuleNotFound = "promotion_rule_not_found"
	// CodeUsageLimitReached promosyonun kullanım hakkının bittiğini bildirir.
	CodeUsageLimitReached = "promotion_usage_limit_reached"
	// CodePromotionNotActive promosyonun yayında OLMADIĞINI bildirir; taslak ve
	// pasif promosyon kullanılamaz (bkz. [Repo.Redeem]).
	CodePromotionNotActive = "promotion_not_active"
	// CodeCampaignWindowClosed kampanyanın tarih penceresinin kullanım anını
	// KAPSAMADIĞINI bildirir (bkz. [Repo.Redeem]).
	CodeCampaignWindowClosed = "campaign_window_closed"
	// CodeBudgetUnitLocked bütçe sayacı sıfır değilken bütçenin BİRİMİNİN (türü
	// ya da para birimi) değiştirilemeyeceğini bildirir (bkz. [Repo.UpdateCampaign]).
	CodeBudgetUnitLocked = "campaign_budget_unit_locked"
	// CodeBudgetExceeded kampanya bütçesinin yetmediğini bildirir.
	CodeBudgetExceeded = "campaign_budget_exceeded"
	// CodeBudgetCurrencyMismatch kullanımın para biriminin kampanya bütçesininkiyle
	// uyuşmadığını bildirir.
	CodeBudgetCurrencyMismatch = "campaign_budget_currency_mismatch"
	// CodeRedemptionRaced serbest bırakma sırasında araya başka bir çağrının
	// girdiğini bildirir; satır kilidi altında oluşmaması BEKLENEN bir durumdur
	// (bkz. [Repo.Release]).
	CodeRedemptionRaced = "promotion_redemption_raced"
	// CodeConstraintViolation veritabanı kısıtının ihlal edildiğini bildirir.
	CodeConstraintViolation = "promotion_constraint_violation"
	// CodeDuplicate benzersizlik ihlalini bildirir.
	CodeDuplicate = "promotion_duplicate"
	// CodeQueryFailed beklenmeyen bir veritabanı hatasını bildirir.
	CodeQueryFailed = "promotion_query_failed"
	// CodeCanceled bağlam iptalini bildirir.
	CodeCanceled = "promotion_canceled"
	// CodeTxFailed işlem (transaction) yönetiminin başarısızlığını bildirir.
	CodeTxFailed = "promotion_tx_failed"
)

// PostgreSQL SQLSTATE kodları (ihtiyaç duyulanlar).
const (
	sqlstateCheckViolation       = "23514"
	sqlstateUniqueViolation      = "23505"
	sqlstateForeignKeyViolation  = "23503"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// emptyJSONObject boş bir metadata gövdesidir; NOT NULL sütuna hiçbir zaman
// nil yazılmaz.
var emptyJSONObject = []byte(`{}`)

// Repo promotion tablolarına erişimi sağlar. Eşzamanlı kullanıma güvenlidir.
type Repo struct {
	pool *pgxpool.Pool
	q    *promotiondb.Queries
}

// New verilen havuz üzerinde çalışan bir depo üretir.
//
// pool nil ise bu, kurulumda değil ilk çağrıda tipli bir hata olarak bildirilir;
// kurulum yolu panik üretmez.
func New(pool *pgxpool.Pool) *Repo {
	r := &Repo{pool: pool}
	if pool != nil {
		r.q = promotiondb.New(pool)
	}
	return r
}

// ready havuzun kullanılabilir olduğunu doğrular.
func (r *Repo) ready() error {
	if r == nil || r.pool == nil || r.q == nil {
		return errors.Unavailable(CodeQueryFailed, "promotion veritabanı havuzu kurulmamış")
	}
	return nil
}

// inTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem GERİ ALINIR.
//
// Kullanım akışı için ZORUNLUDUR: satır kilidi ancak bir işlem boyunca tutulur
// ve sayaç ile defter (promotion_redemption) ya BİRLİKTE yazılır ya hiç
// yazılmaz. Aksi hâlde sayacı artmış ama defteri olmayan bir promosyon, geri
// alınamayan bir kullanım bırakırdı.
func (r *Repo) inTx(ctx context.Context, fn func(q *promotiondb.Queries) error) error {
	if err := r.ready(); err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrapDB(err, "işlem başlatılamadı")
	}
	// Rollback, Commit'ten sonra çağrıldığında pgx.ErrTxClosed döner ve
	// yok sayılır; bu, başarılı yolda da defer'ın güvenle kalmasını sağlar.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(r.q.WithTx(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return wrapDB(err, "işlem tamamlanamadı")
	}
	return nil
}

// wrapDB ham bir veritabanı hatasını tipli hataya çevirir.
//
// Sınıflandırma bilinçlidir: kısıt ihlali İSTEMCİ hatasıdır (422), benzersizlik
// ihlali çakışmadır (409), iptal geçici erişilemezliktir (503); geri kalan her
// şey sunucu hatasıdır ve mesajı istemciye SIZDIRILMAZ (bkz. core/http).
func wrapDB(err error, format string, a ...any) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.Wrap(err, errors.KindUnavailable, CodeCanceled, format, a...)
	case errors.Is(err, pgx.ErrTxClosed), errors.Is(err, pgx.ErrTxCommitRollback):
		return errors.Wrap(err, errors.KindInternal, CodeTxFailed, format, a...)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlstateUniqueViolation:
			return errors.Wrap(err, errors.KindConflict, CodeDuplicate,
				"%s (kısıt: %s)", sprintf(format, a...), pgErr.ConstraintName)
		case sqlstateCheckViolation, sqlstateForeignKeyViolation,
			sqlstateNotNullViolation, sqlstateStringDataRightTrunc:
			return errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation,
				"%s (kısıt: %s)", sprintf(format, a...), pgErr.ConstraintName)
		}
	}

	return errors.Wrap(err, errors.KindInternal, CodeQueryFailed, format, a...)
}

// notFoundOr pgx.ErrNoRows'u NotFound'a, diğer her şeyi wrapDB'ye çevirir.
func notFoundOr(err error, code, format string, a ...any) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.NotFound(code, format, a...)
	}
	return wrapDB(err, format, a...)
}

// sprintf hata mesajını bir kez biçimlendirir.
//
// Argümansız çağrılarda format DEĞİŞTİRİLMEDEN döner; aksi hâlde mesajdaki bir
// yüzde işareti (örn. "%!d(MISSING)") kullanıcıya bozuk metin olarak giderdi.
func sprintf(format string, a ...any) string {
	if len(a) == 0 {
		return format
	}
	return fmt.Sprintf(format, a...)
}

// toTime NULL olmayan bir zaman damgasını UTC time.Time'a çevirir.
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

// toTimePtr NULL olabilen bir zaman damgasını *time.Time'a çevirir.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// fromTime bir zamanı NOT NULL damgaya çevirir; daima UTC yazılır.
func fromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// fromTimePtr isteğe bağlı bir zamanı damgaya çevirir; nil ise SQL NULL.
func fromTimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return fromTime(*t)
}

// deref bir dize işaretçisini değere çevirir; nil ise boş dize.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nilIfEmpty boş dizeyi SQL NULL'a çevirir.
//
// Boş dize ile NULL ayrımı bu şemada anlamlıdır: bütçenin para birimi yalnızca
// "spend" türünde vardır ve diğer türlerde NULL OLMALIDIR (bkz. migration'daki
// campaign_budget_currency_check). Boş dize yazılsaydı kısıt reddederdi.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// copyInt64 bir tam sayı işaretçisini KOPYALAYARAK döner.
//
// Kopya şarttır: üretilen satırın işaretçisi doğrudan domain modeline
// verilseydi, çağıranın modeli değiştirmesi depo tarafındaki tampon üzerinde de
// etki yaratırdı.
func copyInt64(v *int64) *int64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// encodeMetadata metadata haritasını JSONB gövdesine çevirir.
//
// Boş ya da nil harita `{}` yazar: sütun NOT NULL'dur ve JSON null,
// jsonb_typeof kısıtına çarpardı.
func encodeMetadata(md map[string]string) ([]byte, error) {
	if len(md) == 0 {
		return emptyJSONObject, nil
	}
	raw, err := json.Marshal(md)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation,
			"promosyon üstverisi JSON'a çevrilemedi")
	}
	return raw, nil
}

// decodeMetadata JSONB gövdesini metadata haritasına çevirir.
//
// Çözümlenemeyen bir gövde BOŞ harita döner, hata DEĞİL: üstveri iş kuralına
// girmez ve elle yazılmış bozuk bir kayıt yüzünden promosyonun tamamının
// okunamaz olması, hesabı bütünüyle düşürürdü.
func decodeMetadata(raw []byte) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	out := map[string]string{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]string{}
	}
	return out
}
