package arch_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file guards a contract that is held by two string literals in two trees
// with nothing between them.
//
// # What the contract is
//
// A module opens a read surface to the Query layer (ADR 0004) and NAMES the
// fields it serves: inventory declares FieldAvailableQuantity = "available_quantity"
// beside the provider that fills it. Those names are a published vocabulary — the
// thing ADR 0004 exists to let modules share without importing one another.
//
// The admin panel imports nothing from internal/modules, so when it wants a
// provider's field it SPELLS THE NAME AGAIN. Nothing compares the two spellings
// and Go cannot: the panel may not import the constant, which is the whole point
// of ADR 0004.
//
// The re-spelling takes two shapes and this gate reads both, which is why the
// pattern below matches a `json:` tag OR a map index. Today the panel is
// server-rendered and reads the Query layer in process, so the names are Go
// string constants indexing a record. ADR 0030 decided it becomes a single-page
// client of `/admin/v1`, where the same names would sit in struct tags across an
// HTTP boundary.
//
// **That decision is ACCEPTED AND NOT BUILT, and this paragraph used to say
// otherwise** — it opened "The admin panel is an API CLIENT (ADR 0030) ... on the
// other side of an HTTP boundary", in the present tense, about a boundary that
// does not exist. ADR 0031 has it right, in the future tense: "once the panel is
// a client of /admin/v1". The gate was never wrong; the sentence describing why
// it was needed was. See D34.
//
// The correction matters beyond tidiness. A reader deciding how to add a screen
// reads this before they read the panel, and a document that describes a decided
// future as a present fact sends them to build on the shape that was decided
// away.
//
// # Why it is worth a gate
//
// A rename on the module side leaves both sides compiling and both test suites
// green. What breaks is a number on a screen: the panel decodes a field that is
// no longer sent, gets the zero value, and shows a variant with no stock. No
// error, no log line — the same shape as every defect this repository has been
// caught by this week.
//
// # Why the list is written out AND checked against the tree
//
// The map below is the panel's declared dependencies. A hand-written list is
// right on the day it is written, so the second half of this audit reads the
// panel and refuses a dependency that is not declared. That is the same pairing
// the e-mail fold audit uses, for the same reason: a comparison is only as wide
// as its idea of who is being compared.

// panelDecodedProviderFields names every Query-provider field the admin panel
// decodes by name, and the module that publishes it.
//
// One entry today, and it arrived with ADR 0040: the "in stock" definition is
// computed over inventory's available quantity, and the panel already showed that
// number on the variant screen before the definition existed.
var panelDecodedProviderFields = map[string]string{
	"available_quantity": "inventory",
}

// # The second consumer, and it is not an API client
//
// ADR 0040 opened a pairing of the same shape INSIDE the module tree, and it is
// the reason the file no longer talks only about the panel. "In stock" is a
// CATALOG answer computed over an INVENTORY fact: two of its three inputs are
// product's own columns and the third is inventory's available quantity, which
// arrives as a loosely typed Query record. The catalog may not import inventory
// (Principle 2.1), so it spells "available_quantity" as a string of its own --
// the same two-literals-and-nothing-between-them contract the panel has, minus
// the HTTP boundary.
//
// The failure is the same and it is quiet in a direction that matters: a rename
// on inventory's side would make every counted variant read as having no
// quantity, so ADR 0040's definition would answer "out of stock" for the whole
// catalog. Nothing would error, the storefront would return 200, and the badge
// and the filter would agree with each other about the wrong answer.
//
// ADR 0041 then added a SECOND consumer of the same road, and this audit's job
// widened with it: the price filter reads six names off pricing's price
// sub-records. Pricing publishes them as UNEXPORTED constants, so the scan below
// cannot bind them to anything -- and that hole is REGISTERED rather than
// omitted, in [catalogUnboundForeignFields], with a test that fails the day
// pricing exports them and the entry has to move.

// catalogReadForeignFields names every field the CATALOG reads out of another
// module's loose record and can be bound to a published constant.
//
// One entry, and ADR 0040 grants exactly that one: the decision says in as many
// words that it "does not license a second". The map is what turns that sentence
// into something a test can fail on.
var catalogReadForeignFields = map[string]string{
	"available_quantity": "inventory",
}

// catalogUnboundForeignFields names the fields the catalog reads that NO module
// publishes as a constant, and the module each belongs to.
//
// These are pricing's price sub-record fields, and ADR 0041's filter cannot be
// evaluated without them: the amount, the currency it is in, the quantity tier
// it covers and whether it belongs to a price list are the four halves of "the
// BASE price at quantity one, in the request's currency".
//
// They are listed here rather than in [catalogReadForeignFields] because they
// cannot be checked: pricing declares them as unexported constants beside its
// provider (fieldAmount, fieldCurrencyCode, ...), so there is no published
// vocabulary for this audit to compare against. Writing them into the bound map
// would produce a green assertion about a name nothing publishes, which is worse
// than no assertion at all.
//
// What CAN be checked is that the hole is still the hole, and
// [TestEveryUnboundForeignFieldIsStillUnpublished] does exactly that: the day
// pricing exports these names, that test fails and the entry has to move up.
// Until then the honest reading of this block is that a rename inside pricing
// would silently empty a price-filtered catalog, and the fix is one word per
// constant in a file this audit cannot reach on its own.
var catalogUnboundForeignFields = map[string]string{
	"prices":        "pricing",
	"price_list_id": "pricing",
	"currency_code": "pricing",
	"amount":        "pricing",
	"min_quantity":  "pricing",
	"max_quantity":  "pricing",
}

// catalogModuleDir is the module tree the catalog's foreign-field reads live in.
const catalogModuleDir = "product"

// providerFieldConstant matches a published field name in a module's provider.
var providerFieldConstant = regexp.MustCompile(`Field[A-Z][A-Za-z]*\s*=\s*"([a-z_]+)"`)

// foreignFieldConstant matches a field name the catalog declares as ANOTHER
// module's.
//
// The prefix is the whole mechanism, and it is why the catalog's constants are
// named "foreign<Name>" rather than "foreignField<Name>": the second spelling
// would also match [providerFieldConstant], and this audit would read the
// catalog as PUBLISHING the very names it borrows -- an audit comparing an input
// against itself.
var foreignFieldConstant = regexp.MustCompile(`foreign[A-Z][A-Za-z]*\s*=\s*"([a-z_]+)"`)

// panelFieldReference matches the two ways the panel names a field it decodes: a
// struct tag, and an index into a loosely typed record.
var panelFieldReference = regexp.MustCompile(`json:"([a-z_]+)|\[\s*"([a-z_]+)"\s*\]`)

// TestEveryProviderFieldThePanelDecodesIsStillPublished is the guard.
func TestEveryProviderFieldThePanelDecodesIsStillPublished(t *testing.T) {
	t.Parallel()

	published := publishedProviderFields(t)

	require.NotEmpty(t, published,
		"no Query-provider field constant was found in any module; the scan has gone BLIND and "+
			"this audit would pass whatever the panel decoded")

	for field, module := range panelDecodedProviderFields {
		names, serves := published[module]

		assert.Truef(t, serves,
			"the panel decodes %q and this map says the %s module publishes it, but that module "+
				"publishes no provider fields at all. Either the module was renamed or its "+
				"provider is gone; the panel is decoding a field nothing sends.", field, module)

		if serves {
			assert.Containsf(t, names, field,
				"the panel decodes %q but the %s module no longer publishes a field by that "+
					"name.\nThe panel cannot import the module's "+
					"constant, so a rename on the module side leaves both trees compiling and "+
					"both suites green — and shows a screen with a zero where the number was.\n"+
					"Rename the panel's side too, or drop the entry if the panel stopped "+
					"reading it. Published today: %v", field, module, names)
		}
	}
}

// TestEveryProviderFieldThePanelReadsIsDeclared is the half that survives a NEW
// dependency.
//
// The map above is hand-written. This reads the panel's own source and fails when
// it names a field some module publishes without saying so — which is the moment
// a second screen starts depending on a vocabulary nobody is comparing.
func TestEveryProviderFieldThePanelReadsIsDeclared(t *testing.T) {
	t.Parallel()

	published := publishedProviderFields(t)
	everyName := map[string]string{}

	for module, names := range published {
		for _, name := range names {
			everyName[name] = module
		}
	}

	read := panelReadFields(t)

	require.NotEmpty(t, read,
		"no field name was read out of internal/adminui; the panel scan has gone BLIND")

	for _, field := range read {
		module, isProviderField := everyName[field]
		if !isProviderField {
			// A name the panel uses for its own JSON, which is its own business.
			continue
		}

		assert.Containsf(t, panelDecodedProviderFields, field,
			"the panel names %q, which the %s module publishes as a Query-provider field, and "+
				"panelDecodedProviderFields does not mention it.\nThat is a cross-module "+
				"contract held by two literals with nothing comparing them. Add it to the map "+
				"so a rename on the module side fails here instead of on a screen.", field, module)
	}
}

// TestEveryForeignFieldTheCatalogReadsIsStillPublished is the catalog's half of
// the first guard.
//
// It fails when inventory stops publishing the one name ADR 0040 lets the
// catalog borrow. Without it the rename is invisible: both trees compile, both
// suites pass, and every product on the storefront quietly reads as out of
// stock.
func TestEveryForeignFieldTheCatalogReadsIsStillPublished(t *testing.T) {
	t.Parallel()

	published := publishedProviderFields(t)

	require.NotEmpty(t, published,
		"no Query-provider field constant was found in any module; the scan has gone BLIND and "+
			"this audit would pass whatever the catalog read")

	for field, module := range catalogReadForeignFields {
		names, serves := published[module]

		assert.Truef(t, serves,
			"the catalog reads %q and this map says the %s module publishes it, but that module "+
				"publishes no provider fields at all. Either the module was renamed or its "+
				"provider is gone; the catalog is reading a field nothing sends.", field, module)

		if serves {
			assert.Containsf(t, names, field,
				"the catalog reads %q and the %s module no longer publishes a field by that "+
					"name.\nThe catalog cannot import the constant (Principle 2.1), so a rename "+
					"on the module side leaves both trees compiling and both suites green -- and "+
					"turns ADR 0040's \"in stock\" into \"out of stock\" for the WHOLE catalog, "+
					"with no error and a 200 response.\nRename the catalog's side too, or drop "+
					"the entry if it stopped reading it. Published today: %v",
				field, module, names)
		}
	}
}

// TestEveryForeignFieldTheCatalogReadsIsDeclared is the half that survives a NEW
// dependency, and it is one of the two mechanisms behind a sentence in ADR 0040.
//
// The decision grants the catalog ONE inventory field and says it "does not
// license a second". A sentence cannot enforce itself; this reads the catalog's
// own source, collects every "foreign<Name>" constant, and fails when one is not
// declared above.
//
// On its own this test holds only half of that sentence, and the half it holds
// is the one that assumes good faith: it audits the constants somebody DECLARED,
// so a name that never becomes a constant is invisible to it.
// [TestEveryForeignFieldTheCatalogReadsIsNamedByAConstant] is the other half --
// it audits the READ SITES and refuses a name that was not spelled as one of
// these constants. The pair is what makes "a seventh field read cannot arrive
// without somebody registering it" a property of the tree rather than of a
// naming convention nobody enforces.
func TestEveryForeignFieldTheCatalogReadsIsDeclared(t *testing.T) {
	t.Parallel()

	read := catalogForeignFields(t)

	require.NotEmpty(t, read,
		"no foreign field constant was found in "+modulesDir+"/"+catalogModuleDir+
			"; the catalog scan has gone BLIND")

	for _, field := range read {
		_, bound := catalogReadForeignFields[field]
		_, unbound := catalogUnboundForeignFields[field]

		assert.Truef(t, bound || unbound,
			"the catalog declares %q as another module's field name and neither "+
				"catalogReadForeignFields nor catalogUnboundForeignFields mentions it.\nThat is "+
				"a cross-module contract held by two literals that no compiler compares. Add it "+
				"to the first map if the owning module publishes the name as an exported "+
				"constant, or to the second WITH the reason it cannot be bound.", field)
	}
}

// TestEveryForeignFieldTheCatalogReadsIsNamedByAConstant audits the READ SITES
// rather than the declarations, and it exists because the declaration audit
// above was found to hold less than it claimed.
//
// # What was wrong with the pair before this test
//
// [TestEveryForeignFieldTheCatalogReadsIsDeclared] collects constants whose
// NAME begins with "foreign". A developer who never writes the constant is
// therefore never audited: `recordInt(inventory, "reserved_quantity")` written
// inline compiles, reads a second inventory field, and leaves every test in this
// package green. That was verified by mutation on 2026-09-08 -- the whole suite
// stayed green -- so ADR 0040's "a seventh field read cannot arrive without
// somebody registering it" rested on a naming convention nothing enforced.
//
// # The two shapes a foreign field name is consumed in
//
// Both are found syntactically, with no type checker:
//
//  1. A call to a LOOSE-RECORD READER, that is, a function the catalog declares
//     whose whole parameter list is (query.Record, string). Today those are
//     service.recordInt, recordString and recordSlice; the set is DERIVED from
//     the source rather than listed here, so a fourth reader joins the audit by
//     being written.
//  2. An index expression on a PARAMETER of type query.Record --
//     `price[foreignPriceListID]` in service.isBasePrice is the one such read
//     today.
//
// In shape 1 the field argument must be an identifier declared as a
// "foreign<Name>" constant, which hands it straight to the declaration audit. In
// shape 2 a bare string literal is refused, and so is a package-level constant
// that is not one of the foreign ones.
//
// # What this does NOT hold, so the next reader knows where the edge is
//
//   - A read through a query.Record that is a LOCAL or RANGE variable rather
//     than a parameter is not shape 2 and is not audited. The catalog has one
//     such loop (service.enrichVariants reads rec[keyPriceSet] and
//     rec[keyInventory]) and its keys are the catalog's OWN aliases, chosen in
//     the same Expand call -- not another module's vocabulary.
//   - Within shape 2, an index by a name the compiler bound LOCALLY is allowed
//     through. That is what keeps service.project green: it indexes a record
//     parameter by a field name its CALLER asked for, one range variable at a
//     time, which is the catalog serving a projection rather than borrowing a
//     vocabulary. A literal, and a package-level constant that is not one of the
//     foreign ones, are both refused there.
//   - A field name that reaches a record by some other road -- reflection, or a
//     JSON round trip into a struct with tags -- is invisible here. Nothing in
//     the catalog does that today, and the day something does, this audit will
//     not notice.
//
// Those two holes are narrower than the one this test closes, and they are the
// reason ADR 0040's sentence is written as "through the two shapes this audit
// reads" rather than as an unqualified impossibility.
func TestEveryForeignFieldTheCatalogReadsIsNamedByAConstant(t *testing.T) {
	t.Parallel()

	fset, files := catalogSource(t)

	readers := looseRecordReaders(files)
	require.NotEmpty(t, readers,
		"no loose-record reader (a func taking exactly (query.Record, string)) was found in "+
			modulesDir+"/"+catalogModuleDir+"; shape 1 of this audit has gone BLIND and a "+
			"foreign field name could be read through a helper nobody is watching")

	declared := map[string]bool{}
	for _, name := range catalogForeignConstantNames(t) {
		declared[name] = true
	}
	require.NotEmpty(t, declared,
		"no foreign<Name> constant was found in "+modulesDir+"/"+catalogModuleDir+
			"; the constant scan has gone BLIND")

	constants := packageLevelConstants(files)
	reads := catalogRecordReads(fset, files, readers)

	require.NotEmpty(t, reads,
		"no foreign field READ was found in "+modulesDir+"/"+catalogModuleDir+
			"; the read-site scan has gone BLIND and this audit would pass whatever the "+
			"catalog read out of another module's record")

	for _, read := range reads {
		if name, isIdent := read.identifier(); isIdent {
			if read.shape == shapeIndex && !constants[name] {
				// A local or a range variable: the catalog indexing its own
				// record by a name its caller asked for. See the godoc.
				continue
			}

			assert.Truef(t, declared[name],
				"%s reads a loose record by %s, and %q is not one of the catalog's "+
					"foreign<Name> constants.\nA field name borrowed from another module has to "+
					"be declared in that constant block, because THAT is what "+
					"TestEveryForeignFieldTheCatalogReadsIsDeclared collects and what makes ADR "+
					"0040's \"does not license a second field\" a gate rather than a wish.\n"+
					"Declared today: %v", read.position, read.description, name, sortedNames(declared))

			continue
		}

		assert.Failf(t, "a foreign field name is spelled inline",
			"%s reads a loose record by %s.\nA field name borrowed from another module may not "+
				"be written at the read site: declare it as a foreign<Name> constant beside the "+
				"others and register it in catalogReadForeignFields or "+
				"catalogUnboundForeignFields.\nAn inline literal is exactly the hole this test "+
				"was written to close -- it compiles, it reads another module's record, and "+
				"nothing else in this repository would say a word.", read.position, read.description)
	}
}

// The two shapes [catalogRecordReads] recognizes.
const (
	// shapeCall is a call to a loose-record reader: recordInt(rec, field).
	shapeCall = "call"
	// shapeIndex is an index into a query.Record PARAMETER: rec[field].
	shapeIndex = "index"
)

// foreignFieldRead is one place the catalog names a field of a loose record.
type foreignFieldRead struct {
	// position is file:line, relative to the repository root.
	position string
	// shape is [shapeCall] or [shapeIndex].
	shape string
	// description is how the read reads in the source, for the failure message.
	description string
	// field is the expression standing for the field name.
	field ast.Expr
}

// identifier returns the name the field expression spells, when it is a plain
// identifier.
func (r foreignFieldRead) identifier() (string, bool) {
	ident, ok := r.field.(*ast.Ident)
	if !ok {
		return "", false
	}

	return ident.Name, true
}

// catalogSource parses the catalog module's PRODUCTION source.
//
// Test files are left out for the same reason the regexp scan leaves them out: a
// fixture naming another module's field is a fixture, not a dependency.
func catalogSource(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()

	fset := token.NewFileSet()

	var files []*ast.File

	root := filepath.Join(repoRoot, modulesDir, catalogModuleDir)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		parsed, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}

		files = append(files, parsed)

		return nil
	})
	require.NoError(t, err, "%s/%s could not be parsed", modulesDir, catalogModuleDir)
	require.NotEmpty(t, files, "%s/%s holds no production Go file", modulesDir, catalogModuleDir)

	return fset, files
}

// looseRecordReaders returns the names of the catalog's loose-record readers.
//
// The shape IS the definition: a function whose whole parameter list is
// (query.Record, string) exists to pull one named field off another module's
// record and can do nothing else. Deriving the set instead of listing it is what
// makes a fourth reader audited on the day it is written rather than on the day
// somebody remembers this file.
func looseRecordReaders(files []*ast.File) map[string]bool {
	out := map[string]bool{}

	for _, file := range files {
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv != nil || fn.Type.Params == nil {
				continue
			}

			params := fn.Type.Params.List
			if len(params) != 2 || len(params[0].Names) != 1 || len(params[1].Names) != 1 {
				continue
			}
			if !isQueryRecordType(params[0].Type) || !isStringType(params[1].Type) {
				continue
			}

			out[fn.Name.Name] = true
		}
	}

	return out
}

// isQueryRecordType reports whether the type expression is query.Record.
func isQueryRecordType(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := selector.X.(*ast.Ident)

	return ok && pkg.Name == "query" && selector.Sel.Name == "Record"
}

// isStringType reports whether the type expression is the predeclared string.
func isStringType(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)

	return ok && ident.Name == "string"
}

// packageLevelConstants returns every constant name declared at file scope.
//
// It is the line between "a name somebody wrote down" and "a name the compiler
// bound to a local": an index by a package-level constant is a spelled field
// name and belongs in the foreign block, while an index by a range variable is
// the catalog walking its own record.
func packageLevelConstants(files []*ast.File) map[string]bool {
	out := map[string]bool{}

	for _, file := range files {
		for _, decl := range file.Decls {
			gen, isGen := decl.(*ast.GenDecl)
			if !isGen || gen.Tok != token.CONST {
				continue
			}

			for _, spec := range gen.Specs {
				value, isValue := spec.(*ast.ValueSpec)
				if !isValue {
					continue
				}

				for _, name := range value.Names {
					out[name.Name] = true
				}
			}
		}
	}

	return out
}

// catalogRecordReads finds every place the catalog names a field of a loose
// record, in the two shapes described on
// [TestEveryForeignFieldTheCatalogReadsIsNamedByAConstant].
func catalogRecordReads(
	fset *token.FileSet,
	files []*ast.File,
	readers map[string]bool,
) []foreignFieldRead {
	var out []foreignFieldRead

	at := func(node ast.Node) string {
		position := fset.Position(node.Pos())
		relative, err := filepath.Rel(repoRoot, position.Filename)
		if err != nil {
			relative = position.Filename
		}

		return fmt.Sprintf("%s:%d", filepath.ToSlash(relative), position.Line)
	}

	for _, file := range files {
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Body == nil {
				continue
			}

			recordParams := map[string]bool{}

			if fn.Type.Params != nil {
				for _, param := range fn.Type.Params.List {
					if !isQueryRecordType(param.Type) {
						continue
					}

					for _, name := range param.Names {
						recordParams[name.Name] = true
					}
				}
			}

			ast.Inspect(fn.Body, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.CallExpr:
					callee, isIdent := typed.Fun.(*ast.Ident)
					if !isIdent || !readers[callee.Name] || len(typed.Args) != 2 {
						return true
					}

					out = append(out, foreignFieldRead{
						position: at(typed),
						shape:    shapeCall,
						description: fmt.Sprintf("%s(..., %s)",
							callee.Name, exprText(typed.Args[1])),
						field: typed.Args[1],
					})
				case *ast.IndexExpr:
					record, isIdent := typed.X.(*ast.Ident)
					if !isIdent || !recordParams[record.Name] {
						return true
					}

					out = append(out, foreignFieldRead{
						position: at(typed),
						shape:    shapeIndex,
						description: fmt.Sprintf("%s[%s]",
							record.Name, exprText(typed.Index)),
						field: typed.Index,
					})
				}

				return true
			})
		}
	}

	return out
}

// exprText renders the two expression shapes this audit reports on.
//
// A full printer would be more general and would also make the failure message
// depend on go/printer's formatting; the two cases that can appear as a field
// name are an identifier and a literal.
func exprText(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.BasicLit:
		return typed.Value
	default:
		return fmt.Sprintf("%T", expr)
	}
}

// catalogForeignConstantNames returns the NAMES of the catalog's foreign field
// constants, where [catalogForeignFields] returns their values.
func catalogForeignConstantNames(t *testing.T) []string {
	t.Helper()

	_, files := catalogSource(t)

	var out []string

	for _, file := range files {
		for _, decl := range file.Decls {
			gen, isGen := decl.(*ast.GenDecl)
			if !isGen || gen.Tok != token.CONST {
				continue
			}

			for _, spec := range gen.Specs {
				value, isValue := spec.(*ast.ValueSpec)
				if !isValue {
					continue
				}

				for _, name := range value.Names {
					if foreignConstantName.MatchString(name.Name) {
						out = append(out, name.Name)
					}
				}
			}
		}
	}

	sort.Strings(out)

	return out
}

// foreignConstantName matches the NAME of a catalog constant that stands for
// another module's field, where [foreignFieldConstant] matches the whole
// declaration and captures its value.
var foreignConstantName = regexp.MustCompile(`^foreign[A-Z][A-Za-z]*$`)

// sortedNames returns a set's members in order, for a stable failure message.
func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}

	sort.Strings(out)

	return out
}

// TestEveryUnboundForeignFieldIsStillUnpublished keeps the registered hole
// honest.
//
// [catalogUnboundForeignFields] is a list of names this audit CANNOT check, and
// a list like that rots in one specific way: the owning module starts publishing
// the name, the entry stays where it is, and a pairing that could now be checked
// goes on being trusted. This fails on that day and names the entry to move.
func TestEveryUnboundForeignFieldIsStillUnpublished(t *testing.T) {
	t.Parallel()

	published := publishedProviderFields(t)

	for field, module := range catalogUnboundForeignFields {
		assert.NotContainsf(t, published[module], field,
			"%q is listed as a field the %s module does NOT publish, and it publishes it now.\n"+
				"The pairing can be checked from here at last: move the entry into "+
				"catalogReadForeignFields so a rename fails in this test rather than in a "+
				"storefront that quietly stops matching a price bracket.", field, module)
	}
}

// catalogForeignFields returns the field names the catalog declares as another
// module's.
func catalogForeignFields(t *testing.T) []string {
	t.Helper()

	seen := map[string]bool{}
	root := filepath.Join(repoRoot, modulesDir, catalogModuleDir)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		for _, match := range foreignFieldConstant.FindAllStringSubmatch(string(body), -1) {
			seen[match[1]] = true
		}

		return nil
	})
	require.NoError(t, err, "%s/%s could not be walked", modulesDir, catalogModuleDir)

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}

	sort.Strings(out)

	return out
}

// publishedProviderFields returns the field names each module publishes to the
// Query layer, by module.
func publishedProviderFields(t *testing.T) map[string][]string {
	t.Helper()

	out := map[string][]string{}
	root := filepath.Join(repoRoot, modulesDir)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		matches := providerFieldConstant.FindAllStringSubmatch(string(body), -1)
		if len(matches) == 0 {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}

		module := strings.Split(filepath.ToSlash(rel), "/")[0]
		for _, match := range matches {
			if !slices.Contains(out[module], match[1]) {
				out[module] = append(out[module], match[1])
			}
		}

		return nil
	})
	require.NoError(t, err, "%s could not be walked for provider fields", modulesDir)

	for module := range out {
		sort.Strings(out[module])
	}

	return out
}

// panelReadFields returns every field name the admin panel names in its own
// production source.
func panelReadFields(t *testing.T) []string {
	t.Helper()

	seen := map[string]bool{}
	root := filepath.Join(repoRoot, "internal", "adminui")

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		for _, match := range panelFieldReference.FindAllStringSubmatch(string(body), -1) {
			for _, name := range match[1:] {
				if name != "" {
					seen[name] = true
				}
			}
		}

		return nil
	})
	require.NoError(t, err, "internal/adminui could not be walked")

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}

	sort.Strings(out)

	return out
}
