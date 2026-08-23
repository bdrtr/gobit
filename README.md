# gobit

Go tabanlı, modüler, headless commerce framework. Tek binary olarak çalışan bir
**modüler monolit**: modüller izole olduğu için herhangi biri ileride ayrı
servise çıkarılabilir.

Uygulama planının tamamı için: [`go-commerce-framework-plan.md`](./go-commerce-framework-plan.md)

**Mevcut durum: Faz 0 — Proje İskeleti & Tooling ✅**

## Hızlı başlangıç

```bash
make up      # Postgres 16 + Redis 7 (sağlıklı olana kadar bekler)
make run     # sunucuyu :9000'de başlatır
curl -s localhost:9000/health
# {"status":"ok","version":"dev"}
```

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

> **Üretim uyarısı:** `DATABASE_URL` ve `REDIS_URL` varsayılanları yalnızca
> yerel geliştirme içindir; üretimde mutlaka ezilmelidir.

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

Varsayılan modül yolu `github.com/turkbirdev/gobit`. Kendi deponuza taşımak için:

```bash
make rename-module MODULE=github.com/kullanici/repo
```

## Faz durumu

| Faz | Kapsam | Durum |
|---|---|---|
| 0 | Proje iskeleti & tooling | ✅ |
| 1 | Çekirdek altyapı (errors, db, container, module, eventbus, http middleware) | ⬜ |
| 2 | Module Links & Query | ⬜ |
| 3 | Workflow Engine (saga) | ⬜ |
| 4 | Katalog (product · pricing · inventory) | ⬜ |
| 5 | Sepet (cart · customer · region) | ⬜ |
| 6 | Ödeme & sipariş tamamlama | ⬜ |
| 7 | Fulfillment · promotion · tax | ⬜ |
| 8 | Auth · admin user · API key · RBAC | ⬜ |
| 9 | Plugin sistemi · observability · sertleştirme | ⬜ |
