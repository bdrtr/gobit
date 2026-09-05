package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// pprofImportPath is the package whose IMPORT is the thing being audited.
//
// Importing it runs an init that registers six endpoints on
// [net/http.DefaultServeMux]. The import is the whole event; no call is needed.
const pprofImportPath = "net/http/pprof"

// profilingHandlerFile is the one file allowed to import it.
const profilingHandlerFile = "core/http/profiling.go"

// defaultMuxNames are the net/http identifiers that READ OR WRITE the default mux.
//
// Handle and HandleFunc write to it. ListenAndServe serves it whenever the
// handler it is given is nil, which is the shape that turns an unrelated
// listener into a profile endpoint without anybody typing "pprof".
var defaultMuxNames = map[string]struct{}{
	"DefaultServeMux":   {},
	"Handle":            {},
	"HandleFunc":        {},
	"ListenAndServe":    {},
	"ListenAndServeTLS": {},
}

// TestTheDefaultServeMuxIsNeverUsed keeps the profiles off every other listener.
//
// net/http/pprof publishes itself through a package-level global. Anything that
// ends up serving that global — a health listener, a metrics port, a plugin's
// own little server — starts handing out heap dumps, and the code that caused
// it contains no mention of profiling at all. The two halves are audited
// together because either one alone is harmless: the import is inert while
// nothing serves the default mux, and serving the default mux is empty while
// nothing imports pprof.
//
// gobit's own profiles are on a mux built by hand
// ([github.com/bdrtr/gobit/core/http.ProfilingHandler]) and on a
// listener of their own, so this rule costs the repository nothing to keep.
func TestTheDefaultServeMuxIsNeverUsed(t *testing.T) {
	t.Parallel()

	for _, tree := range productionTrees {
		for _, file := range treeFiles(t, tree) {
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("%s could not be parsed: %v", file, err)
			}

			relative, err := filepath.Rel(repoRoot, file)
			if err != nil {
				t.Fatalf("%s could not be made relative to %s: %v", file, repoRoot, err)
			}
			relative = filepath.ToSlash(relative)

			assertNoDefaultMuxUse(t, relative, parsed)
			assertPprofStaysInItsFile(t, relative, parsed)
		}
	}
}

// assertNoDefaultMuxUse fails when the file touches the default mux.
func assertNoDefaultMuxUse(t *testing.T, file string, parsed *ast.File) {
	t.Helper()

	local := localNameOf(parsed, "net/http")
	if local == "" {
		return
	}

	ast.Inspect(parsed, func(n ast.Node) bool {
		selector, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != local {
			return true
		}
		if _, forbidden := defaultMuxNames[selector.Sel.Name]; forbidden {
			t.Errorf("%s uses %s.%s, which reaches net/http's DEFAULT mux.\n"+
				"That mux is a process-wide global: whatever registers on it — pprof being "+
				"the one that does so from an import alone — becomes reachable from every "+
				"listener that serves it. Build a mux and pass it explicitly.",
				file, local, selector.Sel.Name)
		}

		return true
	})
}

// assertPprofStaysInItsFile fails when net/http/pprof is imported anywhere else.
func assertPprofStaysInItsFile(t *testing.T, file string, parsed *ast.File) {
	t.Helper()

	if localNameOf(parsed, pprofImportPath) == "" || file == profilingHandlerFile {
		return
	}

	t.Errorf("%s imports %s, which is only allowed in %s.\n"+
		"The import alone registers six endpoints on net/http's default mux. Keeping it "+
		"in one file is what makes the rule above auditable.", file, pprofImportPath, profilingHandlerFile)
}

// localNameOf returns the name the file refers to an imported package by, or
// the empty string when the file does not import it.
func localNameOf(parsed *ast.File, path string) string {
	for _, imp := range parsed.Imports {
		if strings.Trim(imp.Path.Value, `"`) != path {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}

		return path[strings.LastIndex(path, "/")+1:]
	}

	return ""
}
