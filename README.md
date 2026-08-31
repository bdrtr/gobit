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
# {"status":"ok","version":"dev"}
curl -s localhost:9000/ready
# {"status":"ok","version":"dev","checks":{"postgres":{"status":"ok"}}}
```

`/health` yalnızca sürecin canlı olduğunu bildirir (liveness) ve bağımlılıkları
sınamaz — geçici bir veritabanı kesintisi sürecin öldürülmesine yol açmamalıdır.
`/ready` bağımlılıkları sınar ve biri düşükse **503** döner (readiness).

Tüm hedefler için `make help`.

## Gereksinimler

| Araç | Sürüm |
|---|---|
| Go | 1.26+ |
| Docker + Compose | v2+ |
| make | GNU Make |

`make tools`, sabitlenmiş sürümlerle `golangci-lint` ve `sqlc`'yi `./bin` altına kurar.

## Yapılandırma

Tüm ayarlar ortam değişkeninden okunur (12-factor). Varsayılanlar
`deploy/docker-compose.yml` ile uyumludur, bu yüzden yerelde `.env` gerekmez.

`cp .env.example .env` ile özelleştirebilirsiniz. Değişken listesi için
[`.env.example`](./.env.example) veya `internal/core/config/config.go`.

> **Üretim koruması:** `DATABASE_URL` ve `REDIS_URL` varsayılanları yalnızca
> yerel geliştirme içindir. `APP_ENV=production` iken bu ikisi ezilmemişse
> uygulama **açılışta hata verip durur** — eksik secret enjeksiyonunun
> sabit-kodlu kimlik bilgisiyle sessizce üretime çıkmasını engeller.

> **`.env` biçimi:** Dosya POSIX kabuk semantiğiyle yüklenir. İçinde `$` geçen
> değerleri **tek tırnağa alın** (`REDIS_URL='redis://:pa$word@…'`), aksi hâlde
> kabuk onları genişletir.

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

sqlc, golang-migrate ve samber/do **Faz 1–4 arasında** devreye girer; Faz 0
yalnızca iskeleti kurar.

## Mimari kurallar lint ile zorlanır

Planın Bölüm 2'deki değişmez kuralları `.golangci.yml` içinde `depguard` ile
derleme öncesi denetlenir:

- **Prensip 2.4** — `internal/core/**` içinden `internal/modules/**` import edilemez.
- **Prensip 2.1 / 2.4** — Hiçbir modül başka bir modülü import edemez
  (12 modül × 11 yasak = tam izolasyon). Modüller arası erişim container'dan
  çözülen servis interface'i üzerinden yapılır.

Yeni modül eklerken `.golangci.yml` içindeki `depguard.rules` listesini de
güncelleyin.

## Çekirdek paketler

| Paket | Sorumluluk |
|---|---|
| `core/config` | env tabanlı 12-factor config + doğrulama, üretim koruması |
| `core/logger` | slog JSON/text handler |
| `core/errors` | Tipli hatalar (`Kind`), stdlib `errors` yardımcılarını yeniden dışa verir |
| `core/db` | pgxpool havuzu + modül başına ayrı versiyon tablolu migration runner |
| `core/container` | İsimli kayıt, generic `Resolve[T]`, tembel singleton, döngü tespiti, ters sırada kapatma |
| `core/module` | `Module` sözleşmesi + `ModuleRegistry` (register → migrate → routes) |
| `core/eventbus` | `EventBus` + InMemory (dev) ve Redis Streams (prod, consumer group + XACK) |
| `core/http` | chi router, RequestID/RequestLogger/Recoverer/Telemetry, RequireAdmin/RequireStore/RequireScope, `Scoped`/`APIGuards` koruma yığını, hız sınırı, idempotency, `Kind`→status eşlemesi |
| `core/link` | Module Links — modüller arası ilişki FK olmadan; kardinalite veritabanı kısıtıyla zorlanır |
| `core/query` | Cross-module okuma — kök çek, link çöz, batch getir, birleştir; N+1 yapısal olarak yok |
| `core/workflow` | Saga motoru — ters sırada telafi, retry, idempotency-key, panik izolasyonu |
| `core/workflow/pgstore` | Yürütme durumunun Postgres deposu (`workflow_executions`) |
| `workflows/cart` | Sepet akışları: create_cart, add_line_item, update_line_item, calculate_totals |
| `workflows/checkout` | `complete_cart` saga: stok ayır → sipariş → yetkilendir → tahsil et → sepeti kapat |
| `core/provider` | Ödeme/kargo sağlayıcı sözleşmeleri (plan Bölüm 5.6) |
| `core/plugin` | Eklenti sözleşmesi + iki fazlı kurulum (`Install` → modüller → `Start`) |
| `core/observability` | OpenTelemetry trace + metrik kurulumu; toplayıcı yoksa gerçekten kapalı |
| `core/openapi` | Router ağacından OpenAPI şeması üretimi (`/openapi.json`) |

Event bus arka ucu `EVENT_BUS=inmemory|redis` ile seçilir. `redis` seçildiğinde
Redis erişilemezse uygulama açılışta durur.

## API güvenliği

İki yüzey, iki kimlik. Koruma modüllerde değil, **router'ı kuran tarafta**
(`cmd/server`) takılır: modüller route'larını tam yolla düz bir router'a
kaydeder, kapsamlama `corehttp.Scoped` ile yapılır ve sıra tek bir yerde,
`corehttp.APIGuards` içinde yazılıdır.

| Yüzey | Kimlik | Başlık |
|---|---|---|
| `/admin/v1/**` | Oturum jetonu (HS256 JWT) **ya da** gizli anahtar (`sk_…`) | `Authorization: Bearer …` |
| `/store/v1/**` | Publishable anahtar (`pk_…`) | `x-publishable-api-key: …` |
| `/health`, `/ready`, `/openapi.json` | yok | — |

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
   birlikte tutulur.

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

# 3) Çıkış (çağıranın TÜM oturumlarını düşürür)
curl -s -X POST localhost:9000/admin/v1/auth/logout -H "Authorization: Bearer $TOKEN"

# 4) Mağaza yüzeyi
curl -s localhost:9000/store/v1/products -H "x-publishable-api-key: pk_…"
```

**İlk yönetici** bir tohum adımıyla doğar: yönetim uçları korumalı olduğu için
ilk kullanıcıyı HTTP'den yaratmanın yolu yoktur. Tohum **yalnızca hiç kullanıcı
yokken** çalışır — yeniden başlatma güvenlidir ve var olan bir kurulumun
yetkilerini asla değiştirmez. İki değişken **birlikte** verilir; yalnızca biri
verilirse uygulama açılışta durur.

**Oturum iptali** iki yoldan olur ve ikisi de **toptan**dır: parola değişimi ve
`POST /admin/v1/auth/logout`. Tek bir cihazı düşürmek yoktur — bunun için jti
bazlı, her istekte okunan bir kara liste gerekirdi. API anahtarının oturumu
yoktur; o `POST /admin/v1/api-keys/{id}/revoke` ile kapatılır.

`JWT_SECRET` verilmezse geliştirmede **açılışa özel rastgele** bir sır
üretilir (yeniden başlatmada oturumlar düşer) ve uyarı loglanır; paylaşılan
ortamlarda config doğrulaması sırrı zorunlu kılar.

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

`TRUSTED_PROXY_HOPS`, istekle aramızdaki **güvenilen** ters proxy sayısıdır.
Fazla verilen bir değer, istemcinin `X-Forwarded-For`'a kendi yazdığı adresi
gerçek sanmaya ve hız sınırının IP uydurularak atlanmasına yol açar; doğrudan
internete bakan bir kurulumda `0` bırakın.

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
| `paymentstripe` | **iskelet** — kayıt ve yaşam döngüsü tam çalışır, Stripe API çağrıları yapılmamıştır ve para hareketi üreten her metod açık bir "uygulanmadı" hatası döner | sağlayıcı kaydı |
| `searchpg` | **gerçek özellik** — ürün olaylarını dinler, PostgreSQL tam metin indeksini taze tutar, `GET /store/v1/search` ve `POST /admin/v1/search/reindex` uçlarını açar | kendi modülü + migration'ı, olay aboneliği, kendi route'ları |

```bash
PLUGINS=searchpg make run
curl -s 'localhost:9000/store/v1/search?q=tişört' -H "x-publishable-api-key: pk_…"
```

Arama motoru bilinçli olarak **dış bir servis değildir**: PostgreSQL tam metin
araması, yeni bir bağımlılık ve yeni bir compose servisi getirmeden gerçek bir
özellik verir; eklenti sınırı sayesinde ileride Meilisearch/OpenSearch'e geçmek
başka hiçbir yeri değiştirmez.

> **Arama, kanal süzmesinin bypass'ı değildir.** Eklenti yalnızca ürün
> *kimliklerini* indeksler; kayıtları `product.interop` getirir ve görünürlük
> kuralı tek yerde kalır. Kuralı eklentide tekrar etmek, biri değiştiğinde
> vitrin ile aramanın sessizce ayrışması demek olurdu.

## Alan olayları

Modüller kendi alan olaylarını event bus'a yayımlar; aboneler (eklentiler,
entegrasyonlar) onları dinler.

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

ADR 0001, planın Bölüm 2.1 ("erişim public service interface üzerinden") ile
Bölüm 2.4 ("modüller derleme zamanında birbirine bağımlı olmaz") arasındaki
çelişkiyi çözer — Go'da interface'ler paketlerde yaşadığı için sağlayıcının
interface'ini import etmek 2.4'ü ihlal ederdi.

## Geliştirme

```bash
make test              # birim testleri (race + coverage)
make test-integration  # gerçek Postgres ile entegrasyon + uçtan uca testler
make load-test         # temel yük testi (REQUESTS=… CONCURRENCY=… ile ayarlanır)
make lint              # golangci-lint
make fmt               # gofmt -s + go mod tidy
make down              # altyapıyı durdur
```

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

Güncel sürüm: **v0.2.0**. Değişiklikler için
[`CHANGELOG.md`](./CHANGELOG.md).

- **v0.1.0** — Faz 0–9'un tamamı.
- **v0.2.0** — yol haritası bittikten sonra bulunanlar: satış kanalı katalog
  süzmesi, çoklu depo, alan olayları ve ilk gerçek eklenti (arama).

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
