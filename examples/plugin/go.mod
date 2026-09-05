// This is a SEPARATE Go module on purpose.
//
// A plugin written inside the gobit module could reach internal/ and compile;
// a plugin written outside it cannot. Only the second one proves the published
// surface (ADR 0026) is enough to write a plugin against, which is why this
// directory carries its own go.mod and is built by internal/arch rather than by
// the repository's own `go build ./...`.
module example.com/gobit-plugin-example

go 1.26.6

require (
	github.com/bdrtr/gobit v0.0.0
	github.com/go-chi/chi/v5 v5.3.2
)

require (
	github.com/caarlos0/env/v11 v11.4.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.10.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/redis/go-redis/v9 v9.22.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.45.0 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.opentelemetry.io/otel/trace v1.45.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/bdrtr/gobit => ../..
