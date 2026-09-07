package audit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/audit"
	"github.com/bdrtr/gobit/core/errors"
)

// TestAnEntryWithNoMethodOrPathIsRefusedBeforeTheDatabaseIsTouched pins the one
// refusal [audit.Store.Write] makes on its own, and pins that it makes it FIRST.
//
// The schema refuses an empty method or path as well (audit_log_method_not_empty
// and audit_log_path_not_empty), so at first sight the Go guard looks like a
// duplicate that could be deleted. It is not, and the difference is what the
// caller is told. A CHECK violation comes back as an INTERNAL error naming a
// constraint — the shape of "the database is broken" — while the entry being
// incomplete is the CALLER's fault and comes back as INVALID. The middleware
// logs whatever it gets and serves the request either way, so
// the only reader of that distinction is the operator staring at the log line
// after an admin write left no trail; sending them to the database when the
// wiring above them handed over a blank method wastes the one clue they have.
// The middleware that produces these entries is
// [github.com/bdrtr/gobit/core/http.Audit].
//
// The store is built over a NIL pool ON PURPOSE, and that is what makes this a
// test of the ORDER rather than of the message. A *pgxpool.Pool that is nil
// panics the moment Exec touches it, so a guard moved below the INSERT does not
// merely return a different error here — the test blows up. The final assertion
// is the control that gives the other three their meaning: it shows that
// reaching the INSERT with this store really does panic, so the three entries
// that returned an error cannot have reached it.
func TestAnEntryWithNoMethodOrPathIsRefusedBeforeTheDatabaseIsTouched(t *testing.T) {
	t.Parallel()

	store := audit.NewStore(nil)

	cases := []struct {
		name  string
		entry audit.Entry
	}{
		{"neither", audit.Entry{ActorID: "usr_1", ActorKind: "user", Status: 200}},
		{"no method", audit.Entry{ActorID: "usr_1", ActorKind: "user", Path: "/admin/v1/products", Status: 200}},
		{"no path", audit.Entry{ActorID: "usr_1", ActorKind: "user", Method: "POST", Status: 200}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := store.Write(t.Context(), "aud_1", tc.entry)

			require.Error(t, err, "an entry with %s cannot be recorded; there is nothing to look up later", tc.name)
			assert.True(t, errors.IsInvalid(err),
				"an incomplete entry is the CALLER's fault, not the database's; it must not read as an outage")
			assert.Equal(t, audit.CodeWriteFailed, errors.CodeOf(err),
				"the operator greps the log for one code when a write left no trail")
		})
	}

	assert.Panics(t, func() {
		_ = store.Write(t.Context(), "aud_1", audit.Entry{Method: "POST", Path: "/admin/v1/products", Status: 200})
	}, "the control for the three cases above: a complete entry DOES reach the nil pool and blows up, "+
		"so the entries that came back with an error were stopped before the INSERT")
}
