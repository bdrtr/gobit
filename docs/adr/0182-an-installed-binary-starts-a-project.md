# ADR 0182 — An installed binary starts a project

**Summary:** When the build injected no facts, `gobit new` requires the library
at the version the Go toolchain stamped into the binary. It costs a direct
require of `golang.org/x/mod`, and it lets `go install …@v0.9.0` start a project.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0182](../measurements/0182-the-binary-knew-its-version.md)

## Context

ADR 0154 made `gobit new` require the version its binary was built from, and
took that version only from the facts the Makefile injects with `-ldflags`.
v0.9.0 is the first tag a generated project can require, and the one route to
its binary that runs no Makefile is `go install
github.com/bdrtr/gobit/cmd/server@v0.9.0`. That binary carries no build facts,
so it would have refused to generate anything. Since Go 1.24 the toolchain
records the main module's version in every binary it builds, so the refusing
binary already knew its own version.

## Decision

When the build injected no facts, `gobit new` reads the version the toolchain
stamped for the library, whether the binary is gobit itself or embeds it. It
uses that version only when the module proxy serves it, and refuses as before
otherwise.

## Consequences

`go install` at a tag and a plain `go build` of a clean checkout now start a
project. At a tag the stamp is the tag. At any other commit it is the
pseudo-version of that commit, the same string the build facts compose. An
embedding project's binary requires the gobit it depends on, which is the
library its templates came from.

Three stamps are refused, and each was measured. `go run` and `go test` write
`(devel)`. A tree with uncommitted changes, an untracked file included, writes
`+dirty`, which the proxy does not serve and whose templates belong to no commit.
A dependency that a `replace` points elsewhere carries a version that names what
was required, not the code that was compiled. The refusal names these three
builds and the ways out.

The build facts still answer first, so every build that named a version before
names the same one. `make build` of a tree with changes still writes the
pseudo-version of its commit.

A pseudo-version of a commit that was never pushed is one the proxy cannot
serve, from either source. The generated project then fails at `go mod tidy`,
as it did before this record.

`golang.org/x/mod` is now required directly, for `semver`. It was already in
every embedder's graph as an indirect module, so no module is added.

A test stands in for the toolchain through `readBuildInfo`, and the route itself
was proved by building: a clean clone built with `go build` at an untagged
commit, at a tag and with a change, each running `new`.

## Rejected

- **The stamp alone, with the build facts removed.** It changes what an
  existing build writes: `make build` of a tree with changes would refuse. This
  record adds a route and changes none.
- **Stripping `+dirty`.** The templates of a tree with changes are not those of
  the commit the stripped version names.
- **The newest tag when nothing is stamped.** ADR 0154 measured it broken, and
  it is still wrong for a binary built after that tag.
- **The main module's version without looking up the library.** In an
  embedding project's binary the main module is the project, not gobit.
