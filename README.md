# gobit

Go tabanlı, modüler, headless commerce framework. Tek binary olarak çalışan bir
**modüler monolit**: modüller izole olduğu için herhangi biri ileride ayrı
servise çıkarılabilir.

Uygulama planının tamamı için: [`go-commerce-framework-plan.md`](./go-commerce-framework-plan.md)

**Mevcut durum: Faz 3 — Workflow Engine ✅**

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
| `core/http` | chi router, RequestID/RequestLogger/Recoverer/RequireAuth, `Kind`→status eşlemesi |
| `core/link` | Module Links — modüller arası ilişki FK olmadan; kardinalite veritabanı kısıtıyla zorlanır |
| `core/query` | Cross-module okuma — kök çek, link çöz, batch getir, birleştir; N+1 yapısal olarak yok |
| `core/workflow` | Saga motoru — ters sırada telafi, retry, idempotency-key, panik izolasyonu |
| `core/workflow/pgstore` | Yürütme durumunun Postgres deposu (`workflow_executions`) |

Event bus arka ucu `EVENT_BUS=inmemory|redis` ile seçilir. `redis` seçildiğinde
Redis erişilemezse uygulama açılışta durur.

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

ADR 0001, planın Bölüm 2.1 ("erişim public service interface üzerinden") ile
Bölüm 2.4 ("modüller derleme zamanında birbirine bağımlı olmaz") arasındaki
çelişkiyi çözer — Go'da interface'ler paketlerde yaşadığı için sağlayıcının
interface'ini import etmek 2.4'ü ihlal ederdi.

## Geliştirme

```bash
make test      # birim testleri (race + coverage)
make lint      # golangci-lint
make fmt       # gofmt -s + go mod tidy
make down      # altyapıyı durdur
```

CI (`.github/workflows/ci.yml`) her push ve PR'da `gofmt`, `go mod tidy`
farkı, `golangci-lint`, `go vet` ve race'li testleri çalıştırır.

## Modül yolunu değiştirme

Varsayılan modül yolu `github.com/bdrtr/gobit`. Kendi deponuza taşımak için:

```bash
make rename-module MODULE=github.com/kullanici/repo
```

## Faz durumu

| Faz | Kapsam | Durum |
|---|---|---|
| 0 | Proje iskeleti & tooling | ✅ |
| 1 | Çekirdek altyapı (errors, db, container, module, eventbus, http middleware) | ✅ |
| 2 | Module Links & Query | ✅ |
| 3 | Workflow Engine (saga) | ✅ |
| 4 | Katalog (product · pricing · inventory) | ⬜ |
| 5 | Sepet (cart · customer · region) | ⬜ |
| 6 | Ödeme & sipariş tamamlama | ⬜ |
| 7 | Fulfillment · promotion · tax | ⬜ |
| 8 | Auth · admin user · API key · RBAC | ⬜ |
| 9 | Plugin sistemi · observability · sertleştirme | ⬜ |
