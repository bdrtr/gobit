package arch_test

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unwrittenColumns are the columns nothing writes, with the REASON.
//
// The key is "<module>.<table>.<column>". As long as a column is not written
// here with a reason, one nothing writes is a bug: it is a field that is always
// its zero value, on a row every reader pays to filter by.
//
// # Two kinds of entry, and the difference is the point
//
// A DECISION says the absence is deliberate and argued: somebody looked, and
// the column stays unwritten on purpose. An UNCLOSED FINDING says the opposite
// — the audit found it, nobody has decided anything yet, and the entry exists
// only so the gate can go green on the rest of the repository while the finding
// is worked.
//
// The two must never be written the same way. An exemption whose reason
// pretends an absence is intentional is exactly the failure D16 records: a gate
// that keeps its own counter-example in its documentation and passes anyway.
// Every unclosed finding below therefore opens with "UNCLOSED FINDING" and says
// what is not yet known.
//
// # The decisions
//
// The ten order and payment entries are ONE finding stated ten times, and it is
// a real one: the order and payment modules never soft-delete anything. Every
// read in both carries "deleted_at IS NULL" — a predicate that has never once
// been false. Removing the column is a schema decision and taking the deletes
// on is a product one; recording it here is what keeps it from being
// rediscovered.
//
// # The unclosed findings
//
// The nine remaining entries all appeared the day this audit started binding a
// column to its TABLE instead of matching bare names module-wide. Each is a
// table whose module soft-deletes some of its tables and not this one, so the
// written deleted_at of a sibling table covered it. Nobody has yet decided
// whether these rows should be deletable or whether the column should go, which
// is why none of them claims to be a decision.
var unwrittenColumns = map[string]string{
	"order.orders.deleted_at": "the order module never soft-deletes; nothing sets this and " +
		"every read filters on it",
	"order.order_line_items.deleted_at":   "as orders.deleted_at",
	"order.order_returns.deleted_at":      "as orders.deleted_at",
	"order.order_return_items.deleted_at": "as orders.deleted_at",
	"order.order_claims.deleted_at":       "as orders.deleted_at",
	"order.order_exchanges.deleted_at":    "as orders.deleted_at",
	"payment.payment_collections.deleted_at": "the payment module never soft-deletes; money " +
		"records are kept, and a refund is a row rather than a deletion",
	"payment.payment_sessions.deleted_at": "as payment_collections.deleted_at",
	"payment.payments.deleted_at":         "as payment_collections.deleted_at",
	"payment.refunds.deleted_at":          "as payment_collections.deleted_at",

	"product.product_category.deleted_at": "UNCLOSED FINDING, not a decision. The module " +
		"soft-deletes product, product_variant, product_option and product_image; this table " +
		"is not one of them and nothing has ever set the column. It was invisible while the " +
		"written set was keyed by bare name, because those four writes covered it. Whether a " +
		"category should be deletable at all has not been decided.",
	"product.product_collection.deleted_at":   "UNCLOSED FINDING, as product_category.deleted_at",
	"product.product_tag.deleted_at":          "UNCLOSED FINDING, as product_category.deleted_at",
	"product.product_option_value.deleted_at": "UNCLOSED FINDING, as product_category.deleted_at",
	"region.country.deleted_at": "UNCLOSED FINDING, not a decision. Only the region table " +
		"itself is soft-deleted in this module; country and currency carry the column and " +
		"nothing writes either. The country list is seeded and its rows are re-pointed at a " +
		"region rather than removed, so the column may well be wrong rather than unwritten — " +
		"but that is a schema question nobody has answered.",
	"region.currency.deleted_at": "UNCLOSED FINDING, as country.deleted_at",
	"inventory.stock_locations.deleted_at": "UNCLOSED FINDING, not a decision. The module " +
		"soft-deletes inventory_items and inventory_levels; a stock location has no delete " +
		"path at all, and its column was covered by those two writes. Whether closing a " +
		"location is a delete or a status has not been decided.",
	"inventory.inventory_reservations.deleted_at": "UNCLOSED FINDING, not a decision. A " +
		"reservation is released by SetReservationStatus rather than deleted, so the column " +
		"is the second way of saying a thing the status already says.",
	"fulfillment.fulfillments.deleted_at": "UNCLOSED FINDING, not a decision. The module " +
		"soft-deletes shipping_profiles, shipping_options and shipping_option_rules; a " +
		"fulfillment is never deleted, only advanced through UpdateFulfillmentStatus. Its " +
		"column was covered by the three that are written.",
}

// tableColumn is one column of one table.
//
// The type exists because the pair is the KEY of this whole audit. It was a
// bare string once, and D16 is what that cost: "completed_at" written on
// order_claims satisfied "completed_at" declared on order_exchanges, and the
// gate named that exact case in its own documentation while passing.
type tableColumn struct {
	table  string
	column string
}

// TestEveryColumnIsWrittenBySomething catches a column that exists and means
// nothing.
//
// # What the defect looks like
//
// A column is added, the model gains a field, the reader maps it back — and no
// INSERT or UPDATE ever names it. Everything compiles, every test passes, and
// the field is its zero value forever.
//
// # The claim this test used to make, struck
//
// The paragraph above used to continue: "It has happened here more than once:
// order_exchanges keeps completed_at and canceled_at and nothing writes either,
// so an exchange can never be recorded as finished (gaps.md D4)."
//
// That sentence was true and this test did not catch it. Measured 2026-09-06:
// both columns were provably unwritten, neither was in unwrittenColumns, and
// this gate was GREEN — for two reasons it named nowhere.
//
// The written set was keyed by bare column NAME across the whole module, so
// CompleteOrderClaim setting completed_at on order_claims satisfied
// order_exchanges.completed_at, and three unrelated queries setting canceled_at
// satisfied the other. And declarations were read out of CREATE TABLE bodies
// only, so every column added by ALTER TABLE ... ADD COLUMN since the initial
// migration was invisible — orders.archived_at among them, added the day
// before by internal/modules/order/migrations/000007_order_archived_at.up.sql.
//
// The citation is kept struck rather than deleted because the failure is the
// interesting part: a gate can carry its own counter-example in its
// documentation and still pass. The record is docs/gaps.md D16.
//
// # What changed
//
// Declarations are now REPLAYED. The module's .up.sql files are read in
// migration order and CREATE TABLE, ALTER TABLE ... ADD COLUMN, ALTER TABLE ...
// DROP COLUMN and DROP TABLE are applied in sequence, so the schema this audit
// sees is the schema the database ends up with rather than the one it started
// with. Writes are bound to their table: an INSERT's column list belongs to the
// table it inserts into, and a SET's assignments to the table the statement
// updates.
//
// Both are read through the tokenizer this package already had for the module
// SQL audit ([blankSQLNoise] and [tokenizeSQL]) rather than through line
// regexes. That is not a style preference. The line-based reader could not see
// a column definition whose type it did not have in a list, could not tell a
// multi-line CHECK constraint from a column, and read the word UPDATE out of
// the prose in a migration comment — and these migrations carry more prose than
// SQL.
//
// # The hole that is still open
//
// This audit reads SQL TEXT, not the call graph. A write statement that exists
// and is never called still counts as a write, and no operator can produce it.
//
// Measured rather than assumed, 2026-09-06: of the 445 sqlc queries in
// internal/modules, 9 have no hand-written caller anywhere in internal or core
// — and all 9 are SELECTs (GetInvoiceByNumber, ListInvoiceLinesForInvoices,
// GetSeries, GetRefund, GetCollectionByHandle, GetCategoryByHandle,
// ListTagsByIDs, GetVariantBySKU, CountTaxRatesByRegion). So the hole masks
// exactly ZERO columns today: every INSERT and every UPDATE in the repository
// has a caller.
//
// It is not closed here, and the reason is that the cheap version of the fix
// would be the same lie again. Requiring a write statement to have a
// hand-written caller moves the boundary one hop and stops: the caller can
// itself be unreachable, which is precisely how D17's CancelReturn and
// CancelClaim sit in the tree. Closing it honestly means reachability from the
// real entry points — routes, jobs, workflows — which is an SSA call graph over
// the 317k lines of internal and core, and golang.org/x/tools is an INDIRECT
// dependency of this module today. That is a different machine from a text
// audit and it should be built as one, with its own measurement, not bolted on
// here. Until then this test claims to read text and nothing more.
//
// # What counts as written
//
// A column named in an INSERT's column list, or on the left of an assignment in
// a SET — including the SET of an INSERT's ON CONFLICT DO UPDATE, which belongs
// to the table being inserted into. A column the DATABASE supplies — DEFAULT or
// GENERATED — is out of scope: its value is the schema's answer, not a missing
// write.
//
// Migrations are read for their INSERTs too: a seed table (the currency list)
// is filled once by a migration and never by a query, and calling that dead
// would be the audit misreading the one legitimate case.
func TestEveryColumnIsWrittenBySomething(t *testing.T) {
	t.Parallel()

	modules := moduleNames(t)
	require.NotEmpty(t, modules)

	scanned, used := 0, map[string]bool{}
	for _, module := range modules {
		migrationsPath := filepath.Join(repoRoot, modulesDir, module, migrationsDirName)
		migrations := readSQL(t, migrationsPath, upMigrationSuffix)
		if migrations == "" {
			continue
		}
		requireOrderedMigrations(t, migrationsPath)

		queries := readSQL(t, filepath.Join(repoRoot, modulesDir, module, "queries"), ".sql")
		audit := auditColumns(migrations, queries)
		scanned += audit.scanned

		for _, shape := range audit.unreadable {
			t.Errorf("the %s module's migrations contain a statement this audit cannot read: %s\n"+
				"The schema it replays is therefore NOT the schema the database ends up with, "+
				"and every column the statement touches is audited against the wrong "+
				"declaration. Teach the replay the shape rather than letting it guess; a "+
				"reader that skips what it does not understand is how this gate came to keep "+
				"its own counter-example in its documentation (docs/gaps.md D16).",
				module, shape)
		}

		for _, finding := range audit.unwritten {
			key := module + "." + finding.table + "." + finding.column
			if reason, exempt := unwrittenColumns[key]; exempt {
				used[key] = true
				require.NotEmpty(t, reason, "%s is exempt with no reason", key)

				continue
			}

			t.Errorf("%s is never written: no INSERT names it and no UPDATE sets it.\n"+
				"The column is its zero value on every row, and every read that filters "+
				"on it filters on nothing. Either write it, drop it, or — if the "+
				"database is meant to supply it — give it a DEFAULT. If it is "+
				"deliberately unwritten, record it in unwrittenColumns WITH ITS REASON; "+
				"if you have not looked yet, say THAT in the reason rather than inventing "+
				"an intention.",
				key)
		}
	}

	require.Positive(t, scanned,
		"no column was scanned; the audit has gone blind.\n"+
			"The migrations moved, or CREATE TABLE is no longer written the way the "+
			"replay expects — and a scan that reads no column approves every dead one.")

	for _, key := range sortedKeys(unwrittenColumns) {
		assert.True(t, used[key],
			"%s is exempt in unwrittenColumns but is NOT unwritten any more.\n"+
				"Either the column is written now, or it is gone. Delete the entry — a dead "+
				"exemption covers up the next real one.", key)
	}
}

// columnAudit is the readout of one module's SQL.
//
// scanned is carried out of the audit because it is the only defense against
// the whole thing silently reading nothing: a replay that builds an empty
// schema reports no unwritten column, which is indistinguishable from a clean
// module unless the count is checked.
type columnAudit struct {
	scanned    int
	unwritten  []tableColumn
	unreadable []string
}

// auditColumns replays a module's migrations and reports the columns its own
// SQL never writes.
//
// The migrations are handed to the write reader as well as to the replay,
// deliberately: a seed INSERT in a migration is the one legitimate way a table
// is filled without a query.
func auditColumns(migrations, queries string) columnAudit {
	replay := newSchemaReplay()
	replay.apply(migrations)
	writes := columnWrites(migrations + "\n" + queries)

	audit := columnAudit{unreadable: replay.unreadable}
	for _, table := range sortedTableNames(replay.tables) {
		for _, column := range sortedColumnNames(replay.tables[table]) {
			audit.scanned++
			if replay.tables[table][column] || writes[tableColumn{table: table, column: column}] {
				continue
			}
			audit.unwritten = append(audit.unwritten, tableColumn{table: table, column: column})
		}
	}

	return audit
}

// migrationSchema is the table set a module's migrations leave behind: for each
// table, its columns, each mapped to whether the DATABASE supplies the value.
type migrationSchema map[string]map[string]bool

// schemaReplay applies a module's migrations in order and collects the shapes
// it could not read.
//
// # Why a replay and not a scan
//
// Reading CREATE TABLE bodies alone was the previous shape and it answered the
// wrong question: it described the schema the module STARTED with. Every column
// added since — four of them in this repository, in three modules — was outside
// the audit entirely, and one of them was the archived_at added the day before
// D16 was measured. A migration directory is a sequence of edits to a schema,
// so the only faithful reading of it is to apply them.
//
// # Why unreadable shapes are collected instead of skipped
//
// Skipping is what a scanner does; it is also what made this gate green while
// its own documentation named the column it was missing. ALTER TABLE has
// actions this replay does not model — RENAME COLUMN changes a column's name
// and ALTER COLUMN ... SET DEFAULT changes whether the database supplies it —
// and both would leave the replayed schema quietly wrong. They are reported as
// findings so the reader is forced to teach the replay rather than discover
// years later that it guessed.
type schemaReplay struct {
	tables     migrationSchema
	unreadable []string
}

// newSchemaReplay returns an empty replay.
func newSchemaReplay() *schemaReplay {
	return &schemaReplay{tables: migrationSchema{}}
}

// apply runs every statement of an SQL body against the replay, in order.
func (r *schemaReplay) apply(sql string) {
	for _, statement := range sqlStatements(sql) {
		switch {
		case wordAt(statement, 0, "create") && wordAt(statement, 1, "table"):
			r.createTable(statement)
		case wordAt(statement, 0, "alter") && wordAt(statement, 1, "table"):
			r.alterTable(statement)
		case wordAt(statement, 0, "drop") && wordAt(statement, 1, "table"):
			r.dropTable(statement)
		}
	}
}

// note records a statement the replay could not read.
func (r *schemaReplay) note(what string, tokens []sqlToken) {
	r.unreadable = append(r.unreadable, what+": "+summarizeTokens(tokens))
}

// createTable declares the table's columns.
func (r *schemaReplay) createTable(statement []sqlToken) {
	at := skipWords(statement, 2, "if", "not", "exists")
	name, at := qualifiedSQLName(statement, at)
	body, _, ok := parenBody(statement, at)
	if name == "" || !ok {
		r.note("CREATE TABLE", statement)

		return
	}

	columns := map[string]bool{}
	for _, definition := range splitTopLevel(body) {
		if len(definition) == 0 || tableConstraintLeads[definition[0].text] {
			continue
		}
		if !definition[0].word || len(definition) < 2 || !definition[1].word {
			r.note("column definition in "+name, definition)

			continue
		}
		columns[definition[0].text] = databaseSupplies(definition)
	}
	r.tables[name] = columns
}

// alterTable applies the column actions of an ALTER TABLE.
func (r *schemaReplay) alterTable(statement []sqlToken) {
	at := skipWords(statement, 2, "only")
	name, at := qualifiedSQLName(statement, at)
	if name == "" {
		r.note("ALTER TABLE", statement)

		return
	}
	if r.tables[name] == nil {
		r.tables[name] = map[string]bool{}
	}

	for _, action := range splitTopLevel(statement[at:]) {
		if len(action) == 0 || !action[0].word {
			continue
		}
		switch {
		case action[0].text == "add" && !namesAConstraint(action):
			r.addColumn(name, action)
		case action[0].text == "drop" && !namesAConstraint(action):
			r.dropColumn(name, action)
		case action[0].text == "add" || action[0].text == "drop":
			// A constraint, not a column. Constraints are the schema's own
			// business: they change what a value may BE, never whether the
			// column exists or who supplies it.
		case harmlessAlterActions[action[0].text]:
			// Storage, ownership and trigger settings, none of which move a
			// column into or out of this audit's scope.
		default:
			r.note("ALTER TABLE "+name+" action", action)
		}
	}
}

// addColumn declares a column an ALTER TABLE adds.
//
// The word COLUMN is optional in Postgres — "ALTER TABLE t ADD c text" is legal
// — so the action is read as an add-column whenever what follows ADD is not one
// of the constraint keywords. Requiring the word was the first draft and it is
// the same class of mistake this whole file is about: a form the reader does
// not recognize silently becoming a column that is never audited.
func (r *schemaReplay) addColumn(table string, action []sqlToken) {
	at := skipWords(action, 1, "column")
	at = skipWords(action, at, "if", "not", "exists")
	if at >= len(action) || !action[at].word {
		r.note("ALTER TABLE "+table+" ADD", action)

		return
	}
	r.tables[table][action[at].text] = databaseSupplies(action[at:])
}

// dropColumn removes a column an ALTER TABLE drops.
func (r *schemaReplay) dropColumn(table string, action []sqlToken) {
	at := skipWords(action, 1, "column")
	at = skipWords(action, at, "if", "exists")
	if at >= len(action) || !action[at].word {
		r.note("ALTER TABLE "+table+" DROP", action)

		return
	}
	delete(r.tables[table], action[at].text)
}

// dropTable removes the tables a DROP TABLE names.
func (r *schemaReplay) dropTable(statement []sqlToken) {
	at := skipWords(statement, 2, "if", "exists")
	for _, part := range splitTopLevel(statement[at:]) {
		if name, _ := qualifiedSQLName(part, 0); name != "" {
			delete(r.tables, name)
		}
	}
}

// namesAConstraint reports whether an ALTER TABLE action's second word makes it
// a constraint action rather than a column one.
func namesAConstraint(action []sqlToken) bool {
	return len(action) > 1 && action[1].word && tableConstraintLeads[action[1].text]
}

// tableConstraintLeads are the words that begin a TABLE constraint rather than
// a column.
//
// This is a DENY list and the direction matters. The previous reader used an
// allow list of column TYPES — nine of them — and anything else in a CREATE
// TABLE body was silently not a column. A type list is open-ended: the day a
// uuid or a date column is added it leaves the audit without a sound. The
// constraint keywords are a closed grammar, so denying them and treating
// everything else as a column can only ever fail in the direction that gets
// noticed, which is a false accusation somebody has to answer.
//
// LIKE is in the list because it is a table element too — it copies another
// table's columns — and this replay does not follow it; it has no use here yet,
// and if one appears the copied columns would need copying.
var tableConstraintLeads = map[string]bool{
	"primary":    true,
	"foreign":    true,
	"unique":     true,
	"check":      true,
	"constraint": true,
	"exclude":    true,
	"like":       true,
}

// harmlessAlterActions are the ALTER TABLE actions that cannot change the
// column set or who supplies a value.
//
// ALTER and RENAME are deliberately ABSENT: "ALTER COLUMN x SET DEFAULT" moves
// a column out of this audit's scope and "RENAME COLUMN" changes its name, so
// both must be reported as unreadable until the replay learns them.
var harmlessAlterActions = map[string]bool{
	"validate": true,
	"enable":   true,
	"disable":  true,
	"cluster":  true,
	"owner":    true,
	"set":      true,
	"reset":    true,
	"inherit":  true,
	"attach":   true,
	"detach":   true,
	"replica":  true,
	"no":       true,
}

// databaseSupplies reports whether a column definition hands the value to the
// database.
func databaseSupplies(definition []sqlToken) bool {
	for _, token := range definition {
		if token.word && (token.text == "default" || token.text == "generated") {
			return true
		}
	}

	return false
}

// columnWrites returns every table-bound column an INSERT names or a SET
// assigns.
//
// # Why the target is carried across the statement
//
// A statement has at most one table it writes, and it may name the columns in
// two places: the INSERT's column list and the SET of an ON CONFLICT DO UPDATE.
// Carrying the target forward binds the second to the same table as the first,
// which is what the three upserts in this repository need (product's variant
// option values, cart's addresses, promotion's application method).
//
// # Why FOR UPDATE does not become a target
//
// "SELECT ... FOR UPDATE" and "FOR NO KEY UPDATE" are the lock this repository
// takes before nearly every write, and the word UPDATE in them means the
// opposite of a write. Two guards keep them out: the token before UPDATE must
// not be FOR or KEY, and the target is only accepted when a SET actually
// follows the table name and its optional alias. The second guard is what makes
// auth's RegisterLoginFailure readable — a CTE that takes FOR UPDATE inside its
// parentheses and then updates auth_identity AS i.
func columnWrites(sql string) map[tableColumn]bool {
	out := map[tableColumn]bool{}
	for _, statement := range sqlStatements(sql) {
		target := ""
		for i := 0; i < len(statement); i++ {
			if !statement[i].word {
				continue
			}
			switch statement[i].text {
			case "insert":
				if !wordAt(statement, i+1, "into") {
					continue
				}
				name, next := qualifiedSQLName(statement, i+2)
				if name == "" {
					continue
				}
				target = name
				for _, column := range insertColumnList(statement, next) {
					out[tableColumn{table: name, column: column}] = true
				}
				i = next - 1
			case "update":
				if wordAt(statement, i-1, "for") || wordAt(statement, i-1, "key") {
					continue
				}
				name, set := updateTarget(statement, i+1)
				if name == "" {
					continue
				}
				target = name
				i = set - 1
			case "set":
				if target == "" {
					continue
				}
				for _, column := range setAssignments(statement, i+1) {
					out[tableColumn{table: target, column: column}] = true
				}
			}
		}
	}

	return out
}

// insertColumnList returns the column names of the parenthesis that follows an
// INSERT's table name, and nothing when there is no such list.
//
// "INSERT INTO t VALUES (...)" and "INSERT INTO t SELECT ..." name no columns,
// and neither does a parenthesis holding anything other than bare names. The
// refusal is deliberate: reading a VALUES list as a column list would mark every
// column of the table written by the literals inside it.
func insertColumnList(tokens []sqlToken, at int) []string {
	body, _, ok := parenBody(tokens, at)
	if !ok {
		return nil
	}

	var out []string
	for _, part := range splitTopLevel(body) {
		if len(part) != 1 || !part[0].word {
			return nil
		}
		out = append(out, part[0].text)
	}

	return out
}

// updateTarget reads "<table> [AS <alias>] SET" and returns the table with the
// index of the SET.
//
// An empty name means this UPDATE is not a statement target — the lock keyword,
// or the UPDATE of an ON CONFLICT DO UPDATE, whose SET belongs to the INSERT's
// table and is left to the caller's carried target.
func updateTarget(tokens []sqlToken, at int) (name string, set int) {
	name, next := qualifiedSQLName(tokens, at)
	if name == "" || name == "set" {
		return "", at
	}

	probe := skipWords(tokens, next, "as")
	if !wordAt(tokens, probe, "set") && probe < len(tokens) && tokens[probe].word {
		probe++
	}
	if !wordAt(tokens, probe, "set") {
		return "", at
	}

	return name, probe
}

// setAssignments returns the columns assigned by a SET clause.
//
// # Why position and not a pattern
//
// A SET clause is a comma-separated list of assignments, so a column name is
// only ever the first word after the SET or after a comma at the clause's own
// depth. Matching "name =" anywhere instead — the previous shape — reads the
// equalities inside a CASE as writes: auth's RegisterLoginFailure sets
// locked_until from a CASE whose branches compare three other columns, and each
// comparison would have counted as writing them.
//
// # Where it stops
//
// At WHERE, RETURNING or FROM at the clause's own depth, or at the parenthesis
// that closes the statement around it. FROM has to be a terminator because an
// UPDATE ... FROM is how a CTE's result is joined in, and it must NOT terminate
// inside parentheses because EXTRACT(EPOCH FROM x) is an ordinary expression.
//
// The multi-column form, "SET (a, b) = (SELECT ...)", is read rather than
// skipped. It appears nowhere in this repository today; it is here because a
// SET shape the reader does not understand reports the columns as UNWRITTEN,
// and a false accusation is how a gate loses the argument and gets deleted.
func setAssignments(tokens []sqlToken, at int) []string {
	var out []string
	depth, expectColumn := 0, true
	for i := at; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case !token.word && token.text == "(":
			if depth == 0 && expectColumn {
				body, next, ok := parenBody(tokens, i)
				if ok {
					out = append(out, bareNamesIn(body)...)
					expectColumn = false
					i = next - 1

					continue
				}
			}
			depth++
		case !token.word && token.text == ")":
			if depth == 0 {
				return out
			}
			depth--
		case !token.word && token.text == ",":
			if depth == 0 {
				expectColumn = true
			}
		case token.word && depth == 0:
			if setClauseTerminators[token.text] {
				return out
			}
			if expectColumn {
				if i+1 < len(tokens) && !tokens[i+1].word && tokens[i+1].text == "=" {
					out = append(out, token.text)
				}
				expectColumn = false
			}
		}
	}

	return out
}

// setClauseTerminators are the keywords that end a SET clause.
var setClauseTerminators = map[string]bool{
	"where":     true,
	"returning": true,
	"from":      true,
}

// bareNamesIn returns the comma-separated single-word parts of a token run.
func bareNamesIn(tokens []sqlToken) []string {
	var out []string
	for _, part := range splitTopLevel(tokens) {
		if len(part) == 1 && part[0].word {
			out = append(out, part[0].text)
		}
	}

	return out
}

// sqlStatements splits an SQL body into statements, with its comments and
// quoted literals already blanked.
//
// Blanking first is not optional here. These migrations carry more argued prose
// than SQL — one of them spends sixty lines on why a column has no DEFAULT —
// and the word UPDATE appears in that prose more often than in the statements.
// The literals go with the comments because the seed migrations hold country
// and currency names, and a semicolon inside one would split a statement in
// half.
func sqlStatements(sql string) [][]sqlToken {
	var out [][]sqlToken
	var current []sqlToken
	for _, token := range tokenizeSQL(blankSQLNoise(sql)) {
		if !token.word && token.text == ";" {
			if len(current) > 0 {
				out = append(out, current)
			}
			current = nil

			continue
		}
		current = append(current, token)
	}
	if len(current) > 0 {
		out = append(out, current)
	}

	return out
}

// wordAt reports whether the token at the index is the given keyword.
func wordAt(tokens []sqlToken, at int, word string) bool {
	return at >= 0 && at < len(tokens) && tokens[at].word && tokens[at].text == word
}

// skipWords advances past the keyword sequence when the whole of it is present,
// and returns the index unchanged otherwise.
func skipWords(tokens []sqlToken, at int, words ...string) int {
	for offset, word := range words {
		if !wordAt(tokens, at+offset, word) {
			return at
		}
	}

	return at + len(words)
}

// parenBody returns the tokens inside the parenthesis that starts at the index,
// with the index just past its close.
func parenBody(tokens []sqlToken, at int) (body []sqlToken, next int, ok bool) {
	if at >= len(tokens) || tokens[at].word || tokens[at].text != "(" {
		return nil, at, false
	}

	depth := 0
	for i := at; i < len(tokens); i++ {
		if tokens[i].word {
			continue
		}
		switch tokens[i].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return tokens[at+1 : i], i + 1, true
			}
		}
	}

	return nil, at, false
}

// splitTopLevel splits a token run at the commas that are not inside a
// parenthesis.
//
// This is what lets a multi-line CHECK constraint be ONE table element. Split
// by LINE instead — the previous shape — and the constraint's own lines arrive
// as separate definitions: campaign's budget CHECK produced four of them, of
// which "AND budget_currency_code IS NOT NULL" looks exactly like a column
// declaration.
func splitTopLevel(tokens []sqlToken) [][]sqlToken {
	var out [][]sqlToken
	depth, start := 0, 0
	for i, token := range tokens {
		if token.word {
			continue
		}
		switch token.text {
		case "(":
			depth++
		case ")":
			depth--
		case ",":
			if depth == 0 {
				out = append(out, tokens[start:i])
				start = i + 1
			}
		}
	}
	if start < len(tokens) {
		out = append(out, tokens[start:])
	}

	return out
}

// summarizeTokens renders a token run back into something a failure message can
// print.
//
// It is only ever used to describe a statement the replay could NOT read, so
// the point is recognizability rather than fidelity: the reader has to be able
// to find the statement in the migration, and a run of sixty tokens would bury
// the shape that matters.
func summarizeTokens(tokens []sqlToken) string {
	const shown = 12

	parts := make([]string, 0, shown)
	for _, token := range tokens[:min(shown, len(tokens))] {
		parts = append(parts, token.text)
	}
	if len(tokens) > shown {
		parts = append(parts, "...")
	}

	return strings.Join(parts, " ")
}

// requireOrderedMigrations verifies that a migration directory's files sort the
// way they run.
//
// The replay depends on it completely: [readSQL] concatenates the directory in
// the order the filesystem lists it, which is lexical, and applying a DROP
// COLUMN before the ADD COLUMN it undoes would leave the audit with a column
// the database does not have. Lexical order equals migration order only while
// every prefix is padded to the same width, so that is what is checked — one
// file numbered "10_" beside "000009_" is the whole failure.
func requireOrderedMigrations(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "%s could not be read", dir)

	width, previous := 0, -1
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), upMigrationSuffix) {
			continue
		}
		digits := entry.Name()[:strings.IndexByte(entry.Name()+"_", '_')]
		number, convErr := strconv.Atoi(digits)
		require.NoError(t, convErr,
			"%s does not begin with a migration number; the replay reads this directory in "+
				"lexical order and can only trust it while every file is numbered", entry.Name())

		if width == 0 {
			width = len(digits)
		}
		require.Equal(t, width, len(digits),
			"%s numbers its migration with %d digits where its siblings use %d.\n"+
				"Lexical order stops matching migration order at that point, and the replay "+
				"would apply the ALTERs of this directory in the wrong sequence.",
			entry.Name(), len(digits), width)
		require.Greater(t, number, previous,
			"%s does not come after the migration before it; two migrations share a number, "+
				"and which one the replay applies last is the filesystem's choice",
			entry.Name())
		previous = number
	}
}

// sortedTableNames returns a schema's table names in order.
func sortedTableNames(schema migrationSchema) []string {
	out := make([]string, 0, len(schema))
	for name := range schema {
		out = append(out, name)
	}
	sort.Strings(out)

	return out
}

// sortedColumnNames returns a table's column names in order.
func sortedColumnNames(columns map[string]bool) []string {
	out := make([]string, 0, len(columns))
	for name := range columns {
		out = append(out, name)
	}
	sort.Strings(out)

	return out
}

// readSQL concatenates the SQL files of a directory; a missing directory is the
// empty string, because not every module has one.
func readSQL(t *testing.T, dir, suffix string) string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	var builder strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, readErr, "%s could not be read", entry.Name())
		builder.Write(content)
		builder.WriteString("\n")
	}

	return builder.String()
}

// sortedKeys returns the map's keys in order, so the errors do not shuffle.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)

	return out
}

// columnScannerCase is one planted SQL body with the reading it must produce.
//
// tables is written as "<table>.<column>" for the columns the replay must
// declare, and supplied lists the subset the DATABASE fills in. writes is the
// same shape for the table-bound columns the write reader must find. A nil
// expectation means "nothing", and it is as much of an assertion as a full one:
// the refusals are where this reader earns its keep.
type columnScannerCase struct {
	name     string
	sql      string
	tables   []string
	supplied []string
	writes   []string
	// unreadable is true when the replay must REPORT the statement rather than
	// quietly do something with it.
	unreadable bool
}

// columnScannerCases pin the shapes the reader must get right.
//
// Each of the first four is a shape the previous line-based reader got WRONG,
// and the last group is the set of refusals that keep the new one from
// over-reading. They are written as SQL rather than as token slices because the
// tokenizer is part of what is under test: a case built from tokens would pass
// while the blanking of comments and literals was broken, and the prose in
// these migrations is exactly where that would bite.
var columnScannerCases = []columnScannerCase{
	{
		name: "a column added by ALTER TABLE is declared",
		sql: "CREATE TABLE alpha (id TEXT NOT NULL);\n" +
			"ALTER TABLE alpha ADD COLUMN IF NOT EXISTS shipped_at TIMESTAMPTZ;",
		tables: []string{"alpha.id", "alpha.shipped_at"},
	},
	{
		name: "a column added by ALTER TABLE with a DEFAULT is supplied",
		sql: "CREATE TABLE alpha (id TEXT);\n" +
			"ALTER TABLE alpha ADD COLUMN tax_rate_bps INTEGER NOT NULL DEFAULT 0;",
		tables:   []string{"alpha.id", "alpha.tax_rate_bps"},
		supplied: []string{"alpha.tax_rate_bps"},
	},
	{
		name: "a column dropped by ALTER TABLE is gone",
		sql: "CREATE TABLE alpha (id TEXT, completed_at TIMESTAMPTZ);\n" +
			"ALTER TABLE alpha DROP COLUMN IF EXISTS completed_at;",
		tables: []string{"alpha.id"},
	},
	{
		name: "ADD without the word COLUMN is still a column",
		sql: "CREATE TABLE alpha (id TEXT);\n" +
			"ALTER TABLE alpha ADD shipped_at TIMESTAMPTZ;",
		tables: []string{"alpha.id", "alpha.shipped_at"},
	},
	{
		name:   "a dropped table takes its columns with it",
		sql:    "CREATE TABLE alpha (id TEXT);\nCREATE TABLE beta (id TEXT);\nDROP TABLE IF EXISTS beta;",
		tables: []string{"alpha.id"},
	},
	{
		name: "a multi-line CHECK is one table element, not four columns",
		sql: "CREATE TABLE alpha (\n" +
			"    id TEXT NOT NULL,\n" +
			"    budget_type TEXT,\n" +
			"    CONSTRAINT alpha_budget_shape CHECK (\n" +
			"        CASE budget_type\n" +
			"            WHEN 'spend' THEN budget_currency_code IS NOT NULL\n" +
			"            ELSE budget_currency_code IS NULL\n" +
			"        END\n" +
			"    ),\n" +
			"    PRIMARY KEY (id)\n" +
			");",
		tables: []string{"alpha.budget_type", "alpha.id"},
	},
	{
		name:   "a type the reader has never heard of is still a column",
		sql:    "CREATE TABLE alpha (id UUID NOT NULL, span INTERVAL, tags TEXT[]);",
		tables: []string{"alpha.id", "alpha.span", "alpha.tags"},
	},
	{
		name:       "RENAME COLUMN is reported rather than ignored",
		sql:        "CREATE TABLE alpha (id TEXT);\nALTER TABLE alpha RENAME COLUMN id TO alpha_id;",
		tables:     []string{"alpha.id"},
		unreadable: true,
	},
	{
		name:       "ALTER COLUMN SET DEFAULT is reported rather than ignored",
		sql:        "CREATE TABLE alpha (id TEXT);\nALTER TABLE alpha ALTER COLUMN id SET DEFAULT '';",
		tables:     []string{"alpha.id"},
		unreadable: true,
	},
	{
		name:   "ADD CONSTRAINT is neither a column nor a complaint",
		sql:    "CREATE TABLE alpha (id TEXT);\nALTER TABLE alpha ADD CONSTRAINT alpha_id_check CHECK (id <> '');",
		tables: []string{"alpha.id"},
	},
	{
		name:   "an INSERT writes ITS table's column and no other's",
		sql:    "CREATE TABLE alpha (id TEXT);\nCREATE TABLE beta (id TEXT);\nINSERT INTO alpha (id) VALUES ($1);",
		tables: []string{"alpha.id", "beta.id"},
		writes: []string{"alpha.id"},
	},
	{
		name:   "an UPDATE with an alias binds to the aliased table",
		sql:    "UPDATE alpha AS i SET shipped_at = now() FROM other WHERE i.id = other.id;",
		writes: []string{"alpha.shipped_at"},
	},
	{
		name:   "an UPDATE with a bare alias binds to the aliased table",
		sql:    "UPDATE alpha i SET shipped_at = now() WHERE i.id = $1;",
		writes: []string{"alpha.shipped_at"},
	},
	{
		name:   "FOR UPDATE is a lock and writes nothing",
		sql:    "SELECT * FROM alpha WHERE id = $1 FOR NO KEY UPDATE;\nSELECT * FROM beta FOR UPDATE;",
		writes: nil,
	},
	{
		name: "a CTE that locks and then updates binds to the updated table",
		sql: "WITH next_state AS (\n" +
			"    SELECT k.id FROM alpha k WHERE k.id = $1 FOR UPDATE\n" +
			")\n" +
			"UPDATE alpha AS i SET failed_attempts = next_state.attempts\n" +
			"FROM next_state WHERE i.id = next_state.id RETURNING i.*;",
		writes: []string{"alpha.failed_attempts"},
	},
	{
		name: "an upsert's DO UPDATE SET belongs to the table inserted into",
		sql: "INSERT INTO alpha (variant_id, value_id) VALUES ($1, $2)\n" +
			"ON CONFLICT (variant_id) DO UPDATE SET value_id = EXCLUDED.value_id;",
		writes: []string{"alpha.value_id", "alpha.variant_id"},
	},
	{
		name:   "a comparison inside a CASE is not a write",
		sql:    "UPDATE alpha SET locked_until = CASE WHEN failed_attempts >= $1 THEN $2 WHEN status = 'x' THEN $3 END WHERE id = $4;",
		writes: []string{"alpha.locked_until"},
	},
	{
		name:   "a WHERE equality after the SET is not a write",
		sql:    "UPDATE alpha SET shipped_at = now() WHERE deleted_at IS NULL AND status = 'pending';",
		writes: []string{"alpha.shipped_at"},
	},
	{
		name:   "a function argument inside the SET is not a column",
		sql:    "UPDATE alpha SET total = greatest($1, $2), stamp = extract(epoch FROM now()) WHERE id = $3;",
		writes: []string{"alpha.stamp", "alpha.total"},
	},
	{
		name:   "an INSERT without a column list names no columns",
		sql:    "INSERT INTO alpha VALUES ($1, $2, $3);\nINSERT INTO beta SELECT id, name FROM alpha;",
		writes: nil,
	},
	{
		name:   "the multi-column SET form is read, not refused",
		sql:    "UPDATE alpha SET (total, stamp) = (SELECT sum(x), now() FROM beta) WHERE id = $1;",
		writes: []string{"alpha.stamp", "alpha.total"},
	},
	{
		name: "SQL written inside a comment is prose",
		sql: "-- The module has no UPDATE alpha SET shipped_at = now() anywhere, and an\n" +
			"-- INSERT INTO beta (id, shipped_at) would be the wrong shape.\n" +
			"CREATE TABLE alpha (id TEXT);",
		tables: []string{"alpha.id"},
		writes: nil,
	},
	{
		name:   "SQL written inside a string literal is data",
		sql:    "INSERT INTO alpha (id, note) VALUES ($1, 'UPDATE beta SET shipped_at = now()');",
		writes: []string{"alpha.id", "alpha.note"},
	},
}

// TestTheColumnScannerIsNotBlind pins the floor under the reading.
//
// TestEveryColumnIsWrittenBySomething passes on today's tree, and D16 is the
// proof that a green gate says nothing on its own: it passed for a year while
// the two columns it cites in its own documentation went unwritten. These cases
// are what separates "there is nothing wrong" from "I am not looking", and half
// of them are refusals — the reader that over-reads accuses somebody, and a
// gate that accuses gets deleted.
func TestTheColumnScannerIsNotBlind(t *testing.T) {
	t.Parallel()

	for _, planted := range columnScannerCases {
		t.Run(planted.name, func(t *testing.T) {
			t.Parallel()

			replay := newSchemaReplay()
			replay.apply(planted.sql)

			var declared, supplied []string
			for _, table := range sortedTableNames(replay.tables) {
				for _, column := range sortedColumnNames(replay.tables[table]) {
					declared = append(declared, table+"."+column)
					if replay.tables[table][column] {
						supplied = append(supplied, table+"."+column)
					}
				}
			}

			assert.ElementsMatch(t, planted.tables, declared,
				"the columns replayed out of %q are not the ones the case expects.\n"+
					"A column that went MISSING is a column this audit will never ask about "+
					"again; one that appeared out of nowhere becomes a finding against a "+
					"column that does not exist.", planted.sql)
			assert.ElementsMatch(t, planted.supplied, supplied,
				"the DATABASE-supplied columns of %q are not the ones the case expects.\n"+
					"Calling a plain column supplied buys silence for exactly the defect this "+
					"gate exists to find; calling a DEFAULT column unsupplied accuses the "+
					"schema of a write it was never meant to receive.", planted.sql)

			var written []string
			for pair := range columnWrites(planted.sql) {
				written = append(written, pair.table+"."+pair.column)
			}
			assert.ElementsMatch(t, planted.writes, written,
				"the writes read out of %q are not the ones the case expects.\n"+
					"A write bound to the WRONG table is D16 itself: it satisfies a "+
					"same-named column on a table nothing writes.", planted.sql)

			assert.Equal(t, planted.unreadable, len(replay.unreadable) > 0,
				"the replay's report of unreadable statements in %q is wrong; it said %v.\n"+
					"A shape it silently misreads leaves the schema wrong with nothing said, "+
					"and a false complaint sends the reader after a statement that is fine.",
				planted.sql, replay.unreadable)
		})
	}
}

// TestTheColumnAuditCatchesAViolation is the positive control on the DECISION
// rather than on the reading.
//
// The reader can be perfect and the audit still toothless: a schema replayed
// and never compared, a lookup keyed the wrong way round, a supplied flag read
// as its opposite. This plants whole two-file corpora and requires the audit to
// name exactly the right column in each.
//
// The first two cases are the two holes D16 measured, each planted in the shape
// that hid it.
func TestTheColumnAuditCatchesAViolation(t *testing.T) {
	t.Parallel()

	t.Run("a column added by ALTER TABLE and never written is found", func(t *testing.T) {
		t.Parallel()

		audit := auditColumns(
			"CREATE TABLE alpha (id TEXT NOT NULL);\n"+
				"ALTER TABLE alpha ADD COLUMN IF NOT EXISTS shipped_at TIMESTAMPTZ;",
			"INSERT INTO alpha (id) VALUES ($1);")

		assert.Equal(t, []tableColumn{{table: "alpha", column: "shipped_at"}}, audit.unwritten,
			"the planted ALTER-added column was NOT reported.\n"+
				"This is the hole verbatim: while declarations were read out of CREATE TABLE "+
				"bodies alone, every column added by a later migration was outside the audit, "+
				"and the gate was green on all four of them.")
		assert.Equal(t, 2, audit.scanned,
			"the audit did not scan both columns of the planted table")
	})

	t.Run("a same-named column on an unwritten table is found", func(t *testing.T) {
		t.Parallel()

		audit := auditColumns(
			"CREATE TABLE alpha (id TEXT, completed_at TIMESTAMPTZ);\n"+
				"CREATE TABLE beta (id TEXT, completed_at TIMESTAMPTZ);",
			"INSERT INTO alpha (id) VALUES ($1);\n"+
				"UPDATE alpha SET completed_at = now() WHERE id = $1;\n"+
				"INSERT INTO beta (id) VALUES ($1);")

		assert.Equal(t, []tableColumn{{table: "beta", column: "completed_at"}}, audit.unwritten,
			"the planted same-named column was NOT reported.\n"+
				"This is D16's first hole verbatim: order_claims.completed_at is written and "+
				"order_exchanges.completed_at is not, and a written set keyed by bare name "+
				"let the first stand in for the second.")
	})

	t.Run("a written column is not reported", func(t *testing.T) {
		t.Parallel()

		audit := auditColumns(
			"CREATE TABLE alpha (id TEXT);\nALTER TABLE alpha ADD COLUMN shipped_at TIMESTAMPTZ;",
			"INSERT INTO alpha (id) VALUES ($1);\nUPDATE alpha SET shipped_at = now() WHERE id = $1;")

		assert.Empty(t, audit.unwritten,
			"a column an UPDATE plainly sets was reported as unwritten; the rule has become "+
				"'every column is a violation' and the whole repository is one")
	})

	t.Run("a column the database supplies is out of scope", func(t *testing.T) {
		t.Parallel()

		audit := auditColumns(
			"CREATE TABLE alpha (id TEXT);\n"+
				"ALTER TABLE alpha ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();",
			"INSERT INTO alpha (id) VALUES ($1);")

		assert.Empty(t, audit.unwritten,
			"a column carrying a DEFAULT was reported as unwritten. Its value is the schema's "+
				"answer rather than a missing write, and reporting it would push every author "+
				"towards adding a DEFAULT to silence the gate — which is the one change that "+
				"makes the real defect permanently invisible.")
	})

	t.Run("a seed INSERT in a migration counts as a write", func(t *testing.T) {
		t.Parallel()

		audit := auditColumns(
			"CREATE TABLE currency (code TEXT, name TEXT);\n"+
				"INSERT INTO currency (code, name) VALUES ('usd', 'US Dollar');",
			"")

		assert.Empty(t, audit.unwritten,
			"a table filled once by its own migration was called dead. The currency list is "+
				"exactly that, and calling it a finding is the audit misreading the one "+
				"legitimate case.")
	})

	t.Run("an unreadable ALTER is carried out of the audit", func(t *testing.T) {
		t.Parallel()

		audit := auditColumns(
			"CREATE TABLE alpha (id TEXT);\nALTER TABLE alpha RENAME COLUMN id TO alpha_id;",
			"INSERT INTO alpha (alpha_id) VALUES ($1);")

		assert.NotEmpty(t, audit.unreadable,
			"the audit swallowed an ALTER TABLE shape it does not model. The replayed schema "+
				"is wrong from that statement on, and nothing says so.")
	})
}
