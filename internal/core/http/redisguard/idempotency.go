package redisguard

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// DefaultIdempotencyTTL kaydın varsayılan saklanma süresidir.
//
// corehttp.NewMemoryIdempotencyStore'un varsayılanıyla aynıdır; iki depo
// birbirinin yerine takılabildiği için aynı girdide farklı davranmaları
// sinsi bir tuzak olurdu.
const DefaultIdempotencyTTL = 24 * time.Hour

// Kaydın durumunu değerin başındaki imden okuruz.
//
// Durumu ayrı bir alana (örn. JSON içinde "durum") koymak, Abort'un kaydı
// silip silmeyeceğine karar verebilmek için Lua tarafında cjson çözmeyi
// gerektirirdi. İm, değerin ilk iki baytıdır: Abort tek bir string.sub
// karşılaştırmasıyla "bu ayırma hâlâ işlemde mi" sorusunu yanıtlar.
const (
	// imIslemde ayrılmış ama henüz tamamlanmamış kaydın imidir; ardından
	// ayırmayı yapan isteğin parmak izi gelir.
	imIslemde = "i:"
	// imTamam tamamlanmış kaydın imidir; ardından JSON gövde gelir.
	imTamam = "c:"
)

// beginBetigi anahtarı ayırır ya da mevcut değeri döner.
//
// SET NX ayırmanın kendisini atomik yapar: iki istek aynı anda gelirse
// yalnızca biri true alır, diğeri kaydı okur. GET'in AYNI betikte olması
// şarttır; ayrı bir gidiş-dönüş olsaydı SET NX'in başarısız olmasıyla GET'in
// çalışması arasında anahtarın TTL'i dolabilir, GET boş dönerdi ve "kayıt yok
// ama ayırma da bende değil" gibi karar verilemez bir duruma düşerdik.
//
// Yeni ayırmada BOŞ DİZE döner. Saklanan her değer bir imle ([imIslemde] ya
// da [imTamam]) başladığı için boş dize gerçek bir kayıtla karışamaz. Lua'nın
// false'u (yani Redis nil'i) bu iş için kullanılamazdı: GET'in boş dönmesi de
// aynı nil'e düşer ve "ayırmayı ben aldım" ile "anahtar kayboldu" ayırt
// edilemez olurdu — ilkini ikincisi sanmak iki isteğin birden anahtarı
// sahiplenmesi demektir.
var beginBetigi = redis.NewScript(`
if redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2], 'NX') then
  return ''
end
return redis.call('GET', KEYS[1])
`)

// abortBetigi yalnızca TAMAMLANMAMIŞ bir ayırmayı siler.
//
// Koşulsuz DEL, geç gelen bir Abort'un (örn. handler paniklediğinde çalışan
// defer) çoktan yazılmış bir yanıtı yok etmesine izin verirdi; o kayıt
// silinirse tekrar gelen istek baştan işlenir ve idempotency'nin engellemesi
// gereken çift işlem tam da bu yolla olur.
var abortBetigi = redis.NewScript(`
local mevcut = redis.call('GET', KEYS[1])
if mevcut and string.sub(mevcut, 1, string.len(ARGV[1])) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// kayit tamamlanmış yanıtın Redis'te saklanan biçimidir.
//
// Body []byte'tır ve encoding/json onu BASE64 olarak yazar; ~%33 yer kaybı
// bilinçlidir. Alan string olsaydı json.Marshal geçersiz UTF-8 baytlarını
// sessizce U+FFFD ile değiştirirdi: PDF, resim ya da protobuf döndüren bir uç
// noktanın çalınan yanıtı BOZUK çıkardı ve bu bozulma ancak istemcide fark
// edilirdi.
type kayit struct {
	Status      int         `json:"status"`
	Header      http.Header `json:"header,omitempty"`
	Body        []byte      `json:"body,omitempty"`
	Fingerprint string      `json:"fingerprint"`
}

// IdempotencyStore idempotency kayıtlarını Redis'te tutar.
//
// Kayıt tek bir anahtarda, tek bir dizede yaşar: durum imi + (parmak izi ya
// da JSON gövde). Ayırma ve okuma tek atomik adımdır, bu yüzden aynı anahtarla
// aynı anda gelen iki istekten yalnızca biri işi yapar — hangi örneğe
// düştükleri fark etmez.
//
// # Anahtar biçimi
//
// Kayıtlar "<önek>:idem:<anahtar>" adresine yazılır; varsayılan önekle
// "kiracı-1:abc" anahtarı "gobit:idem:kiracı-1:abc" kaydına düşer. Önek
// kurucudan gelir ve aynı Redis'i paylaşan iki kurulumu ayıran şeydir (bkz.
// paket godoc'u).
//
// Depoya gelen anahtar çağıranın kimliğiyle ad alanına alınmış hâldedir ve
// istemciye dayatılan 255 karakterlik sınırdan uzun olabilir (bkz.
// corehttp.IdempotencyStore godoc'u); Redis'te anahtar uzunluğu sorun
// olmadığı için kısaltılmaz, olduğu gibi öneke eklenir. Kısaltmak (örn.
// hash'lemek) çakışma riski getirir ve çakışan iki anahtar, iki FARKLI
// isteğin birbirinin yanıtını görmesi demektir.
type IdempotencyStore struct {
	client *redis.Client
	// onek kayıt anahtarlarının TAM önekidir (örn. "gobit:idem:").
	//
	// Ad alanı önekiyle bölüm adı her çağrıda yeniden birleştirilmesin diye
	// kurucuda bir kez kurulur.
	onek string
	// ttl kayıtların saklanma süresidir.
	ttl time.Duration
	// ttlMs ttl'in milisaniye karşılığıdır; betiğe her çağrıda yeniden
	// hesaplanmasın diye kurucuda bir kez çıkarılır.
	ttlMs int64
}

var _ corehttp.IdempotencyStore = (*IdempotencyStore)(nil)

// NewIdempotencyStore verilen saklama süresiyle Redis tabanlı depo kurar.
//
// keyPrefix kayıtların ad alanı önekidir; kayıtlar "<keyPrefix>:idem:<anahtar>"
// adresine yazılır. Biçimi [dogrulaOnek] denetler ve geçersiz önek HATA döner.
//
// ÖNEKTE ttl'in aksine varsayılana düşülmez ve bu ayrım bilinçlidir: geçersiz
// bir ttl'in bedeli kaydın beklenenden uzun/kısa yaşamasıdır, geçersiz bir
// önekin bedeli ise iki kurulumun AYNI ad alanını paylaşmasıdır — yani birinin
// yanıtının ötekinin istemcisine gitmesi. Sessizce düzeltmek, düzeltmeye
// çalıştığımız arızayı geri getirirdi.
//
// ttl sıfır ya da negatifse [DefaultIdempotencyTTL] kullanılır; bu,
// corehttp.NewMemoryIdempotencyStore ile aynı davranıştır. client nil ise
// hata döner.
func NewIdempotencyStore(client *redis.Client, keyPrefix string, ttl time.Duration) (*IdempotencyStore, error) {
	if client == nil {
		return nil, coreerrors.Invalid(CodeInvalidConfig, "redis istemcisi nil olamaz")
	}

	if err := dogrulaOnek(keyPrefix); err != nil {
		return nil, err
	}

	if ttl <= 0 {
		ttl = DefaultIdempotencyTTL
	}

	// Redis'in en küçük çözünürlüğü milisaniyedir; daha kısa bir ttl "PX 0"
	// ile komut hatasına dönerdi.
	ttl = max(ttl, time.Millisecond)

	return &IdempotencyStore{
		client: client,
		onek:   keyPrefix + ayirici + idempotencyBolumu + ayirici,
		ttl:    ttl,
		ttlMs:  ttl.Milliseconds(),
	}, nil
}

// Begin anahtarı bu istek için ayırmaya çalışır.
//
// Anahtar yeniyse (nil, false, nil) döner ve anahtar "işlemde" işaretlenir;
// tamamlanmış bir kayıt varsa (kayıt, true, nil); başka bir istek anahtarı
// işlerken corehttp.ErrIdempotencyKeyInFlight döner.
//
// fingerprint ayırma imine yazılır. Bugün hiçbir karar ona bakmaz —
// corehttp.Idempotency parmak izini yalnızca TAMAMLANMIŞ kayıtta karşılaştırır
// — ama "bu anahtarı hangi istek tuttu" sorusunun yanıtı üretimde bir sorunu
// teşhis ederken tek başına redis-cli GET ile okunabilir olmalıdır.
func (s *IdempotencyStore) Begin(
	ctx context.Context, key, fingerprint string,
) (*corehttp.IdempotentResponse, bool, error) {
	deger, err := beginBetigi.Run(ctx, s.client,
		[]string{s.onek + key},
		imIslemde+fingerprint,
		s.ttlMs,
	).Text()

	switch {
	case coreerrors.Is(err, redis.Nil):
		// SET NX başarısız oldu ama GET boş döndü. Betik atomik çalıştığı için
		// bu durum beklenmez; "yeni ayırma" saymak İKİ isteğin birden anahtarı
		// sahiplendiğini sanmasına yol açacağı için hata dönülür.
		return nil, false, coreerrors.Unavailable(CodeIdempotencyStoreFailed,
			"idempotency anahtarı ne ayrılabildi ne de okunabildi")
	case err != nil:
		return nil, false, coreerrors.Wrap(err, coreerrors.KindUnavailable,
			CodeIdempotencyStoreFailed, "idempotency anahtarı ayrılamadı")
	}

	switch {
	case deger == "":
		return nil, false, nil
	case strings.HasPrefix(deger, imIslemde):
		return nil, false, corehttp.ErrIdempotencyKeyInFlight
	case !strings.HasPrefix(deger, imTamam):
		// Anahtarı bu paketin dışında biri yazmış (önek çakışması) ya da kayıt
		// biçimi değişmiş. Tanımadığımız bir değeri kayıt sanıp çözmeye
		// çalışmaktansa hata dönmek doğrudur: yanlış çözülen bir kayıt,
		// istemciye BAŞKA bir isteğin yanıtı olarak gidebilir.
		return nil, false, coreerrors.Internal(CodeIdempotencyStoreFailed,
			"idempotency kaydının durum imi tanınmadı")
	}

	var k kayit
	if err := json.Unmarshal([]byte(strings.TrimPrefix(deger, imTamam)), &k); err != nil {
		return nil, false, coreerrors.Wrap(err, coreerrors.KindInternal,
			CodeIdempotencyStoreFailed, "idempotency kaydı çözülemedi")
	}

	return &corehttp.IdempotentResponse{
		Status:      k.Status,
		Header:      k.Header,
		Body:        k.Body,
		Fingerprint: k.Fingerprint,
	}, true, nil
}

// Complete işlemi biten anahtarın yanıtını kaydeder.
//
// Ayırmanın hâlâ duruyor olması ARANMAZ, kayıt koşulsuz yazılır: handler
// çalışmış ve yan etkileri gerçekleşmiştir, o yüzden kaydın var olması
// ayırmanın (örn. TTL dolduğu için) kaybolmuş olmasından daha önemlidir.
//
// TTL kaydın yazıldığı andan itibaren yeniden başlar. İstemcinin tekrar deneme
// penceresi, ayırmanın değil YANITIN doğduğu andan sayılmalıdır; aksi hâlde
// uzun süren bir handler, istemciye kalan saklama süresini kendi çalışma
// süresi kadar kısaltırdı.
func (s *IdempotencyStore) Complete(
	ctx context.Context, key string, resp corehttp.IdempotentResponse,
) error {
	ham, err := json.Marshal(kayit{
		Status:      resp.Status,
		Header:      resp.Header,
		Body:        resp.Body,
		Fingerprint: resp.Fingerprint,
	})
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInternal,
			CodeIdempotencyStoreFailed, "idempotency kaydı JSON'a çevrilemedi")
	}

	if err := s.client.Set(ctx, s.onek+key, imTamam+string(ham), s.ttl).Err(); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable,
			CodeIdempotencyStoreFailed, "idempotency kaydı yazılamadı")
	}

	return nil
}

// Abort ayırmayı geri alır; anahtar yeniden denenebilir hâle gelir.
//
// Yalnızca [imIslemde] imli değer silinir (bkz. [abortBetigi]).
//
// İki ARDIŞIK ayırma birbirinden ayırt edilmez: A anahtarı ayırdıktan sonra
// TTL dolar, B aynı anahtarı yeniden ayırır ve ardından A'nın Abort'u B'nin
// ayırmasını siler. Bunu kapatmak, ayırma başına rastgele bir jeton üretip
// Abort'un onu geri vermesini gerektirirdi; corehttp.IdempotencyStore'un
// Abort(ctx, key) imzasında böyle bir jetona yer yok ve arayüzü yalnızca bu
// uygulama için genişletmek soyutlamayı Redis'e bağlardı. Pratikte de
// gereksizdir: durumun oluşması için TTL'in (varsayılan 24 saat) TEK BİR
// istek daha sürerken dolması gerekir.
func (s *IdempotencyStore) Abort(ctx context.Context, key string) error {
	if err := abortBetigi.Run(ctx, s.client,
		[]string{s.onek + key},
		imIslemde,
	).Err(); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable,
			CodeIdempotencyStoreFailed, "idempotency ayırması geri alınamadı")
	}

	return nil
}
