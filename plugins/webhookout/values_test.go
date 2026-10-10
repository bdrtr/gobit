package webhookout

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// payloadValueFloor is the fewest payload values the gate could read and still
// be reading the publishers; under the live count on purpose, as every floor in
// this package is.
const payloadValueFloor = 30

// TestEveryPayloadValueIsAString holds the bus's rule — every payload value is
// a string — to the source of every forwarded topic's publisher, by TYPE
// (ADR 0435).
//
// # Why it exists
//
// The served document says every field of a topic's "data" is a string, and a
// receiver generates a model from that or validates a delivery against it. The
// rule behind the sentence was prose in docs/extending.md: a publisher that
// wrote `"item_count": 3` tomorrow would leave the census, TopicFields, the
// table and the e2e comparison green, every one of them reading keys, while a
// receiver validating against the document refused the delivery and it went to
// the dead pile. On the in-memory bus the same value fails a subscriber's
// `.(string)` too, and so does a value of a NAMED string type, which is why
// the rule here is the plain type string and not "anything that encodes as
// one".
//
// # How it reads them
//
// The payloads are found the way the census finds their keys ([payloadEntries]:
// a same-package builder returning a map literal, or a local map grown by index
// assignments), in the packages that publish a forwarded topic, and those
// packages are type-checked from source: `go list -export` builds the export
// data of what they import, and the standard library's importer reads it, so
// the gate needs nothing beyond the toolchain that runs it. An entry whose
// value cannot be paired with one expression, or whose type is not known, is a
// failure, for the reason an unresolvable payload is: a value this test cannot
// read is one it cannot check.
func TestEveryPayloadValueIsAString(t *testing.T) {
	t.Parallel()

	census := parseProduction(t)
	constants := collectConstants(census)

	checked := map[string]int{}
	values := 0
	var offenders []string
	for _, pkg := range typedPublishers(t, census, constants) {
		for _, f := range pkg.files {
			var enclosing *ast.FuncDecl
			ast.Inspect(f.tree, func(node ast.Node) bool {
				if decl, ok := node.(*ast.FuncDecl); ok {
					enclosing = decl
				}
				topics, data := forwardedPublish(f, enclosing, node, constants, pkg.files)
				if len(topics) == 0 {
					return true
				}
				entries, found := payloadEntries(f, enclosing, data, pkg.files)
				require.Truef(t, found, "%s, %s: %s", f.path, siteName(enclosing), unreadPayload(topics))
				for _, entry := range entries {
					where := pkg.fset.Position(entry.key.Pos())
					require.NotNilf(t, entry.value, "%s: a payload entry of %v has no single value "+
						"expression this test can type", where, topics)
					typ := pkg.info.TypeOf(entry.value)
					require.NotNilf(t, typ, "%s: the type of a payload value of %v is not known", where, topics)
					values++
					for _, topic := range topics {
						checked[topic]++
					}
					if !isPlainString(typ) {
						offenders = append(offenders, fmt.Sprintf("%s: %v carries %s, a %s",
							where, topics, types.ExprString(entry.value), typ))
					}
				}

				return true
			})
		}
	}

	require.GreaterOrEqualf(t, values, payloadValueFloor,
		"only %d payload values were typed; the gate has gone blind", values)
	for _, topic := range ForwardedTopics {
		assert.Positivef(t, checked[topic], "no payload value of %q was typed", topic)
	}
	assert.Empty(t, offenders,
		"a forwarded topic's payload carries a value that is not a plain string. The bus's rule "+
			"(docs/extending.md) and the served document's \"data\" both say string: format the "+
			"value with strconv, time.Format or .String() before it goes in")
}

// isPlainString reports whether a value's type is string itself: an untyped
// string constant, or the predeclared type, and not a type declared over it.
func isPlainString(typ types.Type) bool {
	basic, ok := typ.(*types.Basic)

	return ok && (basic.Kind() == types.String || basic.Kind() == types.UntypedString)
}

// forwardedPublish returns the topics an eventbus.Event literal publishes and
// its Data, when node is one whose topic resolves.
func forwardedPublish(
	f *censusFile, enclosing *ast.FuncDecl, node ast.Node, constants map[string]string, files []*censusFile,
) ([]string, ast.Expr) {
	literal, ok := node.(*ast.CompositeLit)
	if !ok || !isEventLiteral(f, literal) {
		return nil, nil
	}
	name, found := fieldValue(literal, "Name")
	if !found {
		return nil, nil
	}
	topics := resolveName(f, enclosing, name, constants, files)
	if len(topics) == 0 {
		return nil, nil
	}
	data, _ := fieldValue(literal, "Data")

	return topics, data
}

// typedPackage is one publishing package, parsed and type-checked.
type typedPackage struct {
	fset  *token.FileSet
	files []*censusFile
	info  *types.Info
}

// listedPackage is what `go list -json` says of a package that the gate reads.
type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Export     string
	Error      *struct{ Err string }
}

// typedPublishers type-checks the packages that publish a forwarded topic.
func typedPublishers(t *testing.T, census []*censusFile, constants map[string]string) []typedPackage {
	t.Helper()

	var targets []string
	for _, f := range census {
		var enclosing *ast.FuncDecl
		ast.Inspect(f.tree, func(node ast.Node) bool {
			if decl, ok := node.(*ast.FuncDecl); ok {
				enclosing = decl
			}
			if topics, _ := forwardedPublish(f, enclosing, node, constants, census); len(topics) > 0 &&
				!slices.Contains(targets, modulePath+"/"+f.pkgDir) {
				targets = append(targets, modulePath+"/"+f.pkgDir)
			}

			return true
		})
	}
	require.NotEmpty(t, targets, "no package publishes a forwarded topic; the gate has gone blind")

	listed := goListExport(t, targets)
	exports := map[string]string{}
	for _, pkg := range listed {
		exports[pkg.ImportPath] = pkg.Export
	}

	var out []typedPackage
	for _, path := range targets {
		pkg := listedByPath(t, listed, path)
		fset := token.NewFileSet()
		var trees []*ast.File
		var files []*censusFile
		for _, name := range pkg.GoFiles {
			tree, err := parser.ParseFile(fset, filepath.Join(pkg.Dir, name), nil, parser.SkipObjectResolution)
			require.NoError(t, err)
			trees = append(trees, tree)
			files = append(files, censusFileOf(strings.TrimPrefix(path, modulePath+"/"), name, tree))
		}

		info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
		config := types.Config{Importer: importer.ForCompiler(fset, "gc", func(imported string) (io.ReadCloser, error) {
			export, ok := exports[imported]
			if !ok || export == "" {
				return nil, fmt.Errorf("go list gave no export data for %s", imported)
			}

			return os.Open(export)
		})}
		_, err := config.Check(path, fset, trees, info)
		require.NoErrorf(t, err, "%s does not type-check from its export data", path)

		out = append(out, typedPackage{fset: fset, files: files, info: info})
	}

	return out
}

// censusFileOf wraps a parsed file the way [parseProduction] does.
func censusFileOf(pkgDir, name string, tree *ast.File) *censusFile {
	file := &censusFile{path: pkgDir + "/" + name, pkgDir: pkgDir, tree: tree, imports: map[string]string{}}
	for _, imported := range tree.Imports {
		importPath := strings.Trim(imported.Path.Value, `"`)
		local := filepath.Base(importPath)
		if imported.Name != nil {
			local = imported.Name.Name
		}
		file.imports[local] = importPath
	}

	return file
}

// goListExport runs `go list -export -deps -json` over the packages.
//
// The toolchain is the one on PATH, and it has to be the one that built this
// test: export data is read by the standard library's importer of the release
// that compiled it, and a mismatch is a parse error rather than a finding.
func goListExport(t *testing.T, paths []string) []listedPackage {
	t.Helper()

	goTool, err := exec.LookPath("go")
	require.NoError(t, err, "the go toolchain is not on PATH; the payload types cannot be read")

	version, err := exec.CommandContext(t.Context(), goTool, "env", "GOVERSION").Output()
	require.NoError(t, err)
	require.Equalf(t, runtime.Version(), strings.TrimSpace(string(version)),
		"the go on PATH is %s and this test was built by %s; the export data one writes is "+
			"read by the other's importer. Run both under the release go.mod names "+
			"(GOTOOLCHAIN), as every make lane does", strings.TrimSpace(string(version)), runtime.Version())

	var stderr bytes.Buffer
	cmd := exec.CommandContext(t.Context(), goTool,
		append([]string{"list", "-export", "-deps", "-json=ImportPath,Dir,GoFiles,Export,Error"}, paths...)...)
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	require.NoErrorf(t, err, "go list failed: %s", stderr.String())

	var listed []listedPackage
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg listedPackage
		err := decoder.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		require.Nilf(t, pkg.Error, "go list reported %s: %v", pkg.ImportPath, pkg.Error)
		listed = append(listed, pkg)
	}

	return listed
}

// listedByPath finds a package go list reported.
func listedByPath(t *testing.T, listed []listedPackage, path string) listedPackage {
	t.Helper()

	for _, pkg := range listed {
		if pkg.ImportPath == path {
			return pkg
		}
	}
	require.Failf(t, "go list did not report a package", "%s", path)

	return listedPackage{}
}
