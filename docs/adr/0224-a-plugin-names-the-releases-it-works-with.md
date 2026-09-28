# ADR 0224 — A plugin names the releases it works with

**Summary:** A plugin can name the gobit releases it works with, and the
registry refuses to install it with any other; the library's own release is
read from the build by a published package.

- **Status:** Accepted
- **Date:** 2026-09-28

Measurement: [measurements/0224](../measurements/0224-the-release-a-plugin-expects.md)

## Context

A plugin is compiled in and implements `Name` and `Setup` (core/plugin).
A plugin in a module of its own requires gobit in its `go.mod`, which sets the
lowest release a build may use and nothing above it, while a release before
1.0.0 may break a plugin in a minor version (ADR 0036), and a name looked up at
run time breaks with no compiler to see it. The library's release was read from
the build only for `gobit new` (ADR 0182), in an internal package.

## Decision

A plugin that implements `core/plugin.CoreRequirement` names a range of
releases, comparisons that must all hold, and `Registry.Install` refuses it
before any plugin's Setup when the binary's release is outside the range or the
range cannot be read. The release is `core/version.Library`, the rule `gobit
new` used, moved into the published tree.

## Consequences

The interface is optional, so no plugin changes and the `Plugin` interface
keeps its two methods (ADR 0026). A range takes `>=`, `<=`, `>`, `<` and `=`
before a semantic version, with or without its `v`, separated by spaces or
commas; a pre-release sorts before its release. An outside release is refused
with `plugin_core_unsupported` and an unreadable range with
`plugin_core_range_invalid`, and the application does not start.

A build that does not stamp its release — `go run`, `go test`, a `replace`, a
tree with uncommitted changes — installs the plugin and logs that its range was
not checked; a range is still read there, so a malformed one fails in
development as well. `core/version` is the twentieth published package and
`core/plugin` now imports `golang.org/x/mod/semver`, which every module
requiring gobit already has in its graph; the out-of-tree example's `go.sum`
gained it. The example plugin names its range.

## Rejected

- **A method on `Plugin`.** Every plugin would stop compiling for a check most
  do not need.
- **The range in `go.mod` alone.** An upper bound cannot be written there and
  `exclude` applies only in the main module.
- **Checking at `Add`.** It returns nothing and is published; an error there
  would change its signature.
- **Refusing an unstamped build.** Every test and every `go run` would refuse
  every plugin that names a range.
