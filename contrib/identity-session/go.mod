// This module is SEPARATE on purpose (ADR 0127).
//
// gobit is imported, so every line of its go.mod is a line in the embedder's
// module graph, their vulnerability scan and their legal review — the dependency
// gate says so in those words. A session package grows towards WebAuthn, and a
// library for that belongs in the graph of whoever asked for it.
module github.com/bdrtr/gobit/contrib/identity-session

go 1.26.6

require (
	github.com/bdrtr/gobit v0.0.0
	github.com/go-chi/chi/v5 v5.3.2
	github.com/jackc/pgx/v5 v5.10.0
	github.com/stretchr/testify v1.12.1
	golang.org/x/crypto v0.55.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/golang-migrate/migrate/v4 v4.19.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/lib/pq v1.10.9 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.45.0 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.opentelemetry.io/otel/trace v1.45.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/bdrtr/gobit => ../..
