package app

import (
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/core/config"
)

// personColumnNames are the column names that unambiguously hold a natural
// person, wherever they appear.
//
// # Why a name list and not a type or a comment
//
// There is no way to tell from a schema alone that a column holds a person, and
// ADR 0029 says gobit must not guess: whether a free-form column holds personal
// data in a given deployment is the controller's judgement, not the framework's.
// What CAN be decided mechanically is the opposite and narrower question — a
// column literally called `email` or `postal_code` holds a person, and a module
// that has one and declares nothing has an incomplete declaration.
//
// The list is therefore deliberately SHORT and made only of names whose meaning
// is not in doubt. It is a floor, not a definition: it cannot find a person in
// `metadata`, in `description` or in a column somebody named `contact_line_2`,
// and it is not supposed to. Its whole job is to make sure the obvious cases
// cannot be forgotten, because those are the ones a reader assumes were checked.
//
// Adding a name here is cheap and safe; every module that has the column must
// then declare it or say why not. Removing one needs an argument.
var personColumnNames = []string{
	"address_1",
	"address_2",
	"author_name",
	"buyer_address",
	"buyer_email",
	"buyer_name",
	"buyer_tax_number",
	"buyer_tax_office",
	"email",
	"first_name",
	"last_name",
	"password_hash",
	"phone",
	"postal_code",
	"provider_identity",
	"seller_address",
	"seller_email",
	"seller_name",
	"seller_tax_number",
	"seller_tax_office",
}

// undeclaredPersonColumns are the person-named columns a module deliberately
// does not declare, with the reason.
//
// The map is EMPTY and that is the intended state rather than an accident: every
// module that holds one of the names above declares it today. An entry here is
// a promise that somebody looked, so the reason has to say what was found — and
// "this has not been looked at yet" is an acceptable reason, while an invented
// intention is not.
var undeclaredPersonColumns = map[string]string{}

// TestEveryPersonColumnIsDeclared is the audit ADR 0029 said this repository did
// not have.
//
// # What it protects
//
// The erasure mechanism's third obligation is a declaration of what each module
// holds, and ADR 0029 recorded its own weakness in the same breath: "the
// declaration is only as true as the modules keep it. A module that grows a
// personal column and does not declare it makes the contract lie." That is not a
// hypothetical failure mode, it is the ordinary one — the column is added for a
// feature, the declaration lives in a different file, and nothing connects them.
//
// The damage is specific and it is not a build error. An embedder publishes its
// privacy notice from the declaration and answers data subjects from the erasure
// report; a column missing from the declaration is a place the controller has
// told a person nobody is looking, in writing.
//
// # Both directions, because each catches a different mistake
//
// A declared column that does not exist is drift — a rename or a DROP COLUMN
// that left the declaration behind, so the report names a place that is not
// there. An existing column that is not declared is the omission above. Neither
// direction implies the other and a gate with only one of them reads as though
// it had both.
func TestEveryPersonColumnIsDeclared(t *testing.T) {
	t.Parallel()

	mods := registeredModules(t)
	if len(mods) == 0 {
		t.Fatal("no module was registered; the audit has gone blind")
	}

	scannedColumns, declaringModules := 0, 0
	used := map[string]bool{}

	for _, mod := range mods {
		schema := schemaOfModule(t, mod)
		scannedColumns += countColumns(schema)

		declared := declaredHoldings(mod)
		if len(declared) > 0 {
			declaringModules++
		}

		// Direction one: what is declared has to exist.
		for key := range declared {
			table, column, _ := strings.Cut(key, ".")
			if schema[table] == nil {
				t.Errorf("the %s module declares %s but its migrations create no table called %q.\n"+
					"The declaration names a place that does not exist, so the report tells a "+
					"controller to look somewhere there is nothing to find. Either the table was "+
					"renamed and the declaration was left behind, or the declaration was written "+
					"from memory.", mod.Name(), key, table)

				continue
			}

			if !schema[table][column] {
				t.Errorf("the %s module declares %s and the %q table has no such column.\n"+
					"A declared column that is not in the schema is drift — usually a rename or a "+
					"DROP COLUMN that did not reach the declaration.", mod.Name(), key, table)
			}
		}

		// Direction two: an obvious person column has to be declared.
		for table, columns := range schema {
			for column := range columns {
				if !isPersonColumnName(column) {
					continue
				}

				key := table + "." + column
				if declared[key] {
					continue
				}

				exemptKey := mod.Name() + "." + key
				if reason, exempt := undeclaredPersonColumns[exemptKey]; exempt {
					used[exemptKey] = true

					if reason == "" {
						t.Errorf("%s is exempt with no reason", exemptKey)
					}

					continue
				}

				t.Errorf("%s holds %s and does not declare it.\n"+
					"The column's name says it holds a natural person, and the module's "+
					"PersonalData does not mention it — so an embedder publishing its privacy "+
					"notice from the declaration, and a controller answering a data subject from "+
					"an erasure report, are both told nothing is kept here.\n"+
					"Declare it, or record it in undeclaredPersonColumns WITH ITS REASON; if you "+
					"have not looked yet, say THAT rather than inventing an intention.",
					mod.Name(), key)
			}
		}
	}

	if scannedColumns == 0 {
		t.Fatal("no column was read out of any module's migrations; the schema reader has gone blind.\n" +
			"A reader that finds no column approves every undeclared one.")
	}

	if declaringModules == 0 {
		t.Fatal("no module declares any personal data.\n" +
			"Either the capability is no longer discovered by type assertion, or the modules " +
			"stopped implementing it — and a sweep over such a tree answers a data subject with " +
			"an empty report.")
	}

	for _, key := range sortedKeys(undeclaredPersonColumns) {
		if !used[key] {
			t.Errorf("%s is exempt in undeclaredPersonColumns but is not undeclared any more.\n"+
				"Either it is declared now, or the column is gone. Delete the entry — a dead "+
				"exemption covers up the next real one.", key)
		}
	}
}

// TestEveryDeclaredHoldingIsWellFormed keeps the report readable.
//
// A holding with no table, no column or no reason still counts as a declaration
// and still appears in the answer a controller sends a person; it is worse than
// a missing one, because it looks like somebody wrote it down. The Kind matters
// for the same reason: it is what tells the embedder whether gobit knows what is
// in the column or is only reporting that the column exists.
func TestEveryDeclaredHoldingIsWellFormed(t *testing.T) {
	t.Parallel()

	seen := 0

	for _, mod := range registeredModules(t) {
		declarer, ok := mod.(personaldata.Declarer)
		if !ok {
			continue
		}

		for _, h := range declarer.PersonalData().Holdings {
			seen++

			if h.Table == "" || h.Column == "" {
				t.Errorf("the %s module declares a holding with no table or no column: %+v", mod.Name(), h)
			}

			if h.Kind != personaldata.Named && h.Kind != personaldata.Open {
				t.Errorf("the %s module declares %s.%s with kind %q; it has to be %q or %q",
					mod.Name(), h.Table, h.Column, h.Kind, personaldata.Named, personaldata.Open)
			}

			if strings.TrimSpace(h.Why) == "" {
				t.Errorf("the %s module declares %s.%s with no reason.\n"+
					"The reason is the sentence a controller repeats to the person who asked; "+
					"without it the declaration says a column exists and nothing else.",
					mod.Name(), h.Table, h.Column)
			}
		}
	}

	if seen == 0 {
		t.Fatal("no holding was checked; the audit has gone blind")
	}
}

// TestEveryEraserAlsoDeclares holds the two halves of the contract together.
//
// A module that erases without declaring can answer "anonymized" and leave a
// controller with no way to say WHAT was anonymized, which is the half of
// ADR 0029's contract that makes the other half checkable. The reverse is
// allowed and deliberate — the review module declares and cannot erase, because
// it stores no handle on which person an author is.
func TestEveryEraserAlsoDeclares(t *testing.T) {
	t.Parallel()

	erasers := 0

	for _, mod := range registeredModules(t) {
		if _, ok := mod.(personaldata.Eraser); !ok {
			continue
		}

		erasers++

		if _, ok := mod.(personaldata.Declarer); !ok {
			t.Errorf("the %s module implements personaldata.Eraser and not personaldata.Declarer.\n"+
				"It can say it erased something and cannot say what it held, so nothing can "+
				"check the claim and no controller can describe it.", mod.Name())
		}
	}

	if erasers == 0 {
		t.Fatal("no module implements personaldata.Eraser; the audit has gone blind")
	}
}

// TestTheSchemaReaderIsNotBlind is the positive control for the parser above.
//
// The two audits are only as good as the column reader, and a reader that
// silently understands nothing reports no violation at all — which is
// indistinguishable from a clean tree. This plants each shape the reader claims
// to handle and fails if any of them goes missing.
func TestTheSchemaReaderIsNotBlind(t *testing.T) {
	t.Parallel()

	const sql = `
-- a comment naming email, which must not be read as a column
CREATE TABLE IF NOT EXISTS planted (
    id         TEXT PRIMARY KEY,
    email      TEXT NOT NULL DEFAULT 'a@b.example',
    first_name TEXT,
    CONSTRAINT planted_email_check CHECK (email <> ''),
    UNIQUE (id, email)
);

ALTER TABLE planted ADD COLUMN phone TEXT;
ALTER TABLE planted ADD COLUMN IF NOT EXISTS postal_code TEXT;
ALTER TABLE planted DROP COLUMN first_name;

CREATE TABLE removed (id TEXT PRIMARY KEY, last_name TEXT);
DROP TABLE removed;
`

	schema := readSchema(sql)

	for _, want := range []string{"id", "email", "phone", "postal_code"} {
		if !schema["planted"][want] {
			t.Errorf("the reader missed the %q column; every audit built on it is now blind to that shape", want)
		}
	}

	if schema["planted"]["first_name"] {
		t.Error("the reader kept a column that was dropped; a declaration could name a column that is gone")
	}

	if schema["planted"]["CONSTRAINT"] || schema["planted"]["UNIQUE"] || schema["planted"]["planted_email_check"] {
		t.Errorf("the reader took a table constraint for a column: %v", sortedColumns(schema["planted"]))
	}

	if _, ok := schema["removed"]; ok {
		t.Error("the reader kept a table that was dropped")
	}
}

// registeredModules builds the real module list the composition root builds.
//
// It goes through registerModules rather than through a list written here, and
// that is the point: a second list would be right on the day it was written and
// would miss the next module somebody adds — which is exactly the module whose
// declaration nobody has checked yet.
func registeredModules(t *testing.T) []module.Module {
	t.Helper()

	registry := module.NewRegistry(slog.New(slog.DiscardHandler), nil)
	registerModules(registry, config.Config{}, slog.New(slog.DiscardHandler), nil)

	return registry.Modules()
}

// declaredHoldings indexes a module's declaration by "table.column".
func declaredHoldings(mod module.Module) map[string]bool {
	declarer, ok := mod.(personaldata.Declarer)
	if !ok {
		return nil
	}

	out := map[string]bool{}
	for _, h := range declarer.PersonalData().Holdings {
		out[h.Table+"."+h.Column] = true
	}

	return out
}

// schemaOfModule reads a module's own migrations through the interface.
//
// The files are taken from Migrations() rather than from a path under
// internal/modules, so a module that lives somewhere else — one a plugin brings,
// or one an embedding application wrote — is read the same way as the seventeen
// in this tree.
func schemaOfModule(t *testing.T, mod module.Module) map[string]map[string]bool {
	t.Helper()

	fsys := mod.Migrations()
	if fsys == nil {
		return nil
	}

	var sql strings.Builder

	names := []string{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".up.sql") {
			names = append(names, p)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("the %s module's migrations could not be walked: %v", mod.Name(), err)
	}

	// In migration order: a later file may add or drop what an earlier one
	// created, and reading them out of order would describe a schema no
	// database ever has.
	sort.Slice(names, func(i, j int) bool { return path.Base(names[i]) < path.Base(names[j]) })

	for _, name := range names {
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatalf("the %s module's %s could not be read: %v", mod.Name(), name, err)
		}

		sql.Write(body)
		sql.WriteString(";\n")
	}

	return readSchema(sql.String())
}

// Patterns the schema reader uses.
var (
	lineComment  = regexp.MustCompile(`--[^\n]*`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	stringLit    = regexp.MustCompile(`'[^']*'`)
	createTable  = regexp.MustCompile(`(?is)^\s*create\s+table\s+(?:if\s+not\s+exists\s+)?([a-z0-9_."]+)\s*\((.*)\)\s*$`)
	alterAdd     = regexp.MustCompile(`(?is)^\s*alter\s+table\s+(?:if\s+exists\s+)?([a-z0-9_."]+)\s+add\s+column\s+(?:if\s+not\s+exists\s+)?([a-z0-9_"]+)`)
	alterDrop    = regexp.MustCompile(`(?is)^\s*alter\s+table\s+(?:if\s+exists\s+)?([a-z0-9_."]+)\s+drop\s+column\s+(?:if\s+exists\s+)?([a-z0-9_"]+)`)
	dropTable    = regexp.MustCompile(`(?is)^\s*drop\s+table\s+(?:if\s+exists\s+)?([a-z0-9_."]+)`)
)

// tableConstraintWords open a table constraint rather than a column.
var tableConstraintWords = map[string]bool{
	"constraint": true,
	"primary":    true,
	"unique":     true,
	"foreign":    true,
	"check":      true,
	"exclude":    true,
	"like":       true,
}

// readSchema replays a body of SQL and returns the tables it ends with.
//
// It understands four shapes and no more — CREATE TABLE, ALTER TABLE ADD
// COLUMN, ALTER TABLE DROP COLUMN and DROP TABLE — because those are the four
// this repository's migrations use. A fifth shape appearing is not a silent
// problem: the columns it touches simply do not enter the schema, and
// [TestTheSchemaReaderIsNotBlind] is what keeps the four honest.
func readSchema(sql string) map[string]map[string]bool {
	sql = lineComment.ReplaceAllString(sql, " ")
	sql = blockComment.ReplaceAllString(sql, " ")
	sql = stringLit.ReplaceAllString(sql, "''")

	schema := map[string]map[string]bool{}

	for _, statement := range splitStatements(sql) {
		switch {
		case createTable.MatchString(statement):
			m := createTable.FindStringSubmatch(statement)
			table := bareName(m[1])
			if schema[table] == nil {
				schema[table] = map[string]bool{}
			}

			for _, item := range splitTopLevel(m[2]) {
				column := firstWord(item)
				if column == "" || tableConstraintWords[strings.ToLower(column)] {
					continue
				}

				schema[table][bareName(column)] = true
			}

		case alterAdd.MatchString(statement):
			m := alterAdd.FindStringSubmatch(statement)
			table := bareName(m[1])
			if schema[table] == nil {
				schema[table] = map[string]bool{}
			}

			schema[table][bareName(m[2])] = true

		case alterDrop.MatchString(statement):
			m := alterDrop.FindStringSubmatch(statement)
			if cols := schema[bareName(m[1])]; cols != nil {
				delete(cols, bareName(m[2]))
			}

		case dropTable.MatchString(statement):
			delete(schema, bareName(dropTable.FindStringSubmatch(statement)[1]))
		}
	}

	return schema
}

// splitStatements cuts SQL on semicolons that are not inside parentheses.
func splitStatements(sql string) []string {
	var (
		out   []string
		depth int
		start int
	)

	for i, r := range sql {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ';':
			if depth == 0 {
				out = append(out, sql[start:i])
				start = i + 1
			}
		}
	}

	if start < len(sql) {
		out = append(out, sql[start:])
	}

	return out
}

// splitTopLevel cuts a CREATE TABLE body on its top-level commas.
func splitTopLevel(body string) []string {
	var (
		out   []string
		depth int
		start int
	)

	for i, r := range body {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, body[start:i])
				start = i + 1
			}
		}
	}

	if start < len(body) {
		out = append(out, body[start:])
	}

	return out
}

// firstWord returns the first identifier of a CREATE TABLE body item.
func firstWord(item string) string {
	for _, field := range strings.Fields(item) {
		return field
	}

	return ""
}

// bareName strips quoting and any schema prefix from an identifier.
func bareName(name string) string {
	name = strings.ReplaceAll(name, `"`, "")
	if _, after, found := strings.Cut(name, "."); found {
		name = after
	}

	return strings.TrimSpace(name)
}

// isPersonColumnName reports whether the column's name is on the short list.
func isPersonColumnName(column string) bool {
	lower := strings.ToLower(column)
	for _, name := range personColumnNames {
		if lower == name {
			return true
		}
	}

	return false
}

// countColumns totals a schema's columns, for the blindness guard.
func countColumns(schema map[string]map[string]bool) int {
	n := 0
	for _, columns := range schema {
		n += len(columns)
	}

	return n
}

// sortedColumns lists a table's columns in order, for a readable failure.
func sortedColumns(columns map[string]bool) []string {
	out := make([]string, 0, len(columns))
	for c := range columns {
		out = append(out, c)
	}
	sort.Strings(out)

	return out
}

// sortedKeys lists a map's keys in order, so a failure reads the same twice.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)

	return out
}
