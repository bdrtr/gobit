package repository

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// TestWrapDBSeparatesSQLSTATEClasses proves that the error classification is
// made according to the SQLSTATE.
//
// Why it is tested here (INSIDE the package): some branches of this mapping
// cannot be triggered deliberately on a real server — producing a syntax error
// or an exhausted connection pool would make the test itself fragile. The
// classification is a pure function and can be tested COMPLETELY with a fake
// *pgconn.PgError; the integration test, where the codes are really produced,
// additionally verifies it on a live server.
func TestWrapDBSeparatesSQLSTATEClasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sqlstate   string
		constraint string
		kind       errors.Kind
		errCode    string
	}{
		// The 22xxx "data exception" class: every one of them is born from a
		// value the CLIENT sent, none of them is a server error.
		{name: "text did not fit the column", sqlstate: "22001", kind: errors.KindInvalid, errCode: CodeConstraintViolation},
		{name: "value could not be converted to the target type", sqlstate: "22P02", kind: errors.KindInvalid, errCode: CodeConstraintViolation},
		{name: "a NUL escape was put into jsonb", sqlstate: "22P05", kind: errors.KindInvalid, errCode: CodeConstraintViolation},
		{name: "byte with no counterpart in the encoding", sqlstate: "22021", kind: errors.KindInvalid, errCode: CodeConstraintViolation},
		{name: "division by zero", sqlstate: "22012", kind: errors.KindInvalid, errCode: CodeConstraintViolation},

		// The 23xxx integrity constraints.
		{
			name: "check violation", sqlstate: "23514", constraint: "auth_user_email_check",
			kind: errors.KindInvalid, errCode: CodeConstraintViolation,
		},
		{name: "foreign key violation", sqlstate: "23503", kind: errors.KindInvalid, errCode: CodeConstraintViolation},
		{name: "not null violation", sqlstate: "23502", kind: errors.KindInvalid, errCode: CodeConstraintViolation},
		{
			name: "uniqueness violation", sqlstate: "23505", constraint: IndexUserEmail,
			kind: errors.KindConflict, errCode: CodeDuplicate,
		},

		// Everything else is OUR fault and returns a 500 to the client.
		{name: "syntax error", sqlstate: "42601", kind: errors.KindInternal, errCode: CodeQueryFailed},
		{name: "connections exhausted", sqlstate: "53300", kind: errors.KindInternal, errCode: CodeQueryFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := wrapDB(&pgconn.PgError{Code: tc.sqlstate, ConstraintName: tc.constraint},
				"could not write record: %s", "apikey_1")

			assert.Equal(t, tc.kind, errors.KindOf(err), "SQLSTATE %s mapped to the wrong kind", tc.sqlstate)
			assert.Equal(t, tc.errCode, errors.CodeOf(err))
			assert.Contains(t, err.Error(), "apikey_1", "the formatted message must be preserved")
		})
	}
}

// TestWrapDBAppendsConstraintNameOnlyWhenPresent proves that the message does
// not end with a half-written "(constraint: )" suffix.
//
// On data exceptions the constraint name is empty; had the suffix been written
// unconditionally, whoever read the error would go looking for a constraint
// that does not exist.
func TestWrapDBAppendsConstraintNameOnlyWhenPresent(t *testing.T) {
	t.Parallel()

	named := wrapDB(&pgconn.PgError{Code: "23505", ConstraintName: IndexTokenHash}, "could not write")
	assert.Contains(t, named.Error(), "(constraint: "+IndexTokenHash+")")

	// The suffix is searched for and not the bare word "constraint": the error
	// CODE (auth_constraint_violation) is part of Error() and would match a
	// bare word on every path.
	unnamed := wrapDB(&pgconn.PgError{Code: "22001"}, "could not write")
	assert.NotContains(t, unnamed.Error(), "(constraint:")
}

// TestIdentityConflictIsNotCountedAsEmailConflict proves that a violation of
// the one-identity-per-user-per-provider rule is reported as a SEPARATE error.
//
// Had the two been reduced to a single code, the caller would say "this email
// is in use" and the user would go looking for a free email; yet what conflicts
// is not the email but the user's identity with that provider.
func TestIdentityConflictIsNotCountedAsEmailConflict(t *testing.T) {
	t.Parallel()

	err := classifyUserWrite(
		&pgconn.PgError{Code: "23505", ConstraintName: IndexIdentityUserProvider},
		"person@example.test", "could not create identity record")

	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, CodeDuplicate, errors.CodeOf(err))
	assert.NotContains(t, err.Error(), "person@example.test", "the error must not look like an email conflict")

	emailErr := classifyUserWrite(
		&pgconn.PgError{Code: "23505", ConstraintName: IndexIdentityProvider},
		"person@example.test", "could not create identity record")
	assert.Equal(t, CodeEmailTaken, errors.CodeOf(emailErr))
}

// TestWrapDBSwallowsNilError proves that a nil input returns nil; the callers
// rely on that contract and go through wrapDB on every path.
func TestWrapDBSwallowsNilError(t *testing.T) {
	t.Parallel()

	assert.NoError(t, wrapDB(nil, "irrelevant"))
}
