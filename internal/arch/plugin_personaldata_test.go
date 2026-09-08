package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file closes the blindness D30 found: the personal-data audit could not
// see a plugin at all.
//
// # What was wrong
//
// internal/app/personaldata_test.go checks, in both directions, that every
// person-shaped column a module holds is declared and that every declaration
// names a column that exists. Its population comes from registeredModules, which
// calls registerModules with an EMPTY config.Config — so no plugin is installed,
// no plugin-brought module enters the walk, and four plugins that DO bring
// modules were outside it. plugins/webpush held a push endpoint, two device keys
// and a customer id and declared none of them, with every gate green.
//
// # Why this audit is STATIC and does not install anything
//
// The obvious fix is to widen that walk by installing the plugins. It was tried
// and rejected on measurement: web-push alone refuses to install without a VAPID
// private key, a subject and a template directory, and error-otlp refuses without
// an endpoint. A gate that has to satisfy every plugin's configuration is a gate
// that breaks the day a plugin adds a required setting — and a gate that breaks
// for reasons unrelated to what it checks is a gate somebody deletes.
//
// So the declaration is read from the SOURCE instead: a personaldata.Holding is a
// composite literal with a Table and a Column, and the migrations say which
// columns exist. Nothing is constructed, nothing is configured, and the two
// directions are the same two the app-level audit checks.

// personColumnsInPlugins are the column names that hold a person, matched
// EXACTLY.
//
// The first twenty are the app-level audit's own list, copied rather than
// imported because that list lives in package app's tests. The last three are
// what D30 added, and they are the reason this audit exists:
//
//   - endpoint — a push service URL minted for ONE browser on ONE device and
//     UNIQUE in its table. It identifies a person the way a phone number does:
//     not by naming them, but by reaching them.
//   - p256dh and auth — that device's public key and its secret.
//
// Matching is EXACT and not by substring, which is not a detail. A substring rule
// was written first and it flagged webhook_delivery.endpoint_id (a receiver's
// foreign key) and webhook_delivery.event_name (a topic) as people. A rule that
// cries wolf on a foreign key is a rule somebody turns off.
//
// customer_id is deliberately NOT here, for the same reason the app-level list
// omits it: it is a pseudonymous identifier that appears as a foreign key
// throughout the schema, and requiring a declaration for every one of them would
// flag half the tables. A plugin may still DECLARE it — webpush does, because a
// controller answering "what do you hold about me" should hear about it — and
// declaring more than is required is not an error here.
var personColumnsInPlugins = []string{
	"address_1", "address_2", "author_name", "buyer_address", "buyer_email",
	"buyer_name", "buyer_tax_number", "buyer_tax_office", "email", "first_name",
	"last_name", "password_hash", "phone", "postal_code", "provider_identity",
	"seller_address", "seller_email", "seller_name", "seller_tax_number",
	"seller_tax_office",
	"endpoint", "p256dh", "auth",
}

// personColumnSuffixes are the endings that make a column personal whatever
// comes before them.
//
// Exact matching alone is not enough and that was measured: a mutation adding
// contact_email to a plugin produced NOTHING, because the exact list holds
// email, buyer_email and seller_email but not that one. A curated list of names
// is a list somebody has to keep, and the column it misses is always the next
// one somebody invents.
//
// Only two suffixes are here, and the ones left out are the point. "_name" would
// match webhook_delivery.event_name, which is a topic; "_id" would match
// webhook_delivery.endpoint_id, which is a receiver's foreign key. An audit that
// cries wolf on a foreign key is an audit somebody turns off, so the rule stops
// where the signal stops being unambiguous.
var personColumnSuffixes = []string{"_email", "_phone"}

// isPersonColumnInPlugin reports whether a column name holds a person.
func isPersonColumnInPlugin(column string) bool {
	lowered := strings.ToLower(column)
	if slices.Contains(personColumnsInPlugins, lowered) {
		return true
	}

	for _, suffix := range personColumnSuffixes {
		if strings.HasSuffix(lowered, suffix) {
			return true
		}
	}

	return false
}

// pluginColumnDefinition matches a column at the head of a line in a CREATE
// TABLE body. The name allows digits after the first character: the column this
// audit exists for is called p256dh, and a pattern without them silently skips it.
var pluginColumnDefinition = regexp.MustCompile(
	`(?im)^\s+([a-z_][a-z0-9_]*)\s+(text|boolean|timestamptz|integer|bigint|jsonb|uuid)\b`)

// pluginAddColumn matches the OTHER way a column arrives: ALTER TABLE ... ADD
// COLUMN.
//
// It was missing from the first version of this file, which parsed CREATE TABLE
// bodies alone — the same blind spot D30's own fix had to close in
// internal/arch/email_test.go hours earlier, reproduced here in new code. An
// existing plugin cannot grow a column any other way, so without this the audit
// could only ever see a person column that arrived with a brand new table.
// Caught by mutation: adding contact_email to webhookout with ALTER TABLE
// produced nothing at all.
var pluginAddColumn = regexp.MustCompile(
	`(?is)alter\s+table\s+([a-z_][a-z0-9_]*).*?add\s+column\s+(?:if\s+not\s+exists\s+)?([a-z_][a-z0-9_]*)`)

// pluginCreateTable picks the table name off a CREATE TABLE statement.
var pluginCreateTable = regexp.MustCompile(`(?i)create\s+table\s+(?:if\s+not\s+exists\s+)?([a-z_][a-z0-9_]*)`)

// pluginsWithNoPersonalData names a plugin that holds no person column at all,
// with what was checked.
//
// It exists so that "this plugin declares nothing" and "this plugin holds
// nothing" cannot look the same, which is the confusion D30 was made of. An entry
// here is a claim about the plugin's schema, and the audit below verifies it: a
// plugin listed here that GROWS a person column fails.
var pluginsWithNoPersonalData = map[string]string{
	"searchpg":     "indexes the catalog; its table holds a product id and a tsvector",
	"webhookout":   "holds receivers and delivery attempts, keyed by event and receiver",
	"paymentpaytr": "holds a provider's payment references, keyed by the payment session",
	"webpush":      "", // holds four, and declares them — see the audit below
}

// TestEveryPersonColumnInAPluginIsDeclared is the audit.
func TestEveryPersonColumnInAPluginIsDeclared(t *testing.T) {
	t.Parallel()

	schemas := pluginSchemas(t)
	declared := pluginDeclarations(t)

	require.NotEmpty(t, schemas,
		"no plugin schema was read; the migration scan has gone BLIND and this audit would "+
			"pass whatever a plugin held")
	// assert rather than require: this is true of TWO different failures — the
	// parser stopped finding holdings, or the one plugin that declares any
	// stopped declaring them — and letting the per-column report below run is
	// what tells a reader which. Today webpush is the only declarer, so removing
	// its holdings empties this map, and a message that only said "the scan is
	// blind" would send somebody to debug a parser that is working.
	assert.NotEmpty(t, declared,
		"no personaldata.Holding was found in ANY plugin. Either the declaration scan has "+
			"gone blind, or the plugin that declares them stopped; the per-column failures "+
			"below say which — if there are none, suspect the scanner.")

	for plugin, columns := range schemas {
		for _, key := range columns {
			_, column, _ := strings.Cut(key, ".")
			if !isPersonColumnInPlugin(column) {
				continue
			}

			assert.Containsf(t, declared[plugin], key,
				"the %s plugin holds %s and declares no such holding.\nA plugin's rows are "+
					"swept by the same data-subject answers a module's are, and an undeclared "+
					"column is invisible to BOTH: the disclosure lists every holder except this "+
					"one and the erasure sweeps every holder except this one, with nothing "+
					"raised either time (D30).\nDeclare it with a personaldata.Holding, or "+
					"rename the column if it does not hold a person.", plugin, key)
		}
	}

	for plugin, holdings := range declared {
		for _, key := range holdings {
			assert.Containsf(t, schemas[plugin], key,
				"the %s plugin declares %s and its migrations create no such column.\n"+
					"A declaration that points at nothing tells a controller to look where "+
					"there is nothing to find.", plugin, key)
		}
	}
}

// TestAPluginClaimingToHoldNobodyStillHoldsNobody keeps the exemption honest.
//
// Three plugins are recorded as holding no personal data. That is a claim about
// their schema and not a permission, so it is re-checked here: a plugin on that
// list which grows a person column fails, which is the moment somebody has to
// either declare it or explain the name.
func TestAPluginClaimingToHoldNobodyStillHoldsNobody(t *testing.T) {
	t.Parallel()

	schemas := pluginSchemas(t)

	for plugin, reason := range pluginsWithNoPersonalData {
		if reason == "" {
			continue
		}

		assert.Containsf(t, schemas, plugin,
			"pluginsWithNoPersonalData names %q and no plugin by that name has migrations; "+
				"the entry describes something that has moved or gone", plugin)

		for _, key := range schemas[plugin] {
			_, column, _ := strings.Cut(key, ".")

			assert.Falsef(t, isPersonColumnInPlugin(column),
				"the %s plugin is recorded as holding no personal data (%q) and now holds %s.\n"+
					"Either declare it with a personaldata.Holding and remove the entry, or say "+
					"here why that column does not hold a person.", plugin, reason, key)
		}
	}
}

// pluginSchemas returns every column each plugin's migrations create, as
// "table.column".
func pluginSchemas(t *testing.T) map[string][]string {
	t.Helper()

	out := map[string][]string{}
	root := filepath.Join(repoRoot, "plugins")

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".up.sql") {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}

		plugin := strings.Split(filepath.ToSlash(rel), "/")[0]

		// Comments first: these migrations explain every column above it, and the
		// word "customer_id" in prose is not a column.
		text := regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(string(body), "")

		for _, block := range strings.Split(text, "CREATE TABLE")[1:] {
			table := pluginCreateTable.FindStringSubmatch("CREATE TABLE" + block)
			if table == nil {
				continue
			}

			for _, match := range pluginColumnDefinition.FindAllStringSubmatch(block, -1) {
				key := table[1] + "." + match[1]
				if !slices.Contains(out[plugin], key) {
					out[plugin] = append(out[plugin], key)
				}
			}
		}

		// And the columns that arrive later, on a table that already exists.
		for _, statement := range strings.Split(text, ";") {
			match := pluginAddColumn.FindStringSubmatch(statement)
			if match == nil {
				continue
			}

			key := match[1] + "." + match[2]
			if !slices.Contains(out[plugin], key) {
				out[plugin] = append(out[plugin], key)
			}
		}

		return nil
	})
	require.NoError(t, err, "the plugin tree could not be walked for migrations")

	for plugin := range out {
		sort.Strings(out[plugin])
	}

	return out
}

// pluginDeclarations returns the holdings each plugin declares, read from the
// SOURCE rather than from a constructed module.
//
// A personaldata.Holding is a composite literal carrying Table and Column, so the
// declaration can be read without installing the plugin — which is the whole
// reason this audit is static. See the file header.
func pluginDeclarations(t *testing.T) map[string][]string {
	t.Helper()

	out := map[string][]string{}
	root := filepath.Join(repoRoot, "plugins")
	fset := token.NewFileSet()

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

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}

		plugin := strings.Split(filepath.ToSlash(rel), "/")[0]
		constants := stringConstants(parsed)

		ast.Inspect(parsed, func(node ast.Node) bool {
			composite, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}

			table, column := holdingTableColumn(composite, constants)
			if table == "" || column == "" {
				return true
			}

			key := table + "." + column
			if !slices.Contains(out[plugin], key) {
				out[plugin] = append(out[plugin], key)
			}

			return true
		})

		return nil
	})
	require.NoError(t, err, "the plugin tree could not be walked for declarations")

	for plugin := range out {
		sort.Strings(out[plugin])
	}

	return out
}

// holdingTableColumn reads the Table and Column of a personaldata.Holding
// literal, resolving a constant when the field is one.
func holdingTableColumn(
	lit *ast.CompositeLit, constants map[string]string,
) (table, column string) {

	for _, element := range lit.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}

		key, isIdent := pair.Key.(*ast.Ident)
		if !isIdent {
			continue
		}

		value, resolved := literalOrConstant(pair.Value, constants)
		if !resolved {
			continue
		}

		switch key.Name {
		case "Table":
			table = value
		case "Column":
			column = value
		}
	}

	return table, column
}

// literalOrConstant resolves a string literal, or a same-package constant that
// holds one.
func literalOrConstant(expr ast.Expr, constants map[string]string) (string, bool) {
	switch typed := expr.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(typed.Value)

		return value, err == nil
	case *ast.Ident:
		value, known := constants[typed.Name]

		return value, known
	}

	return "", false
}

// stringConstants indexes a file's string constants by name.
func stringConstants(file *ast.File) map[string]string {
	out := map[string]string{}

	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}

		for i, name := range spec.Names {
			if i >= len(spec.Values) {
				continue
			}

			literal, isLit := spec.Values[i].(*ast.BasicLit)
			if !isLit || literal.Kind != token.STRING {
				continue
			}

			if value, err := strconv.Unquote(literal.Value); err == nil {
				out[name.Name] = value
			}
		}

		return true
	})

	return out
}
