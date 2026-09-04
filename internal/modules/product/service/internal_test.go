package service

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// This file is INSIDE the package: the things verified here (slug generation,
// the id body, the paging clamp) are helpers that are not exported and can only
// be seen from here. The exported behavior is tested in the service_test
// package.
//
// The inputs that carry Turkish letters are written with \u escapes so that the
// file itself stays ASCII while the transliteration table (turkishASCII) is
// still exercised with the letters it actually maps.

// TestSlugify verifies the handle generation from free text.
func TestSlugify(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"turkish letters fold to ascii", "Ti\u015f\u00f6rt", "tisort"},
		{"repeated spaces collapse into one dash", "\u015e\u0131k Ti\u015f\u00f6rt  Mavi", "sik-tisort-mavi"},
		{"the dotted capital i keeps its letter", "\u0130stanbul Ceketi", "istanbul-ceketi"},
		{"surrounding whitespace is trimmed", "  bo\u015fluklu  ", "bosluklu"},
		{"uppercase folds to lowercase", "\u00c7OK-G\u00dcZEL", "cok-guzel"},
		{"punctuation becomes a single dash", "a__b..c//d", "a-b-c-d"},
		{"leading and trailing dashes are dropped", "---edge---", "edge"},
		{"symbols are dropped", "\u00d6zel #1 (mavi)", "ozel-1-mavi"},
		{"every mapped letter is folded", "\u011f\u00fc\u015fi\u00f6\u00e7", "gusioc"},
		{"empty input stays empty", "", ""},
		{"only symbols produce an empty slug", "%%%", ""},
		{"digits survive", "Summer 2026 Collection", "summer-2026-collection"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, slugify(tc.in))
		})
	}
}

// TestSlugifyIsIdempotent verifies that the generated slug does not change when
// it is slugified again.
//
// This property is the foundation of validateHandle: handle validation is done
// with the comparison "slugify(h) == h", so every generated handle has to pass
// the validation.
func TestSlugifyIsIdempotent(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"Ti\u015f\u00f6rt",
		"\u0130stanbul Ceketi",
		"\u00d6zel #1 (mavi)",
		"a__b..c//d",
		"\u00c7OK-G\u00dcZEL",
	}
	for _, input := range inputs {
		once := slugify(input)
		assert.Equal(t, once, slugify(once), "the slug of %q should be stable", input)
		if once != "" {
			_, err := validateHandle(once)
			assert.NoError(t, err, "a generated handle should pass validation: %q", once)
		}
	}
}

// TestValidateHandleRejectsBadShapes verifies that the handle shape is enforced.
func TestValidateHandleRejectsBadShapes(t *testing.T) {
	t.Parallel()

	bad := []string{"B\u00fcy\u00fck", "with space", "-lead", "trail-", "two--dashes", "ti\u015f\u00f6rt", "UPPER"}
	for _, handle := range bad {
		_, err := validateHandle(handle)
		assert.Error(t, err, "%q should have been rejected", handle)
		assert.True(t, errors.IsInvalid(err), "a validation error was expected for %q", handle)
	}

	good := []string{"shirt", "shirt-blue", "product-1", "a"}
	for _, handle := range good {
		got, err := validateHandle(handle)
		require.NoError(t, err, "%q should have been accepted", handle)
		assert.Equal(t, handle, got)
	}
}

// TestValidateHandleLengthLimit verifies that an overly long handle is rejected.
func TestValidateHandleLengthLimit(t *testing.T) {
	t.Parallel()

	_, err := validateHandle(strings.Repeat("a", maxHandleLen+1))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestRequireIDRejectsWhitespace verifies that ids are NOT TRIMMED and that an
// id carrying whitespace is rejected.
//
// A silent trim separates the id that was sent from the id that is stored; the
// difference only becomes visible after the data is corrupted (see the id
// contract of core/link).
func TestRequireIDRejectsWhitespace(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", " ", "prod_1\n", " prod_1", "prod_1 "} {
		_, err := requireID("id", id)
		assert.Error(t, err, "%q should have been rejected", id)
	}

	got, err := requireID("id", "prod_1")
	require.NoError(t, err)
	assert.Equal(t, "prod_1", got)
}

// TestNormalizePaging verifies the paging rules.
func TestNormalizePaging(t *testing.T) {
	t.Parallel()

	limit, offset, err := normalizePaging(0, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, limit)
	assert.Zero(t, offset)

	limit, _, err = normalizePaging(MaxLimit+1, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, limit, "a limit above the ceiling should be clamped")

	limit, offset, err = normalizePaging(5, 10)
	require.NoError(t, err)
	assert.Equal(t, 5, limit)
	assert.Equal(t, 10, offset)

	_, _, err = normalizePaging(-1, 0)
	assert.Error(t, err)
	_, _, err = normalizePaging(0, -1)
	assert.Error(t, err)
}

// TestNewIDShape verifies that the generated id carries the prefix + 26
// character body shape.
func TestNewIDShape(t *testing.T) {
	t.Parallel()

	id := newID(prefixProduct)
	assert.True(t, strings.HasPrefix(id, prefixProduct), "it should carry the prefix: %s", id)

	body := strings.TrimPrefix(id, prefixProduct)
	assert.Len(t, body, 26, "the body should be the Crockford Base32 form of 16 bytes: %s", body)
	assert.Equal(t, strings.ToUpper(body), body, "the alphabet consists of capitals and digits: %s", body)
	for _, r := range body {
		assert.NotContains(t, "ILOU", string(r),
			"the Crockford alphabet does not contain the confusable letters (I, L, O, U): %s", body)
	}
}

// TestNewIDIsUnique verifies that ids generated within the same millisecond do
// not collide.
func TestNewIDIsUnique(t *testing.T) {
	t.Parallel()

	const count = 2000
	seen := make(map[string]struct{}, count)
	for range count {
		id := newID(prefixVariant)
		_, dup := seen[id]
		require.False(t, dup, "the id repeated: %s", id)
		seen[id] = struct{}{}
	}
}

// TestIDBytesAreTimeOrdered verifies that the id is time-ordered.
//
// Sortability is a requirement of Section 8 of the plan: the id carries roughly
// the creation order, so the records sit in their natural order in the primary
// key index too.
func TestIDBytesAreTimeOrdered(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	earlier := idEncoding.EncodeToString(idBytes(base))
	later := idEncoding.EncodeToString(idBytes(base.Add(time.Millisecond)))
	muchLater := idEncoding.EncodeToString(idBytes(base.Add(72 * time.Hour)))

	assert.Less(t, earlier, later, "an id 1 ms later should come later lexicographically")
	assert.Less(t, later, muchLater, "an id 3 days later should come later lexicographically")
}

// TestIDBytesClampsPreEpoch verifies that a time before 1970 does not break the
// ordering.
func TestIDBytesClampsPreEpoch(t *testing.T) {
	t.Parallel()

	old := idEncoding.EncodeToString(idBytes(time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC)))
	now := idEncoding.EncodeToString(idBytes(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	assert.Less(t, old, now, "a clamped timestamp should not break the ordering")
}

// TestInt32From verifies that the narrowing does not flip the sign.
func TestInt32From(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int32(0), int32From(-5), "a negative value should be pulled to zero")
	assert.Equal(t, int32(7), int32From(7))
	assert.Equal(t, int32(2147483647), int32From(1<<40), "a value above the ceiling should be clamped to it")
}

// TestTrimOptionalEmptyBecomesNil verifies that an optional field that became
// empty is nil.
func TestTrimOptionalEmptyBecomesNil(t *testing.T) {
	t.Parallel()

	value := "   "
	got, err := trimOptional(&value, "subtitle", 10)
	require.NoError(t, err)
	assert.Nil(t, got)

	value = "  sub title "
	got, err = trimOptional(&value, "subtitle", 100)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "sub title", *got)

	long := strings.Repeat("a", 11)
	_, err = trimOptional(&long, "subtitle", 10)
	assert.Error(t, err)
}

// TestUniqueIDsPreservesOrder verifies that deduplication preserves the order.
func TestUniqueIDsPreservesOrder(t *testing.T) {
	t.Parallel()

	got, err := uniqueIDs("tag_ids", []string{"ptag_2", "ptag_1", "ptag_2"})
	require.NoError(t, err)
	assert.Equal(t, []string{"ptag_2", "ptag_1"}, got)
}
