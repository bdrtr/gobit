package pgstore

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bdrtr/gobit/core/errors"
)

// Field length ceilings. The id and the idempotency key are indexed columns;
// left unbounded, a very long value would hit the B-tree's row size limit and
// come back as an unintelligible driver error. The bound is applied here, where
// it can still be turned into a typed error.
const (
	maxIDLen   = 128
	maxNameLen = 128
	maxKeyLen  = 256
)

// nulEscape is the escape sequence that writes a NUL character in JSON text: a
// backslash followed by u0000. It is written escaped in the source; its value is
// six characters.
const nulEscape = "\\u0000"

// jsonParam turns a JSON field into a query parameter.
//
// The distinction is deliberate and is kept in both directions:
//
//   - a nil json.RawMessage → SQL NULL   ("no value")
//   - the bytes "null"      → JSONB null (JSON's own null VALUE)
//
// A value that is not nil but has zero length is not valid JSON; NULL is
// written.
//
// The criterion is what JSONB accepts, NOT what Go accepts: a body that passes
// json.Valid can still blow up in the database. Hence three checks — syntax,
// UTF-8 validity and the NUL escape. Without the last two the failure would come
// back from the driver as an unclassified (KindInternal) error, when the fault
// is in the caller's data and belongs to Invalid.
//
// The return type is any because nil itself — not a typed []byte nil — has to
// reach pgx as NULL.
func jsonParam(raw json.RawMessage, field string) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, errors.Invalid(CodeInvalid, "the %s field is not valid JSON", field)
	}
	if !utf8.Valid(raw) {
		return nil, errors.Invalid(CodeInvalid,
			"the %s field is not valid UTF-8; JSONB cannot store it", field)
	}
	if hasNULEscape(raw) {
		return nil, errors.Invalid(CodeInvalid,
			"the %s field contains a %s escape; JSONB cannot turn it into text", field, nulEscape)
	}

	return []byte(raw), nil
}

// hasNULEscape reports whether the JSON text holds a REAL NUL escape.
//
// PostgreSQL's jsonb rejects that escape (SQLSTATE 22P05): a jsonb value has to
// be convertible to text, and NUL cannot appear in text.
//
// A plain search is not enough: a u0000 following an escaped backslash is NOT an
// escape, it is six ordinary characters. The scanner therefore tracks whether it
// is inside a string and skips the next character at every escape. It is only
// called for a body that has already passed json.Valid.
func hasNULEscape(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		if !inString {
			if raw[i] == '"' {
				inString = true
			}

			continue
		}
		switch raw[i] {
		case '"':
			inString = false
		case '\\':
			// A JSON escape is written only with a lowercase "u" and the zero
			// digits have no case, so an exact match is enough.
			if bytes.HasPrefix(raw[i+1:], []byte(nulEscape[1:])) {
				return true
			}
			i++
		}
	}

	return false
}

// jsonValue turns the JSONB bytes that were read into a json.RawMessage.
//
// A NULL column arrives as a nil byte slice and becomes a nil RawMessage, so
// "no value" stays separate from a JSON null on the read side exactly as it does
// on the write side.
//
// Note: JSONB normalizes the text (key order and whitespace are not preserved).
// The value read back means the same thing; it may not be byte for byte the same.
func jsonValue(raw []byte) json.RawMessage {
	if raw == nil {
		return nil
	}

	return json.RawMessage(raw)
}

// keyParam turns the idempotency key into a query parameter.
//
// THE EMPTY STRING means "no key" and NULL is written: were the empty string
// stored, two keyless executions would clash with each other in the partial
// unique index.
//
// A key made up of WHITESPACE ONLY is rejected. The caller DID give a key;
// turning it into NULL in silence would remove the repeat protection they asked
// for without a word of warning — a second execution opened with the same key
// would not clash and the work (a capture, say) would be done twice. The read
// path (see [store.FindByIdempotencyKey]) rejects the same key; the two paths
// accept exactly the same set.
//
// A non-empty key is stored AS IT IS — the key is an opaque value from outside,
// and trimming it could make two different keys identical.
func keyParam(key string) (*string, error) {
	if key == "" {
		return nil, nil
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.Invalid(CodeInvalid,
			"an idempotency key cannot be whitespace only")
	}
	if len(key) > maxKeyLen {
		return nil, errors.Invalid(CodeInvalid,
			"an idempotency key can be at most %d bytes, %d bytes were given",
			maxKeyLen, len(key))
	}
	if !isStorableText(key) {
		return nil, errors.Invalid(CodeInvalid,
			"an idempotency key cannot contain a NUL byte or an invalid UTF-8 sequence")
	}

	return &key, nil
}

// keyValue turns a NULL key into the empty string.
func keyValue(key *string) string {
	if key == nil {
		return ""
	}

	return *key
}

// timeParam turns the zero time into SQL NULL and everything else into UTC
// (plan Section 8).
func timeParam(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.UTC()

	return &utc
}

// timeValue turns a NULL time into the zero time.Time and moves the rest to UTC.
func timeValue(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}

	return t.UTC()
}

// requireText validates a mandatory text field and returns it trimmed.
//
// A value holding a NUL byte or an invalid UTF-8 sequence is rejected:
// PostgreSQL cannot write those into a TEXT column (SQLSTATE 22021) and the
// error coming back from the driver would be unclassified. The fields here are
// IDENTIFYING ones — id, name, status — and rejecting them is right; repairing
// them by trimming is not.
func requireText(value, field string, maxLen int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.Invalid(CodeInvalid, "%s cannot be empty", field)
	}
	if len(trimmed) > maxLen {
		return "", errors.Invalid(CodeInvalid,
			"%s can be at most %d bytes, %d bytes were given", field, maxLen, len(trimmed))
	}
	if !isStorableText(trimmed) {
		return "", errors.Invalid(CodeInvalid,
			"%s cannot contain a NUL byte or an invalid UTF-8 sequence", field)
	}

	return trimmed, nil
}

// isStorableText reports whether the text can be written into a TEXT column.
//
// Two things cannot be stored in a UTF8 PostgreSQL database: a NUL byte and an
// invalid UTF-8 sequence. Go strings can carry both, so the check is done on the
// application side.
func isStorableText(v string) bool {
	return !strings.ContainsRune(v, 0) && utf8.ValidString(v)
}

// safeText makes free diagnostic text writable into a TEXT column.
//
// NUL bytes and invalid UTF-8 sequences are DROPPED. A failure description is
// diagnostic text meant for a human and is not worth as much as the record's
// STATUS: refusing to write it would mean never recording the execution's
// terminal state and leaving the record "running" forever — a record that could
// then be neither completed nor have its idempotency key reused.
func safeText(v string) string {
	if isStorableText(v) {
		return v
	}

	return strings.ReplaceAll(strings.ToValidUTF8(v, ""), "\x00", "")
}

// requireCount validates fields that cannot be negative, such as counters and
// indexes.
//
// The ceiling is int32: the columns are INTEGER, so a larger value would
// overflow in the database; the bound is turned into a typed error here.
func requireCount(value int, field string) (int32, error) {
	if value < 0 {
		return 0, errors.Invalid(CodeInvalid, "%s cannot be negative, %d was given", field, value)
	}
	if value > math.MaxInt32 {
		return 0, errors.Invalid(CodeInvalid,
			"%s can be at most %d, %d was given", field, math.MaxInt32, value)
	}

	return int32(value), nil
}

// createError turns the raw driver error from opening an execution into a typed
// one.
//
// Which clash it was is read from the name of the VIOLATED constraint: the
// idempotency index and the primary key are different failures and the caller
// has to be able to tell them apart. Catching the violation rather than doing
// "SELECT first, then INSERT" GUARANTEES that only one of two processes opening
// a record at the same instant succeeds.
//
// THE CLASS ASSIGNMENT is part of the contract, because the engine branches on
// the class:
//
//   - the idempotency index → KindConflict. It is the ONLY violation that says
//     "this request was made before"; on a Conflict the engine takes the replay
//     path.
//   - the primary key → KindInvalid. The id the caller supplied is already in
//     use; that is an input error, not a repeat request. Returning Conflict
//     would have the engine confuse it with an idempotency clash, look for a key
//     it is not holding, and hand the caller an unrelated "the execution could
//     not be read".
//   - an unrecognized constraint → KindInternal. The schema has drifted from the
//     assumption in the code; saying Conflict without knowing what happened
//     would send the engine down the wrong path.
func createError(err error, id, name, key string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		details := map[string]any{
			keyExecutionID: id,
			keyWorkflow:    name,
		}
		switch pgErr.ConstraintName {
		case idempotencyIndex:
			return errors.Wrap(err, errors.KindConflict, CodeDuplicateKey,
				"an execution with the idempotency key %q is already open in the %s workflow",
				key, name).WithDetails(details)
		case executionsPKConstraint:
			return errors.Wrap(err, errors.KindInvalid, CodeDuplicateID,
				"an execution with the id %q already exists", id).WithDetails(details)
		default:
			return errors.Wrap(err, errors.KindInternal, CodeConflict,
				"the execution record could not be opened: the %s constraint was violated",
				pgErr.ConstraintName).WithDetails(details)
		}
	}

	return wrapDB(err, CodeQueryFailed, "the execution record for the %s workflow could not be opened", name)
}

// wrapDB turns a raw driver error into a typed one.
//
// Cancellation and timeout are KindUnavailable (a budget running out, not a
// failure); known schema violations and encoding errors arising from the
// caller's data go to their own classes; everything else is wrapped as
// KindInternal. The raw error stays in the chain and is reachable with
// errors.Is/As.
func wrapDB(err error, code, format string, a ...any) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.Wrap(err, errors.KindUnavailable, CodeCanceled,
			format+" (the context was canceled)", a...)
	case errors.Is(err, pgx.ErrNoRows):
		return errors.Wrap(err, errors.KindNotFound, CodeNotFound, format, a...)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case foreignKeyViolation:
			// The only foreign key in the schema binds a step to an execution,
			// so violating it means "there is no such execution".
			return errors.Wrap(err, errors.KindNotFound, CodeNotFound,
				format+" (the execution record it belongs to does not exist)", a...)
		case checkViolation:
			// A new slice is built so as not to write into a's own array; the
			// arguments the caller gave must not change.
			args := make([]any, 0, len(a)+1)
			args = append(args, a...)
			args = append(args, pgErr.ConstraintName)

			return errors.Wrap(err, errors.KindInvalid, CodeInvalid,
				format+" (the %s constraint was violated)", args...)
		case notInRepertoire, untranslatableCharacter, invalidTextRepresentation:
			// The value could not be converted to the column's type: a NUL byte
			// in text, a NUL escape in JSON, a broken UTF-8 sequence. Input
			// validation already rules these out; this branch is the defense in
			// depth that keeps a shape which escaped that validation from coming
			// back as a 500 (plan Section 8: an error in the caller's data is
			// Invalid). The driver's message is NOT added, because it can carry
			// the caller's data.
			return errors.Wrap(err, errors.KindInvalid, CodeInvalid,
				format+" (the value could not be converted to the column's type)", a...)
		}
	}

	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}
