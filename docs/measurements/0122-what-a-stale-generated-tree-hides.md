# What a stale generated tree hides — measured 2026-09-10

Serves [ADR 0122](../adr/0122-generated-code-is-verified-by-regenerating-it.md).

## The population

```
git ls-files | grep -c 'queries/.*\.sql$'      # 75 query files
git ls-files | grep -c '\.sql\.go$'            # 75 generated files
git ls-files | grep -c 'generated\.go$\|graph/' # 14 gqlgen files
```

Every one of them is committed, and every one of them is written by a tool from
a source that is also committed. Nothing in the tree compares the two.

## The drift that was found, and the one that was measured

Gap D60 is the found one: `internal/modules/order/queries/spending.sql` had its
long comment corrected by ADR 0121 without `make gen`, so the generated copy of
that comment said the opposite for an hour, on main, with CI green. It surfaced
only because a later change to a DIFFERENT query in the same module made the
generator rewrite both files at once.

The measured one is worse, because a comment is at least readable. The source
`.sql` file is NOT the query the database runs — the string embedded in the
generated `.sql.go` is — so an edit to the source alone changes nothing that any
lane executes.

Multiplying the B2B spending window by a thousand in the source and not
regenerating:

```
-SELECT COALESCE(SUM(o.total - COALESCE(s.refunded_total, 0)), 0)::bigint AS spent
+SELECT COALESCE(SUM(o.total * 1000), 0)::bigint AS spent
```

| Lane | Result |
|---|---|
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `go test ./internal/modules/order/...` | clean |
| `go test -tags=integration ./internal/modules/order/` | ok, 5.304s |

Four lanes, no complaint. The integration lane runs against a real PostgreSQL
and still passes, because it runs the OLD query — which is the correct one. The
edit is invisible in the only direction that matters: it is invisible while it
is wrong.

## What the gate costs

```
make gen                                              # 2 seconds
GOBIN=... go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
```

| Build | Seconds |
|---|---|
| sqlc, empty GOCACHE | 27 |
| sqlc, warm GOCACHE | 4 |
| `make gen` itself | 2 |

Measured on the development machine; a hosted runner is slower, and the lint job
it joins already spends minutes in golangci-lint. `actions/setup-go` caches the
build cache, so the warm figure is the ordinary one and the cold figure is the
first run after a Go version bump.

gqlgen needs no install: it runs through `go tool` from the version pinned in
`go.mod`, which is deliberate — the generated code has to come from the same
version as the library that runs it.

## Why the comparison is the whole file and not the comments

A gate that compared the doc comment on each generated function against the
comment block above its `-- name:` line would have caught D60 exactly and would
have been green through the measurement above: multiplying a query by a thousand
leaves every comment in the file byte-identical.

## The precedent this follows

CI already asks one question of this shape, and it is the model:

```yaml
- name: Is go.mod up to date
  run: |
    go mod tidy
    git diff --exit-code -- go.mod go.sum
```

Run the generator, ask git whether anything moved. `/bin/` is gitignored, so the
installed tool cannot itself dirty the tree.
