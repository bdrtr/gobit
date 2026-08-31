# Go Headless Commerce Framework — Uygulama Planı

> **Bu doküman bir AI kod ajanı için yazılmıştır.** Amacı, Medusa.js'e benzer; modüler, headless,
> Go tabanlı bir e-ticaret altyapısını **faz faz** inşa ettirmektir. Aşağıdaki fazları **sırayla**
> uygula. Her fazın sonunda "Definition of Done" (DoD) kriterlerini sağla, testleri yaz ve commit at.
> Bir sonraki faza geçmeden önceki fazın DoD'sini tamamla.

---

## 0. AI Ajanı İçin Çalışma Talimatları

1. Fazları **sırayla** uygula; faz atlama. Her faz bir öncekinin üstüne kurulur.
2. Bölüm 2'deki **Mimari Prensipleri asla ihlal etme** (özellikle modül izolasyonu ve cross-module FK yasağı).
3. Her core bileşen ve modül için **birim + entegrasyon testi** yaz. Entegrasyon testlerinde `testcontainers-go` kullan.
4. Her fazın sonunda `make lint`, `make test` temiz geçmeli; ardından anlamlı bir commit at.
5. Bir kararı netleştirmen gerekirse, varsayılanı bu dokümandan al; doküman sessizse en sade, en idiomatik Go çözümünü seç ve kararı kod yorumunda belgele. `docs/adr/` altındaki kararlar bu doküman kadar bağlayıcıdır; çelişki hâlinde **ADR geçerlidir** (daha sonra alınmıştır). Mimari bir belirsizlik çözdüğünde yeni bir ADR yaz.
6. Üretilen her public fonksiyon/interface için kısa godoc yorumu yaz.

---

## 1. Proje Özeti

**Hedef:** Geliştiricilerin üstüne kendi iş akışlarını ekleyebileceği, hazır commerce modülleri sunan bir framework. Tek binary olarak çalışır (modüler monolit), ama modüller izole olduğu için herhangi biri ileride ayrı servise çıkarılabilir.

**Temel yetenekler:**
- İzole commerce modülleri (product, pricing, inventory, cart, order, payment, fulfillment, customer, promotion, tax …)
- Modüller arası ilişki için **Module Links**
- Birden çok modülden tek seferde okuma için **Query** katmanı
- Modüller arası işlemleri yürüten, geri alma (saga/compensation) destekli **Workflow Engine**
- Asenkron yan etkiler için **Event Bus**
- Ödeme/kargo/bildirim/dosya için **Provider** soyutlaması
- **Store API** (müşteri) + **Admin API** (yönetim)
- Plugin sistemi (modül, subscriber, workflow, route, provider eklenebilir)

**Non-goals (ilk sürümde yok):** Storefront UI, admin panel UI, GraphQL (REST ile başla), çoklu-tenant. Bunlar sonraki sürümlere bırakılır.

---

## 2. Mimari Prensipler (DEĞİŞMEZ KURALLAR)

1. **Modül izolasyonu:** Bir modül başka bir modülün repository'sine, modeline veya tablosuna **doğrudan erişemez**. Erişim yalnızca bir **servis interface'i** üzerinden olur; bu interface'i **tüketen modül kendi paketinde** tanımlar, sağlayıcı modül import edilmez (bkz. [ADR 0001](docs/adr/0001-modul-arasi-iletisim.md)).
2. **Cross-module FK yok:** Farklı modüllerin tabloları arasında foreign key **kurulmaz**. İlişki yalnızca **Module Links** ile kurulur.
3. **Veri sahipliği:** Her veri tam olarak bir modüle aittir. O modül o verinin tek yazma yetkilisidir.
4. **Bağımlılık yönü:** Core, modülleri tanımaz; modüller core'a bağımlıdır. Modüller birbirini **hiçbir koşulda import etmez** — ortak bir interface/sözleşme paketi de yoktur. Tüketici, ihtiyaç duyduğu **dar** interface'i kendi paketinde tanımlar; sağlayıcının somut tipi bunu yapısal olarak karşılar ve container'dan **isimle** çözülür (bkz. [ADR 0001](docs/adr/0001-modul-arasi-iletisim.md)). Bu kural `.golangci.yml` içindeki `depguard` ile CI'da zorlanır.
5. **İş mantığı modülde, orkestrasyon workflow'da:** Tek modüle ait kural modül servisinde; birden çok modüle dokunan akış workflow'da yazılır.
6. **Idempotency:** Dışarıya açık state-değiştiren işlemler (ödeme, sipariş) idempotency-key ile korunur.
7. **Açık hata tipleri:** Hatalar `core/errors` içindeki tipli hatalarla (`NotFound`, `Invalid`, `Conflict`, `Unauthorized`) döner; HTTP katmanı bunları status koda çevirir.

---

## 3. Teknoloji Stack'i

| Alan | Seçim | Not |
|---|---|---|
| Dil | Go 1.26+ | `go.mod` sürümü referans alınır |
| HTTP router | `chi` | Hafif, idiomatic, middleware dostu |
| Veritabanı | PostgreSQL 16+ | |
| DB erişim | **`sqlc` + `pgx/v5`** | Modül başına ayrı codegen paketi; ent'in FK/graph modeli modül izolasyonuyla çelişiyor |
| Migration | **`golang-migrate`** | `Module.Migrations() fs.FS` ile birebir uyum; modül başına `x-migrations-table` |
| DI / container | **El yazması** (`internal/core/container`) | Bölüm 5.1 sözleşmesi `Provide(name, ctor any)` istiyor; samber/do tip parametreli olduğu için `any`'ye düzleşiyor ve teşhis/conflict/kapatma sırası elden gidiyor — bkz. [ADR 0002](docs/adr/0002-di-container-el-yazmasi.md) |
| Event bus | In-memory (dev) + Redis Streams (prod) | Arayüz tek, backend pluggable |
| Cache / kuyruk | Redis | İş kuyruğu için `hibiken/asynq` opsiyonel |
| Workflow | Custom saga engine | İstenirse ileride Temporal'a köprü |
| Config | **`caarlos0/env`** | 12-factor; viper'ın dosya/uzak config yükü gereksiz |
| Validation | `go-playground/validator` | |
| Auth | JWT + session; OAuth opsiyonel | |
| Loglama | `log/slog` (stdlib) | Structured, JSON |
| Observability | OpenTelemetry (trace + metric) | |
| Test | stdlib `testing` + `testify` + `testcontainers-go` | |
| Lint | `golangci-lint` | |

---

## 4. Dizin Yapısı

```
/cmd
  /server                  # main: config yükle, container kur, modülleri register et, API mount et
/internal
  /core
    /config                # config loader (env)
    /errors                # tipli hatalar
    /logger                # slog kurulumu
    /db                    # postgres bağlantı yönetimi, migration runner
    /container             # DI + ModuleRegistry
    /module                # Module interface + lifecycle
    /eventbus              # EventBus interface + inmemory/redis backend
    /link                  # Module Links servisi
    /query                 # cross-module query resolver
    /workflow              # workflow engine (Step, Workflow, Executor, saga, state)
    /provider              # provider interface'leri (payment, fulfillment, notification, file)
    /http                  # router, middleware, response/error helper, auth middleware
  /modules
    /product
      /models              # domain modelleri
      /repository          # SADECE bu modülün tabloları
      /service             # bu modülün servisi + DIŞARIDAN ihtiyaç duyduğu dar interface'ler (ADR 0001)
      /migrations          # bu modülün migration'ları
      /api                 # store + admin route'ları
      module.go            # Module interface implementasyonu (register)
    /pricing  /inventory  /cart  /order  /payment
    /fulfillment  /customer  /promotion  /tax  /region  /auth
  /workflows               # cross-module workflow'lar (complete_cart, create_order, …)
/plugins
  /payment-stripe          # örnek provider plugin
/migrations                # global/çekirdek migration'lar (links tablosu vb.)
/config                    # ortam config dosyaları
/deploy                    # docker-compose, Dockerfile
Makefile
go.mod
```

---

## 5. Çekirdek (Core) Sözleşmeleri

AI bunları birebir bu imzalarla (gerekirse genişleterek) implemente etmeli.

### 5.1 Module & Registry

```go
// internal/core/module/module.go
type Module interface {
    Name() string
    // Register: servisleri container'a kaydeder, link tanımlarını ve event subscriber'ları bildirir.
    Register(ctx context.Context, c *container.Container) error
    // Migrations: bu modülün migration kaynak yolunu döner.
    Migrations() fs.FS
    // Routes: bu modülün store/admin route'larını router'a bağlar.
    Routes(r chi.Router)
}
```

```go
// internal/core/container/container.go
type Container struct { /* DI wrapper */ }

func (c *Container) Provide(name string, ctor any) error
func Resolve[T any](c *Container, name string) (T, error)

// ModuleRegistry: tüm modülleri tutar, sırayla Register/Migrate/Routes çağırır.
type ModuleRegistry struct { /* ... */ }
func (m *ModuleRegistry) Add(mod module.Module)
func (m *ModuleRegistry) Bootstrap(ctx context.Context, c *Container) error
```

### 5.2 Module Links

```go
// internal/core/link/link.go
type LinkDefinition struct {
    Name      string // örn. "product_price"
    From      LinkSide // {Module: "product", Field: "product_id"}
    To        LinkSide // {Module: "pricing", Field: "price_set_id"}
    Cardinality Cardinality // OneToOne | OneToMany | ManyToMany
}

type LinkService interface {
    Define(def LinkDefinition) error          // link tablosunu oluşturur/migrate eder
    Create(ctx context.Context, name string, fromID, toID string) error
    Delete(ctx context.Context, name string, fromID, toID string) error
    List(ctx context.Context, name string, fromID string) ([]string, error)
}
```

### 5.3 Query (cross-module okuma)

```go
// internal/core/query/query.go
// Modüllerden veriyi alıp link'ler üzerinden birleştirir.
type Query interface {
    Graph(ctx context.Context, spec GraphSpec) ([]map[string]any, error)
}
// GraphSpec: kök entity, seçilecek alanlar ve link üzerinden genişletmeler (expand).
```
> İlk sürümde basit tut: kök modülden kayıtları çek → link'lerle ilgili ID'leri bul →
> ilgili modüllerin servislerinden batch ile getir → birleştir. Sonra graph resolver'a evrilebilir.

### 5.4 Event Bus

```go
// internal/core/eventbus/eventbus.go
type Event struct {
    Name string            // "order.placed"
    Data map[string]any
}
type Handler func(ctx context.Context, e Event) error

type EventBus interface {
    Publish(ctx context.Context, e Event) error   // handler'ları BEKLEMEZ
    Subscribe(eventName string, h Handler) error
    Shutdown(ctx context.Context) error           // çalışan handler'ları ctx sınırında bekler
}
// Backendler: InMemoryBus (dev), RedisStreamBus (prod). Aynı interface.
```

### 5.5 Workflow Engine (saga)

```go
// internal/core/workflow/workflow.go
type StepContext struct {
    Input  any
    Shared map[string]any // adımlar arası veri
}

type Step interface {
    Name() string
    Invoke(ctx context.Context, sc *StepContext) (output any, err error)
    Compensate(ctx context.Context, sc *StepContext) error // Invoke'un geri alımı
}

type Workflow struct {
    Name  string
    Steps []Step
}

type Executor interface {
    // Run: adımları sırayla çalıştırır. Bir adım patlarsa, o ana kadar başarılı
    // adımların Compensate'lerini TERS sırada çalıştırır. Yürütme durumunu persist eder (retry için).
    Run(ctx context.Context, wf Workflow, input any) (any, error)
}
```
> Gereksinimler: ardışık + (opsiyonel) paralel adım, compensation ters sırada, state persistence
> (`workflow_executions` tablosu), retry, idempotency-key desteği.

### 5.6 Provider Soyutlamaları

```go
// internal/core/provider/payment.go
type PaymentProvider interface {
    ID() string
    CreateSession(ctx context.Context, in CreateSessionInput) (Session, error)
    Authorize(ctx context.Context, sessionID string) (AuthResult, error)
    Capture(ctx context.Context, sessionID string, amount int64) error
    Refund(ctx context.Context, sessionID string, amount int64) error
}
// Benzer şekilde: FulfillmentProvider, NotificationProvider, FileProvider.
// Pluginler bu interface'leri implemente edip registry'ye kaydeder.
```

---

## 6. Commerce Modülleri (sorumluluk + temel modeller)

> Modeller temsilîdir; AI gerektikçe alan ekleyebilir. Her modül kendi tablolarına sahiptir.

- **product** — Katalog. `Product, ProductVariant, ProductOption, ProductOptionValue, ProductCategory, ProductCollection, ProductTag, ProductImage`.
- **pricing** — Fiyatlandırma. `PriceSet, Price, PriceList, PriceRule` (para birimi/segment kurallarıyla).
- **inventory** — Stok. `InventoryItem, InventoryLevel, StockLocation, Reservation`.
- **cart** — Sepet. `Cart, LineItem, ShippingMethod, CartAddress`.
- **order** — Sipariş. `Order, OrderLineItem, OrderSummary, Return, Exchange, Claim`.
- **payment** — Ödeme. `PaymentCollection, PaymentSession, Payment, Refund` (+ PaymentProvider'lar).
- **fulfillment** — Kargo/teslimat. `Fulfillment, ShippingOption, ShippingProfile` (+ FulfillmentProvider'lar).
- **customer** — Müşteri. `Customer, CustomerGroup, CustomerAddress`.
- **promotion** — Promosyon. `Promotion, Campaign, PromotionRule, ApplicationMethod`.
- **tax** — Vergi. `TaxRegion, TaxRate, TaxProvider`.
- **region** — Bölge/para birimi. `Region, Currency, Country`.
- **auth** — Kimlik. `User (admin), AuthIdentity, ApiKey (publishable/secret), SalesChannel`.

**Önemli linkler:**
`product↔pricing` (variant→price_set), `product↔inventory` (variant→inventory_item),
`cart↔customer`, `cart↔region`, `order↔customer`, `order↔payment`, `order↔fulfillment`,
`product↔sales_channel`.

---

## 7. Geliştirme Fazları (Roadmap)

### Faz 0 — Proje İskeleti & Tooling
**Yapılacaklar:** go.mod, dizin yapısı, `Makefile` (run/test/lint/migrate/up/down), `deploy/docker-compose.yml` (Postgres + Redis), config loader, `slog` kurulumu, `golangci-lint` config, basit `/health` endpoint'i, CI (lint+test).
**DoD:** `make up` Postgres+Redis'i ayağa kaldırır; `make run` boş server'ı başlatır; `GET /health` `200` döner; `make lint && make test` temiz geçer.

### Faz 1 — Çekirdek Altyapı
**Yapılacaklar:** `core/errors`, `core/db` (bağlantı + migration runner), `core/container` + `ModuleRegistry`, `core/module` interface, `core/eventbus` (InMemory + Redis), `core/http` (router, request-id, recover, logging, error→status middleware, auth stub). Bir `dummy` modül ile registry akışını doğrula.
**DoD:** Dummy modül register edilip servisi container'dan resolve edilebiliyor; `eventbus.Publish/Subscribe` in-memory ve Redis backend'de testle çalışıyor; migration runner dummy migration'ı uyguluyor.
> **Tamamlandı.** Ek olarak: `/ready` bağımlılık kontrolü, `EVENT_BUS` ile backend seçimi, migration'da gerçek iptal ([ADR 0003](docs/adr/0003-migration-iptali.md)), el yazması container ([ADR 0002](docs/adr/0002-di-container-el-yazmasi.md)).

### Faz 2 — Module Links & Query
**Yapılacaklar:** `core/link` (LinkDefinition, link tablosu oluşturma, CRUD), `core/query` (basit resolver: kök çek → link çöz → batch getir → birleştir). İki dummy modül ile uçtan uca doğrula.
**DoD:** İki modül arasında link tanımlanıp kayıt bağlanabiliyor; `query.Graph` ile birleşik veri (kök + expand) dönüyor; testlerle kanıtlı.
> **Tamamlandı.** Ek olarak: ters yön çözümü (`ListManyByTo`), iç içe genişletme, N+1'in yapısal engellenmesi, ve şema doğrulaması ([ADR 0004](docs/adr/0004-query-veri-erisimi.md), [ADR 0005](docs/adr/0005-link-semasi-migration-disinda.md)).

### Faz 3 — Workflow Engine
**Yapılacaklar:** `Step`, `Workflow`, `Executor`; ardışık yürütme, ters sırada compensation, `workflow_executions` ile state persistence, retry, idempotency-key.
**DoD:** 3 adımlı örnek workflow; ortadaki adım hata verince önceki adımların `Compensate`'leri **ters sırada** çağrılıyor (test ile doğrulanmış); başarılı koşuda state `completed` olarak persist ediliyor.
> **Tamamlandı.** Not: 3 adımlı senaryo ters sırayı KANITLAYAMAZ — ortadaki adım
> patlayınca telafi edilecek tek adım kalır ve tek elemanlı dizinin ileri/ters
> sırası aynıdır. Sıra iddiası `TestCompensationOrderWithFiveSteps` ile
> kanıtlanmıştır (mutasyonla doğrulandı). Ek olarak: retry, idempotency-key,
> panik izolasyonu, eşzamanlı bileşik adım, ve retry'lanan adımın en iyi çaba
> telafisi.

### Faz 4 — Katalog Modülleri (Product · Pricing · Inventory)
**Yapılacaklar:** Üç modülün modelleri, migration'ları, servisleri, store+admin API'leri. `product↔pricing` ve `product↔inventory` linkleri. Admin API: ürün/varyant/fiyat/stok oluşturma. Store API: ürün listeleme (fiyat + stok ile, `query` üzerinden).
**DoD:** Admin API'den ürün + varyant + fiyat + stok seviyesi oluşturulabiliyor; Store API ürünleri fiyat ve stok bilgisiyle birlikte listeliyor; entegrasyon testi yeşil.
> **Tamamlandı.** Modül başına entegrasyon testleri gerçek Postgres üzerinde
> koşar; katalog listesi fiyat ve stoğu `core/query` üzerinden çeker, yani
> modüller birbirini import etmeden birleşik veri döner.

### Faz 5 — Sepet Akışı (Cart · Customer · Region)
**Yapılacaklar:** Cart, Customer, Region/Currency modülleri. Cart workflow'ları: `create_cart`, `add_line_item`, `update_line_item`, `calculate_totals` (fiyatı pricing'den, vergiyi tax stub'ından alır). `cart↔customer`, `cart↔region` linkleri.
**DoD:** Sepet oluştur → ürün ekle → adet güncelle → ara toplam/indirim/vergi/genel toplam doğru hesaplanıyor; misafir ve kayıtlı müşteri senaryoları test edilmiş.
> **Tamamlandı.** DoD `internal/e2e` altında GERÇEK modüllerle ve gerçek Postgres'le doğrulandı (8 senaryo). Ek olarak: [ADR 0006](docs/adr/0006-workflow-modul-erisimi.md) ile workflow→modül erişimi karara bağlandı, `cart` modülü ilkel `interop.go` yüzeyi yayımlıyor, indirim ara toplamla sınırlandı ve kargo vergi tabanına girmiyor.

### Faz 6 — Ödeme & Sipariş Tamamlama
**Yapılacaklar:** Payment modülü + `PaymentProvider` soyutlaması + `manual/test` provider. Order modülü. `complete_cart` workflow'u (saga): `reserve_inventory → create_order → authorize/capture_payment → clear_cart`. Hata durumunda compensation: rezervasyonu geri al, ödemeyi iptal et, siparişi iptal et.
**DoD:** Uçtan uca sepet→sipariş akışı test provider ile çalışıyor; ödeme adımı başarısızken **stok rezervasyonu ve sipariş geri alınıyor** (saga testi); `order.placed` eventi yayınlanıyor.
> **Tamamlandı.** DoD `internal/e2e` altında GERÇEK modüller, gerçek Postgres ve pgstore üzerindeki saga motoruyla doğrulandı. Not: `capture_payment` bir PİVOTTUR — tahsilat denendikten sonra geri alma yapılmaz; belirsiz tahsilatın kalan riski ve mutabakat ihtiyacı `internal/workflows/checkout/doc.go`'da belgelidir.

### Faz 7 — Fulfillment · Promotion · Tax
**Yapılacaklar:** Fulfillment modülü + `FulfillmentProvider` soyutlaması (+ manual provider), shipping option'lar. Promotion modülü (indirim kuralları, kampanya) ve cart/order toplamına uygulanması. Tax modülü gerçek hesaplama (region bazlı rate).
**DoD:** Siparişe fulfillment oluşturulabiliyor; sepete indirim uygulanıp toplam doğru güncelleniyor; vergi region'a göre hesaplanıyor.
> **Tamamlandı.** İki geçici çözüm devralındı: indirim artık `promotion.interop`'tan (önceden DAİMA sıfırdı), vergi `tax.interop`'tan (önceden region alanından). Region'ın vergi alanı KALDIRILMADI, bilinçli GERİ DÜŞÜŞ yolu oldu ve hangi kaynağın konuştuğu `Totals.TaxSource` ile raporlanıyor.

### Faz 8 — Auth · Admin User · API Key · RBAC
**Yapılacaklar:** Auth modülü: admin user, JWT login, publishable/secret API key, sales channel. HTTP'de gerçek auth middleware (admin route'ları korumalı, store route'ları publishable key ile).
**DoD:** Yetkisiz istek `401`; admin login → token ile korumalı endpoint'e erişim; publishable key olmadan store API erişimi reddediliyor.
> **Tamamlandı.** DoD `internal/e2e/kimlik_test.go` altında GERÇEK modüller,
> gerçek Postgres ve ÜRETİMDEKİ koruma yığınıyla doğrulandı. Kritik ayrım:
> koruma modülde değil, router'ı kuran tarafta takılır — modüller route'larını
> tam yolla düz bir router'a kaydettiği için chi'nin doğal kapsamlama aracı
> (Route/Group) kullanılamaz ve kapsam `corehttp.Scoped` ile middleware'in
> kendi içinde kurulur. Sıra tek bir yerde (`corehttp.APIGuards`) yazılıdır ve
> uçtan uca testler o yığının TA KENDİSİNİ kurar. Tanımsız bir `/admin/v1/...`
> yolu da 401 döner: 404 olsaydı uç haritası status kodundan sızardı.
> Kimlik doğrulayıcı router'dan SONRA doğduğu için `corehttp.DeferredAuthenticator`
> ile bağlanır; bağlanmadan gelen istek reddedilir (ADR 0007).
>
> RBAC yalnızca auth'ta değil, **TÜM modüllerde** zorlanır: sözlük tek
> kuraldan türer (`<modül>:read` / `<modül>:write`, `admin` üst yetki) ve
> `internal/e2e/yetki_test.go` router ağacını GEZEREK 183 yönetim ucunun
> tamamında yetkisiz bir jetonun 403 aldığını denetler — elle yazılmış bir uç
> listesi, eklenmesi unutulan ilk uçta kör kalırdı.
>
> İlk yönetici bir TOHUM adımıyla doğar (`ADMIN_BOOTSTRAP_*`) ve tohum yalnızca
> hiç kullanıcı yokken çalışır; bu adım olmadan yönetim uçları korumalı olduğu
> için taze bir kurulum kullanılamaz durumdaydı. Oturum iptali iki yoldan olur
> ve ikisi de TOPTANDIR: parola değişimi ve `POST /admin/v1/auth/logout`.

### Faz 9 — Plugin Sistemi · Observability · Sertleştirme
**Yapılacaklar:** Plugin yükleme mekanizması (modül/subscriber/route/provider register edebilen); örnek `payment-stripe` plugin iskeleti; OpenTelemetry trace+metric; rate limiting; idempotency middleware; OpenAPI/Swagger üretimi; README + mimari dokümanı.
**DoD:** Örnek payment provider plugin'i çekirdeğe dokunmadan takılıp seçilebiliyor; trace'ler dışa aktarılıyor; OpenAPI şeması üretiliyor; temel yük testi geçiyor.
> **Tamamlandı.** Eklenti mekanizması DERLEME ZAMANI kaydıdır; Go'nun `plugin`
> paketi (.so) bilinçli olarak kullanılmadı (yalnızca Linux/macOS, çapraz
> derleme yok, tüm bağımlılıkların bit düzeyinde aynı sürümde derlenmiş olması
> şartı). "Çekirdeğe dokunmadan" ölçütü şöyle karşılanır: eklenti eklemek
> `cmd/server` katalog haritasına bir satır ekler, çekirdek ve modüller
> DEĞİŞMEZ; hangisinin kurulacağını `PLUGINS` seçer.
> Kurulum iki fazlıdır (`Install` → modüller → `Start`) çünkü sağlayıcı kaydı
> ancak payment modülü ayağa kalkınca vardır.
> Arıza davranışı [ADR 0007](docs/adr/0007-sertlestirme-arizada-davranis.md)
> ile karara bağlandı: kimlik fail-closed, hız sınırı fail-open, idempotency
> ayırmada reddeder / kayıtta anahtarı serbest bırakır. Tek tip kural YOKTUR.
> Yük testi süreç içidir (`internal/e2e/yuk_test.go`): ölçtüğü şey mutlak
> performans değil, eşzamanlı yük altında DOĞRULUK — düşen istek, 5xx ve
> koruma yığınındaki yarış. Kapasite planı için bir sayı üretmez.
>
> Hız sınırı ve idempotency deposu `GUARD_BACKEND` ile seçilir. `memory`
> (varsayılan) tek süreçliktir; `redis` (`internal/core/http/redisguard`)
> paylaşılandır. Fark derece değil TÜR farkıdır: sınırın gevşemesi bir hız
> sorunudur, idempotency'nin çalışmaması bir DOĞRULUK sorunudur — aynı
> anahtarla farklı örneklere düşen iki istek iki sipariş üretir. Paylaşılan bir
> ortamda `memory` bırakmak açılışta uyarı üretir.

---

## 8. Konvansiyonlar

- **Hatalar:** Servisler `core/errors` tipli hatalarını döner; HTTP katmanı map eder (`NotFound→404`, `Invalid→422`, `Conflict→409`, `Unauthorized→401`).
- **ID'ler:** Prefix'li ULID/KSUID (örn. `prod_…`, `cart_…`, `order_…`).
- **Para:** Tam sayı **minor unit** (kuruş/cent) olarak sakla; float kullanma. Para birimi ayrı alan.
- **Zaman:** UTC, `created_at/updated_at/deleted_at` (soft delete `deleted_at` ile).
- **Migration:** Modül başına ayrı klasör; geri-alınabilir (up/down). Cross-module FK yok.
- **API:** Versiyonlu (`/store/v1`, `/admin/v1`); cursor-based pagination; tutarlı zarf (`{ data, count, offset, limit }`).
- **Test:** Birim testleri servis seviyesinde; entegrasyon testleri gerçek Postgres/Redis ile (`testcontainers-go`); her workflow için saga (hata + compensation) testi zorunlu.
- **Context:** Tüm servis/repository metodları `context.Context` alır.
- **Loglama:** Yapısal (`slog`), her isteğe `request_id`, hassas veri loglanmaz.

---

## 9. İlk Somut Görevler (Faz 0 başlangıcı)

1. `go mod init` ve Bölüm 4'teki dizin ağacını oluştur.
2. `Makefile` hedefleri: `run, build, test, lint, migrate-up, migrate-down, up, down, gen`.
3. `deploy/docker-compose.yml`: Postgres 16 + Redis 7.
4. `core/config`: env'den `APP_PORT, DATABASE_URL, REDIS_URL, LOG_LEVEL` oku.
5. `core/logger`: `slog` JSON handler, log level config'den.
6. `cmd/server/main.go`: config yükle → logger kur → (boş) container kur → chi router → `/health` → dinle.
7. `golangci-lint` config + GitHub Actions CI (lint + test).
8. İlk commit: `chore: project skeleton (phase 0)`.

---

## 10. Sonraki Sürüm Fikirleri (şimdilik kapsam dışı)

GraphQL query yüzeyi, admin panel UI, storefront SDK, çoklu-tenant, çoklu-depo gelişmiş stok, Temporal entegrasyonu, B2B (quote/şirket hesapları), arama (OpenSearch/Meilisearch entegrasyonu).
