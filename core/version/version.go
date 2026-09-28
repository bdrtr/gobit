// Package version reads which gobit release a binary was built from (ADR 0224).
//
// A binary that embeds gobit carries the library's version in the build
// information the Go toolchain stamps: the main module's own when the binary is
// gobit, a dependency's when it embeds it. `gobit new` reads it to require the
// release it was built from (ADR 0182), and the plugin registry reads it to
// check the releases a plugin names ([github.com/bdrtr/gobit/core/plugin.CoreRequirement]).
package version

import (
	"runtime/debug"

	"golang.org/x/mod/semver"
)

// Module is the library's module path.
const Module = "github.com/bdrtr/gobit"

// readBuildInfo is the toolchain's record of this binary, a variable so a test
// can stand in for a build it cannot perform.
var readBuildInfo = debug.ReadBuildInfo

// Library returns the gobit version this binary was built with, or "" when its
// build does not say which: a `go run` or `go test` build, a tree with
// uncommitted changes, or a `replace` pointing the library elsewhere.
func Library() string {
	info, ok := readBuildInfo()
	if !ok {
		return ""
	}
	return Of(info)
}

// Of returns the library version the Go toolchain recorded in a binary's
// build information, or "" when that version is not one the module proxy serves.
//
// Since Go 1.24 `go build` stamps the main module's version from version control
// — the tag when the commit carries one, the pseudo-version of the commit
// otherwise — and `go install <path>@<version>` stamps the version it fetched.
// That second route is the one no Makefile runs, so it is the one that injects
// no build facts. The library is looked up by its own path, whether this binary
// IS gobit (the main module) or embeds it (a dependency); in the second case the
// dependency's version is exactly the library the embedded templates came from.
//
// Three stamps are refused rather than used, and each was measured:
//   - "(devel)", which `go run` and `go test` write: no version at all;
//   - a version with build metadata, "+dirty" for a tree with uncommitted
//     changes: the proxy serves no such version, and the templates are not
//     those of any commit;
//   - a dependency a `replace` points elsewhere: its version names what was
//     required, not the code that was compiled.
func Of(info *debug.BuildInfo) string {
	if info == nil {
		return ""
	}

	library := &info.Main
	if library.Path != Module {
		library = nil
		for _, dep := range info.Deps {
			if dep.Path == Module {
				library = dep

				break
			}
		}
	}
	if library == nil || library.Replace != nil {
		return ""
	}

	// Canonical drops build metadata and completes a shorthand, so a version
	// that survives it unchanged is one the proxy can be asked for.
	version := library.Version
	if version == "" || semver.Canonical(version) != version {
		return ""
	}

	return version
}
