# gobit — Go Headless Commerce Framework
# Tüm hedefler için: make help

MODULE      := github.com/turkbirdev/gobit
BIN_DIR     := $(CURDIR)/bin
COMPOSE     := docker compose -f deploy/docker-compose.yml
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

GOLANGCI_VERSION := v2.13.1
SQLC_VERSION     := v1.31.1
GOLANGCI         := $(BIN_DIR)/golangci-lint
SQLC             := $(BIN_DIR)/sqlc

# Varsa .env dosyasındaki değişkenleri ortama al.
ifneq (,$(wildcard .env))
include .env
export
endif

.DEFAULT_GOAL := help
.PHONY: help run build test test-integration lint fmt tidy gen up down logs psql redis-cli migrate-up migrate-down tools clean rename-module

help: ## Bu yardım metnini göster
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

## --- Uygulama ---

run: ## Sunucuyu yerelde çalıştır
	go run -ldflags '$(LDFLAGS)' ./cmd/server

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

fmt: ## Kaynakları biçimlendir
	gofmt -w -s .
	go mod tidy

tidy: ## go.mod/go.sum'ı düzenle ve doğrula
	go mod tidy
	go mod verify

## --- Altyapı ---

up: ## Postgres + Redis'i ayağa kaldır (sağlıklı olana kadar bekler)
	$(COMPOSE) up -d --wait
	@echo "postgres ve redis hazır."

down: ## Servisleri durdur (veri korunur)
	$(COMPOSE) down

logs: ## Servis loglarını izle
	$(COMPOSE) logs -f

psql: ## Postgres'e psql ile bağlan
	$(COMPOSE) exec postgres psql -U gobit -d gobit

redis-cli: ## Redis'e redis-cli ile bağlan
	$(COMPOSE) exec redis redis-cli

## --- Migration (Faz 1'de core/db migration runner'a bağlanacak) ---

migrate-up: ## Bekleyen migration'ları uygula
	@echo "migrate-up: Faz 1'de core/db migration runner'ı devreye girecek (golang-migrate, modül başına ayrı klasör)."

migrate-down: ## Son migration'ı geri al
	@echo "migrate-down: Faz 1'de core/db migration runner'ı devreye girecek."

## --- Kod üretimi ---

gen: ## sqlc ile repository kodunu üret (Faz 4'ten itibaren)
	@if [ ! -f sqlc.yaml ]; then \
		echo "gen: sqlc.yaml henüz yok — ilk modül (Faz 4: product) ile birlikte eklenecek."; \
	else \
		$(MAKE) $(SQLC) && $(SQLC) generate; \
	fi

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

rename-module: ## Go modül yolunu değiştir: make rename-module MODULE=github.com/kullanici/repo
	test -n "$(MODULE)" || (echo "kullanım: make rename-module MODULE=github.com/kullanici/repo" && exit 1)
	old=$$(head -1 go.mod | awk '{print $$2}'); \
		grep -rl "$$old" --include='*.go' --include='go.mod' --include='Makefile' . | xargs sed -i "s|$$old|$(MODULE)|g"; \
		echo "modül yolu $$old -> $(MODULE) olarak güncellendi"
	go mod tidy
