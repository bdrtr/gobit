package pgstore

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Alan uzunluğu üst sınırları. Kimlik ve idempotency anahtarı indekslenen
// sütunlardır; sınırsız bırakılsalardı çok uzun bir değer B-tree'nin satır
// boyu sınırına çarpar ve anlaşılmaz bir sürücü hatası dönerdi. Sınır burada,
// tipli bir hataya çevrilebildiği yerde uygulanır.
const (
	maxIDLen   = 128
	maxNameLen = 128
	maxKeyLen  = 256
)

// nulEscape JSON metninde NUL karakterini yazan kaçış dizisidir: ters bölü,
// ardından u0000. Kaynakta kaçırılmış yazılır; değeri altı karakterdir.
const nulEscape = "\\u0000"

// jsonParam bir JSON alanını sorgu parametresine çevirir.
//
// Ayrım bilinçlidir ve iki yönde de korunur:
//
//   - nil json.RawMessage  → SQL NULL   ("değer yok")
//   - "null" baytları      → JSONB null (JSON'un kendi null DEĞERİ)
//
// Uzunluğu sıfır olan ama nil olmayan bir değer geçerli JSON değildir; NULL
// yazılır.
//
// Ölçüt JSONB'nin kabul ettiğidir, Go'nun kabul ettiği DEĞİL: json.Valid'i
// geçen bir gövde de veritabanında patlayabilir. Bu yüzden üç denetim yapılır
// — sözdizimi, UTF-8 geçerliliği ve NUL kaçışı. Son ikisi bırakılsaydı arıza
// sürücüden sınıflandırılmamış (KindInternal) bir hata olarak dönerdi; oysa
// kusur çağıranın verisindedir ve Invalid olmalıdır.
//
// Dönen değer any'dir çünkü nil'in kendisi (tipli bir []byte nil'i değil)
// pgx'e NULL olarak gitmelidir.
func jsonParam(raw json.RawMessage, field string) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, errors.Invalid(CodeInvalid, "%s alanı geçerli JSON değil", field)
	}
	if !utf8.Valid(raw) {
		return nil, errors.Invalid(CodeInvalid,
			"%s alanı geçerli UTF-8 değil; JSONB saklayamaz", field)
	}
	if hasNULEscape(raw) {
		return nil, errors.Invalid(CodeInvalid,
			"%s alanı %s kaçışı içeriyor; JSONB onu metne çeviremez", field, nulEscape)
	}
	return []byte(raw), nil
}

// hasNULEscape JSON metninde GERÇEK bir NUL kaçışı olup olmadığını söyler.
//
// PostgreSQL jsonb bu kaçışı reddeder (SQLSTATE 22P05): jsonb değeri metne
// çevrilebilmek zorundadır, NUL ise metinde yer alamaz.
//
// Düz arama yetmez: kaçırılmış bir ters bölünün ardından gelen u0000 kaçış
// DEĞİLDİR, sıradan altı karakterdir. Tarayıcı bu yüzden dizge içinde olup
// olmadığını izler ve her kaçışta bir sonraki karakteri atlar. Yalnızca
// json.Valid'i geçmiş gövde için çağrılır.
func hasNULEscape(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		if !inString {
			if raw[i] == '"' {
				inString = true
			}
			continue
		}
		switch raw[i] {
		case '"':
			inString = false
		case '\\':
			// JSON kaçışı yalnızca küçük "u" ile yazılır ve sıfır
			// basamaklarının büyük/küçük hâli yoktur; tam eşleşme yeter.
			if bytes.HasPrefix(raw[i+1:], []byte(nulEscape[1:])) {
				return true
			}
			i++
		}
	}
	return false
}

// jsonValue okunan JSONB baytlarını json.RawMessage'a çevirir.
//
// NULL sütun nil bayt dilimi olarak gelir ve nil RawMessage'a dönüşür; böylece
// "değer yok" ile JSON null yazma yönünde olduğu gibi okuma yönünde de ayrılır.
//
// Not: JSONB metni normalleştirir (anahtar sırası ve boşluk korunmaz). Geri
// okunan değer anlamca aynıdır, bayt bayt aynı olmayabilir.
func jsonValue(raw []byte) json.RawMessage {
	if raw == nil {
		return nil
	}
	return json.RawMessage(raw)
}

// keyParam idempotency anahtarını sorgu parametresine çevirir.
//
// BOŞ DİZE "anahtar yok" demektir ve NULL yazılır: boş dize saklansaydı
// anahtarsız iki yürütme kısmi benzersiz indekste birbiriyle çakışırdı.
//
// YALNIZCA BOŞLUKTAN oluşan anahtar reddedilir. Çağıran bir anahtar VERMİŞTİR;
// onu sessizce NULL'a çevirmek, istenen tekrar korumasını hiçbir uyarı
// vermeden kaldırırdı — aynı anahtarla açılan ikinci yürütme çakışmaz ve iş
// (örneğin bir tahsilat) iki kez yapılırdı. Okuma yolu da (bkz.
// [store.FindByIdempotencyKey]) aynı anahtarı reddeder; iki yolun kabul
// kümesi aynıdır.
//
// Dolu anahtar OLDUĞU GİBİ saklanır — anahtar dışarıdan gelen opak bir
// değerdir, kırpılırsa farklı iki anahtar aynı hâle gelebilir.
func keyParam(key string) (*string, error) {
	if key == "" {
		return nil, nil
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.Invalid(CodeInvalid,
			"idempotency anahtarı yalnızca boşluktan oluşamaz")
	}
	if len(key) > maxKeyLen {
		return nil, errors.Invalid(CodeInvalid,
			"idempotency anahtarı en fazla %d bayt olabilir, %d bayt verildi",
			maxKeyLen, len(key))
	}
	if !isStorableText(key) {
		return nil, errors.Invalid(CodeInvalid,
			"idempotency anahtarı NUL baytı ya da geçersiz UTF-8 dizisi içeremez")
	}
	return &key, nil
}

// keyValue NULL anahtarı boş dizeye çevirir.
func keyValue(key *string) string {
	if key == nil {
		return ""
	}
	return *key
}

// timeParam sıfır zamanı SQL NULL'a, diğerlerini UTC'ye çevirir (plan Bölüm 8).
func timeParam(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// timeValue NULL zamanı sıfır time.Time'a çevirir, diğerlerini UTC'ye taşır.
func timeValue(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// requireText zorunlu bir metin alanını doğrular ve kırpılmış hâlini döner.
//
// NUL baytı ya da geçersiz UTF-8 dizisi içeren değer reddedilir: PostgreSQL
// bunları TEXT sütununa yazamaz (SQLSTATE 22021) ve sürücüden dönen hata
// sınıflandırılmamış olurdu. Buradaki alanlar kimlik, ad ve durum gibi
// TANIMLAYICI alanlardır; kırpılıp düzeltilmeleri değil, reddedilmeleri
// doğrudur.
func requireText(value, field string, maxLen int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.Invalid(CodeInvalid, "%s boş olamaz", field)
	}
	if len(trimmed) > maxLen {
		return "", errors.Invalid(CodeInvalid,
			"%s en fazla %d bayt olabilir, %d bayt verildi", field, maxLen, len(trimmed))
	}
	if !isStorableText(trimmed) {
		return "", errors.Invalid(CodeInvalid,
			"%s NUL baytı ya da geçersiz UTF-8 dizisi içeremez", field)
	}
	return trimmed, nil
}

// isStorableText metnin TEXT sütununa yazılabilir olup olmadığını söyler.
//
// PostgreSQL UTF8 veritabanında iki şey saklanamaz: NUL baytı ve geçersiz
// UTF-8 dizisi. Go dizgeleri ikisini de taşıyabildiği için denetim uygulama
// tarafında yapılır.
func isStorableText(v string) bool {
	return !strings.ContainsRune(v, 0) && utf8.ValidString(v)
}

// safeText tanı amaçlı serbest metni TEXT sütununa yazılabilir hâle getirir.
//
// NUL baytı ve geçersiz UTF-8 dizileri ATILIR. Arıza açıklaması insana yönelik
// bir tanı metnidir ve kaydın DURUMU kadar değerli değildir: yazmayı reddetmek,
// yürütmenin uç durumunu hiç kaydedememek ve kaydı sonsuza dek "running"
// bırakmak demekti — o kayıt bir daha ne tamamlanabilir ne de idempotency
// anahtarı yeniden kullanılabilirdi.
func safeText(v string) string {
	if isStorableText(v) {
		return v
	}
	return strings.ReplaceAll(strings.ToValidUTF8(v, ""), "\x00", "")
}

// requireCount sayaç ve sıra numarası gibi negatif olamayan alanları doğrular.
//
// Üst sınır int32'dir: sütunlar INTEGER olduğu için daha büyük bir değer
// veritabanında taşardı; sınır burada tipli bir hataya çevrilir.
func requireCount(value int, field string) (int32, error) {
	if value < 0 {
		return 0, errors.Invalid(CodeInvalid, "%s negatif olamaz, %d verildi", field, value)
	}
	if value > math.MaxInt32 {
		return 0, errors.Invalid(CodeInvalid,
			"%s en fazla %d olabilir, %d verildi", field, math.MaxInt32, value)
	}
	return int32(value), nil
}

// createError yürütme açarken dönen ham sürücü hatasını tipli hataya çevirir.
//
// Çakışma İHLAL EDİLEN kısıtın adından okunur: idempotency indeksi ile birincil
// anahtar farklı arızalardır ve çağıran taraf ikisini ayırt edebilmelidir.
// "Önce SELECT sonra INSERT" yerine ihlalin yakalanması, iki sürecin aynı anda
// kayıt açtığı yarışta yalnızca birinin başarılı olmasını GARANTİ eder.
//
// SINIF DAĞITIMI (motor sınıfa bakarak dallandığı için sözleşmenin parçasıdır):
//
//   - idempotency indeksi → KindConflict. "Bu istek daha önce yapıldı"
//     diyen TEK ihlal budur; motor Conflict görünce tekrar (replay) yoluna
//     gider.
//   - birincil anahtar → KindInvalid. Çağıranın verdiği kimlik zaten
//     kullanılmıştır; bu bir tekrar isteği değil, girdi hatasıdır. Conflict
//     dönseydi motor onu idempotency çakışmasıyla karıştırır, aramadığı bir
//     anahtarı arar ve çağırana alakasız bir "yürütme okunamadı" hatası
//     dönerdi.
//   - tanınmayan kısıt → KindInternal. Şema koddaki varsayımdan sapmıştır;
//     ne olduğu bilinmeden Conflict demek motoru yanlış yola sokardı.
func createError(err error, id, name, key string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		details := map[string]any{
			keyExecutionID: id,
			keyWorkflow:    name,
		}
		switch pgErr.ConstraintName {
		case idempotencyIndex:
			return errors.Wrap(err, errors.KindConflict, CodeDuplicateKey,
				"%s workflow'unda %q idempotency anahtarıyla açılmış bir yürütme zaten var",
				name, key).WithDetails(details)
		case executionsPKConstraint:
			return errors.Wrap(err, errors.KindInvalid, CodeDuplicateID,
				"%q kimlikli yürütme zaten var", id).WithDetails(details)
		default:
			return errors.Wrap(err, errors.KindInternal, CodeConflict,
				"yürütme kaydı açılamadı: %s kısıtı ihlal edildi", pgErr.ConstraintName).
				WithDetails(details)
		}
	}
	return wrapDB(err, CodeQueryFailed, "%s workflow'u için yürütme kaydı açılamadı", name)
}

// wrapDB ham sürücü hatasını tipli hataya çevirir.
//
// İptal ve zaman aşımı KindUnavailable'dır (arıza değil, bütçenin dolması);
// bilinen şema ihlalleri ve çağıran verisinden doğan kodlama hataları kendi
// sınıflarına gider; kalan her şey KindInternal olarak sarmalanır. Ham hata
// zincirde kalır, errors.Is/As ile erişilebilir.
func wrapDB(err error, code, format string, a ...any) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.Wrap(err, errors.KindUnavailable, CodeCanceled,
			format+" (bağlam iptal edildi)", a...)
	case errors.Is(err, pgx.ErrNoRows):
		return errors.Wrap(err, errors.KindNotFound, CodeNotFound, format, a...)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case foreignKeyViolation:
			// Şemadaki tek yabancı anahtar adımı yürütmeye bağlar; ihlali
			// "yürütme yok" demektir.
			return errors.Wrap(err, errors.KindNotFound, CodeNotFound,
				format+" (bağlı yürütme kaydı yok)", a...)
		case checkViolation:
			// a'nın kendi dizisine yazmamak için yeni dilim kurulur; çağıranın
			// verdiği argümanlar değişmemelidir.
			args := make([]any, 0, len(a)+1)
			args = append(args, a...)
			args = append(args, pgErr.ConstraintName)
			return errors.Wrap(err, errors.KindInvalid, CodeInvalid,
				format+" (%s kısıtı ihlal edildi)", args...)
		case notInRepertoire, untranslatableCharacter, invalidTextRepresentation:
			// Değer sütun tipine çevrilemedi: metinde NUL baytı, JSON'da NUL
			// kaçışı, bozuk UTF-8 dizisi... Girdi doğrulaması bunları zaten
			// eler; buradaki dal, elemeden kaçan bir biçimin 500 olarak
			// dönmemesi için savunma katmanıdır (plan Bölüm 8: çağıran verisi
			// hatası Invalid'dir). Sürücü mesajı çağıranın verisini
			// taşıyabileceği için mesaja EKLENMEZ.
			return errors.Wrap(err, errors.KindInvalid, CodeInvalid,
				format+" (değer sütun tipine çevrilemedi)", a...)
		}
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}
