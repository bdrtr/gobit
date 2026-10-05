package arch_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conditionImport is the package that holds the rule words and reads a rule
// condition (ADR 0396).
const conditionImport = "github.com/bdrtr/gobit/internal/core/condition"

// conditionTrees are the trees whose rules the one evaluator reads.
var conditionTrees = []string{"internal/modules", "internal/workflows", "plugins"}

// conditionModels are the models packages that spell a rule operator, and
// how many of the package's words each converts.
var conditionModels = map[string]int{
	"internal/modules/pricing/models":     8,
	"internal/modules/fulfillment/models": 8,
	"internal/modules/promotion/models":   9,
}

// conditionAllowlist is the comparison switches that are not on the evaluator
// yet, as "file:function". The segment flow's compare moves onto
// condition.Compare in a later record (ADR 0396).
var conditionAllowlist = []string{
	"internal/workflows/segment/segment.go:compare",
}

// comparisonWords are the numeric words a case may not name outside the
// evaluator.
var comparisonWords = []string{"gt", "gte", "lt", "lte"}

// TestOneConditionEvaluator holds ADR 0396: pricing, fulfillment and promotion
// read a rule condition through internal/core/condition, and no switch under
// the module, workflow and plugin trees compares a comparison word by hand.
//
// It fails for three shapes, all read from syntax:
//
//   - a `case` whose value is gt, gte, lt or lte, in a non-test file and
//     outside the three models packages' RuleOperator.Valid. The value is
//     resolved through the constants the case names, across packages, so a
//     word under any name is caught: a string literal, a module's OpGte,
//     condition.Gte, the customer module's SegmentGte, a conversion of any of
//     them. That is a matcher copied back, or a hand-written Numeric, and the
//     copies are what drifted into D252.
//   - a RuleOperator constant in the three models packages that is not
//     converted from condition's word.
//   - a RuleOperator.Valid in those packages that is anything but one switch
//     on the receiver whose cases list those converted constants and return
//     true, with a default that returns false. A word Valid admits beside them,
//     as a string, an untyped constant or a variable, would be written by the
//     API and matched by nothing, the same silently dead rule as D252.
//
// An `if` chain comparing operators outside Valid is outside what it reads;
// that is the review's.
func TestOneConditionEvaluator(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	consts := &constIndex{t: t, fset: fset, pkgs: map[string]*constPackage{}}
	allowed := map[string]bool{}
	converted := map[string]map[string]bool{}
	var valids []ruleOperatorValid

	for _, tree := range conditionTrees {
		for _, path := range productionFiles(t, filepath.Join(repoRoot, tree)) {
			rel := filepath.ToSlash(repoPath(path))
			dir := filepath.ToSlash(filepath.Dir(rel))
			_, model := conditionModels[dir]
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			require.NoError(t, err, "%s could not be parsed", rel)

			for _, decl := range file.Decls {
				name := ""
				if fn, ok := decl.(*ast.FuncDecl); ok {
					name = fn.Name.Name
					if model && name == "Valid" && receiverType(fn) == "RuleOperator" {
						valids = append(valids, ruleOperatorValid{dir: dir, rel: rel, fn: fn})
						continue
					}
				}
				hits := comparisonCases(decl, file, dir, consts)
				if len(hits) == 0 {
					continue
				}
				key := rel + ":" + name
				if slices.Contains(conditionAllowlist, key) {
					allowed[key] = true
					continue
				}
				for _, pos := range hits {
					t.Errorf("%s:%d: a case names a comparison operator in %q — read the rule "+
						"through condition.Match or condition.Compare (ADR 0396); a matcher "+
						"copied per module keeps answering after one copy drifts (D252).",
						rel, fset.Position(pos).Line, name)
				}
			}

			if model {
				if converted[dir] == nil {
					converted[dir] = map[string]bool{}
				}
				for _, name := range checkRuleOperatorConsts(t, fset, file, rel, conditionLocalName(file)) {
					converted[dir][name] = true
				}
			}
		}
	}

	seen := map[string]int{}
	for _, valid := range valids {
		seen[valid.dir]++
		checkRuleOperatorValid(t, fset, valid, converted[valid.dir])
	}

	for _, entry := range conditionAllowlist {
		assert.True(t, allowed[entry],
			"the allowlist names %s and no comparison case was found there: remove the entry, "+
				"an entry that matches nothing allows the next copy written under its name", entry)
	}
	for pkg, floor := range conditionModels {
		assert.Equal(t, 1, seen[pkg],
			"%s declares %d RuleOperator.Valid methods, one is expected; none means the walker "+
				"has gone blind", pkg, seen[pkg])
		assert.GreaterOrEqual(t, len(converted[pkg]), floor,
			"%s converts %d RuleOperator constants from condition, at least %d expected; fewer "+
				"means the walker has gone blind or a word was dropped", pkg, len(converted[pkg]), floor)
	}
}

// TestComparisonCasesResolveEveryName holds the gate's reader to the names a
// comparison word has in the tree. Nothing in the tree names one through
// another package after ADR 0396, so a resolver gone blind to a selector, an
// alias or a conversion would leave the gate above green; this source does.
func TestComparisonCasesResolveEveryName(t *testing.T) {
	t.Parallel()

	const src = `package probe

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/condition"
	customer "github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	pm "github.com/bdrtr/gobit/internal/modules/promotion/models"
)

const localGte = models.OpGte

const localEq = "eq"

func probe(op string) {
	switch op {
	case "gte": // hit
	case condition.Lt: // hit
	case models.OpGt: // hit
	case customer.SegmentLte: // hit
	case pm.OpLt: // hit
	case localGte: // hit
	case string(models.RuleOperator("gt")): // hit
	case (models.OpLte): // hit
	case "eq", localEq, models.OpEq, condition.AnyIn, customer.SegmentIn, http.MethodGet:
	}
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", src, parser.ParseComments|parser.SkipObjectResolution)
	require.NoError(t, err)
	const dir = "internal/modules/probe/service"
	consts := &constIndex{t: t, fset: fset, pkgs: map[string]*constPackage{}}
	local := &constPackage{consts: map[string]constDecl{}}
	local.add(file, dir)
	consts.pkgs[dir] = local

	var want []int
	for _, group := range file.Comments {
		if strings.TrimSpace(group.Text()) == "hit" {
			want = append(want, fset.Position(group.Pos()).Line)
		}
	}
	var got []int
	for _, decl := range file.Decls {
		for _, pos := range comparisonCases(decl, file, dir, consts) {
			got = append(got, fset.Position(pos).Line)
		}
	}
	require.Len(t, want, 8, "the probe marks eight hits")
	assert.Equal(t, want, got, "the lines whose case names gt, gte, lt or lte, and only those")
}

// ruleOperatorValid is one models package's RuleOperator.Valid.
type ruleOperatorValid struct {
	dir, rel string
	fn       *ast.FuncDecl
}

// receiverType is the name of a method's receiver type, or "" for a function.
func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// checkRuleOperatorValid fails unless Valid is one switch on its receiver
// whose cases name only the package's converted constants and return true,
// and whose default returns false.
func checkRuleOperatorValid(t *testing.T, fset *token.FileSet, valid ruleOperatorValid, converted map[string]bool) {
	t.Helper()

	fail := func(pos token.Pos, format string, args ...any) {
		t.Errorf("%s:%d: RuleOperator.Valid %s — Valid admits only the words converted from "+
			"condition (ADR 0396); a word it admits beside them is written by the API and matched "+
			"by nothing (D252).", valid.rel, fset.Position(pos).Line, fmt.Sprintf(format, args...))
	}

	recv := ""
	if names := valid.fn.Recv.List[0].Names; len(names) == 1 {
		recv = names[0].Name
	}
	body := valid.fn.Body
	if body == nil || len(body.List) != 1 {
		fail(valid.fn.Pos(), "has to be one switch statement")
		return
	}
	sw, ok := body.List[0].(*ast.SwitchStmt)
	if !ok || sw.Init != nil || recv == "" || !isIdent(sw.Tag, recv) {
		fail(body.List[0].Pos(), "has to be one switch on its receiver")
		return
	}
	for _, stmt := range sw.Body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			fail(stmt.Pos(), "has a statement that is not a case")
			continue
		}
		want := "true"
		if clause.List == nil {
			want = "false"
		}
		if !returnsIdent(clause.Body, want) {
			fail(clause.Pos(), "has a clause that does not just return %s", want)
		}
		for _, expr := range clause.List {
			ident, ok := expr.(*ast.Ident)
			if !ok || !converted[ident.Name] {
				fail(expr.Pos(), "admits a case that is not a RuleOperator(condition.<Word>) constant")
			}
		}
	}
}

// returnsIdent reports whether the statements are exactly `return <name>`.
func returnsIdent(body []ast.Stmt, name string) bool {
	if len(body) != 1 {
		return false
	}
	ret, ok := body[0].(*ast.ReturnStmt)
	return ok && len(ret.Results) == 1 && isIdent(ret.Results[0], name)
}

// conditionLocalName is the name the file imports the condition package under,
// or "" when it does not import it.
func conditionLocalName(file *ast.File) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != conditionImport {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return "condition"
	}
	return ""
}

// comparisonCases returns the position of every case expression under the
// declaration whose value is a comparison word.
func comparisonCases(decl ast.Decl, file *ast.File, dir string, consts *constIndex) []token.Pos {
	var hits []token.Pos
	ast.Inspect(decl, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			value, ok := consts.stringValue(expr, file, dir, 0)
			if ok && slices.Contains(comparisonWords, value) {
				hits = append(hits, expr.Pos())
			}
		}
		return true
	})
	return hits
}

// constIndex resolves a constant expression to its string value across the
// repository's packages, reading each package's constants once.
type constIndex struct {
	t    *testing.T
	fset *token.FileSet
	pkgs map[string]*constPackage
}

// constPackage is one package's name and its constants.
type constPackage struct {
	name   string
	consts map[string]constDecl
}

// constDecl is one constant's value expression and where it is written.
type constDecl struct {
	value ast.Expr
	file  *ast.File
	dir   string
}

// pkg reads the constants of the package at the repository-relative dir.
func (x *constIndex) pkg(dir string) *constPackage {
	if p, ok := x.pkgs[dir]; ok {
		return p
	}
	p := &constPackage{consts: map[string]constDecl{}}
	x.pkgs[dir] = p
	full := filepath.Join(repoRoot, filepath.FromSlash(dir))
	if info, err := os.Stat(full); err != nil || !info.IsDir() {
		return p
	}
	for _, path := range packageFiles(x.t, full) {
		file, err := parser.ParseFile(x.fset, path, nil, parser.SkipObjectResolution)
		require.NoError(x.t, err, "%s could not be parsed", path)
		p.add(file, dir)
	}
	return p
}

// add records the file's package name and its constants.
func (p *constPackage) add(file *ast.File, dir string) {
	p.name = file.Name.Name
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range value.Names {
				if i < len(value.Values) {
					p.consts[name.Name] = constDecl{value: value.Values[i], file: file, dir: dir}
				}
			}
		}
	}
}

// importDir is the repository-relative dir of the package the file imports
// under local, or "" when it is not one of this module's packages.
func (x *constIndex) importDir(file *ast.File, local string) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !strings.HasPrefix(path, modulePath+"/") {
			continue
		}
		dir := strings.TrimPrefix(path, modulePath+"/")
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		} else {
			name = x.pkg(dir).name
		}
		if name == local {
			return dir
		}
	}
	return ""
}

// stringValue resolves an expression written in file, of the package at dir,
// to the string constant it denotes: a string literal, a constant of the same
// package or of an imported one, or a conversion of any of these.
func (x *constIndex) stringValue(expr ast.Expr, file *ast.File, dir string, depth int) (string, bool) {
	if depth > 8 {
		return "", false
	}
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(e.Value)
		return value, err == nil
	case *ast.Ident:
		decl, ok := x.pkg(dir).consts[e.Name]
		if !ok {
			return "", false
		}
		return x.stringValue(decl.value, decl.file, decl.dir, depth+1)
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		target := x.importDir(file, pkg.Name)
		if target == "" {
			return "", false
		}
		decl, ok := x.pkg(target).consts[e.Sel.Name]
		if !ok {
			return "", false
		}
		return x.stringValue(decl.value, decl.file, decl.dir, depth+1)
	case *ast.CallExpr:
		// A conversion such as models.RuleOperator("gte").
		if len(e.Args) != 1 {
			return "", false
		}
		return x.stringValue(e.Args[0], file, dir, depth+1)
	case *ast.ParenExpr:
		return x.stringValue(e.X, file, dir, depth+1)
	default:
		return "", false
	}
}

// checkRuleOperatorConsts fails for every RuleOperator constant in the file
// that is not RuleOperator(condition.<Word>), and returns the names of those
// that are.
func checkRuleOperatorConsts(t *testing.T, fset *token.FileSet, file *ast.File, rel, conditionName string) []string {
	t.Helper()

	var converted []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			typed := isIdent(value.Type, "RuleOperator")
			for i, name := range value.Names {
				var expr ast.Expr
				if i < len(value.Values) {
					expr = value.Values[i]
				}
				conversion := isRuleOperatorConversion(expr)
				if conversion && fromCondition(expr, conditionName) {
					converted = append(converted, name.Name)
					continue
				}
				if !typed && !conversion {
					continue
				}
				t.Errorf("%s:%d: %s is a RuleOperator constant not written as "+
					"RuleOperator(condition.<Word>) — a module spells no rule word of its own (ADR 0396); "+
					"a word the API writes and the evaluator does not know is a rule that silently matches nothing (D252).",
					rel, fset.Position(name.Pos()).Line, name.Name)
			}
		}
	}
	return converted
}

// isIdent reports whether the expression is the bare identifier name.
func isIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

// isRuleOperatorConversion reports whether the expression is RuleOperator(x).
func isRuleOperatorConversion(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && isIdent(call.Fun, "RuleOperator") && len(call.Args) == 1
}

// fromCondition reports whether a RuleOperator(x) conversion's x is one of
// condition's words.
func fromCondition(expr ast.Expr, conditionName string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || conditionName == "" {
		return false
	}
	sel, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == conditionName
}
