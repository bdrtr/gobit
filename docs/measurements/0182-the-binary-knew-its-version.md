# The binary knew its version — measured 2026-09-25

The evidence behind [ADR 0182](../adr/0182-an-installed-binary-starts-a-project.md).
Toolchain: `go1.26.6 linux/amd64`.

## 1. The refusal, with the version in hand

A plain `go build ./cmd/server` of this tree at 885adef, with the v0.9.0 cut
uncommitted in it:

```
$ go version -m ./plain | grep -E '^\s+mod'
	mod	github.com/bdrtr/gobit	v0.8.1-0.20260925143814-885adefab01b+dirty
$ ./plain new /tmp/proj1
fatal: cli_new_invalid_input: this binary does not know which github.com/bdrtr/gobit
version a generated project should require, ... It was built without the build
facts (`go build` alone does that; `make build` injects them). ...
```

The binary recorded its own version and refused because it read only the
`-ldflags` facts. `go install github.com/bdrtr/gobit/cmd/server@v0.9.0` builds
the same way, with no Makefile, so after the tag it would have refused too.

## 2. What the toolchain stamps

A throwaway module with one commit, printing the main module's version from
`debug.ReadBuildInfo`:

| Build | Stamped version |
|---|---|
| `go test` | `(devel)` |
| `go build`, clean, untagged commit | `v0.0.0-20260925145847-987b048f048f` |
| `go build`, tag `v0.3.0`, the previous binary untracked in the tree | `v0.3.0+dirty` |
| `go run .` | `(devel)` |
| `go build`, a source file changed | `v0.3.0+dirty` |

An untracked file is enough for `+dirty`. That is why the binaries in section 3
are written outside the clone.

## 3. The route, built

A clone of this repository in a scratch directory, with the change applied and
committed there, and a tag that exists only in that clone:

| Build | Stamp | `gobit new` writes |
|---|---|---|
| `go build`, untagged commit | `v0.8.1-0.20260925150141-dc7fc4a60ec1` | `require github.com/bdrtr/gobit v0.8.1-0.20260925150141-dc7fc4a60ec1` |
| `go build`, local tag `v0.9.0` | `v0.9.0` | `require github.com/bdrtr/gobit v0.9.0` |
| `go build`, `README.md` changed | `v0.9.0+dirty` | refuses; the directory is not created |

## 4. Mutations

Each run against `./internal/scaffold/ ./internal/app/`, with a green baseline
first.

| Mutation | Result |
|---|---|
| the `replace` check dropped | red: `TestTheToolchainStampIsUsedOnlyWhenTheProxyServesIt` |
| `Canonical(v) != v` weakened to `Canonical(v) == ""` | red: the same (`+dirty` and `v0.9` accepted) |
| the dependency lookup disabled | red: the same (the embedding case) |
| the main module accepted whatever its path | red: the same (a binary without gobit) |
| the fallback unwired in `requiredVersion` | red: `TestNewRequiresTheVersionTheToolchainStamped` |

The first form of the second mutation removed the `semver` call and did not
compile, so it was discarded and rewritten.

## 5. The dependency gate

Requiring `golang.org/x/mod` directly turned
`TestEveryDependencyAnEmbedderInheritsIsWrittenDown` red twice: the module was
missing from the direct reasons, and still listed in
`testdata/indirect-dependencies.txt`. `go.sum` did not change.
