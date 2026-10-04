# gobit — Go Headless Commerce Framework
# For every target: make help

BIN_DIR     := $(CURDIR)/bin
COMPOSE     := docker compose -f deploy/docker-compose.yml
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Every lane runs the Go release go.mod names, which is the one CI installs
# (D138). A newer local release builds the same code, but an allocation count
# measured under one release is not the count of another, and a lane that ran
# whatever Go the machine had last been upgraded to judged the budgets against
# numbers CI never produces.
export GOTOOLCHAIN := go$(shell sed -n 's/^go //p' go.mod)

# The BUILD FACTS that decide the version `gobit new` writes into the go.mod it
# generates (ADR 0154). All three are taken from git AS THEY ARE; the
# arithmetic (incrementing the patch by one, the stamp's format, shortening the
# hash) is on the Go side and TESTED.
#
# RELEASE is filled only when a tag sits ON the commit: `git describe
# --exact-match` fails in every other case, so a build that is not tagged
# cannot take itself for a release.
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

# The modules with a go.mod of their own — the root INCLUDED.
#
# The list and the COUNT stand together and the count is a FLOOR: if a glob
# matches nothing, the loop never turns and the target still returns 0, so
# "there is no vulnerability" and "I looked nowhere" cannot be told apart by
# exit code. Adding a module to the list raises the count too, and if it does
# not, the floor drops.
SEPARATE_MODULES      := . examples/starter examples/storefront examples/plugin contrib/identity-session contrib/identity-passkey
SEPARATE_MODULE_COUNT := 6
GOLANGCI         := $(BIN_DIR)/golangci-lint
GOVULN           := $(BIN_DIR)/govulncheck
SQLC             := $(BIN_DIR)/sqlc

# .env is loaded with POSIX shell semantics, NOT with make's `include`
# mechanism. `include .env` + `export` cannot be used, because make:
#   - takes everything after a `#` inside a value for a comment and cuts it
#     (a password containing pa#ss -> "pa"),
#   - reads the `$` character as variable expansion (se$cret -> "seret"),
#   - leaves the quotes as part of the value (LOG_FORMAT="text" -> `"text"`).
# A real DSN containing a password was being corrupted this way, silently.
#
# PRECEDENCE: a variable given on the command line OVERRIDES .env, not the
# other way round. A plain `. ./.env` did exactly the opposite and the fault
# was SILENT: the commands that a developer who followed the README's
# `cp .env.example .env` step typed next,
# `OTEL_EXPORTER_OTLP_ENDPOINT=… make run`, `PLUGINS=… make run` and
# `ADMIN_BOOTSTRAP_EMAIL=… make run`, were all overridden by the EMPTY value
# in .env — tracing did not switch on, the plugin did not load, the first
# admin was not seeded, and none of them raised an error (measured: the
# startup log printed an empty plugin list). The precedence was turned to
# match docker compose's: environment > .env.
#
# The method does NOT PARSE: the caller's exported environment is saved with
# `export -p`, .env is loaded by the shell, then the saved environment is
# applied again. A sed transformation such as `KEY=$${KEY:-value}` would depend
# on the contents of .env (a comment at the end of a line, a multi-line value)
# — exactly what this file avoids.
DOTENV = set -a; [ -f .env ] && { __caller_env=$$(export -p); . ./.env; eval "$$__caller_env"; }; set +a;

.DEFAULT_GOAL := help
.PHONY: help run build test test-integration smoke seed load-test fuzz openapi-schema openapi-client openapi-validate lint fmt tidy gen up up-tracing down logs psql redis-cli migrate-status migrate-up migrate-down tools clean rename-module

help: ## Show this help text
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

## --- Application ---

run: ## Run the server locally
	@$(DOTENV) go run -ldflags '$(LDFLAGS)' ./cmd/server

build: ## Build the binary as bin/gobit
	@mkdir -p $(BIN_DIR)
	go build -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/gobit ./cmd/server
	@echo "built: $(BIN_DIR)/gobit ($(VERSION))"

## --- Quality ---

# The test lanes give the tests NO database FROM THE ENVIRONMENT (ADR 0163).
# The configuration's defaults point at localhost:5432 and localhost:6379, and
# on a development machine both are listening; a test that forgets to start
# its own setup therefore passes green here and turns red on the runner
# — D107 happened exactly this way. The address is the same, the port is 1,
# where nothing listens: the value still PARSES and still RESOLVES, so only
# connecting fails; tests that load the config for something else are not
# affected.
NO_AMBIENT_SERVICES := DATABASE_URL='postgres://gobit:gobit@127.0.0.1:1/gobit?sslmode=disable' REDIS_URL='redis://:gobit@127.0.0.1:1/0'

# Every package process of one `go test` shares ONE testcontainers reaper, and
# the reaper stops ten seconds after its last client leaves. A package that
# looks it up while it is stopping waits sixty seconds for a port that never
# opens (ADR 0138). In CI the reaper is off; here it is kept alive for five
# minutes after the last client, far past any gap between two packages of a
# lane, so the next package finds it running (ADR 0262). internal/arch holds
# every recipe that starts containers to carrying it.
REAPER_WAITS_OUT_THE_GAP := TESTCONTAINERS_RYUK_RECONNECTION_TIMEOUT=5m

test: ## Run the unit tests (race + coverage)
	# Without -coverpkg only the tested package's OWN code is counted; a
	# package covered by another package's test does not show. The figure
	# here belongs to the unit tests ALONE (~55%); the repository's real
	# coverage is measured together with the integration tests
	# (make test-integration, ~76%).
	$(NO_AMBIENT_SERVICES) go test -race -coverpkg=./... -coverprofile=coverage.out -covermode=atomic ./...

test-integration: ## Run the integration tests (requires testcontainers)
	$(NO_AMBIENT_SERVICES) $(REAPER_WAITS_OUT_THE_GAP) go test -race -tags=integration -count=1 -timeout 15m -coverpkg=./... \
		-coverprofile=coverage-integration.out -covermode=atomic ./...
	@go tool cover -func=coverage-integration.out | tail -1

# The smoke tests BUILD the binary and start real processes; they were not
# folded into the integration tag because, had they been, hundreds of tests
# that start no process would pay that cost on every run too (see
# internal/smoke).
#
# There is NO -race, and that means something: the race detector watches the
# test PROCESS, while the server under test is a SEPARATE process and is not
# covered. Setting the flag would imply a guarantee it does not measure.
#
# The timeout is given explicitly: the default 10 minutes can be tight on a
# cold machine for pulling the containers + building + five scenarios in total.
smoke: ## Smoke tests: start the real binary and test process behavior (requires Docker)
	$(NO_AMBIENT_SERVICES) $(REAPER_WAITS_OUT_THE_GAP) go test -tags=smoke -count=1 -timeout 20m ./internal/smoke/

# The benchmarks do NOT TOUCH the database: all of them run on pure functions
# or fake services. The rest of the repository's measurement was on the SQL
# side (EXPLAIN, a 52-thousand-row fixture); the figures here are the Go
# side's own cost, and neither measurement stands in for the other.
#
# BENCH selects a single benchmark: make bench BENCH=StorefrontQuery
bench: ## Run the Go-side benchmarks (with allocation counts)
	go test -run '^$$' -bench '$(or $(BENCH),.)' -benchmem ./...

# The SEEDS of the fuzz targets run in the ordinary test lane; this is the lane
# of generated inputs and it is run BY HAND. It is not a CI job: a fuzz run's
# value grows with its duration, and running for 30 seconds on every push
# would do a second time the work the seeds already do.
#
# The `-fuzz` pattern is ANCHORED and the reason was MEASURED: `go test` reads
# it as an unanchored regexp and REFUSES to fuzz when it matches more than one
# target — with `FuzzMulDivModAgain` added to the same package, trying
# `-fuzz FuzzMulDivMod` says "will not fuzz, -fuzz matches more than one fuzz
# test" and exits 1. So with an unanchored pattern, the day a second target
# sharing a prefix is added this loop stops there and NONE of the REMAINING
# targets runs.
#
# FUZZTIME sets the duration: make fuzz FUZZTIME=5m
FUZZTIME ?= 30s
fuzz: ## Run the fuzz targets one after another (set with FUZZTIME)
	@found=0; \
	for pkg in $$(go list ./...); do \
		for target in $$(go test -list '^Fuzz' $$pkg 2>/dev/null | grep '^Fuzz'); do \
			echo "  $$pkg: $$target ($(FUZZTIME))"; \
			go test -run '^$$' -fuzz "^$$target\$$" -fuzztime=$(FUZZTIME) $$pkg || exit 1; \
			found=$$((found+1)); \
		done; \
	done; \
	if [ "$$found" -eq 0 ]; then \
		echo "fuzz: no target found, at least one was expected" >&2; exit 1; \
	fi

# The measurement rig is now built FROM THE REPOSITORY.
#
# The 52-thousand-product catalog lived for months in a single Docker volume
# and the repository had nothing that could build it again: no seed file, no
# seed target, no seed program. With `docker compose down -v`, every timing
# sentence in 28 files was turning into unverifiable prose — the rule "a
# performance sentence is not written without being measured" hung on a
# Docker volume.
#
# The target is NOT A SEPARATE SCRIPT, it is the binary's own subcommand, and
# that is required: the schema comes from the modules' OWN migrations, and on
# top of that three link tables (link_product_variant_price_set,
# link_product_variant_inventory, link_product_sales_channel) are in no
# migration — core/link creates them AT STARTUP, with the Define call. A plain
# `psql -f seed.sql` would therefore blow up on the first link INSERT.
#
# The SIZE is a parameter and its default is the rig's own shape (50,000
# single-variant + 2,000 two-variant products = 52,004). For a small catalog:
#   make seed PRODUCTS=200 MULTI=20
# When a variable is not given, the flag is NOT PASSED AT ALL; the default
# count lives NOT here but inside the binary (internal/rig). Were it repeated
# in a second place, the day one of them changed, the Makefile would silently
# go on building the old shape.
#
# The TARGET DATABASE comes from the environment (DATABASE_URL) — the same
# setting as the server's.
#
# DELETION (-reset) is NOT IN THIS TARGET, and the reason is the same as
# migrate-down's: the confirmation is a REPETITION of the database name; had a
# Makefile variable carried the confirmation along with it, deleting would
# have become "runnable by accident". The deleting form is written by hand:
#   go run ./cmd/server seed -reset -confirm <database-name>
SEED_FLAGS := $(if $(PRODUCTS),-products $(PRODUCTS)) $(if $(MULTI),-multi $(MULTI))

seed: ## Build the measurement catalog (sized with PRODUCTS/MULTI)
	@$(DOTENV) go run -ldflags '$(LDFLAGS)' ./cmd/server seed $(SEED_FLAGS)

load-test: ## Run the baseline load test (set with REQUESTS/CONCURRENCY)
	GOBIT_LOAD_REQUESTS=$(or $(REQUESTS),5000) \
	GOBIT_LOAD_CONCURRENCY=$(or $(CONCURRENCY),32) \
	$(REAPER_WAITS_OUT_THE_GAP) go test -tags=integration -count=1 -v -run TestStaysCorrectUnderBaselineLoad ./internal/e2e/

lint: $(GOLANGCI) ## Run golangci-lint (root + separate modules)
	$(GOLANGCI) run ./...
	@# A separate go.mod is a separate build unit and `run ./...` does NOT REACH it.
	@# It is run with the same configuration: a tree whose rules differed from
	@# the root's would allow two different styles in one repository.
	@#
	@# The counter is there for the same reason as the vuln target's: if a glob
	@# matches nothing, the loop never turns and the target still returns 0.
	@found=0; \
	for mod in $(SEPARATE_MODULES); do \
		[ "$$mod" = "." ] && continue; \
		[ -f "$$mod/go.mod" ] || continue; \
		echo "  $$mod: golangci-lint"; \
		(cd "$$mod" && $(abspath $(GOLANGCI)) run --config $(CURDIR)/.golangci.yml ./...) || exit 1; \
		found=$$((found+1)); \
	done; \
	if [ "$$found" -lt $$(($(SEPARATE_MODULE_COUNT) - 1)) ]; then \
		echo "lint: only $$found separate modules were checked" >&2; exit 1; \
	fi

# vuln looks for known vulnerabilities across the whole of SEPARATE_MODULES:
# the root, the examples and the contrib trees.
#
# The count is NOT WRITTEN here, and once it was: it said "in THREE modules at
# once", the list had grown to six and the sentence had stayed at three. A
# count written by hand goes silently wrong when the thing it counts grows.
#
# The examples are included because gobit is a LIBRARY (ADR 0025) and they are
# the closest example of what an embedding project actually builds. The root's
# graph can be clean while the starter's is not.
#
# `|| exit 1` is INSIDE THE LOOP, and this line is the target's one fragile
# spot: a shell `for` loop returns the exit code of the LAST iteration, so a
# red root + green examples = make returns 0. A false green. The same guard is
# in the `gen` target, for the same reason.
#
# The `found` counter is the second half: if a glob matches nothing, the loop
# never turns and the target still returns 0 — "there is no vulnerability" and
# "I looked nowhere" cannot be told apart by exit code.
#
# IF THERE IS A FINDING THE BUILD GOES RED, and an exemption mechanism for an
# advisory that has no fix was DELIBERATELY not written: a capability with no
# consumer is the shape this repository rejects (ADR 0009). When that day
# comes, the options are pinning, patching, or an exemption written that day —
# all three are things somebody decides, not an escape route built in advance.
vuln: $(GOVULN) ## Look for known vulnerabilities (root + separate modules)
	@found=0; \
	for mod in $(SEPARATE_MODULES); do \
		[ -f "$$mod/go.mod" ] || continue; \
		echo "  $$mod: govulncheck"; \
		(cd "$$mod" && $(abspath $(GOVULN)) ./...) || exit 1; \
		found=$$((found+1)); \
	done; \
	if [ "$$found" -lt $(SEPARATE_MODULE_COUNT) ]; then \
		echo "vuln: only $$found modules were scanned, $(SEPARATE_MODULE_COUNT) were expected" >&2; exit 1; \
	fi

# The tests of every module that has a go.mod of its own.
#
# `go test ./...` run from the root does NOT REACH these modules: a separate
# module is a separate build unit. contrib/identity-session carries
# twenty-eight tests and no lane was running them; a test that is not run is
# WORSE than a test that does not exist, because it looks like coverage.
#
# The example modules have no tests, and that they build is proven by
# TestTheOutOfTreeExamplesCompile in the arch suite; this target runs them
# too, because "has no tests" is a fact about today, not the rule.
test-modules: ## Run the separate modules' tests
	@found=0; \
	for mod in $(SEPARATE_MODULES); do \
		[ -f "$$mod/go.mod" ] || continue; \
		echo "  $$mod: go test"; \
		(cd "$$mod" && go test -count=1 ./...) || exit 1; \
		found=$$((found+1)); \
	done; \
	if [ "$$found" -lt $(SEPARATE_MODULE_COUNT) ]; then \
		echo "test-modules: only $$found modules ran, $(SEPARATE_MODULE_COUNT) were expected" >&2; exit 1; \
	fi

# The INTEGRATION tests of the separate modules.
#
# A separate target, because `test-modules` runs in CI's Test job and there is
# NO Docker there. This target belongs to the Integration job.
#
# The floor is one IN TOTAL, not PER MODULE.
#
# Which module has integration tests is a fact about today — the example
# modules carry no tests at all — and a floor per module would impose a rule
# nobody has written. But leaving it without a floor was measured and came out
# BAD: when the scan breaks, the target runs nothing and returns 0, and "all
# passed" and "I looked at none" are once again the same exit code.
#
# One floor gives both: it puts no debt of writing tests on anybody, and it
# stops when the scan silently comes up empty. Today one module runs; if that
# one goes too, this line asks somebody to make a DECISION.
test-modules-integration: ## Run the separate modules' integration tests (Docker)
	@found=0; \
	for mod in $(SEPARATE_MODULES); do \
		[ "$$mod" = "." ] && continue; \
		[ -f "$$mod/go.mod" ] || continue; \
		if ! grep -rqls '//go:build integration' "$$mod"; then continue; fi; \
		echo "  $$mod: go test -tags=integration"; \
		(cd "$$mod" && $(REAPER_WAITS_OUT_THE_GAP) go test -tags=integration -count=1 ./...) || exit 1; \
		found=$$((found+1)); \
	done; \
	if [ "$$found" -lt 1 ]; then \
		echo "test-modules-integration: no separate module ran" >&2; exit 1; \
	fi

fmt: $(GOLANGCI) ## Format the sources (gofmt + goimports)
	@$(GOLANGCI) fmt ./...
	@go mod tidy

tidy: ## Tidy and verify go.mod/go.sum
	go mod tidy
	go mod verify

## --- Infrastructure ---

up: ## Bring up Postgres + Redis (waits until healthy)
	@$(DOTENV) $(COMPOSE) up -d --wait
	@echo "postgres and redis are ready."

up-tracing: ## Bring up the infrastructure with the Jaeger trace collector
	@$(DOTENV) $(COMPOSE) --profile tracing up -d --wait
	@echo "postgres, redis and jaeger are ready."
	@echo "to turn tracing on: OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 OTEL_EXPORTER_OTLP_INSECURE=true make run"
	@echo "UI: http://localhost:$${JAEGER_UI_PORT:-16686}"

down: ## Stop the services (data is kept)
	@$(DOTENV) $(COMPOSE) --profile tracing down

logs: ## Follow the service logs
	@$(DOTENV) $(COMPOSE) logs -f

psql: ## Connect to Postgres with psql
	@$(DOTENV) $(COMPOSE) exec postgres psql -U "$${POSTGRES_USER:-gobit}" -d "$${POSTGRES_DB:-gobit}"

redis-cli: ## Connect to Redis with redis-cli
	@$(DOTENV) $(COMPOSE) exec redis redis-cli --no-auth-warning -a "$${REDIS_PASSWORD:-gobit}"

## --- Migration ---
#
# There is NO SEPARATE COMMAND FOR THE FORWARD DIRECTION, and that is
# deliberate: migrations are applied AT APPLICATION STARTUP, per module and
# under golang-migrate's lock (see core/db.Migrate and
# module.Registry.Bootstrap). A separate command would make the "I forgot to
# update the schema" fault possible — which is what sooner or later happens in
# every installation where the code and the schema move forward in separate
# steps.
#
# Concurrent startup is safe: when several instances start at once,
# golang-migrate's lock lets one through and the others wait (verified with
# three instances against a real server).
#
# ROLLING BACK, on the other hand, is the binary's own subcommand; the targets
# below wrap it. The server still starts when run WITHOUT ARGUMENTS and starts
# in no other form.

migrate-status: ## Reports each owner's schema version and dirty state
	@$(DOTENV) go run -ldflags '$(LDFLAGS)' ./cmd/server migrate status

migrate-up: ## Migrations are applied automatically at startup (no separate command)
	@echo "migrate-up: there is NO separate command."
	@echo "  Migrations are applied at startup by 'make run', per module."
	@echo "  To install only the schema: DATABASE_URL=... go run ./cmd/server (stop it once it has started)."
	@echo "  To see the applied versions: make migrate-status"

# The CONFIRMATION is a REPETITION of the owner name and this target does NOT
# GIVE it: make migrate-down OWNER=cart only prints the plan and returns a
# non-zero code. The confirmed form is written by hand, because had a Makefile
# variable carried the confirmation along with it, rolling back would have
# become "runnable by accident" — the .down.sql files DROP what they created
# and the rows do not come back.
migrate-down: ## Prints the PLAN for rolling back one module's schema (OWNER=<module>)
	@test -n "$(OWNER)" || { echo "migrate-down: OWNER=<module> is required (for the owners: make migrate-status)"; exit 2; }
	@$(DOTENV) go run -ldflags '$(LDFLAGS)' ./cmd/server migrate down "$(OWNER)"

## --- Client generation ---

# Because the schema is generated from the router and the bodies are derived
# from Go types, the client is NOT KEPT IN THE TREE: vendoring an SDK in the
# repository means versioning a second artifact and keeping it in sync with
# the schema. The command is documented instead, and whoever wants a client
# generates it in their own language.
#
# openapi-client pulls the schema from a running server; the server must be up
# (make up && make run). The CLIENT_LANG variable changes the target:
#   make openapi-client CLIENT_LANG=go
#   make openapi-client CLIENT_LANG=python

OPENAPI_URL ?= http://localhost:$(or $(APP_PORT),9000)/openapi.json
CLIENT_LANG ?= typescript-fetch

openapi-schema: ## Download the OpenAPI schema from a running server (openapi.json)
	@curl -sSf $(OPENAPI_URL) -o openapi.json
	@echo "written: openapi.json ($$(wc -c < openapi.json) bytes)"

# The generator container runs as root by default and writes root-owned files
# into the mounted directory: the generated client is readable at the time,
# but `make clean` CANNOT DELETE it ("Permission denied") and the developer is
# left needing sudo in their own working tree (this happened). --user pins the
# owner of the generated files to the caller.
DOCKER_USER := --user $(shell id -u):$(shell id -g)

openapi-client: openapi-schema ## Generate a client from the schema (CLIENT_LANG=... selects the language)
	@docker run --rm $(DOCKER_USER) -v $(CURDIR):/local \
		openapitools/openapi-generator-cli:v7.10.0 \
		generate -i /local/openapi.json -g $(CLIENT_LANG) -o /local/clients/$(CLIENT_LANG)
	@echo "generated: clients/$(CLIENT_LANG)"

openapi-validate: openapi-schema ## Validate the schema with the real OpenAPI generator
	@docker run --rm -v $(CURDIR):/local \
		openapitools/openapi-generator-cli:v7.10.0 \
		validate -i /local/openapi.json

## --- Code generation ---

gen: $(SQLC) ## Refresh the generated code: sqlc (repository) + gqlgen (GraphQL)
	@found=0; \
	for cfg in internal/modules/*/sqlc.yaml; do \
		[ -e "$$cfg" ] || continue; \
		mod=$$(basename $$(dirname $$cfg)); \
		if [ -z "$$(ls -A $$(dirname $$cfg)/queries 2>/dev/null)" ]; then \
			echo "  $$mod: no queries, skipping"; continue; \
		fi; \
		echo "  $$mod: sqlc generate"; \
		$(SQLC) generate -f "$$cfg" || exit 1; \
		found=$$((found+1)); \
	done; \
	if [ "$$found" = "0" ]; then echo "gen: no queries found to generate"; fi
	@# gqlgen departs from sqlc at TWO points, and both are deliberate:
	@#
	@# 1. The generator is NOT INSTALLED under bin/; it runs with `go tool`
	@#    from the version in go.mod (the "tool" line in go.mod). Reason: the
	@#    generated code must come from the SAME version as the library that
	@#    runs it. The day a second version pin (a GQLGEN_VERSION here) drifted
	@#    apart, the generated code would call a helper whose signature had
	@#    changed, and the error would surface somewhere unrelated to the schema.
	@#
	@# 2. It ENTERS the module directory. gqlgen resolves paths relative to the
	@#    working directory; running it from the root gives no error, it
	@#    silently reads an EMPTY schema and generates a graph/ directory at the
	@#    root (tried). The "cd" makes that silent failure impossible.
	@for cfg in internal/modules/*/gqlgen.yml; do \
		[ -e "$$cfg" ] || continue; \
		mod=$$(basename $$(dirname $$cfg)); \
		echo "  $$mod: gqlgen generate"; \
		(cd $$(dirname $$cfg) && go tool gqlgen generate --config $$(basename $$cfg)) || exit 1; \
	done

## --- Tools ---

tools: $(GOLANGCI) $(SQLC) $(GOVULN) ## Install the local tools at their pinned versions

hooks: ## Install the pre-push gate in this clone (.githooks/pre-push)
	git config core.hooksPath .githooks
	@echo "core.hooksPath = .githooks — build + lint will run before every push."
	@echo "To skip it deliberately: git push --no-verify"

$(GOLANGCI):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

$(SQLC):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

$(GOVULN):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULN_VERSION)

clean: ## Remove the generated files
	rm -rf $(BIN_DIR) coverage.out coverage-integration.out openapi.json clients

rename-module: ## Change the Go module path: make rename-module MODULE=github.com/user/repo
	@test -n "$(MODULE)" || (echo "usage: make rename-module MODULE=github.com/user/repo" >&2 && exit 1)
	@old=$$(head -1 go.mod | awk '{print $$2}'); \
	if [ "$$old" = "$(MODULE)" ]; then echo "module path is already $(MODULE)"; exit 0; fi; \
	files=$$(grep -rlI --exclude-dir=.git --exclude-dir=bin --exclude-dir=vendor -- "$$old" . || true); \
	if [ -z "$$files" ]; then echo "error: $$old was found in no file" >&2; exit 1; fi; \
	echo "$$files" | xargs sed -i "s|$$old|$(MODULE)|g"; \
	remaining=$$(grep -rlI --exclude-dir=.git --exclude-dir=bin --exclude-dir=vendor -- "$$old" . || true); \
	if [ -n "$$remaining" ]; then echo "error: the old path is still in these files: $$remaining" >&2; exit 1; fi; \
	echo "module path $$old -> $(MODULE) ($$(echo "$$files" | wc -l) files updated)"; \
	echo "note: the .golangci.yml depguard rules and the README were included."
	@go mod tidy
