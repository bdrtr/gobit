# gobit — Go Headless Commerce Framework
# Tüm hedefler için: make help

BIN_DIR     := $(CURDIR)/bin
COMPOSE     := docker compose -f deploy/docker-compose.yml
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

GOLANGCI_VERSION := v2.13.1
SQLC_VERSION     := v1.31.1
GOLANGCI         := $(BIN_DIR)/golangci-lint
SQLC             := $(BIN_DIR)/sqlc

# .env, make'in `include` mekanizmasıyla DEĞİL, POSIX kabuk semantiğiyle yüklenir.
# `include .env` + `export` kullanılamaz çünkü make:
#   - değerin içindeki `#` karakterinden sonrasını yorum sayıp keser
#     (pa#ss içeren parola -> "pa"),
#   - `$` karakterini değişken genişletmesi olarak yorumlar (se$cret -> "seret"),
#   - tırnakları değerin parçası bırakır (LOG_FORMAT="text" -> `"text"`).
# Parola içeren gerçek bir DSN bu yolla sessizce bozuluyordu.
DOTENV = set -a; [ -f .env ] && . ./.env; set +a;

.DEFAULT_GOAL := help
.PHONY: help run build test test-integration smoke load-test openapi-schema openapi-client openapi-validate lint fmt tidy gen up up-tracing down logs psql redis-cli migrate-up migrate-down tools clean rename-module

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

load-test: ## Temel yük testini çalıştır (REQUESTS/CONCURRENCY ile ayarlanır)
	GOBIT_LOAD_REQUESTS=$(or $(REQUESTS),5000) \
	GOBIT_LOAD_CONCURRENCY=$(or $(CONCURRENCY),32) \
	go test -tags=integration -count=1 -v -run TestTemelYukAltindaDogruKalir ./internal/e2e/

lint: $(GOLANGCI) ## golangci-lint çalıştır
	$(GOLANGCI) run ./...

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
# AYRI BİR KOMUT YOKTUR ve bu bilinçlidir: migration'lar uygulama AÇILIŞINDA,
# modül başına ve golang-migrate'in kilidiyle uygulanır (bkz. core/db.Migrate
# ve module.Registry.Bootstrap). Ayrı bir komut, "şemayı güncellemeyi unuttum"
# hatasını mümkün kılardı — kod ile şemanın ayrı adımlarda ilerlediği her
# kurulumda er geç olan budur.
#
# Eşzamanlı açılış güvenlidir: birden çok örnek aynı anda açıldığında
# golang-migrate'in kilidi birini geçirir, ötekiler bekler (gerçek sunucuyla
# üç örnekle doğrulandı).

migrate-up: ## Migration'lar açılışta otomatik uygulanır (ayrı komut yok)
	@echo "migrate-up: ayrı bir komut YOKTUR."
	@echo "  Migration'lar 'make run' ile açılışta, modül başına uygulanır."
	@echo "  Yalnızca şemayı kurmak için: DATABASE_URL=... go run ./cmd/server (açıldıktan sonra durdurun)."

migrate-down: ## Geri alma yolu YOK (bkz. README, bilinen sınırlar)
	@echo "migrate-down: geri alma için bir komut YOKTUR."
	@echo "  Her modülün .down.sql dosyaları vardır ve geri alınabilirlikleri"
	@echo "  internal/arch TestMigrationlarGercektenGeriAlinabilir ile denetlenir,"
	@echo "  ama bugün onları çağıracak bir yüzey yok. Geri alma elle yapılmalıdır."

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

openapi-client: openapi-schema ## Şemadan istemci üret (DIL=... ile dil seçilir)
	@docker run --rm -v $(CURDIR):/local \
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

tools: $(GOLANGCI) $(SQLC) ## Sabitlenmiş sürümlerle yerel araçları kur

$(GOLANGCI):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

$(SQLC):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

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
