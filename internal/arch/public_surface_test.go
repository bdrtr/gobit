package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
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
	facadePackage,
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

// facadePackage is the package at the repository root (ADR 0027).
//
// It is the one published package that is not a contract: it names the modules,
// the plugins and the lifecycle to assemble an installation, and an outside
// program uses it without ever naming any of them.
const facadePackage = "."

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

	// The facade is not under the published tree; it IS the repository root.
	if holdsProductionGo(t, repoRoot) {
		found[facadePackage] = true
	}

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
		// The facade is the exemption, and it is the only one. A composition
		// root exists to name what it assembles: it cannot register the
		// commerce modules without importing them, and they are internal by
		// the decision this rule protects. What it may not do is EXPOSE any of
		// them, which is the rule that actually matters here — see
		// [TestTheFacadeExposesNothingInternal], which enforces it.
		if pkg == facadePackage {
			continue
		}
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
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "bin" ||
				isNestedModule(path) {
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

// outOfTreePlugin is the example module that stands outside gobit.
const outOfTreePlugin = "examples/plugin"

// TestTheOutOfTreePluginCompiles is the surface's only end-to-end proof.
//
// Every other audit here reads the repository's own source, and all of them
// would stay green on a surface nobody outside could actually use: a contract
// whose input type is unexported, a helper the caller needs that was never
// exported, a constructor that only the composition root can reach. None of
// that is an import of internal/ and none of it is a missing declaration — it
// is a surface that compiles here and not there.
//
// So the check is a COMPILATION, from outside. examples/plugin is a separate Go
// module: Go refuses it internal/ by the language's own rule, which makes it the
// one place in this repository where "an outside program can do this" is a fact
// rather than a claim. It registers a payment provider, mounts a route,
// subscribes to an event and returns a typed error — the four things a plugin
// exists to do.
func TestTheOutOfTreePluginCompiles(t *testing.T) {
	t.Parallel()

	// Not a skip: this test is RUNNING under the go toolchain, so a lookup that
	// fails means PATH was changed out from under the run, and the check would
	// otherwise report "the surface is fine" having compiled nothing.
	goTool, err := exec.LookPath("go")
	require.NoError(t, err,
		"the go toolchain is not on PATH, so the published surface was never compiled "+
			"from outside — the one check here that can catch an unusable surface did "+
			"not run")

	dir := filepath.Join(repoRoot, outOfTreePlugin)
	_, err = os.Stat(filepath.Join(dir, goModFileName))
	require.NoError(t, err,
		"%s has no %s of its own.\n"+
			"Without it the example is part of this module, Go allows it internal/, and "+
			"compiling it proves nothing about what an outside author can reach.",
		outOfTreePlugin, goModFileName)

	cmd := exec.CommandContext(t.Context(), goTool, "build", "./...")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err,
		"the out-of-tree plugin no longer compiles against the published surface:\n%s\n"+
			"This is the failure the published surface exists to prevent. Something a "+
			"plugin author needs has stopped being reachable — an unexported type in a "+
			"contract, a helper that was never exported, a signature that changed. Fix "+
			"the surface; do not fix the example by reaching further in, because it "+
			"cannot reach further in.", output)
}

// TestTheFacadeExposesNothingInternal is the self-containment rule for the one
// package that cannot obey the simple version of it.
//
// Every other published package is checked by its IMPORTS, which is a stricter
// line than the defect requires and a cheap one to hold. The facade cannot hold
// it: assembling an installation means naming the modules being assembled, and
// those are internal on purpose.
//
// So the facade is checked by its SIGNATURES instead, which is the rule the
// import check was standing in for all along. An internal type may be mentioned
// inside a function body — an outside program never has to write that type
// there. It may not appear in a parameter, a result, a receiver, the type of an
// exported variable, or the type of an EXPORTED FIELD, because each of those is
// a place the caller has to be able to name.
func TestTheFacadeExposesNothingInternal(t *testing.T) {
	t.Parallel()

	internalPrefix := modulePath + "/internal/"
	files := treeFiles(t, facadePackage)

	checkedFiles, checkedDecls := 0, 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		require.NoError(t, err, "%s could not be parsed", path)
		checkedFiles++

		aliases := map[string]string{}
		for _, spec := range parsed.Imports {
			imported, quoteErr := strconv.Unquote(spec.Path.Value)
			require.NoError(t, quoteErr, "%s has an unreadable import path", path)
			if !strings.HasPrefix(imported, internalPrefix) {
				continue
			}
			alias := imported[strings.LastIndex(imported, "/")+1:]
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			aliases[alias] = imported
		}
		if len(aliases) == 0 {
			continue
		}

		for _, decl := range parsed.Decls {
			for _, node := range exportedSignatures(decl) {
				checkedDecls++
				name, imported := internalUse(node, aliases)
				require.Empty(t, imported,
					"%s exposes %s through %s in an exported signature.\n"+
						"The facade may NAME an internal package — it has to, to assemble "+
						"the installation — but it may not put one where a caller has to "+
						"write the type. An outside program cannot declare a variable of "+
						"that type, implement that interface, or satisfy that contract, and "+
						"the wall it hits is in its own code with no explanation on this "+
						"side.", filepath.Base(path), imported, name)
			}
		}
	}

	require.Positive(t, checkedFiles,
		"no file was read at the repository root, so the facade was never checked.\n"+
			"If the facade moved, this audit is passing on an empty set.")
	require.Positive(t, checkedDecls,
		"the facade declares nothing exported, or it imports nothing internal.\n"+
			"Either way this audit proved nothing: it is written for a package that "+
			"names internal packages AND publishes an API over them.")
}

// exportedSignatures returns the parts of a declaration a caller has to name.
//
// Function BODIES are deliberately left out: that is where an internal package
// may be used, and it is the whole distinction this audit draws. Unexported
// declarations are left out for the same reason — an outside program cannot
// refer to them at all.
func exportedSignatures(decl ast.Decl) []ast.Node {
	var out []ast.Node
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if !d.Name.IsExported() {
			return nil
		}
		if d.Recv != nil {
			out = append(out, d.Recv)
		}
		out = append(out, d.Type)
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch sp := spec.(type) {
			case *ast.TypeSpec:
				if sp.Name.IsExported() {
					out = append(out, exportedTypeParts(sp.Type)...)
				}
			case *ast.ValueSpec:
				exported := false
				for _, name := range sp.Names {
					exported = exported || name.IsExported()
				}
				if !exported {
					continue
				}
				if sp.Type != nil {
					out = append(out, sp.Type)
				}
				for _, value := range sp.Values {
					out = append(out, value)
				}
			}
		}
	}

	return out
}

// exportedTypeParts narrows a type to the parts a caller can write.
//
// A struct's UNEXPORTED fields are the mechanism this repository uses to hold
// internal state on a published type — the facade's own App does exactly that —
// so they are not a leak and must not be reported as one. The same holds for an
// interface's unexported methods, which no outside type can implement anyway.
func exportedTypeParts(expr ast.Expr) []ast.Node {
	switch t := expr.(type) {
	case *ast.StructType:
		var out []ast.Node
		for _, field := range t.Fields.List {
			if fieldIsExported(field) {
				out = append(out, field.Type)
			}
		}

		return out
	case *ast.InterfaceType:
		var out []ast.Node
		for _, method := range t.Methods.List {
			if fieldIsExported(method) {
				out = append(out, method.Type)
			}
		}

		return out
	default:
		return []ast.Node{expr}
	}
}

// fieldIsExported reports whether a struct field or interface method can be
// named from outside the package. An embedded field carries no name of its own
// and is exported when the type it embeds is.
func fieldIsExported(field *ast.Field) bool {
	if len(field.Names) == 0 {
		return true
	}
	for _, name := range field.Names {
		if name.IsExported() {
			return true
		}
	}

	return false
}

// internalUse reports the first internal package a node names, with the
// selector that named it.
func internalUse(node ast.Node, aliases map[string]string) (selector, imported string) {
	name := ""
	ast.Inspect(node, func(n ast.Node) bool {
		if imported != "" {
			return false
		}
		expr, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		qualifier, ok := expr.X.(*ast.Ident)
		if !ok {
			return true
		}
		if path, found := aliases[qualifier.Name]; found {
			name = qualifier.Name + "." + expr.Sel.Name
			imported = path
		}

		return false
	})

	return name, imported
}
