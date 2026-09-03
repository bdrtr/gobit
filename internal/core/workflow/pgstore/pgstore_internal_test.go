package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// TestNewExecutionIDFormat verifies the id's prefix and length contract.
func TestNewExecutionIDFormat(t *testing.T) {
	t.Parallel()

	id := newExecutionID(time.Now())

	assert.True(t, strings.HasPrefix(id, "wfx_"), "the id %q has to start with the wfx_ prefix", id)
	body := strings.TrimPrefix(id, idPrefix)
	assert.Len(t, body, idBodyLen, "the body has to be as long as the Base32 form of 16 bytes")
	assert.Equal(t, strings.ToUpper(body), body, "the Crockford Base32 alphabet is upper case")
	assert.NotContains(t, body, "I", "the Crockford alphabet has no I")
	assert.NotContains(t, body, "U", "the Crockford alphabet has no U")
}

// TestNewExecutionIDIsUnique verifies that ids produced in the same millisecond
// do not clash: uniqueness rests on 80 bits of randomness, not on the timestamp.
func TestNewExecutionIDIsUnique(t *testing.T) {
	t.Parallel()

	instant := time.Now()
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := newExecutionID(instant)
		_, repeat := seen[id]
		require.False(t, repeat, "the same id was produced twice: %s", id)
		seen[id] = struct{}{}
	}
}

// TestNewExecutionIDIsTimeOrdered verifies that the lexical order of the ids is
// the same as their time order. That would not hold if the timestamp were not
// first.
func TestNewExecutionIDIsTimeOrdered(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	previous := newExecutionID(base)
	for i := 1; i < 50; i++ {
		next := newExecutionID(base.Add(time.Duration(i) * time.Second))
		assert.Less(t, previous, next, "the id of the later instant has to be lexically greater")
		previous = next
	}
}

// TestNewExecutionIDWithAPastInstant verifies that a pre-1970 timestamp does not
// break the id (a negative millisecond is clamped to the floor).
func TestNewExecutionIDWithAPastInstant(t *testing.T) {
	t.Parallel()

	id := newExecutionID(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC))

	assert.True(t, strings.HasPrefix(id, idPrefix))
	assert.Len(t, strings.TrimPrefix(id, idPrefix), idBodyLen)
	// Because the timestamp is clamped to the floor the body starts with zeros.
	assert.True(t, strings.HasPrefix(strings.TrimPrefix(id, idPrefix), "000"),
		"a pre-1970 instant has to be clamped to zero")
}

// TestJSONParamTellsNullFromEmpty verifies how the JSON fields tell
// NULL / empty / JSON-null apart. Without that distinction "no value" and "the
// value is null" get mixed up.
func TestJSONParamTellsNullFromEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input json.RawMessage
		want  any
	}{
		{"nil becomes SQL NULL", nil, nil},
		{"an empty slice becomes SQL NULL", json.RawMessage{}, nil},
		{"whitespace only becomes SQL NULL", json.RawMessage("  \n\t "), nil},
		{"a JSON null value is preserved", json.RawMessage(`null`), []byte(`null`)},
		{"an object is preserved", json.RawMessage(`{"a":1}`), []byte(`{"a":1}`)},
		{"an array is preserved", json.RawMessage(`[1,2]`), []byte(`[1,2]`)},
		{"a number is preserved", json.RawMessage(`0`), []byte(`0`)},
		{"an empty string value is preserved", json.RawMessage(`""`), []byte(`""`)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := jsonParam(tc.input, "input")

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestJSONParamRejectsInvalidJSON verifies that invalid JSON is turned into a
// typed error WITHOUT GOING to the database.
func TestJSONParamRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	for _, input := range []string{`{`, `{"a":}`, `hello`, `{"a":1},`} {
		got, err := jsonParam(json.RawMessage(input), "input")

		require.Error(t, err, "%q has to count as invalid", input)
		assert.Nil(t, got)
		assert.True(t, coreerrors.IsInvalid(err), "the error has to be of the Invalid class: %v", err)
		assert.Contains(t, err.Error(), "input")
	}
}

// TestJSONParamRejectsWhatJSONBRefuses verifies that bodies which PASS
// json.Valid but JSONB does not accept are turned into Invalid before reaching
// the store.
//
// Without the check the error would come back from the driver (SQLSTATE 22P05 /
// 22021) and fall to KindInternal: an HTTP 500 produced by the caller's own
// data — and, because Create failed, the record opened with that idempotency key
// could never be completed.
func TestJSONParamRejectsWhatJSONBRefuses(t *testing.T) {
	t.Parallel()

	// nulEscape is backslash + u0000; written into the source directly the
	// compiler would turn it into a real NUL character and the case under test
	// would be gone.
	tests := []struct {
		name  string
		input string
	}{
		{"a NUL escape in a value", `{"x":"a` + nulEscape + `b"}`},
		{"a NUL escape in a key", `{"` + nulEscape + `":1}`},
		{"a NUL escape in a root string", `"` + nulEscape + `"`},
		{"a NUL escape in an array", `["` + nulEscape + `"]`},
		{"an invalid UTF-8 sequence", "{\"x\":\"\xff\xfe\"}"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.True(t, json.Valid([]byte(tc.input)),
				"for the case to mean anything the body has to PASS json.Valid")

			got, err := jsonParam(json.RawMessage(tc.input), "input")

			require.Error(t, err)
			assert.Nil(t, got)
			assert.True(t, coreerrors.IsInvalid(err), "the error has to be of the Invalid class: %v", err)
			assert.Equal(t, CodeInvalid, coreerrors.CodeOf(err))
			assert.Contains(t, err.Error(), "input")
		})
	}
}

// TestJSONParamWithAnEscapedBackslash verifies that the NUL-escape check does
// not produce a FALSE POSITIVE: a u0000 following an escaped backslash is not an
// escape, it is ordinary text and JSONB stores it without trouble.
func TestJSONParamWithAnEscapedBackslash(t *testing.T) {
	t.Parallel()

	// Two backslashes + u0000: in JSON that is the TEXT backslash + u0000 (six
	// characters).
	body := `{"x":"a` + nulEscape[:1] + nulEscape + `b"}`
	require.True(t, json.Valid([]byte(body)))

	got, err := jsonParam(json.RawMessage(body), "input")

	require.NoError(t, err, "an escaped backslash is not a NUL escape")
	assert.Equal(t, []byte(body), got)
}

// TestJSONValueTellsNullApart verifies that on the read path SQL NULL and JSON
// null are told apart.
func TestJSONValueTellsNullApart(t *testing.T) {
	t.Parallel()

	assert.Nil(t, jsonValue(nil), "SQL NULL has to be a nil RawMessage")
	assert.Equal(t, json.RawMessage(`null`), jsonValue([]byte(`null`)),
		"a JSONB null value has to come back as the bytes \"null\"")
	assert.Equal(t, json.RawMessage(`{"a": 1}`), jsonValue([]byte(`{"a": 1}`)))
}

// TestKeyParam verifies that only the EMPTY STRING counts as "no key" and that a
// filled key is stored AS IT IS.
func TestKeyParam(t *testing.T) {
	t.Parallel()

	empty, err := keyParam("")
	require.NoError(t, err)
	assert.Nil(t, empty, "an empty key has to be NULL")

	got, err := keyParam(" ord_1 ")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, " ord_1 ", *got, "a filled key has to be stored untrimmed")
}

// TestKeyParamRefusesAWhitespaceKey verifies that a key made of whitespace only
// is not turned into NULL SILENTLY.
//
// The silent conversion would remove the repeat protection the caller asked for
// without any warning: because NULLs do not clash in the partial unique index, a
// second and a third execution would open with the same key and the work would
// be done twice.
func TestKeyParamRefusesAWhitespaceKey(t *testing.T) {
	t.Parallel()

	for _, input := range []string{" ", "   ", "\t", "\n", " \t\n "} {
		got, err := keyParam(input)

		require.Errorf(t, err, "the key %q has to be refused", input)
		assert.Nil(t, got)
		assert.True(t, coreerrors.IsInvalid(err), "the error has to be of the Invalid class: %v", err)
		assert.Equal(t, CodeInvalid, coreerrors.CodeOf(err))
	}
}

// TestKeyParamLimits verifies that the length and encoding limits are applied on
// the write path.
func TestKeyParamLimits(t *testing.T) {
	t.Parallel()

	exact, err := keyParam(strings.Repeat("k", maxKeyLen))
	require.NoError(t, err, "a key exactly at the limit has to be accepted")
	assert.NotNil(t, exact)

	_, err = keyParam(strings.Repeat("k", maxKeyLen+1))
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err))

	_, err = keyParam("ord\x00_1")
	require.Error(t, err, "a NUL byte cannot be written into a TEXT column")
	assert.True(t, coreerrors.IsInvalid(err))
}

// TestKeyValue verifies that a NULL key comes back as an empty string.
func TestKeyValue(t *testing.T) {
	t.Parallel()

	assert.Empty(t, keyValue(nil))

	value := "ord_1"
	assert.Equal(t, "ord_1", keyValue(&value))
}

// TestTimeParam verifies that a zero time turns into NULL and a filled time into
// UTC.
func TestTimeParam(t *testing.T) {
	t.Parallel()

	assert.Nil(t, timeParam(time.Time{}), "a zero time has to be NULL")

	zone := time.FixedZone("UTC+3", 3*60*60)
	instant := time.Date(2026, 8, 23, 15, 4, 5, 0, zone)
	got := timeParam(instant)
	require.NotNil(t, got)
	assert.Equal(t, time.UTC, got.Location(), "the time has to be moved to UTC (plan Section 8)")
	assert.True(t, got.Equal(instant))
}

// TestTimeValue verifies that a NULL time comes back as the zero time.Time.
func TestTimeValue(t *testing.T) {
	t.Parallel()

	assert.True(t, timeValue(nil).IsZero())

	instant := time.Date(2026, 8, 23, 15, 4, 5, 0, time.FixedZone("UTC+3", 3*60*60))
	got := timeValue(&instant)
	assert.Equal(t, time.UTC, got.Location())
	assert.True(t, got.Equal(instant))
}

// TestRequireText exercises the required-text validation.
func TestRequireText(t *testing.T) {
	t.Parallel()

	got, err := requireText("  order  ", "the workflow name", maxNameLen)
	require.NoError(t, err)
	assert.Equal(t, "order", got, "the value has to come back trimmed")

	for _, input := range []string{"", "   ", "\t\n"} {
		_, err = requireText(input, "the workflow name", maxNameLen)
		require.Error(t, err)
		assert.True(t, coreerrors.IsInvalid(err))
		assert.Contains(t, err.Error(), "the workflow name")
	}

	_, err = requireText(strings.Repeat("a", maxNameLen+1), "the workflow name", maxNameLen)
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "a value over the limit has to be Invalid")

	_, err = requireText(strings.Repeat("a", maxNameLen), "the workflow name", maxNameLen)
	assert.NoError(t, err, "a value exactly at the limit has to be accepted")
}

// TestRequireTextWithUnwritableBytes verifies that bytes which cannot be written
// into a TEXT column are turned into Invalid WITHOUT GOING to the database.
//
// Without the check the driver would return SQLSTATE 22021 and the error would
// fall to KindInternal: a failure born of the caller's data would show up as a
// 500.
func TestRequireTextWithUnwritableBytes(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"a NUL byte":                "complete\x00order",
		"invalid UTF-8":             "complete\xff",
		"a NUL after trimming":      "  \x00  ",
		"nothing but invalid UTF-8": "\xc3\x28",
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := requireText(input, "the workflow name", maxNameLen)

			require.Error(t, err)
			assert.True(t, coreerrors.IsInvalid(err), "the error has to be of the Invalid class: %v", err)
			assert.Equal(t, CodeInvalid, coreerrors.CodeOf(err))
		})
	}
}

// TestSafeText verifies that diagnostic text is MADE WRITABLE rather than
// REFUSED.
//
// The distinction is deliberate: identifying fields (requireText) are refused,
// a failure description is cleaned. Not being able to write the terminal state
// because of its description would leave the record "running" forever and make
// the idempotency key unusable.
func TestSafeText(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "the step blew up", safeText("the step blew up"), "clean text must not change")
	assert.Empty(t, safeText(""))
	assert.Equal(t, "ab", safeText("a\x00b"), "a NUL byte has to be dropped")
	assert.Equal(t, "ab", safeText("a\xffb"), "an invalid UTF-8 sequence has to be dropped")
	assert.Equal(t, "ab", safeText("a\xff\x00b"), "the two together have to be cleaned as well")

	cleaned := safeText("the stock\x00 service \xff did not answer")
	assert.True(t, isStorableText(cleaned), "the result has to be writable into a TEXT column: %q", cleaned)
	assert.Contains(t, cleaned, "stock", "the readable part has to be preserved")
	assert.Contains(t, cleaned, "did not answer")
}

// TestRequireCount exercises the counter validation; an INTEGER column overflow
// has to be caught before reaching the database.
func TestRequireCount(t *testing.T) {
	t.Parallel()

	got, err := requireCount(3, "the step index")
	require.NoError(t, err)
	assert.Equal(t, int32(3), got)

	got, err = requireCount(0, "the step index")
	require.NoError(t, err)
	assert.Equal(t, int32(0), got)

	_, err = requireCount(-1, "the step index")
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err))

	_, err = requireCount(math.MaxInt32+1, "the step index")
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err))

	_, err = requireCount(math.MaxInt32, "the step index")
	assert.NoError(t, err, "a value at the int32 limit has to be accepted")
}

// TestCreateErrorMapsConflicts verifies that a uniqueness violation is turned
// into a separate CODE and a separate CLASS depending on which constraint it
// came from.
//
// The class distinction is part of the contract: the engine reads Conflict as
// "this request was made before" and goes down the replay path. On an id clash
// the key is not in the store at all; had Conflict been returned the engine
// would look for a key it never asked about, FindByIdempotencyKey would return
// NotFound and the caller would never see the real failure ("the id is already
// in use") in the message.
func TestCreateErrorMapsConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		constraint string
		code       string
		kind       coreerrors.Kind
		contains   string
	}{
		{"the idempotency index", idempotencyIndex, CodeDuplicateKey, coreerrors.KindConflict, "idempotency"},
		{"the primary key", executionsPKConstraint, CodeDuplicateID, coreerrors.KindInvalid, "already exists"},
		{"an unrecognized constraint", "other_constraint", CodeConflict, coreerrors.KindInternal, "other_constraint"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := &pgconn.PgError{Code: uniqueViolation, ConstraintName: tc.constraint}

			err := createError(raw, "wfx_1", "order", "ord_1")

			require.Error(t, err)
			assert.Equal(t, tc.kind, coreerrors.KindOf(err), "the class mapping: %v", err)
			assert.Equal(t, tc.code, coreerrors.CodeOf(err))
			assert.Contains(t, err.Error(), tc.contains)
			assert.ErrorIs(t, err, raw, "the raw driver error has to stay in the chain")
		})
	}
}

// TestCreateErrorReservesConflictForIdempotency verifies that the Conflict class
// is reserved for the idempotency clash ALONE; the engine entering the replay
// path depends on it.
func TestCreateErrorReservesConflictForIdempotency(t *testing.T) {
	t.Parallel()

	for _, constraint := range []string{executionsPKConstraint, "other_constraint"} {
		raw := &pgconn.PgError{Code: uniqueViolation, ConstraintName: constraint}

		err := createError(raw, "wfx_1", "order", "ord_1")

		require.Error(t, err)
		assert.Falsef(t, coreerrors.IsConflict(err),
			"a %s violation MUST NOT be Conflict: the engine would take it for an idempotent repeat (%v)", constraint, err)
	}
}

// TestCreateErrorLeavesOtherFailuresAlone verifies that failures other than a
// uniqueness violation are NOT TURNED into a conflict.
func TestCreateErrorLeavesOtherFailuresAlone(t *testing.T) {
	t.Parallel()

	raw := &pgconn.PgError{Code: "42P01", Message: "relation does not exist"}

	err := createError(raw, "wfx_1", "order", "")

	require.Error(t, err)
	assert.False(t, coreerrors.IsConflict(err), "a missing-table error is not a conflict")
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err))
	assert.Equal(t, CodeQueryFailed, coreerrors.CodeOf(err))
}

// TestWrapDBClassifies verifies the classification of the driver errors.
func TestWrapDBClassifies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  error
		kind coreerrors.Kind
		code string
	}{
		{"nil", nil, coreerrors.KindInternal, ""},
		{"cancellation", context.Canceled, coreerrors.KindUnavailable, CodeCanceled},
		{"deadline exceeded", context.DeadlineExceeded, coreerrors.KindUnavailable, CodeCanceled},
		{"no rows", pgx.ErrNoRows, coreerrors.KindNotFound, CodeNotFound},
		{
			"a foreign key violation",
			&pgconn.PgError{Code: foreignKeyViolation, ConstraintName: "fk"},
			coreerrors.KindNotFound, CodeNotFound,
		},
		{
			"a CHECK violation",
			&pgconn.PgError{Code: checkViolation, ConstraintName: "chk_name"},
			coreerrors.KindInvalid, CodeInvalid,
		},
		{
			"a NUL byte in text",
			&pgconn.PgError{Code: notInRepertoire},
			coreerrors.KindInvalid, CodeInvalid,
		},
		{
			"an escape JSONB cannot convert",
			&pgconn.PgError{Code: untranslatableCharacter},
			coreerrors.KindInvalid, CodeInvalid,
		},
		{
			"text that could not be parsed into the target type",
			&pgconn.PgError{Code: invalidTextRepresentation},
			coreerrors.KindInvalid, CodeInvalid,
		},
		{"unknown", errors.New("a broken connection"), coreerrors.KindInternal, CodeQueryFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := wrapDB(tc.raw, CodeQueryFailed, "the operation failed")

			if tc.raw == nil {
				assert.NoError(t, err, "a nil error must not be wrapped")
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.kind, coreerrors.KindOf(err))
			assert.Equal(t, tc.code, coreerrors.CodeOf(err))
			assert.ErrorIs(t, err, tc.raw, "the raw error has to stay in the chain")
		})
	}
}

// TestWrapDBCarriesTheCheckConstraintName verifies that the constraint name
// appears in the message of a CHECK violation and that the caller's arguments
// are not disturbed.
func TestWrapDBCarriesTheCheckConstraintName(t *testing.T) {
	t.Parallel()

	args := []any{"wfx_1"}
	raw := &pgconn.PgError{Code: checkViolation, ConstraintName: "workflow_executions_status_not_blank"}

	err := wrapDB(raw, CodeQueryFailed, "%s could not be written", args...)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "wfx_1")
	assert.Contains(t, err.Error(), "workflow_executions_status_not_blank")
	assert.Equal(t, []any{"wfx_1"}, args, "the caller's argument slice must not change")
}

// TestScanTargetsBoundary verifies the execution/step boundary of the scan
// targets. If execColumnCount drifted, skipExecColumns would skip the wrong
// columns and the record read would be corrupted silently.
func TestScanTargetsBoundary(t *testing.T) {
	t.Parallel()

	var (
		row  execRow
		step stepRow
	)

	targets := scanTargets(&row, &step)

	require.Len(t, targets, 17, "a joined row carries 9 execution + 8 step columns")
	assert.Equal(t, &row.id, targets[0], "the first column is the execution id")
	assert.Equal(t, &row.updatedAt, targets[execColumnCount-1],
		"the last execution column has to be updated_at")
	assert.Equal(t, &step.index, targets[execColumnCount],
		"the step columns have to start exactly after that boundary")
}

// TestSkipExecColumnsSkipsTheExecutionOnly verifies the boundary of the target
// clearing: the execution columns are skipped while the step columns are scanned
// on EVERY row.
func TestSkipExecColumnsSkipsTheExecutionOnly(t *testing.T) {
	t.Parallel()

	var (
		row  execRow
		step stepRow
	)
	targets := scanTargets(&row, &step)
	stepTargets := append([]any(nil), targets[execColumnCount:]...)

	skipExecColumns(targets)

	for i, target := range targets[:execColumnCount] {
		assert.Nilf(t, target, "execution column %d must not be scanned after the first row", i)
	}
	assert.Equal(t, stepTargets, targets[execColumnCount:],
		"the targets of the step columns must not change")
}

// TestFoldRowsScansTheExecutionColumnsOnce verifies that the execution columns
// the LEFT JOIN carries AGAIN for every step are scanned on the FIRST row only.
//
// Were they scanned, pgx would allocate a fresh byte slice for input and output
// on every row and throw it away immediately; reading a record with a 256 KB
// input and eight steps allocated 2.17 MB instead of 0.28 MB (measured against
// real Postgres).
func TestFoldRowsScansTheExecutionColumnsOnce(t *testing.T) {
	t.Parallel()

	source := &fakeRows{t: t, stepCount: 3}

	exec, err := foldRows(source)

	require.NoError(t, err)
	require.NotNil(t, exec)
	require.Len(t, source.skipped, 3, "one row has to be scanned per step")

	assert.NotContains(t, source.skipped[0], true,
		"no column may be skipped on the first row: the execution is built from it")
	for row, skipped := range source.skipped[1:] {
		for i := range execColumnCount {
			assert.Truef(t, skipped[i],
				"execution column %d must not be scanned again on row %d", i, row+1)
		}
		for i := execColumnCount; i < len(skipped); i++ {
			assert.Falsef(t, skipped[i],
				"step column %d has to be scanned on row %d", i, row+1)
		}
	}

	// The skipping must not corrupt the record that is read.
	assert.Equal(t, "wfx_1", exec.ID)
	assert.Equal(t, json.RawMessage(`{"a":1}`), exec.Input)
	require.Len(t, exec.Steps, 3)
	for i, step := range exec.Steps {
		assert.Equal(t, i, step.Index)
	}
}

// fakeRows is a rowSource that produces joined rows without a database.
//
// On every Scan call it records which targets arrived nil (skipped): that is the
// only way to see that the execution columns are scanned once.
type fakeRows struct {
	t         *testing.T
	stepCount int
	position  int
	skipped   [][]bool
}

func (r *fakeRows) Next() bool {
	if r.position >= r.stepCount {
		return false
	}
	r.position++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	r.t.Helper()
	require.Len(r.t, dest, execColumnCount+8, "a joined row has 17 columns")

	skipped := make([]bool, len(dest))
	for i, target := range dest {
		skipped[i] = target == nil
	}
	r.skipped = append(r.skipped, skipped)

	instant := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	key := "ord_1"
	stepName := "step"
	stepStatus := "invoked"
	stepFailure := ""
	index := int32(r.position - 1)
	attempts := int32(1)

	scanInto(r.t, dest[0], "wfx_1")
	scanInto(r.t, dest[1], "order")
	scanInto(r.t, dest[2], &key)
	scanInto(r.t, dest[3], "running")
	scanInto(r.t, dest[4], []byte(`{"a":1}`))
	scanInto(r.t, dest[5], []byte(nil))
	scanInto(r.t, dest[6], "")
	scanInto(r.t, dest[7], instant)
	scanInto(r.t, dest[8], instant)
	scanInto(r.t, dest[9], &index)
	scanInto(r.t, dest[10], &stepName)
	scanInto(r.t, dest[11], &stepStatus)
	scanInto(r.t, dest[12], []byte(nil))
	scanInto(r.t, dest[13], &stepFailure)
	scanInto(r.t, dest[14], &attempts)
	scanInto(r.t, dest[15], (*time.Time)(nil))
	scanInto(r.t, dest[16], (*time.Time)(nil))
	return nil
}

// scanInto does what the driver does: if the target is nil it SKIPS the column,
// otherwise it writes the value (pgx.Rows.Scan: "nil will skip the value
// entirely").
func scanInto[T any](t *testing.T, target any, value T) {
	t.Helper()
	if target == nil {
		return
	}
	p, fits := target.(*T)
	require.Truef(t, fits, "unexpected scan target type: %T", target)
	*p = value
}

// TestMigrationsComeInUpDownPairs verifies that the embedded migrations come in
// up/down pairs (plan Section 8: reversible migrations).
func TestMigrationsComeInUpDownPairs(t *testing.T) {
	t.Parallel()

	entries, err := fs.ReadDir(Migrations(), ".")
	require.NoError(t, err)
	require.NotEmpty(t, entries, "at least one migration has to be embedded")

	ups := map[string]bool{}
	downs := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		content, readErr := fs.ReadFile(Migrations(), name)
		require.NoError(t, readErr)
		assert.NotEmpty(t, strings.TrimSpace(string(content)), "%s must not be empty", name)

		switch {
		case strings.HasSuffix(name, ".up.sql"):
			ups[strings.TrimSuffix(name, ".up.sql")] = true
		case strings.HasSuffix(name, ".down.sql"):
			downs[strings.TrimSuffix(name, ".down.sql")] = true
		default:
			t.Errorf("%s has to end with .up.sql or .down.sql", name)
		}
	}
	assert.Equal(t, ups, downs, "every up file has to have a down counterpart")
}

// TestMigrationsSchemaContract verifies that the schema meets the assumptions in
// the code: the error mapping rests on the index NAME and the idempotency
// guarantee on the index being PARTIAL. If either changes in the migration file
// this test fails.
func TestMigrationsSchemaContract(t *testing.T) {
	t.Parallel()

	up, err := fs.ReadFile(Migrations(), "000001_workflow_init.up.sql")
	require.NoError(t, err)
	sql := string(up)

	assert.Contains(t, sql, "CREATE TABLE workflow_executions")
	assert.Contains(t, sql, "CREATE TABLE workflow_execution_steps")
	assert.Contains(t, sql, "CREATE UNIQUE INDEX "+idempotencyIndex,
		"the error mapping rests on this index name")
	assert.Contains(t, sql, "WHERE idempotency_key IS NOT NULL",
		"the index has to be PARTIAL: keyless executions must not block each other")
	assert.Contains(t, sql, "PRIMARY KEY (execution_id, step_index)",
		"the steps have to be unique by (execution_id, step_index)")
	assert.Contains(t, sql, "JSONB", "the JSON fields have to be stored as JSONB")
	assert.Contains(t, sql, "TIMESTAMPTZ", "the time fields have to be TIMESTAMPTZ")

	down, err := fs.ReadFile(Migrations(), "000001_workflow_init.down.sql")
	require.NoError(t, err)
	assert.Contains(t, string(down), "DROP TABLE IF EXISTS workflow_execution_steps")
	assert.Contains(t, string(down), "DROP TABLE IF EXISTS workflow_executions")
}

// TestMigrationOwnerConstant pins the owner name in the version ledger.
func TestMigrationOwnerConstant(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "workflow", MigrationOwner)
}
