# What an embedder could fetch — measured 2026-10-15

Evidence for [ADR 0441](../adr/0441-a-contrib-module-is-tagged-with-the-release-it-requires.md)
and gap D295. Read on v0.13.0 (bf77793c), through the public proxy
(`GOPROXY=https://proxy.golang.org`), from an empty module `probe` made with
`go mod init` in a scratch directory, Go 1.27.1.

## 1. The tags

`git tag -l 'contrib/*'` printed nothing, and
`https://proxy.golang.org/github.com/bdrtr/gobit/contrib/identity-session/@v/list`
answered an empty list. Both contrib go.mod files required
`github.com/bdrtr/gobit v0.0.0`; the passkey module also required
`github.com/bdrtr/gobit/contrib/identity-session v0.0.0`. Each carried a
replace into this tree.

## 2. A project that has not pinned gobit

```
go get github.com/bdrtr/gobit/contrib/identity-session@latest
go: github.com/bdrtr/gobit/contrib/identity-session imports
	github.com/bdrtr/gobit/core/personaldata: github.com/bdrtr/gobit@v0.0.0: reading https://proxy.golang.org/github.com/bdrtr/gobit/@v/v0.0.0.zip: 404 Not Found
```

`@bf77793c`, the v0.13.0 commit, answered the same.

## 3. A project that pinned gobit first

```
go get github.com/bdrtr/gobit@v0.13.0
go get github.com/bdrtr/gobit/contrib/identity-session@bf77793c
go: added github.com/bdrtr/gobit/contrib/identity-session v0.0.0-20261015000518-bf77793c3b9f
```

A `main.go` importing the module built, exit 0. Minimal version selection
kept the project's v0.13.0 over the module's v0.0.0, which said nothing about
the release the module needs.

The same steps from a fresh module pinned at `github.com/bdrtr/gobit@v0.10.0`
kept v0.10.0 (`go.mod`: `github.com/bdrtr/gobit v0.10.0`, the session module
at the bf77793c pseudo-version), and the same `main.go` built against it with
no message: v0.13.0's session module on a v0.10.0 core, a pairing nothing
refused at build time and the v0.11.0 upgrade paragraph in
`docs/operating.md` had to warn about by hand.

## 4. After

With each module requiring `github.com/bdrtr/gobit v0.13.0` (and the passkey
module the session module at the same version), `go mod tidy` and `go build
./...` pass in both, and `examples/starter`'s tidy lifts its own gobit
requirement from `v0.0.0` to `v0.13.0` through the session module: the lift an
embedder's `go get` makes. `TestEveryContribModuleRequiresTheRelease` fails on
the old go.mod files with three lines, one per requirement at `v0.0.0`.

A cut that moves the session module's requirement and forgets the starter is
refused by the starter's own build: with the session module requiring
`v0.13.1` and the starter left at `v0.13.0`, `go build ./...` and `go vet ./...`
in `examples/starter` both stop on `go: updates to go.mod needed`, and
`make lint` walks every module in `SEPARATE_MODULES`. CI's tidy check reads
the root module alone.
