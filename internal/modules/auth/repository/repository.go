// Package repository auth modülünün veritabanı erişim katmanıdır.
//
// sqlc'nin ürettiği authdb paketi bu paketin İÇİNDE kalır: dışarıya yalnızca
// [models] domain tipleri verilir, pgtype hiçbir imzada görünmez. Bu sınır
// bilinçlidir — servis ve API katmanları depolama ayrıntısına bağlanmaz ve
// üretilen kod yeniden üretildiğinde yalnızca bu paket etkilenir.
//
// Ham hatalar da sınırı geçmez: pgx.ErrNoRows ve PostgreSQL kısıt ihlalleri
// burada internal/core/errors'ın tipli hatalarına çevrilir, böylece HTTP katmanı
// status kodunu doğru seçer (plan Bölüm 2.7).
//
// # Sırlar
//
// Bu paket iki sır sütunu okur: auth_identity.password_hash ve
// api_key.token_hash. İkisi de HASH'tir, düz metin değildir; yine de hiçbir
// hata mesajına, log satırına ya da kısıt açıklamasına KONMAZ. Hata
// mesajlarında kullanılan tek tanımlayıcı kayıt kimliğidir.
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeUserNotFound istenen kullanıcının bulunamadığını bildirir.
	CodeUserNotFound = "auth_user_not_found"
	// CodeIdentityNotFound istenen kimlik kaydının bulunamadığını bildirir.
	CodeIdentityNotFound = "auth_identity_not_found"
	// CodeAPIKeyNotFound istenen API anahtarının bulunamadığını bildirir.
	CodeAPIKeyNotFound = "auth_api_key_not_found" //nolint:gosec // G101: kimlik bilgisi değil, sabit hata KODU
	// CodeSalesChannelNotFound istenen satış kanalının bulunamadığını bildirir.
	CodeSalesChannelNotFound = "auth_sales_channel_not_found"
	// CodeAlreadyRevoked anahtarın zaten iptal edilmiş olduğunu bildirir.
	CodeAlreadyRevoked = "auth_api_key_already_revoked"
	// CodeConstraintViolation veritabanı kısıtının ihlal edildiğini bildirir.
	CodeConstraintViolation = "auth_constraint_violation"
	// CodeDuplicate benzersizlik ihlalini bildirir (örn. kayıtlı e-posta).
	CodeDuplicate = "auth_duplicate"
	// CodeEmailTaken e-postanın başka bir kullanıcıda olduğunu bildirir.
	CodeEmailTaken = "auth_email_taken"
	// CodeChannelNameTaken kanal adının kullanıldığını bildirir.
	CodeChannelNameTaken = "auth_sales_channel_name_taken"
	// CodeMetadataInvalid metadata alanının çözümlenemediğini bildirir.
	CodeMetadataInvalid = "auth_metadata_invalid"
	// CodeQueryFailed beklenmeyen bir veritabanı hatasını bildirir.
	CodeQueryFailed = "auth_query_failed"
	// CodeCanceled bağlam iptalini bildirir.
	CodeCanceled = "auth_canceled"
	// CodeTxFailed işlem (transaction) yönetiminin başarısızlığını bildirir.
	CodeTxFailed = "auth_tx_failed"
)

// Benzersiz indekslerin adları.
//
// Adlar hata sınıflandırmasında kullanılır: bir benzersizlik ihlalinin hangi
// kuraldan geldiği yalnızca kısıt adından okunabilir ve çağıran "e-posta
// kullanımda" ile "kanal adı kullanımda" arasını ancak böyle ayırabilir.
const (
	// IndexUserEmail canlı kullanıcıların e-posta benzersizliğidir.
	IndexUserEmail = "auth_user_email_uniq"
	// IndexIdentityProvider bir sağlayıcıdaki kimliğin benzersizliğidir.
	IndexIdentityProvider = "auth_identity_provider_uniq"
	// IndexIdentityUserProvider kullanıcı başına sağlayıcı başına TEK kimlik
	// kuralıdır.
	IndexIdentityUserProvider = "auth_identity_user_provider_uniq"
	// IndexChannelName satış kanalı adlarının benzersizliğidir.
	IndexChannelName = "sales_channel_name_uniq"
	// IndexTokenHash API anahtarı özetinin benzersizliğidir.
	IndexTokenHash = "api_key_token_hash_uniq" //nolint:gosec // G101: kimlik bilgisi değil, veritabanı İNDEKS adı
)

// PostgreSQL SQLSTATE kodları (ihtiyaç duyulanlar).
const (
	sqlstateCheckViolation      = "23514"
	sqlstateUniqueViolation     = "23505"
	sqlstateForeignKeyViolation = "23503"
	sqlstateNotNullViolation    = "23502"
)

// sqlstateDataException "veri istisnası" SINIFININ önekidir (22xxx).
//
// Sınıf tek tek kodlarla değil ÖNEKLE tanınır ve bu bilinçlidir: sınıfın
// tamamı İSTEMCİNİN gönderdiği değerden doğar — metin sütuna sığmadı (22001),
// değer hedef tipe çevrilemedi (22P02), jsonb'ye NUL kaçışı kondu (22P05),
// metinde sunucu kodlamasında karşılığı olmayan bayt var (22021). Kodlar elle
// sayılsaydı liste er geç eksik kalır, eksik kalan kod KindInternal'a düşer ve
// istemcinin yazdığı bozuk bir alan 500 üretirdi; doğru cevap 422'dir.
const sqlstateDataException = "22"

// Repo auth tablolarına erişimi sağlar. Eşzamanlı kullanıma güvenlidir.
type Repo struct {
	pool *pgxpool.Pool
	q    *authdb.Queries
}

// New verilen havuz üzerinde çalışan bir depo üretir.
//
// pool nil ise bu, kurulumda değil ilk çağrıda tipli bir hata olarak bildirilir;
// kurulum yolu panik üretmez.
func New(pool *pgxpool.Pool) *Repo {
	r := &Repo{pool: pool}
	if pool != nil {
		r.q = authdb.New(pool)
	}
	return r
}

// ready havuzun kullanılabilir olduğunu doğrular.
func (r *Repo) ready() error {
	if r == nil || r.pool == nil || r.q == nil {
		return errors.Unavailable(CodeQueryFailed, "auth veritabanı havuzu kurulmamış")
	}
	return nil
}

// inTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem GERİ ALINIR.
//
// Atomiklik iki yerde zorunludur: kullanıcı + kimlik kaydının birlikte
// oluşturulmasında (kimliksiz bir kullanıcı hiç giriş yapamaz, kullanıcısız bir
// kimlik ise sahipsiz kalır) ve e-posta değişiminde (kullanıcı satırı yeni
// adresi, kimlik satırı eskisini gösterirse giriş kopar).
func (r *Repo) inTx(ctx context.Context, fn func(q *authdb.Queries) error) error {
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
// Sınıflandırma bilinçlidir: kısıt ihlali ve veri istisnası İSTEMCİ hatasıdır
// (422), benzersizlik ihlali çakışmadır (409), iptal geçici erişilemezliktir
// (503); geri kalan her şey sunucu hatasıdır ve mesajı istemciye SIZDIRILMAZ
// (bkz. core/http).
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
		message := kisitli(sprintf(format, a...), pgErr.ConstraintName)
		switch {
		case pgErr.Code == sqlstateUniqueViolation:
			return errors.Wrap(err, errors.KindConflict, CodeDuplicate, "%s", message)
		case pgErr.Code == sqlstateCheckViolation,
			pgErr.Code == sqlstateForeignKeyViolation,
			pgErr.Code == sqlstateNotNullViolation,
			strings.HasPrefix(pgErr.Code, sqlstateDataException):
			return errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation, "%s", message)
		}
	}

	return errors.Wrap(err, errors.KindInternal, CodeQueryFailed, format, a...)
}

// kisitli mesaja kısıt adını YALNIZCA varsa ekler.
//
// Veri istisnalarında (22xxx) kısıt adı boştur: hata bir kuralın değil, bir
// DEĞERİN reddidir. Ad koşulsuz eklenseydi mesaj "… (kısıt: )" diye biterdi ve
// hatayı okuyan kişi olmayan bir kısıtı aramaya çıkardı.
func kisitli(message, constraint string) string {
	if constraint == "" {
		return message
	}
	return fmt.Sprintf("%s (kısıt: %s)", message, constraint)
}

// ConstraintName hatanın hangi veritabanı kısıtından geldiğini döner; kısıt
// bilgisi yoksa boş dize.
//
// Servis bunu benzersizlik ihlalinin GEREKÇESİNİ ayırt etmek için kullanır:
// aynı SQLSTATE altında "e-posta kullanımda" ile "kanal adı kullanımda"
// birbirinden yalnızca kısıt adıyla ayrılır.
func ConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
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

// toInt32 sayfalama değerini sorgunun beklediği int32'ye GÜVENLE daraltır.
//
// Negatif değer sıfıra, int32'yi aşan değer üst sınıra çekilir: aksi hâlde
// daraltma sessizce işaret değiştirir ve "LIMIT -2147483648" gibi bir sorgu
// üretirdi. Sınır kontrolü çağıranın doğrulamasına bırakılmaz; burası son
// savunmadır.
func toInt32(n int64) int32 {
	switch {
	case n < 0:
		return 0
	case n > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(n)
	}
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

// toMetadata jsonb sütununu haritaya çevirir.
//
// Boş ya da JSON null değer nil harita döner; böylece API yanıtında
// "metadata": null yerine alan hiç görünmez (omitempty).
func toMetadata(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeMetadataInvalid,
			"metadata alanı çözümlenemedi")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromMetadata haritayı jsonb sütununa yazılacak bayta çevirir.
//
// nil harita boş nesneye ('{}') çevrilir: sütun NOT NULL'dur ve "metadata yok"
// ile "metadata boş" arasında saklamada bir fark yoktur.
func fromMetadata(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeMetadataInvalid,
			"metadata alanı JSON'a çevrilemedi")
	}
	return raw, nil
}

// patchMetadata kısmi güncelleme için metadata parametresini üretir.
//
// nil harita SQL NULL'a çevrilir; COALESCE onu görünce sütunu OLDUĞU GİBİ
// bırakır. Boş olmayan harita ise gerçek bir yazımdır.
func patchMetadata(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return fromMetadata(m)
}
