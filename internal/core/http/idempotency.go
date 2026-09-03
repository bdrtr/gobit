package http

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// IdempotencyKeyHeader istemcinin tekrar denemeyi işaretlediği başlıktır.
const IdempotencyKeyHeader = "Idempotency-Key"

// IdempotencyReplayedHeader yanıtın kayıttan çalındığını bildirir.
//
// İstemcinin "bu gerçekten şimdi mi oldu, yoksa daha önce mi" sorusunu
// yanıtlayabilmesi için vardır; yoksa iki denemeyi ayırt edemez.
const IdempotencyReplayedHeader = "Idempotency-Replayed"

// CodeIdempotencyConflict aynı anahtarın FARKLI bir gövdeyle kullanıldığını
// bildiren hata kodudur.
const CodeIdempotencyConflict = "idempotency_key_reuse"

// CodeIdempotencyKeyTooLong anahtarın uzunluk sınırını aştığını bildiren
// hata kodudur.
//
// [CodeIdempotencyConflict]'ten AYRI bir kod olması şarttır: iki durum
// istemciye ZIT şeyler söyler. "Yeniden kullanım" gören istemcinin doğru
// tepkisi YENİ bir anahtar üretip tekrar denemektir; anahtar uzun olduğu için
// reddedilen istemci bunu yaptığında yeni anahtar da uzun olur ve istemci
// sonsuza dek döner. Bu kod "anahtarı KISALT" der ve döngüyü kırar.
const CodeIdempotencyKeyTooLong = "idempotency_key_too_long"

// CodeIdempotencyInFlight aynı anahtarla eşzamanlı ikinci bir isteğin
// reddedildiğini bildiren hata kodudur.
const CodeIdempotencyInFlight = "idempotency_in_flight"

// maxIdempotencyKeyLen kabul edilen anahtar uzunluğunun üst sınırıdır.
//
// Sınırsız anahtar, deposu ne olursa olsun bellek/disk şişirme vektörüdür.
// Sınır İSTEMCİNİN gönderdiği ham başlığa uygulanır; depoya giden anahtar
// çağıranın kimliğiyle ad alanına alındığı için daha uzundur (bkz.
// [IdempotencyStore]).
const maxIdempotencyKeyLen = 255

// anonimIdempotencyKovasi kimliği çözülmemiş isteklerin paylaştığı ad alanıdır.
//
// TÜM anonim çağıranlar bu tek kovadadır; gerekçesi [Idempotency] godoc'unda.
const anonimIdempotencyKovasi = "anon"

// idempotencyKapanisSuresi handler bittikten sonraki depo yazımlarına verilen
// azami süredir.
//
// Kapanış çağrıları isteğin context'inden KOPARILDIĞI için (bkz.
// [kapanisContext]) onları durduracak başka bir şey kalmaz; süresiz bırakmak,
// yanıtı çoktan verilmiş bir isteğin goroutine'ini erişilemez bir depoya
// süresiz asardı. 5 saniye tek satır yazan bir depo için fazlasıyla uzun,
// kapanışta sunucuyu bekletmek için yeterince kısadır.
const idempotencyKapanisSuresi = 5 * time.Second

// maxIdempotentBodyBytes idempotent isteklerde tamponlanan azami gövde boyutudur.
//
// Gövdeyi parmak izi çıkarmak için okumak zorundayız; sınırsız okumak, tek bir
// isteğin sunucunun belleğini tüketmesine izin verirdi.
const maxIdempotentBodyBytes = 1 << 20 // 1 MiB

// defaultIdempotencyTTL kaydın ne kadar saklanacağıdır.
//
// 24 saat, Stripe'ın yerleşik davranışıyla aynıdır: bir istemcinin tekrar
// denemesi için fazlasıyla uzun, sonsuza dek saklamak için yeterince kısa.
const defaultIdempotencyTTL = 24 * time.Hour

// varsayilanIdempotencyButcesi bellek içi deponun tamamlanmış kayıtlar için
// harcayabileceği varsayılan bayt bütçesidir.
//
// 64 MiB, ölçülmüş bir taban ile ölçülmüş bir tavan arasında seçildi:
// [girdiYuku]'nun iki başlıklı, gövdesiz bir kayda biçtiği bedel 955 bayt
// olduğu için bütçe ~70.000 kayda, tipik bir sipariş yanıtında (~2 KiB)
// ~22.000 kayda, [maxIdempotentBodyBytes] sınırındaki bir yanıtta ise 63
// kayda karşılık gelir. Tek örnekli bir mağazanın 24 saatte ürettiği mutasyon
// sayısı ilk iki rakamın altındadır; üçüncüsüne düşen bir kurulumun ihtiyacı
// daha büyük bir bütçe değil PAYLAŞILAN bir depodur (GUARD_BACKEND=redis).
//
// Değer IDEMPOTENCY_MAX_MEMORY_BYTES ile değiştirilebilir; config'teki
// envDefault ile uyumu bir testle sabitlenmiştir.
const varsayilanIdempotencyButcesi int64 = 64 << 20

// girdiSabitYuku tek bir kaydın gövde, anahtar, parmak izi ve başlıklar
// DIŞINDA tuttuğu bayt sayısıdır.
//
// Ölçüldü (runtime.MemStats, GC sonrası, 200.000 kayıt): 44 baytlık anahtar,
// 32 baytlık parmak izi, BOŞ gövde ve HİÇ başlık ile kayıt başına 323 bayt
// tutuluyordu; anahtar ile parmak izi ayrıca yüklendiği için geriye kalan
// yapısal maliyet ~250 bayttır (girdi, liste düğümü, harita gözü). Sabit
// bilerek daha YÜKSEK seçildi.
//
// Yön önemlidir: eksik yüklemek, operatöre söylenen sınırın gerçekte sessizce
// aşılması demek olurdu. [girdiYuku] godoc'u ölçülmüş fazla yükleme oranını
// verir.
const girdiSabitYuku int64 = 320

// basliklarGrupYuku bir yanıt başlığı haritasının SEKİZLİ her grubu için
// yüklenen bayttır.
//
// Ölçüldü: aynı kayda tek bir başlık eklemek kayıt başına 675-323 = 352 bayt
// getiriyor, ikinci ve sekizinci başlık ise HİÇBİR ŞEY getirmiyordu (675 bayt
// sabit kaldı); dokuzuncudan sonra 1067 bayta çıktı. Go'nun haritası gözleri
// SEKİZLİ gruplar hâlinde ayırır ve bir başlık haritasının bedeli, başlık
// sayısıyla değil grup sayısıyla artar. Başlık başına sabit bir bedel biçmek
// tek başlıklı kaydı EKSİK yüklerdi — ölçümden önceki muhasebenin hatası tam
// olarak buydu (charged/actual = 0,95).
const basliklarGrupYuku int64 = 448

// basliklarGrupBoyu bir harita grubundaki göz sayısıdır.
const basliklarGrupBoyu = 8

// basliklarDegerYuku bir başlık değerinin dize içeriği dışında tuttuğu
// bayttır (tek elemanlı dilimin arka dizisi).
const basliklarDegerYuku int64 = 16

// tahliyeLogAraligi bütçe tahliyesi uyarılarının en sık yazılma aralığıdır.
//
// İlk tahliye HER ZAMAN loglanır; sonrası bu aralıkla kısılır. Kısıntı
// olmasaydı bütçesi sürekli dolu bir kurulumda her mutasyon isteği bir WARN
// satırı üretirdi ve uyarı, aradığı ilgiyi kendi gürültüsünde boğardı.
const tahliyeLogAraligi = time.Minute

// ErrIdempotencyKeyInFlight aynı anahtarla bir istek hâlâ işlenirken
// ikinci bir istek geldiğini bildirir.
var ErrIdempotencyKeyInFlight = errors.New("idempotency anahtarı işlemde")

// IdempotentResponse tekrar çalınacak yanıtın kaydıdır.
type IdempotentResponse struct {
	// Status kaydedilen HTTP durum kodudur.
	Status int
	// Header kaydedilen yanıt başlıklarıdır.
	Header http.Header
	// Body kaydedilen yanıt gövdesidir.
	Body []byte
	// Fingerprint isteğin çağıran+metod+yol+sorgu+gövde parmak izidir;
	// anahtarın farklı bir istekle yeniden kullanılmasını yakalamak için
	// saklanır.
	Fingerprint string
}

// IdempotencyStore idempotency kayıtlarını tutar.
//
// Uygulamalar eşzamanlı çağrıya güvenli olmalıdır.
//
// Aldığı key, istemcinin gönderdiği HAM başlık değildir: çağıranın kimliğiyle
// ad alanına alınmış hâlidir (bkz. [Idempotency]). Bu yüzden istemciye
// dayatılan 255 karakterlik sınırdan uzun olabilir ve kalıcı bir depo,
// sütununu kimliğin sığacağı genişlikte tanımlamalıdır.
type IdempotencyStore interface {
	// Begin anahtarı bu istek için ayırmaya çalışır.
	//
	// Anahtar yeniyse (nil, false, nil) döner ve anahtar "işlemde" işaretlenir.
	// Tamamlanmış bir kayıt varsa (kayıt, true, nil) döner.
	// Anahtar başka bir istek tarafından işlemdeyse
	// [ErrIdempotencyKeyInFlight] döner.
	Begin(ctx context.Context, key, fingerprint string) (*IdempotentResponse, bool, error)
	// Complete işlemi biten anahtarın yanıtını kaydeder.
	Complete(ctx context.Context, key string, resp IdempotentResponse) error
	// Abort ayırmayı geri alır; kayıt saklanmaz ve anahtar yeniden denenebilir.
	Abort(ctx context.Context, key string) error
}

// Idempotency aynı [IdempotencyKeyHeader] ile gelen tekrarları ilk yanıtla
// karşılayan middleware üretir.
//
// store nil ise middleware bir no-op'tur. Bu, [RateLimit] ile aynı gerekçeye
// dayanır: yapılandırılmamış bir altyapı bileşeni yüzünden tüm trafiği
// reddetmek, korumaya çalıştığı servisi çökertmek olurdu.
//
// Yalnızca GÜVENSİZ metodlara (POST, PUT, PATCH, DELETE) uygulanır. GET ve
// HEAD zaten tanımı gereği idempotenttir; onları kaydetmek yalnızca depoyu
// şişirirdi.
//
// Anahtar YOKSA istek normal akar. Anahtarı zorunlu kılmak, mevcut tüm
// istemcileri bir gecede kırardı; zorunluluk uç nokta bazında ayrıca
// dayatılmalıdır.
//
// 5xx yanıtlar KAYDEDİLMEZ: sunucu hatası geçici olabilir ve istemcinin
// tekrar denemesi tam da istediğimiz şeydir. Kalıcı bir 500'ü 24 saat boyunca
// çalmak, kendini onaran bir arızayı kalıcı arızaya çevirirdi.
//
// Bu koruma yalnızca DURUM KODUNA bakar ve bakabildiği tek şey odur: kararı
// gövdeden vermek, her yüzeyin hata biçimini bu middleware'e öğretmek olurdu
// — kural o an tek yerden çıkar ve her yeni zarf onu yeniden yazmayı
// gerektirir. Bedeli AÇIKTIR: iç hatasını da 200 ile bildiren bir yüzey
// korumanın DIŞINDA kalır ve geçici arıza TTL boyunca çalınır. Bugün depoda
// böyle tek bir yüzey var (GraphQL vitrin ucu; sözleşmesi gereği çözümlenen
// her isteğe 200 der) ve çözümü kaydı akıllandırmak değil, ucu yığından
// çıkarmaktır: bkz. [GuardOptions.IdempotencyExempt].
//
// # Kimlik ad alanı
//
// Hem depo anahtarı hem parmak izi ÇAĞIRANIN KİMLİĞİYLE ad alanına alınır
// (bkz. [PrincipalFromContext]); bu yüzden middleware kimlik doğrulamadan
// SONRA takılmalıdır (bkz. [APIGuards]). Ham başlık değeri doğrudan depo
// anahtarı olsaydı "1" ya da "order-1" gibi sıradan bir anahtarı seçen iki
// FARKLI çağıran aynı kayda düşerdi: istek bayt bayt aynıysa ikinci çağıran
// BİRİNCİNİN yanıtını oynatır — çapraz kiracı veri sızıntısı; farklıysa 409
// alır, yani bir çağıran diğerinin anahtar alanını işgal eder.
//
// Kimliği ÇÖZÜLMEMİŞ istekler tek bir ORTAK kovayı paylaşır: korumasız bir
// uçta tüm anonim çağıranlar aynı ad alanındadır ve yukarıdaki iki sonuç
// orada hâlâ mümkündür. Bu bilinçli bir tercihtir — anonim isteği IP'ye göre
// ayırmak, anahtarı gerçekten kiracıya bağlamadan (IP taklit edilebilir, NAT
// paylaşılır) idempotency'yi BOZARDI: mobil ağı değişip tekrar deneyen
// istemci kendi kaydını bulamaz ve tam da korumanın işe yarayacağı anda çift
// işlem yapardı.
//
// # Kimlik doğrulamak, ÇAĞIRANLARI AYIRMAK demek değildir
//
// Yukarıdaki mantık bir cümleyle biterdi — "anahtar alanının kiracıya ait
// olması gereken uçlar kimlik doğrulamanın ardında olmalıdır" — ve o cümle
// VİTRİNDE yanlış sonuç verir. /store/v1 kimlik doğrulamalıdır, ama çözülen
// kimlik alışverişçinin değil MAĞAZANINDIR: publishable anahtar her tarayıcıda
// aynıdır ve zaten gizli olmadığı [Authenticator.AuthenticateStore] godoc'unda
// yazılıdır. Yani vitrindeki her müşteri TEK bir kovayı paylaşır ve o kovanın
// içindeki kaydı seçen şey istemcinin seçtiği bir başlıktır.
//
// Vitrin bunu iki şeye borçlu olarak atlatır. Birincisi parmak izine YOLUN da
// girmesi: sepet kapsamlı uçların yolunda sepet kimliği vardır, dolayısıyla
// aynı anahtarı kendi sepetinde kullanan ikinci müşteri başkasının verisini
// değil 409 alır. İkincisi, geriye kalan tek uç — sepet YARATMA — bu halkadan
// MUAF tutulmuştur: yolu hiçbir yetenek taşımaz ve yanıtı bir yetenek ÜRETİR,
// yani aynı anahtar + aynı gövde ile gelen ikinci müşteriye birincinin sepet
// kimliği veriliyordu. Gerekçe ve ölçüm cmd/server'daki muafiyet listesinde.
//
// Buradan çıkan kural: bu middleware'i yeni bir yüzeye takarken sorulacak soru
// "kimlik doğrulanıyor mu" değil, "çözülen kimlik ÇAĞIRANI mı yoksa çağıranın
// bağlı olduğu KURULUMU mu adlandırıyor" olmalıdır.
func Idempotency(store IdempotencyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if store == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ham := strings.TrimSpace(r.Header.Get(IdempotencyKeyHeader))
			if ham == "" || !idempotentMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			// AKIŞLA işlenen gövdeler tamponlanMAZ ve idempotency kaydı da
			// alınmaz.
			//
			// Parmak izi gövdenin TAMAMINI okumayı gerektirir; bir dosya
			// yüklemesinde bu, akışın anlamını yok eder (aynı baytlar hem
			// bellekte hem diskte) ve sınırı da sessizce değiştirir: buradaki
			// 1 MiB tampon, yükleme ucunun kendi (çok daha büyük) sınırından
			// ÖNCE devreye girer ve istemci, ayarladığı sınırın altında bir
			// yerde "gövde çok büyük" hatası alır. İki farklı sınırın aynı
			// isteğe uygulanması, hangisinin konuştuğu anlaşılmayan bir arıza
			// üretirdi.
			//
			// Bedeli AÇIKTIR: multipart bir istek tekrarlandığında yeniden
			// işlenir. Yükleme için bu, ikinci bir dosya nesnesi demektir —
			// mükerrer bir kayıt, tamponlanmış bir akıştan ve yanlış sınırdan
			// ucuzdur. Gerçekten idempotent yükleme isteyen bir uç, anahtarı
			// gövdeden değil içerik ÖZETİNDEN türetmelidir.
			if akisliGovde(r) {
				next.ServeHTTP(w, r)
				return
			}

			if len(ham) > maxIdempotencyKeyLen {
				WriteError(r.Context(), w, coreerrors.Invalid(CodeIdempotencyKeyTooLong,
					"idempotency anahtarı en fazla %d karakter olabilir", maxIdempotencyKeyLen))
				return
			}

			govde, err := readLimited(r)
			if err != nil {
				WriteError(r.Context(), w, err)
				return
			}

			// Depoya giden anahtar HAM başlık değil, çağıranın kovasıyla ad
			// alanına alınmış hâlidir; gerekçesi godoc'un "Kimlik ad alanı"
			// bölümünde.
			kova := idempotencyKovasi(r.Context())
			izi := fingerprint(kova, r, govde)
			key := depoAnahtari(kova, ham)

			kayit, tamam, err := store.Begin(r.Context(), key, izi)

			switch {
			case errors.Is(err, ErrIdempotencyKeyInFlight):
				WriteError(r.Context(), w, coreerrors.Conflict(CodeIdempotencyInFlight,
					"aynı idempotency anahtarıyla bir istek hâlâ işleniyor"))

				return
			case err != nil:
				WriteError(r.Context(), w, err)
				return
			}

			if tamam {
				replay(r.Context(), w, kayit, izi)
				return
			}

			kaydet(r.Context(), w, r, next, store, key, izi)
		})
	}
}

// kaydet handler'ı çalıştırır ve yanıtı tamponlayıp depoya yazar.
//
// Ayrı fonksiyondur ki panik hâlinde ayırmanın geri alınmasını sağlayan
// defer, handler çağrısını tam olarak sarmalasın.
func kaydet(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	store IdempotencyStore,
	key, izi string,
) {
	rec := &recordingWriter{ResponseWriter: w, status: http.StatusOK}

	// Handler panikler ya da 5xx dönerse ayırma geri alınmalı; aksi hâlde
	// anahtar "işlemde" kilitli kalır ve istemci bir daha asla deneyemez.
	tamamlandi := false

	defer func() {
		if tamamlandi {
			return
		}

		kapanis, iptal := kapanisContext(ctx)
		defer iptal()

		if err := store.Abort(kapanis, key); err != nil {
			LoggerFromContext(ctx).ErrorContext(ctx,
				"idempotency ayırması geri alınamadı, anahtar kilitli kalabilir",
				"error", err)
		}
	}()

	next.ServeHTTP(rec, r)

	if rec.status >= http.StatusInternalServerError {
		return
	}

	if rec.tasti {
		// Yanıt tampon sınırını aştı: eksik bir gövdeyi kaydedip sonra
		// çalmak, istemciye BOZUK bir yanıt vermek olurdu. Kaydetmemek
		// yalnızca tekrarın yeniden işlenmesine yol açar.
		LoggerFromContext(ctx).WarnContext(ctx,
			"yanıt idempotency tampon sınırını aştı, kaydedilmiyor",
			"limit_bytes", maxIdempotentBodyBytes)

		return
	}

	kapanis, iptal := kapanisContext(ctx)
	defer iptal()

	if err := store.Complete(kapanis, key, IdempotentResponse{
		Status:      rec.status,
		Header:      rec.Header().Clone(),
		Body:        rec.buf.Bytes(),
		Fingerprint: izi,
	}); err != nil {
		// Yanıt istemciye ÇOKTAN yazıldı; artık hata döndüremeyiz.
		// Yapılabilecek tek doğru şey ayırmayı serbest bırakmak: aksi hâlde
		// anahtar sonsuza dek "işlemde" kalır ve istemci ne yanıt alabilir
		// ne de tekrar deneyebilir. Serbest bırakmanın bedeli, tekrarın
		// yeniden işlenme ihtimalidir — kalıcı kilitten iyidir.
		LoggerFromContext(ctx).ErrorContext(ctx,
			"idempotency kaydı yazılamadı, anahtar serbest bırakılıyor",
			"error", err)

		return
	}

	tamamlandi = true
}

// kapanisContext handler bittikten sonraki depo çağrıları için context üretir.
//
// İsteğin kendi context'i KULLANILAMAZ: istemci bağlantıyı keserse (tarayıcı
// sekmesi kapanır, yük dengeleyici zaman aşımına uğrar) o context iptal olur.
// İptal, tam da Complete/Abort'un çalıştığı ana denk gelirse ya kayıt hiç
// yazılmaz ya da ayırma geri alınamaz ve anahtar "işlemde" kilitli kalır —
// istemci ne yanıt alabilir ne tekrar deneyebilir. Oysa handler ÇOKTAN
// çalışmıştır: yan etkiler (tahsilat, sipariş) gerçekleşmiştir ve tekrarın
// onları ikinci kez üretmesini engellemek tam olarak bu kaydın işidir. Yani
// kapanış işlemleri isteğin ömrüne DEĞİL, sunucunun kendi ömrüne bağlıdır.
//
// WithoutCancel iptali koparır ama değerleri (logger, istek kimliği) korur;
// süre sınırı ise koparılmış çağrının sonsuza dek asılı kalmasını engeller.
func kapanisContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), idempotencyKapanisSuresi)
}

// replay kaydedilmiş yanıtı istemciye yazar.
//
// Parmak izi tutmuyorsa yanıt ÇALINMAZ: aynı anahtarla farklı bir istek
// göndermek istemci tarafında bir hatadır ve sessizce yanlış yanıtı döndürmek
// (örn. başka bir siparişin kaydı) sessiz veri bozulmasıdır.
func replay(ctx context.Context, w http.ResponseWriter, kayit *IdempotentResponse, izi string) {
	if kayit == nil {
		WriteError(ctx, w, coreerrors.Internal(defaultInternalCode,
			"idempotency kaydı boş döndü"))
		return
	}

	if kayit.Fingerprint != izi {
		WriteError(ctx, w, coreerrors.Conflict(CodeIdempotencyConflict,
			"bu idempotency anahtarı farklı bir istek için kullanılmış"))

		return
	}

	hedef := w.Header()
	for k, v := range kayit.Header {
		hedef[k] = append([]string(nil), v...)
	}

	hedef.Set(IdempotencyReplayedHeader, "true")
	w.WriteHeader(kayit.Status)
	// Çalınan gövde istemci girdisi değil, bu sunucunun daha önce ÜRETTİĞİ
	// yanıttır; Content-Type dâhil başlıkları da aynen çalınır. Yani ilk
	// yanıttan daha fazla risk taşımaz.
	_, _ = w.Write(kayit.Body) //nolint:gosec // G705: gövde sunucunun kendi ürettiği yanıttır
}

// readLimited istek gövdesini sınırlı biçimde okur ve tekrar okunabilir kılar.
//
// Sınırı aşan gövde KindInvalid ile reddedilir, yani istemci 422 görür.
// RFC 9110'un bu durum için ayırdığı kod 413'tür ve daha doğrudur; yine de
// 422 bilinçli olarak korunur. Sebep, status kodunun bu çerçevede tek tek
// çağrılarca değil hata SINIFINDAN türetilmesidir (bkz. [StatusFor]): 413
// dönmek için core/errors'a yeni bir Kind eklemek gerekir ve bu, tek bir
// middleware'in ihtiyacından çok daha geniş bir karardır. O gün gelene kadar
// istemcinin ayırt edici tutamağı status değil "body_too_large" KODUDUR —
// kod sözleşmenin değişmez tarafıdır, status ise sınıf eşlemesi
// değiştiğinde değişebilir.
func readLimited(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	// Sınırdan bir fazla okumaya çalış ki taşmayı ayırt edebilelim.
	govde, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotentBodyBytes+1))
	if err != nil {
		return nil, coreerrors.Invalid("invalid_body", "istek gövdesi okunamadı")
	}

	if len(govde) > maxIdempotentBodyBytes {
		return nil, coreerrors.Invalid("body_too_large",
			"idempotent istek gövdesi en fazla %d bayt olabilir", maxIdempotentBodyBytes)
	}

	// Gövdeyi tükettik; handler'ın okuyabilmesi için geri koy.
	r.Body = io.NopCloser(bytes.NewReader(govde))

	return govde, nil
}

// idempotencyKovasi çağıranın ad alanını üretir.
//
// Kimlik yoksa tüm anonim çağıranların PAYLAŞTIĞI ortak kova döner; bunun
// neden IP'ye göre ayrılmadığı [Idempotency] godoc'unda anlatılır.
func idempotencyKovasi(ctx context.Context) string {
	if p, ok := PrincipalFromContext(ctx); ok && p.ID != "" {
		return p.Kind + ":" + p.ID
	}

	return anonimIdempotencyKovasi
}

// depoAnahtari kovayı ve istemcinin anahtarını tek bir depo anahtarında birleştirir.
//
// Kovanın UZUNLUĞU öne yazılır. Düz birleştirme (kova + ayraç + anahtar)
// yetmezdi: ayraç iki parçanın herhangi birinde de geçebildiği için "a:b"
// kovası + "c" anahtarı ile "a" kovası + "b:c" anahtarı aynı dizeye düşerdi.
// Anahtarı istemci SEÇTİĞİNE göre bu, ad alanının kendisini istemciye
// açardı — yani düzeltmeye çalıştığımız sızıntının başka bir kapısı olurdu.
func depoAnahtari(kova, key string) string {
	return strconv.Itoa(len(kova)) + ":" + kova + ":" + key
}

// fingerprint isteğin kimliğini çağıran, metod, yol ve gövdeden türetir.
//
// Sorgu dizesi de dâhildir: aynı yola farklı filtrelerle giden iki POST
// farklı isteklerdir.
//
// Kova da karışıma girer. Depo anahtarı zaten kovayla ayrıldığından bu
// FAZLADAN bir savunmadır: ad alanını yanlış kuran ya da eski şemayla yazılmış
// satırlar taşıyan bir depo uygulamasında, başka bir çağıranın kaydı elimize
// geçse bile parmak izi tutmaz ve o yanıt oynatılmaz.
func fingerprint(kova string, r *http.Request, govde []byte) string {
	h := sha256.New()
	h.Write([]byte(kova))
	h.Write([]byte{0})
	h.Write([]byte(r.Method))
	h.Write([]byte{0})
	h.Write([]byte(r.URL.Path))
	h.Write([]byte{0})
	h.Write([]byte(r.URL.RawQuery))
	h.Write([]byte{0})
	h.Write(govde)

	return hex.EncodeToString(h.Sum(nil))
}

// akisliGovde isteğin gövdesinin akışla işlenmesi gereken bir tür olup
// olmadığını bildirir.
//
// Bugün yalnızca multipart. Ayrım Content-Type'tan yapılır çünkü karar gövde
// OKUNMADAN verilmelidir — okunduktan sonra vermek, tam da kaçınılmak istenen
// tamponlamayı yapmış olmak demektir.
func akisliGovde(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/")
}

// idempotentMethod metodun idempotency kaydı gerektirip gerektirmediğini bildirir.
func idempotentMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// recordingWriter yanıtı hem istemciye yazan hem de tamponlayan sarmalayıcıdır.
type recordingWriter struct {
	http.ResponseWriter
	status  int
	yazildi bool
	// tasti gövdenin tampon sınırını aştığını bildirir; aşan yanıt kaydedilmez.
	tasti bool
	buf   bytes.Buffer
}

// WriteHeader durum kodunu kaydeder ve aktarır.
func (w *recordingWriter) WriteHeader(status int) {
	if w.yazildi {
		return
	}

	w.status = status
	w.yazildi = true
	w.ResponseWriter.WriteHeader(status)
}

// Write gövdeyi hem tampona hem istemciye yazar.
func (w *recordingWriter) Write(b []byte) (int, error) {
	if !w.yazildi {
		w.WriteHeader(http.StatusOK)
	}

	// İstemciye her hâlükârda tam yanıt gider; sınırlanan yalnızca KAYIT.
	if !w.tasti {
		if w.buf.Len()+len(b) > maxIdempotentBodyBytes {
			w.tasti = true
			w.buf.Reset()
		} else {
			w.buf.Write(b)
		}
	}

	return w.ResponseWriter.Write(b)
}

// Unwrap sarmalanan yazıcıyı açar ki http.ResponseController çalışsın.
func (w *recordingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// girdi bellek içi depodaki tek bir anahtarın durumudur.
type girdi struct {
	// key haritadaki anahtardır ve girdinin KENDİSİNDE de durur.
	//
	// Hem süre dolumu hem bütçe tahliyesi sıra listesinin ÖNÜNDEN çalışır;
	// oradan haritaya dönmenin başka yolu yoktur. Anahtarı listeye ayrıca
	// koymak aynı dizeyi iki kez saklardı.
	key string
	// resp tamamlanmış yanıttır; işlemdeyken nil'dir.
	resp *IdempotentResponse
	// fingerprint ayırma sırasında verilen parmak izidir.
	fingerprint string
	// expiresAt kaydın geçerlilik sonudur.
	expiresAt time.Time
	// yuk bu girdinin bütçeden düşülen bayt karşılığıdır.
	//
	// Tamamlanmamış ayırmalarda SIFIRDIR; gerekçesi
	// [MemoryIdempotencyStore]'un bütçe bölümündedir. Girdide saklanır çünkü
	// düşerken yeniden hesaplamak, arada değişmiş bir yanıtta bütçeyi kalıcı
	// olarak kaydırırdı.
	yuk int64
	// el girdinin sıra listesindeki düğümüdür.
	el *list.Element
}

// MemoryIdempotencyStore süreç belleğinde çalışan idempotency deposudur.
//
// Tek örnekli kurulumlar ve testler içindir. Yatay ölçeklenen bir dağıtımda
// her örnek kendi kaydını tutar; aynı anahtarla farklı örneklere düşen iki
// istek İKİ KEZ işlenir. Çok örnekli kurulumda paylaşılan bir depo
// (Postgres ya da Redis) gerekir — bu, hız sınırlayıcıdaki durumun aksine
// bir hız değil DOĞRULUK sorunudur.
//
// # Bellek bütçesi
//
// Depo TAMAMLANMIŞ kayıtlar için bir bayt bütçesi tutar (bkz.
// [NewMemoryIdempotencyStore]) ve bütçe aşılınca en ESKİ kaydı DÜŞÜRÜR.
//
// Bütçesiz hâlde tek sınır TTL'di ve o sınır büyümeyi hiçbir yerde
// durdurmuyordu: kaydı açan anahtarı İSTEMCİ seçer, kayıt 24 saat yaşar ve
// yanıt gövdesi [maxIdempotentBodyBytes] kadar (1 MiB) olabilir. Bütçesiz
// depo ölçüldü (runtime.MemStats, GC sonrası): 1 KiB gövdeli 10.000 kayıt
// 15,51 MiB, 64 KiB gövdeli 10.000 kayıt 630,69 MiB, 1 MiB gövdeli 1.000
// kayıt 999,58 MiB tutuyordu; gövdesi BOŞ, başlıksız bir kayıt bile 323 bayt.
// Aynı yükte 24 saat sonunda düşen kayıt sayısı SIFIRDI (50.000 kayıt yazılıp
// 23 saat ilerletildi, harita 50.001'de kaldı). Varsayılan 600
// istek/dakikalık hız sınırıyla tek bir istemci 24 saatte 864.000 kayıt
// açabilir.
//
// Bütçeli hâlde AYNI yük ölçüldü (64 MiB bütçeyle): 64 KiB gövdeli 10.000
// kayıtta 630,69 MiB yerine 63,67 MiB tutuluyor ve haritada 1.009 kayıt
// kalıyor; 1 MiB gövdeli 1.000 kayıtta 999,58 MiB yerine 62,04 MiB ve 63
// kayıt. Tutulan bellek bütçenin ALTINDA kalır, çünkü muhasebe bilerek fazla
// yükler (bkz. [girdiYuku]). Bedeli kayıt başına ~%5 ek bellektir (1 KiB
// gövdeli kayıt 1626 bayttan 1706 bayta çıktı): sıra düğümü ve girdide ikinci
// kez tutulan anahtar.
//
// Bütçe yalnızca KAYITLARI kapsar. Hâlâ işlenmekte olan ayırmalar
// yüklenmez: gövdeleri yoktur (birkaç yüz bayt) ve sayıları sunucunun aynı
// anda taşıdığı istek sayısıyla zaten sınırlıdır. Yüklenselerdi, tahliye
// edilemeyen (bkz. aşağıdaki kural) bir yük bütçeyi tek başına doldurabilir
// ve operatöre söylenen sınır anlamını yitirirdi.
//
// # Neden en ESKİYİ düşürüyor, neden REDDETMİYOR
//
// Üç seçeneğin de bir bedeli vardı:
//
//   - Hiçbir şey yapmamak: süreç OOM ile ölür. Bedeli TÜM kayıtlar birden ve
//     o anda işlenmekte olan her istektir.
//   - Bütçe dolunca yeni isteği REDDETMEK: güvence tam kalır ama depoyu
//     dolduran şey istemcinin SEÇTİĞİ bir başlıktır — herhangi bir istemci
//     uydurduğu anahtarlarla mağazanın tüm mutasyon trafiğini kapatabilirdi.
//     Bir bellek arızası, tetiklemesi bedava bir erişim arızasına dönüşürdü.
//   - En eskiyi düşürmek: bedeli, düşen anahtarla TEKRAR gelen isteğin
//     yeniden işlenmesi, yani mükerrer bir yan etkidir.
//
// Üçüncüsü seçildi çünkü bedeli TTL sınırında ZATEN ödenen bedelin aynısıdır:
// süresi dolan kayıt nasıl olsa siliniyor ve ondan sonra gelen tekrar yeniden
// işleniyor. Tahliye bu silmeyi ERKENE alır. En eskinin seçilmesinin sebebi
// de budur — süresi dolmaya en yakın olan, korumasından geriye en az kalmış
// kayıttır.
//
// Ödün SESSİZ DEĞİLDİR: ilk tahliye ve sonrasında [tahliyeLogAraligi] kısıntısıyla
// WARN loglanır (bkz. [MemoryIdempotencyStore.Complete]), bütçe açılışta
// cmd/server tarafından yazılır ve .env.example'da belgelenmiştir.
//
// # Sıra listesi
//
// Kayıtlar haritanın yanında bir de bağlı listede, expiresAt'e göre ARTAN
// sırada durur. Liste iki şeyi birden ucuzlatır ve ikisi de ölçülmüştür:
//
//   - Tahliye. En eskiyi haritada aramak istek başına O(n) olurdu.
//   - Süre dolumu. Eski hâl TÜM haritayı tarıyordu ve tarama, sürecin TEK
//     kilidini tutarken koşuyordu: 1.000.000 kayıtta 50,3 ms, 100.000 kayıtta
//     2,13 ms. Artık yalnızca süresi dolan ÖN EK dolaşılır, yani maliyet
//     harita boyuyla değil gerçekten silinen kayıt sayısıyla orantılıdır: aynı
//     iki harita boyunda 188 ns ve 164 ns, yani boydan BAĞIMSIZ (benchmark,
//     aynı makine, silinecek kayıt yok).
//
// Sıralamayı ayakta tutan şey saatin GERİ GİTMEMESİDİR: hem ayırma hem
// tamamlama girdiyi listenin SONUNA koyar ve ikisi de expiresAt'i o anki
// zamana göre kurar. Saat geri giderse liste sıralı olmaktan çıkar ve süre
// dolumu erken durur; sonucu, birkaç kaydın hak ettiğinden UZUN yaşamasıdır.
// Bu, güvenli olan yöndür — koruma zayıflamaz — ve bellek yine bütçeyle
// sınırlıdır.
//
// # Kilidin altında ne YAPILMAZ
//
// Depoda tek bir mutex vardır ve HER mutasyon isteği ondan geçer. Bu yüzden
// kilidin altında yalnızca harita ve liste işlemleri yapılır; yanıt gövdesi
// kadar süren iki iş dışarıda tutulur:
//
//   - Kaydın kopyası. Hem yazarken (bkz. [MemoryIdempotencyStore.Complete])
//     hem oynatırken (bkz. [MemoryIdempotencyStore.ayir]) gövde kopyalanır ve
//     kopya 1 MiB'a kadar çıkabilir.
//   - Bütçe muhasebesi. [girdiYuku] başlıkları dolaşır.
//
// Ölçüldü (aynı makine, 16 goroutine): 1 MiB gövdeli kayıtların eşzamanlı
// OYNATILMASI kopya kilit altındayken 50,1-52,7 µs/işlem, dışındayken
// 34,5-40,8 µs/işlem; 64 KiB gövdeli eşzamanlı YAZMA 5,26-5,49 µs'ten
// 4,27-4,73 µs'e indi. Kazanç kilidin serbest bıraktığı paralellikten gelir;
// kopyanın kendisi ucuzlamaz.
//
// Bunun bedeli, oynatmada depodaki kayda kilit bırakıldıktan sonra
// dokunulmasıdır; onu güvenli kılan değişmezlik kuralı [MemoryIdempotencyStore.ayir]
// godoc'undadır.
type MemoryIdempotencyStore struct {
	// ttl kayıtların saklanma süresidir.
	ttl time.Duration
	// butce tamamlanmış kayıtların toplam bayt sınırıdır.
	butce int64
	// now zamanı okur; testler saati ilerletebilsin diye alandır.
	now func() time.Time

	mu    sync.Mutex
	girdi map[string]*girdi
	// sira girdileri expiresAt'e göre artan sırada tutar; en eskisi öndedir.
	sira *list.List
	// yuk tamamlanmış kayıtların [girdiYuku]'na göre toplam bayt karşılığıdır.
	yuk int64
	// tahliyeToplam bütçe yüzünden düşürülmüş toplam kayıt sayısıdır.
	tahliyeToplam int64
	// tahliyeBekleyen son uyarıdan bu yana düşürülmüş kayıt sayısıdır.
	tahliyeBekleyen int
	// tahliyeLogAt bir sonraki uyarının yazılabileceği en erken andır.
	tahliyeLogAt time.Time
}

// NewMemoryIdempotencyStore verilen saklama süresi ve bellek bütçesiyle
// bellek içi depo kurar.
//
// ttl sıfır ya da negatifse [defaultIdempotencyTTL] kullanılır; butce sıfır ya
// da negatifse [varsayilanIdempotencyButcesi] kullanılır.
//
// butce, tamamlanmış kayıtların toplam bayt sınırıdır ve aşılınca en eski
// kayıt düşürülür; ne anlama geldiği ve neden düşürmenin reddetmeye tercih
// edildiği [MemoryIdempotencyStore] godoc'undadır.
//
// [maxIdempotentBodyBytes]'tan (1 MiB) küçük bir bütçe verilebilir ama
// ANLAMSIZDIR: o boyuta yaklaşan tek bir yanıt yazıldığı anda bütçeyi aşar ve
// hemen kendisi düşürülür, yani büyük yanıtlar hiç oynatılamaz. Yapılandırma
// yolunda bu, config.Validate tarafından açılışta reddedilir; kurucu, testler
// küçük bütçeleri kasten kullanabilsin diye kısıtlamaz.
func NewMemoryIdempotencyStore(ttl time.Duration, butce int64) *MemoryIdempotencyStore {
	if ttl <= 0 {
		ttl = defaultIdempotencyTTL
	}

	if butce <= 0 {
		butce = varsayilanIdempotencyButcesi
	}

	return &MemoryIdempotencyStore{
		ttl:   ttl,
		butce: butce,
		now:   time.Now,
		girdi: make(map[string]*girdi),
		sira:  list.New(),
	}
}

// Butce deponun ÇALIŞTIĞI bayt bütçesini döner.
//
// Erişimci, yapılandırmadan gelen sayının depoya gerçekten ULAŞTIĞINI bileşim
// kökünün sınayabilmesi içindir. Sınanmadığında sessiz kalan bir hâl var ve
// ölçüldü: kurucuya sıfır geçen bir bağlama noktası varsayılan bütçeyle çalışır
// ama açılış logu yapılandırmadaki sayıyı yazmayı sürdürür, yani operatör
// yürürlükte OLMAYAN bir sınır okur. Havuzun MaxConns'ında aynı sınıf hata
// mutasyonla bulunmuştu; burada da kapatıldı.
func (s *MemoryIdempotencyStore) Butce() int64 { return s.butce }

// Begin anahtarı ayırır ya da mevcut kaydı döner.
//
// Oynatılan kaydın KOPYASI kilidin DIŞINDA çıkarılır; ölçüsü ve gerekçesi
// [MemoryIdempotencyStore]'un kilit bölümündedir.
func (s *MemoryIdempotencyStore) Begin(
	_ context.Context, key, fp string,
) (*IdempotentResponse, bool, error) {
	kayit, err := s.ayir(s.now(), key, fp)
	if kayit == nil || err != nil {
		return nil, false, err
	}

	// Kopya dön: çağıran döndürülen kaydı değiştirirse depo bozulmasın.
	kopya := *kayit
	kopya.Header = kayit.Header.Clone()
	kopya.Body = bytes.Clone(kayit.Body)

	return &kopya, true, nil
}

// ayir anahtarı ayırır ya da oynatılacak kaydı DOĞRUDAN (kopyalamadan) döner.
//
// Döndürülen işaretçi depodaki kaydın kendisidir ve çağıran onu kilit
// bırakıldıktan sonra kopyalar. Bunun güvenli olmasını sağlayan tek şey,
// yayımlanmış bir kaydın bir daha DEĞİŞMEMESİDİR: [MemoryIdempotencyStore.yaz]
// girdinin resp alanını yeni bir işaretçiyle DEĞİŞTİRİR, işaret ettiği yapıyı
// hiçbir zaman yerinde güncellemez. O kural bozulursa buradaki kopyalama
// yarışa girer.
func (s *MemoryIdempotencyStore) ayir(
	now time.Time, key, fp string,
) (*IdempotentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.collect(now)

	g, ok := s.girdi[key]
	if !ok {
		yeni := &girdi{key: key, fingerprint: fp, expiresAt: now.Add(s.ttl)}
		yeni.el = s.sira.PushBack(yeni)
		s.girdi[key] = yeni

		return nil, nil
	}

	if g.resp == nil {
		return nil, ErrIdempotencyKeyInFlight
	}

	return g.resp, nil
}

// Complete yanıtı kaydeder.
//
// Kayıt bütçeyi aşırırsa en eski kayıtlar düşürülür ve bu WARN loglanır: ilk
// tahliye her zaman, sonrası [tahliyeLogAraligi] kısıntısıyla. Uyarı kilidin
// DIŞINDA yazılır — log yazıcısı bloklarsa sürecin tek idempotency kilidi
// onunla birlikte bloklanmamalıdır.
func (s *MemoryIdempotencyStore) Complete(
	ctx context.Context, key string, resp IdempotentResponse,
) error {
	// Kopya ve muhasebe kilidin DIŞINDA hazırlanır; ölçüsü ve gerekçesi
	// [MemoryIdempotencyStore]'un kilit bölümündedir.
	kopya := resp
	kopya.Header = make(http.Header, len(resp.Header))
	maps.Copy(kopya.Header, resp.Header)
	kopya.Body = bytes.Clone(resp.Body)

	rapor, toplam := s.yaz(s.now(), key, &kopya, girdiYuku(key, &kopya))
	if rapor > 0 {
		LoggerFromContext(ctx).WarnContext(ctx,
			"idempotency bellek bütçesi doldu, en eski kayıtlar düşürülüyor",
			"budget_bytes", s.butce,
			"dropped_since_last_warning", rapor,
			"dropped_total", toplam,
			"consequence", "düşen anahtarla gelen tekrar YENİDEN işlenir",
			"remedy", "GUARD_BACKEND=redis ya da daha büyük IDEMPOTENCY_MAX_MEMORY_BYTES")
	}

	return nil
}

// yaz kaydı yerleştirir, bütçeyi uygular ve uyarı için sayıları döner.
//
// rapor sıfırdan büyükse çağıran uyarıyı yazmalıdır; kısıntı burada
// uygulanır ki karar, kilidin altında tutulan sayaçlarla verilsin.
func (s *MemoryIdempotencyStore) yaz(
	now time.Time, key string, kopya *IdempotentResponse, yuk int64,
) (rapor int, toplam int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.girdi[key]
	if !ok {
		// Ayırma olmadan gelen tamamlama da yazılır; gerekçe redisguard'ın
		// Complete godoc'unda: handler çalışmış ve yan etkileri gerçekleşmiştir.
		g = &girdi{key: key}
		g.el = s.sira.PushBack(g)
		s.girdi[key] = g
	} else {
		s.yuk -= g.yuk
		s.sira.MoveToBack(g.el)
	}

	g.resp = kopya
	g.fingerprint = kopya.Fingerprint
	g.expiresAt = now.Add(s.ttl)
	g.yuk = yuk
	s.yuk += g.yuk

	dusen := s.butceyeSigdir()
	if dusen == 0 {
		return 0, s.tahliyeToplam
	}

	s.tahliyeToplam += int64(dusen)
	s.tahliyeBekleyen += dusen

	if now.Before(s.tahliyeLogAt) {
		return 0, s.tahliyeToplam
	}

	rapor = s.tahliyeBekleyen
	s.tahliyeBekleyen = 0
	s.tahliyeLogAt = now.Add(tahliyeLogAraligi)

	return rapor, s.tahliyeToplam
}

// Abort ayırmayı geri alır.
//
// Yalnızca TAMAMLANMAMIŞ bir ayırma silinir: tamamlanmış bir kaydı silmek,
// geç gelen bir Abort'un çalınabilir yanıtı yok etmesi demek olurdu.
func (s *MemoryIdempotencyStore) Abort(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if g, ok := s.girdi[key]; ok && g.resp == nil {
		s.sil(g)
	}

	return nil
}

// collect süresi dolmuş kayıtları siler. Çağıran s.mu'yu tutuyor olmalıdır.
//
// Yalnızca listenin ÖN EKİNİ dolaşır ve ilk süresi dolmamış girdide durur;
// sıralamanın hangi varsayıma dayandığı [MemoryIdempotencyStore] godoc'unda.
//
// Her [MemoryIdempotencyStore.Begin]'de koşar ve bu bilinçlidir. Eski hâl
// taramayı dakikada bire kısıyordu, çünkü tarama TÜM haritayı dolaşıyordu;
// kısıntının bedeli, süresi dolmuş bir kaydın bir dakikaya kadar OYNATILMAYA
// devam etmesiydi — TTL'in operatöre söylediğinden uzun bir koruma. Ön ek
// dolaşımı kısıntıyı gereksiz kıldığı için o sapma da kapandı.
func (s *MemoryIdempotencyStore) collect(now time.Time) {
	for e := s.sira.Front(); e != nil; e = s.sira.Front() {
		g, ok := e.Value.(*girdi)
		if !ok || !now.After(g.expiresAt) {
			return
		}

		s.sil(g)
	}
}

// butceyeSigdir bütçe aşılmışsa en eski KAYITLARI düşürür ve sayısını döner.
// Çağıran s.mu'yu tutuyor olmalıdır.
//
// Tamamlanmamış ayırmalar atlanır: onlar bütçeye yüklenmez (yani düşürmek
// bütçeyi rahatlatmaz) ve düşürülmeleri çok daha pahalıya mal olurdu —
// işlemekte olan bir isteğin ayırması silinirse AYNI ANDA gelen ikinci istek
// de geçer, yani engellenmesi gereken çift işlem tam da o anda olur.
func (s *MemoryIdempotencyStore) butceyeSigdir() int {
	dusen := 0

	for e := s.sira.Front(); e != nil && s.yuk > s.butce; {
		sonraki := e.Next()

		if g, ok := e.Value.(*girdi); ok && g.resp != nil {
			s.sil(g)
			dusen++
		}

		e = sonraki
	}

	return dusen
}

// sil girdiyi haritadan ve sıradan çıkarıp yükünü bütçeden düşer.
// Çağıran s.mu'yu tutuyor olmalıdır.
func (s *MemoryIdempotencyStore) sil(g *girdi) {
	s.sira.Remove(g.el)
	delete(s.girdi, g.key)
	s.yuk -= g.yuk
}

// girdiYuku bir kaydın bütçeden düşülecek bayt karşılığını hesaplar.
//
// Gövde, anahtar, parmak izi ve başlık adları/değerleri dize UZUNLUKLARIYLA;
// kaydın geri kalanı ölçülmüş sabitlerle yüklenir (bkz. [girdiSabitYuku],
// [basliklarGrupYuku]).
//
// Sonuç bir TAHMİNDİR, tam bayt sayısı değildir; Go'nun ayırıcısının boy
// sınıflarını ve haritanın iç düzenini bir formülle birebir izlemek mümkün
// değildir. Tahminin YÖNÜ ise ölçülerek sabitlendi: aşağıdaki her biçimde
// yüklenen bedel, GERÇEKTE tutulan bayttan büyüktür (runtime.MemStats, GC
// sonrası, aynı makine):
//
//	biçim                               gerçek   yüklenen   oran
//	anahtar 44, gövde 0, başlık 0         323 B     396 B   1,23
//	anahtar 44, gövde 0, başlık 2         675 B     955 B   1,41
//	anahtar 44, gövde 0, başlık 8         675 B    1284 B   1,90
//	anahtar 44, gövde 0, başlık 10       1067 B    1842 B   1,73
//	anahtar 44, gövde 2 KiB, başlık 2    2731 B    3003 B   1,10
//	anahtar 44, gövde 64 KiB, başlık 2  66214 B   66491 B   1,00
//
// Fazla yükleme bedavaya gelmez — bütçe, sığdırabileceğinden daha az kayıt
// tutar — ama ters yön OOM demektir; gerekçesi [girdiSabitYuku]'ndadır. Oran
// gövde büyüdükçe 1'e yaklaşır, çünkü büyük kayıtlarda bedelin neredeyse
// tamamı gövdedir ve gövde TAM ölçülür.
func girdiYuku(key string, resp *IdempotentResponse) int64 {
	yuk := girdiSabitYuku +
		int64(len(key)) +
		int64(len(resp.Fingerprint)) +
		int64(len(resp.Body))

	if len(resp.Header) > 0 {
		grup := (len(resp.Header) + basliklarGrupBoyu - 1) / basliklarGrupBoyu
		yuk += int64(grup) * basliklarGrupYuku
	}

	for ad, degerler := range resp.Header {
		yuk += int64(len(ad))
		for _, deger := range degerler {
			yuk += basliklarDegerYuku + int64(len(deger))
		}
	}

	return yuk
}
