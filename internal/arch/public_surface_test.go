package arch_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// publishedPackages is the surface gobit promises to keep (ADR 0026).
//
// It is written out rather than discovered, and the difference is the whole
// point: a discovered list would grow the moment somebody adds a directory
// under core/, and the addition would be a permanent public commitment made by
// a file move. Written out, publishing is an EDIT — one that shows up in the
// diff next to the ADR it has to be justified by.
//
// The rule that decides membership is not taste. A package belongs here when a
// program outside this repository must NAME it to compile: to implement a
// provider, to register a module, to write a plugin, or to boot the server.
// Everything else stays under internal/, where it can still change.
var publishedPackages = []string{
	"core/audit",
	"core/container",
	"core/db",
	"core/errorreport",
	"core/errors",
	"core/eventbus",
	"core/eventbus/outbox",
	"core/http",
	"core/http/redisguard",
	"core/link",
	"core/module",
	"core/plugin",
	"core/provider",
	"core/query",
}

// publishedTree is the directory the published packages live in.
const publishedTree = "core"

// TestThePublishedPackagesAreTheDeclaredOnes keeps publishing deliberate.
//
// Moving a directory out of internal/ is a one-line operation and its effect is
// permanent: from the next tag on, an outside program can import it, and every
// exported name in it is something this repository has promised. That
// asymmetry — trivial to do, impossible to undo — is exactly the shape of
// change that needs a test standing in front of it.
func TestThePublishedPackagesAreTheDeclaredOnes(t *testing.T) {
	declared := map[string]bool{}
	for _, pkg := range publishedPackages {
		declared[pkg] = true
	}

	found := map[string]bool{}
	root := filepath.Join(repoRoot, publishedTree)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		found[filepath.ToSlash(rel)] = true

		return nil
	})
	require.NoError(t, err, "the published tree could not be walked")

	for pkg := range found {
		require.True(t, declared[pkg],
			"%s is importable by any program in the world and is not in publishedPackages.\n"+
				"Publishing is permanent: a name that ships in a tag cannot be taken back "+
				"without a major version. Either declare it here alongside the reason in "+
				"ADR 0026, or move it under internal/ where it can still change.", pkg)
	}
	for pkg := range declared {
		require.True(t, found[pkg],
			"publishedPackages names %s but no non-test Go file was found there.\n"+
				"A stale entry is not harmless: it is the record of what this repository "+
				"promised, and a promise nobody can look up is one nobody can keep.", pkg)
	}
}

// TestNoPublishedPackageImportsAnInternalOne keeps the surface self-contained.
//
// Go's internal/ rule does not catch this. The restriction is on where the
// IMPORTER sits, and a published package sits inside this module, so it may
// import internal/ and compile perfectly here. The breakage lands downstream,
// and only for some uses: an outside program can call a function whose
// signature mentions an internal type, but it cannot declare a variable of that
// type, implement that interface, or satisfy that contract — so what it hits is
// not a build failure in gobit but a wall in its own code, with no explanation
// on this side.
//
// The rule enforced is the strict one — no import at all, rather than no
// internal type in an exported signature. It is stricter than the defect
// requires and it is true today, which makes it the cheap line to hold. If it
// ever has to be relaxed, relax it to the signature rule, deliberately, and not
// by deleting the test.
func TestNoPublishedPackageImportsAnInternalOne(t *testing.T) {
	internalPrefix := modulePath + "/internal/"

	checked := 0
	for _, pkg := range publishedPackages {
		dir := filepath.Join(repoRoot, pkg)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err, "%s could not be read", pkg)

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
				strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			fset := token.NewFileSet()
			parsed, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			require.NoError(t, parseErr, "%s could not be parsed", path)
			checked++

			for _, spec := range parsed.Imports {
				imported, quoteErr := strconv.Unquote(spec.Path.Value)
				require.NoError(t, quoteErr, "%s has an unreadable import path", path)
				require.False(t, strings.HasPrefix(imported, internalPrefix),
					"%s/%s imports %s.\n"+
						"A published package may not depend on one that stayed internal: "+
						"the dependency compiles here and strands the outside program that "+
						"has to name the internal type. Either publish what is needed — "+
						"deliberately, in publishedPackages and ADR 0026 — or move the "+
						"shared piece into the published tree.", pkg, entry.Name(), imported)
			}
		}
	}

	require.Positive(t, checked,
		"no published file was read, so this audit proved nothing.\n"+
			"The published tree moving or emptying out would leave the check green "+
			"having opened no file.")
}

// TestNoPackageEscapesTheSurface catches a fifth tree appearing.
//
// The two audits above look at core/ and internal/. A package created anywhere
// else — a helpers/ at the repository root, a pkg/ someone reached for out of
// habit — is importable from outside and is covered by neither. This test is
// the backstop: every Go package in the repository is internal, published, a
// command, or a plugin, and there is no fifth kind.
func TestNoPackageEscapesTheSurface(t *testing.T) {
	published := map[string]bool{}
	for _, pkg := range publishedPackages {
		published[pkg] = true
	}

	seen := 0
	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// The walk starts at the repository root, whose own directory entry
			// is named ".." — skipping it on the hidden-name rule would end the
			// walk before it read a single file, and the audit would pass having
			// classified nothing.
			if path == repoRoot {
				return nil
			}
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "bin" {
				return filepath.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		seen++

		switch {
		case strings.HasPrefix(dir, "internal/"), dir == "internal":
			return nil
		case published[dir]:
			return nil
		case strings.HasPrefix(dir, "cmd/"), strings.HasPrefix(dir, "plugins/"):
			return nil
		}

		t.Errorf("%s is in neither internal/, the published tree, cmd/ nor plugins/.\n"+
			"Every Go package in this repository has to be one of those four: the first "+
			"can change, the second is promised, and the last two are programs. A package "+
			"outside all four is importable by the world and audited by nothing.", dir)

		return nil
	})
	require.NoError(t, err, "the repository could not be walked")
	require.Positive(t, seen, "no Go file was classified, so this audit proved nothing")
}
