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
.PHONY: help run build test test-integration lint fmt tidy gen up down logs psql redis-cli migrate-up migrate-down tools clean rename-module

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
	go test -race -coverprofile=coverage.out -covermode=atomic ./...

test-integration: ## Entegrasyon testlerini çalıştır (testcontainers gerektirir)
	go test -race -tags=integration -count=1 ./...

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

down: ## Servisleri durdur (veri korunur)
	@$(DOTENV) $(COMPOSE) down

logs: ## Servis loglarını izle
	@$(DOTENV) $(COMPOSE) logs -f

psql: ## Postgres'e psql ile bağlan
	@$(DOTENV) $(COMPOSE) exec postgres psql -U "$${POSTGRES_USER:-gobit}" -d "$${POSTGRES_DB:-gobit}"

redis-cli: ## Redis'e redis-cli ile bağlan
	@$(DOTENV) $(COMPOSE) exec redis redis-cli --no-auth-warning -a "$${REDIS_PASSWORD:-gobit}"

## --- Migration (Faz 1'de core/db migration runner'a bağlanacak) ---

migrate-up: ## Bekleyen migration'ları uygula
	@echo "migrate-up: Faz 1'de core/db migration runner'ı devreye girecek (golang-migrate, modül başına ayrı klasör)."

migrate-down: ## Son migration'ı geri al
	@echo "migrate-down: Faz 1'de core/db migration runner'ı devreye girecek."

## --- Kod üretimi ---

gen: $(SQLC) ## sqlc ile repository kodunu üret (modül başına ayrı config)
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

## --- Araçlar ---

tools: $(GOLANGCI) $(SQLC) ## Sabitlenmiş sürümlerle yerel araçları kur

$(GOLANGCI):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

$(SQLC):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

clean: ## Üretilmiş dosyaları temizle
	rm -rf $(BIN_DIR) coverage.out

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
