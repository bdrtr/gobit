# gobit

**Türkçe** · [English](./README.en.md)

Go ile yazılmış, modüler, headless commerce **kütüphanesi**. Kopyalanıp
değiştirilen bir şablon değildir: gömen proje gobit'i `go.mod`'da bir bağımlılık
olarak izler, kendi modülünü ve eklentisini ekler, kurulumu yayımlanmış cepheden
kurar (ADR 0025). Çalışırken **tek süreçtir** ve içinde bir **modüler
monolit** taşır — modüller derleme zamanında birbirini tanımaz, kimin kiminle
konuştuğu kararı tek bir pakette (`internal/app`) verilir, ve izolasyon
korunduğu için herhangi bir modül ileride ayrı bir servise çıkarılabilir.

Gömen programın gördüğü yüzeyin tamamı budur:

```go
gobit.New().Version(version).Add(myModule).Use(myPlugin).Main(os.Args[1:], os.Stdout)
```

Mimarinin **neden** böyle kurulduğu: [`docs/mimari.md`](./docs/mimari.md).

## Hızlı başlangıç

```bash
make up      # Postgres 16 + Redis 7 (sağlıklı olana kadar bekler)
make run     # sunucuyu :9000'de başlatır
curl -s localhost:9000/health
# {"status":"ok","version":"v0.8.0"}
curl -s localhost:9000/ready
# {"status":"ok","version":"v0.8.0","checks":{"postgres":{"status":"ok"}}}
```

`/health` yalnızca sürecin canlı olduğunu bildirir; `/ready` bağımlılıkları
sınar ama hepsine aynı oyu vermez — Postgres trafiği keser, Redis
derecelendirir. İkisinin farkı, `degraded`/`unavailable` tablosu ve
`READINESS_DEGRADED_TIMEOUT` bütçesinin neden kısa olmak zorunda olduğu
[`docs/operating.md`](./docs/operating.md) içindedir.

Tüm hedefler için `make help`.

## Gereksinimler

| Araç | Sürüm | Ne için |
|---|---|---|
| Go | 1.26+ | derleme ve testler |
| Docker + Compose | v2+ | Postgres, Redis, izleme toplayıcısı, istemci üreteci |
| make | GNU Make | tüm hedefler |
| curl + jq | — | belgelerdeki örnekler |

`curl` ve `jq` uygulamanın değil **belgelerin** bağımlılığıdır: belgelerdeki
kabuk örnekleri ikisini de kullanır (`jq` olmadan
`TOKEN=$(… | jq -r .data.token)` satırı boş bir jeton üretir ve sıradaki istek
`401` alır).

`make tools`, sabitlenmiş sürümlerle `golangci-lint` ve `sqlc`'yi `./bin` altına
kurar.

## Yapılandırma

Tüm ayarlar ortam değişkeninden okunur (12-factor) ve varsayılanlar
`deploy/docker-compose.yml` ile uyumludur, bu yüzden yerelde `.env` gerekmez.
`cp .env.example .env` ile özelleştirilir.

**Ayarların yazılı kaydı `.env.example`'dır.** O dosya ile
`internal/core/config/config.go` ayrışamaz: bir test `Config`'i yansımayla gezer
ve her `env` etiketinin belgede yazdığını, oradaki değerin de `envDefault` ile
aynı olduğunu doğrular; ters yönde de karşılığı olmayan bir değişken bırakılamaz.

Elle ayarlanması gereken avuç dolusu şunlardır:

| Değişken | Ne zaman |
|---|---|
| `DATABASE_URL` | `APP_ENV=production` iken **zorunlu** — ezilmemişse uygulama açılışta durur |
| `REDIS_URL` | aynı kural |
| `JWT_SECRET` | verilmezse kimlik katmanı **her isteği reddeder** (ADR 0007) |
| `APP_ENV` | `development` dışındaki her değer paylaşılan ortam sayılır ve uyarıları açar |
| `EVENT_BUS` · `GUARD_BACKEND` | birden çok örnek çalıştırıyorsanız ikisi de `redis` olmalıdır |
| `PLUGINS` | kurulacak eklentilerin adları, örneğin `PLUGINS=search-pg` |

Öncelik, üretim koruması, `.env`'in kabuk semantiği ve kalanların tamamı
[`docs/operating.md`](./docs/operating.md) içindedir.

## Dizin yapısı

```
gobit.go              # YAYIMLANMIŞ cephe: New().Version().Add().Use().Main()
core                  # YAYIMLANMIŞ sözleşmeler — on beş paket (ADR 0026):
                      # errors, db, container, module, eventbus (+outbox), link,
                      # query, provider, plugin, http (+redisguard), audit,
                      # errorreport, personaldata
internal/app          # KOMPOZİSYON KÖKÜ (ADR 0027): config -> logger ->
                      # container -> router -> dinle; operatör alt komutları
                      # (migrate, stuck, recover, jobs, deadletters, seed)
cmd/server            # ikili: gobit'i çalıştırabilen en küçük program — ve
                      # kopyalanacak örnek
internal/core         # yayımlanmayan çekirdek: config, logger, job, workflow,
                      # observability, openapi, page
internal/modules      # on yedi izole commerce modülü (product, pricing,
                      # inventory, cart, order, payment, …)
internal/workflows    # modüller arası saga'lar (cart, checkout, invoicing,
                      # fulfilling, returns, datasubject)
internal/adminui      # yönetim paneli: dördüncü ağaç (ADR 0011)
plugins               # ağaç içi eklentiler (search-pg, error-sentry, file-s3, …)
examples/plugin       # AYRI modül: yayımlanmış yüzeyin dışarıdan derlenen kanıtı
examples/starter      # AYRI modül: gobit'i import eden, DERLENİP ÇALIŞTIRILAN
                      # örnek uygulama
migrations            # global (çekirdek) migration'lar
deploy                # docker-compose, Dockerfile
```

## Zorlanan mimari kurallar

İzolasyon derleme öncesinde `.golangci.yml` içindeki `depguard` ile denetlenir:
`core/**` ve `internal/core/**` modülleri import edemez (planın Prensip 2.4'ü),
hiçbir modül başka bir modülü import edemez (Prensip 2.1 / 2.4 — on yedi modül ×
on altı yasak = tam izolasyon), ve modüller arası erişim container'dan çözülen
dar bir interface üzerinden yapılır. Yeni
modül eklerken `depguard.rules` listesi de güncellenir; liste **elle** tutulur
ama unutulursa kural denetimsiz kalmaz — `TestModulesDoNotImportEachOther`
modül ağacını gezip gerçek import grafiğine bakar ve o listeden haberi yoktur.

`internal/arch` altındaki testler ise **davranışsal** değişmezleri zorlar.
Hepsinin ortak kuralı şudur: **yapıyı gezerler, ad listesi tutmazlar.** Liste
tutan bir test kuralı yalnızca *bugün* için uygular — yarın eklenen vaka
sessizce dışarıda kalır.

| Değişmez | Nerede zorlanır | Yaşanmış arıza |
|---|---|---|
| Modüller birbirini import etmez | `TestModulesDoNotImportEachOther` | — (depguard ile birlikte ikinci savunma hattı) |
| Yayımlanan yüzey bilerek seçilir | `TestThePublishedPackagesAreTheDeclaredOnes` | Dizin açmak kalıcı bir kamu taahhüdüne dönüşürdü |
| Yayımlanan hiçbir paket `internal/` import etmez | `TestNoPublishedPackageImportsAnInternalOne` | Dışarıdan derlenemeyen bir "yayımlanmış" paket |
| Yüzey ağaç dışından gerçekten çalışır | `TestTheOutOfTreeStarterRuns` | Derlenen ama koşmayan bir örnek, çalıştığını kanıtlamaz |
| Her modül kompozisyon kökünde kayıtlı | `TestEveryModuleIsRegisteredInTheCompositionRoot` | Faz 8/9'un tamamı yazılmıştı, testleri yeşildi, ve `/admin/v1/**` uçlarının **hiçbiri mount edilmemişti** |
| Kayıtlı her modül e2e zemininde de kurulu | `TestEveryRegisteredModuleIsSetUpInTheE2EHarness` | Kayıt satırının derlenmesi ile modülün gerçekten çalışması aynı şey değil |
| Kaydedilen her `*.interop` çözülüyor | `TestTheInteropSurfacesHaveAConsumer` | Ölü sözleşme; `Host.AddModule` hiç çağrılmıyordu |
| Yayımlanan her olay konusunun abonesi var | `TestTheEventTopicsHaveASubscriber` | `order.placed` uzun süre abonesizdi ve olay hiçbir şey yapmıyordu |
| Bildirilen her bağ **okunuyor** | `TestTheLinkDefinitionsAreTraversed` | Satış kanalı bağı yazılıyor, hiç okunmuyordu; test ilk koşuşunda **dört ölü bağ** buldu |
| Her `env` etiketi `.env.example`'da ve varsayılanı aynı | `TestTheEnvExampleAgreesWithTheConfigDefaults` | `.env.example` "aşağıdaki **iki** sınır" diyordu, yedi taneydi |
| Belgede karşılığı olmayan değişken yok | `TestNoVariableInTheEnvExampleIsOrphaned` | Silinen ayarın belgede kalması, operatöre çalışmayan bir kol vaat eder |
| Belgelerdeki eklenti adları kayıtlı adlar | `TestThePluginNamesInTheDocsAreReal` | Eklentiyi dizin adıyla çağıran bir örnek; kopyalayan kurulum açılışta "bilinmeyen eklenti" ile duruyordu |
| Hata gövdesi yalnızca `corehttp.WriteError`'dan | `TestErrorResponsesAreWrittenInOnePlace` | GraphQL sunucusu kuralı tekrar etmeye çalışıp ayrıştı; DSN+parola istemciye ulaştı, loglanmadı |
| Her GraphQL `Max*` sınırının çekirdekte karşılığı var | `TestTheGraphQLLimitDefaultsAgreeWithTheConfig` | Beş sertleştirme sınırının ortam değişkeni yoktu; operatör onları ayarlayamıyordu |
| `variant` okuyan her yol satış kanalı kararı verir | `TestVariantReadsGoThroughTheChannelDecision` | Kapsam okumada uygulanıyor, sepete eklemede uygulanmıyordu: B kanalının anahtarıyla A kanalının varyantı satın alınabiliyordu |
| Belgelerdeki her yol ve simge çözülür | `TestTheReferencesInTheDocsResolve` | Bağımsız bir doğrulama bir ADR'de hem simgeyi hem yolu kırdı ve `internal/arch` yeşil kaldı |
| Her ADR göndermesi gerçek bir kaydı adlandırır | `TestTheADRReferencesResolve` | Numarası değişen bir kayda yapılan gönderme sessizce başka bir kararı gösterir |
| Ledger dışında Türkçe yok | `TestNoTurkishOutsideLedger` | Yalnız diyakritiğe bakan bir kural tek bir harf çevirisiyle yalan söyler (ADR 0012) |

Bu testlerin hepsi **mutasyonla doğrulanmıştır**: değişmez kasten bozulduğunda
düştükleri gösterilmiştir. Düşürülemeyen bir mimari testi, olmayan bir mimari
testinden daha kötüdür — güvence hissi verir, güvence vermez.

Bir değişmezden **muaf tutma** gerekiyorsa mekanizma koddadır ve gerekçe
zorunludur; ayrıca muafiyetler **bayatlarsa testi düşürür**: muaf tutulan şey
artık kuralı ihlal etmiyorsa satır silinmek zorundadır. Muafiyet borçtur, borç
ödendiğinde defterde kalmaz.

## Daha ileri okuma

| Belge | Neyi cevaplar |
|---|---|
| [`docs/adr/`](./docs/adr/) | Kararlar. Otuz dört kayıt; plan ile çelişirse **ADR geçerlidir** |
| [`go-commerce-framework-plan.md`](./go-commerce-framework-plan.md) | Uygulama planı: kapsam, prensip numaraları ve fazlar |
| [`docs/mimari.md`](./docs/mimari.md) | Mimarinin anlatısı: katmanlar, isteğin ve modülün yaşam döngüsü, veri, saga'lar, teknoloji seçimleri, çekirdek paketler |
| [`docs/gaps.md`](./docs/gaps.md) | Ölçülmüş envanter: neyin olduğu, neyin olmadığı, ve her yokluğun **boşluk mu karar mı** olduğu — sıralanmış bir yapılacaklar listesiyle birlikte |
| [`docs/security.md`](./docs/security.md) | Kimlik ve yetki: iki yüzey, katalogun satış kanalına göre süzülmesi, scope sözlüğü, curl ile uçtan uca yürüyüş, sertleştirme halkaları ve tek örnek/çok örnek ayrımı |
| [`docs/commerce-flows.md`](./docs/commerce-flows.md) | Sepetten siparişe: akışların HTTP sahibi kim, fiyata ve para birimine kim karar verir, hangi depodan gönderilir, ve B2B'de harcama limiti nerede kontrol edilir |
| [`docs/api-surfaces.md`](./docs/api-surfaces.md) | Üretilen OpenAPI belgesi ve GraphQL vitrin okuma yüzeyi; maliyeti istemci belirlerken sunucunun koyduğu sınırlar ve hata politikası |
| [`docs/extending.md`](./docs/extending.md) | Eklentiler, dosya yükleme sağlayıcısı ve alan olayları — yeni bir yetenek nasıl eklenir |
| [`docs/operating.md`](./docs/operating.md) | Çalıştırma ve geliştirme: `/health` ile `/ready`, yapılandırmanın tamamı, olay veri yolu arka uçları, izleme, make hedefleri, modül yolunu değiştirme ve sürüm geçmişi |
| [`docs/known-limits.md`](./docs/known-limits.md) | Bilinen sınırlar: yirmi bir madde, dört küme — kimlik ve yetki, satış kanalı kapsamı, kurulum ve işletim, değişmezlerin sınırı |
| [`docs/catalog-search-cost.md`](./docs/catalog-search-cost.md) | Katalog aramasının ölçülmüş maliyeti |
| [`CHANGELOG.md`](./CHANGELOG.md) | Sürüm sürüm ne değişti |

## Faz durumu ve sürüm

Yol haritasının **on fazının hepsi tamamlandı**: proje iskeleti (0), çekirdek
altyapı (1), Module Links ve Query (2), saga motoru (3), katalog (4), sepet (5),
ödeme ve sipariş tamamlama (6), fulfillment · promotion · tax (7), auth · admin
user · API key · RBAC (8), eklenti sistemi · observability · sertleştirme (9),
GraphQL vitrin yüzeyi ve B2B (10). Yol haritası bittikten sonra bulunanlar
sürümlerde izlenir.

Güncel sürüm **v0.8.0**. `0.x` boyunca **kırıcı değişiklikler minor sürümlerde
gelebilir**; yüzey `1.0.0` ile donar. Sürüm sürüm ne değiştiği
[`CHANGELOG.md`](./CHANGELOG.md) içinde, her sürümün neyi neden getirdiği
[`docs/operating.md`](./docs/operating.md) içindedir.
