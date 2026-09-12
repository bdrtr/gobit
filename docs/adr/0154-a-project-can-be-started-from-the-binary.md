# ADR 0154 — A project can be started from the binary

**Summary:** `gobit new <dir>` writes a project that embeds gobit, from templates
embedded in the binary, requiring the library at the version that binary was
built from. It costs a template tree that is a second copy of things this
repository already has, and it gives a stranger a working shop in two commands.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

gobit is a library, and its front door was shut. Nothing in the repository wrote
a project: an author's first step was to read `cmd/server/main.go`, guess a
`go.mod`, guess which settings exist, and guess which services to run.

Measured while choosing this slice, and it is the reason the shape below looks the
way it does: `github.com/bdrtr/gobit@latest` resolves to v0.8.0, and that tag does
NOT contain the root package — the facade landed after it. So `require
github.com/bdrtr/gobit v0.8.0` plus `import "github.com/bdrtr/gobit"` fails at
`go mod tidy` with "does not contain package", and `@latest` fails the same way.
The one version of this library anybody can actually import today is a
pseudo-version of a commit.

Measurement: [measurements/0154](../measurements/0154-the-front-door-was-shut.md)

## Decision

`gobit new <dir>` renders a project from templates embedded in the binary, and
the generated `go.mod` requires the library at the version the GENERATING BINARY
was built from — a tag when it was built from one, a pseudo-version of its commit
otherwise. A build that knows neither refuses rather than guessing, and points at
`-replace` for a checkout.

## Consequences

The verb reads no configuration, and it is the only one that does not. Every
other subcommand calls `config.Load`, which is right — run inside the container
they are already pointed at the right database — and this one runs on a laptop
where nothing exists yet.

A build that cannot name a version REFUSES. The alternatives were measured rather
than argued about: `@latest` and the newest tag resolve to a release without the
published surface, so the generated project fails at its first command with an
error about a package that is missing rather than a version that is wrong.

Two template traps are closed by the file NAMES, and both were measured. A
template named `go.mod` removes its whole directory from the embedded set —
silently, with `all:` not lifting it — so a binary would compile, embed part of
the tree and generate an incomplete project. And a template ending in `.go` is
parsed by two repository gates that walk every non-test Go file, plus by
`go build ./...`. The mapping from template name to written name is therefore
data, checked against the embedded set in both directions at init.

The generated project is proved by being built and RUN, not by being inspected.
What that lane cannot prove is named rather than implied: it rewrites the go.mod
to point at this checkout, so it cannot tell "the template works at the version
it pins" from "the template works at the tip of this tree" — and today those
differ absolutely. Closing that needs a release.

The language gate now scans `.tmpl`. A template's prose is rendered into somebody
else's project, so Turkish left in one does not stay here — it ships.

Eleven mutations, eleven bites, two only after the check was fixed. Removing
`new` from the help text left the derived verb gate green, because a `Contains`
on a three-letter word is satisfied by the flags section below — it looks for the
usage LINE now. And the go-directive check passed the directive in and asserted
it came back, which proves the template writes what it is given and nothing about
what the command gives it; its subject is the command's own constant now.

## Rejected

- **Embedding `examples/starter`.** The row's own proposal, and it is impossible:
  its directory contains a `go.mod`, which removes it from the embedded set.
- **Pinning the newest tag, or `@latest`.** Measured broken, both.
- **Copying `.env.example` into the project.** It is 833 lines of Turkish and the
  single written record of what settings exist; the generated file names the
  minimum and a gate checks every key against that record.
- **A Dockerfile in the generated project.** A deployment builds its own image,
  and a wrong one shipped as a starting point is worse than none.
