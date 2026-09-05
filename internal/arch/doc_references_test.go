package arch_test

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file enforces ONE invariant: EVERY REFERENCE IN THE DOCUMENTATION RESOLVES.
//
// This repository's most stubborn class of fault is not a rule being broken but THE
// DOCUMENTATION ROTTING: a godoc points at a symbol, the symbol is deleted or
// renamed, the godoc stays where it was. No tool makes a sound — Go does not treat
// an unresolvable link as an error, it prints it as plain text. Measured examples
// came up in this round too: one godoc pointed at a Config method that had been
// renamed once; another at a generator name that had never existed; a third at a
// repository method whose name was made longer later. All three were inside code
// that compiled, passed its tests and had been through review.
//
// The cost of the rot is GREATER than the cost of a wrong comment: a wrong comment
// misleads the reader, a rotten reference sends the reader SEARCHING. The thing
// searched for does not exist and the searcher learns that only after grepping the
// whole repository.
//
// # Why here and why by WALKING
//
// The audit KEEPS NO LIST: which link resolves where is derived from the source. A
// hand-written "valid symbols" list would miss exactly the thing it is trying to
// protect against (a symbol being deleted) — the list would not be updated along
// with it.
//
// # What this audit does NOT guarantee
//
// Keeping the scope narrow means keeping the promise it makes narrow too:
//
//   - It does not say the reference goes to the RIGHT place, it says it RESOLVES. If
//     a cart's total and its subtotal have been mixed up both links resolve and the
//     audit stays silent.
//   - It does not see mentions that are not INSIDE brackets. A plain-text mention
//     like "see service.Foo" is not audited; a bracket is a PROMISE, plain text is
//     not.
//   - It looks only at COMMENTS, not at strings. That would be a gap — one of this
//     repository's measured rots was inside the message a test prints WHEN IT FAILS
//     — but the measurement says otherwise: across every string constant in the
//     repository there are eight texts in bracket shape and all eight are not links
//     but EMPHASIS in a developer message. Taking strings into scope starts with
//     eight false positives and in return catches nothing at all today.
//   - It does not verify the symbols of third-party packages (see
//     [lookUpInPackage]).
//   - Declarations in the same package's test files count too. A production godoc
//     linking to a name that exists only in a test is therefore not caught;
//     separating them would break the test godocs that link to their own helpers,
//     and the gain would not be worth that price.
//   - Member names without a receiver (see [referencePackage]) resolve to a member
//     of ANY type in the package. A reference naming a deleted field passes the
//     audit silently if another type carries the same name.
//
// The items above are for GO COMMENTS. Markdown documents are audited too, but their
// scope is drawn separately: there the anchor is not a square bracket but the shape
// of the path itself together with a backtick. The boundaries are written in the
// [markdownReferences] and [TestTheReferencesInTheDocsResolve] godocs.

// referenceFile is a single scanned Go file.
type referenceFile struct {
	// path is relative to the repository root and this is what shows up in error
	// messages.
	path string
	dir  string
	pkg  *referencePackage
	tree *ast.File
}

// referencePackage is the name and import table of a directory + package name pair.
//
// The unit is NOT THE DIRECTORY but the package name: two separate packages, "foo"
// and "foo_test", can live in the same directory, and the external test package
// CANNOT SEE the unexported names of the package under test. Treating the two as one
// would show a link that cannot resolve as resolved.
type referencePackage struct {
	dir  string
	name string
	// names holds the top-level declarations and the "Receiver.Method" and
	// "Type.Field" pairs.
	names map[string]bool
	// members are the member names (methods and fields) that can be mentioned
	// without writing their receiver.
	//
	// Go's own rule does not know this one; this repository does, because the godoc
	// of a struct points at its sibling field by NAME ALONE and the reader finds it
	// one line below. The price is that the set is WIDE, and it is written down in
	// the "does not guarantee" list at the head of this file.
	members map[string]bool
	// embeds holds the types a type embeds; the "T.Method" lookup is done by
	// following the embedded ones (testing.T.TempDir really lives on common).
	embeds map[string][]string
	// imports goes from the local package name to the import path; an empty value
	// says the same name is bound to two different paths (AMBIGUOUS).
	imports map[string]string
}

// referenceScan is the repository's Go source scanned for the reference audit.
type referenceScan struct {
	fset     *token.FileSet
	files    []*referenceFile
	packages map[string]*referencePackage
	// productionName goes from the import path to the package's PRODUCTION name;
	// this is the local name of an unaliased import and it can differ from the
	// directory name.
	productionName map[string]string
	// testNames are the names of ALL the test functions in the repository.
	//
	// Test packages CANNOT BE IMPORTED, which means a reference to a test can never
	// be written qualified. The repository mentions them by name
	// (configuration_test.go builds the same set for the README) and "go test -run"
	// addresses the name too.
	testNames map[string]bool
	// stdCache caches the resolved stdlib packages; a nil value means "not stdlib".
	stdCache map[string]*referencePackage
}

// scanDocReferences parses the Go source under the production roots.
//
// Test files ARE INCLUDED: this repository's densest godocs are inside the
// architecture tests and a rotten reference does the same damage there — some of
// them even leak into the message a test prints WHEN IT FAILS.
func scanDocReferences(t *testing.T) *referenceScan {
	t.Helper()

	scan := &referenceScan{
		fset:           token.NewFileSet(),
		packages:       map[string]*referencePackage{},
		productionName: map[string]string{},
		testNames:      map[string]bool{},
		stdCache:       map[string]*referencePackage{},
	}

	for _, root := range productionTrees {
		abs := filepath.Join(repoRoot, root)
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("the %q root was not found: %v", root, err)
		}
		for _, filePath := range goFiles(t, abs) {
			tree, err := parser.ParseFile(scan.fset, filePath, nil, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("%s could not be parsed: %v", filePath, err)
			}
			rel, err := filepath.Rel(repoRoot, filePath)
			if err != nil {
				t.Fatalf("%s could not be turned into a relative path: %v", filePath, err)
			}
			rel = filepath.ToSlash(rel)
			file := &referenceFile{
				path: rel,
				dir:  filepath.ToSlash(filepath.Dir(rel)),
				tree: tree,
			}
			scan.files = append(scan.files, file)
			if !strings.HasSuffix(tree.Name.Name, "_test") {
				scan.productionName[modulePath+"/"+file.dir] = tree.Name.Name
			}
		}
	}

	// Second pass: the import tables are built AFTER the package names have been
	// collected. The local name of an unaliased import is the name the target
	// DECLARES, and that name is known only once the target has been parsed.
	for _, file := range scan.files {
		file.pkg = scan.packageFor(file.dir, file.tree.Name.Name)
		scan.collectImports(file)
		collectDeclarationNames(file.tree, file.pkg)
		for _, decl := range file.tree.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				scan.testNames[fn.Name.Name] = true
			}
		}
	}

	return scan
}

// packageFor returns the directory + package name unit, creating it if there is none.
func (s *referenceScan) packageFor(dir, name string) *referencePackage {
	key := dir + "\x00" + name
	if p, ok := s.packages[key]; ok {
		return p
	}
	p := &referencePackage{
		dir:     dir,
		name:    name,
		names:   map[string]bool{},
		members: map[string]bool{},
		embeds:  map[string][]string{},
		imports: map[string]string{},
	}
	s.packages[key] = p
	return p
}

// collectImports adds the file's imports to the PACKAGE's table.
//
// The table is at PACKAGE level rather than file level because go/doc does the same:
// a qualified link resolves if ANY file of the package imports the qualifying
// package. Without the rule, every link in a documentation file such as doc.go,
// which imports nothing, would count as broken.
func (s *referenceScan) collectImports(file *referenceFile) {
	for _, imp := range file.tree.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		local := ""
		switch {
		case imp.Name != nil:
			local = imp.Name.Name
		case s.productionName[importPath] != "":
			local = s.productionName[importPath]
		default:
			local = assumedPackageName(importPath)
		}
		if local == "" || local == "_" || local == "." {
			continue
		}
		if previous, ok := file.pkg.imports[local]; ok && previous != importPath {
			file.pkg.imports[local] = ""
			continue
		}
		file.pkg.imports[local] = importPath
	}
}

// assumedPackageName guesses the local name of an import that has no alias.
//
// It is the same as go/doc's assumedPackageName and it MUST be the same: the last
// element of the path "github.com/go-chi/chi/v5" is "v5", while its local name is
// "chi". A drift in the guess would show ALL of that package's links as broken with
// "no import" — that is, it would produce a pile of false accusations.
func assumedPackageName(importPath string) string {
	notIdentifier := func(ch rune) bool {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '_':
			return false
		case ch >= utf8.RuneSelf && (unicode.IsLetter(ch) || unicode.IsDigit(ch)):
			return false
		default:
			return true
		}
	}
	base := path.Base(importPath)
	if strings.HasPrefix(base, "v") {
		if _, err := strconv.Atoi(base[1:]); err == nil {
			if dir := path.Dir(importPath); dir != "." {
				base = path.Base(dir)
			}
		}
	}
	base = strings.TrimPrefix(base, "go-")
	if i := strings.IndexFunc(base, notIdentifier); i >= 0 {
		base = base[:i]
	}
	return base
}

// collectDeclarationNames adds the file's top-level declarations to the package.
func collectDeclarationNames(tree *ast.File, pkg *referencePackage) {
	for _, decl := range tree.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil || len(d.Recv.List) == 0 {
				pkg.names[d.Name.Name] = true
				continue
			}
			pkg.names[referenceReceiverName(d.Recv.List[0].Type)+"."+d.Name.Name] = true
			pkg.members[d.Name.Name] = true
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					pkg.names[sp.Name.Name] = true
					collectMemberNames(sp.Name.Name, sp.Type, pkg)
				case *ast.ValueSpec:
					for _, name := range sp.Names {
						pkg.names[name.Name] = true
					}
				}
			}
		}
	}
}

// collectMemberNames records a type's fields, interface methods and embeddings.
func collectMemberNames(typeName string, expr ast.Expr, pkg *referencePackage) {
	var list *ast.FieldList
	switch t := expr.(type) {
	case *ast.StructType:
		list = t.Fields
	case *ast.InterfaceType:
		list = t.Methods
	default:
		return
	}
	if list == nil {
		return
	}
	for _, field := range list.List {
		if len(field.Names) == 0 {
			// Embedded field: its name is the name of the EMBEDDED TYPE and its
			// members come from that type.
			if name := referenceReceiverName(field.Type); name != "" {
				pkg.embeds[typeName] = append(pkg.embeds[typeName], name)
				pkg.names[typeName+"."+name] = true
				pkg.members[name] = true
			}
			continue
		}
		for _, name := range field.Names {
			pkg.names[typeName+"."+name.Name] = true
			pkg.members[name.Name] = true
		}
	}
}

// referenceReceiverName returns the unqualified name of a type expression.
func referenceReceiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return referenceReceiverName(t.X)
	case *ast.IndexExpr:
		return referenceReceiverName(t.X)
	case *ast.IndexListExpr:
		return referenceReceiverName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}

// linkForm is the SYNTAX class of a doc link candidate.
//
// The class is the measure of the blindness check: when the scanner breaks it
// usually loses not ALL the links but one CLASS of them (reading the brackets,
// parsing the dots and the path shape are separate code paths). A counter per class
// makes that loss visible.
type linkForm int

const (
	// linkLocalName is a single-element name inside brackets.
	linkLocalName linkForm = iota
	// linkQualified has two elements: package.Name or Type.Member.
	linkQualified
	// linkThreePart is package + receiver + name: [testing.T.TempDir].
	linkThreePart
	// linkFullPath carries an import path:
	// [github.com/bdrtr/gobit/core/module.Module].
	linkFullPath
	linkFormCount
)

// linkFormNames is the name of a form in error messages.
var linkFormNames = [linkFormCount]string{
	linkLocalName: "local name ([Name])",
	linkQualified: "qualified name ([package.Name] or [Type.Member])",
	linkThreePart: "package + receiver ([package.Type.Member])",
	linkFullPath:  "full import path ([github.com/…/package.Name])",
}

// linkCandidate is a text appearing in a comment that has the SHAPE of a doc link.
type linkCandidate struct {
	content string
	form    linkForm
	file    *referenceFile
	line    int
}

// linkCandidates extracts the doc link candidates in a comment text.
//
// # A false positive rests on a RULE, not on a list
//
// A square bracket is not always a link: comments also hold JSON arrays
// (["a","b"]), mathematical ranges ([0, %100]), slice syntax ([]string) and brackets
// used instead of parentheses in Turkish prose. The filtering is done with three
// rules and none of them is a "ignore these texts" list:
//
//  1. Go's OWN context rule: the character before and after the bracket must be a
//     space or punctuation (the rule is taken verbatim from go/doc/comment).
//  2. The content, after an optional "*" has been peeled off, must split into Go
//     identifiers separated by DOTS (or into an import path + identifiers). A space,
//     a quote, a brace, a percent sign, a Turkish letter — all of them are
//     eliminated.
//  3. The number of elements cannot exceed three; Go itself cannot express anything
//     deeper.
//
// The one place where the rule DEPARTS from Go is the requirement that a name start
// with a capital letter: go/doc counts only exported names as links, while this
// repository links to lowercase local names as well (they are valid inside the
// package and half of the godocs sit above unexported definitions). The departure is
// deliberate; its price is that a single ASCII word written inside brackets COUNTS
// AS a link.
func linkCandidates(text string, file *referenceFile, line int) []linkCandidate {
	var candidates []linkCandidate
	for start := 0; start < len(text); start++ {
		if text[start] != '[' {
			continue
		}
		offset := strings.IndexByte(text[start+1:], ']')
		if offset < 0 {
			break
		}
		end := start + 1 + offset
		content := text[start+1 : end]
		before, after := text[:start], text[end+1:]
		start = end

		if !linkContextIsValid(before, after) {
			continue
		}
		form, ok := linkFormOf(content)
		if !ok {
			continue
		}
		candidates = append(candidates, linkCandidate{content: content, form: form, file: file, line: line})
	}
	return candidates
}

// linkContextIsValid applies go/doc/comment's link context rule.
//
// The rule is this: the character immediately before and immediately after the
// bracket must be a space or punctuation. A bracket stuck to a word ("array[i]") is
// not a link.
func linkContextIsValid(before, after string) bool {
	if before != "" {
		r, _ := utf8.DecodeLastRuneInString(before)
		if !unicode.IsPunct(r) && r != ' ' && r != '\t' && r != '\n' {
			return false
		}
	}
	if after != "" {
		r, _ := utf8.DecodeRuneInString(after)
		if !unicode.IsPunct(r) && r != ' ' && r != '\t' && r != '\n' {
			return false
		}
	}
	return true
}

// isLinkIdentifier says whether an element is a Go identifier.
//
// It is limited to ASCII, and that limit is the very rule that eliminates the false
// positives: a word put inside brackets in Turkish prose almost always carries a
// letter outside ASCII and is therefore not counted as a link. (The example this
// paragraph would like to show cannot be written here: ADR 0012 forbids a Turkish
// letter in a translated file, and a bracketed ASCII word would itself become a
// candidate.) The non-ASCII identifiers Go allows are not used in this repository.
func isLinkIdentifier(part string) bool {
	if part == "" {
		return false
	}
	for i := 0; i < len(part); i++ {
		switch c := part[i]; {
		case c == '_', c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		default:
			return false
		}
	}
	return part[0] < '0' || part[0] > '9'
}

// splitLink splits a link's content into an import path and dot-separated elements.
func splitLink(content string) (importPath string, parts []string, ok bool) {
	content = strings.TrimPrefix(content, "*")
	if content == "" {
		return "", nil, false
	}
	if !strings.Contains(content, "/") {
		parts = strings.Split(content, ".")
		for _, part := range parts {
			if !isLinkIdentifier(part) {
				return "", nil, false
			}
		}
		return "", parts, true
	}
	slash := strings.LastIndexByte(content, '/')
	tail := strings.Split(content[slash+1:], ".")
	for _, part := range tail {
		if !isLinkIdentifier(part) {
			return "", nil, false
		}
	}
	pkgPath := content[:slash+1] + tail[0]
	if !isLinkPath(pkgPath) {
		return "", nil, false
	}
	return pkgPath, tail[1:], true
}

// isLinkPath says whether a string has the shape of an import path.
func isLinkPath(pkgPath string) bool {
	if pkgPath == "" || strings.Contains(pkgPath, "//") || strings.HasSuffix(pkgPath, "/") {
		return false
	}
	for _, part := range strings.Split(pkgPath, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			valid := c == '_' || c == '-' || c == '.' || c == '~' ||
				c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			if !valid {
				return false
			}
		}
	}
	return true
}

// linkFormOf returns the link form of a content; false if it is not in link shape.
func linkFormOf(content string) (linkForm, bool) {
	pkgPath, parts, ok := splitLink(content)
	if !ok {
		return 0, false
	}
	if pkgPath != "" {
		if len(parts) > 2 {
			return 0, false
		}
		return linkFullPath, true
	}
	switch len(parts) {
	case 1:
		return linkLocalName, true
	case 2:
		return linkQualified, true
	case 3:
		return linkThreePart, true
	}
	return 0, false
}

// resolveLink says whether a link resolves; an empty string means IT RESOLVED.
//
// The returned value is a REASON, not a flag: knowing "it did not resolve" does not
// on its own make the fix easier — what the searcher needs to learn is where the
// name was looked for.
func (s *referenceScan) resolveLink(candidate linkCandidate) string {
	pkg := candidate.file.pkg
	pkgPath, parts, ok := splitLink(candidate.content)
	if !ok {
		return "the form was not recognized"
	}
	if pkgPath != "" {
		return s.lookUpInPackage(pkgPath, parts)
	}

	switch len(parts) {
	case 1:
		name := parts[0]
		switch {
		case s.hasLocalName(pkg, name):
			return ""
		case pkg.imports[name] != "":
			// A reference to the package itself, such as [fmt].
			return ""
		case name == pkg.name || name+"_test" == pkg.name:
			// A package may mention its own name (go/doc allows it too).
			return ""
		case s.testNames[name]:
			return ""
		}
		return fmt.Sprintf("there is no such declaration in %s (package %s) and no such "+
			"test in the repository either.%s", pkg.dir, pkg.name, emphasisHint(name))

	case 2:
		// The ORDER MATTERS: a qualified name is tried first as a PACKAGE, then as
		// the member of a local type. Go makes that distinction by case (a qualified
		// name whose first element is capitalized is a receiver), while this
		// repository uses lowercase type names as well (uploadDTO.URL) and a rule
		// that looked at the case would miss them.
		if target, ok := pkg.imports[parts[0]]; ok && target != "" {
			return s.lookUpInPackage(target, parts[1:])
		}
		if s.hasLocalName(pkg, strings.Join(parts, ".")) ||
			s.hasLocalMember(pkg, parts[0], parts[1]) {
			return ""
		}
		if !s.hasLocalName(pkg, parts[0]) {
			return fmt.Sprintf("%q is neither a package imported by package %s nor a type "+
				"defined in %s", parts[0], pkg.name, pkg.dir)
		}
		return fmt.Sprintf("type %s has no member called %s", parts[0], parts[1])

	case 3:
		target, ok := pkg.imports[parts[0]]
		if !ok || target == "" {
			return fmt.Sprintf("package %s does not import a package called %q", pkg.name, parts[0])
		}
		return s.lookUpInPackage(target, parts[1:])
	}
	return "the form was not recognized"
}

// hasLocalName says whether a name is declared in the package itself.
//
// An external test package (foo_test) looks at the PRODUCTION package's names too:
// those files live next to the package under test and their godocs mention its
// exported names. There is no reverse direction; production cannot see the test's
// names.
func (s *referenceScan) hasLocalName(pkg *referencePackage, name string) bool {
	if pkg.names[name] || (!strings.Contains(name, ".") && pkg.members[name]) {
		return true
	}
	if !strings.HasSuffix(pkg.name, "_test") {
		return false
	}
	production, ok := s.packages[pkg.dir+"\x00"+strings.TrimSuffix(pkg.name, "_test")]
	if !ok {
		return false
	}
	return production.names[name] || (!strings.Contains(name, ".") && production.members[name])
}

// hasLocalMember looks for the "Receiver.Name" pair by FOLLOWING the embedded types.
func (s *referenceScan) hasLocalMember(pkg *referencePackage, receiver, name string) bool {
	return hasMemberReference(pkg, receiver, name, 0)
}

// hasMemberReference says whether a type (and what it embeds) carries the given
// member.
//
// The depth limit stops two types that embed each other from causing an infinite
// descent; real embedding chains are far below this depth.
func hasMemberReference(pkg *referencePackage, receiver, name string, depth int) bool {
	if depth > 4 {
		return false
	}
	if pkg.names[receiver+"."+name] {
		return true
	}
	for _, embedded := range pkg.embeds[receiver] {
		if hasMemberReference(pkg, embedded, name, depth+1) {
			return true
		}
	}
	return false
}

// referenceTarget returns the scanned package of an import path and whether it is
// inside the repository.
//
// For a path inside the repository a nil target means "there is NO such package";
// for a path outside it, it only means "its source could not be looked at" (if it is
// not stdlib it is third party). What tells the two apart is the second return
// value; without that distinction a repository package that does not exist would be
// approved as silently as an unverifiable third-party package.
func (s *referenceScan) referenceTarget(importPath string) (target *referencePackage, inRepo bool) {
	inRepo = importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/")
	if !inRepo {
		return s.stdPackage(importPath), false
	}
	name, ok := s.productionName[importPath]
	if !ok {
		return nil, true
	}
	dir := strings.TrimPrefix(importPath, modulePath+"/")
	return s.packages[dir+"\x00"+name], true
}

// lookUpInPackage looks for a name in the package at an import path.
//
// There are three classes of package and the promise made to each is DIFFERENT:
//
//   - Repository packages: both the package and the symbol are verified.
//   - stdlib: verified from the source in GOROOT. It is not free but it is cheap,
//     and go test already runs inside a Go installation.
//   - Third party: ONLY the fact that the package is imported is verified, the
//     symbol is NOT. Verifying it would require resolving the module cache; the gain
//     is small (these links only rot on a dependency upgrade) and the cost is tying
//     the audit to the network and to the module layout.
func (s *referenceScan) lookUpInPackage(importPath string, parts []string) string {
	target, inRepo := s.referenceTarget(importPath)
	if inRepo && target == nil {
		return "there is no such package in the repository: " + importPath
	}

	switch {
	case len(parts) == 0:
		return ""
	case target == nil:
		return "" // third party: the symbol is not verified
	case len(parts) == 1:
		if target.names[parts[0]] {
			return ""
		}
		return fmt.Sprintf("package %s has no exported declaration called %s",
			importPath, parts[0])
	default:
		if hasMemberReference(target, parts[0], parts[1], 0) {
			return ""
		}
		return fmt.Sprintf("package %s has no member called %s.%s",
			importPath, parts[0], parts[1])
	}
}

// stdPackage reads a stdlib package from the GOROOT source; nil if it is not stdlib.
func (s *referenceScan) stdPackage(importPath string) *referencePackage {
	if pkg, ok := s.stdCache[importPath]; ok {
		return pkg
	}
	dir := filepath.Join(build.Default.GOROOT, "src", filepath.FromSlash(importPath))
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		s.stdCache[importPath] = nil
		return nil
	}
	pkg := &referencePackage{
		dir:     dir,
		name:    path.Base(importPath),
		names:   map[string]bool{},
		members: map[string]bool{},
		embeds:  map[string][]string{},
		imports: map[string]string{},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		s.stdCache[importPath] = nil
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(s.fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		collectDeclarationNames(tree, pkg)
	}
	s.stdCache[importPath] = pkg
	return pkg
}

// allLinkCandidates returns the link candidates in every scanned comment.
func (s *referenceScan) allLinkCandidates() []linkCandidate {
	var candidates []linkCandidate
	for _, file := range s.files {
		for _, group := range file.tree.Comments {
			line := s.fset.Position(group.Pos()).Line
			candidates = append(candidates, linkCandidates(group.Text(), file, line)...)
		}
	}
	return candidates
}

// TestTheGodocLinksResolve verifies that every doc link in the comments resolves to
// a REAL declaration.
//
// A link that does not resolve does not break the build, is not caught by a linter
// and is hard to see by eye: Go prints it as plain text, so the reader cannot even
// say "there was a link here". Its only symptom is that the person who goes looking
// for that name finds nothing.
func TestTheGodocLinksResolve(t *testing.T) {
	t.Parallel()

	scan := scanDocReferences(t)
	for _, candidate := range scan.allLinkCandidates() {
		if reason := scan.resolveLink(candidate); reason != "" {
			t.Errorf("%s:%d: the [%s] link does not resolve — %s.\n"+
				"A bracket is a PROMISE: it sends the reader looking for that name. If the "+
				"symbol was renamed the link must change too; if it lives in another "+
				"package and this package does not import it, the FULL PATH must be written "+
				"([github.com/bdrtr/gobit/…/package.Name]); if it no longer exists anywhere "+
				"the reference must be deleted. Removing the bracket and turning it into "+
				"plain text is an answer too, but it must be DELIBERATE: plain text is "+
				"outside this audit.",
				candidate.file.path, candidate.line, candidate.content, reason)
		}
	}
}

// TestTheLinkScannerIsNotBlind verifies that the link scanner STILL sees links.
//
// [TestTheGodocLinksResolve] passes SILENTLY when it finds no candidate at all: a
// scanner that produces no finding and a repository with no violation look exactly
// the same in the output. This test tells the two apart, and that is the reason it
// is a separate test — inside the same test the error of the first unresolved link
// would be mixed up with the blindness message.
//
// The counter is kept per form because the scanner usually goes blind PIECE BY PIECE
// rather than ALL AT ONCE: reading the brackets, parsing the dots and the import
// path shape are separate code paths, and when one of them breaks the total is still
// in the thousands.
func TestTheLinkScannerIsNotBlind(t *testing.T) {
	t.Parallel()

	scan := scanDocReferences(t)
	require.NotEmpty(t, scan.files, "no Go file was found under the production roots")

	counts := [linkFormCount]int{}
	roots := map[string]int{}
	for _, candidate := range scan.allLinkCandidates() {
		counts[candidate.form]++
		roots[strings.SplitN(candidate.file.path, "/", 2)[0]]++
	}

	for form, count := range counts {
		require.Positive(t, count,
			"NOT ONE doc link in %s form was found; the scanner must have gone BLIND in "+
				"this class.\nThat branch of link resolution no longer audits anything: if "+
				"the class really has left the repository, both this assertion and the "+
				"matching resolution branch must be deleted; staying green silently gives "+
				"the impression that the class is still protected.",
			linkFormNames[form])
	}

	for _, root := range productionTrees {
		require.Positive(t, roots[root],
			"no doc link was seen in the %s/ tree; the scan may never have read that root "+
				"(the goFiles walk or the comment parsing). Every rotten reference in an "+
				"unread tree is approved.", root)
	}

	// Positive controls. The resolution can silently swallow the difference between
	// "I did not find it" and "I could not look": when a package's source cannot be
	// reached at all the audit does NOT VERIFY the symbol and approves the link (that
	// is the promise made for third-party packages). The same silence would be a
	// FAULT for repository and stdlib packages, so it is separately tested that both
	// of them REJECT a symbol that does not exist.
	require.NotEmpty(t, scan.lookUpInPackage("testing", []string{"NoSuchSymbol"}),
		"a symbol that does not exist in a stdlib package was approved; the GOROOT "+
			"source must have been unreadable (build.Default.GOROOT is empty or the source "+
			"tree is missing). In that case every stdlib link passes without its symbol "+
			"ever being looked for.")
	require.NotEmpty(t, scan.lookUpInPackage(modulePath+"/core/module", []string{"NoSuchSymbol"}),
		"a symbol that does not exist in a repository package was approved; the package "+
			"directory must not have been built. In that case every qualified link inside "+
			"the repository passes unverified.")
}

// docSectionReference captures a document section MENTIONED inside a godoc.
//
// The pattern looks for the word "section" immediately after a heading written in
// quotes. Mentions without quotes (a sentence that names the trust boundary section
// without quoting it, say) are OUT OF SCOPE and have to be: there is no boundary
// that says where the heading ends, so either half a sentence is taken for a heading
// or nothing is found at all.
//
// The pattern is BILINGUAL. Translated godocs write "section" and the repository's
// untranslated ones still write the Turkish word for it; the Turkish half is spelled
// with \x{00f6} and \x{00fc} escapes because ADR 0012 forbids a Turkish LETTER in a
// translated file. The escape matches exactly the same text and is no letter to the
// scanner; folding it to ASCII would leave this audit with nothing to find today,
// which is the same as switching it off.
var docSectionReference = regexp.MustCompile(`"([^"]{3,80})"\s*(?:section|b\x{00f6}l\x{00fc}m)`)

// TestTheGodocSectionReferencesResolve verifies that the document section a godoc
// mentions by name REALLY exists.
//
// This is the second face of link rot and it has a measured example: the godoc of a
// DTO field said its boundary was written "in that section of the package
// documentation"; that section had been renamed in a rewrite and the reader was
// looking for a heading the package documentation did not have.
//
// The heading is looked for IN THE SAME PACKAGE. If a godoc points at another
// package's section it must say which package; the phrase "in the package
// documentation" is unambiguous only for its own package.
func TestTheGodocSectionReferencesResolve(t *testing.T) {
	t.Parallel()

	scan := scanDocReferences(t)

	// Heading index: package → set of headings, and the whole repository.
	packageHeadings := map[*referencePackage]map[string]bool{}
	repoHeadings := map[string]string{}
	headingCount := 0
	for _, file := range scan.files {
		for _, group := range file.tree.Comments {
			for _, line := range strings.Split(group.Text(), "\n") {
				heading, ok := strings.CutPrefix(line, "# ")
				if !ok {
					continue
				}
				heading = strings.TrimSpace(heading)
				headingCount++
				if packageHeadings[file.pkg] == nil {
					packageHeadings[file.pkg] = map[string]bool{}
				}
				packageHeadings[file.pkg][heading] = true
				if _, seen := repoHeadings[heading]; !seen {
					repoHeadings[heading] = file.path
				}
			}
		}
	}

	referenceCount := 0
	for _, file := range scan.files {
		for _, group := range file.tree.Comments {
			text := strings.ReplaceAll(group.Text(), "\n", " ")
			for _, match := range docSectionReference.FindAllStringSubmatch(text, -1) {
				heading := strings.TrimSpace(match[1])
				referenceCount++
				if packageHeadings[file.pkg][heading] {
					continue
				}
				where := "no godoc has such a heading"
				if in, seen := repoHeadings[heading]; seen {
					where = fmt.Sprintf("the heading exists in %s but not in THIS package", in)
				}
				t.Errorf("%s:%d: a godoc points at the %q section but %s (package looked in: %s).\n"+
					"A section mentioned by name becomes silently unreadable when its name "+
					"changes: the reader looks for a heading the document does not have.",
					file.path, scan.fset.Position(group.Pos()).Line, heading, where, file.pkg.dir)
			}
		}
	}

	require.Positive(t, headingCount,
		"no godoc had a \"# Heading\" line; the heading index must have gone BLIND.\n"+
			"An empty index counts every section reference as a violation and produces a "+
			"pile of false accusations; that is where the pile comes from.")
	require.Positive(t, referenceCount,
		"no godoc had a quoted section reference; the pattern may no longer match the "+
			"way the repository writes.\nIf the repository really has dropped this turn of "+
			"phrase, this audit must be deliberately removed too; staying green with a "+
			"pattern that matches nothing gives the impression that section references are "+
			"still audited.")
}

// adrNumberReference captures the references of the form "ADR 0004" in a text.
var adrNumberReference = regexp.MustCompile(`\bADR ?(\d{4})\b`)

// adrPathReference captures the references made to a decision record BY FILE PATH.
var adrPathReference = regexp.MustCompile(`docs/adr/(\d{4})-[a-z0-9-]+\.md`)

// TestTheADRReferencesResolve verifies that every ADR reference in the code and in
// the documents goes to a REAL decision record.
//
// In this repository the ADRs are the ONLY justification of a decision and the code
// comments point at them by number (ADR 0001 alone is mentioned in 174 places). A
// number shifting, or a record being renumbered, would silently send all 174 of
// those references to the wrong place.
//
// That the number goes to the RIGHT decision cannot be verified — writing "ADR 0004"
// while meaning 0006 is a fault this audit cannot see. What is verified is that a
// file matching the number EXISTS.
func TestTheADRReferencesResolve(t *testing.T) {
	t.Parallel()

	adrDir := filepath.Join(repoRoot, "docs", "adr")
	entries, err := os.ReadDir(adrDir)
	require.NoError(t, err, "the ADR directory could not be read")

	records := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".md") || len(name) < 4 {
			continue
		}
		records[name[:4]] = name
	}
	require.NotEmpty(t, records,
		"no record starting with NNNN- was found under docs/adr; the ADR index must have "+
			"gone BLIND.\nAn empty index counts every ADR reference as a violation.")

	numberReferences, pathReferences := 0, 0
	check := func(source string, lineNo int, text string) {
		for _, match := range adrNumberReference.FindAllStringSubmatch(text, -1) {
			numberReferences++
			if _, seen := records[match[1]]; !seen {
				t.Errorf("%s:%d: there is no decision record called %q (no file starting "+
					"with %s- was found under docs/adr).\nA reference made by number points "+
					"silently at another decision when a record is renumbered.",
					source, lineNo, match[0], match[1])
			}
		}
		for _, match := range adrPathReference.FindAllStringSubmatch(text, -1) {
			pathReferences++
			if records[match[1]] != filepath.Base(match[0]) {
				t.Errorf("%s:%d: there is no file %q; record number %s carries the name %q today.\n"+
					"When a record's title changes its file name changes too, and every "+
					"reference made to it by path becomes a 404.",
					source, lineNo, match[0], match[1], records[match[1]])
			}
		}
	}

	scan := scanDocReferences(t)
	for _, file := range scan.files {
		for _, group := range file.tree.Comments {
			check(file.path, scan.fset.Position(group.Pos()).Line, group.Text())
		}
	}
	for _, doc := range markdownDocs(t) {
		for i, line := range doc.lines {
			check(doc.path, i+1, line)
		}
	}

	require.Positive(t, numberReferences,
		"no reference in the form \"ADR NNNN\" was found anywhere; the pattern must have "+
			"gone BLIND.\nIf the repository has stopped naming its decisions together with "+
			"their justifications, the scope of this audit must be rewritten.")
	require.Positive(t, pathReferences,
		"no docs/adr/NNNN-… path was found anywhere; the path pattern must have gone "+
			"BLIND (the records may have moved to another directory or the naming may have "+
			"changed).")
}

// markdownDoc is a markdown file that has been read.
type markdownDoc struct {
	path  string
	lines []string
}

// markdownDocs reads the markdown files in the repository.
func markdownDocs(t *testing.T) []markdownDoc {
	t.Helper()

	var docs []markdownDoc
	err := filepath.WalkDir(repoRoot, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(current, ".md") {
			return nil
		}
		content, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repoRoot, current)
		if err != nil {
			return err
		}
		docs = append(docs, markdownDoc{
			path:  filepath.ToSlash(rel),
			lines: strings.Split(string(content), "\n"),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("the markdown documents could not be scanned: %v", err)
	}
	require.NotEmpty(t, docs, "no markdown file was found in the repository")
	return docs
}

// rootedPathReference captures the paths written FROM THE REPOSITORY ROOT.
//
// Only rooted paths are audited. Relative mentions ("see interop.go",
// "service/provider.go") are OUT OF SCOPE and this is a measured decision: a file
// with the same name exists in sixteen modules at once (one interop.go per module),
// the headings sqlc generates mention the query files from a sibling directory, and
// third-party file names (transport/http_post.go) have the same shape as well. An
// audit that tried to resolve relative names would either count all of them as
// violations or forgive all of them; neither is what is expected of an audit.
var rootedPathReference = regexp.MustCompile(
	`(?:^|[^\w/.-])((?:cmd|core|internal|plugins|docs|config|deploy|migrations)/[A-Za-z0-9_][A-Za-z0-9_./-]*)`)

// pathReferenceExemption is the justification of a path reference that does NOT
// resolve today.
type pathReferenceExemption struct {
	file   string
	path   string
	reason string
}

// pathReferenceExemptions are the references that deliberately name paths which do
// not exist.
//
// The price of an exemption is measured exactly: the day the path DOES exist the
// test fails and asks for the exemption to be removed. Once the debt is paid it does
// not stay on the books.
var pathReferenceExemptions = []pathReferenceExemption{
	{
		file: "internal/modules/tax/service/service.go",
		path: "core/provider/tax.go",
		reason: "The tax provider contract deliberately lives in the module; the godoc " +
			"names by its name the place it WILL MOVE TO once a second real provider is " +
			"written. The reference is a MOVE DECISION, not a claim about today's location.",
	},
	{
		file: "internal/modules/tax/service/taxprovider.go",
		path: "core/provider/tax.go",
		reason: "The same move decision as it stands in the file where the contract is " +
			"defined; its justification is the same as the exemption in service.go.",
	},
}

// TestThePathReferencesInCommentsResolve verifies that every repository path
// mentioned in a comment REALLY exists.
//
// Path references rot more quietly than symbol references: when a package moves the
// compiler fixes the imports, but nobody sees the path in a comment.
//
// The path is looked for first at the repository root, then in the parent
// directories of the file the reference WAS MADE IN. The second rule comes not from
// a style but from a measurement: the migration references written from inside a
// module are RELATIVE to the module root (a directory with the same name exists at
// the repository root as well) and an audit that looked only from the root would
// unfairly count them as broken.
//
// # Telling a path reference from a symbol reference
//
// Comments also write a symbol name after a directory such as "core/http".
// The distinction comes out of a rule, not out of a list: if the last element starts
// with a CAPITAL letter it is a Go symbol (exported names are capitalized, file
// extensions are lowercase) and the audit verifies only the PACKAGE directory. If
// the symbol itself was written in brackets, [TestTheGodocLinksResolve] already
// audits it; if it was written without brackets it is plain text and, by the scope
// rule at the head of this file, is not audited.
func TestThePathReferencesInCommentsResolve(t *testing.T) {
	t.Parallel()

	scan := scanDocReferences(t)
	used := make([]bool, len(pathReferenceExemptions))
	seen := 0

	for _, file := range scan.files {
		for _, group := range file.tree.Comments {
			// The doc links are STRIPPED first: a link in the form
			// [github.com/…/package.Name] looks like a path but goes to a package
			// rather than to the file system, and auditing it here as well would
			// mean looking the same reference up in two different dictionaries.
			text := stripLinkBrackets(group.Text())
			for _, match := range rootedPathReference.FindAllStringSubmatch(text, -1) {
				mentioned := trimPathReference(match[1])
				if mentioned == "" {
					continue
				}
				mentioned = stripSymbolFromPathReference(mentioned)
				seen++
				if pathReferenceResolves(file.dir, mentioned) {
					continue
				}
				if i := findPathExemption(file.path, mentioned); i >= 0 {
					used[i] = true
					continue
				}
				t.Errorf("%s:%d: the path %q mentioned in a comment does not exist.\n"+
					"The path was looked for at the repository root and in the parent "+
					"directories of the file. If the file moved the reference must move "+
					"too; if it describes a place that has not been written yet its "+
					"justification must be written into pathReferenceExemptions — an "+
					"'it will exist later' reference without a justification cannot be "+
					"told apart from a rotten one.",
					file.path, scan.fset.Position(group.Pos()).Line, mentioned)
			}
		}
	}

	for i, exemption := range pathReferenceExemptions {
		assert.True(t, used[i],
			"exemption STALE: in %s the %q reference is no longer broken (either the "+
				"reference was deleted or the path really came into being).\nJustification: "+
				"%s\nOnce the debt is paid the exemption must come off the books too; an "+
				"exemption that stays behind silently forgives the next broken reference.",
			exemption.file, exemption.path, exemption.reason)
	}

	require.Positive(t, seen,
		"NOT ONE repository path reference was found in the comments; the pattern must "+
			"have gone BLIND.\nThe pattern is anchored from the start on the top-level "+
			"directory names (cmd, internal, plugins, docs, config, deploy, migrations); "+
			"when the tree is reorganized it sees no path at all and every reference to a "+
			"moved file passes silently.")
}

// stripLinkBrackets replaces the [ … ] blocks in a text with a space.
func stripLinkBrackets(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] != '[' {
			b.WriteByte(text[i])
			continue
		}
		offset := strings.IndexByte(text[i+1:], ']')
		if offset < 0 {
			b.WriteByte(text[i])
			continue
		}
		b.WriteByte(' ')
		i += offset + 1
	}
	return b.String()
}

// trimPathReference drops the sentence punctuation at the end of a path reference.
//
// A Turkish suffix is written with an apostrophe ("openapi.go'da") and the path ends
// there; the punctuation that ends a sentence is not part of the path either.
// Without the trimming every correctly written reference would look broken.
func trimPathReference(raw string) string {
	if apostrophe := strings.IndexByte(raw, '\''); apostrophe >= 0 {
		raw = raw[:apostrophe]
	}
	return strings.TrimRight(raw, ".,;:)\"")
}

// stripSymbolFromPathReference drops the symbol from a reference in the form
// "package/path.Symbol".
//
// The criterion of the distinction is the CAPITAL LETTER: in Go exported names start
// with a capital, while file extensions are lowercase. Without the criterion every
// correctly written symbol reference such as "core/http.WriteError" would
// look broken with "there is no such file" — and conversely, a rule that took the
// extension for a symbol would never audit the file name of an "x.go" reference.
func stripSymbolFromPathReference(mentioned string) string {
	pkgPath, parts, ok := splitLink(mentioned)
	if !ok || pkgPath == "" || len(parts) == 0 {
		return mentioned
	}
	first, _ := utf8.DecodeRuneInString(parts[0])
	if !unicode.IsUpper(first) {
		return mentioned
	}
	return pkgPath
}

// pathReferenceResolves looks for the path at the repository root and above the
// directory the reference was made in.
func pathReferenceResolves(referringDir, mentioned string) bool {
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(mentioned))); err == nil {
		return true
	}
	for dir := referringDir; dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		candidate := filepath.Join(repoRoot, filepath.FromSlash(dir), filepath.FromSlash(mentioned))
		if _, err := os.Stat(candidate); err == nil {
			return true
		}
	}
	return false
}

// findPathExemption returns the index of the exemption matching the given reference,
// or -1.
func findPathExemption(file, mentioned string) int {
	return slices.IndexFunc(pathReferenceExemptions, func(e pathReferenceExemption) bool {
		return e.file == file && e.path == mentioned
	})
}

// markdownReferenceClass is the SYNTAX class of a markdown reference.
//
// The class is the measure of the blindness check: every class goes through a
// separate code path (the path pattern, the symbol stripping, the qualifier table,
// the member index) and when one of them breaks the total is still in the hundreds.
// A counter per class makes that loss visible.
type markdownReferenceClass int

const (
	// mdPath is a directory or file path written from the repository root.
	mdPath markdownReferenceClass = iota
	// mdPathQualifiedSymbol carries a package PATH + a symbol:
	// core/http.Scoped.
	mdPathQualifiedSymbol
	// mdQualifiedSymbol qualifies with the package NAME: corehttp.RequireScope.
	mdQualifiedSymbol
	// mdMemberSymbol is a receiver + member pair: Config.FileRoot.
	mdMemberSymbol
	mdClassCount
)

// mdClassNames is the name of a class in error messages.
var mdClassNames = [mdClassCount]string{
	mdPath:                "repository path (core/…, internal/…)",
	mdPathQualifiedSymbol: "path-qualified symbol (core/…/package.Name)",
	mdQualifiedSymbol:     "qualified symbol (package.Name)",
	mdMemberSymbol:        "member pair (Type.Member)",
}

// markdownReference is a single reference in a document that is expected to resolve.
type markdownReference struct {
	doc   string
	line  int
	raw   string
	class markdownReferenceClass
	// path is, in the mdPath class, the file or directory path relative to the
	// repository root.
	path string
	// importPath is, in the symbol classes, the package the name is looked for in.
	importPath string
	// parts are the dot-separated elements of the symbol (Name or Type.Member).
	parts []string
}

// mdQualifiedPattern captures the "package.Name" references inside backticks.
//
// The pattern ENFORCES three things and all three are the very rule that eliminates
// the false positives:
//
//  1. The qualifier starts with a lowercase letter. Package names in Go are
//     lowercase; a qualifier starting with a capital is a TYPE name
//     ("Principal.Kind") and belongs to a separate class, [mdMemberPattern] — there
//     it is not a package but a RECEIVER that is looked for.
//  2. The symbol starts with a CAPITAL letter. The only name that can be mentioned
//     from outside a package is the exported one; this is Go's own rule and it gives
//     exactly the distinction needed in this repository: "product.created" is an
//     event name, "cart.interop" a container registration, "region.currency_code" a
//     column — none of them is a symbol.
//  3. The reference fills the WHOLE of the backticks. Spellings such as
//     "eventbus.Publish/Subscribe" or "db.Pool.Pool()", which are a DESCRIPTION
//     rather than a bare name, are eliminated.
var mdQualifiedPattern = regexp.MustCompile(`^([a-z][a-z0-9]*)\.([A-Z][A-Za-z0-9_]*)(?:\.([A-Za-z_][A-Za-z0-9_]*))?$`)

// mdMemberPattern captures the "Type.Member" references inside backticks.
//
// The receiver name starts with a CAPITAL letter and carries at least one lowercase
// letter. The second condition eliminates file names ("CHANGELOG.md" and "README.md"
// are exactly of this shape); Go type names are CamelCase, and a token that is all
// capitals is in this repository a file or an abbreviation, not a type.
var mdMemberPattern = regexp.MustCompile(`^([A-Z][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)$`)

// mdInlineCode captures the backtick-quoted pieces on a line.
var mdInlineCode = regexp.MustCompile("`([^`]+)`")

// mdOptionHeadingWords are the heading words that make an ADR section an "options
// discussion".
//
// The words are NOT an exemption list: they do not say which path is forgiven, they
// say which SECTION of a document carries no claim about today's repository. See
// [markdownReferences].
//
// The list is BILINGUAL. ADR 0012 made English the repository's working language and
// the new ADRs carry English headings; a rule that knew only the Turkish words would
// take an English-written ADR's rejected options for REAL references and report
// symbols that were never built as broken. The old headings STAY in the list: past
// ADRs are not translated retroactively.
//
// The Turkish entries are spelled with \u escapes because ADR 0012 also forbids a
// Turkish LETTER in a translated file. The escape is the same string to the matcher
// and no letter to the scanner; folding it to ASCII instead would stop it matching
// the very headings it exists for.
var mdOptionHeadingWords = []string{
	"se\u00e7enek", "de\u011ferlendirme",
	"option", "rejected", "evaluation", "considered", "alternative",
}

// markdownReferences extracts the reference candidates in a single document.
//
// # The scope is drawn by a RULE, not by a list
//
// Four decisions, all four given by measurement:
//
//  1. Code blocks (triple backticks) are IN SCOPE. Measurement: every repository
//     path in those blocks resolves today and the blocks carry the most-read map
//     there is (the README's directory tree, the plan document's file headings). If
//     the correctness of a reference depended on the typesetting of the place it was
//     written, we would end up with a map that silently turns wrong when the tree
//     moves.
//  2. Inside backticks and plain text are THE SAME — for path references. The path
//     pattern carries its own anchor (a top-level directory name + a slash) and that
//     shape does not arise by accident in Turkish prose. Measurement: a part of the
//     README's paths is written without backticks (link targets, mentions inside a
//     sentence) and a rule that required the backtick would miss them.
//  3. SYMBOL references ("package.Name" and "Type.Member") are looked for only
//     inside backticks. The justification for this is the reverse of (2): the dotted
//     shape does NOT CARRY its own anchor — sentence-ending punctuation, version
//     numbers and table cells produce the same shape. Here the backtick is the
//     author saying "this is a name".
//  4. The option sections of ADRs are OUT OF SCOPE. The rule looks not at the path
//     itself but at the STRUCTURE of the document: by definition, the option section
//     of an ADR describes a world that was NOT BUILT. Its measured example is in ADR
//     0001 (a contract package that was never written, mentioned as "a neutral
//     package"). A rule was found instead of writing an exemption, because an
//     exemption would have to be rewritten for every new ADR; the role of the
//     section, on the other hand, is a part of the ADR form.
//
// # What this extraction does NOT guarantee
//
//   - Relative mentions ("core/link", "cart/api/store.go") are not seen. The
//     justification is the same as [rootedPathReference]'s and rests on the same
//     measurement: a file with the same name exists in sixteen modules at once.
//   - SINGLE-ELEMENT bare names ("salesChannelVisibleTemplate", "ModuleRegistry")
//     are not seen. See [TestTheReferencesInTheDocsResolve].
//   - The symbols of third-party packages are not verified (see [lookUpInPackage]).
func markdownReferences(doc markdownDoc, qualifiers map[string]string) []markdownReference {
	var references []markdownReference
	section := ""
	codeBlock := false

	for i, line := range doc.lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			codeBlock = !codeBlock
			continue
		}
		if !codeBlock {
			if heading, ok := strings.CutPrefix(line, "## "); ok {
				section = strings.TrimSpace(heading)
			} else if strings.HasPrefix(line, "# ") {
				section = ""
			}
		}
		if isADROptionSection(doc.path, section) {
			continue
		}

		for _, match := range rootedPathReference.FindAllStringSubmatch(line, -1) {
			raw := trimPathReference(match[1])
			if raw == "" {
				continue
			}
			reference := markdownReference{doc: doc.path, line: i + 1, raw: raw}
			if pkgPath, parts := markdownPathQualifiedSymbol(raw); pkgPath != "" {
				reference.class = mdPathQualifiedSymbol
				reference.importPath = modulePath + "/" + pkgPath
				reference.parts = parts
			} else {
				reference.class = mdPath
				reference.path = raw
			}
			references = append(references, reference)
		}

		for _, match := range mdInlineCode.FindAllStringSubmatch(line, -1) {
			member := mdMemberPattern.FindStringSubmatch(match[1])
			if len(member) == 3 && strings.ToUpper(member[1]) != member[1] {
				references = append(references, markdownReference{
					doc:   doc.path,
					line:  i + 1,
					raw:   match[1],
					class: mdMemberSymbol,
					parts: member[1:],
				})
				continue
			}
			parts := mdQualifiedPattern.FindStringSubmatch(match[1])
			if parts == nil {
				continue
			}
			target, ok := qualifiers[parts[1]]
			if !ok {
				continue
			}
			name := []string{parts[2]}
			if parts[3] != "" {
				name = append(name, parts[3])
			}
			references = append(references, markdownReference{
				doc:        doc.path,
				line:       i + 1,
				raw:        match[1],
				class:      mdQualifiedSymbol,
				importPath: target,
				parts:      name,
			})
		}
	}
	return references
}

// isADROptionSection says whether a section is an ADR's option discussion.
//
// The rule holds only under docs/adr: an option section is a part of the ADR FORM.
// In the README or in the CHANGELOG the same heading would be a claim about today's
// repository and there is no reason to forgive it.
func isADROptionSection(docPath, section string) bool {
	if !strings.HasPrefix(docPath, "docs/adr/") || section == "" {
		return false
	}
	lower := strings.ToLower(section)
	return slices.ContainsFunc(mdOptionHeadingWords, func(w string) bool {
		return strings.Contains(lower, w)
	})
}

// markdownPathQualifiedSymbol splits a "package/path.Symbol" reference into its
// package path and symbol elements; it returns an empty package path if the
// reference is not in that form.
//
// The criterion of the distinction is THE SAME as [stripSymbolFromPathReference]'s
// (if the last element starts with a capital it is a symbol) but the result is
// different: in comments the symbol is DROPPED and only the directory is verified,
// while in markdown the symbol is looked for too. The difference is deliberate — a
// symbol in a comment is, when written in brackets, already audited by
// [TestTheGodocLinksResolve], while in markdown there is no bracket and no other
// place left to audit it in.
func markdownPathQualifiedSymbol(reference string) (pkgPath string, parts []string) {
	found, tail, ok := splitLink(reference)
	if !ok || found == "" || len(tail) == 0 {
		return "", nil
	}
	first, _ := utf8.DecodeRuneInString(tail[0])
	if !unicode.IsUpper(first) {
		return "", nil
	}
	return found, tail
}

// markdownQualifiers builds the repository-wide table that goes from a package name
// to an import path.
//
// # Why the table is derived FROM THE SOURCE
//
// When a document writes "corehttp.Principal", only the repository's own imports
// know which package "corehttp" is; a hand-written mapping would be a second truth
// that does not get updated when a package moves.
//
// # AMBIGUOUS qualifiers are DROPPED from the table
//
// Two rules do the eliminating:
//
//   - If the same name is bound to two different paths ("api", "service", "models",
//     "repository", "errors") the qualifier ADDRESSES no package at all.
//   - If the name is a MODULE name it is dropped. In this repository's documents a
//     module name mentions not the Go package but the MODULE, and a module spreads
//     across the api/service/repository trio: "order.CreateOrder" really lives in
//     order/service, not in the module's root package. Without the rule every
//     correctly written module mention would look broken.
func markdownQualifiers(t *testing.T, scan *referenceScan) map[string]string {
	t.Helper()

	table := map[string]string{}
	for _, pkg := range scan.packages {
		for local, importPath := range pkg.imports {
			previous, seen := table[local]
			if importPath == "" || (seen && previous != importPath) {
				table[local] = ""
				continue
			}
			table[local] = importPath
		}
	}
	for _, module := range moduleNames(t) {
		delete(table, module)
	}
	for name, importPath := range table {
		if importPath == "" {
			delete(table, name)
		}
	}

	require.NotEmpty(t, table,
		"NOT ONE qualifier could be derived from the repository's imports; the table must "+
			"have gone BLIND.\nWith an empty table every qualified symbol reference is "+
			"skipped silently and the audit says nothing at all in that class.")
	return table
}

// resolveMarkdownSymbol resolves a symbol reference in markdown; an empty string
// means IT RESOLVED.
//
// Its only difference from [lookUpInPackage] is that a single-element name is looked
// for among the package's MEMBER names as well. The widening is measured: the
// documents mention a method together with its package ("config.Validate") and do
// not write the receiver type — Go does not know this, but for the reader the
// reference resolves. Its price is that the receiverless member rule from the "does
// not guarantee" list at the head of this file spreads to markdown too: if another
// type carries the same name, a reference to a deleted member passes silently.
func (s *referenceScan) resolveMarkdownSymbol(importPath string, parts []string) string {
	reason := s.lookUpInPackage(importPath, parts)
	if reason == "" || len(parts) != 1 {
		return reason
	}
	if target, _ := s.referenceTarget(importPath); target != nil && target.members[parts[0]] {
		return ""
	}
	return reason
}

// resolveMarkdownReference says whether a reference resolves; an empty string means
// IT RESOLVED.
func (s *referenceScan) resolveMarkdownReference(reference markdownReference) string {
	switch reference.class {
	case mdPath:
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(reference.path))); err == nil {
			return ""
		}
		return "there is no such file or directory at the repository root"
	case mdMemberSymbol:
		if s.hasMemberPair(reference.parts[0], reference.parts[1]) {
			return ""
		}
		return fmt.Sprintf("no type %s in the repository has a member called %s",
			reference.parts[0], reference.parts[1])
	default:
		return s.resolveMarkdownSymbol(reference.importPath, reference.parts)
	}
}

// hasMemberPair looks for the "Type.Member" pair ACROSS THE WHOLE REPOSITORY.
//
// The search is package-less because the reference is package-less too; this is the
// price of the pair being writable unqualified, and it is the markdown counterpart
// of the receiverless member rule in the "does not guarantee" list at the head of
// this file: if another type carries the same name, a reference to a deleted member
// passes silently.
func (s *referenceScan) hasMemberPair(typeName, member string) bool {
	for _, pkg := range s.packages {
		if hasMemberReference(pkg, typeName, member, 0) {
			return true
		}
	}
	return false
}

// TestTheReferencesInTheDocsResolve verifies that every path and symbol reference in
// the markdown documents resolves to a REAL file, directory or declaration.
//
// # Why markdown is a separate audit
//
// The references in Go comments are caught by the other checks in this file;
// markdown WAS NOT BEING CAUGHT and this was known through a MEASUREMENT: an
// independent verification broke both a symbol and a path in an ADR and
// internal/arch stayed green. The gap mattered exactly in the round where
// [TestTheDocsCarryNoLineNumberReference] forbade the line number and turned the
// existing references into symbols: the documents were being steered towards a form
// that was not audited.
//
// In the documents the price is GREATER than in the comments. ADRs are long-lived
// decision records: nobody compiles them, nobody rereads them, and the evidence
// blocks are exactly where the rot sets in. The repository has its own rehearsal —
// all four of ADR 0009's four line-number references had shifted.
//
// # Why SINGLE-ELEMENT bare names are out of scope
//
// A single-element name such as "salesChannelVisibleTemplate" or "ModuleRegistry" is
// OUT of scope, and this is not a gap but the boundary being drawn honestly: a bare
// name does not SAY which package it is to be looked for in, which means the audit
// cannot know which sky to look at. The measurement confirms it — the repository's
// markdown holds hundreds of single-element names written inside backticks, and
// among them stand THIRD-PARTY names such as golang-migrate's "SetVersion",
// net/http's "ServeContent" and gqlgen's "SetParserTokenLimit". (The exact number is
// NOT WRITTEN DOWN: the definition of a "single-element candidate" changes with the
// backtick, capital-letter and exportedness criteria, and a number that cannot be
// reproduced is a claim that cannot be verified.) A rule that looked them up in the
// repository's source would declare them broken; a rule that did not look them up
// would only ask "does this name appear somewhere in the repository", and that
// question approves a grep and NOT a reference, because it does not verify which
// package the name is in. The same justification is the very decision of
// [lookUpInPackage] not to verify third-party symbols; there the package is known
// and the audit stays silent, here the package is not known at all.
//
// The "Type.Member" PAIR, on the other hand, IS in scope (see [mdMemberSymbol]) and
// the ground of the distinction is measurement, not intuition: the pair carries a
// context the single-element name does not (the receiver type) and all 18 of the 18
// mentions in the repository are types of this repository — there is no third-party
// pair. If one appears one day the way out is open and is a better spelling:
// qualify the pair with its package ("http.Server.ReadTimeout"), a form that
// resolves through [lookUpInPackage].
//
// The price of being out of scope was measured and accepted: in this round there
// were two rotten references that could not be caught because they were written with
// a single element ("ModuleRegistry" and "regionCurrency"); both were turned into an
// auditable form, which means that from today on they ARE in scope. The right way to
// fix a rotten reference is not to widen the audit but to write the reference in an
// auditable form.
func TestTheReferencesInTheDocsResolve(t *testing.T) {
	t.Parallel()

	scan := scanDocReferences(t)
	qualifiers := markdownQualifiers(t, scan)

	for _, doc := range markdownDocs(t) {
		for _, reference := range markdownReferences(doc, qualifiers) {
			reason := scan.resolveMarkdownReference(reference)
			if reason == "" {
				continue
			}
			t.Errorf("%s:%d: the %q reference does not resolve — %s.\n"+
				"A reference in a document sends the reader SEARCHING; if the thing "+
				"searched for does not exist, the reader learns that only after grepping "+
				"the whole repository. If a name or a path changed the reference must "+
				"change too; if the thing described was never built (like an option an ADR "+
				"rejected) that description belongs in the document's OPTIONS section.",
				reference.doc, reference.line, reference.raw, reason)
		}
	}
}

// markdownResolverExample is the example, made of DELIBERATELY BROKEN references,
// that tests whether the resolver can really say "did not resolve" in EVERY class.
//
// # Why the scanner example is not enough
//
// [markdownScannerExample] protects the scanner: it pins down which spelling COUNTS
// AS a reference. But the resolver can go blind while the scanner is intact and that
// blindness is SILENT: the candidate count does not drop, the counter stays happy,
// every reference counts as "resolved". It was measured — making the path branch of
// resolveMarkdownReference "always resolved" silently kills 142 of the 223 audited
// references (close to three quarters) and the WHOLE arch package stayed green.
//
// This example closes that gap: it makes a reference to a target that does not exist
// in all four of the four classes and requires ALL FOUR to be reported. Whichever
// class the resolver goes blind in, that line drops out.
var markdownResolverExample = markdownDoc{
	path: "docs/adr/9997-resolver-example.md",
	lines: []string{
		"# ADR 9997 — resolver example",
		"",
		"## Context",
		"",
		"Path: `internal/core/no-such-package`.",
		"Path-qualified symbol: `core/http.NoSuchSymbol`.",
		"Qualified symbol: `corehttp.NoSuchSymbol`.",
		"Member pair: `Config.NoSuchMember`.",
	},
}

// TestTheMarkdownResolverIsNotBlind verifies that the resolver bites in all four
// classes.
//
// It is a positive control: it tests what the audit CAN FIND, not what it does not.
// If a class's resolution turns into "always fine" that line drops out of the list
// and this test fails — while [TestTheReferencesInTheDocsResolve] would stay
// silently green.
func TestTheMarkdownResolverIsNotBlind(t *testing.T) {
	t.Parallel()

	scan := scanDocReferences(t)
	qualifiers := markdownQualifiers(t, scan)

	references := markdownReferences(markdownResolverExample, qualifiers)
	require.Len(t, references, 4,
		"all four classes of the resolver example must get through the scanner; if they "+
			"do not, what is being tested is not the resolver but the scanner (see "+
			"markdownScannerExample)")

	var reported []string
	for _, reference := range references {
		if reason := scan.resolveMarkdownReference(reference); reason != "" {
			reported = append(reported, mdClassNames[reference.class])
		}
	}

	require.ElementsMatch(t,
		[]string{
			mdClassNames[mdPath],
			mdClassNames[mdPathQualifiedSymbol],
			mdClassNames[mdQualifiedSymbol],
			mdClassNames[mdMemberSymbol],
		},
		reported,
		"the resolver must report a reference made to a target that does not exist in "+
			"ALL FOUR classes.\nA class that is missing means its resolution has gone "+
			"blind: the candidate count does not drop, the counter stays happy and every "+
			"reference in that class silently counts as RESOLVED.")
}

// markdownScannerExample is the example that tests EVERY rule of the scanner in a
// single document.
//
// The example is not a fixture but the EXECUTABLE spelling of the rules: which
// spelling counts as a reference and which does not is read off the code here. If
// the rules loosen, the example fails.
var markdownScannerExample = markdownDoc{
	path: "docs/adr/9999-scanner-example.md",
	lines: []string{
		"# ADR 9999 — scanner example",
		"",
		"## Context",
		"",
		"Path in backticks: `core/db/migrate.go`.",
		"Plain text path: it lives inside internal/arch.",
		"Path-qualified symbol: `core/http.Scoped`.",
		"Qualified symbol: `corehttp.Principal`.",
		"",
		"```",
		"internal/core/config/config.go",
		"```",
		"",
		"Member pair: `Principal.Kind`.",
		"",
		"The event name `product.created`, the container registration `cart.interop`,",
		"the column `region.currency_code` and the file name `CHANGELOG.md` are NOT",
		"symbols. A mention by module name, `order.CreateOrder`, is out of scope too.",
		"So are the relative mention `service/interop.go` and the spelling `db.Pool.Pool()`.",
		"",
		"## Rejected options",
		"",
		"A neutral package such as `internal/contracts/product` and a `db.Conn` interface.",
	},
}

// TestTheMarkdownReferenceScannerIsNotBlind verifies that the markdown scanner STILL
// sees references and classifies what it sees CORRECTLY.
//
// [TestTheReferencesInTheDocsResolve] passes SILENTLY when it finds no candidate at
// all: a scanner whose pattern is broken and a repository with no rot look exactly
// the same in the output. An audit that stays green in a vacuum is worse than an
// audit that does not exist.
func TestTheMarkdownReferenceScannerIsNotBlind(t *testing.T) {
	t.Parallel()

	scan := scanDocReferences(t)
	qualifiers := markdownQualifiers(t, scan)

	// 1. The rules: what comes out of the example document is tested EXACTLY.
	var example []string
	for _, reference := range markdownReferences(markdownScannerExample, qualifiers) {
		example = append(example, fmt.Sprintf("%s|%s", mdClassNames[reference.class], reference.raw))
	}
	require.Equal(t, []string{
		mdClassNames[mdPath] + "|core/db/migrate.go",
		mdClassNames[mdPath] + "|internal/arch",
		mdClassNames[mdPathQualifiedSymbol] + "|core/http.Scoped",
		mdClassNames[mdQualifiedSymbol] + "|corehttp.Principal",
		mdClassNames[mdPath] + "|internal/core/config/config.go",
		mdClassNames[mdMemberSymbol] + "|Principal.Kind",
	}, example,
		"the scanner read the example document differently than expected.\nThe example is "+
			"the executable spelling of the scope rules: an extra line says a FALSE "+
			"POSITIVE rule (event name, container registration, column, file name, module "+
			"mention, relative path, option section) has loosened; a missing line says the "+
			"scanner has GONE BLIND in that class.")

	// 2. The real documents: is every class still seen in the repository?
	counts := [mdClassCount]int{}
	optionSections := 0
	for _, doc := range markdownDocs(t) {
		for _, reference := range markdownReferences(doc, qualifiers) {
			counts[reference.class]++
		}
		for _, line := range doc.lines {
			if heading, ok := strings.CutPrefix(line, "## "); ok &&
				isADROptionSection(doc.path, strings.TrimSpace(heading)) {
				optionSections++
			}
		}
	}
	for class, count := range counts {
		require.Positive(t, count,
			"NOT ONE reference of the %s class was found in the documents; the scanner "+
				"must have gone BLIND in this class.\nIf the class really has left the "+
				"documents, both this assertion and the matching extraction branch must be "+
				"deleted; staying green silently gives the impression that the class is "+
				"still audited.",
			mdClassNames[class])
	}
	require.Positive(t, optionSections,
		"no ADR had an option section; the rule that separates the hypothetical paths "+
			"must have gone BLIND.\nIf the headings changed, mdOptionHeadingWords must "+
			"change too: a rule that does not match takes the paths of rejected options for "+
			"real references and produces a pile of false accusations.")

	// 3. Positive controls: the resolution can silently swallow the difference
	// between "I did not find it" and "I could not look". That a symbol which does
	// not exist is REJECTED is separately tested for both a repository and a stdlib
	// package.
	require.NotEmpty(t, scan.resolveMarkdownSymbol(modulePath+"/core/http", []string{"NoSuchSymbol"}),
		"a symbol that does not exist in a repository package was approved; the member "+
			"widening must be accepting every name. In that case every qualified reference "+
			"in markdown passes unverified.")
	require.NotEmpty(t, scan.resolveMarkdownSymbol("net/http", []string{"NoSuchSymbol"}),
		"a symbol that does not exist in a stdlib package was approved; the GOROOT source "+
			"must have been unreadable.")
	require.True(t, scan.hasMemberPair("Principal", "Kind"),
		"a member pair that does exist in the repository was not found; the member index "+
			"must have gone BLIND, and in that case every Type.Member reference is "+
			"unfairly counted as broken.")
	require.False(t, scan.hasMemberPair("Principal", "NoSuchMember"),
		"a member pair that does not exist was approved; the search must be accepting "+
			"every name.")
}

// lineNumberReference captures the references in the form "file.go:line".
var lineNumberReference = regexp.MustCompile(`[A-Za-z0-9_./-]+\.go:\d+`)

// TestTheDocsCarryNoLineNumberReference FORBIDS the references made with a line
// number.
//
// The ban is not a matter of style but a MEASURED result. There were four such
// references in the repository (all of them in the evidence block of an ADR) and all
// four had rotted: at the lines they pointed at stood not the code shown as evidence
// but the middle of a neighboring comment or another function. None of them had
// produced an error, because nothing verifies a line number.
//
// This is the only form of reference STRUCTURALLY doomed to rot: even adding a line
// above it shifts it, and the shift leaves no trace.
//
// # The form put in its place IS NOW audited
//
// This godoc once said the opposite: it said that nothing verified the
// "package/path.Symbol" references in the ADRs and in the README, and that auditing
// markdown was a separate job that had not been done yet. The gap was closed with
// [TestTheReferencesInTheDocsResolve] and the closing was verified by measurement:
// breaking either the symbol or the path in an ADR now FAILS the test. The form the
// ban steers towards came under audit in the same round as the ban itself.
//
// TWO points are still open and both of them were MEASURED:
//
//   - BARE symbol names; the justification is written in the
//     [TestTheReferencesInTheDocsResolve] godoc: a name whose package is not
//     mentioned does not say which package it is to be looked for in.
//   - The qualified symbols inside a CODE BLOCK. Path references are audited in code
//     blocks too, symbols are not: a Go code block can carry a third-party or a
//     hypothetical call, and a rule that declared them broken would drag every
//     example snippet in the documents into the audit.
//
// The ban is right all the same and its justification is to be read together with
// this gap: a rotten line number is SILENT — the reader looks at the wrong code and
// thinks that is what they are looking at. A rotten symbol name can at least BE
// SEARCHED FOR: the reader who greps the name finds no result and understands that
// the reference has gone stale. The ban turns silent rot into noisy rot.
//
// The scope of the ban is Go comments and markdown documents. The "file.go:line"
// strings that appear in the source CODE (error messages, position formats) are out
// of scope: they are not a reference but a position PRODUCED at run time.
func TestTheDocsCarryNoLineNumberReference(t *testing.T) {
	t.Parallel()

	// Positive control: the ban itself can go blind too. A change that broke the
	// pattern would leave behind a test that finds nothing and therefore stays green
	// forever — while going on carrying the word "ban" in its name.
	require.Regexp(t, lineNumberReference, "see core/http/guard.go:16",
		"the pattern does not catch even its own example; the ban must have gone BLIND")

	report := func(source string, lineNo int, text string) {
		for _, match := range lineNumberReference.FindAllString(text, -1) {
			t.Errorf("%s:%d: %q — a reference is not made with a line number.\n"+
				"A line number points somewhere else the moment a line is added above it, "+
				"and nothing reports that. Write a SYMBOL instead (package.Function, "+
				"Type.Method) or mention the godoc's heading: when a name changes, the "+
				"audits in this file catch it.",
				source, lineNo, match)
		}
	}

	scan := scanDocReferences(t)
	for _, file := range scan.files {
		for _, group := range file.tree.Comments {
			report(file.path, scan.fset.Position(group.Pos()).Line, group.Text())
		}
	}
	for _, doc := range markdownDocs(t) {
		for i, line := range doc.lines {
			report(doc.path, i+1, line)
		}
	}
}

// emphasisHint produces an extra diagnostic sentence for references that look like a
// word written inside brackets for EMPHASIS; in any other case it returns an empty
// string.
//
// # Why it is needed
//
// The link recognition rule deliberately departs from Go and counts lowercase local
// names as links too (half of the godocs sit above unexported definitions). The
// measured cost of that is a single ASCII Turkish word inside brackets — such as
// "zorunlu", "sonuc" or "tanim" — being taken for a link. Turkish is rich in ASCII
// words, which makes this not a theoretical but an EXPECTED situation.
//
// The raw error message tells that author "the package is not imported" and the
// author, knowing they wrote no link, finds the message meaningless. An audit found
// meaningless ends up silenced; this is why the message SAYS its guess.
//
// # Why the rule is not loosened
//
// Saying "do not count a single word with no capital letter as a link" would take a
// reference made to a real local helper such as "kirp" outside the audit. The
// direction of failure is on the safe side as well: the test FAILS, it does not
// approve silently. While the price can be paid with a one-sentence diagnostic,
// narrowing the scope would mean losing the real broken links that are being caught.
func emphasisHint(name string) string {
	if strings.ContainsAny(name, "._*") || name != strings.ToLower(name) {
		return ""
	}
	return "\nDid you mean EMPHASIS? In a godoc the square bracket is RESERVED for a " +
		"link (pkg.go.dev shows it as a broken link). Use quotes for emphasis " +
		"or write the word in CAPITALS"
}
