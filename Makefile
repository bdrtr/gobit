# gobit — Go Headless Commerce Framework
# Tüm hedefler için: make help

BIN_DIR     := $(CURDIR)/bin
COMPOSE     := docker compose -f deploy/docker-compose.yml
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# `gobit new`'in üretilen go.mod'a yazacağı sürümü belirleyen DERLEME OLGULARI
# (ADR 0154). Üçü de git'ten OLDUĞU GİBİ alınır; aritmetiği (yamanın bir
# artırılması, damganın biçimi, hash'in kısaltılması) Go tarafında ve TESTLİ.
#
# RELEASE yalnızca commit'in ÜSTÜNDE bir etiket varsa doluyor: `git describe
# --exact-match` başka her durumda başarısız oluyor, yani etiketli olmayan bir
# derleme kendisini sürüm sanamıyor.
BUILD_RELEASE    := $(shell git describe --tags --exact-match 2>/dev/null)
BUILD_BASE_TAG   := $(shell git describe --tags --abbrev=0 2>/dev/null)
BUILD_COMMIT     := $(shell git rev-parse HEAD 2>/dev/null)
BUILD_COMMIT_TS  := $(shell git log -1 --format=%ct 2>/dev/null)

LDFLAGS     := -s -w -X main.version=$(VERSION) \
	-X github.com/bdrtr/gobit/internal/app.buildRelease=$(BUILD_RELEASE) \
	-X github.com/bdrtr/gobit/internal/app.buildBaseTag=$(BUILD_BASE_TAG) \
	-X github.com/bdrtr/gobit/internal/app.buildCommit=$(BUILD_COMMIT) \
	-X github.com/bdrtr/gobit/internal/app.buildCommitTime=$(BUILD_COMMIT_TS)

GOLANGCI_VERSION := v2.13.1
GOVULN_VERSION   := v1.1.4
SQLC_VERSION     := v1.31.1

# Ayrı go.mod'u olan modüller — kök DAHİL.
#
# Liste ve SAYI birlikte duruyor ve sayı bir TABAN: bir glob hiçbir şey
# eşleştirmezse döngü hiç dönmez ve hedef yine 0 döner, yani "hiçbir açık yok"
# ile "hiçbir yere bakmadım" aynı çıkış koduyla anlatılamaz. Listeye bir modül
# eklerken sayı da artar, ve artmazsa taban düşer.
SEPARATE_MODULES      := . examples/starter examples/storefront examples/plugin contrib/identity-session contrib/identity-passkey
SEPARATE_MODULE_COUNT := 6
GOLANGCI         := $(BIN_DIR)/golangci-lint
GOVULN           := $(BIN_DIR)/govulncheck
SQLC             := $(BIN_DIR)/sqlc

# .env, make'in `include` mekanizmasıyla DEĞİL, POSIX kabuk semantiğiyle yüklenir.
# `include .env` + `export` kullanılamaz çünkü make:
#   - değerin içindeki `#` karakterinden sonrasını yorum sayıp keser
#     (pa#ss içeren parola -> "pa"),
#   - `$` karakterini değişken genişletmesi olarak yorumlar (se$cret -> "seret"),
#   - tırnakları değerin parçası bırakır (LOG_FORMAT="text" -> `"text"`).
# Parola içeren gerçek bir DSN bu yolla sessizce bozuluyordu.
#
# ÖNCELİK: komut satırından verilen değişken .env'i EZER, tersi değil. Düz
# `. ./.env` bunun tam tersini yapıyordu ve arıza SESSİZDİ: README'nin
# `cp .env.example .env` adımını izleyen geliştiricinin ardından yazdığı
# `OTEL_EXPORTER_OTLP_ENDPOINT=… make run`, `PLUGINS=… make run` ve
# `ADMIN_BOOTSTRAP_EMAIL=… make run` komutlarının hepsi .env'deki BOŞ değerle
# eziliyordu — izleme açılmıyor, eklenti yüklenmiyor, ilk yönetici
# tohumlanmıyordu ve hiçbiri hata vermiyordu (ölçüldü: `eklentiler=[]`).
# Öncelik docker compose'unkiyle aynı yöne çevrildi: ortam > .env.
#
# Yöntem AYRIŞTIRMAZ: çağıranın dışa verilmiş ortamı `export -p` ile
# saklanır, .env kabukla yüklenir, sonra saklanan ortam geri uygulanır.
# `KEY=$${KEY:-değer}` gibi bir sed dönüşümü .env'in içeriğine bağımlı olurdu
# (satır sonundaki yorum, çok satırlı değer) — tam da bu dosyada kaçınılan şey.
DOTENV = set -a; [ -f .env ] && { __cagiran_ortam=$$(export -p); . ./.env; eval "$$__cagiran_ortam"; }; set +a;

.DEFAULT_GOAL := help
.PHONY: help run build test test-integration smoke seed load-test fuzz openapi-schema openapi-client openapi-validate lint fmt tidy gen up up-tracing down logs psql redis-cli migrate-status migrate-up migrate-down tools clean rename-module

help: ## Bu yardım metnini göster
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

## --- Uygulama ---

run: ## Sunucuyu yerelde çalıştır
	@$(DOTENV) go run -ldflags '$(LDFLAGS)' ./cmd/server

build: ## Binary'yi bin/gobit olarak derle
	@mkdir -p $(BIN_DIR)
	go build -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/gobit ./cmd/server
	@echo "derlendi: $(BIN_DIR)/gobit ($(VERSION))"

## --- Kalite ---

test: ## Birim testlerini çalıştır (race + coverage)
	# -coverpkg olmadan yalnızca test edilen paketin KENDİ kodu sayılır; bir
	# paketi başka paketin testi kapsadığında görünmez. Buradaki sayı YALNIZCA
	# birim testlerinindir (~%55); deponun gerçek kapsamı entegrasyon
	# testleriyle birlikte ölçülür (make test-integration, ~%76).
	go test -race -coverpkg=./... -coverprofile=coverage.out -covermode=atomic ./...

test-integration: ## Entegrasyon testlerini çalıştır (testcontainers gerektirir)
	go test -race -tags=integration -count=1 -coverpkg=./... \
		-coverprofile=coverage-integration.out -covermode=atomic ./...
	@go tool cover -func=coverage-integration.out | tail -1

# Smoke testleri ikiliyi DERLER ve gerçek süreçler başlatır; entegrasyon
# etiketine karıştırılmadılar çünkü karıştırılsalardı süreç başlatmayan
# yüzlerce test de bu maliyeti her koşumda öderdi (bkz. internal/smoke).
#
# -race YOKTUR ve bunun bir anlamı var: yarış dedektörü test SÜRECİNİ
# izler, sınanan sunucu ise AYRI bir süreçtir ve kapsanmaz. Bayrağı koymak,
# ölçmediği bir güvenceyi ima ederdi.
#
# Zaman aşımı açıkça verilir: varsayılan 10 dakika, konteyner çekme +
# derleme + beş senaryonun toplamı için soğuk bir makinede dar kalabilir.
smoke: ## Smoke testleri: gerçek ikiliyi açıp süreç davranışını sınar (Docker gerektirir)
	go test -tags=smoke -count=1 -timeout 20m ./internal/smoke/

# Benchmark'lar veritabanına DOKUNMAZ: hepsi saf fonksiyonlar ya da sahte
# servisler üzerinde koşar. Deponun geri kalan ölçümü SQL tarafındaydı
# (EXPLAIN, 52 bin satırlık fikstür); buradaki rakamlar Go tarafının kendi
# maliyetidir ve iki ölçüm birbirinin yerine geçmez.
#
# BENCH ile tek bir benchmark seçilebilir: make bench BENCH=StorefrontQuery
bench: ## Go tarafı benchmark'ları çalıştır (tahsisat sayısıyla birlikte)
	go test -run '^$$' -bench '$(or $(BENCH),.)' -benchmem ./...

# Fuzz hedeflerinin TOHUMLARI olağan test şeridinde koşar; burası üretilen
# girdilerin şerididir ve ELLE koşulur. CI'da bir iş değil: bir fuzz koşusunun
# değeri süreyle artar ve her push'ta 30 saniye koşmak, tohumların zaten
# yaptığı işi ikinci kez yapmaktır.
#
# `-fuzz` deseni ÇAPALIDIR ve gerekçesi ÖLÇÜLDÜ: `go test` onu çapasız bir
# regexp gibi okur ve birden çok hedefe uyunca fuzz'lamayı REDDEDER — aynı
# pakete `FuzzMulDivModAgain` eklenip `-fuzz FuzzMulDivMod` denendiğinde
# "will not fuzz, -fuzz matches more than one fuzz test" deyip 1 ile çıkıyor.
# Yani çapasız desende, önek paylaşan ikinci bir hedefin eklendiği gün bu
# döngü orada durur ve KALAN hedeflerin hiçbiri koşmaz.
#
# FUZZTIME ile süre ayarlanır: make fuzz FUZZTIME=5m
FUZZTIME ?= 30s
fuzz: ## Fuzz hedeflerini sırayla çalıştır (FUZZTIME ile ayarlanır)
	@found=0; \
	for pkg in $$(go list ./...); do \
		for target in $$(go test -list '^Fuzz' $$pkg 2>/dev/null | grep '^Fuzz'); do \
			echo "  $$pkg: $$target ($(FUZZTIME))"; \
			go test -run '^$$' -fuzz "^$$target\$$" -fuzztime=$(FUZZTIME) $$pkg || exit 1; \
			found=$$((found+1)); \
		done; \
	done; \
	if [ "$$found" -eq 0 ]; then \
		echo "fuzz: hiçbir hedef bulunamadı, en az bir tane bekleniyordu" >&2; exit 1; \
	fi

# Ölçüm düzeneği artık DEPODAN kurulur.
#
# 52 bin ürünlük katalog aylarca tek bir Docker biriminde yaşadı ve depoda onu
# yeniden kuracak hiçbir şey yoktu: seed dosyası yok, seed hedefi yok, seed
# programı yok. `docker compose down -v` ile 28 dosyadaki her zamanlama cümlesi
# doğrulanamaz düzyazıya dönüşüyordu — "performans cümlesi ölçülmeden yazılmaz"
# kuralı bir Docker birimine bağlıydı.
#
# Hedef AYRI BİR BETİK DEĞİL, ikilinin kendi alt komutudur ve bu zorunludur:
# şema modüllerin KENDİ migration'larından gelir, üstelik üç link tablosu
# (link_product_variant_price_set, link_product_variant_inventory,
# link_product_sales_channel) hiçbir migration'da yoktur — core/link onları
# AÇILIŞTA, Define çağrısıyla yaratır. Saf bir `psql -f seed.sql` bu yüzden ilk
# link INSERT'ünde patlardı.
#
# BOYUT parametredir ve varsayılanı düzeneğin kendi şeklidir (50.000 tek
# varyantlı + 2.000 çift varyantlı ürün = 52.004). Küçük bir katalog için:
#   make seed URUNLER=200 COKLU=20
# Değişken verilmediğinde bayrak HİÇ GEÇİLMEZ; varsayılan sayı burada DEĞİL,
# ikilinin içindedir (internal/rig). İkinci bir yerde tekrarlansaydı, biri
# değiştiğinde Makefile sessizce eski şekli kurmaya devam ederdi.
#
# HEDEF VERİTABANI ortamdan gelir (DATABASE_URL) — sunucununkiyle aynı ayar.
#
# SİLME (-reset) BU HEDEFTE YOKTUR ve gerekçe migrate-down'unkiyle aynıdır:
# onay, veritabanı adının TEKRARIDIR; bir Makefile değişkeni onayı da beraberinde
# taşısaydı silme "yanlışlıkla çalıştırılabilir" hâle gelirdi. Silmeli hâli elle
# yazılır:
#   go run ./cmd/server seed -reset -confirm <veritabanı-adı>
SEED_FLAGS := $(if $(URUNLER),-products $(URUNLER)) $(if $(COKLU),-multi $(COKLU))

seed: ## Ölçüm kataloğunu kur (URUNLER/COKLU ile boyutlandırılır)
	@$(DOTENV) go run -ldflags '$(LDFLAGS)' ./cmd/server seed $(SEED_FLAGS)

load-test: ## Temel yük testini çalıştır (REQUESTS/CONCURRENCY ile ayarlanır)
	GOBIT_LOAD_REQUESTS=$(or $(REQUESTS),5000) \
	GOBIT_LOAD_CONCURRENCY=$(or $(CONCURRENCY),32) \
	go test -tags=integration -count=1 -v -run TestStaysCorrectUnderBaselineLoad ./internal/e2e/

lint: $(GOLANGCI) ## golangci-lint çalıştır (kök + ayrı modüller)
	$(GOLANGCI) run ./...
	@# Ayrı bir go.mod ayrı bir derleme birimidir ve `run ./...` ona ULAŞMIYOR.
	@# Aynı yapılandırmayla koşuluyor: kuralı kökten farklı olan bir ağaç, aynı
	@# depoda iki farklı üsluba izin verirdi.
	@#
	@# Sayaç vuln hedefinin sayacıyla aynı sebeple: bir glob hiçbir şey
	@# eşleştirmezse döngü hiç dönmez ve hedef yine 0 döner.
	@found=0; \
	for mod in $(SEPARATE_MODULES); do \
		[ "$$mod" = "." ] && continue; \
		[ -f "$$mod/go.mod" ] || continue; \
		echo "  $$mod: golangci-lint"; \
		(cd "$$mod" && $(abspath $(GOLANGCI)) run --config $(CURDIR)/.golangci.yml ./...) || exit 1; \
		found=$$((found+1)); \
	done; \
	if [ "$$found" -lt $$(($(SEPARATE_MODULE_COUNT) - 1)) ]; then \
		echo "lint: yalnızca $$found ayrı modül denetlendi" >&2; exit 1; \
	fi

# vuln, bilinen açıkları ÜÇ modülde birden arar: kök ve iki örnek.
#
# Örnekler dahildir çünkü gobit bir KÜTÜPHANEDIR (ADR 0025) ve o iki modül,
# gömen bir projenin gerçekten derlediği şeyin en yakın örneğidir. Kökün graf'ı
# temiz olup starter'ınkinin olmaması mümkündür.
#
# `|| exit 1` DÖNGÜNÜN İÇİNDE, ve bu satır hedefin tek kırılgan yeri: bir shell
# `for` döngüsü SON yinelemenin çıkış kodunu döndürür, yani kök kırmızı +
# örnekler yeşil = make 0 döner. Yanlış yeşil. Aynı koruma `gen` hedefinde de
# var ve aynı sebeple.
#
# `found` sayacı ise ikinci yarısı: bir glob hiçbir şey eşleştirmezse döngü hiç
# dönmez ve hedef yine 0 döner — "hiçbir açık yok" ile "hiçbir yere bakmadım"
# aynı çıkış koduyla anlatılamaz.
#
# BULGU VARSA BUILD KIRMIZI OLUR, ve düzeltmesi olmayan bir tavsiye için bir
# muafiyet mekanizması BİLEREK yazılmadı: tüketicisi olmayan bir yetenek bu
# deponun reddettiği şekildir (ADR 0009). O gün geldiğinde seçenekler pinlemek,
# yamalamak ya da o gün yazılmış bir muafiyettir — üçü de birinin karar verdiği
# şeyler, bugünden kurulmuş bir kaçış yolu değil.
vuln: $(GOVULN) ## Bilinen açıkları ara (kök + ayrı modüller)
	@found=0; \
	for mod in $(SEPARATE_MODULES); do \
		[ -f "$$mod/go.mod" ] || continue; \
		echo "  $$mod: govulncheck"; \
		(cd "$$mod" && $(abspath $(GOVULN)) ./...) || exit 1; \
		found=$$((found+1)); \
	done; \
	if [ "$$found" -lt $(SEPARATE_MODULE_COUNT) ]; then \
		echo "vuln: yalnızca $$found modül tarandı, $(SEPARATE_MODULE_COUNT) bekleniyordu" >&2; exit 1; \
	fi

# Ayrı go.mod'u olan her modülün testleri.
#
# `go test ./...` kökten koşulduğunda bu modüllere ULAŞMIYOR: ayrı bir modül,
# ayrı bir derleme birimidir. contrib/identity-session yirmi sekiz test taşıyor
# ve hiçbir şerit onları koşmuyordu; koşulmayan bir test, olmayan bir testten
# KÖTÜDÜR, çünkü kapsam varmış gibi görünür.
#
# Örnek modüllerin testi yok ve derlenmeleri arch süitindeki
# TestTheOutOfTreeExamplesCompile ile kanıtlanıyor; bu hedef onları da koşuyor,
# çünkü "testi yok" bugünün olgusu, kuralın değil.
test-modules: ## Ayrı modüllerin testlerini koştur
	@found=0; \
	for mod in $(SEPARATE_MODULES); do \
		[ -f "$$mod/go.mod" ] || continue; \
		echo "  $$mod: go test"; \
		(cd "$$mod" && go test -count=1 ./...) || exit 1; \
		found=$$((found+1)); \
	done; \
	if [ "$$found" -lt $(SEPARATE_MODULE_COUNT) ]; then \
		echo "test-modules: yalnızca $$found modül koşuldu, $(SEPARATE_MODULE_COUNT) bekleniyordu" >&2; exit 1; \
	fi

# Ayrı modüllerin ENTEGRASYON testleri.
#
# Ayrı bir hedef, cunku `test-modules` CI'nin Test isinde kosuyor ve orada Docker
# YOK. Bu hedef Integration isine ait.
#
# Taban MODUL BASINA degil, TOPLAMDA birdir.
#
# Hangi modulun entegrasyon testi olacagi bugunun olgusu — ornek moduller hic
# test tasimiyor — ve modul basina bir taban, yazilmamis bir kurali dayatirdi.
# Ama tabansiz birakmak olculdu ve KOTU cikti: tarama bozulunca hedef hicbir
# sey kosmadan 0 donuyor, ki "hepsi gecti" ile "hicbirine bakmadim" yine ayni
# cikis kodu demek.
#
# Bir taban ikisini birden veriyor: kimseye test yazma borcu yuklemiyor, ve
# tarama sessizce bosa dustugunde duruyor. Bugun bir modul kosuyor; o da
# kalmayacaksa bu satir birinin KARAR vermesini istiyor.
test-modules-integration: ## Ayrı modüllerin entegrasyon testlerini koştur (Docker)
	@found=0; \
	for mod in $(SEPARATE_MODULES); do \
		[ "$$mod" = "." ] && continue; \
		[ -f "$$mod/go.mod" ] || continue; \
		if ! grep -rqls '//go:build integration' "$$mod"; then continue; fi; \
		echo "  $$mod: go test -tags=integration"; \
		(cd "$$mod" && go test -tags=integration -count=1 ./...) || exit 1; \
		found=$$((found+1)); \
	done; \
	if [ "$$found" -lt 1 ]; then \
		echo "test-modules-integration: hicbir ayri modul kosulmadi" >&2; exit 1; \
	fi

fmt: $(GOLANGCI) ## Kaynakları biçimlendir (gofmt + goimports)
	@$(GOLANGCI) fmt ./...
	@go mod tidy

tidy: ## go.mod/go.sum'ı düzenle ve doğrula
	go mod tidy
	go mod verify

## --- Altyapı ---

up: ## Postgres + Redis'i ayağa kaldır (sağlıklı olana kadar bekler)
	@$(DOTENV) $(COMPOSE) up -d --wait
	@echo "postgres ve redis hazır."

up-tracing: ## Altyapıyı Jaeger izleme toplayıcısıyla birlikte kaldır
	@$(DOTENV) $(COMPOSE) --profile tracing up -d --wait
	@echo "postgres, redis ve jaeger hazır."
	@echo "izlemeyi açmak için: OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 OTEL_EXPORTER_OTLP_INSECURE=true make run"
	@echo "arayüz: http://localhost:$${JAEGER_UI_PORT:-16686}"

down: ## Servisleri durdur (veri korunur)
	@$(DOTENV) $(COMPOSE) --profile tracing down

logs: ## Servis loglarını izle
	@$(DOTENV) $(COMPOSE) logs -f

psql: ## Postgres'e psql ile bağlan
	@$(DOTENV) $(COMPOSE) exec postgres psql -U "$${POSTGRES_USER:-gobit}" -d "$${POSTGRES_DB:-gobit}"

redis-cli: ## Redis'e redis-cli ile bağlan
	@$(DOTENV) $(COMPOSE) exec redis redis-cli --no-auth-warning -a "$${REDIS_PASSWORD:-gobit}"

## --- Migration ---
#
# İLERİ YÖN İÇİN AYRI BİR KOMUT YOKTUR ve bu bilinçlidir: migration'lar
# uygulama AÇILIŞINDA, modül başına ve golang-migrate'in kilidiyle uygulanır
# (bkz. core/db.Migrate ve module.Registry.Bootstrap). Ayrı bir komut, "şemayı
# güncellemeyi unuttum" hatasını mümkün kılardı — kod ile şemanın ayrı adımlarda
# ilerlediği her kurulumda er geç olan budur.
#
# Eşzamanlı açılış güvenlidir: birden çok örnek aynı anda açıldığında
# golang-migrate'in kilidi birini geçirir, ötekiler bekler (gerçek sunucuyla
# üç örnekle doğrulandı).
#
# GERİ ALMA ise ikilinin kendi alt komutudur; aşağıdaki hedefler onu sarar.
# Sunucu hâlâ ARGÜMANSIZ çalıştırıldığında başlar ve başka hiçbir biçimde
# başlamaz.

migrate-status: ## Her sahibin şema sürümünü ve dirty durumunu bildirir
	@$(DOTENV) go run -ldflags '$(LDFLAGS)' ./cmd/server migrate status

migrate-up: ## Migration'lar açılışta otomatik uygulanır (ayrı komut yok)
	@echo "migrate-up: ayrı bir komut YOKTUR."
	@echo "  Migration'lar 'make run' ile açılışta, modül başına uygulanır."
	@echo "  Yalnızca şemayı kurmak için: DATABASE_URL=... go run ./cmd/server (açıldıktan sonra durdurun)."
	@echo "  Uygulanmış sürümleri görmek için: make migrate-status"

# ONAY, sahip adının TEKRARIDIR ve bu hedef onu VERMEZ: make migrate-down
# OWNER=cart yalnızca planı basar ve sıfırdan farklı kodla döner. Onaylı hâli
# elle yazılır, çünkü bir Makefile değişkeni onayı da beraberinde taşısaydı
# geri alma "yanlışlıkla çalıştırılabilir" hâle gelirdi — .down.sql dosyaları
# yarattıkları şeyi DROP eder ve satırlar geri gelmez.
migrate-down: ## Bir modülün şemasını geri alma PLANINI basar (OWNER=<modül>)
	@test -n "$(OWNER)" || { echo "migrate-down: OWNER=<modül> gerekir (sahipler için: make migrate-status)"; exit 2; }
	@$(DOTENV) go run -ldflags '$(LDFLAGS)' ./cmd/server migrate down "$(OWNER)"

## --- İstemci üretimi ---

# Şema router'dan üretildiği ve gövdeler Go tiplerinden türetildiği için
# istemci ELDE TUTULMAZ: depoda bir SDK vendorlamak, ikinci bir artefaktı
# sürümlemek ve şemayla senkron tutmak demektir. Bunun yerine komut belgelenir
# ve isteyen kendi dilinde üretir.
#
# openapi-client, çalışan bir sunucudan şemayı çeker; sunucu ayakta olmalıdır
# (make up && make run). DIL değişkeniyle hedef değiştirilir:
#   make openapi-client DIL=go
#   make openapi-client DIL=python

OPENAPI_URL ?= http://localhost:$(or $(APP_PORT),9000)/openapi.json
DIL         ?= typescript-fetch

openapi-schema: ## Çalışan sunucudan OpenAPI şemasını indir (openapi.json)
	@curl -sSf $(OPENAPI_URL) -o openapi.json
	@echo "yazıldı: openapi.json ($$(wc -c < openapi.json) bayt)"

# Üreteç konteyneri varsayılan olarak root koşar ve bağlanan dizine root'a ait
# dosyalar yazar: üretilen istemci o an okunabilir ama `make clean` onu SİLEMEZ
# ("Permission denied") ve geliştirici kendi çalışma ağacında sudo'ya muhtaç
# kalır (yaşandı). --user, üretilen dosyaların sahibini çağırana sabitler.
DOCKER_USER := --user $(shell id -u):$(shell id -g)

openapi-client: openapi-schema ## Şemadan istemci üret (DIL=... ile dil seçilir)
	@docker run --rm $(DOCKER_USER) -v $(CURDIR):/local \
		openapitools/openapi-generator-cli:v7.10.0 \
		generate -i /local/openapi.json -g $(DIL) -o /local/clients/$(DIL)
	@echo "üretildi: clients/$(DIL)"

openapi-validate: openapi-schema ## Şemayı gerçek OpenAPI üreteciyle doğrula
	@docker run --rm -v $(CURDIR):/local \
		openapitools/openapi-generator-cli:v7.10.0 \
		validate -i /local/openapi.json

## --- Kod üretimi ---

gen: $(SQLC) ## Üretilen kodu yenile: sqlc (repository) + gqlgen (GraphQL)
	@found=0; \
	for cfg in internal/modules/*/sqlc.yaml; do \
		[ -e "$$cfg" ] || continue; \
		mod=$$(basename $$(dirname $$cfg)); \
		if [ -z "$$(ls -A $$(dirname $$cfg)/queries 2>/dev/null)" ]; then \
			echo "  $$mod: sorgu yok, atlanıyor"; continue; \
		fi; \
		echo "  $$mod: sqlc generate"; \
		$(SQLC) generate -f "$$cfg" || exit 1; \
		found=$$((found+1)); \
	done; \
	if [ "$$found" = "0" ]; then echo "gen: üretilecek sorgu bulunamadı"; fi
	@# gqlgen, sqlc'den İKİ noktada ayrılır ve ikisi de bilinçlidir:
	@#
	@# 1. Üreteç bin/ altına KURULMAZ, `go tool` ile go.mod'daki sürümden
	@#    çalıştırılır (go.mod'daki "tool" satırı). Sebep: üretilen kod, kendisini
	@#    çalıştıran kütüphaneyle AYNI sürümden gelmelidir. İkinci bir sürüm pini
	@#    (burada bir GQLGEN_VERSION) ayrıştığı gün, üretilen kod imzası değişmiş
	@#    bir yardımcıyı çağırır ve hata şemayla ilgisi olmayan bir yerde çıkar.
	@#
	@# 2. Modül dizinine GİRİLİR. gqlgen yolları çalışma dizinine göre çözer;
	@#    kökten çalıştırmak hata vermez, sessizce BOŞ bir şema okuyup kökte bir
	@#    graph/ dizini üretir (denendi). "cd", o sessiz arızayı imkânsız kılar.
	@for cfg in internal/modules/*/gqlgen.yml; do \
		[ -e "$$cfg" ] || continue; \
		mod=$$(basename $$(dirname $$cfg)); \
		echo "  $$mod: gqlgen generate"; \
		(cd $$(dirname $$cfg) && go tool gqlgen generate --config $$(basename $$cfg)) || exit 1; \
	done

## --- Araçlar ---

tools: $(GOLANGCI) $(SQLC) $(GOVULN) ## Sabitlenmiş sürümlerle yerel araçları kur

hooks: ## Push öncesi kapıyı bu klona kur (.githooks/pre-push)
	git config core.hooksPath .githooks
	@echo "core.hooksPath = .githooks — push öncesi build + lint çalışacak."
	@echo "Bilerek atlamak için: git push --no-verify"

$(GOLANGCI):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

$(SQLC):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

$(GOVULN):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULN_VERSION)

clean: ## Üretilmiş dosyaları temizle
	rm -rf $(BIN_DIR) coverage.out coverage-integration.out openapi.json clients

rename-module: ## Go modul yolunu degistir: make rename-module MODULE=github.com/kullanici/repo
	@test -n "$(MODULE)" || (echo "kullanim: make rename-module MODULE=github.com/kullanici/repo" >&2 && exit 1)
	@old=$$(head -1 go.mod | awk '{print $$2}'); \
	if [ "$$old" = "$(MODULE)" ]; then echo "modul yolu zaten $(MODULE)"; exit 0; fi; \
	files=$$(grep -rlI --exclude-dir=.git --exclude-dir=bin --exclude-dir=vendor -- "$$old" . || true); \
	if [ -z "$$files" ]; then echo "hata: $$old hicbir dosyada bulunamadi" >&2; exit 1; fi; \
	echo "$$files" | xargs sed -i "s|$$old|$(MODULE)|g"; \
	kalan=$$(grep -rlI --exclude-dir=.git --exclude-dir=bin --exclude-dir=vendor -- "$$old" . || true); \
	if [ -n "$$kalan" ]; then echo "hata: eski yol hala su dosyalarda: $$kalan" >&2; exit 1; fi; \
	echo "modul yolu $$old -> $(MODULE) ($$(echo "$$files" | wc -l) dosya guncellendi)"; \
	echo "not: .golangci.yml depguard kurallari ve README dahil edildi."
	@go mod tidy
