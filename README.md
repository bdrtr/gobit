# gobit

Go tabanlı, modüler, headless commerce framework. Tek binary olarak çalışan bir
**modüler monolit**: modüller izole olduğu için herhangi biri ileride ayrı
servise çıkarılabilir.

Uygulama planının tamamı için: [`go-commerce-framework-plan.md`](./go-commerce-framework-plan.md)
Mimarinin **neden** böyle kurulduğu için: [`docs/mimari.md`](./docs/mimari.md)

**Mevcut durum: Faz 9 — Eklenti sistemi · Observability · Sertleştirme ✅**

## Hızlı başlangıç

```bash
make up      # Postgres 16 + Redis 7 (sağlıklı olana kadar bekler)
make run     # sunucuyu :9000'de başlatır
curl -s localhost:9000/health
# {"status":"ok","version":"v0.5.0"}
curl -s localhost:9000/ready
# {"status":"ok","version":"v0.5.0","checks":{"postgres":{"status":"ok"}}}
```

`/health` yalnızca sürecin canlı olduğunu bildirir (liveness) ve bağımlılıkları
sınamaz — geçici bir veritabanı kesintisi sürecin öldürülmesine yol açmamalıdır.
`/ready` bağımlılıkları sınar ve biri düşükse **503** döner (readiness).

`version` alanı derleme sırasında `git describe --tags --always --dirty`
çıktısından gömülür, yani yukarıdaki değer sizin çalışma ağacınızda farklı
olacaktır (çalışma ağacı kirliyse sonuna `-dirty` eklenir). `dev` yalnızca
ldflags'siz derlenen bir ikilinin — örneğin düz `go run ./cmd/server` — yanıtıdır;
`make run` ve `make build` sürümü her zaman gömer.

Tüm hedefler için `make help`.

## Gereksinimler

| Araç | Sürüm | Ne için |
|---|---|---|
| Go | 1.26+ | derleme ve testler |
| Docker + Compose | v2+ | Postgres, Redis, izleme toplayıcısı, istemci üreteci |
| make | GNU Make | tüm hedefler |
| curl + jq | — | bu belgedeki örnekler |

`curl` ve `jq` uygulamanın değil **belgenin** bağımlılığıdır: aşağıdaki her
örnek ikisini de kullanır (`jq` olmadan `TOKEN=$(… | jq -r .data.token)`
satırı boş bir jeton üretir ve sıradaki istek `401` alır).

`make tools`, sabitlenmiş sürümlerle `golangci-lint` ve `sqlc`'yi `./bin` altına kurar.

## Yapılandırma

Tüm ayarlar ortam değişkeninden okunur (12-factor). Varsayılanlar
`deploy/docker-compose.yml` ile uyumludur, bu yüzden yerelde `.env` gerekmez.

`cp .env.example .env` ile özelleştirebilirsiniz. Değişken listesi için
[`.env.example`](./.env.example) veya `internal/core/config/config.go`.

İkisi **ayrışamaz**: bir test `Config`'i yansımayla gezer ve `env` etiketli her
alanın `.env.example`'da yazdığını, oradaki değerin de `envDefault` ile aynı
olduğunu doğrular. Ters yön de denetlenir — belgede duran ama ne uygulamanın, ne
compose'un, ne de bir eklentinin okuduğu bir değişken bırakılamaz
(`internal/arch/yapilandirma_test.go`). Bilinçli tek ayrışma `LOG_FORMAT`'tır:
kodun varsayılanı `json`, bu dosyanınki `text`; gerekçesi testte yazılıdır ve
ayrışma ortadan kalkarsa test kaydın **silinmesini** ister.

Belgesi eskimiş bir ayar, yanlış bir varsayılan kadar zararlıdır ve daha
sessizdir: ikisi de operatörün ayarladığını sandığı şeyi ayarlamamasıyla
sonuçlanır, ama hiçbir test düşmez ve hiçbir log satırı düşmez — fark ancak
sınır aşıldığında, yani üretimde edilir.

> **Üretim koruması:** `DATABASE_URL` ve `REDIS_URL` varsayılanları yalnızca
> yerel geliştirme içindir. `APP_ENV=production` iken bu ikisi ezilmemişse
> uygulama **açılışta hata verip durur** — eksik secret enjeksiyonunun
> sabit-kodlu kimlik bilgisiyle sessizce üretime çıkmasını engeller.

> **`.env` biçimi:** Dosya POSIX kabuk semantiğiyle yüklenir. İçinde `$` geçen
> değerleri **tek tırnağa alın** (`REDIS_URL='redis://:pa$word@…'`), aksi hâlde
> kabuk onları genişletir.

> **Öncelik:** Komut satırından verilen değişken `.env`'i **ezer**
> (`PLUGINS=search-pg make run`), tersi değil — docker compose'un kuralıyla
> aynı yön. Bu belgedeki `DEĞİŞKEN=… make run` biçimindeki her örnek, siz
> `.env` oluşturmuş olsanız da yazdığını yapar. Ters öncelik **sessiz** bir
> arıza sınıfıydı: `.env.example`'daki boş `PLUGINS=` satırı komut satırındaki
> eklenti adını yutuyor, uygulama hatasız açılıyor ve eklentinin uçları
> yalnızca **404** dönüyordu.

### Güvenlik varsayılanları

- Compose portları `127.0.0.1`'e bağlanır; paylaşılan bir ağda (ofis/kafe WiFi)
  Postgres ve Redis dışarıdan erişilebilir olmaz.
- Redis `requirepass` ile açılır; parolasız bağlantı reddedilir.
- HTTP sunucusunda `ReadHeaderTimeout` yanında `ReadTimeout`, `WriteTimeout` ve
  `IdleTimeout` de tanımlıdır — gövdeyi bayt bayt akıtan Slowloris türevine karşı
  `ReadHeaderTimeout` tek başına yetmez.
- Kapanışta `ShutdownTimeout` dolarsa açık bağlantılar `Close` ile **zorla
  kapatılır**; `Shutdown` tek başına aktif bağlantıları koparmaz.

## Dizin yapısı

```
cmd/server            # giriş noktası: config -> logger -> container -> router -> dinle
internal/core         # çekirdek: config, errors, logger, db, container, module,
                      # eventbus, link, query, workflow, provider, http
internal/modules      # izole commerce modülleri (product, pricing, inventory, …)
internal/workflows    # modüller arası saga workflow'ları
plugins               # harici modül/provider eklentileri
migrations            # global (çekirdek) migration'lar
deploy                # docker-compose, Dockerfile
```

## Teknoloji seçimleri

| Alan | Seçim | Gerekçe |
|---|---|---|
| Router | `chi` | Hafif, `net/http` uyumlu, middleware dostu |
| DB erişimi | **`sqlc` + `pgx/v5`** | SQL-first ve modül başına codegen; ORM'in FK/graph modeli modül izolasyonuyla çelişiyor |
| Migration | **`golang-migrate`** | `Module.Migrations() fs.FS` ile birebir uyum, modül başına `x-migrations-table` |
| DI | **`samber/do` v2** | Kontrat isimli servis + generic resolve istiyor; lazy instantiation ve shutdown hook'ları hazır |
| Config | `caarlos0/env` | Yalnızca env okur; viper'ın dosya/uzak config yükü gereksiz |
| Log | `log/slog` (stdlib) | Yapısal, bağımlılıksız |
| GraphQL | **`99designs/gqlgen`** | Şema-önce: şema incelenebilir bir artefakt kalır ve üretilen tipli resolver'lar imza kaymasını derleme zamanında yakalar (sqlc ile aynı disiplin) |

sqlc, golang-migrate ve samber/do **Faz 1–4 arasında** devreye girer; Faz 0
yalnızca iskeleti kurar.

## Mimari kurallar lint ile zorlanır

Planın Bölüm 2'deki değişmez kuralları `.golangci.yml` içinde `depguard` ile
derleme öncesi denetlenir:

- **Prensip 2.4** — `internal/core/**` içinden `internal/modules/**` import edilemez.
- **Prensip 2.1 / 2.4** — Hiçbir modül başka bir modülü import edemez
  (15 modül × 14 yasak = tam izolasyon). Modüller arası erişim container'dan
  çözülen servis interface'i üzerinden yapılır.

Yeni modül eklerken `.golangci.yml` içindeki `depguard.rules` listesini de
güncelleyin. Liste **elle** tutulur ama güncellenmesi unutulursa kural
denetimsiz kalmaz: `internal/arch` içindeki
`TestModullerBirbiriniImportEtmez` modül ağacını gezip gerçek import
grafiğine bakar ve o listeden haberi yoktur.

### Mimari testler: yapıyı gezerler, liste tutmazlar

`depguard` import grafiğini tutar; `internal/arch` altındaki testler ise
**davranışsal** değişmezleri zorlar. Hepsinin ortak kuralı şudur: **yapıyı
gezerler, ad listesi tutmazlar.** Liste tutan bir test kuralı yalnızca *bugün*
için uygular — yarın eklenen vaka sessizce dışarıda kalır.

| Değişmez | Ne zorlar | Yaşanmış arıza |
|---|---|---|
| `TestHerModulBilesimKokundeKayitli` | `module.Module`'ü uygulayan her paket `cmd/server`'da kayıtlı | Faz 8/9'un tamamı yazılmıştı, testleri yeşildi, ve `/admin/v1/**` uçlarının **hiçbiri mount edilmemişti** |
| `TestKayitliHerModulE2EZemindeKurulu` | Kayıtlı her modül e2e zemininde de kurulu | Kayıt satırının derlenmesi ile modülün gerçekten çalışması aynı şey değil |
| `TestInteropYuzeylerininTuketicisiVar` | Kaydedilen her `*.interop` çözülüyor | Ölü sözleşme; `Host.AddModule` hiç çağrılmıyordu |
| `TestOlayKonularininAbonesiVar` | Yayımlanan her konunun abonesi var | `order.placed` uzun süre abonesizdi ve olay hiçbir şey yapmıyordu |
| `TestLinkTanimlariGeziliyor` | Bildirilen her bağ **okunuyor** | Satış kanalı bağı yazılıyor, hiç okunmuyordu. Bu test ilk koşuşunda **dört ölü bağ** buldu (bkz. CHANGELOG) |
| `TestOrtamOrnegiConfigVarsayilanlariylaUyusuyor` | Her `env` etiketi `.env.example`'da ve varsayılanı aynı | `.env.example` "aşağıdaki **iki** sınır" diyordu, yedi taneydi |
| `TestOrtamOrnegindeSahipsizDegiskenYok` | `.env.example`'da karşılığı olmayan değişken yok | Silinen ayarın belgede kalması, operatöre çalışmayan bir kol vaat eder |
| `TestBelgelerdekiEklentiAdlariGercek` | Belgelerdeki eklenti adları kayıtlı adlar | README, eklentiyi dizin adıyla (tireli kayıt adı yerine) çağıran bir komut örneği veriyordu; kopyalayan kurulum açılışta "bilinmeyen eklenti" ile duruyordu |
| `TestHataYanitlariTekYerdenYazilir` | Hata gövdesi yalnızca `corehttp.WriteError`'dan | GraphQL sunucusu kuralı tekrar etmeye çalışıp ayrıştı; DSN+parola istemciye ulaştı, loglanmadı |
| `TestGraphQLSinirVarsayilanlariConfigleUyusuyor` | `graph.Options`'ın her `Max*` alanının çekirdekte karşılığı var | Beş sertleştirme sınırının ortam değişkeni yoktu; operatör onları ayarlayamıyordu |
| `TestVaryantOkumalariKanalKararindanGecer` | Query'den `variant` okuyan her fonksiyon satış kanalı kapsamı hakkında **görünür bir karar** verir | Kapsam okuma yüzeyinde uygulanıyor, sepete ekleme yolunda uygulanmıyordu: B kanalının anahtarıyla A kanalının varyantı satın alınabiliyordu |

Bu testlerin hepsi **mutasyonla doğrulanmıştır**: değişmez kasten bozulduğunda
düştükleri gösterilmiştir. Düşürülemeyen bir mimari testi, olmayan bir mimari
testinden daha kötüdür — güvence hissi verir, güvence vermez.

Bir değişmezden **muaf tutma** gerekiyorsa mekanizma koddadır ve gerekçe
ZORUNLUDUR; ayrıca muafiyetler **bayatlarsa testi düşürür**: muaf tutulan şey
artık kuralı ihlal etmiyorsa satır silinmek zorundadır. Muafiyet borçtur, borç
ödendiğinde defterde kalmaz.

## Çekirdek paketler

| Paket | Sorumluluk |
|---|---|
| `core/config` | env tabanlı 12-factor config + doğrulama, üretim koruması |
| `core/logger` | slog JSON/text handler |
| `core/errors` | Tipli hatalar (`Kind`), stdlib `errors` yardımcılarını yeniden dışa verir |
| `core/db` | pgxpool havuzu + modül başına ayrı versiyon tablolu migration runner |
| `core/container` | İsimli kayıt, generic `Resolve[T]`, tembel singleton, döngü tespiti, ters sırada kapatma |
| `core/module` | `module.Module` sözleşmesi + `module.Registry` (register → migrate → routes) |
| `core/eventbus` | `EventBus` + InMemory (dev) ve Redis Streams (prod, consumer group + XACK) |
| `core/http` | chi router, RequestID/RequestLogger/Recoverer/Telemetry, RequireAdmin/RequireStore/RequireScope, `Scoped`/`APIGuards` koruma yığını, hız sınırı, idempotency, `Kind`→status eşlemesi |
| `core/link` | Module Links — modüller arası ilişki FK olmadan; kardinalite veritabanı kısıtıyla zorlanır |
| `core/query` | Cross-module okuma — kök çek, link çöz, batch getir, birleştir; N+1 yapısal olarak yok |
| `core/workflow` | Saga motoru — ters sırada telafi, retry, idempotency-key, panik izolasyonu |
| `core/workflow/pgstore` | Yürütme durumunun Postgres deposu (`workflow_executions`) |
| `workflows/cart` | Sepet akışları: create_cart, add_line_item, update_line_item, calculate_totals. `cmd/server` `workflows.cart.interop` adıyla kaydeder, `cart` modülünün vitrin uçları o adla çözer |
| `workflows/checkout` | `complete_cart` saga: stok ayır → sipariş → yetkilendir → tahsil et → sepeti kapat. `workflows.checkout.interop` adıyla kaydedilir, `POST /store/v1/carts/{id}/complete` onu çağırır |
| `core/provider` | Ödeme/kargo sağlayıcı sözleşmeleri (plan Bölüm 5.6) |
| `core/plugin` | Eklenti sözleşmesi + iki fazlı kurulum (`Install` → modüller → `Start`) |
| `core/observability` | OpenTelemetry trace + metrik kurulumu; toplayıcı yoksa gerçekten kapalı |
| `core/openapi` | Router ağacından OpenAPI şeması üretimi (`/openapi.json`) |

Event bus arka ucu `EVENT_BUS=inmemory|redis` ile seçilir. `redis` seçildiğinde
Redis erişilemezse uygulama açılışta durur.

`inmemory` **kalıcı değildir**: teslim asenkrondur ve süreç çökerse ya da
kapanış `SHUTDOWN_TIMEOUT` içinde bitmezse teslim edilmemiş olaylar iz
bırakmadan kaybolur — sipariş konmuş, onay bildirimi hiç gitmemiş olur.
Paylaşılan bir ortamda (`APP_ENV != development`) bu risk açılışta **uyarı**
üretir; açılış durmaz, çünkü tek örnekli bir staging kurulumunda `inmemory`
hâlâ meşrudur. Ödünç `GUARD_BACKEND=memory` ile aynıdır.

`redis` seçildiğinde olayların ad alanını `REDIS_KEY_PREFIX` belirler: stream
anahtarı `<önek>:events:<olay adı>`, consumer group ise `<önek>`. **İkisinin de
ayrılması şarttır.** Yalnızca stream ayrılsaydı iki kurulum aynı gruba bağlanır
ve consumer group'un tanımı gereği bir olayı ikisinden yalnızca **biri** alırdı
— üretimin `order.placed` olayı staging tarafından tüketilip yutulabilirdi.

`EVENT_BUS_CONSUMER` ters yönde çalışır: kurulumları değil, **aynı gruptaki
süreçleri** ayırır. Boş bırakılırsa `<hostname>-<pid>` kullanılır. Aynı adı iki
örneğe vermek çift işleme yol açar (her ikisi de açılışta o adın bekleyen
listesini okur, yani ötekinin hâlâ işlemekte olduğu mesajları da alır) ve
doğrulama bunu göremez — tek süreç, ötekini bilmez. Bu yüzden çözülen ad
açılışta loglanır; çakışma ancak iki açılış logu yan yana konduğunda görülür.

## API güvenliği

İki yüzey, iki kimlik. Koruma modüllerde değil, **router'ı kuran tarafta**
(`cmd/server`) takılır: modüller route'larını tam yolla düz bir router'a
kaydeder, kapsamlama `corehttp.Scoped` ile yapılır ve sıra tek bir yerde,
`corehttp.APIGuards` içinde yazılıdır.

| Yüzey | Kimlik | Hız sınırı | Başlık |
|---|---|---|---|
| `/admin/v1/**` | Oturum jetonu (HS256 JWT) **ya da** gizli anahtar (`sk_…`) | var | `Authorization: Bearer …` |
| `/store/v1/**` | Publishable anahtar (`pk_…`) | var | `x-publishable-api-key: …` |
| `/files/**`, `/openapi.json` | yok | **var** | — |
| `/health`, `/ready` | yok | yok | — |

**Kimlik ve kota AYRI kararlardır.** Dosya sunumu ve şema ucu kimliksizdir
çünkü istemcileri başlık gönderemez (vitrindeki `<img>`, bir kod üreteci) —
ama bedava değildir: biri disk ve veritabanı okur, öteki route ağacını gezer.
Sağlık uçları ise ikisinin de dışındadır; onları çağıran orkestratördür ve
kotaya takılan bir sağlık ucu, sağlıklı bir örneği trafikten çektirir — yani
sınırın kendisi arızayı üretir. Kapsam `internal/smoke`'ta gerçek süreçte
sabitlenmiştir (`TestKotaKapsamiGercekSurecte`), çünkü eksik bir önek hiçbir
şeyi düşürmez: uç çalışmaya devam eder, yalnızca kotasız çalışır.

**Tek korumasız yönetim ucu** `POST /admin/v1/auth/login`'dir: kimliği
doğrulanacak istek, kimliği daha yeni kuracaktır. Muafiyet elle yazılmaz,
`authapi.LoginPath` sabitinden okunur.

Koruma yığınının sırası bilinçlidir:

1. **Hız sınırı** — kimlik doğrulamadan *önce*. Aksi hâlde parola deneyen bir
   saldırgan her denemede bcrypt + veritabanı maliyetini ödetir, kotası ancak
   ondan sonra düşerdi.
2. **Kimlik** — giriş ucu hariç tüm yönetim yüzeyi, anahtarsız tüm mağaza
   yüzeyi reddedilir. Tanımsız bir `/admin/v1/...` yolu da **401** döner (404
   olsaydı uç haritası status kodundan sızardı).
3. **Idempotency** — kimlikten *sonra*; kayıt anahtarı çağıranın kimliğiyle
   birlikte tutulur. Tek tek yollar bu halkadan (yalnızca bu halkadan) muaf
   tutulabilir; bugün tek muaf yol GraphQL vitrin ucudur — gerekçe
   "Sertleştirme" bölümünde.

Publishable anahtar bir **sır değildir**: tarayıcıda görünür ve tek işi isteği
bir satış kanalına bağlamaktır — yetki taşımaz.

### Katalog satış kanalına göre süzülür

`GET /store/v1/products` isteğin anahtarına bağlı kanalları `Principal`'dan
okur ve kataloğu ona göre süzer. Kural tek cümlelik:

> Kanal ataması **olmayan** ürün tüm kanallarda görünür; ataması **olan** ürün
> yalnızca atandığı kanallarda görünür.

Kanal **sorgu dizesinden alınmaz**, kimlikten gelir — alınsaydı süzgeç bir
yetkilendirme olmaktan çıkıp görüntüleme tercihine dönüşür, elindeki herhangi
bir publishable anahtarla gelen istemci başka bir vitrinin kataloğunu okurdu.
Tekil uç (`/store/v1/products/{id}`) de aynı süzgece tabidir ve gizlenen ürün,
hiç var olmayan ürünle **aynı** hata kodunu döner.

Bağ yönetimden kurulur:

```
POST   /admin/v1/products/{id}/sales-channels
DELETE /admin/v1/products/{id}/sales-channels/{sales_channel_id}
GET    /admin/v1/products/{id}/sales-channels
```

> **Dikkat:** Bir ürünü vitrinden kaldırmanın yolu son kanal bağını silmek
> **değildir** — kural gereği ataması kalmayan ürün *tüm* vitrinlerde görünür
> olur, yani tam tersi olur. Gizlemek için `status` alanını kullanın
> (`draft` / `archived`).

#### Kural SEPETE EKLEMEDE de uygulanır

Kapsam bir görüntüleme tercihi değil, bir **yetkilendirme** olduğu için okuma
yüzeyinde durması yetmez. `POST /store/v1/carts/{id}/line-items` varyantı
kataloğa sorarken isteğin kanallarını da taşır: kapsam dışı bir varyant için
katalog kayıt döndürmez ve satır **hiç yazılmaz**.

Kural ikinci kez yazılmaz — akış "bu varyant bu kanallarda görünür mü" diye
`product`'a sorar ve cevabı, vitrin listesinin kullandığı SQL şablonunun ta
kendisi üretir. Kanallar yine **kimlikten** gelir; nil / boş küme / dolu küme
ayrımı okuma yüzeyindekiyle birebir aynıdır.

Kapsam dışı varyant, hiç var olmayan varyantla **aynı** hatayı döner
(`404 cart_workflow_variant_unknown`) — farklı bir sınıf, ürünün varlığını ele
verirdi.

> **Sınır:** denetim varyantın sepete **girdiği** yerdedir. Adet güncelleme ve
> sepeti tamamlama yolları kapsamı yeniden sormaz; sepete varyant sokabilen tek
> yol satır eklemedir ve sepete girmiş bir satır, ürünü sonradan başka bir
> kanala taşıyan bir düzenleme yüzünden ödenemez hâle gelmez. Kararı
> `TestVaryantOkumalariKanalKararindanGecer` (bkz. `internal/arch`) korur: yeni
> bir varyant okuması ya kanal kararını verir ya da gerekçesini yazar.

Katı alternatif — "ataması olmayan ürün hiçbir kanalda görünmez" — daha doğru
olanıdır ve sektör alışkanlığıdır (yayımlama açık bir eylem olur). Uygulanmadı
çünkü açıldığı gün mevcut her kurulumun kataloğu bir anda boşalır; bir sürüm
sınırında duyurulması gerekir (bkz. [`CHANGELOG.md`](./CHANGELOG.md)). Gizli anahtar yetki taşır ve
satış kanalına bağlanmaz; ikisini karıştıran bir girdi sessizce düzeltilmez,
reddedilir.

### Yetki (scope)

Kimlik "kimsin", yetki "ne yapabilirsin" sorusudur; ikisi ayrı katmandır.
`RequireAdmin` yalnızca kimliği çözer, yetkiyi `RequireScope` uç uç zorlar:

Sözlük tek kuraldan türer:

| Uç | İstenen |
|---|---|
| `POST /admin/v1/auth/login` | — (kimlik daha yeni kurulacak) |
| `GET /admin/v1/auth/me`, `POST /admin/v1/auth/logout` | yalnızca kimlik |
| `/admin/v1/**` **okuma** (GET, HEAD) | `<modül>:read` |
| `/admin/v1/**` **yazma** (POST, PUT, PATCH, DELETE) | `<modül>:write` |
| `/store/v1/**` | — (publishable anahtar yetki taşımaz) |

`<modül>` uca sahip modülün adıdır: `product:read`, `order:write`,
`promotion:write` … `admin` **üst yetkidir** ve hepsini kapsar.

Tek istisna auth modülünün yazma uçlarıdır: orada `auth:write` yerine doğrudan
`admin` istenir. O uçlarda yazılan şeyin kendisi yetkidir (kullanıcının
yetkisi, anahtarın yetkisi, anahtarın göreceği kanal), yani yetki yazabilen
bir kimlik tek istekte kendini admin yapabilir — zaten admindir; ayrı bir ad
gerçekte var olmayan bir sınırı varmış gibi gösterirdi.

**Yetki yükseltme iki katmanda** engellenir: middleware ucu kapatır, servis ise
çağıranın *kendisinde olmayan* bir yetkiyi vermesini reddeder. İkinci katman
gereklidir çünkü ilkinin haritası bir gün gevşetilebilir.

Yetkisi **boş** (nil değil, boş dilim) bir kullanıcı giriş yapabilir ama
hiçbir korumalı uçta iş yapamaz. Bu bir kaza değil sözleşmedir ve router ağacı
gezilerek denetlenir: `internal/e2e/yetki_test.go` her `/admin/v1` ucuna
yetkisiz bir jetonla gidip **403** bekler, yani zorlamayı eklemeyi unutan bir
modül sessiz kalamaz.

```bash
# 0) İlk yönetici (yalnızca boş bir veritabanında)
ADMIN_BOOTSTRAP_EMAIL=admin@example.com \
ADMIN_BOOTSTRAP_PASSWORD='…' make run

# 1) Giriş -> jeton
TOKEN=$(curl -s localhost:9000/admin/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"admin@example.com","password":"…"}' | jq -r .data.token)

# 2) Korumalı uç
curl -s localhost:9000/admin/v1/auth/me -H "Authorization: Bearer $TOKEN"

# 3) Vitrinin anahtarı: önce satış kanalı, sonra publishable anahtar
SC=$(curl -s localhost:9000/admin/v1/sales-channels \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"name":"Varsayılan vitrin"}' | jq -r .data.id)

PK=$(curl -s localhost:9000/admin/v1/api-keys \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d "{\"title\":\"vitrin\",\"type\":\"publishable\",\"scopes\":[],\"sales_channel_ids\":[\"$SC\"]}" \
  | jq -r .data.key)

# 4) Mağaza yüzeyi
curl -s localhost:9000/store/v1/products -H "x-publishable-api-key: $PK"

# 5) Çıkış (çağıranın TÜM oturumlarını düşürür)
curl -s -X POST localhost:9000/admin/v1/auth/logout -H "Authorization: Bearer $TOKEN"
```

**Publishable anahtar bir satış kanalına bağlı doğar.** Sıra tersine
çevrilemez: anahtar üretme gövdesi `sales_channel_ids` ister ve **boş bir
listeyle de anahtar üretilir** — ama o anahtar mağaza yüzeyinde
`401 unauthenticated` alır (sunucu logunda `auth_no_sales_channel`), çünkü
kimliğin taşıdığı tek bilgi zaten kanaldır ve kanalsız bir publishable anahtar
hiçbir isteği bağlayamaz. Anahtar sonradan da bağlanabilir:
`POST /admin/v1/api-keys/{id}/sales-channels`.

Bu paragrafın üç cümlesi de gerçek ikilide çividir:
`internal/smoke/anahtar_test.go` içindeki
`TestKanalsizPublishableAnahtarVitrindeReddedilir` kanalsız anahtarı üretir
(201), mağaza yüzeyinde **401** alır, teşhis kodunu sunucunun **logunda**
arar ve kanalı sonradan bağlayıp **aynı** anahtarla girer.

Düz anahtar **yalnızca üretim yanıtında** döner; diğer tüm uçlar maskelenmiş
gösterimini (`pk_...WnjU`) verir. Kaybederseniz tek yol iptal edip yenisini
üretmektir. Yönetim tarafının anahtarı (`sk_…`) aynı uçtan `"type":"secret"`
ile çıkar ve o **bir sırdır**: yetki taşır, satış kanalına bağlanmaz.

**İlk yönetici** bir tohum adımıyla doğar: yönetim uçları korumalı olduğu için
ilk kullanıcıyı HTTP'den yaratmanın yolu yoktur. Tohum **yalnızca hiç kullanıcı
yokken** çalışır — yeniden başlatma güvenlidir ve var olan bir kurulumun
yetkilerini asla değiştirmez. İki değişken **birlikte** verilir; yalnızca biri
verilirse uygulama açılışta durur.

**İkisini birden boş bırakmak** kurulmuş bir sistem için meşrudur — kullanıcılar
zaten vardır — ama **taze bir veritabanında** sonuç yönetilemez bir kurulumdur:
hiç kullanıcı yoktur, yönetim yüzeyi giriş ucu dışında tamamen korumalıdır ve
ilk kullanıcıyı HTTP'den yaratmanın yolu yoktur; mağaza yüzeyi de kapalıdır,
çünkü publishable anahtarı da yönetim ucu üretir. Sunucu yine de açılır,
`/health` ve `/ready` yeşil döner. Bu yüzden sıfır kullanıcı + tohumsuz
yapılandırma paylaşılan ortamlarda **açılışı durdurur**, yerel geliştirmede ise
yalnızca uyarı üretir (`.env` olmadan `make up && make run` sözü orada
korunur). Ayrım `JWT_SECRET`'inkiyle aynıdır.

**Oturum iptali** iki yoldan olur ve ikisi de **toptan**dır: parola değişimi ve
`POST /admin/v1/auth/logout`. Tek bir cihazı düşürmek yoktur — bunun için jti
bazlı, her istekte okunan bir kara liste gerekirdi. API anahtarının oturumu
yoktur; o `POST /admin/v1/api-keys/{id}/revoke` ile kapatılır.

`JWT_SECRET` verilmezse geliştirmede **açılışa özel rastgele** bir sır
üretilir (yeniden başlatmada oturumlar düşer) ve uyarı loglanır; paylaşılan
ortamlarda config doğrulaması sırrı zorunlu kılar.

## Sepetten siparişe: akışları kim çağırıyor, fiyatı kim belirliyor

Sepetin uçları `cart` modülündedir ama sepetin **bölgesi** de **tutarı** da
orada değildir: bölge `region`'ın, fiyat `pricing`'in, başlık kataloğun, vergi
`tax`/`region`'ın, sipariş ise `order` + `payment` + `inventory`'nin verisidir.
Bu yüzden vitrinin **yazan uçlarının tamamı** işini kendi servisiyle değil,
modüller arası bir **akışla** yapar:

| Uç | Ne yapar | Akış |
|---|---|---|
| `POST /store/v1/carts` | `country_code`'dan bölgeyi ve para birimini **sunucu** türetir, müşteriyi doğrular, sepeti açar | `workflows/cart` create_cart |
| `POST /store/v1/carts/{id}/line-items` | Fiyatı ve başlığı **sunucu** belirler, satırı ekler, toplamları yeniler | `workflows/cart` add_line_item |
| `PATCH /store/v1/carts/{id}/line-items/{line_item_id}` | Adedi yazar ve satırı **yeniden fiyatlar**; sıfır adet satırı kaldırır (204) | `workflows/cart` update_line_item |
| `POST /store/v1/carts/{id}/complete` | Stok ayırır, siparişi açar, ödemeyi tahsil eder, sepeti kapatır | `workflows/checkout` complete_cart |

### Akışın HTTP sahibi modüldür

Modül somut akışı **tanımaz**: kendi paketinde dar bir arayüz tanımlar
(`api.CartOpening`, `api.LinePricing`, `api.CartCompletion`) ve somut tipi
container'dan `workflows.cart.interop` / `workflows.checkout.interop` adıyla
çözer (ADR 0001).
`cmd/server` yalnızca akışları **kurar ve kaydeder**; bileşim köküne handler
kodu girmez. Bu, `order` → `b2b` harcama kuralında zaten kullanılan kalıbın
aynısıdır.

Kayıt sırası **dairesel**dir ve daire iki yerden kırılır: akış tüm modüllerin
yüzeylerini çözdüğü için ancak `Bootstrap`'tan **sonra** kurulabilir, modülün
handler'ı ise `Register` sırasında kurulur ve akışa ihtiyaç duyar. Bu yüzden
modül tarafındaki çözüm **tembeldir** — ilk istekte yapılır ve sonucu
saklanır.

### Fiyat yetkisi sunucudadır ve yol KAPALI arızalanır

Satır ekleme gövdesi bir zamanlar `unit_price` alıyordu ve cart servisi onu
olduğu gibi yazıyordu; yalnızca aralığı denetleniyor, **doğruluğu**
denetlenmiyordu. Alanın godoc'u "nihai fiyatı `calculate_totals` yazar" diyordu
ama o akış hiçbir kurulumda kablolanmamıştı — yani istemcinin gönderdiği tutar
nihai tutardı ve vitrinin kimliği (publishable anahtar) tarayıcıda durduğu için
bu, **herkesin erişebildiği** bir uçtu. `title` de aynı sınıftaydı: satırın adı
kataloğun verisidir ve sepette, siparişte, faturada görünen odur.

İkisi de vitrin gövdesinden **kaldırıldı** (kırıcı; `0.x`). Gövde tanınmayan
alanı reddettiği için eski bir istemci sessizce eski davranışa dönmez, `422`
alır. Yönetim tarafında karşılığı yoktur: `cart`'ın `/admin/v1` yüzeyi tanımı
gereği yalnızca okumadır (sepeti değiştiren tek taraf müşteridir), dolayısıyla
"yönetici fiyat girebilsin" için açılacak bir uç da yoktur.

Fiyatlandırıcı çözülemezse satır **hiç eklenmez**. Bu, `b2b` harcama kuralının
bilinçli tersidir: `b2b` kurulu değilse "limit yok" doğru cevaptır, ama
fiyatlandırıcı yoksa "fiyat yok" satırı yazmak — istemcinin fiyatıyla ya da
sıfırla — sessizce bedava mal satmaktır.

Arıza **`500`** ile bildirilir, `404` ya da `422` ile değil. Ayrım kozmetik
değildir: container kayıtsız bir adı `not_found`, yanlış tipte bir kaydı
`invalid` sayar ve bu sınıflar olduğu gibi geçirilseydi uç istemciye "böyle bir
uç yok" ya da "gövden geçersiz" derdi — oysa arıza **sunucu
yapılandırmasındadır**. `5xx` uyarı zinciri o zaman hiç çalmaz ve ara katmanlar
`404`'ü önbelleğe alıp arızayı kurulum düzeldikten sonra da sürdürebilir.
Operatörün ihtiyacı olan metin (hangi ad çözülemedi) hatada korunur ama
istemciye sızmaz: `KindInternal` gövdeleri maskelenir, geriye yalnızca
`cart_module_setup_failed` kodu kalır.

### Sepetin bölgesi ve para birimi ÜLKEDEN türetilir

`POST /store/v1/carts` gövdesi bir zamanlar `currency_code`, sonra da
`region_id` alıyordu ve ikisinin de sınıfı `unit_price` ile **aynıydı**:
sunucunun verisi istemciden geliyordu.

Para birimi `region` şemasında bölge başına **tek bir sütundur**
(`region.currency_code`, `currency` tablosuna FK), yani bir bölgenin iki para
birimi olamaz — sepetin para birimi bir seçim değil bir **türetmedir**. Ayrışma
reddedilmiyordu da: `cart` servisi `region`'ı tanımadığı için (ADR 0006) kodun
yalnızca biçimini doğruluyor, bölgeninkiyle karşılaştırmıyordu. TRY bölgesinde
açılan bir sepete `EUR` yazan istemci, o sepeti gerçekten EUR olarak alıyordu.
Sonucu kozmetik değildi, çünkü para birimi **fiyat seçer**: satır akışı birim
fiyatı varyantın fiyat kümesinden "sepetin para biriminde" okur.

`region_id` bir tur daha kaldı ve iki sebeple düştü. Birincisi aynı sınıftadır:
bölge sepetin **vergi oranını** seçer. İkincisi daha temeldir — `region_id`
müşterinin ifade etmek istediği şey **değildir**. Müşteri bir **ülke** seçer
(ya da tarayıcısı söyler); bölge, o ülkenin sunucudaki karşılığıdır ve eşlemeyi
operatör kurar. İstemciye bir iç varlık kimliği yazdırmak, kapatılan sınıfın
daha yumuşak bir biçimiydi. Üstelik türetmeyi zaten yapan bir akış vardı —
`create_cart` ülke kodundan hem bölgeyi hem para birimini çözer — ve **vitrin
ucu onu atlıyordu**: aynı işlem için iki sözleşme, işletmecinin gördüğü yol da
ham olan.

Bugün gövde yalnızca `country_code` (zorunlu), `customer_id`, `email` ve
`metadata` alır. Kalıp fiyattakiyle aynı: `cart` akışı import etmez; kendi
paketinde dar bir arayüz tanımlar (`api.CartOpening`) ve somut tipi
container'dan `workflows.cart.interop` adıyla **tembel** çözer. Yol da aynı
şekilde **kapalı** arızalanır — akış çözülemezse sepet hiç açılmaz, çünkü bir
varsayılana düşmek (mağazanın ilk bölgesi ya da istemcinin dediği) kapatılan
kapıyı geri açardı.

Yan etkisi mimaridir: `cart` modülünün başka bir modülü adla çözdüğü **tek yer
de kapandı**. Para birimini okumak için tuttuğu `region.service` bağı ve
`api.RegionCurrencyReader` arayüzü kaldırıldı; bölgeyi bilen taraf artık
akıştır.

Hata yüzeyi de ülkeye taşındı: bölgesi olmayan geçerli bir ülke `404`
(operatör o ülkeye satış açmamıştır — istemci başka bir ülke seçebilir), biçimi
bozuk ya da boş bir kod `422`'dir. `metadata` gövdede **kaldı** ve akışa olduğu
gibi taşınır; o gerçekten istemcinin verisidir ve hiçbir hesaba girmez — aynı
karar satır metadata'sında da verilmişti.

Yönetim tarafında aynı alan **meşrudur** ve kaldırılmadı: `POST
/admin/v1/regions` gövdesindeki `currency_code` bölgeyi **tanımlar** — operatör
orada bir kopya değil aslı yazar ve kopyalanacak bir kaynak yoktur. Ölçüt "alan
gövdede mi" değil, "bu değer çağıranın kendi verisi mi" sorusudur. `cart`'ın
kendi `/admin/v1` yüzeyinde soru hiç doğmaz: orası yalnızca okur.

### Reddin sebebi vitrine ulaşır

Saga motoru patlayan adımı sararken hatanın **sınıfını** alt hatadan
devralıyor ama **kodunu** kendi sabitiyle (`workflow_step_failed`) eziyordu.
Gövdedeki tek makine okunur alan `error.code` olduğu için her saga hatası
istemci için tek bir değere düzleşiyordu: B2B harcama limitini aşan alışveriş
`409` alıyor, `spending_limit` yanıtın hiçbir yerinde geçmiyordu ve vitrin
"limitiniz yetmedi" ile "geçici çakışma, tekrar deneyin"i ayırt edemiyordu —
oysa `409` tam olarak tekrarın **çözmediği** sınıftır.

Kod artık korunur (`order_spending_limit_exceeded` gövdeye kadar gelir);
kodsuz bir adım hatası motorun kendi sabitini alır. Taşınan **yalnızca
koddur**: mesaj ve `Details` zincirde kalır ve `KindInternal` hatalarında yine
maskelenir. Değişikliğin sınırı da testle çizilidir — telafi patladığında
dıştaki kod `workflow_compensation_failed` olarak **kalır**, çünkü orada
okunması gereken şey adımın neden düştüğü değil, sistemin tutarsız kaldığıdır.

### Tamamlama gövdesindeki her alan bir yetki sorusudur

- `payment_provider_id` **var**: hangi sağlayıcıyla ödendiği müşterinin
  seçimidir. Ad sunucuda kayıtlı olmak zorundadır.
- `payment_data` **var**: sağlayıcıya olduğu gibi iletilen serbest veri.
- `expected_total` **var ve zorunlu**: müşteriye onaylatılan toplam. Hesap
  tamamlamanın başında yenilenir; ayrışma `409` üretir ve **hiçbir yan etki**
  uygulanmaz (kontrol saga'nın ilk adımından önce koşar). Opsiyonel olsaydı
  alanı unutan her istemci korumayı sessizce kapatırdı.
- `email` **yok**: sepetin iletişim adresi zaten sepettedir ve handler onu
  kendi servisinden okur; gövdeye açmak, siparişin sepette görünenden başka bir
  adrese bağlanmasına izin verirdi.
- `location_id` **yok**: hangi depodan çıkılacağı bir kargo kararıdır ve akış
  onu satır başına `inventory` + `fulfillment`'a sorarak verir. Müşteriye depo
  seçtirmek hem stok topolojisini sızdırır hem de siparişin nereden çıkacağını
  ona bırakırdı.

Yanıt siparişin kimliğini ve tahsil edilen tutarı taşır; ödeme oturumu,
koleksiyon ve rezervasyon kimlikleri ile operatöre ait uyarılar **yayımlanmaz**.

### Aynı ölçütün henüz uygulanmadığı yer

Kayda geçmemiş bir açık, kimsenin kapatmadığı açıktır. Araştırıldı, karar
verildi ve bilerek açık bırakıldı; gerekçesi kodun godoc'undadır.

- **Vitrin sepetlerinde sahiplik denetimi yok.** Model bir **yetenek URL**'idir:
  sepet kimliği 48 bit zaman damgası + 80 bit kriptografik rastgelelikten
  üretilir, tahmin edilemez ve onu bilmek erişim hakkını taşır. Zorunluluktan
  da doğar — mağaza yüzeyinin tek kimliği publishable anahtardır ve o bir sır
  değildir; ortada müşteri oturumu yoktur. Modelin kuralı da uygulanır: vitrin
  tarafında **liste ucu yoktur**, çünkü bir liste ucu tek bir kimliği bilmeyi
  tüm sepetleri okumaya çevirirdi. Modelin **kapsamadığı** şey gövdelerdeki
  `customer_id`'dir: yetenek "elimdeki kimliğe erişebilirim" der, "ben şu
  müşteriyim" demez — ve sepetin müşterisi, b2b harcama limitinin hangi şirket
  penceresinden düşüleceğini belirler. Tek doğru kapatma müşteri oturumudur ve
  o **henüz yoktur**: "Faz durumu" tablosundaki Faz 8 yönetim kimliğidir
  (admin user, API key, RBAC) ve tamamlanmıştır; müşteri oturumu hiçbir fazın
  kapsamında değildir.

- **Müşteri kimliği doğrulanmıyor, dolayısıyla harcama limiti KOŞULLU
  uygulanıyor.** Yukarıdaki maddenin doğrudan sonucudur ama ayrı yazılmayı hak
  eder, çünkü ölçülen davranış üç ayrı biçimde ifade edilebiliyor: `customer_id`
  alanını hiç göndermemek (misafir sepeti, hiçbir limit uygulanmaz), başkasının
  kimliğini göndermek (harcama onun penceresinden düşer) ve
  `POST /store/v1/customers` ile taze bir misafir kaydı açıp onu göndermek
  (yeni kayıt hiçbir şirkete bağlı olmadığı için kuralsızdır). Üçü de gerçek
  ikilide, tek bir publishable anahtarla ölçüldü; sayılar aşağıdaki B2B
  bölümünün "Kuralın koşulu" başlığında, karar ise
  [ADR 0008](docs/adr/0008-musteri-kimligi-guven-siniri.md)'de. Çerçeve kimliği
  doğrulayan bir yüzey **sunmuyor**; sunması gereken taraf gömen uygulamadır.

## Hangi depodan gönderilir

Bir siparişin satırları farklı depolardan ayrılabilir ve karar iki modüle
bölünmüştür: **hangi depolarda yeterli stok var** bir olgudur ve stok
modülünden gelir, **hangisinden gönderelim** bir karardır ve kargo modülüne
aittir. Sepet akışı hiçbirini kendi vermez.

Kargo modülü tek bir depo değil bir **tercih sırası** döner; sepet akışı ilk
depoda ayırmayı dener, o depo yarışta tükenmişse sıradakine geçer ve kargo
modülüne yeniden sormaz. Sıra satır başına **bir kez** hesaplanır.

### Politika: ele, sırala, eşitliği boz

Politika `/admin/v1/shipping-locations` altından yazılır ve depo başına iki şey
taşır: hizmet ettiği kargo bölgeleri ve tercih sırası (`priority`).

```bash
# Ankara deposu yalnızca reg_tr'ye hizmet etsin ve varsayılanların önüne geçsin
curl -X PUT http://localhost:9000/admin/v1/shipping-locations/sloc_ankara \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"priority": -1, "region_ids": ["reg_tr"]}'
```

Sırayla uygulanır:

1. **Eleme** — bir depoya en az bir bölge bağlanmışsa ve sepetin bölgesi onların
   arasında değilse aday düşer. Hiç bağı olmayan depo **tüm** bölgelere hizmet
   eder.
2. **Sıralama** — kalanlar `priority` küçükten büyüğe dizilir. Kaydı olmayan
   depo sıfır önceliktedir; bir depoyu varsayılanların önüne almak için negatif
   değer verilir.
3. **Eşitlik bozma** — eşit öncelikte kimliği küçük olan öne geçer.

Gövde bir düzeltme değil, politikanın **tamamıdır** (bu yüzden `PATCH` değil
`PUT`): `region_ids` verilmezse deponun bağları silinir ve depo tüm bölgelere
açılır. `DELETE` kaydı siler, yani depoyu **kapatmaz, varsayılana döndürür** —
bir depoyu adaylıktan çıkarmak kargo modülünün yetkisinde değildir, aday
listesini stok olgusu üretir.

**Politika kaydı yoksa SEÇİLEN DEPO eskisiyle aynıdır**: eleme ve sıralama boşa
düşer, geriye eşitliği bozan kural kalır. Katı alternatif (kaydı olmayan depo
aday olamaz) açıldığı gün mevcut kurulumların tüm siparişlerini durdururdu.

Aynısı kalmayan iki şey var ve ikisi de kayıtsız kurulumu da etkiler: satır
başına **bir SQL sorgusu** eklendi (eski seçim veritabanına hiç dokunmuyordu,
yani yeni bir 500 yolu da açıldı) ve stok ayırma hatalarının **kodu** değişti
(bkz. `CHANGELOG.md`, kırıcı değişiklikler). Kod değişikliği depo bildiren
çağrıları da etkiler; o yol politikaya hiç girmez ama aynı sarmalamadan geçer.

### Kapsam bir kısıttır; tercih ayrı yazılır

`region_ids` "bu depo oraya **gönderemez**" demektir, "oraya göndermeyi tercih
etmem" demek değil. İki depoyu ayrı bölgelere bağlarsanız, birinin stoğu
tükendiğinde sipariş **düşer** — diğerinde mal olsa bile. "Önce Ankara,
tükenirse İstanbul" istiyorsanız bağ vermeyin, `priority` verin.

Bunun bir tuzağı var ve ölçülmüş hâliyle "Bilinen sınırlar"da yazılıdır: var
olmayan bir bölge kimliği bağlamak o depoyu her sepette eler ve tek depolu bir
kurulumda mağazayı kapatır.

### Reddin sebebi ayırt edilebilir

Eleme yüzünden düşen bir sipariş, stok yetersizliğinden farklı bir kod taşır:

| Durum | Kod |
|---|---|
| Hiçbir depoda yeterli stok yok | `checkout_workflow_reservation_failed` |
| Seçilen depolar tükendi | `inventory_insufficient_stock` |
| Hiçbir aday sepetin bölgesine hizmet etmiyor | `fulfillment_no_serviceable_location` |

Üçü de `409`'dur — girdide düzeltilecek bir şey yoktur — ama işletmecinin
yapacağı iş farklıdır. Vitrine ulaşan tek ayrım **koddur**: adım hatası alt
hatanın kodunu korur, mesaj ise her üç durumda da aynıdır (taşıma katmanı
gövdeye yalnızca en dıştaki mesajı yazar).

Üçüncü durumun mesajı adayların gerçekte hangi bölgelere bağlı olduğunu da
yazar — silinip yeniden açılmış bir bölgenin ölü kimliği ancak böyle görülür —
ama o metin **sunucu logunda ve `workflow_executions` kaydındadır**, HTTP
gövdesinde değil. Yani kod istemciye, döküm operatöre gider.

Karar ve reddedilen seçenekler:
[ADR 0010](docs/adr/0010-depo-secim-politikasi.md).

## Sertleştirme

| Bileşen | Ayar | Yapılandırılmamışsa |
|---|---|---|
| Hız sınırı | `RATE_LIMIT_PER_MINUTE`, `TRUSTED_PROXY_HOPS` | no-op (geçirir) |
| Idempotency | `IDEMPOTENCY_TTL` | no-op (geçirir) |
| Kimlik | `JWT_SECRET` | **her isteği reddeder** |

Neden aynı kural değil: bkz. [ADR 0007](docs/adr/0007-sertlestirme-arizada-davranis.md).

`Idempotency-Key` başlığı taşıyan bir POST/PUT/PATCH/DELETE bir kez işlenir;
tekrar aynı yanıtı `Idempotency-Replayed: true` ile alır. Aynı anahtarla
**farklı** bir gövde göndermek `409` döner — sessizce ilk yanıtı çalmak,
istemcinin ikinci isteğinin hiç işlenmediğini gizlerdi. Kayıt anahtarı
**çağıranın kimliğiyle ad alanına alınır**: aynı anahtarı seçen iki çağırandan
biri diğerinin yanıtını göremez.

Kayıt kararı **yalnızca durum koduna** bakar — gövdeden vermek, her yüzeyin
hata biçimini çekirdeğe öğretmek olurdu. Bunun bedeli, hatasını da `200` ile
bildiren bir yüzeyin korumanın dışında kalmasıdır; bugün depoda öyle tek bir uç
var (`POST /store/v1/graphql`) ve çözüm kaydı akıllandırmak değil **ucu yığından
çıkarmaktır**: `GuardOptions.IdempotencyExempt`. Muafiyet **yalnızca**
idempotency halkasınadır — muaf yol hız sınırından ve kimlikten geçmeye devam
eder — ve **tam yola** uygulanır, önekin tamamına değil. Yol çekirdekte yazılı
değildir (çekirdek modülleri import edemez); bileşim kökünden, modülün
`graph.Path` sabitinden geçirilir.

`TRUSTED_PROXY_HOPS`, istekle aramızdaki **güvenilen** ters proxy sayısıdır ve
iki yönde de yanlış verilebilir — ama bedelleri **aynı sınıfta değildir**:

| Değer | Sonuç | Sınıf |
|---|---|---|
| Fazla | İstemcinin `X-Forwarded-For`'a kendi yazdığı adres gerçek sanılır; saldırgan her istekte taze bir kova alır ve sınırı **tamamen** atlar | Güvenlik açığı |
| Eksik (`0`, proxy arkasında) | `X-Forwarded-For` hiç okunmaz, anahtar bağlantının adresine düşer; o adres her istekte proxy'nindir, yani `RATE_LIMIT_PER_MINUTE` "müşteri başına" değil **"tüm mağaza için"** bir tavan olur | Kapasite sorunu |

Varsayılan bu yüzden `0`'dır ve **değiştirilmedi**: doğrudan internete bakan bir
kurulumda doğru cevap odur ve yapılandırma hangisinin geçerli olduğunu bilemez.
Ama eksik değer de sessiz kalamaz — headless ticarette ters proxy arkasında
çalışmak neredeyse tek dağıtım biçimidir — bu yüzden hız sınırı **açıkken**
`TRUSTED_PROXY_HOPS=0` bırakılmış bir paylaşılan kurulum açılışta **uyarı**
üretir. Proxy arkasındaysanız aradaki güvendiğiniz atlama sayısını yazın (tek
ingress için `1`).

`RATE_LIMIT_PER_MINUTE <= 0` sınırlayıcıyı **hiç kurmaz** (ADR 0007'de sıfır
"kapat" demektir). Meşru bir seçimdir ama giriş ucunu da kotasız bırakır ve
kimsenin bilmediği bir "kapalı", kazayla yazılmış bir sıfırdan ayırt edilemez;
paylaşılan ortamlarda bu da uyarılır.

### Tek örnek mi, birden çok mu?

`GUARD_BACKEND` ikisini birden seçer:

| Değer | Hız sınırı | Idempotency |
|---|---|---|
| `memory` (varsayılan) | örnek sayısıyla **çarpılır** | örnekler arasında **hiç çalışmaz** |
| `redis` | paylaşılan | paylaşılan |

Aynı Redis örneğini paylaşan iki gobit kurulumu (örn. staging ve prod) farklı
`REDIS_KEY_PREFIX` kullanmalıdır; aksi hâlde birbirlerinin hız sınırını harcar
ve — daha kötüsü — birbirlerinin idempotency kaydını oynatır.

Fark yalnızca derece değil **tür** farkıdır: hız sınırının gevşemesi bir *hız*
sorunudur, hiçbir istek yanlış işlenmez. Idempotency'nin çalışmaması bir
*doğruluk* sorunudur — aynı anahtarla farklı örneklere düşen iki istek iki kez
işlenir, yani iki sipariş ve iki tahsilat. Birden çok örnek çalıştırıyorsanız
`GUARD_BACKEND=redis` zorunludur; paylaşılan bir ortamda `memory` bırakmak
açılışta uyarı üretir.

`GUARD_BACKEND=redis` ve `EVENT_BUS=redis` **aynı** istemciyi paylaşır; ikisi de
kapalıysa hiç bağlantı açılmaz.

Birden çok **örnek** birden çok **kiracı** demek değildir: örnekler aynı veritabanını
ve aynı kataloğu paylaşır, tek bir kurulumun yatay kopyalarıdır. Çerçeve kiracılar
arası bir sınır **tanımaz** — 74 tablonun hiçbirinde "bu satır kime ait" sorusunun
cevabı yoktur ve hiçbir sorgu böyle bir süzgeç taşımaz. İki müşteriye tek kurulumdan
hizmet vermek istiyorsanız cevap iki kurulumdur: bir kiracı = bir kurulum = bir
veritabanı = bir süreç. Bunun neden bir eksiklik değil bir karar olduğu, hangi
seçeneklerin reddedildiği ve kararı yeniden neyin açacağı
[ADR 0009](docs/adr/0009-cok-kiracililik-kurulum-siniri.md)'da yazılıdır.

## İzleme

OTLP toplayıcısının adresi (`OTEL_EXPORTER_OTLP_ENDPOINT`) verilmezse izleme
**tamamen kapanır** ve hiçbir dış bağlantı denenmez. Toplayıcıya ulaşılamaması
uygulamayı düşürmez.

Her isteğe bir span açılır; span adı ham yol değil **route deseni**dir
(`GET /store/v1/products/{id}`) — ham yol kullanılsaydı her ürün kimliği ayrı
bir metrik serisi üretir ve kardinalite patlardı. Ham yol yine de span'da
`url.path` özniteliğinde durur, yani ayrıntı kaybolmaz; kardinalite yalnızca
metriklerde sınırlanır.

Koruma middleware'inde reddedilen bir istek route eşleşmesine hiç varmaz ve
`http.route` değeri `unknown` olur; hangi uca gidildiği o span'ın `url.path`
özniteliğinden okunur.

Yerelde denemek için:

```bash
make up-tracing        # Postgres + Redis + Jaeger
OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 OTEL_EXPORTER_OTLP_INSECURE=true make run
# arayüz: http://localhost:16686
```

> Jaeger **yalnızca trace** kabul eder; uygulama aynı uca metrik de göndermeye
> çalışır ve her aralıkta bir `failed to upload metrics` satırı düşer.
> Zararsızdır (izleme arızası uygulamayı düşürmez) ama metrik de toplamak
> isteyen bir kurulum araya bir OpenTelemetry Collector koymalıdır.

## Eklentiler

Eklenti sıradan bir Go paketidir; `plugins/` altında yaşar ve hiçbir commerce
modülünü import etmez. Sözleşmeyi `core/provider`'dan, kayıt noktasını
`core/plugin.Host`'tan alır. Eklenti eklemek çekirdeği ya da bir modülü
**değiştirmez**: `cmd/server` içindeki katalog haritasına bir satır eklenir ve
kurulum `PLUGINS` ile seçilir.

```bash
PLUGINS=payment-stripe STRIPE_API_KEY=sk_test_… make run
```

Kurulum iki fazlıdır: `Install` modüllerden **önce** (eklentinin getirdiği
modül de yaşam döngüsünden geçebilsin), `Start` modüllerden **sonra**
(sağlayıcı kaydı ancak payment modülü ayağa kalkınca vardır). Bilinmeyen bir
eklenti adı ya da eksik ayar açılışta hata verir.

İki eklenti iki farklı uzatma biçimini gösterir:

| Eklenti | Ne yapar | Hangi uzatma noktaları |
|---|---|---|
| `payment-stripe` | **iskelet** — kayıt ve yaşam döngüsü tam çalışır, Stripe API çağrıları yapılmamıştır ve para hareketi üreten her metod açık bir "uygulanmadı" hatası döner | sağlayıcı kaydı |
| `search-pg` | **gerçek özellik** — ürün olaylarını dinler, PostgreSQL tam metin indeksini taze tutar, `GET /store/v1/search` ve `POST /admin/v1/search/reindex` uçlarını açar | kendi modülü + migration'ı, olay aboneliği, kendi route'ları |

```bash
PLUGINS=search-pg make run

# Eklenti kurulmadan ÖNCE var olan katalog indekste yoktur: bir kereye mahsus
# yeniden indeksleme. (Bundan sonrası olaylarla kendiliğinden tazelenir.)
curl -s -X POST localhost:9000/admin/v1/search/reindex -H "Authorization: Bearer $TOKEN"
# {"data":{"indexed":1,"removed":0,"pages":1}}

curl -s 'localhost:9000/store/v1/search?q=tişört' -H "x-publishable-api-key: $PK"
```

İndeks **olaylarla** tazelenir (`product.created` / `product.updated` /
`product.deleted`), yani eklenti açıkken eklenen ürün aramaya kendiliğinden
girer. Yeniden indeksleme yalnızca eklentinin **görmediği** dönem için
gerekir: eklentisiz açılmış bir kurulumda oluşturulan ürünler aksi hâlde
aramada hiç görünmez ve uç bunu bir hatayla değil, **boş bir sonuçla** bildirir.

Arama motoru bilinçli olarak **dış bir servis değildir**: PostgreSQL tam metin
araması, yeni bir bağımlılık ve yeni bir compose servisi getirmeden gerçek bir
özellik verir; eklenti sınırı sayesinde ileride Meilisearch/OpenSearch'e geçmek
başka hiçbir yeri değiştirmez.

> **Arama, kanal süzmesinin bypass'ı değildir.** Eklenti yalnızca ürün
> *kimliklerini* indeksler; kayıtları `product.interop` getirir ve görünürlük
> kuralı tek yerde kalır. Kuralı eklentide tekrar etmek, biri değiştiğinde
> vitrin ile aramanın sessizce ayrışması demek olurdu.

## Dosya yükleme

`POST /admin/v1/uploads` (multipart) bir görsel alır ve erişilebilir bir adres
döner; o adres mevcut ürün görseli akışına doğrudan takılır.

Bu, istemciden **rastgele bayt** kabul edilen tek yerdir ve kuralları yapısaldır:

- **Depo anahtarı üretilir.** İstemcinin dosya adı hiçbir yol ifadesine
  girmez, yani yol geçişi (`../`) *imkânsızdır* — "temizlemeyle" çözmek, her
  yeni kodlama numarasında yeniden karar vermek demekti.
- **İçerik tipi istemciye sorulmaz**, içerikten tespit edilir. İstemcinin
  `Content-Type`'ı bir iddiadır; ona güvenen bir izin listesi hiçbir şey elemez.
- **İzin listesi**, yasak listesi değil. Varsayılan yalnızca yaygın görsel
  tipleridir ve SVG **yoktur**: SVG bir belgedir, script taşır ve aynı kökenden
  sunulunca depolanmış XSS olur. Yapılandırmaya `text/html` gibi tarayıcıda
  çalışan bir tip yazmak da **reddedilir** — `nosniff` onu durdurmaz, çünkü
  yanıt gerçekten o tiptir.
- Sunumda `Content-Type` **saklanan** tipten yazılır ve `nosniff` her yanıtta
  bulunur.

Sunum ucu kimliksizdir (vitrindeki `<img>` başlık gönderemez) ama **kotasız
değildir**: kimlik ve kota ayrı kararlardır.

> Varsayılan `local` sağlayıcısı diske yazar ve kök dizin **kalıcı** olmalıdır.
> Göreli bir kök yerel geliştirmede doğrudur; paylaşılan bir ortamda konteynerin
> kalıcı olmayan katmanına düşer ve bir sonraki dağıtımda görseller kaybolur —
> ürün kaydındaki adres yerinde kalır, yani hiçbir hata görünmeden her görsel
> 404 döner. Açılışta uyarı loglanır.
>
> **Mutlak olması yetmez.** `FILE_ROOT=/tmp/gobit-uploads` mutlaktır, "göreli yol
> vermeyin" öğüdünü geçer ve yine de kaybolur: `/tmp`, `/var/tmp`, `/dev/shm` ve
> `TMPDIR`'in gösterdiği yer işletim sistemi tarafından temizlenir, üstelik çoğu
> dağıtımda tmpfs oldukları için yeniden başlatmayı bile beklemez. Uyarı bu
> kökleri de sayar; ölçüt "çalışma dizininden bağımsız mı" değil, **"süreç
> yeniden başladığında yerinde kalır mı"**dır.

## Alan olayları

Modüller kendi alan olaylarını event bus'a yayımlar; aboneler (eklentiler,
entegrasyonlar) onları dinler.

Aboneler bugün: arama eklentisi (`product.*`) ve bildirim modülü
(`order.placed`).

| Olay | Yük |
|---|---|
| `order.placed` | `order_id`, `display_id`, `status`, `region_id`, `customer_id`, `currency_code`, `total`, `item_count` |
| `product.created` / `product.updated` | `product_id`, `status` |
| `product.deleted` | `product_id` |

İki kural bağlayıcıdır:

- **Yük DARDIR.** Aboneye lazım olan her alanı olaya koymak, olayı kaydın
  ikinci bir kopyasına çevirir ve iki gösterim ayrışır. Abonenin elinde kimlik
  vardır, kaydı okuyabilir.
- **Tüm değerler dizedir**, sayısal olanlar da. Redis backend'i `Data`'yı JSON
  ile yazar; JSON'un tek sayı tipi olduğu için `int64` konan bir alan aboneye
  `float64` olarak ulaşır — aynı abone geliştirmede çalışıp **üretimde**
  düşerdi, üstelik para float üzerinden geçerdi.

**Bildirim** `order.placed`'i dinler ve sipariş onayını seçili
`NotificationProvider` üzerinden gönderir. Alıcı adresi **olaydan değil
sipariş kaydından** okunur (`order.interop`), çünkü kalıcı bir akışa kişisel
veri konmaz. Varsayılan sağlayıcı `log`'dur ve gerçekten göndermez — bunu
WARN seviyesinde açıkça söyler; gerçek gönderim bir eklentinin işidir
(`Host.RegisterNotificationProvider`).

> Handler hata dönerse olay **işlenmiş sayılır**; hiçbir backend yeniden
> teslim etmez (Redis, handler'ın sonucundan bağımsız olarak ACK'ler). Hata
> dönmek bir "yeniden dene" isteği değil, **görünürlük** sağlar.

## OpenAPI

Şema router ağacından **üretilir**, elle yazılmaz:

```bash
curl -s localhost:9000/openapi.json | jq '.paths | keys'
```

Uç yalnızca route desenlerini yayımlar, veri değil. Elle yazılan bir şema ilk
route değişikliğinde sessizce yalan söylemeye başlardı.

Gövde şemaları da **elle yazılmaz, Go tiplerinden türetilir** — aynı gerekçeyle:
elle yazılmış bir alan listesi, DTO'ya alan eklendiği gün eksik kalır ve kimse
fark etmez. Türetme `encoding/json`'un davranışını taklit eder (etiket,
`omitempty`, dışa kapalı alanlar, gömülü struct düzleştirmesi ve gölgelenme);
taklidin eksik olduğu yerde şema, hiç şema olmamasından kötüdür — istemci
doğru sandığı bir alan adını gönderir.

Modüller opsiyonel `openapi.Describer` arayüzüyle kendi uçlarını anlatır:

```go
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }
```

`module.Module` sözleşmesine metot EKLENMEDİ: anlatılmamış bir modül de
geçerli bir modeldir ve zorunlu kılmak tüm modülleri kırardı.

Şemadan istemci üretebilirsiniz:

```bash
make openapi-validate              # gerçek üreteçle doğrula
make openapi-client DIL=go         # ya da typescript-fetch, python, …
```

Depoda SDK **vendorlanmaz**: şema router'dan üretildiğine göre ikinci bir
artefaktı sürümlemek ve şemayla senkron tutmak gereksiz bir yüktür. Komut
belgelenir, isteyen kendi dilinde üretir.

## GraphQL vitrin okuma yüzeyi

Katalog ikinci bir yüzeyden daha okunur:

```bash
curl -s localhost:9000/store/v1/graphql \
  -H "x-publishable-api-key: pk_…" -H 'content-type: application/json' \
  -d '{"query":"{ products(limit: 5, q: \"tişört\") { count items { handle variants { sku priceSet } } } }"}'
```

Yüzey **dar** tutuldu: `products` ve `product` sorguları, mutation **yok**.
Sözleşme `internal/modules/product/graph/schema.graphqls` dosyasıdır — OpenAPI
belgesi gibi incelenebilir bir artefakt; Go tarafı ondan **üretilir**
(`make gen`, gqlgen).

Kararlar ve gerekçeleri:

| Karar | Gerekçe |
|---|---|
| Resolver'lar **vitrin servisini** çağırır (depoya inmez, yeni SQL yazmaz) | Satış kanalı görünürlük kuralı tek bir yerde yaşar; ikinci bir uygulama sessizce ayrışır ve yüzeylerden biri kataloğu sızdırır |
| Satış kanalı **argüman değildir**, `Principal`'dan okunur | Argüman olsaydı süzgeç yetkilendirme olmaktan çıkıp görüntüleme tercihine dönerdi (REST ile aynı gerekçe) |
| Uç `/store/v1` altındadır | Publishable anahtar doğrulaması ve hız sınırı önek yığınından **otomatik** gelir; ayrı bir önek, kimlik ve kota kurallarını ikinci kez yazmak olurdu |
| Yalnızca **POST** (GET 405) | Yanıt satış kanalına göre değiştiği için GET'in önbellek getirisi yok; bedeli var (sorgu loglara/tarayıcı geçmişine düşer, uzun sorgu 414 olur) |
| Uç **idempotency'den muaftır** (`Idempotency-Key` yok sayılır) | POST olması onu bir yazma yapmaz: kaydın koruyacağı yan etki yok, sakladığı şey yalnızca bayat katalog olurdu. Asıl gerekçe ise şu: GraphQL sözleşmesi gereği iç hatada da **200** döner, yani "5xx kaydedilmez" koruması bu yüzeyde hiç devreye girmez ve geçici bir arıza `IDEMPOTENCY_TTL` boyunca çalınırdı — arıza giderilse bile istemci aynı hata gövdesini almaya devam ederdi. Muafiyet aynı zamanda 900 KiB'lık bir sorgunun 64 KiB'lık gövde kapısından **önce** parmak izi için belleğe alınmasını da bitirir |
| `priceSet` / `inventoryItem` **JSON skalarıdır** | Tiplemek pricing/inventory şemasını `product`'a kopyalamak olurdu; kayıt bu modüle zaten gevşek tipli gelir. Bedeli kabul edildi: alan adları şemadan öğrenilemez, sahibi modülün belgesinden okunur. Karşılığında alan `null` olabilir — sağlayıcı kurulu değilse "fiyatı sıfır ürün" uydurmak zorunda kalmayız |
| Tek belgeye **yedi aile kapı**: fragment açılımı, derinlik, karmaşıklık, alan tekrarı, iç gözlem, ayrıştırma ve **yanıt baytı** | Bu uçta maliyeti sorguyu **yazan** belirler; hız sınırlayıcı ise takma adlarla yüzlerce kök sorgu taşıyan belgeyi de **bir** istek sayar. Ayrıntı ve ölçümler aşağıda |
| **İç gözlem açık** (`GRAPHQL_INTROSPECTION=false` ile kapanır), ama kendi kapılarının arkasında | Şema bu deponun içinde duran bir dosyadır: kapatmak saldırgandan bir şey saklamaz, kod üreteçlerini körleştirir. Şemasına kendi alanlarını ekleyen kurulum için hesap değişir, o yüzden anahtar var. Anahtar artık bir acil durum vanası değil: iç gözlemin kök sayısı ve derinliği ayrıca sınırlıdır |
| Hata gövdesi çekirdeğin `WriteError`'ından geçirilir; ayrım hatanın **tipine değil kaynağına** bakar | "Hangi hata istemciye olduğu gibi verilir" kuralının ikinci bir uygulaması, ayrıştığı gün sunucu içi ayrıntı sızdırırdı — nitekim sızdırdı: tipe bakan eski ayrım, sınıflandırılmamış sürücü hatasını (bağlantı dizesi, parola, SQL metni) maskelemeden ve loglamadan geçiriyordu. Kodlar REST ile aynıdır (`extensions.code`) |

### Sertleştirme: maliyeti istemci belirliyorsa sınırı sunucu koyar

REST'te bir isteğin maliyetini sunucu belirler — yol sabit, gövde sabit, bir
istek bir sorgudur. GraphQL'de sorgunun **şeklini**, yani maliyetini istemci
yazar; hız sınırlayıcı ise iki yüzeyde de aynı şeyi sayar: **bir istek**. Kapı
ailesi yedidir ve her biri ötekinin göremediği belgeyi yakalar:

| Kapı | Varsayılan | Neyi sayar | Yakaladığı |
|---|---|---|---|
| Açılım | `GRAPHQL_MAX_SELECTIONS=10000` | fragment'lar açıldıktan sonraki seçim | **Fragment bombası.** `f(k) = ...f(k-1) ...f(k-1)` zinciri 26 seviyede **1.127 bayt** yazıp 2²⁶ seçim açar; ağacı gezen her hesap (derinlik, tekrar, gqlgen'in karmaşıklığı) orada asılırdı. En başta koşar ve ötekileri korur; bütçe bitince gezinme yarıda kesilir |
| Derinlik | `GRAPHQL_MAX_DEPTH=10` | seviye | İç içe geçen sorgu. Şema bugün döngüsel değil (en derin meşru yol 5), ama bir alan geri referans verdiği gün sorgu şemanın değil istemcinin yazdığı yere kadar iner |
| Karmaşıklık | `GRAPHQL_MAX_COMPLEXITY=50000` | alan × eleman | Sığ ama pahalı sorgu: `limit=100` ile yüz ürünün tüm ağacı, ya da takma adlarla yığılmış yüzlerce kök sorgu |
| Alan tekrarı | `GRAPHQL_MAX_FIELD_REPETITION=20` | aynı kümede aynı `(tip, alan)` | **Aynı alanın takma adlarla yığılması.** Karmaşıklık alan *sayısını* fiyatlar, baytı değil: 489 kez istenen `description` 50.000'lik tavana tam oturur ve 204,9 MiB yanıt üretirdi |
| İç gözlem kökü | `GRAPHQL_MAX_INTROSPECTION_ROOTS=2` | belgedeki `__schema` / `__type` | Tek belgede yığılmış iç gözlem. Kökler *sığdır*, yani derinlik kapısı onları hiçbir ayarla göremez |
| İç gözlem derinliği | `GRAPHQL_MAX_INTROSPECTION_DEPTH=15` | seviye | İç gözlem ağacının derinliği. Veri tavanından **ayrıdır**: standart iç gözlem sorgusu 13 seviyedir, tek tavan olsaydı veri sınırını da 13'ün üstüne çıkarmak gerekirdi |
| Gövde | 64 KiB (sabit) | bayt | Yukarıdakiler ancak belge **ayrıştırıldıktan sonra** ölçülebilir; ayrıştırma maliyetini yalnızca bu ve jeton sınırı bağlar |
| Jeton | 8.192 (sabit) | jeton | Gövdeye sığan ama binlerce jeton taşıyan belge (64 KiB en ucuz jetonlarla 32.000 jeton demektir). En ucuz kapıdır: belge sonuna kadar ayrıştırılmaz |
| Yanıt | `GRAPHQL_MAX_RESPONSE_BYTES=4194304` | **gerçekleşen bayt** | Tahminin kaçırdığı her şey. Diğer kapılar belgeye bakıp maliyeti *tahmin* eder ve bir alanın **içeriğini** bilemez; son söz ölçümündür |

Karmaşıklığın birimi "kaç alan çözülür"dür ve **liste alanlarında eleman
sayısıyla çarpılır** — sabit maliyet vermek, tam da pahalı olan sorguyu ucuz
gösterirdi. Kök sorgular ayrıca sabit bir taban taşır (bir veritabanı
gidiş-dönüşü, seçilen alan azalınca ucuzlamaz).

Kalibrasyon **ölçülmüştür** ve `graph/limits_test.go` içindeki
`kalibrasyonBelgeleri` tablosunda sabitlenmiştir; bayt sütunu aynı dosyadaki
ölçüm fikstürüyle (4 KiB açıklamalı ürün, üç varyant, fiyat ve stok kayıtları)
alınmıştır:

| belge | istek | karmaşıklık | yanıt | sonuç |
|---|---:|---:|---:|---|
| ürün sayfası (PDP, her şey dâhil) | 643 B | 2.368 | 6,8 KiB | geçer |
| kategori listesi (24 ürün, kart + fiyat) | 118 B | 2.344 | 15,1 KiB | geçer |
| varsayılan sayfada TÜM alanlar (20 ürün × tüm ağaç) | 655 B | 28.440 | 136 KiB | geçer |
| `limit=100` ile TÜM alanlar | 667 B | 138.200 | 680 KiB | karmaşıklık |
| 400 takma adlı `products { count }` | 9,7 KiB | 408.000 | 8,5 KiB | alan tekrarı |
| **489 takma adlı `description`, `limit=100`** | 8,5 KiB | **50.000** | **204,9 MiB** | alan tekrarı |
| **1500 takma adlı `description`, varsayılan sayfa** | 26,8 KiB | 31.020 | **125,7 MiB** | alan tekrarı |
| **302 takma adlı `__schema`** | 44,7 KiB | **0** | **5,00 MiB** | jeton (9.364) |
| **448 takma adlı `__type`** | 58,5 KiB | 7.168 | 1,32 MiB | jeton (14.786) |
| **302 küçük `__schema` kökü** | 9,3 KiB | **0** | 0,84 MiB | iç gözlem kökü |
| **26 seviyelik katlanan fragment** | **1,1 KiB** | ölçülemez (asılır) | — | açılım bütçesi |

Kalın satırların **yanıt sütunu, o kapılar eklenmeden önce ölçülen** yanıttır;
bugün hiçbiri çalıştırılmıyor (reddedilen belgelerin yanıt sütunu, çalıştırılsa
ne üreteceklerini gösterir). Tablonun asıl söylediği şey şudur: karmaşıklık
sütunu tek başına bakıldığında bu belgelerin hepsi **masumdur** — 489 takma adlı
belge tavana tam oturur, iç gözlem belgelerinin karmaşıklığı sıfırdır, fragment
bombasının karmaşıklığı hiç hesaplanamaz. Alan sayımı, tam da kaçırdığı boyutu
hiç sormuyordu.

Karşılaştırma noktası `limit=100` satırıdır: aynı yüz ürünü tüm alanlarıyla
çekmek **680 KiB**'dır ve REST'ten `GET /store/v1/products?limit=100` ile
istemek de aynı mertebedir. 204,9 MiB'ı üreten şey daha çok *kayıt* değil,
**aynı kaydın 489 kez serileştirilmesidir** — ve REST istemcisi bunu isteyemez.

Yanıt sınırına çarpıldığında **yarım JSON gönderilmez**: gövdenin hiçbir baytı
gitmemişken (bugünkü POST taşıması yanıtı tek seferde yazar) aşan gövde atılır
ve yerine tam bir hata zarfı yazılır; bir kısmı gitmişse tam bir belge artık
imkânsızdır ve bağlantı `http.ErrAbortHandler` ile bırakılır. Kırpılmış bir
gövde istemciyi ya çözemeyeceği bir ayrıştırma hatasına düşürür ya da — daha
kötüsü — kısa bir sonuç sanılır.

Sorgu önbelleği **girdi sayısıyla değil bayt ile** sınırlıdır (girdi başına 8
KiB) ve bir belge önbelleğe ancak **tüm kapılardan geçtikten sonra** girer:
gqlgen belgeyi doğrulamadan hemen sonra saklar, sınır eklentileri ise ondan
sonra koşar — yani reddedilen belgeler de yer tutuyordu (ölçüldü: 100 reddedilmiş
belge, `runtime.GC` sonrası 171,8 MiB kalıcı yığın) ve vitrinin gerçek
belgelerini önbellekten atıyordu.

Sınırlar **yükseltilebilir, kaldırılamaz**: `0` ya da negatif değer "sınırsız"
değil "varsayılanı kullan" demektir; ortam değişkeni olarak verilen `0`/negatif
değerde uygulama açılışta durur. (`RATE_LIMIT_PER_MINUTE` ile karıştırmayın;
orada `0` gerçekten "kapat" demektir — bkz. ADR 0007.) Gövde ve jeton sınırları
sabittir: ikisi de ayrıştırıcıyı bağlar ve gevşetilmeleri bir kapasite tercihi
değil, ayrıştırıcıyı istemciye açmaktır. Kalan **yedi kapının hepsinin** bir ortam
değişkeni vardır ve varsayılanları iki yerde (çekirdek yapılandırması ve
sınırı uygulayan `graph` paketi) tekrarlandığı için bağ bir testle sabitlenmiştir
(`internal/arch`). Test yalnızca bugünkü değerleri karşılaştırmakla kalmaz,
`graph.Options`'a eklenen **her** yeni sınırın çekirdekte karşılığı olmasını
zorlar: ayarlanamayan bir sınır, kurulumu koda çatal atmaya zorlar ve bunu
operatör ancak üretimde fark eder.

### Hata politikası: ayrım tipe değil kaynağa bakar

Hata gövdesi tarafında da tek kural geçerlidir ve kural **kaynak** üzerinedir:

- **Servis hataları** — resolver'ın altından gelen her şey, *tipli olsun
  olmasın* — çekirdeğin `WriteError` yolundan geçer. Yani sınıflandırılmamış
  bir hata (sürücünün `pq: … password=… ; SELECT …` metni gibi) burada da
  `KindInternal` sayılır: istemci genel mesajı ve `internal_error` kodunu
  görür, gerçek metin **loglanır**. Ayrım bir zamanlar hatanın *tipine*
  bakıyordu ve tipsiz olanı istemciye olduğu gibi veriyor, üstelik hiç
  loglamıyordu — yani REST'te maskelenen hata bu yüzeyden çıkabiliyordu.
- **Protokol hataları** — ayrıştırma, doğrulama ve sınır kapıları — maskelenmez;
  maskelenseydi istemci sorgusunu düzeltemezdi. Aynı sebeple bunlar sunucu
  hatası olarak **loglanmaz**: istemcinin yazım yanlışı, logu istemcinin
  doldurabildiği bir boru hâline getirirdi.
- **Taşıma hataları** — belge daha okunamadan başarısız olan istek — kendi
  metnimizle döner. gqlgen'in taşıması JSON'u çözemediğinde **ham gövdeyi**
  hata mesajına ekliyordu; 64 KiB'a kadar saldırgan denetimindeki metin hem
  yanıta hem de yanıtı kaydeden ara katmanların loglarına giriyordu.

`GRAPHQL_INTROSPECTION=false` **önerileri de kapatır** ve bu, anahtarın
vaadinin tamamlanmasıdır: doğrulayıcı, `__schema` kapalıyken bile
`Did you mean "products" or "product"?` diyerek şemanın adlarını perakende
dağıtıyordu — hem de bütün hataları tek yanıtta topladığı için bir istekte
onlarca ad denenebiliyordu. Kapanan şey adların *sayılmasıdır*, tek tek
*tahmin edilmesi* değil; onu da kapatmanın tek yolu doğrulama mesajlarını
tümüyle silmek, yani yüzeyi meşru istemci için de hata ayıklanamaz hâle
getirmekti. Aynı anahtarla `__schema`/`__type` isteyen belge artık
çalıştırılmadan reddedilir (`INTROSPECTION_DISABLED`); `__typename` bir iç
gözlem kökü değildir ve çalışmaya devam eder.

Kodlar `extensions.code` altındadır: `DEPTH_LIMIT_EXCEEDED`,
`COMPLEXITY_LIMIT_EXCEEDED`, `FIELD_REPETITION_LIMIT_EXCEEDED`,
`INTROSPECTION_LIMIT_EXCEEDED`, `INTROSPECTION_DISABLED`,
`RESPONSE_LIMIT_EXCEEDED`, `SELECTION_BUDGET_EXCEEDED`,
`REQUEST_BODY_TOO_LARGE`, `REQUEST_DECODE_FAILED`.

## B2B: alıcı bir birey değil, yetkisi sınırlı bir çalışan

`b2b` modülü **şirket** ve **şirket çalışanı** kavramlarını ekler; çalışanın
dönem başına harcayabileceği bir üst sınırı olabilir. Modül başka hiçbir modülü
import etmez ve çekirdeğe dokunmaz — bu deponun "modüler monolit" iddiasının
sınavı da buydu: yeni bir alan modülü mevcut kalıba uyarak eklenebiliyor mu.

Çalışan → müşteri bağı **yalnızca `core/link`'tedir**; `b2b_company_employee`
tablosunda `customer_id` sütunu **yoktur**. Aynı ilişkiyi hem sütunda hem link'te
tutmak, ikisinin ayrışabileceği bir yer açardı.

### Kural iki modüle bölünmüştür ve bu bilinçlidir

Harcama limiti iki bilgiyi birleştirir: **limit** (`b2b`'nin verisi) ve
**harcama** (`order`'ın verisi — verilmiş siparişlerin toplamı). İkisi
birbirini import edemez, bu yüzden sözleşme JSON'dur ve `order` kendi dar
arayüzünü (`service.SpendingPolicy`) kendi paketinde tanımlar, somut tipi
container'dan `b2b.interop` adıyla çözer (ADR 0001).

Bunun kabul edilen bedeli şudur: **derleyici bu sözleşmeyi denetlemez.** Alan
adlarından biri ayrışsaydı her iki paketin birim testleri de yeşil kalır,
üretimde ise `"limited"` alanı çözülemediği için limit *sessizce kalkardı*.
Sözleşmenin iki ucu bu yüzden gerçek container üzerinden, e2e'de birleştirilir
(`internal/e2e/b2b_test.go`).

### Kontrol nerede ve neden orada

Kural `order.CreateOrder` içinde, siparişin yazıldığı **işlemin içinde** ve
müşteri kilidi altında uygulanır. İki sonucu vardır:

- **Para hiç yetkilendirilmez.** `complete_cart` saga'sında `create_order`,
  `authorize_payment`'tan **önce** koşar. Reddedilecek bir alışverişin parasını
  çekip sonra iade etmek yanlış olurdu.
- **İki eşzamanlı sipariş limiti birlikte aşamaz.** Kontrolü çağıran tarafta
  (örneğin saga'da) yapmak mümkündü ama kontrol ile yazma iki ayrı işleme
  düşer ve ikisi de limitin altında görünürdü.

İkinci sebep **kaçıştır**: bu modülde sipariş yaratan tek yol `CreateOrder`'dır.
Kural saga'ya konsaydı, ileride eklenecek ikinci bir çağıran onu sessizce
atlardı — bu deponun defalarca bulduğu hata sınıfı tam olarak budur.

### Kuralın koşulu: limit, müşterisini BEYAN EDEN alışverişe uygulanır

Yukarıdaki her cümle doğrudur ama tek başına okununca yanlış bir şey söyler.
Kural `CreateOrderInput.CustomerID` üzerinden çalışır ve o kimlik zincire vitrin
sepetinin **gövdesinden** girer. Mağaza yüzeyinin tek kimliği publishable
anahtardır ve o bir satış kanalını temsil eder, bir müşteriyi değil
(`corehttp.Principal` müşteri kimliği taşımaz). Yani `customer_id` bir olgu
değil, hiçbir kanıt istemeyen bir **iddiadır**.

Gerçek ikili üzerinde, tek bir publishable anahtarla ölçüldü — aynı sepet, aynı
istemci, tek fark gövdedeki alan (limit `50_000`, sepet toplamı `76_800`):

| `POST /store/v1/carts` gövdesi | Tamamlama sonucu |
|---|---|
| `{"country_code":"TR","customer_id":"cus_…"}` | **`409`** `order_spending_limit_exceeded` |
| `{"country_code":"TR"}` | **`200`**, sipariş açılır (`customer_id: ""`) |

Aynı ölçümün ikinci yarısı: başkasının `customer_id`'siyle tamamlanan alışveriş
o müşterinin adına yazıldı ve harcaması **onun** penceresinden düştü — ardından
çalışanın kendi alışverişi `409` aldı. Yani iddia yalnızca bir kaçış yolu değil,
adı bilinen bir çalışanın harcama hakkını **yakma** yoludur.

Beyanı zorunlu kılmak da kapatmaz: `POST /store/v1/customers` publishable
anahtarla yeni bir misafir kaydı açar ve o kayıt hiçbir şirkete bağlı
olmadığı için kuralsızdır.

Dördüncü kapı atfın **sonradan** yapılabilmesidir: misafir olarak açılan bir
sepet `POST /store/v1/carts/{id}` ile başkasının `customer_id`'sine devredilir
ve sipariş o kimliğe yazılır. Yani atıf yalnızca sepet açılışında değil,
sepetin ömrü boyunca beyana dayanır (ölçüldü: devir `200`, sipariş kurbanın
adına).

Bu bir açık değil, **çizilmiş bir sınırdır**: gobit müşteri kimliğini
doğrulayan bir yüzey sunmaz; sunması gereken taraf gömen uygulamadır. Kararın
tamamı, reddedilen seçenekleri ve gömen uygulamaya düşen işin listesi
[ADR 0008](docs/adr/0008-musteri-kimligi-guven-siniri.md)'dedir. Sınırın
bugünkü yeri `order`'da iki testle sabitlenmiştir
(`TestMisafirSiparisindeHarcamaKuraliHicSorulmaz`,
`TestHarcamaKuraliBeyanEdilenMusteriyeUygulanir`); ikisi de bir yeteneği değil
bir kararı korur ve kimlik doğrulama geldiğinde **düşmeleri beklenir**.

Kuralın ne işe yaradığı bu koşulla birlikte okunmalıdır: kimliğin doğrulandığı
bir vitrinde limit muhasebe disiplinini **uygular**; doğrulanmadığı bir
vitrinde ise yalnızca dürüst istemcinin hatasını yakalar.

### Sınırlar

- Limit `nil` ise **sınırsız**, `0` ise **gerçek bir sıfır limit**. İkisi ayrı
  cümlelerdir; karışsalardı limiti girilmemiş her çalışan alışveriş yapamazdı.
- Pencere **takvimdendir** (aylık: ayın 1'i, yıllık: 1 Ocak, UTC), çalışanın işe
  başlama anından değil. Dönem ortasında şirket değiştiren çalışan eski şirketteki
  harcamasını da taşır; sapma tek yönlü ve **kısıtlayıcıdır** (hak ettiğinden az
  harcar, fazlasını asla).
- Şirketin para birimi ile sepetinki farklıysa sipariş reddedilir; çevirmek için
  bir kur kaynağı gerekirdi ve o karar bu modülün değildir.
- `b2b` **kayıtlı değilken** davranış b2b hiç yokmuş gibidir: hiçbir okuma,
  hiçbir kilit. Saf B2C kurulum, `cmd/server`'daki tek satır silinerek elde
  edilir — yani bir **kod** değişikliğiyle. Kapatan bir ortam değişkeni
  **bilinçli olarak yoktur**: yanlışlıkla `false` verilen bir anahtar, harcama
  limitini hiçbir hata üretmeden kaldırır ve bu tam da kapatılmaya çalışılan
  sessiz arıza sınıfıdır. Kod yolu ise yarım kalamaz —
  `TestHerModulBilesimKokundeKayitli` satırı silen kişiden kararı gerekçesiyle
  yazmasını ister. Modülü B2C kurulumda **bırakmanın** bedeli de küçüktür ve
  görünürdür: iki boş tablo ve hiçbir şirket kaydı olmadığı için asla
  tetiklenmeyen bir kural.

## Mimari kararlar (ADR)

Planın bıraktığı belirsizlikler `docs/adr/` altında karara bağlanır. ADR'ler plan
dokümanı kadar bağlayıcıdır; çelişki hâlinde ADR geçerlidir.

| # | Karar | Özet |
|---|---|---|
| [0001](docs/adr/0001-modul-arasi-iletisim.md) | Modüller arası iletişim | Tüketici tarafı interface: ihtiyacı olan modül, dar interface'i **kendi paketinde** tanımlar; sağlayıcı import edilmez, container'dan isimle çözülür |
| [0002](docs/adr/0002-di-container-el-yazmasi.md) | DI container | `samber/do` yerine el yazması: Bölüm 5.1 sözleşmesi `any` alan `Provide` istiyor, do tip parametreli olduğu için teşhis/conflict/kapatma sırası elden gidiyordu |
| [0003](docs/adr/0003-migration-iptali.md) | Migration iptali | golang-migrate sürücüsü ctx kullanmıyor; bağlantının sahibi biz olup iptalde kapatıyoruz, böylece iptal edilen akış dönüşten sonra ilerlemiyor |
| [0004](docs/adr/0004-query-veri-erisimi.md) | Query veri erişimi | Modüller container'a `<modül>.query` adıyla dar bir `Provider` kaydeder; çekirdek modülleri tanımadan batch okuma yapar |
| [0005](docs/adr/0005-link-semasi-migration-disinda.md) | Link şeması | Link tabloları derleme zamanında bilinmediği için migration dosyasıyla değil, bildirim anında idempotent DDL ile kurulur |
| [0006](docs/adr/0006-workflow-modul-erisimi.md) | Workflow → modül erişimi | `internal/workflows` de modülleri import etmez; dar arayüz + container'dan adla çözüm (ADR 0001'in workflow'lara uygulanması) |
| [0007](docs/adr/0007-sertlestirme-arizada-davranis.md) | Sertleştirmede arıza davranışı | Tek tip kural yok: kimlik **kapalı kalır** (fail-closed), hız sınırı **açık kalır** (fail-open), idempotency ayırmada reddeder / kayıtta anahtarı serbest bırakır |
| [0008](docs/adr/0008-musteri-kimligi-guven-siniri.md) | Müşteri kimliği güven sınırı | Çerçeve müşteri kimliğini **doğrulamaz**: `customer_id` bir iddiadır, harcama limiti yalnızca müşterisini **beyan eden** alışverişe uygulanır; doğrulama gömen uygulamanın işidir |
| [0009](docs/adr/0009-cok-kiracililik-kurulum-siniri.md) | Çok kiracılılık | Sınır **kurulumdur, satır değil**: her kurulum tek kiracılıdır, izolasyon dağıtım katmanındadır; çerçeve kiracılar arası bir sınır tanımaz, uygular ve iddia etmez |
| [0012](docs/adr/0012-repository-language-and-solid.md) | Depo dili ve SOLID | Çalışma dili **İngilizce**; geçiş kademeli ve **defterlidir** (`internal/arch/testdata/turkish_ledger.txt` yalnızca küçülür). Dedektör üç şeritlidir çünkü yalnız diyakritiğe bakan bir kural tek bir harf çevirisiyle yalan söyler. SOLID yalnızca **mekanik olarak ölçülebilen** yerde zorlanır; SRP-mikro ve LSP için denetim OLMADIĞI açıkça yazılıdır |
| [0011](docs/adr/0011-yonetim-paneli-dorduncu-agac.md) | Yönetim paneli | Panel dördüncü bir ağaçta (`internal/adminui`) yaşar, oturumu **yalnızca kendi ağacında** geçerli bir çerezle taşır ve HTML'i çekirdeğin yazıcısından geçirir; yönetim API'si başlık-only kalır, CSRF bağışıklığı korunur |
| [0010](docs/adr/0010-depo-secim-politikasi.md) | Depo seçim politikası | Lokasyon modeli kargo modülünün **kendi** şemasındadır; bölge bağı bir **kısıt**, öncelik bir **sıra**dır. Yüzey tek depo değil tercih sırası döner ve satır başına bir kez sorulur |

ADR 0001, planın Bölüm 2.1 ("erişim public service interface üzerinden") ile
Bölüm 2.4 ("modüller derleme zamanında birbirine bağımlı olmaz") arasındaki
çelişkiyi çözer — Go'da interface'ler paketlerde yaşadığı için sağlayıcının
interface'ini import etmek 2.4'ü ihlal ederdi.

## Bilinen sınırlar

Bu bölüm **bugünü** anlatır: aşağıdakiler `main`'de hâlâ geçerli olan
sınırlardır. Bir sürüm adı bilinçli olarak YAZILMAZ — yazılsaydı her sürüm
kesiminde güncellenmesi gereken, güncellenmediğinde de sessizce bayatlayan bir
tarih iddiası olurdu. Kapanmış olanlar buradan DÜŞER; hangi sürümde kapandıkları
[`CHANGELOG.md`](./CHANGELOG.md)'de durur, çünkü orası bir geçmiş kaydıdır ve
geriye dönük düzeltilmez. Hepsi araştırıldı, karara bağlandı ve gerekçeleri
kodun godoc'unda duruyor — kayda geçmemiş bir açık, kimsenin kapatmadığı
açıktır.

**Kimlik ve yetki**

- **Müşteri kimliği doğrulanmaz.** `customer_id` bir olgu değil, hiçbir kanıt
  istemeyen bir iddiadır; bunun harcama limiti üzerindeki üç ayrı sonucu
  yukarıda "Kuralın koşulu" başlığında ölçümleriyle yazılıdır. Sınırın doğru
  cümlesi "harcama limiti uygulanmıyor" değil, "limit yalnızca müşterisini
  **beyan eden** alışverişe uygulanır"dır. Karar
  [ADR 0008](docs/adr/0008-musteri-kimligi-guven-siniri.md)'de; doğrulamayı
  kuracak taraf gömen uygulamadır.
- **Vitrin sepetlerinde sahiplik denetimi yoktur** — model bir yetenek URL'idir
  ve kuralları yukarıda "Aynı ölçütün henüz uygulanmadığı yer" başlığında
  yazılıdır.
- **Oturum iptali yalnızca toptandır.** `POST /admin/v1/auth/logout` ve parola
  değişimi çağıranın BÜTÜN oturumlarını düşürür; tek bir cihazı düşürecek bir
  uç yoktur (bkz. `internal/modules/auth/api`).

**Satış kanalı kapsamı**

- **Kanal ataması olmayan ürün tüm kanallarda görünür.** Kural bilinçli ve
  geriye uyumludur (açıldığı gün katı alternatif mevcut katalogları boşaltırdı)
  ama bir tuzağı vardır: son kanal bağını silmek ürünü gizlemez, tüm vitrinlere
  açar. Gizlemek için `status` kullanılır. Kuralın tek kaynağı
  `internal/modules/product/repository/saleschannel.go`'daki SQL şablonudur.
- **Kapsam GİRİŞTE uygulanır; sepete girmiş bir satırın adedi sonradan
  artırılabilir.** Satır adedini güncelleyen yol
  (`internal/workflows/cart/update_line_item.go`) ve tamamlama akışı kapsamı
  yeniden sormaz. Sonucu: ürün başka bir kanala taşındıktan sonra bile,
  sepetinde zaten bir satırı olan istemci o üründen daha fazla satın alabilir.
  Bu, yukarıda gerekçesi yazılı kararın bedelidir — alternatifi, bir katalog
  düzenlemesinin müşterinin dolu sepetini ödenemez hâle getirmesiydi.

**Kurulum ve işletim**

- **Yönetim paneli YALNIZCA OKUR.** `/admin/ui` altındaki panel
  ([ADR 0011](docs/adr/0011-yonetim-paneli-dorduncu-agac.md)) giriş, çıkış ve
  katalog ekranlarını (ürün listesi, ürün sayfasında varyant/fiyat/stok) taşır;
  hiçbir yazma yolu yoktur. Ürün yaratmak, fiyat vermek ve stok girmek `/admin/v1`
  üzerinden, `Authorization: Bearer` ile yapılır.

  Panelin oturum çerezi yönetim API'sinde KABUL EDİLMEZ ve bu bir eksiklik
  değil karardır: API'nin CSRF bağışıklığı jetonun tarayıcının kendiliğinden
  eklemediği bir başlıkta yaşamasından gelir.

  Katalog ekranı fiyatı, para biriminin ondalık basamak sayısı BİLİNMİYORSA
  ham minor unit tam sayısı olarak ve bunu söyleyerek gösterir. Ölçek bölge
  kaydından okunur; hiç bölge tanımlanmamış bir kurulumda `19990 TRY (minor
  units)` görülür. Sabit 100 varsaymak JPY ve KWD gibi 0 ve 3 basamaklı para
  birimlerinde YANLIŞ tutar gösterirdi.
- **Çok kiracılılık yoktur.** Bir kiracı = bir kurulum = bir veritabanı = bir
  süreç; ayrıntı yukarıda "Tek örnek mi, birden çok mu?" başlığında, karar
  [ADR 0009](docs/adr/0009-cok-kiracililik-kurulum-siniri.md)'da.
- **Migration'ların işletmeciye açık bir geri alma yolu yoktur.** Her modülün
  `.down.sql` dosyaları vardır, geri alınabilirlikleri testle denetlenir ve
  `internal/core/db/migrate.go` içindeki `db.MigrateDown` Go'dan çağrılabilir —
  ama onu çağıran bir komut ya da uç YOKTUR; `make migrate-down` da bunu söyler.
  İleri yön açılışta otomatiktir.
- **Depo politikası bölge KAPSAMINI ve TERCİH sırasını ifade eder, başkasını
  değil.** Stok dağılımı ("en çok stoğu olan depoyu öne al"), maliyet ve sipariş
  düzeyinde karar ("tüm satırları tek depodan çıkar") İFADE EDİLEMEZ; her birinin
  neden edilemediği [ADR 0010](docs/adr/0010-depo-secim-politikasi.md)'da
  yazılıdır. Öncelik **depo** başınadır, yani "R1 için önce A, R2 için önce B"
  de yazılamaz — bölge başına yazılabilen tek şey dışlamadır.
- **Yanlış bir bölge bağı mağazayı KAPATIR ve sepeti kalıcı olarak tüketir.**
  Var olmayan bir bölge kimliği bağlamak (ya da bir bölgeyi silip aynı adla
  yeniden açmak — yeni kayıt yeni kimlik alır) o depoyu her sepette eler; tek
  depolu bir kurulumda sonucu, katalog dolu olduğu hâlde her tamamlamanın
  reddedilmesidir. Düşen sepet bir daha tamamlanamaz, çünkü tamamlama akışının
  idempotency anahtarı sepet kimliğinden türer. Arıza görünürdür ama
  görünürlüğün sınırı vardır: vitrin gövdesine yalnızca KOD ulaşır
  (`fulfillment_no_serviceable_location`); adayların bağlarını yazan döküm
  sunucu logunda ve `workflow_executions` kaydındadır. Geri dönüş tek bir
  yönetim yazmasıdır — ama operatörün o kayda erişebilmesine bağlıdır.
- **Bölge bağı bir TERCİH değil KISITTIR.** İki depoyu ayrı bölgelere bağlayan
  bir işletmeci, ilk deponun stoğu yarışta tükendiğinde siparişin düşmesini
  kabul etmiş olur. "Önce A, tükenirse B" bölge bağıyla değil ÖNCELİKLE yazılır.
- **Son bölge bağını silmek depoyu gizlemez, TÜM bölgelere açar** — satış kanalı
  kuralının aynısı, tek farkla: orada bedel görünürlük, burada düşen sipariştir.

**Değişmezlerin sınırı**

- **Modüller arası imzalar derleme zamanında denetlenmez.** Dar arayüz +
  container'dan adla çözüm, [ADR 0001](docs/adr/0001-modul-arasi-iletisim.md)'in
  kabul edilen bedelidir: ayrışan bir alan adı iki paketin birim testlerini de
  yeşil bırakır, uçları gerçek container üzerinden e2e'de birleşir.
- **`TestHerAkisBilesimKokundeKurulu` sözdizimsel bir vekildir.** "Yanlış
  yapılandırma açılışı durdurabilir mi" sorusunu "kuruluma giden yol bir `go`
  ifadesinden geçiyor mu" diye sorar; `go` tek satırlık bir dolaylamanın
  arkasına saklandığında denetim geçer, oysa özellik sağlanmaz (gerçek süreçte
  ölçüldü). Yakaladığı biçimler kazara yazılanlar, kaçırdığı biçim bilerek
  yazılması gerekendir — ama "açılış kapalı arızalanır" cümlesi bu değişmezden
  ÇIKMAZ. Kapsam `internal/arch/kayit_test.go`'da yazılıdır.
- **Yük testi süreç içidir** (`make load-test`, `internal/e2e`): doğruluğu yük
  altında sınar, kapasite planı üretmez.

## Geliştirme

```bash
make test              # birim testleri (race + coverage)
make test-integration  # gerçek Postgres ile entegrasyon + uçtan uca testler
make smoke             # gerçek ikiliyi açar, süreç davranışını sınar
make load-test         # temel yük testi (REQUESTS=… CONCURRENCY=… ile ayarlanır)
make lint              # golangci-lint
make fmt               # gofmt -s + go mod tidy
make build             # dağıtılabilir ikiliyi bin/gobit olarak derle
make psql              # çalışan Postgres'e psql ile bağlan
make logs              # compose servislerinin loglarını izle
make down              # altyapıyı durdur
```

**Smoke testleri** (`internal/smoke`) bir adım öteye gider: sunucu ikilisini
derleyip **süreç olarak** çalıştırır. Uçtan uca testler `httptest` ile router'ı
sürer, yani `main.go`'nun kablolamasını, açılıştaki migration'ları, config
yüklemesini ve sinyal işlemeyi ATLAR. Bu depoda testler geçerken uygulamayı
elle çalıştırıp bulunan dört arıza tam olarak orada saklanıyordu; smoke
testleri o sınıfı kalıcı olarak kapatır (eşzamanlı açılış yarışı, yanlış
yapılandırmaların açılışta durması, OTLP adres biçimleri, SIGTERM davranışı).

Uçtan uca testler (`internal/e2e`) modülleri **üretimdeki kablolamayla** kurar:
aynı container adları, aynı modül sırası ve aynı koruma yığını
(`corehttp.APIGuards`). Testin kanıtladığı koruma, üretimde çalışanın ta
kendisidir; testin kendi kopyası olsaydı üretimdeki sıra değiştiğinde test
eski sırayı doğrulayıp yeşil kalırdı.

CI (`.github/workflows/ci.yml`) her push ve PR'da `gofmt`, `go mod tidy`
farkı, `golangci-lint`, `go vet` ve race'li testleri çalıştırır.

## Modül yolunu değiştirme

Varsayılan modül yolu `github.com/bdrtr/gobit`. Kendi deponuza taşımak için:

```bash
make rename-module MODULE=github.com/kullanici/repo
```

## Sürüm

Güncel sürüm: **v0.5.0**. Değişiklikler için
[`CHANGELOG.md`](./CHANGELOG.md).

- **v0.1.0** — Faz 0–9'un tamamı.
- **v0.2.0** — yol haritası bittikten sonra bulunanlar: satış kanalı katalog
  süzmesi, çoklu depo, alan olayları ve ilk gerçek eklenti (arama).
- **v0.3.0** — API kendini anlatıyor: 196 uç şemada gövdeleriyle tanımlı,
  şemadan çalışan istemci üretilebiliyor.
- **v0.4.0** — Bölüm 10 (GraphQL vitrin yüzeyi, B2B harcama limiti) ve üç hata
  sınıfını yapısal olarak kapatan mimari değişmezler. Uygulamayı gerçekten
  çalıştırmak, sepeti siparişe çeviren yolun hiçbir kurulumda BAĞLI OLMADIĞINI
  ortaya çıkardı; iplik çekilince fiyat ve para birimi yetkisinin istemcide
  olduğu görüldü. **Mağaza API'sinde kırıcı değişiklikler var.**
- **v0.5.0** — depo seçimi bir POLİTİKA kazandı (kapsam bir kısıt, tercih bir
  sıra; [ADR 0010](docs/adr/0010-depo-secim-politikasi.md)) ve sepet açma
  akışa bağlandı: bölgeyi ve para birimini artık sunucu `country_code`'dan
  türetiyor. İki güven sınırı yazıya geçti — müşteri kimliği doğrulanmıyor
  ([ADR 0008](docs/adr/0008-musteri-kimligi-guven-siniri.md)), her kurulum tek
  kiracılıdır ([ADR 0009](docs/adr/0009-cok-kiracililik-kurulum-siniri.md)) —
  ve aynı avın bulduğu gerçek açık, satış kanalı kuralının yazma yolunda
  uygulanmaması, kapatıldı. Belgeler de denetime girdi: godoc bağları ve
  markdown atıfları çözülüyor (`TestGodocBaglariCozuluyor`,
  `TestBelgelerdekiAtiflarCozuluyor`), satır numarasıyla atıf yasak
  (`TestBelgelerdeSatirNumarasiAtfiYok`) ve para değişmezinin kör noktası
  kapandı (`TestParaTamSayidir`). **Mağaza API'sinde kırıcı değişiklikler var.**

`0.x` boyunca **kırıcı değişiklikler minor sürümlerde gelebilir**: API yüzeyi
henüz sabitlenmemiştir. Sabitlenme `1.0.0` ile olur.

## Faz durumu

| Faz | Kapsam | Durum |
|---|---|---|
| 0 | Proje iskeleti & tooling | ✅ |
| 1 | Çekirdek altyapı (errors, db, container, module, eventbus, http middleware) | ✅ |
| 2 | Module Links & Query | ✅ |
| 3 | Workflow Engine (saga) | ✅ |
| 4 | Katalog (product · pricing · inventory) | ✅ |
| 5 | Sepet (cart · customer · region) | ✅ |
| 6 | Ödeme & sipariş tamamlama | ✅ |
| 7 | Fulfillment · promotion · tax | ✅ |
| 8 | Auth · admin user · API key · RBAC | ✅ |
| 9 | Plugin sistemi · observability · sertleştirme | ✅ |
| 10 | GraphQL vitrin yüzeyi · B2B (şirket · çalışan · harcama limiti) | ✅ |
