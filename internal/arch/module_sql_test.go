package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file enforces ONE invariant: A MODULE'S SQL NAMES ONLY ITS OWN TABLES.
//
// ADR 0001 rests on a single sentence — a module never reaches into another
// module — and the whole architecture is built on it. Before this file existed
// that sentence was enforced in exactly two ways, and a measurement taken on
// 2026-09-05 proved that BOTH of them miss its most direct violation:
//
//   - [TestThereIsNoCrossModuleForeignKey] reads the migrations and finds a
//     REFERENCES clause pointing at a table the module does not own. It reads
//     DDL only; a SELECT is invisible to it.
//   - depguard and [TestModulesDoNotImportEachOther] ban a Go IMPORT of another
//     module's package. They read import lists; SQL is a string to them.
//
// So a line like "SELECT quantity FROM inventory_levels" written inside the
// PRODUCT module's own query file passed every gate in this repository GREEN.
// And it is not a theoretical hole: every module is handed the same connection
// pool, so that query does not fail, does not warn, and returns the right rows.
// It works — which is exactly why nothing caught it and exactly why it is
// lethal. The coupling it creates is invisible until the day the inventory
// module is moved to its own database or its own service, and on that day the
// product module breaks with a "relation does not exist" from a file nobody
// thought to look at.
//
// # What is LEGAL, and why the list is short
//
// A module's SQL may name a table its OWN migrations create, and may name a
// table NO module owns. The second half is not a loophole, it is the whole of
// the shared substrate:
//
//   - The core's own tables. event_outbox belongs to core/eventbus/outbox,
//     the audit log to core/audit, the job record to the job store and the
//     workflow record to the workflow store. None of them is a module's, and a
//     module writing its own outbox row inside its own transaction is the
//     documented mechanism, not a violation.
//   - The LINK tables. core/link creates them at RUN TIME out of a Define call
//     (ADR 0005: the link schema is not written in a migration file, because
//     which links exist is not known at compile time — a plugin may declare
//     one). They therefore appear in NO migration and no module owns them.
//
// The second case has an argued precedent that a future reader must not
// "fix" by banning it. internal/modules/product/repository/saleschannel.go
// filters the product listing against the product ↔ sales channel link table
// in SQL, and states why in its own file header:
//
//	"The first person reading it rightly asks 'is another module's table being
//	touched here'. No: SalesChannelLinkTable is not auth's but the table of the
//	link PRODUCT declares (see service.LinkProductSalesChannel) and there is
//	nothing inside it beyond two free id strings. The query sees none of auth's
//	tables, no REFERENCES is added, and if auth changed its schema this
//	condition would not be affected — the binding Principle 2.2 forbids is
//	exactly this one and it is not established here."
//
// The brackets around the constant's name are the quote's only edit: this
// package does not import the product repository, so a link written here would
// promise the reader a symbol it cannot reach.
//
// That reasoning is what the rule below encodes: ownership is what makes a
// table someone else's, and nobody owns a link table. Turning this gate into
// "no table outside your migrations" would delete that filter and put the
// storefront's product listing back to paginating wrong.
//
// # Which SQL is read
//
// Three surfaces, because a rule that reads two thirds of a module's SQL is
// the same kind of gap this file was written to close:
//
//   - internal/modules/product/queries and its siblings — the sqlc source.
//   - The module's migrations. Almost all of it is DDL, but a data backfill
//     ("INSERT INTO mine SELECT ... FROM yours") is a cross-module read that
//     would otherwise have a whole directory to hide in.
//   - Hand-written SQL in Go string literals. This is not a corner:
//     internal/modules/product/repository/saleschannel.go is the storefront's
//     whole listing and count, written by hand precisely BECAUSE sqlc cannot
//     see the link table's schema, and the order module has more. That file
//     carried a line count here until 2026-09-06, when the filter body became
//     a builder and the number went stale in the same commit that made the
//     scanner cover it — which is why the sentence now describes what the file
//     IS. The scanner walks every string literal in the file rather than only
//     its constant declarations, so SQL living inside a function body is
//     covered exactly as a package-level constant is.
//
// # Tests are NOT scanned, and that is a decision rather than an oversight
//
// A test that seeds another module's table is reaching across the same
// boundary, and the argument for auditing it is real. It is not audited, for
// one measured reason and one argued one. Measured: at the time this gate was
// written the module trees held 183 SQL-shaped string constants inside
// _test.go files and NOT ONE of them named another module's table, so the rule
// would buy nothing today. Argued: an integration test that drives a flow
// spanning several modules legitimately has to arrange state in all of them,
// and the only honest places to arrange it are the owning module's service or
// its table. Banning the second without the first existing yet would push a
// real test either into an exemption or into being written worse — and a gate
// that makes people write worse code is a gate that gets deleted.
//
// # What the extractor deliberately does NOT see
//
// A parser that cries wolf gets deleted, so the reader below UNDER-matches on
// purpose and the skips are written down rather than discovered:
//
//   - SQL assembled through fmt.Sprintf or from a runtime variable. Only
//     constant folding is done (literal + literal, literal + local constant);
//     an operand it cannot resolve becomes a placeholder name that no module
//     owns.
//   - A constant that lives in ANOTHER package. The import table is not
//     followed. Measured: no module builds SQL that way today.
//   - A Go string that does not carry a full statement shape. The classifier
//     wants two SQL keywords in sequence (SELECT … FROM, UPDATE … SET,
//     INSERT INTO, DELETE FROM, JOIN … ON), and it wants them in UPPERCASE.
//     Both narrowings were measured before they were written, against the 640
//     string constants the loose form accepts across the module trees. The
//     uppercase requirement is what keeps the English sentence "could not
//     update product: %s" out — a sentence that, written in any OTHER module,
//     would have been reported as that module reading product's table, and the
//     report would have been a lie. The keyword pair costs 15 of the 640, and
//     the fifteen were read one by one: seven are table-less administrative
//     statements (pg_advisory_xact_lock, to_regclass, setval) and eight are
//     prose sentences that merely shout FOR UPDATE or FROM THE RECORD. NOT ONE
//     of the fifteen names a table any module owns.
//
// [TestTheSQLTableScannerIsNotBlind] pins the forms that DO matter, so the
// under-matching cannot quietly grow into not matching at all.

// queriesDirName is the subdirectory holding a module's sqlc query source.
//
// It sits next to [migrationsDirName] as a named constant for the same reason:
// when the directory is renamed, a scan that hard-codes the old name finds no
// file and passes having read nothing. That the name still holds is verified
// by the file count this audit requires.
const queriesDirName = "queries"

// upMigrationSuffix is the suffix of a forward migration.
//
// Ownership is read from the UP files only. A down file drops what the up file
// created; letting it speak would make ownership depend on which half of the
// pair the walk happened to read.
const upMigrationSuffix = ".up.sql"

// unresolvedGoOperand stands in for a piece of a Go string expression that
// could not be folded to a constant.
//
// It is deliberately a name no migration would ever create, so a reference to
// it resolves to "owned by nobody" and is legal. The alternative — dropping the
// operand and gluing the neighbors together — is worse than it looks: in
// `"SELECT x FROM " + table + " WHERE y"` the glue would put WHERE where the
// table name belongs, and the audit would start reporting the keyword as a
// table.
const unresolvedGoOperand = " unresolved_go_expression "

// sqlToken is one lexical piece of an SQL body.
//
// The offset is kept because the failure message names a LINE. A finding that
// says only "this file names inventory_levels" sends the reader searching
// through 426 lines of hand-written SQL; the line is what makes the message
// actionable.
type sqlToken struct {
	text string
	// word is true for identifiers and keywords, false for punctuation.
	word   bool
	offset int
}

// sqlTableReference is a table name found in an SQL body.
type sqlTableReference struct {
	table string
	// clause is the keyword that introduced the name (from, join, into,
	// update); the failure message prints it because "which FROM" is the first
	// question the reader asks.
	clause string
	offset int
}

// crossModuleRead is one table reference that reaches into another module.
type crossModuleRead struct {
	sqlTableReference
	owner string
}

// blankSQLNoise replaces SQL comments and quoted string literals with spaces,
// keeping the body's LENGTH and its newlines.
//
// # Why not [stripSQLComments]
//
// That helper already exists and is what the foreign key audit uses, but it
// collapses a comment to a single space. Length is exactly what this audit
// cannot lose: every offset it reports would shift by the size of every comment
// above it, and the line numbers in the findings would be wrong — which is
// worse than having no line number at all, because a wrong one is believed.
//
// # Why the quoted literals go too
//
// A table name inside a string is DATA, not a reference. The order module's
// migrations carry SQL examples in prose and the seed migrations carry country
// names; without this, 'FROM inventory_levels' written inside quotes would be
// reported as a read.
//
// The comments are blanked BEFORE the quotes, which is the same order the
// foreign key audit uses. It gets one case wrong — a two-dash sequence inside a
// quoted string ends the scan of that line early — and the cost of being wrong
// is that the rest of the line is not read. That direction is the safe one.
func blankSQLNoise(raw string) string {
	out := []byte(raw)
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}

	for _, span := range sqlBlockComment.FindAllStringIndex(raw, -1) {
		blank(span[0], span[1])
	}
	for _, span := range sqlLineComment.FindAllStringIndex(string(out), -1) {
		blank(span[0], span[1])
	}

	inString := false
	for i := range out {
		if out[i] == '\'' {
			inString = !inString
			out[i] = ' '

			continue
		}
		if inString && out[i] != '\n' {
			out[i] = ' '
		}
	}

	return string(out)
}

// The comment shapes blanked before an SQL body is read.
var (
	sqlLineComment  = regexp.MustCompile(`--[^\n]*`)
	sqlBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// tokenizeSQL splits a blanked SQL body into words and punctuation.
//
// A dollar sign counts as a word character so that a parameter placeholder
// arrives as ONE token: "$9" has to be a single word, otherwise the "$" would
// be punctuation and "9" would look like a bare name after a FROM. That is not
// hypothetical — promotion's campaign query ends with "IS NOT DISTINCT FROM $9"
// and was the first thing the scanner tripped over.
func tokenizeSQL(body string) []sqlToken {
	var tokens []sqlToken
	for i := 0; i < len(body); {
		char := body[i]
		switch {
		case char == ' ' || char == '\t' || char == '\n' || char == '\r':
			i++
		case char == '"':
			end := i + 1
			for end < len(body) && body[end] != '"' {
				end++
			}
			tokens = append(tokens, sqlToken{text: strings.ToLower(body[i+1 : min(end, len(body))]), word: true, offset: i})
			i = end + 1
		case isSQLWordByte(char):
			end := i
			for end < len(body) && (isSQLWordByte(body[end]) || body[end] >= '0' && body[end] <= '9') {
				end++
			}
			tokens = append(tokens, sqlToken{text: strings.ToLower(body[i:end]), word: true, offset: i})
			i = end
		default:
			tokens = append(tokens, sqlToken{text: string(char), offset: i})
			i++
		}
	}

	return tokens
}

// isSQLWordByte reports whether the byte may start or continue a bare SQL name.
func isSQLWordByte(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char == '_' || char == '$'
}

// qualifiedSQLName reads name, schema.name or catalog.schema.name starting at
// the given token and returns the LAST part with the index after it.
//
// The last part is the table; a schema qualification names WHERE the table
// lives, not a different table. Postgres resolves "public.orders" and "orders"
// to the same row set, so treating them as two names would let a module reach
// across simply by writing the schema out.
func qualifiedSQLName(tokens []sqlToken, at int) (name string, next int) {
	if at >= len(tokens) || !tokens[at].word {
		return "", at
	}

	name = tokens[at].text
	at++
	for at+1 < len(tokens) && !tokens[at].word && tokens[at].text == "." && tokens[at+1].word {
		name = tokens[at+1].text
		at += 2
	}

	return name, at
}

// sqlFromHidingCalls are the functions whose argument list contains the word
// FROM without a table anywhere near it.
//
// EXTRACT(EPOCH FROM created_at) is the one that actually bites in this
// repository's date arithmetic; the other four take FROM in the same
// positional-argument style and are listed with it so the next one added is
// added in one place. The whole call is skipped, parenthesis-matched, rather
// than the single FROM: SUBSTRING(x FROM 1 FOR 2) has two of them.
var sqlFromHidingCalls = map[string]bool{
	"extract":   true,
	"substring": true,
	"trim":      true,
	"overlay":   true,
	"position":  true,
}

// sqlTablePrefixWords are words that may sit between the clause keyword and the
// table name.
//
// ONLY is Postgres inheritance syntax, LATERAL introduces a correlated
// subquery, and ROWS is the head of "FROM ROWS FROM (unnest(...))" — the shape
// cart's line item totals update is written in, because sqlc cannot parse the
// multi-argument unnest.
var sqlTablePrefixWords = map[string]bool{
	"only":    true,
	"lateral": true,
	"rows":    true,
}

// sqlNonTableWords are the words that follow a clause keyword without ever
// being a table.
//
// SET closes "ON CONFLICT DO UPDATE SET", SELECT and VALUES open a subquery
// that the parenthesis test does not catch when the parenthesis was consumed
// elsewhere, and DISTINCT is the tail of "IS NOT DISTINCT FROM".
var sqlNonTableWords = map[string]bool{
	"set":      true,
	"select":   true,
	"values":   true,
	"distinct": true,
}

// tablesCreatedIn returns the tables a migration body CREATES.
//
// Ownership is read from this and from nothing else. A hand-written map of
// "which module owns which table" would be the one thing that cannot be trusted
// here: it would be edited when someone remembers, and a table missing from it
// silently becomes ownerless — that is, free for every module to read.
func tablesCreatedIn(raw string) []string {
	tokens := tokenizeSQL(blankSQLNoise(raw))

	var created []string
	for i := 0; i+1 < len(tokens); i++ {
		if !tokens[i].word || tokens[i].text != "create" {
			continue
		}

		at := i + 1
		for at < len(tokens) && tokens[at].word && sqlCreateModifiers[tokens[at].text] {
			at++
		}
		if at >= len(tokens) || !tokens[at].word || tokens[at].text != "table" {
			continue
		}

		at++
		if at+2 < len(tokens) && tokens[at].text == "if" && tokens[at+1].text == "not" && tokens[at+2].text == "exists" {
			at += 3
		}
		if name, _ := qualifiedSQLName(tokens, at); name != "" {
			created = append(created, name)
		}
	}

	return created
}

// sqlCreateModifiers are the words CREATE may carry before TABLE.
//
// None of them appears in this repository's migrations today. They are listed
// so that the day one does, the ownership read does not silently skip the
// statement and hand the table to nobody — which would not fail, it would
// legalize every module's access to it.
var sqlCreateModifiers = map[string]bool{
	"unlogged":  true,
	"temp":      true,
	"temporary": true,
	"global":    true,
	"local":     true,
}

// tablesNamedIn returns the tables an SQL body reads or writes.
//
// The four clause keywords are FROM, JOIN, INSERT INTO and UPDATE; DELETE FROM
// arrives through FROM. Everything the walk refuses is refused for a reason
// that was measured on this repository's own SQL, and each refusal is written
// on the map or the branch that performs it.
func tablesNamedIn(raw string) []sqlTableReference {
	tokens := tokenizeSQL(blankSQLNoise(raw))

	// A WITH name is not a table. The shape "<name> AS (" also matches a window
	// definition, which is equally not a table, so collecting both is right.
	// Collecting them ALL BEFORE the walk matters: a CTE may be referenced in
	// the query before its own definition in a recursive term.
	commonTableNames := map[string]bool{}
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i].word && tokens[i+1].word && tokens[i+1].text == "as" && !tokens[i+2].word && tokens[i+2].text == "(" {
			commonTableNames[tokens[i].text] = true
		}
	}

	var named []sqlTableReference
	for i := 0; i < len(tokens); i++ {
		if !tokens[i].word {
			continue
		}

		keyword := tokens[i].text
		if sqlFromHidingCalls[keyword] && i+1 < len(tokens) && tokens[i+1].text == "(" {
			i = endOfSQLCall(tokens, i+1)

			continue
		}

		at, isClause := tableNameStart(tokens, i)
		if !isClause {
			continue
		}
		for at < len(tokens) && tokens[at].word && sqlTablePrefixWords[tokens[at].text] {
			at++
		}
		if at >= len(tokens) || !tokens[at].word {
			continue
		}

		name, after := qualifiedSQLName(tokens, at)
		// After FROM and JOIN a name followed by "(" is a CALL — unnest() and
		// the other set returning functions; "FROM (VALUES ...)" never gets
		// this far because the parenthesis is not a word.
		//
		// The test is asked ONLY of those two keywords, and the reason is a
		// finding this file's own control produced before the gate ever ran:
		// asked of every keyword it swallows "INSERT INTO price (id, amount)",
		// where the parenthesis opens the COLUMN LIST. Every insert in the
		// repository is written that way, so the rule would have gone blind to
		// cross-module WRITES entirely — the loudest half of the violation —
		// while still looking like it worked.
		if (keyword == "from" || keyword == "join") &&
			after < len(tokens) && !tokens[after].word && tokens[after].text == "(" {
			continue
		}
		if name == "" || commonTableNames[name] || sqlNonTableWords[name] {
			continue
		}

		named = append(named, sqlTableReference{table: name, clause: keyword, offset: tokens[at].offset})
	}

	return named
}

// tableNameStart reports whether the token at the given index is a clause
// keyword that a table name follows, and where that name starts.
func tableNameStart(tokens []sqlToken, i int) (int, bool) {
	previous := ""
	if i > 0 && tokens[i-1].word {
		previous = tokens[i-1].text
	}

	switch tokens[i].text {
	case "from":
		// "IS NOT DISTINCT FROM $1" is a comparison, not a source.
		return i + 1, previous != "distinct"
	case "join":
		return i + 1, true
	case "into":
		return i + 1, previous == "insert"
	case "update":
		// "SELECT ... FOR UPDATE" takes a lock and "ON CONFLICT DO UPDATE"
		// opens an upsert; neither names a table.
		return i + 1, previous != "for" && previous != "do"
	default:
		return i, false
	}
}

// endOfSQLCall returns the index of the parenthesis closing the one at the
// given index, or the last token when the parentheses do not balance.
func endOfSQLCall(tokens []sqlToken, open int) int {
	depth := 0
	for i := open; i < len(tokens); i++ {
		if tokens[i].word {
			continue
		}
		switch tokens[i].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return len(tokens) - 1
}

// crossModuleReads returns the tables the given SQL names that another module
// owns.
//
// This is the RULE, and it is one function so that the audit and its positive
// control cannot drift apart: a control that reimplements the decision proves
// only that two copies agree.
func crossModuleReads(module, raw string, owners map[string]string) []crossModuleRead {
	var found []crossModuleRead
	for _, reference := range tablesNamedIn(raw) {
		owner, owned := owners[reference.table]
		if !owned || owner == module {
			continue
		}
		found = append(found, crossModuleRead{sqlTableReference: reference, owner: owner})
	}

	return found
}

// lineOfOffset returns the 1-based line the byte offset falls on.
func lineOfOffset(body string, offset int) int {
	if offset > len(body) {
		offset = len(body)
	}

	return 1 + strings.Count(body[:offset], "\n")
}

// goStringHoldsSQL recognizes a Go string constant that carries an SQL
// statement.
//
// It wants a keyword PAIR and it wants uppercase. Both narrowings were measured
// against the module trees before they were written down; the reasoning is in
// this file's header, under what the extractor does not see.
var goStringHoldsSQL = regexp.MustCompile(
	`(?s)\bSELECT\b.*\bFROM\b|\bINSERT\s+INTO\b|\bUPDATE\b.*\bSET\b|\bDELETE\s+FROM\b|\bJOIN\b.*\bON\b`)

// goSQLConstant is one SQL-carrying string expression found in Go source.
type goSQLConstant struct {
	path string
	line int
	sql  string
}

// moduleGoSQL returns the SQL-carrying string expressions of a module's
// PRODUCTION Go source.
//
// Generated sqlc files are read along with the hand-written ones. That is
// deliberate duplication of the query directory's contents, and it is worth its
// cost twice over: it proves the generated code still matches the source it
// came from, and it means a violation is caught even if someone edits the
// generated file directly.
func moduleGoSQL(t *testing.T, moduleRoot string) (found []goSQLConstant, deepestFold int) {
	t.Helper()

	fold := func(expr ast.Expr, constants map[string]ast.Expr) string {
		text, reached := foldStringExpr(expr, constants, 0)
		deepestFold = max(deepestFold, reached)

		return text
	}

	for _, dir := range slices.Sorted(maps.Keys(productionPackages(t, moduleRoot))) {
		fset := token.NewFileSet()
		files := parseDir(t, fset, dir, false)

		constants := map[string]ast.Expr{}
		for _, file := range files {
			collectStringConstants(file.tree, constants)
		}

		for _, file := range files {
			ast.Inspect(file.tree, func(node ast.Node) bool {
				expr, isConcatenation := node.(*ast.BinaryExpr)
				switch {
				case isConcatenation && expr.Op == token.ADD:
					// The folded whole is the truth; descending would report
					// the halves a second time.
					found = appendGoSQL(found, file.path, fset.Position(expr.Pos()).Line, fold(expr, constants))

					return false
				case isConcatenation:
					return true
				}
				if literal, isLiteral := node.(*ast.BasicLit); isLiteral && literal.Kind == token.STRING {
					found = appendGoSQL(found, file.path, fset.Position(literal.Pos()).Line, fold(literal, constants))
				}

				return true
			})
		}
	}

	return found, deepestFold
}

// appendGoSQL adds the text to the list when it carries SQL.
func appendGoSQL(found []goSQLConstant, path string, line int, text string) []goSQLConstant {
	if !goStringHoldsSQL.MatchString(text) {
		return found
	}

	return append(found, goSQLConstant{path: path, line: line, sql: text})
}

// collectStringConstants indexes the package level string constants of a file
// by name.
func collectStringConstants(tree *ast.File, into map[string]ast.Expr) {
	for _, decl := range tree.Decls {
		group, isGroup := decl.(*ast.GenDecl)
		if !isGroup || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value, isValue := spec.(*ast.ValueSpec)
			if !isValue {
				continue
			}
			for i, name := range value.Names {
				if i < len(value.Values) {
					into[name.Name] = value.Values[i]
				}
			}
		}
	}
}

// maxSQLFoldDepth is the largest number of steps taken while folding a string
// expression.
//
// A constant declared in terms of itself is a compile error, so a cycle cannot
// reach here from valid source; the limit exists so that a malformed tree
// cannot spin.
//
// The number was 12 when this file was first written, chosen by guessing that a
// concatenation chain is short. It is not: an expression is folded one OPERAND
// at a time, so `a + b + c + d` already costs four steps before a single
// constant is followed, and the module trees hold a chain that reaches 23. The
// guess was therefore TRUNCATING real expressions, and the way it did so is the
// point — the fold returned [unresolvedGoOperand] for the tail and the audit
// went on reading a query with its end cut off, silently. 64 leaves room for
// the chains to grow, and [TestModuleSQLNamesOnlyItsOwnTables] fails rather
// than truncates if they ever grow past it.
const maxSQLFoldDepth = 64

// foldStringExpr evaluates a Go string expression as far as constants allow and
// reports the deepest recursion level it reached.
//
// Anything it cannot resolve — a variable, a function call, a constant from
// another package — becomes [unresolvedGoOperand] rather than disappearing.
//
// The depth is returned rather than kept in a package variable because the
// audits in this package run in parallel; a shared counter would be a data race
// and, worse, would produce a number that depends on the scheduler.
func foldStringExpr(expr ast.Expr, constants map[string]ast.Expr, depth int) (text string, deepest int) {
	if depth > maxSQLFoldDepth {
		return unresolvedGoOperand, depth
	}

	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return unresolvedGoOperand, depth
		}
		unquoted, err := strconv.Unquote(value.Value)
		if err != nil {
			return unresolvedGoOperand, depth
		}

		return unquoted, depth
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return unresolvedGoOperand, depth
		}
		left, leftDepth := foldStringExpr(value.X, constants, depth+1)
		right, rightDepth := foldStringExpr(value.Y, constants, depth+1)

		return left + right, max(leftDepth, rightDepth)
	case *ast.Ident:
		if declared, known := constants[value.Name]; known {
			return foldStringExpr(declared, constants, depth+1)
		}

		return unresolvedGoOperand, depth
	case *ast.ParenExpr:
		return foldStringExpr(value.X, constants, depth+1)
	default:
		return unresolvedGoOperand, depth
	}
}

// tableOwnersByModule maps every table to the module whose migrations create
// it.
func tableOwnersByModule(t *testing.T, modules []string) map[string]string {
	t.Helper()

	owners := map[string]string{}
	for _, module := range modules {
		dir := filepath.Join(repoRoot, modulesDir, module, migrationsDirName)
		files, err := filepath.Glob(filepath.Join(dir, "*"+upMigrationSuffix))
		require.NoError(t, err, "%s could not be scanned", dir)

		for _, path := range files {
			raw, readErr := os.ReadFile(path)
			require.NoError(t, readErr, "%s could not be read", path)

			for _, table := range tablesCreatedIn(string(raw)) {
				previous, taken := owners[table]
				assert.False(t, taken && previous != module,
					"%s: table %q is created by module %q as well as by module %q.\n"+
						"Two owners is not a smaller problem than none: the audit below asks "+
						"ONE question — who owns this — and with two answers it will clear a "+
						"read that reaches across, because the reader happens to match the "+
						"owner that was recorded last.", path, table, previous, module)
				owners[table] = module
			}
		}
	}

	return owners
}

// TestModuleSQLNamesOnlyItsOwnTables enforces ADR 0001 in the place it is most
// easily broken and was never checked.
//
// The invariant, the hole it closes and what stays legal are argued in this
// file's header. What follows is the walk.
func TestModuleSQLNamesOnlyItsOwnTables(t *testing.T) {
	t.Parallel()

	modules := moduleNames(t)
	owners := tableOwnersByModule(t, modules)

	// Five counters and one depth mark. Each of the five stands for a separate
	// link of this walk that can break ON ITS OWN, and every one of them breaks
	// SILENTLY: a link that reads nothing reports no violation, which is
	// indistinguishable from a clean tree. A single total would say that
	// something broke without saying which, and the link that goes quiet is
	// always the one nobody suspects.
	var (
		queryFiles     int
		migrationFiles int
		goConstants    int
		ownReadsInSQL  int
		ownReadsInGo   int
		deepestFold    int
	)
	ownReadsByModule := map[string]int{}

	report := func(module, path string, line int, read crossModuleRead) {
		t.Errorf("%s:%d: the %s module's SQL names table %q in a %s clause, and the %s module owns it (ADR 0001).\n"+
			"This query WORKS today — every module is handed the same connection pool — which is "+
			"why no other gate in this repository sees it and why it is worth failing over: the "+
			"coupling stays invisible until %s is moved to its own database or its own service, "+
			"and then this file breaks with a relation that does not exist.\n"+
			"Read the data through the owning module's interop surface resolved from the container, "+
			"or, when the read is a join across modules, through the cross-module read layer in "+
			"core/query.",
			path, line, module, read.table, strings.ToUpper(read.clause), read.owner, read.owner)
	}

	scanSQLFile := func(module, path string, counter *int) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err, "%s could not be read", path)

		body := string(raw)
		*counter++
		for _, reference := range tablesNamedIn(body) {
			if owners[reference.table] == module {
				ownReadsInSQL++
				ownReadsByModule[module]++
			}
		}
		for _, read := range crossModuleReads(module, body, owners) {
			report(module, path, lineOfOffset(body, read.offset), read)
		}
	}

	for _, module := range modules {
		moduleRoot := filepath.Join(repoRoot, modulesDir, module)

		for _, path := range sqlFilesIn(t, filepath.Join(moduleRoot, queriesDirName)) {
			scanSQLFile(module, path, &queryFiles)
		}
		for _, path := range sqlFilesIn(t, filepath.Join(moduleRoot, migrationsDirName)) {
			scanSQLFile(module, path, &migrationFiles)
		}

		constants, moduleFoldDepth := moduleGoSQL(t, moduleRoot)
		deepestFold = max(deepestFold, moduleFoldDepth)
		for _, constant := range constants {
			goConstants++
			for _, reference := range tablesNamedIn(constant.sql) {
				if owners[reference.table] == module {
					ownReadsInGo++
					ownReadsByModule[module]++
				}
			}
			for _, read := range crossModuleReads(module, constant.sql, owners) {
				report(module, constant.path, constant.line, read)
			}
		}
	}

	require.Positive(t, len(owners),
		"NO table was found in any module's migrations; the ownership read has gone BLIND.\n"+
			"This does not make the audit silent, it makes it USELESS in the quiet direction: "+
			"with an empty ownership map every table belongs to nobody, every cross-module read "+
			"is legal, and the walk below reports a clean tree it never actually checked.")
	require.Positive(t, migrationFiles,
		"no migration file was read; the migrations may have moved out of %q or lost the .sql "+
			"extension. A walk that finds no file approves everything in it.", migrationsDirName)
	require.Positive(t, queryFiles,
		"no query file was read; the sqlc source may have moved out of %q.\n"+
			"This is the surface where a cross-module SELECT is EASIEST to write, because the "+
			"file contains nothing but SQL and no Go compiler ever looks at it.", queriesDirName)
	require.Positive(t, goConstants,
		"no SQL-carrying string constant was found in any module's Go source, though the product "+
			"module alone holds hundreds of lines of hand-written SQL.\n"+
			"Either goStringHoldsSQL no longer recognizes the way SQL is written here, or the "+
			"constant folding stopped resolving; both leave the hand-written half of the rule "+
			"unchecked while the audit keeps passing.")

	// The two surfaces are counted apart on purpose. A single total stays
	// comfortably positive while ONE of them reads nothing at all, and the one
	// that goes quiet is the one nobody notices.
	require.Positive(t, ownReadsInSQL,
		"the SQL files were read but not ONE of their table names matched the ownership map.\n"+
			"The two halves of this audit have stopped meeting: either the name extractor is "+
			"producing something other than table names, or the ownership map is keyed "+
			"differently. Every finding above, if there are any, is suspect for the same reason.")
	require.Positive(t, ownReadsInGo,
		"the Go string constants were read but not ONE of their table names matched the "+
			"ownership map; the hand-written half of the rule is meeting nothing.")
	require.LessOrEqual(t, deepestFold, maxSQLFoldDepth,
		"the constant folding hit its depth limit of %d.\n"+
			"That is not a crash and it does not show up as a finding: the expression that "+
			"ran deep was TRUNCATED, the audit read a query with its tail cut off, and "+
			"whatever the tail named went unchecked. Find the chain that got this deep and "+
			"raise maxSQLFoldDepth past it — the limit is there to stop a malformed tree "+
			"spinning, not to cap how a module writes its SQL.", maxSQLFoldDepth)

	for _, module := range modules {
		if !ownsAnyTable(module, owners) {
			continue
		}
		assert.Positive(t, ownReadsByModule[module],
			"module %q owns tables but NONE of its own SQL was seen naming one of them.\n"+
				"Either its SQL has moved somewhere this walk does not look — and the module is "+
				"now exempt from the rule without anybody deciding that — or it really does not "+
				"touch the tables its migrations create, which is a finding of its own.", module)
	}
}

// ownsAnyTable reports whether any table is owned by the given module.
//
// A module that owns nothing is skipped by the per-module check rather than
// failing it: a module whose whole storage is somebody else's is not a shape
// this repository has today, but demanding that every module read a table of
// its own would fail the day one legitimately does not.
func ownsAnyTable(module string, owners map[string]string) bool {
	for _, owner := range owners {
		if owner == module {
			return true
		}
	}

	return false
}

// sqlFilesIn returns the .sql files directly inside a directory, and an empty
// list when the directory does not exist.
//
// A missing directory is not fatal HERE because the caller counts what it read
// and fails on a total of zero; failing per module would turn a module that
// legitimately has no queries yet into a broken build.
func sqlFilesIn(t *testing.T, dir string) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	require.NoError(t, err, "%s could not be scanned", dir)
	slices.Sort(files)

	return files
}

// sqlExtractionCase is one planted SQL body and the table names the extractor
// has to see in it.
type sqlExtractionCase struct {
	name string
	sql  string
	// created is the exact set of tables the body CLAIMS ownership of.
	created []string
	// named is the exact set of tables the body reads or writes.
	named []string
}

// sqlExtractionCases are the forms the extractor must keep seeing and the forms
// it must keep refusing.
//
// Every one of them is a shape that occurs in this repository's own SQL or a
// trap that was hit while the extractor was being written. They are in-memory
// fixtures rather than files under testdata so that the input and the
// expectation sit on the same screen: a fixture whose expected value lives in
// another file is a fixture people edit without reading.
var sqlExtractionCases = []sqlExtractionCase{
	{
		name:  "a plain read",
		sql:   "SELECT quantity FROM inventory_levels WHERE item_id = $1;",
		named: []string{"inventory_levels"},
	},
	{
		name:  "a join with aliases",
		sql:   "SELECT li.id FROM orders o JOIN order_line_items AS li ON li.order_id = o.id;",
		named: []string{"orders", "order_line_items"},
	},
	{
		name:  "an insert",
		sql:   "INSERT INTO payments (id, amount) VALUES ($1, $2);",
		named: []string{"payments"},
	},
	{
		name:  "an update",
		sql:   "UPDATE carts SET total = 0 WHERE id = $1;",
		named: []string{"carts"},
	},
	{
		name:  "a delete",
		sql:   "DELETE FROM price WHERE price_set_id = $1;",
		named: []string{"price"},
	},
	{
		name:  "a quoted and schema qualified name",
		sql:   `SELECT 1 FROM public."product_variant" WHERE id = $1;`,
		named: []string{"product_variant"},
	},
	{
		name: "the EXTRACT trap",
		// The FROM inside EXTRACT introduces a column, not a table. Reported as
		// a table it would name created_at — which no module owns today, so the
		// bug would have stayed invisible until a column shared a name with
		// somebody's table.
		sql:   "SELECT EXTRACT(EPOCH FROM created_at) FROM orders;",
		named: []string{"orders"},
	},
	{
		name:  "the IS NOT DISTINCT FROM trap",
		sql:   "SELECT 1 FROM campaign WHERE budget_currency_code IS NOT DISTINCT FROM $9;",
		named: []string{"campaign"},
	},
	{
		name:  "a common table expression is not a table",
		sql:   "WITH recent AS (SELECT id FROM orders) SELECT id FROM recent;",
		named: []string{"orders"},
	},
	{
		name:  "a values list is not a table",
		sql:   "SELECT v.x FROM (VALUES (1), (2)) AS v (x);",
		named: nil,
	},
	{
		name:  "a set returning function is not a table",
		sql:   "SELECT r FROM unnest($1::text[]) AS r;",
		named: nil,
	},
	{
		name:  "ROWS FROM keeps the update target",
		sql:   "UPDATE cart_line_items AS li SET total = v.total FROM ROWS FROM (unnest($1::bigint[])) AS v (total) WHERE li.id = $2;",
		named: []string{"cart_line_items"},
	},
	{
		name:  "an upsert does not name a table after DO UPDATE",
		sql:   "INSERT INTO price (id) VALUES ($1) ON CONFLICT (id) DO UPDATE SET amount = 1;",
		named: []string{"price"},
	},
	{
		name:  "a row lock does not name a table after FOR UPDATE",
		sql:   "SELECT id FROM orders WHERE id = $1 FOR UPDATE;",
		named: []string{"orders"},
	},
	{
		name:  "a table named in a comment is not a read",
		sql:   "-- the quantity comes from inventory_levels via the interop surface\nSELECT id FROM product;",
		named: []string{"product"},
	},
	{
		name:  "a table named inside a string is not a read",
		sql:   "SELECT id FROM product WHERE note = 'copied FROM inventory_levels';",
		named: []string{"product"},
	},
	{
		name: "a link table is read like any other unowned table",
		// The extractor SEES it; the rule then clears it because no migration
		// creates it. If this case ever expects nothing, the sales channel
		// filter has gone invisible and the gate would no longer notice the day
		// somebody points it at auth's own table instead.
		sql:   "SELECT scl.to_id FROM link_product_sales_channel scl WHERE scl.from_id = $1;",
		named: []string{"link_product_sales_channel"},
	},
	{
		name:    "a create claims ownership",
		sql:     "CREATE TABLE IF NOT EXISTS inventory_levels (id TEXT PRIMARY KEY);",
		created: []string{"inventory_levels"},
	},
	{
		name:    "a quoted and schema qualified create",
		sql:     `CREATE TABLE public."stock_locations" (id TEXT PRIMARY KEY);`,
		created: []string{"stock_locations"},
	},
	{
		name:    "a create inside a string claims nothing",
		sql:     "INSERT INTO product (note) VALUES ('CREATE TABLE ghost (id TEXT)');",
		created: nil,
		named:   []string{"product"},
	},
	{
		name:    "a create inside a comment claims nothing",
		sql:     "-- CREATE TABLE ghost (id TEXT)\nSELECT id FROM product;",
		created: nil,
		named:   []string{"product"},
	},
	{
		name:    "an index does not claim its table",
		sql:     "CREATE INDEX idx_product_handle ON product (handle);",
		created: nil,
	},
}

// TestTheSQLTableScannerIsNotBlind pins the floor under the extractor.
//
// [TestModuleSQLNamesOnlyItsOwnTables] passes on today's tree because today's
// tree is clean, and a check that only ever sees clean input cannot tell "there
// is nothing wrong" from "I am not looking". The refusals matter as much as the
// matches: the extractor under-matches on purpose, and without these cases the
// under-matching has nothing stopping it from growing into not matching at all.
func TestTheSQLTableScannerIsNotBlind(t *testing.T) {
	t.Parallel()

	for _, planted := range sqlExtractionCases {
		t.Run(planted.name, func(t *testing.T) {
			t.Parallel()

			var named []string
			for _, reference := range tablesNamedIn(planted.sql) {
				named = append(named, reference.table)
				assert.NotEmpty(t, reference.clause,
					"the reference to %q carries no clause keyword; the failure message would "+
						"tell the reader a table is named without saying where", reference.table)
			}

			assert.ElementsMatch(t, planted.named, named,
				"the tables read out of %q are not the ones the case expects.\n"+
					"A name that went MISSING is a hole in the rule; a name that appeared out "+
					"of nowhere is a false accusation, and a gate that accuses gets deleted.",
				planted.sql)
			assert.ElementsMatch(t, planted.created, tablesCreatedIn(planted.sql),
				"the tables created by %q are not the ones the case expects; ownership is read "+
					"from this and nothing else, and a table nobody owns is a table every "+
					"module may read.", planted.sql)
		})
	}
}

// goSQLClassificationCases are the string constants the Go side must classify
// as SQL, and the sentences it must not.
//
// The first refusal is a VERBATIM error message from the product module. The
// classifier once accepted it, and the consequence is worth stating plainly:
// the same sentence written in any other module would have been reported as
// that module reading product's table, and the finding would have been a lie.
var goSQLClassificationCases = map[string]bool{
	"SELECT id FROM product WHERE deleted_at IS NULL":             true,
	"INSERT INTO order_line_items (id) VALUES ($1)":               true,
	"UPDATE inventory_levels SET stocked_quantity = $1":           true,
	"DELETE FROM cart_line_items WHERE cart_id = $1":              true,
	"  JOIN product_variant v ON v.product_id = product.id":       true,
	"could not update product: %s":                                false,
	"the quantity is read from inventory_levels through interop":  false,
	"ORDER BY created_at DESC LIMIT $1":                           false,
	"THE CALLER MUST UPDATE THE ORDERS IT OWNS BEFORE IT COMMITS": false,
}

// TestTheModuleSQLRuleCatchesAViolation is the positive control on the
// DECISION, not on the reading.
//
// The extractor can be perfect and the rule still toothless: an ownership map
// that never matches, a comparison written the wrong way round, a module name
// compared against a path. This plants an ownership map and three bodies
// against it and requires exactly one of them to be reported.
func TestTheModuleSQLRuleCatchesAViolation(t *testing.T) {
	t.Parallel()

	owners := map[string]string{
		"inventory_levels": "inventory",
		"stock_locations":  "inventory",
		"product":          "product",
	}

	reads := crossModuleReads("product", "SELECT quantity FROM inventory_levels WHERE item_id = $1;", owners)
	require.Len(t, reads, 1,
		"the planted cross-module read was NOT reported. The rule is not biting, and "+
			"TestModuleSQLNamesOnlyItsOwnTables is green because it cannot fail.")
	assert.Equal(t, "inventory_levels", reads[0].table)
	assert.Equal(t, "inventory", reads[0].owner,
		"the finding does not name the owning module; without it the message cannot say "+
			"whose interop surface to go through instead")
	assert.Equal(t, "from", reads[0].clause)

	assert.Empty(t, crossModuleReads("product", "SELECT id FROM product WHERE handle = $1;", owners),
		"a module reading its OWN table was reported; the rule has become 'no SQL at all' "+
			"and every module in the repository is a violation")
	assert.Empty(t, crossModuleReads("product", "SELECT to_id FROM link_product_sales_channel WHERE from_id = $1;", owners),
		"reading a LINK table was reported as a violation. It is not one: core/link creates "+
			"those tables at run time (ADR 0005) so no migration owns them, and the argued "+
			"precedent is in internal/modules/product/repository/saleschannel.go. Banning it "+
			"deletes the sales channel filter from the storefront's product listing.")
	assert.Empty(t, crossModuleReads("product", "INSERT INTO event_outbox (topic) VALUES ($1);", owners),
		"writing the core's outbox was reported as a violation; a module publishing an event "+
			"inside its own transaction is the documented mechanism, not a boundary crossing")

	for text, isSQL := range goSQLClassificationCases {
		assert.Equal(t, isSQL, goStringHoldsSQL.MatchString(text),
			"the SQL classifier disagrees about %q.\n"+
				"Accepting a sentence turns an English error message into a false accusation; "+
				"refusing a statement leaves the hand-written SQL of a module unread.", text)
	}

	folded, reached := foldStringExpr(concatenationFixture(t), map[string]ast.Expr{}, 0)
	assert.Contains(t, folded, unresolvedGoOperand,
		"a concatenation with an unresolvable operand folded without leaving a placeholder.\n"+
			"Dropping the operand glues the neighbors together, and the keyword that lands "+
			"where the table name was gets reported as a table.")
	assert.Positive(t, reached,
		"folding a two-operand concatenation reported a depth of zero.\n"+
			"That number is what the audit uses to notice it TRUNCATED an expression; "+
			"reported as zero it can never exceed the limit, and the truncation goes back "+
			"to being silent.")
}

// concatenationFixture parses a planted string concatenation whose middle
// operand cannot be folded.
func concatenationFixture(t *testing.T) ast.Expr {
	t.Helper()

	source := "package p\n\nvar q = \"SELECT x FROM \" + table + \" WHERE y = $1\"\n"
	tree, err := parser.ParseFile(token.NewFileSet(), "planted.go", source, parser.SkipObjectResolution)
	require.NoError(t, err, "the planted concatenation could not be parsed")

	value, ok := tree.Decls[0].(*ast.GenDecl).Specs[0].(*ast.ValueSpec)
	require.True(t, ok, "the planted fixture no longer declares a value")

	return value.Values[0]
}
