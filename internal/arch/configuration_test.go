package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/config"
)

// envExamplePath is the only document the operator learns the settings from.
//
// For an installer who does not read code, THIS is the LIST of the settings; a knob
// not written here does not exist for that installer.
const envExamplePath = ".env.example"

// composePath is the compose file that brings up the local development stack.
//
// Some of the variables in .env.example go not to the application but to THIS file
// (the Postgres port, the Redis password); they are looked for here as the second
// consumer.
const composePath = "deploy/docker-compose.yml"

// pluginsPath is the root of the plugin sources.
//
// The third consumer: the plugin settings (STRIPE_API_KEY, say) are read NOT in
// Config but in the plugin's own source — the core does not know the plugins.
const pluginsPath = "plugins"

// readmePath is the repository's narrative document.
const readmePath = "README.md"

// requiredSettingExampleValue is the value the fields NOT CARRYING an envDefault
// have to take in .env.example: EMPTY.
//
// The decision answers this question: how should a setting without a default look in
// the document? There were three options and two of them do harm.
//
//  1. Do not write it at all. REJECTED: JWT_SECRET and ADMIN_BOOTSTRAP_PASSWORD are
//     exactly the settings that must NOT have a default, that is, the ones the
//     operator MUST know about. If they are not in the document, an installation
//     going to production never hears of them.
//  2. Write an example value. REJECTED: this file is copied AS IT IS with
//     "cp .env.example .env". An example JWT_SECRET would produce a working
//     installation signing its tokens with a secret everybody knows;
//     ADMIN_BOOTSTRAP_PASSWORD would seed a real administrator account. In a
//     document that gets copied there is no such thing as an "example value", there
//     is only the REAL value.
//  3. Write the key and leave the value empty. CHOSEN: the operator sees the knob,
//     while whoever copies it falls into the behavior the code declares — a random
//     per-startup secret in development, startup stopping in a shared environment.
//
// The place for example values is a COMMENT line (see STRIPE_API_KEY and
// "openssl rand -base64 48"); a comment sets nothing even when it is copied.
const requiredSettingExampleValue = ""

// deliberateDivergences maps the settings deliberately written in .env.example
// differently from their default, from their names to their justifications.
//
// Every entry here is a DEBT: the operator reading the document learns the code's
// default wrongly. The justification has to explain that the price is worth paying;
// adding an entry without a justification would be silencing the test, which
// destroys the reason the test exists.
//
// The exemptions DO NOT ROT: the check below errors on an entry that no longer
// diverges too, and asks for the entry to be DELETED. Otherwise the list fills up
// over time with "it used to be different" records and covers a real divergence.
var deliberateDivergences = map[string]string{
	// The code's default is "json" and has to be: what reads the log in production is
	// not a human but a collector. .env.example, on the other hand, exists to be
	// COPIED, and whoever copies it looks at a terminal locally; there, single-line
	// JSON makes following an error message by eye impossible. The divergence does not
	// affect security and the comment in the file states the code's default openly, so
	// the operator does not learn it wrongly.
	"LOG_FORMAT": "terminal readability in local development; the production default stays json",
}

// composeSubstitutionRe catches the ${NAME} and ${NAME:-default} forms in the compose file.
//
// The leading (^|[^$]) group is MANDATORY: in compose the spelling $${NAME} is NOT a
// substitution but an escaped dollar passed to the container's own shell (healthcheck
// commands are written that way). Taking those for substitutions would mean counting
// a variable compose never reads as documented.
var composeSubstitutionRe = regexp.MustCompile(`(?m)(^|[^$])\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// upperCaseStringRe catches the UPPER CASE string constants in the Go source.
//
// Plugin setting names sit like that in the source (const apiKeySetting =
// "STRIPE_API_KEY"); because the name is hidden behind a constant, looking for the
// call site is not enough, the value itself is looked for.
var upperCaseStringRe = regexp.MustCompile(`"([A-Z][A-Z0-9_]*)"`)

// pluginsAssignmentRe catches the PLUGINS=... examples in the README.
var pluginsAssignmentRe = regexp.MustCompile(`PLUGINS=([A-Za-z0-9_,-]+)`)

// settingField is a single field of Config read from the environment.
type settingField struct {
	// name is the name of the environment variable (the env tag).
	name string
	// path is the Go field path; it says which field the error message points at.
	path string
	// def is the value of the envDefault tag.
	def string
	// hasDefault is whether the envDefault tag was FOUND. An empty default and no
	// default at all are separate things.
	hasDefault bool
}

// composeVariable is a single ${...} substitution in the compose file.
type composeVariable struct {
	// def is the fallback in the ${NAME:-default} form.
	def string
	// hasDefault is whether the fallback WAS WRITTEN. For a substitution without a
	// fallback no value comparison can be made, but it still has to be documented.
	hasDefault bool
}

// envAssignment is a single KEY=VALUE line in .env.example.
type envAssignment struct {
	// value is the value with its quotes stripped.
	value string
	// line is the 1-based line number in the file.
	line int
}

// configSettings walks the Config struct by reflection and returns every field read
// from the environment.
//
// Reflection is MANDATORY, not a hand-written list: the reason this test exists is
// to keep a setting added TOMORROW from staying undocumented. A hand-written list
// would not contain exactly that field and would apply the rule for today only.
//
// Nested structs and envPrefix are walked too. Config is a FLAT struct today, that
// is, these branches never run today; they are written nevertheless because if the
// settings are ever grouped (env/v11 supports it) a flat walk would silently skip
// that WHOLE group — and a silently skipped setting is the very thing this test
// exists to prevent.
func configSettings(t *testing.T) []settingField {
	t.Helper()

	var settings []settingField
	var gez func(tip reflect.Type, onek, path string)
	gez = func(tip reflect.Type, onek, path string) {
		for i := range tip.NumField() {
			field := tip.Field(i)
			if !field.IsExported() {
				continue
			}
			fieldType := field.Type
			for fieldType.Kind() == reflect.Pointer {
				fieldType = fieldType.Elem()
			}

			name, etiketVar := field.Tag.Lookup("env")
			if !etiketVar {
				// A STRUCT without an env tag is a node grouping settings; it is descended
				// into.
				if fieldType.Kind() == reflect.Struct {
					gez(fieldType, onek+field.Tag.Get("envPrefix"), path+field.Name+".")
					continue
				}
				// A plain field without an env tag cannot be filled from ANY environment
				// variable. Config's only job is to carry the environment; such a field is
				// either a forgotten tag or a knob nobody can set — both are faults.
				t.Errorf("the config.Config.%s%s field has NO env tag.\n"+
					"Config is filled only from the environment; an untagged field cannot be "+
					"set with any environment variable and is invisible to the operator.",
					path, field.Name)
				continue
			}

			def, hasDefault := field.Tag.Lookup("envDefault")
			settings = append(settings, settingField{
				name:       onek + name,
				path:       path + field.Name,
				def:        def,
				hasDefault: hasDefault,
			})
		}
	}
	gez(reflect.TypeOf(config.Config{}), "", "")

	require.NotEmpty(t, settings, "no field with an env tag was found in config.Config; the walk must be broken")
	return settings
}

// readEnvExample parses the assignments in .env.example with POSIX shell semantics.
//
// The file is loaded with "set -a; . ./.env", that is, it is a shell script: a line
// starting with '#' ASSIGNS nothing and the quotes are NOT part of the value. The
// parser imitates that; without it an example inside a comment (STRIPE_API_KEY, say)
// would be taken for a real setting.
func readEnvExample(t *testing.T) map[string]envAssignment {
	t.Helper()

	ham, err := os.ReadFile(filepath.Join(repoRoot, envExamplePath))
	require.NoError(t, err, "%s could not be read", envExamplePath)

	assignments := make(map[string]envAssignment)
	for i, line := range strings.Split(string(ham), "\n") {
		no := i + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, "export ")

		name, value, bulundu := strings.Cut(trimmed, "=")
		if !bulundu {
			t.Errorf("%s:%d: the line %q is not an assignment.\n"+
				"The file is loaded by a shell; a line that is not an assignment either does "+
				"nothing silently or RUNS as a command.", envExamplePath, no, trimmed)
			continue
		}
		name = strings.TrimSpace(name)

		// The shell counts everything after a '#' preceded by a space in an unquoted
		// value as a comment; the parser has to count it too so the value of a line with
		// a comment is not read wrongly.
		value = strings.TrimSpace(value)
		if !strings.HasPrefix(value, "'") && !strings.HasPrefix(value, `"`) {
			if yorum := strings.Index(value, " #"); yorum >= 0 {
				value = strings.TrimSpace(value[:yorum])
			}
		}
		value = stripQuotes(value)

		if previous, varmis := assignments[name]; varmis {
			t.Errorf("%s:%d: %s is assigned TWICE (the previous one: line %d).\n"+
				"In a shell the last one wins; the document, though, promises two values at "+
				"once. Delete the extra one.", envExamplePath, no, name, previous.line)
			continue
		}
		assignments[name] = envAssignment{value: value, line: no}
	}

	require.NotEmpty(t, assignments, "no assignment was found in %s", envExamplePath)
	return assignments
}

// stripQuotes removes a single layer of quotes wrapping the value.
func stripQuotes(value string) string {
	if len(value) < 2 {
		return value
	}
	ilk, son := value[0], value[len(value)-1]
	if ilk == son && (ilk == '\'' || ilk == '"') {
		return value[1 : len(value)-1]
	}
	return value
}

// composeVariables returns the substitutions in the compose file keyed by name.
func composeVariables(t *testing.T) map[string]composeVariable {
	t.Helper()

	ham, err := os.ReadFile(filepath.Join(repoRoot, composePath))
	require.NoError(t, err, "%s could not be read", composePath)

	variables := make(map[string]composeVariable)
	for _, e := range composeSubstitutionRe.FindAllStringSubmatch(string(ham), -1) {
		name := e[2]
		// The same variable can appear more than once (one with a fallback, one
		// without); the occurrence carrying the fallback is the decisive one.
		if strings.Contains(e[0], ":-") {
			variables[name] = composeVariable{def: e[3], hasDefault: true}
			continue
		}
		if _, varmis := variables[name]; !varmis {
			variables[name] = composeVariable{}
		}
	}

	require.NotEmpty(t, variables, "no ${...} substitution was found in %s", composePath)
	return variables
}

// pluginSettingNames returns the upper-case string constants appearing in the plugin source.
//
// Test files are left OUT: a setting name appearing only in a test means there is
// nobody reading it in production — and documenting a setting without a consumer is
// promising the operator a knob that does not work.
func pluginSettingNames(t *testing.T) map[string]bool {
	t.Helper()

	names := make(map[string]bool)
	root := filepath.Join(repoRoot, pluginsPath)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		ham, okumaHatasi := os.ReadFile(path)
		if okumaHatasi != nil {
			return okumaHatasi
		}
		for _, e := range upperCaseStringRe.FindAllStringSubmatch(string(ham), -1) {
			names[e[1]] = true
		}
		return nil
	})
	require.NoError(t, err, "%s could not be scanned", pluginsPath)
	return names
}

// pluginRegistryNames returns the names the plugins are used under in the PLUGINS list.
//
// The names are READ from the source, not written by hand: a new plugin's name
// escaping the check because it was not added here is exactly what the test is
// supposed to catch.
//
// What is looked for is NOT the package name or the directory name but the value of
// "const Name"; the two can deliberately diverge (the searchpg package's registry
// name is "search-pg") and PLUGINS only recognizes the registry name.
func pluginRegistryNames(t *testing.T) map[string]bool {
	t.Helper()

	names := make(map[string]bool)
	root := filepath.Join(repoRoot, pluginsPath)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, decl := range parsed.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 || value.Names[0].Name != "Name" || len(value.Values) != 1 {
					continue
				}
				if lit, ok := value.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					names[strings.Trim(lit.Value, `"`)] = true
				}
			}
		}
		return nil
	})
	require.NoError(t, err, "%s could not be scanned", pluginsPath)

	require.NotEmpty(t, names, "no plugin name was found under %s", pluginsPath)
	return names
}

// TestTheEnvExampleAgreesWithTheConfigDefaults verifies that the documentation and
// the default of every setting say the same thing.
//
// The invariant checked is this: for EVERY field of config.Config with an env tag,
// (a) its name appears in .env.example, and (b) the value there is THE SAME as the
// envDefault.
//
// This class had three realized instances in this repository and all three were
// harmful in the same way: .env.example said "the TWO limits below" while there were
// seven; GraphQL's five new limits had never been written down at all (that is, the
// operator did not even know about them); handler.go said "the ceiling is 64 KiB"
// while the real ceiling was 1 MiB. In all three the code was right — IT WAS THE
// DOCUMENT THAT WAS WRONG, and with a wrong document the person doing the
// installation decides wrongly despite the right code.
//
// The silence of the divergence is the real issue: a wrong default fails no test and
// produces no log line. Only, the operator has not set what they believed they set,
// and learns it only when the limit is exceeded — that is, in production.
func TestTheEnvExampleAgreesWithTheConfigDefaults(t *testing.T) {
	t.Parallel()

	settings := configSettings(t)
	assignments := readEnvExample(t)

	for _, ayar := range settings {
		atama, documented := assignments[ayar.name]
		if !documented {
			t.Errorf("the config.Config.%s setting (%s) is MISSING from %s.\n"+
				"A setting not written in the document does not exist for the operator: they "+
				"can learn neither its existence nor its default.",
				ayar.path, ayar.name, envExamplePath)
			continue
		}

		if gerekce, exempt := deliberateDivergences[ayar.name]; exempt {
			// The exemption has to be LIVE. If the entry is not deleted once the
			// divergence goes away, the list grows in a way that covers a real divergence
			// too.
			assert.NotEqual(t, ayar.def, atama.value,
				"there is a deliberate divergence record for %s (%q) but the value is now THE SAME as the default (%q).\n"+
					"DELETE the record from deliberateDivergences; a rotten exemption lets "+
					"tomorrow's real divergence through silently.",
				ayar.name, gerekce, ayar.def)
			continue
		}

		if !ayar.hasDefault {
			assert.Equal(t, requiredSettingExampleValue, atama.value,
				"%s:%d: the %s setting has NO envDefault, that is, it is a required setting; "+
					"its value in %s has to be EMPTY, %q was written.\n"+
					"This file is copied into .env as it is: an example secret written here "+
					"produces a working installation that signs with a secret everybody knows. "+
					"The place for an example value is a comment line.",
				envExamplePath, atama.line, ayar.name, envExamplePath, atama.value)
			continue
		}

		assert.Equal(t, ayar.def, atama.value,
			"%s:%d: the %s setting is %q in the document while the default of config.Config.%s is %q.\n"+
				"The document and the default have to say the same thing. If the divergence is "+
				"deliberate, add it to deliberateDivergences WITH ITS JUSTIFICATION; a silent "+
				"divergence is the operator learning wrongly.",
			envExamplePath, atama.line, ayar.name, atama.value, ayar.path, ayar.def)
	}
}

// TestNoVariableInTheEnvExampleIsOrphaned verifies that every variable in the
// document has a CONSUMER.
//
// The reverse direction is at least as important as the first: a deleted setting
// staying in the document is promising the operator a knob THAT DOES NOT WORK. The
// person turning that knob believes they set something, sees no error and the
// behavior does not change — that is the most expensive kind of fault to find.
//
// There are three legitimate consumers and all three are verified FROM THE SOURCE,
// not from a hand-written allow list:
//
//  1. A config.Config field — the application's own setting.
//  2. A deploy/docker-compose.yml substitution — the local stack's setting (the
//     Postgres port, the Redis password). These NEVER reach the application, but
//     compose reads .env too, so they have a place in the document.
//  3. A setting name under plugins/ — a plugin setting (STRIPE_API_KEY). The core
//     does not know the plugins (Principle 2.4), which is why these names have NO
//     counterpart in Config and must not have one.
//
// For the compose variables the value is compared as well: compose's own fallback is
// written in the ${NAME:-default} form, and if the document diverges from it the
// stack brought up by "make up" listens somewhere other than what the document says.
func TestNoVariableInTheEnvExampleIsOrphaned(t *testing.T) {
	t.Parallel()

	assignments := readEnvExample(t)
	ikameler := composeVariables(t)
	eklentiAdlari := pluginSettingNames(t)

	configAdlari := make(map[string]bool)
	for _, ayar := range configSettings(t) {
		configAdlari[ayar.name] = true
	}

	for name, atama := range assignments {
		switch {
		case configAdlari[name]:
			// Within the first test's scope; its value is compared there.
		case ikameler[name].hasDefault:
			assert.Equal(t, ikameler[name].def, atama.value,
				"%s:%d: %s is %q in the document while its fallback in %s is %q.\n"+
					"If the two diverge, the stack brought up with \"make up\" runs with a "+
					"configuration other than what the document describes.",
				envExamplePath, atama.line, name, atama.value, composePath,
				ikameler[name].def)
		case eklentiAdlari[name]:
			// A plugin setting; its value belongs to the plugin's own contract.
		default:
			t.Errorf("%s:%d: nobody READS the %s variable.\n"+
				"It has neither a field in config.Config, nor a substitution in %s, nor a "+
				"reader under %s. A variable that stands in the document but nobody reads "+
				"promises the operator a knob that does not work: they turn it, nothing "+
				"happens, and they get no error either.",
				envExamplePath, atama.line, name, composePath, pluginsPath)
		}
	}
}

// TestTheComposeVariablesAreDocumented verifies that every variable compose reads is
// written in .env.example.
//
// This is the compose-side counterpart of the first test and closes the same silent
// fault: .env.example already documents POSTGRES_PORT and REDIS_PORT, that is, the
// file's own convention is "compose variables are written here too". Writing four of
// them and not writing two produces an operator who believes those two are NOT
// SETTABLE — and somebody hitting a port conflict finds the remedy in editing the
// compose file, that is, in forking the repository.
func TestTheComposeVariablesAreDocumented(t *testing.T) {
	t.Parallel()

	variables := composeVariables(t)
	assignments := readEnvExample(t)

	for name := range variables {
		if _, documented := assignments[name]; !documented {
			t.Errorf("%s is read in %s but is MISSING from %s.\n"+
				"An undocumented compose variable is a setting the operator believes cannot "+
				"be set.", name, composePath, envExamplePath)
		}
	}
}

// TestThePluginNamesInTheDocsAreReal verifies that every plugin the README and
// .env.example mention by name is REALLY registered under that name.
//
// A plugin name can diverge in two places and the divergence WAS MEASURED: the
// package and directory name is "searchpg" while the registry name is "search-pg".
// PLUGINS only recognizes the registry name and the application DOES NOT COME UP
// under a name it does not recognize — that is, a wrong name copied from the
// documentation stops startup on the installation's first attempt.
//
// Both directions are checked. The forward direction: every PLUGINS=... name in the
// documentation has to be registered. The reverse direction: every registered plugin
// has to be MENTIONED in .env.example — a plugin that is written but announced
// nowhere is a capability without a consumer (in this repository the whole of Phase
// 8/9 was once written like that and never mounted).
func TestThePluginNamesInTheDocsAreReal(t *testing.T) {
	t.Parallel()

	kayitli := pluginRegistryNames(t)

	readme, err := os.ReadFile(filepath.Join(repoRoot, readmePath))
	require.NoError(t, err, "%s could not be read", readmePath)
	ortamOrnegi, err := os.ReadFile(filepath.Join(repoRoot, envExamplePath))
	require.NoError(t, err, "%s could not be read", envExamplePath)

	// The input of the forward direction is the PLUGINS=... examples, and that set
	// emptying out is SILENT: because the reverse direction (every registered plugin has
	// to be MENTIONED in the document) does a plain-text search, it keeps passing — that
	// is, on the day the spelling in the document drifts to a form like
	// "PLUGINS: search-pg", only the forward direction disappears and a copyable wrong
	// name is left unchecked.
	exampleCount := 0

	for _, doc := range []struct {
		name   string
		icerik string
	}{
		{readmePath, string(readme)},
		{envExamplePath, string(ortamOrnegi)},
	} {
		for _, e := range pluginsAssignmentRe.FindAllStringSubmatch(doc.icerik, -1) {
			exampleCount++
			for _, name := range strings.Split(e[1], ",") {
				assert.Contains(t, kayitli, name,
					"%s: the name %q in the PLUGINS=%s example is NOT registered.\n"+
						"The registered names are the \"const Name\" values in the plugin source; "+
						"they are NOT the package or directory name. An installation copying this "+
						"example stops at startup with an \"unknown plugin\" error.",
					doc.name, e[1], name)
			}
		}
	}

	require.Positive(t, exampleCount,
		"not a single PLUGINS=... example was found in %s or %s; the forward direction "+
			"must have gone BLIND.\n"+
			"If the spelling pattern in the documents changed (pluginsAssignmentRe no longer "+
			"matches), a WRONG name in a copyable example is left unchecked — and the "+
			"installation copying it stops at startup with \"unknown plugin\". Because the "+
			"reverse direction does a plain-text search, it DOES NOT SEE this loss.", readmePath, envExamplePath)

	for name := range kayitli {
		assert.Contains(t, string(ortamOrnegi), name,
			"the %q plugin is registered but is never MENTIONED in %s.\n"+
				"The PLUGINS section lists the recognized names; a plugin not written there "+
				"is a capability nobody can install.", name, envExamplePath)
	}
}

// testNameInDocsRe catches a Go test name mentioned in the README.
//
// The pattern is deliberately NARROW: only names starting with "Test" and continuing
// with a capital letter, with at least two capitalized parts. A looser pattern would
// also catch Turkish words like "Testler" and the test would drown in its own noise.
var testNameInDocsRe = regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9]*[A-Z][A-Za-z0-9]*\b`)

// TestTheTestsMentionedInTheDocsAreReal verifies that every Go test named in the
// README REALLY exists in the repository.
//
// # Why it exists
//
// The README lists the architectural invariants in a table BY TEST NAME: that is
// where the reader gets the answer to "where is this rule enforced". If the test is
// renamed or deleted, the table quietly starts LYING — the rule looks enforced and
// is not. This is the very class C fault of this repository (a godoc's promise
// diverging from the code's behavior), and because the table was written by hand
// verification it was exactly open to rot.
//
// # Why only the README
//
// The CHANGELOG describes history, and a test from the past not existing today is
// NORMAL: the record of a removed invariant is the record itself. The README, on the
// other hand, describes TODAY, which is why it is the one checked.
func TestTheTestsMentionedInTheDocsAreReal(t *testing.T) {
	t.Parallel()

	ham, err := os.ReadFile("../../README.md")
	require.NoError(t, err, "README.md could not be read")

	anilanlar := testNameInDocsRe.FindAllString(string(ham), -1)
	require.NotEmpty(t, anilanlar,
		"no test name was found in the README; the pattern may be broken — "+
			"a check that finds nothing stays green in a vacuum")

	mevcut := repositoryTestNames(t)

	gorulen := map[string]bool{}
	for _, name := range anilanlar {
		if gorulen[name] {
			continue
		}
		gorulen[name] = true

		if _, var_ := mevcut[name]; var_ {
			continue
		}
		// assert.Contains is DELIBERATELY not used: when map membership fails it prints
		// the WHOLE map (thousands of test names, ~32 KB) and drowns the real message.
		// The value of a check is in being readable when it fails.
		t.Errorf(
			"the README mentions the %q test but there is NO such test in the repository.\n"+
				"The table says where a rule is enforced; pointing at a test that does not "+
				"exist shows an unenforced rule as if it were enforced.\n"+
				"If the test was renamed, update the README too; "+
				"if it was removed, delete the line.", name)
	}
}

// repositoryTestNames collects the names of all the Go test functions in the repository.
//
// Build tags are IGNORED: parsing is independent of the tags and tests behind the
// integration/smoke tags can be mentioned in the README too. Had the tags been
// looked at, the check would count those names as "missing" in an untagged run.
func repositoryTestNames(t *testing.T) map[string]struct{} {
	t.Helper()

	names := map[string]struct{}{}
	fset := token.NewFileSet()

	err := filepath.WalkDir("../..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// bin/ holds compiled tools and .git/ the object store; neither is source and
			// walking them only spends time. testdata/, on the other hand, is the directory
			// the Go tools DO NOT WALK and can deliberately contain files that cannot be
			// parsed.
			if name := d.Name(); name == ".git" || name == "bin" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		// A parse error is NOT SWALLOWED. Had it been, the tests in a single broken file
		// would silently be counted as "missing" and the check would stay green even
		// though the README points at them — that is, the check itself would be an
		// instance of the class it is trying to close.
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, tanim := range file.Decls {
			fn, ok := tanim.(*ast.FuncDecl)
			if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				names[fn.Name.Name] = struct{}{}
			}
		}
		return nil
	})
	require.NoError(t, err, "repo gezilemedi")
	require.NotEmpty(t, names, "no test was found in the repository")

	return names
}

// catalogPath is the composition root file holding the installer's plugin map.
const catalogPath = "internal/app/setup.go"

// catalogVariable is the name of that map.
const catalogVariable = "pluginCatalog"

// installablePluginNames returns the plugin names the BINARY can actually
// install, read from the composition root's catalog.
//
// The distinction from [pluginRegistryNames] is the whole point of this helper
// and it was paid for. That function derives names by parsing "const Name" out
// of the plugins tree, which answers "does a package by this name exist" — and
// [TestThePluginNamesInTheDocsAreReal] asks its two questions in those terms:
// is a documented name declared somewhere, and is a declared name documented.
// Neither is the question the person installing gobit asks, which is whether
// the binary KNOWS the name. On 2026-09-06 the difference was measured rather
// than imagined: plugins/webhookout compiled, carried unit, integration and end
// to end tests that were observed passing, and was described in the environment
// example under a copyable name — and it was absent from this map, the only one
// the installer consults, so copying that line stopped the boot with "unknown
// plugin". "go list -deps ./cmd/server" named eight plugins and not that one:
// the package was not compiled into the binary at all, and its migration was
// outside the migrate surface with it.
//
// The keys are resolved through the file's own import table rather than by
// assuming the package identifier matches the directory: an aliased import
// would otherwise be read as a plugin that does not exist.
func installablePluginNames(t *testing.T) map[string]bool {
	t.Helper()

	path := filepath.Join(repoRoot, catalogPath)
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err, "%s could not be parsed", catalogPath)

	imports := make(map[string]string)
	for _, spec := range parsed.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		local := importBase(importPath)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		imports[local] = importPath
	}

	declared := pluginNameByImportPath(t)

	names := make(map[string]bool)
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != catalogVariable {
				continue
			}
			require.Len(t, value.Values, 1, "%s must be a single map literal", catalogVariable)
			lit, ok := value.Values[0].(*ast.CompositeLit)
			require.True(t, ok, "%s must be a composite literal", catalogVariable)

			for _, elt := range lit.Elts {
				pair, ok := elt.(*ast.KeyValueExpr)
				require.True(t, ok, "every %s entry must be a key/value pair", catalogVariable)
				sel, ok := pair.Key.(*ast.SelectorExpr)
				require.True(t, ok,
					"every %s key must be a package-qualified constant; a literal string here would "+
						"let the catalog and the plugin disagree silently", catalogVariable)
				pkg, ok := sel.X.(*ast.Ident)
				require.True(t, ok, "unresolvable %s key", catalogVariable)
				require.Equal(t, "Name", sel.Sel.Name,
					"every %s key must be the plugin's own Name constant", catalogVariable)

				importPath, known := imports[pkg.Name]
				require.True(t, known, "%s is not imported by %s", pkg.Name, catalogPath)
				name, found := declared[importPath]
				require.True(t, found,
					"%s.Name is in the catalog but %s declares no Name constant", pkg.Name, importPath)
				names[name] = true
			}
		}
	}

	require.NotEmpty(t, names, "no plugin was found in %s", catalogVariable)
	return names
}

// pluginNameByImportPath maps a plugin package's import path to its Name value.
func pluginNameByImportPath(t *testing.T) map[string]string {
	t.Helper()

	out := make(map[string]string)
	root := filepath.Join(repoRoot, pluginsPath)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, decl := range parsed.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 || value.Names[0].Name != "Name" || len(value.Values) != 1 {
					continue
				}
				lit, ok := value.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				rel, relErr := filepath.Rel(repoRoot, filepath.Dir(path))
				if relErr != nil {
					return relErr
				}
				out[modulePath+"/"+filepath.ToSlash(rel)] = strings.Trim(lit.Value, `"`)
			}
		}
		return nil
	})
	require.NoError(t, err, "%s could not be scanned", pluginsPath)

	return out
}

// importBase is filepath.Base for a slash-separated import path.
//
// filepath.Base would be wrong on a platform whose separator is not a slash,
// and an import path is always slash-separated regardless of the host.
func importBase(importPath string) string {
	if i := strings.LastIndex(importPath, "/"); i >= 0 {
		return importPath[i+1:]
	}

	return importPath
}

// TestEveryPluginIsInstallable verifies that every plugin this repository ships
// can actually be switched on.
//
// The rule is one sentence: a package under the plugins tree that declares a
// Name constant must appear in the composition root's catalog. The catalog is
// the only map the installer consults, so a plugin missing from it is not
// merely undocumented — it is not compiled into the binary at all, and its
// migration is outside the migrate surface with it.
//
// # Why this is a SEPARATE test from the documentation one
//
// [TestThePluginNamesInTheDocsAreReal] already walks both directions between
// the documents and the source, and it was GREEN on the day a written, tested,
// documented plugin could not be installed. It could not have caught it: it
// derives the registered set by parsing the plugins tree, so both of its
// directions ask about the source and neither asks about the binary. The
// consequence was the sharpest available — the environment example carried a
// copyable line naming the plugin, and copying it produced precisely the
// startup failure that test's own godoc describes itself as preventing.
//
// The lesson is not about plugins. It is that a gate deriving BOTH sides of a
// comparison from the same place proves they agree with each other and nothing
// about the world. See [installablePluginNames] for where the two sides come
// from here.
func TestEveryPluginIsInstallable(t *testing.T) {
	t.Parallel()

	declared := pluginRegistryNames(t)
	installable := installablePluginNames(t)

	for name := range declared {
		assert.True(t, installable[name],
			"the %q plugin declares a Name but is absent from %s in %s.\n"+
				"That map is the only one the installer reads, so PLUGINS=%s stops the boot with "+
				"\"unknown plugin\" — and because nothing imports the package, it is not compiled "+
				"into the binary and its migration never reaches the migrate surface either.\n"+
				"Add it to the catalog, or delete the plugin: a plugin nobody can switch on is a "+
				"capability with no consumer.",
			name, catalogVariable, catalogPath, name)
	}

	for name := range installable {
		assert.True(t, declared[name],
			"%s in %s offers the %q plugin, but no package under %s declares that Name.\n"+
				"The catalog and the plugin have drifted apart; one of them is wrong.",
			catalogVariable, catalogPath, name, pluginsPath)
	}
}
