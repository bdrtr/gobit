# gobit — Mimari

Bu belge sistemin **neden** böyle kurulduğunu anlatır. Ne yaptığı için
[README](../README.md), tek tek kararlar için [`docs/adr/`](adr/), kapsam ve
fazlar için [uygulama planı](../go-commerce-framework-plan.md).

Çelişki hâlinde sıra: **ADR > plan > bu belge**.

---

## 1. Tek cümlede

gobit, tek binary olarak çalışan bir **modüler monolit**tir: modüller derleme
zamanında birbirinden habersizdir, bu yüzden herhangi biri ileride ayrı bir
servise çıkarılabilir.

"Modüler monolit" burada bir slogan değil, denetlenen bir kısıttır. İzolasyon
üç ayrı yerde zorlanır:

| Kısıt | Nerede zorlanır |
|---|---|
| `internal/core/**` modülleri import edemez | `.golangci.yml` depguard |
| Modüller birbirini import edemez | depguard + `internal/arch` (ikinci savunma hattı) |
| `internal/workflows/**` modülleri import edemez | `internal/arch` (ADR 0006) |
| `plugins/**` modülleri import edemez | `internal/arch` |
| Cross-module foreign key yok | `internal/arch` (migration dosyalarını tarar) |
| Para tam sayı minor unit | `internal/arch` |

Bir kural inceleme turunda yakalanabiliyorsa testte de yakalanabilir; testte
yakalanınca bir daha hiç yazılmaz.

---

## 2. Katmanlar

```
cmd/server            KOMPOZİSYON KÖKÜ — her şeyi bilen tek yer
  │
  ├── internal/core       çekirdek: modülleri TANIMAZ
  ├── internal/modules    commerce modülleri: birbirini TANIMAZ
  ├── internal/workflows  modüller arası saga'lar: modülleri TANIMAZ
  └── plugins             eklentiler: modülleri TANIMAZ
```

Dikkat çeken şey, "tanımaz"ların çokluğudur. Kimin kiminle konuşacağına dair
**her karar tek bir dosyada** (`cmd/server`) verilir; geri kalan her paket
yalnızca kendi ihtiyacını tarif eder.

Bunun bedeli, bağlantıların derleyici tarafından denetlenememesidir: bir
modülün yayımladığı imza ile tüketicinin beklediği imza ayrışırsa hata
çalışma anında görünür. Bedel bilinçli olarak kabul edildi ve iki şeyle
karşılandı: (1) uçtan uca testler üretim kablolamasını **birebir** kurar,
(2) her interop yüzeyinin gerçek bağımlılıklarla koşan bir entegrasyon testi
vardır.

---

## 3. Bir isteğin yaşam döngüsü

```
istek
 └─ RequestID          her isteğe izlenebilir kimlik
     └─ Telemetry      span aç (route deseni handler'dan SONRA bilinir)
         └─ RequestLogger
             └─ Recoverer          panik -> 500, bağlantı kopmaz
                 └─ Hız sınırı     /admin/v1 ve /store/v1 kapsamında
                     └─ Kimlik     giriş ucu MUAF
                         └─ Idempotency
                             └─ chi route eşleşmesi
                                 └─ handler -> service -> repository
```

Sıranın her halkası bir arıza senaryosuna cevaptır:

- **RequestID en başta**: logger ve recoverer isteği kimliğiyle raporlayabilsin.
- **Telemetry, Recoverer'ın ÜSTÜNDE**: handler panikleyip Recoverer 500 yazınca
  span o durumu görsün. Altında kalsaydı en çok bakılacak istek en eksik
  kaydedilen olurdu.
- **Hız sınırı, kimlikten ÖNCE**: parola deneyen saldırgan her denemede bcrypt
  ve veritabanı maliyetini ödetmesin.
- **Idempotency, kimlikten SONRA**: kayıt anahtarı çağıranın kimliğiyle
  birlikte tutulsun; iki farklı çağıranın aynı anahtarı çakışmasın.

Middleware'ler router **kurulurken** takılır — chi, route kaydından sonra
`r.Use` çağrılmasını panikle reddeder. Modüller ise route'larını **tam yolla**
düz bir router'a kaydeder (aynı öneki iki kez `Mount` etmek panik üretirdi),
yani chi'nin doğal kapsamlama aracı elden gider. `corehttp.Scoped` o boşluğu
doldurur: kapsam, router ağacında değil middleware'in kendi içinde kurulur.

Yığının **sırası tek bir yerde** yazılıdır (`corehttp.APIGuards`) ve uçtan uca
testler o yığının ta kendisini kurar. Testin kendi kopyası olsaydı üretimdeki
sıra değiştiğinde test eski sırayı doğrulayıp yeşil kalırdı.

### Hata → status eşlemesi

Servisler tipli hata döner (`core/errors.Kind`), HTTP katmanı eşler. Handler'lar
status kodu **seçmez**:

| Kind | Status | İstemciye mesaj |
|---|---|---|
| `NotFound` | 404 | evet |
| `Invalid` | 422 | evet |
| `Conflict` | 409 | evet |
| `Unauthorized` | 401 | evet |
| `Forbidden` | 403 | evet |
| `TooManyRequests` | 429 | evet |
| `Unavailable` | 503 | evet |
| `Internal` | 500 | **hayır** (loglanır) |

---

## 4. Bir modülün yaşam döngüsü

`module.Registry` üç aşamayı **tüm modüller için sırayla** yürütür:

```
1. Register(ctx, container)   tüm modüller  ─┐
2. Migrations()               tüm modüller   │  aşamalar arası bariyer
3. Routes(router)             tüm modüller  ─┘
```

Bariyer zorunludur: bir modülün handler'ı başka modülün servisini güvenle
çözebilsin diye tüm `Register`'lar bitmeden `Routes`'a geçilmez. Buna karşılık
`Register` **içinde** başka bir modülün servisi çözülemez — o anda henüz
kayıtlı olmayabilir. Gerekiyorsa tembel bir yapıcı verilir ve çözüm ilk
kullanımda yapılır.

Her modül kendi migration klasörüne ve kendi versiyon tablosuna sahiptir
(`x-migrations-table`), yani bir modülün şema geçmişi diğerlerinden bağımsız
ilerler.

---

## 5. Modüller arası iletişim

Go'da interface'ler paketlerde yaşar; sağlayıcının interface'ini import etmek
izolasyonu kırardı. Bu yüzden **arayüz tüketicinin kendi paketinde** tanımlanır
ve somut servis container'dan **adla** çözülür ([ADR 0001](adr/0001-modul-arasi-iletisim.md)):

```go
// product modülünde, auth import EDİLMEDEN:
type SalesChannelReader interface {
    ActiveSalesChannelIDs(ctx context.Context) ([]string, error)
}
channels, err := container.Resolve[SalesChannelReader](c, "auth.service")
```

Yayımlanan yüzeyler bilinçli olarak **dardır ve ilkel tiplerle** konuşur: her
metot, sağlayıcının bir daha değiştiremeyeceği bir sözleşmedir. Zengin veri
gerekiyorsa doğru yol yeni bir ilkel metot değil, Query katmanıdır.

Container'daki ad sözlüğü:

| Ad | İçerik |
|---|---|
| `<modül>.service` | modüller arası ilkel çağrı yüzeyi |
| `<modül>.interop` | saga/çekirdek için dar yüzey |
| `<entity>.query` | Query katmanına açılan okuma sağlayıcısı ([ADR 0004](adr/0004-query-veri-erisimi.md)) |
| `<modül>.providers` | sağlayıcı kaydı (payment, fulfillment) |
| `core.*` | altyapı: db, redis, eventbus, workflow, link, query |

---

## 6. Veri

- **SQL-first**: `sqlc` + `pgx/v5`, modül başına ayrı codegen. ORM'in FK/graph
  modeli modül izolasyonuyla çelişirdi.
- **Cross-module foreign key YOK** (Prensip 2.2). İlişkiler `core/link` ile
  kurulur; kardinalite veritabanı kısıtıyla zorlanır ama tablolar arası FK
  kurulmaz ([ADR 0005](adr/0005-link-semasi-migration-disinda.md)).
- **Cross-module okuma** `core/query` ile yapılır: kök çek → link çöz → batch
  getir → birleştir. N+1 yapısal olarak imkânsızdır.
- **Para** tam sayı minor unit (kuruş/cent); float yoktur, para birimi ayrı
  alandır.
- **Zaman** UTC; `created_at/updated_at/deleted_at`, yumuşak silme
  `deleted_at` ile.

---

## 7. İş akışları (saga)

Modüller arası her çok adımlı işlem `core/workflow` üzerinde bir saga'dır:
ardışık yürütme, hata hâlinde **ters sırada** telafi, retry, idempotency
anahtarı ve panik izolasyonu. Yürütme durumu Postgres'e yazılır, yani
"aynı sepet iki kez tamamlanamaz" iddiası süreç içi bir haritanın değil kalıcı
bir kaydın davranışıdır.

`internal/workflows` de modülleri import etmez ([ADR 0006](adr/0006-workflow-modul-erisimi.md));
aynı dar arayüz + adla çözüm kuralı burada da geçerlidir.

**Çok depolu ayırma** sagaya sonradan eklendi ve seam'i iki modüle bölünmüştür:
stok "hangi depolarda yeterli adet var" olgusunu, fulfillment "hangisinden
gönderelim" kararını verir. Saga hiçbirini kendi vermez — sepet akışının depo
politikası hakkında söyleyecek bir sözü yoktur. Aday listesi kilitsiz okunduğu
için seçilen depo ayırma anında tükenmiş olabilir; o durumda sıradaki adaya
geçilir. Bu, adımı yeniden denemek DEĞİLDİR (o kapalıdır, çünkü `Reserve`'ün
tekrarı ikinci bir rezervasyon üretir) — başarısız bir çağrı hiçbir rezervasyon
bırakmamıştır.

**Pivot adımlar** ayrıca belgelenir. `capture_payment` bir pivottur: tahsilat
denendikten sonra geri alma yapılmaz, çünkü belirsiz bir tahsilatı "iptal
edildi" saymak parayı kaybetmenin en sessiz yoludur. Kalan risk ve mutabakat
ihtiyacı `internal/workflows/checkout/doc.go` içinde yazılıdır.

---

## 8. Kimlik ve sertleştirme

Ayrıntı için [README → API güvenliği](../README.md#api-güvenliği). Mimari
açıdan üç nokta önemlidir:

1. **Çekirdek kimliği NASIL doğruladığını bilmez.** `corehttp.Authenticator`
   tüketici tarafında (çekirdekte) tanımlıdır, auth modülü onu **yapısal
   olarak** karşılar ve container'dan adla çözülür. Çekirdek auth'u import
   etmez.
2. **Koruma modülde değil kompozisyon kökünde takılır.** Modül hangi uçlarının
   korunması gerektiğini godoc'unda bildirir; bağlamayı router'ı kuran taraf
   yapar.
3. **Kimlik ve yetki ayrı katmandır.** `RequireAdmin` "kimsin"i çözer,
   `RequireScope` "ne yapabilirsin"i zorlar. Yetki yükseltme ayrıca servis
   katmanında da kapatılır: çağıran, kendisinde olmayan bir yetkiyi veremez.
   Tek katman yeterli olmazdı — middleware haritası bir gün gevşetilebilir,
   servis kuralı ise verinin yanında durur.
4. **Arıza davranışı bileşene göre değişir** ([ADR 0007](adr/0007-sertlestirme-arizada-davranis.md)):
   kimlik fail-closed, hız sınırı fail-open, idempotency ayırmada reddeder ve
   kayıtta anahtarı serbest bırakır. Tek tip kural yoktur çünkü "bu bileşen
   olmadan ne bozulur" sorusunun cevabı her satırda farklıdır.

Yetki sözlüğü tek kuraldan türer — `<modül>:read` okuma, `<modül>:write` yazma,
`admin` hepsini kapsar — ve her modülün `api` paketi kendi sabitlerini yayımlar.
Zorlamayı eklemeyi unutan bir modül sessiz kalamaz: `internal/e2e/yetki_test.go`
router ağacını **gezip** her `/admin/v1` ucuna yetkisiz bir jetonla gider ve 403
bekler. Elle yazılmış bir uç listesi, listeye eklenmesi unutulan ilk uçta kör
kalırdı — ve unutulacak uç, tam da yeni yazılmış olandır.

Kimlik doğrulayıcı router'dan **sonra** doğduğu için
`corehttp.DeferredAuthenticator` ile bağlanır; bağlanmadan gelen istek
reddedilir.

---

## 9. Genişletme noktaları

### Yeni modül

1. `internal/modules/<ad>/` altında `module.go`, `models`, `repository`,
   `service`, `api`, `migrations`, `queries`, `sqlc.yaml`.
2. `.golangci.yml` içine `modul-izolasyonu-<ad>` depguard bloğu ekle **ve**
   diğer modüllerin bloklarına yeni modülü ekle.
3. `cmd/server` ve `internal/e2e` içindeki modül listesine ekle — ikisi aynı
   sırayı taşır.

### Yeni alan olayı

Modülün `service` paketinde olay adını ve yük anahtarlarını **sabit** olarak
yayımla; ad, Redis backend'inde aynı zamanda stream adıdır ve değişmesi tüm
abonelerin sessizce olay almayı bırakması demektir. Yük dar tutulur ve tüm
değerler dize olur (gerekçe: `order/service/events.go`).

Yayım hatasının yazmayı düşürüp düşürmeyeceği modüle göre değişir: siparişte
saga içindedir, katalogda commit'ten sonradır ve hata dönmek çağırana
"uygulanmadı" demek olurdu.

### Yeni eklenti

`plugins/<ad>/` altında `coreplugin.Plugin` uygulayan bir paket; sözleşme
`core/provider`'dan, kayıt noktası `coreplugin.Host`'tan alınır. Kurulum
dosyasına (`cmd/server/kurulum.go`) katalog satırı eklenir, `PLUGINS` ile
seçilir. Çekirdek ve modüller **değişmez**.

Kurulum iki fazlıdır: `Install` modüllerden önce (eklentinin getirdiği modül de
yaşam döngüsünden geçsin), `Start` modüllerden sonra (sağlayıcı kaydı ancak
hedef modül ayağa kalkınca vardır).

Go'nun standart `plugin` paketi (.so) bilinçli olarak kullanılmadı: yalnızca
Linux/macOS'ta çalışır, çapraz derlemeyi desteklemez ve eklenti ile ana ikilinin
**tüm** bağımlılıklarının bit düzeyinde aynı sürümde derlenmiş olmasını şart
koşar. Bu kısıtlar "çalışırken tak" vaadini pratikte "her eklenti için tüm
uygulamayı yeniden derle"ye çevirir — yani derleme zamanı kaydının zaten
sağladığı şeye, üstüne kırılganlık ekleyerek.

### Yeni modülün yetki sözlüğü

Modülün `api` paketi `ScopeRead = "<modül>:read"` ve
`ScopeWrite = "<modül>:write"` sabitlerini yayımlar; `Routes` içinde okuma ve
yazma alt router'ları `corehttp.RequireScope` ile kurulur. Unutmak sessiz
değildir: `internal/e2e/yetki_test.go` router ağacını gezip her `/admin/v1`
ucuna yetkisiz bir jetonla gider.

### Yeni sağlayıcı (payment/fulfillment)

`core/provider` sözleşmesini uygula ve `<modül>.providers` kaydına ekle.
Aynı kimlikle ikinci bir kayıt reddedilir ve mevcut sağlayıcı korunur:
sessizce üzerine yazmak, hangi sağlayıcının çalıştığını yükleme sırasına
bırakırdı — ödemede bunun bedeli paranın beklenmedik bir kuruluşa gitmesidir.

---

## 10. Bilinen sınırlar

| Sınır | Etki | Çıkış yolu |
|---|---|---|
| Modüller arası imzalar derleme zamanında denetlenmez | Ayrışma çalışma anında görünür | Her interop yüzeyi için entegrasyon testi (mevcut kural) |
| Oturum iptali yalnızca **toptan** | Tek cihazı düşürmek yok | jti bazlı kara liste — her istekte okunan yeni bir depo demektir |
| Yük testi süreç içi | Kapasite planı üretmez | Gerçek dağıtımda dış yük aracı |
| Satış kanalı bağı kuruluyor ama okunmuyor | Katalog her publishable anahtar için aynı | `product↔sales_channel` bağını bildir ve store listesini `Principal.SalesChannelIDs` ile süz |
| Migration geri alma yüzeyi yok | `.down.sql` dosyaları çağrılamıyor | `cmd/server`'a migrate alt komutu |
| Yetki sözlüğü modül başına iki girdi | Kaynak bazlı ayrım yok (örn. yalnızca varyant okuma) | Ayrım gerçekten gerektiğinde eklenir; şimdiden eklemek yanlış bir kesinlik hissi verirdi |

Çok örneklilik artık bir sınır değil bir **ayardır**: `GUARD_BACKEND=redis` hız
sınırını ve idempotency deposunu paylaşılan hâle getirir (bkz.
`internal/core/http/redisguard`). Varsayılan `memory` bilinçlidir — tek örnekli
geliştirme kurulumu Redis istememelidir — ama paylaşılan bir ortamda açılışta
uyarı üretir.
