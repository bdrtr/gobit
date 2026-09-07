package webpush

import (
	"context"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
)

// This file is the gate for the declaration next to it, and it lives HERE for a
// measured reason rather than a stylistic one.
//
// internal/app/personaldata_test.go audits exactly this property across the
// module tree — that every person-shaped column is declared, and that every
// declaration names a column that exists. It cannot see this module. Its
// registry is built by registeredModules, which calls registerModules with an
// empty config.Config, so no plugin is installed and no plugin-brought module
// ever enters the walk. That is why a table holding a push endpoint, two device
// keys and a customer id sat undeclared until 2026-09-07 with every gate green.
//
// The same reasoning ADR 0018 used for this plugin's rollback test applies
// unchanged: what the arch gates cannot reach, the plugin carries itself.

// personColumnPattern is the shape of a column name that obviously holds a
// person, kept deliberately close to the one internal/app uses so that the two
// audits agree about what they are looking for.
var personColumnPattern = regexp.MustCompile(
	`(?i)(email|phone|name|address|customer_id|endpoint|p256dh|auth|ip_address|tax_number)`)

// columnPattern matches a column definition at the head of a line inside a
// CREATE TABLE body.
//
// The name allows DIGITS after the first character, and that is not cosmetic:
// the first version of this pattern was [a-z_]+ and it silently skipped p256dh,
// so the gate reported the declaration as naming a column the schema does not
// have. The scanner was wrong and the declaration was right — which is the
// failure a blindness guard is for, caught here by the other direction of the
// same test.
var columnPattern = regexp.MustCompile(`(?im)^\s{4}([a-z_][a-z0-9_]*)\s+(text|boolean|timestamptz|integer|bigint|jsonb)`)

// tableNamePattern picks the table name off the head of a CREATE TABLE block.
var tableNamePattern = regexp.MustCompile(`(?i)^\s*(?:if not exists\s+)?([a-z_][a-z0-9_]*)`)

// commentPattern blanks SQL line comments, so a column named in the prose above
// a definition is not counted as a definition.
var commentPattern = regexp.MustCompile(`(?m)--.*$`)

// TestTheDeclarationCoversEveryPersonColumnInTheSchema is the audit the app
// cannot run for this module.
//
// It checks BOTH directions, because each catches a different mistake: a
// declared column that does not exist sends a controller looking where there is
// nothing, and an undeclared person column is a holder that answers a
// data-subject request while leaving something out.
func TestTheDeclarationCoversEveryPersonColumnInTheSchema(t *testing.T) {
	t.Parallel()

	declared := map[string]string{}
	for _, holding := range newModule(moduleOptions{}).PersonalData().Holdings {
		declared[holding.Table+"."+holding.Column] = holding.Why
	}

	require.NotEmpty(t, declared,
		"this module declares nothing, and it holds a push endpoint, two device keys and a "+
			"customer id; an empty declaration here is the state ADR 0051 found")

	schema := schemaColumns(t)

	require.NotEmpty(t, schema,
		"no column was read out of this module's migrations; the schema scan has gone BLIND "+
			"and both directions below would pass on nothing")

	for key, why := range declared {
		assert.Containsf(t, schema, key,
			"the declaration names %s and the migrations create no such column.\nA declaration "+
				"that points at nothing tells a controller to look where there is nothing to "+
				"find. (%s)", key, why)
		assert.NotEmptyf(t, strings.TrimSpace(why),
			"%s is declared with an empty Why; a disclosure repeats that sentence to a person, "+
				"so a blank one is a holding nobody can explain", key)
	}

	for key := range schema {
		_, column, _ := strings.Cut(key, ".")
		if !personColumnPattern.MatchString(column) {
			continue
		}

		assert.Containsf(t, declared, key,
			"%s looks like it holds a person and this module does not declare it.\nAn "+
				"undeclared column is invisible to BOTH data-subject answers: the disclosure "+
				"lists every holder except this one, and the erasure sweeps every holder except "+
				"this one, with nothing raised either time. Declare it, or rename it if it does "+
				"not hold a person.", key)
	}
}

// TestErasingByEmailAloneReportsZeroRatherThanFailing pins the answer this
// holder gives to a handle it cannot be asked by.
//
// The table binds a device by customer id and holds no address. A subject
// carrying only an e-mail therefore matches nothing here, and the contract's
// requirement is that the holder still ANSWER — a sweep that got an error from
// one holder would report a partial erasure for a person this module simply has
// nothing about.
func TestErasingByEmailAloneReportsZeroRatherThanFailing(t *testing.T) {
	t.Parallel()

	result, err := newModule(moduleOptions{}).
		Erase(context.Background(), personaldata.Subject{Email: "someone@example.com"})

	require.NoError(t, err, "a handle this holder cannot be asked by is not a fault")
	assert.Equal(t, personaldata.Deleted, result.Outcome)
	assert.Zero(t, result.Rows)
	assert.NotEmpty(t, result.Why,
		"answering zero without saying WHICH handle this holder can be asked by leaves the "+
			"controller unable to tell 'nothing here' from 'not asked properly'")
}

// TestErasingBeforeRegisterIsAnErrorAndNotASilentSuccess is the other half.
//
// A module whose store was never wired has read nothing. Answering Deleted there
// would put this holder in the report as done, which is the false-completeness
// the erasure contract exists to prevent.
func TestErasingBeforeRegisterIsAnErrorAndNotASilentSuccess(t *testing.T) {
	t.Parallel()

	_, err := newModule(moduleOptions{}).
		Erase(context.Background(), personaldata.Subject{CustomerID: "cus_1"})

	require.Error(t, err,
		"an unwired holder must not report itself erased; the sweep would count it done")
}

// schemaColumns reads this module's migrations and returns every column as
// "table.column".
//
// It parses the migrations rather than a hand-written list for the reason the
// rest of this repository's schema audits do: a list is right on the day it is
// written, and the column this gate exists to catch is the one somebody adds
// tomorrow.
func schemaColumns(t *testing.T) map[string]bool {
	t.Helper()

	out := map[string]bool{}

	err := fs.WalkDir(migrationsRoot, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".up.sql") {
			return nil
		}

		body, readErr := fs.ReadFile(migrationsRoot, path)
		if readErr != nil {
			return readErr
		}

		// Comments go first: this plugin's migrations explain every column above
		// it, and the word "customer_id" in prose is not a column.
		text := commentPattern.ReplaceAllString(string(body), "")

		for _, block := range strings.Split(text, "CREATE TABLE")[1:] {
			table := tableNamePattern.FindStringSubmatch(block)
			if table == nil {
				continue
			}

			for _, match := range columnPattern.FindAllStringSubmatch(block, -1) {
				out[table[1]+"."+match[1]] = true
			}
		}

		return nil
	})
	require.NoError(t, err, "the migrations could not be walked")

	return out
}
