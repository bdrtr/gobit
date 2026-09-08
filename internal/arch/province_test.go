package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds ADR 0067's rule: the word "province" is introduced with its
// meaning, never on its own.
//
// # The fault it is built on
//
// Measured 2026-09-08 (D33). The tax schema defines a province region as the
// sub-country unit under a country root — for Turkey an il — and states it in a
// migration header with a uniqueness rule behind it. The end-to-end shipping
// test filed `Province: "Kadikoy"` and said why in its own assertion: a
// domestic carrier prices on the district. Kadikoy is an ilce.
//
// Both readings compiled, both suites were green, and the two had never met:
// the cart's tax request sends the province ALWAYS EMPTY, so nothing had ever
// compared them. The cart's own field carried no comment at all, which is how
// the second reading arrived — not by argument, but by a field being added
// beside the others with nothing said.
//
// # What this gate holds, and what it does not
//
// It refuses a HAND-WRITTEN declaration of the word with no doc comment. It
// does NOT read the comment and cannot tell a right meaning from a wrong one;
// what it removes is the SILENT introduction, which is the step the second
// reading actually took. A reader who writes "the district" in the comment gets
// past this gate and is then arguing in the open, where ADR 0067 answers them.
//
// # Why generated files are out
//
// sqlc writes them from the schema and a comment here would be erased on the
// next generation. The schema's own header is where their meaning lives.

// provinceFieldNames are the spellings the word takes on a struct field.
var provinceFieldNames = []string{"Province", "ProvinceCode"}

// TestEveryProvinceFieldSaysWhatItMeans is the gate.
func TestEveryProvinceFieldSaysWhatItMeans(t *testing.T) {
	t.Parallel()

	scanned, undocumented := 0, 0
	for _, tree := range productionTrees {
		for _, file := range productionFiles(t, filepath.Join(repoRoot, tree)) {
			if !strings.HasSuffix(file, ".go") {
				continue
			}
			rel := strings.TrimPrefix(filepath.ToSlash(file), filepath.ToSlash(repoRoot)+"/")
			if seenProvinceFile[rel] {
				continue
			}
			seenProvinceFile[rel] = true

			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
			require.NoError(t, err, "%s could not be parsed", file)
			if generatedFile(parsed) {
				continue
			}

			ast.Inspect(parsed, func(node ast.Node) bool {
				structType, ok := node.(*ast.StructType)
				if !ok || structType.Fields == nil {
					return true
				}
				// A comment ahead of a RUN of fields documents all of them —
				// "Address1, Address2, City, Province and PostalCode are …" is
				// how this repository writes an address, and Go hangs that
				// comment on the first field only. So a field counts as spoken
				// for when an EARLIER field's comment names it.
				spokenFor := map[string]bool{}
				for _, field := range structType.Fields.List {
					doc := ""
					if field.Doc != nil {
						doc = field.Doc.Text()
					}
					for _, candidate := range provinceFieldNames {
						if strings.Contains(doc, candidate) {
							spokenFor[candidate] = true
						}
					}
					for _, name := range field.Names {
						if !contains(provinceFieldNames, name.Name) {
							continue
						}
						scanned++
						if strings.TrimSpace(doc) != "" || spokenFor[name.Name] {
							continue
						}
						undocumented++
						assert.Fail(t, "a province field says nothing about what it means",
							"%s:%d declares %s with no comment.\n"+
								"The word carries two readings in this tree's history — the "+
								"sub-country unit the tax schema defines (an il), and the "+
								"district a domestic carrier prices on (an ilce) — and the "+
								"second one arrived exactly this way, by a field appearing "+
								"beside the others with nothing said. ADR 0067 settles which "+
								"one it is; this gate only refuses introducing it in silence.",
							rel, fset.Position(name.Pos()).Line, name.Name)
					}
				}

				return true
			})
		}
	}

	require.Positive(t, scanned,
		"no province field was found at all; the scan has gone BLIND. The word is "+
			"declared in the cart, order, inventory and tax modules, so finding none "+
			"means the field name drifted or the walk stopped descending.")
	assert.Zero(t, undocumented)
}

// seenProvinceFile keeps the overlapping production trees from reporting one
// file twice.
var seenProvinceFile = map[string]bool{}

// generatedFile reports whether the file was written by a generator.
func generatedFile(file *ast.File) bool {
	for _, group := range file.Comments {
		for _, line := range group.List {
			if strings.HasPrefix(line.Text, "// Code generated ") {
				return true
			}
		}
	}

	return false
}

// contains is membership over a small fixed list.
func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}

	return false
}
