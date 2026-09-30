package schemaaudit_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// TestTheReaderReplaysTheMigrationsInOrder holds the reader to the statements
// an audit depends on: a column created, one added, one dropped after, several
// added by one statement, a CHECK holding a semicolon, and a table dropped.
func TestTheReaderReplaysTheMigrationsInOrder(t *testing.T) {
	t.Parallel()

	schema := schemaaudit.Replay("CREATE TABLE IF NOT EXISTS planted (\n" +
		"    id TEXT PRIMARY KEY,\n" +
		"    gone TEXT,\n" +
		"    label TEXT DEFAULT ')',\n" +
		"    price NUMERIC(10, 2),\n" +
		"    CONSTRAINT planted_check CHECK (id <> ';')\n" +
		");\n" +
		"ALTER TABLE planted DROP COLUMN IF EXISTS gone;\n" +
		"ALTER TABLE planted ADD COLUMN IF NOT EXISTS note TEXT;\n" +
		"ALTER TABLE planted\n    ADD COLUMN one TEXT,\n    ADD COLUMN two TEXT,\n" +
		"    ADD CONSTRAINT planted_two CHECK (two <> '');\n" +
		"CREATE TABLE dropped (id TEXT);\nDROP TABLE IF EXISTS dropped;\n")

	assert.Equal(t, map[string]map[string]bool{
		"planted": {"id": true, "label": true, "price": true, "note": true, "one": true, "two": true},
	}, schema)
}

// TestCoverFailsEachWayTheDeclarationCanComeApart runs the comparison on a
// schema written for it and requires it to fail on each of its six faults and
// pass on none of them.
func TestCoverFailsEachWayTheDeclarationCanComeApart(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"000001_init.up.sql": {Data: []byte("CREATE TABLE people (\n    id TEXT PRIMARY KEY,\n    email TEXT\n);\n")},
		"000002_more.up.sql": {Data: []byte("ALTER TABLE people ADD COLUMN phone TEXT;\n")},
	}
	holding := func(column string) personaldata.Holding {
		return personaldata.Holding{Table: "people", Column: column, Kind: personaldata.Named, Why: "x"}
	}
	whole := personaldata.Declaration{Holdings: []personaldata.Holding{holding("email"), holding("phone")}}
	judged := map[string][]string{"people": {"id"}}

	cases := map[string]struct {
		declaration personaldata.Declaration
		notPersonal map[string][]string
		fails       bool
	}{
		"everything judged once": {whole, judged, false},
		"an added column undeclared": {
			personaldata.Declaration{Holdings: []personaldata.Holding{holding("email")}}, judged, true,
		},
		"a column both declared and judged": {whole, map[string][]string{"people": {"id", "email"}}, true},
		"a table judged nowhere": {
			personaldata.Declaration{Holdings: append(whole.Holdings, holding("id"))}, map[string][]string{}, true,
		},
		"a judged column that is not there": {whole, map[string][]string{"people": {"id", "fax"}}, true},
		"a judged table that is not there": {
			whole, map[string][]string{"people": {"id"}, "ghosts": {"id"}}, true,
		},
		"a declared column that is not there": {
			personaldata.Declaration{Holdings: append(whole.Holdings, holding("fax"))}, judged, true,
		},
		"a column declared twice": {
			personaldata.Declaration{Holdings: append(whole.Holdings, holding("email"))}, judged, true,
		},
	}
	for name, c := range cases {
		probe := &failRecorder{}
		schemaaudit.Cover(probe, migrations, c.declaration, c.notPersonal)
		assert.Equal(t, c.fails, probe.failed, name)
	}
}

// failRecorder is a testing.TB that records a failure instead of reporting it,
// so the comparison can be run on a fault and asked whether it noticed. The
// embedded TB is nil: a call the comparison was not expected to make panics
// rather than passing quietly.
type failRecorder struct {
	testing.TB
	failed bool
}

func (r *failRecorder) Helper() {}

func (r *failRecorder) Name() string { return "probe" }

func (r *failRecorder) Errorf(string, ...any) { r.failed = true }

func (r *failRecorder) FailNow() { r.failed = true }
