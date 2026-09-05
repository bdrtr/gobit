# ADR 0027 — The composition root is part of the library, and the binary is fifteen lines

- **Status:** Accepted
- **Date:** 2026-09-05
- **Phase:** after the roadmap
- **Closes:** the second half of ADR 0025

## Context

ADR 0026 published fourteen packages and proved, by compiling one, that an
out-of-tree PLUGIN is now possible. It also recorded what was still missing: an
out-of-tree APPLICATION was not, because the composition root was a program.

The measurement that decided this:

| what | non-test lines |
| --- | --- |
| cmd/server, total | 3,518 |
| — the lifecycle (config, container, modules, plugins, routes, jobs, listen) | 2,308 |
| — operator subcommands (migrate, stuck, recover, jobs) | 1,210 |

Both halves were in `package main`, and that is the whole problem. A `main`
package cannot be imported. So an embedding project had exactly one way to add
its own module: copy those 3,518 lines and edit them — the fork model ADR 0025
rejected, reached not by choice but by where the code sat.

The operator subcommands are the sharper half of the argument. `migrate status`
has to build the same module registry the server builds, because a module's
migration source is reachable only through the module VALUE. An embedding
project that added a module and could not run `migrate status` over it would
have a schema its own tooling cannot see.

## Decision

**The lifecycle moves to `internal/app`, and a published facade at the module
root calls it.** The binary becomes the smallest program that can run gobit:

```go
func main() {
	if err := gobit.New().Version(version).Main(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
```

`cmd/server` is now exactly that, and is kept that way on purpose: it is the
example an embedding project copies, so anything it were allowed to do that an
outside program cannot do would be a lie about what the library offers.

**The facade is four methods** — Version, Add, Use, Main. Nothing else is
published, and in particular the lifecycle is NOT: an installation's boot order,
its shutdown order and its startup refusals are things this repository has to
stay free to change. What the facade hands over — the module and plugin
contracts — is already published by ADR 0026 and is not re-exported here, which
would double the surface without adding a capability.

**Add and Use are additive, with no way to remove.** A module missing from an
installation is a set of tables that does not exist; letting a caller subtract
one would make "which schema does gobit have" unanswerable, and the modules that
resolve it by name would not know it had gone.

## Why the facade may import internal/ when nothing else may

ADR 0026's self-containment rule says a published package may not import an
internal one. The facade cannot obey it: assembling an installation means naming
what is being assembled, and the commerce modules are internal by the decision
that rule protects.

So the facade is checked by a different rule, and it is the rule the import ban
was standing in for all along: **an internal type may be named inside a function
body and nowhere a caller has to write it** — not in a parameter, a result, a
receiver, the type of an exported variable, or the type of an exported field.
internal/arch enforces exactly that, and it is the only exemption.

## What this cost, and what it says about the audits

The move itself was mechanical. Everything that broke was a place where a PATH
was a value rather than a reference — the same class ADR 0026 recorded, found
again one layer down:

- **The reachability graph started at a function named `main`.** With the entry
  point renamed the graph was empty, and an empty graph declares every
  registration unreachable.
- **A per-package audit read the SUBTREE of a package's directory.** Harmless
  while every package sat several levels down; the moment a package existed at
  the repository ROOT, one call audited every file in the repository under the
  root package's import aliases and reported violations in modules that have an
  audit of their own. A Go package is one directory, and the audit now reads one
  directory.
- **The reference resolver built import paths by joining.** The root package's
  directory is ".", so it produced ".../gobit/." — an import path nothing writes
  — and every link into the facade reported a missing package.
- **Sixty prose references** named cmd/server as the composition root. The ones
  describing the present were moved; the CHANGELOG and the original plan
  describe a past in which they were true and were left alone.

None of these were caught by a compiler and all of them failed OPEN — the audit
kept passing over a smaller set. That is the recurring shape, and the reason the
tree list is now written once and checked (ADR 0026) rather than copied.

## Consequences

- **An out-of-tree application is now possible.** It is not yet proved the way
  the plugin is; examples/plugin compiles a plugin, and the equivalent for an
  application is a starter module that boots one. That is the obvious next
  example to add.
- **The operator subcommands come with the library.** An embedding project gets
  `migrate status`, `stuck`, `recover` and `jobs` over its own modules for free,
  which is the half of this move that would have been most expensive to
  reproduce by hand.
- **`-ldflags -X main.version` still works.** The version is a variable in the
  binary and reaches the library through the facade, so the build tooling did
  not change and an embedder can pass its own version the same way.
- **The composition root stays a single "knows everything" point.** Moving it
  did not weaken ADR 0001: it still names every module explicitly, and the audit
  that a module NOT named there does not exist in any installation now points at
  internal/app.

## Reopening

Reopen if an embedding project needs to change something the facade does not
expose — a different boot order, a module replaced rather than added, a
subcommand of its own. The first two are refusals this ADR makes deliberately;
the third is a gap and would widen the facade rather than reopen the decision.
