# The first command — measured 2026-09-25

The evidence behind [ADR 0183](../adr/0183-a-stranger-starts-with-one-command.md).
Toolchain: `go1.26.6 linux/amd64`.

## 1. What a stranger could read

After v0.9.0 was published, every Markdown file outside `docs/adr/`,
`docs/measurements/`, the CHANGELOG and the gap ledger was searched for a
`go get`, `go install` or `new` line that starts a project. The only hits were
the README's directory listing, which names `internal/scaffold` as "what
`gobit new` writes", and the v0.9.0 entry in `docs/operating.md`. The README's
quick start is `make up` and `make run` in a checkout of this repository.

## 2. `go run` at a version

From a scratch directory outside the repository, against the module proxy:

```
$ go run github.com/bdrtr/gobit/cmd/server@v0.9.0 new shop
...
  docker compose up -d
  # fill in JWT_SECRET and ADMIN_BOOTSTRAP_* in .env
  go run .
$ grep '^require' shop/go.mod
require github.com/bdrtr/gobit v0.9.0
$ go run github.com/bdrtr/gobit/cmd/server@v0.9.0 help | head -1
gobit dev — headless commerce, one binary.
```

`go run` of a module at a version stamps that version, as `go install` does.
ADR 0182's measurement table has `go run .` as `(devel)`, which is right; its
Consequences say `go run`, which is wider than what was measured (D133).

## 3. Stamps that name nothing

The throwaway module of measurement 0182, two more builds:

| Build | Main path | Main version |
|---|---|---|
| `go build main.go` | empty | empty |
| `go build -buildvcs=false .` | `example.com/bi` | `(devel)` |

The empty version is why the fallback checks for it as well as for `(devel)`.
The first form of the test had no such case, and a mutation that dropped the
check survived until it was added.

## 4. The name, built

A clean clone of this repository with the change committed and a tag `v0.9.1`
that exists only in that clone. The first line of the help:

| Build | First line |
|---|---|
| `go build ./cmd/server`, untagged commit | `gobit v0.9.1-0.20260925185134-6763c86434a9 — ...` |
| `go build ./cmd/server`, tag `v0.9.1` | `gobit v0.9.1 — ...` |
| `go run .` in `cmd/server` | `gobit dev — ...` |
| `go build ./cmd/server`, `README.md` changed | `gobit v0.9.1+dirty — ...` |

## 5. Mutations

| Mutation | Result |
|---|---|
| the stamp ignored | red: `TestAnUnnamedBuildCallsItselfByItsStamp` |
| `(devel)` accepted as a name | red: the same |
| the stamp read before the version the build set | red: the same |
| an empty stamp accepted | survived, then red once the `go build main.go` case was added |
| the README names a binary directory that does not exist | red: `TestTheReadmesFirstCommandNamesABinaryThatTakesIt`, "has no Go files" |
| the README verb `create` | red: the same, "lists no such verb in its usage" |
| the README pins `@v0.9.0` | red: the same, "found 0" |
| the README names `core/errors` | red: the same, "not a main package" |
| `new` removed from the usage text, format line and argument together | red: the same, "lists no such verb" |

Removing only the format line of the usage text leaves the gate green, and it
is not a removal. The arguments shift: the help prints `gobit new <dir> [flags]`
beside the description of `mcp`, and `%!(EXTRA string=reset, string=confirm)`
at the end.
