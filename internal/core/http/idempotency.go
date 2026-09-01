package http

import (
	"bytes"
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
// işlem yapardı. Anahtar alanının kiracıya ait olması gereken uçlar zaten
// kimlik doğrulamanın ardında olmalıdır.
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
	// resp tamamlanmış yanıttır; işlemdeyken nil'dir.
	resp *IdempotentResponse
	// fingerprint ayırma sırasında verilen parmak izidir.
	fingerprint string
	// expiresAt kaydın geçerlilik sonudur.
	expiresAt time.Time
}

// MemoryIdempotencyStore süreç belleğinde çalışan idempotency deposudur.
//
// Tek örnekli kurulumlar ve testler içindir. Yatay ölçeklenen bir dağıtımda
// her örnek kendi kaydını tutar; aynı anahtarla farklı örneklere düşen iki
// istek İKİ KEZ işlenir. Çok örnekli kurulumda paylaşılan bir depo
// (Postgres ya da Redis) gerekir — bu, hız sınırlayıcıdaki durumun aksine
// bir hız değil DOĞRULUK sorunudur.
type MemoryIdempotencyStore struct {
	// ttl kayıtların saklanma süresidir.
	ttl time.Duration
	// now zamanı okur; testler saati ilerletebilsin diye alandır.
	now func() time.Time

	mu      sync.Mutex
	girdi   map[string]*girdi
	temizAt time.Time
}

// NewMemoryIdempotencyStore verilen saklama süresiyle bellek içi depo kurar.
//
// ttl sıfır ya da negatifse [defaultIdempotencyTTL] kullanılır.
func NewMemoryIdempotencyStore(ttl time.Duration) *MemoryIdempotencyStore {
	if ttl <= 0 {
		ttl = defaultIdempotencyTTL
	}

	return &MemoryIdempotencyStore{
		ttl:   ttl,
		now:   time.Now,
		girdi: make(map[string]*girdi),
	}
}

// Begin anahtarı ayırır ya da mevcut kaydı döner.
func (s *MemoryIdempotencyStore) Begin(
	_ context.Context, key, fp string,
) (*IdempotentResponse, bool, error) {
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.collect(now)

	g, ok := s.girdi[key]
	if !ok {
		s.girdi[key] = &girdi{fingerprint: fp, expiresAt: now.Add(s.ttl)}
		return nil, false, nil
	}

	if g.resp == nil {
		return nil, false, ErrIdempotencyKeyInFlight
	}

	// Kopya dön: çağıran döndürülen kaydı değiştirirse depo bozulmasın.
	kopya := *g.resp
	kopya.Header = g.resp.Header.Clone()
	kopya.Body = bytes.Clone(g.resp.Body)

	return &kopya, true, nil
}

// Complete yanıtı kaydeder.
func (s *MemoryIdempotencyStore) Complete(
	_ context.Context, key string, resp IdempotentResponse,
) error {
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	kopya := resp
	kopya.Header = make(http.Header, len(resp.Header))
	maps.Copy(kopya.Header, resp.Header)
	kopya.Body = bytes.Clone(resp.Body)

	s.girdi[key] = &girdi{
		resp:        &kopya,
		fingerprint: resp.Fingerprint,
		expiresAt:   now.Add(s.ttl),
	}

	return nil
}

// Abort ayırmayı geri alır.
//
// Yalnızca TAMAMLANMAMIŞ bir ayırma silinir: tamamlanmış bir kaydı silmek,
// geç gelen bir Abort'un çalınabilir yanıtı yok etmesi demek olurdu.
func (s *MemoryIdempotencyStore) Abort(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if g, ok := s.girdi[key]; ok && g.resp == nil {
		delete(s.girdi, key)
	}

	return nil
}

// collect süresi dolmuş kayıtları siler. Çağıran s.mu'yu tutuyor olmalıdır.
func (s *MemoryIdempotencyStore) collect(now time.Time) {
	if now.Before(s.temizAt) {
		return
	}

	s.temizAt = now.Add(gcInterval)

	for k, g := range s.girdi {
		if now.After(g.expiresAt) {
			delete(s.girdi, k)
		}
	}
}
