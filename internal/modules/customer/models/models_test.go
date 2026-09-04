package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// TestNormalizeEmail proves that e-mail normalization trims and folds to lower
// case.
func TestNormalizeEmail(t *testing.T) {
	cases := map[string]struct{ input, want string }{
		"upper case":         {"ALI@EXAMPLE.COM", "ali@example.com"},
		"mixed":              {"Ali.Veli@Example.Com", "ali.veli@example.com"},
		"whitespace":         {"  ali@example.com \t", "ali@example.com"},
		"already normalized": {"ali@example.com", "ali@example.com"},
		"empty":              {"   ", ""},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, models.NormalizeEmail(tc.input))
		})
	}
}

// TestNormalizeEmailIsIdempotent proves that a second application of the
// normalization does not change the value.
//
// Idempotence is a must: the same value is normalized on both the write and the
// read path, and two passes giving a different result would have meant the
// record cannot be found by its own e-mail.
func TestNormalizeEmailIsIdempotent(t *testing.T) {
	first := models.NormalizeEmail("  Ali@Example.COM ")
	second := models.NormalizeEmail(first)
	assert.Equal(t, first, second)
}

// TestNormalizeCountryCode proves that the country code is converted to UPPER
// case.
func TestNormalizeCountryCode(t *testing.T) {
	assert.Equal(t, "TR", models.NormalizeCountryCode(" tr "))
	assert.Equal(t, "DE", models.NormalizeCountryCode("De"))
	assert.Equal(t, "", models.NormalizeCountryCode("  "))
}

// TestIDFormat proves that the ids are prefixed and of fixed length.
func TestIDFormat(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	cases := map[string]struct {
		newID  func(time.Time) string
		prefix string
	}{
		"customer":         {models.NewCustomerID, models.CustomerIDPrefix},
		"group":            {models.NewCustomerGroupID, models.CustomerGroupIDPrefix},
		"customer address": {models.NewAddressID, models.AddressIDPrefix},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			id := tc.newID(now)
			assert.True(t, strings.HasPrefix(id, tc.prefix), "the %q prefix is expected: %q", id, tc.prefix)
			assert.Len(t, strings.TrimPrefix(id, tc.prefix), models.IDBodyLength())
		})
	}
}

// TestIDIsUnique proves that ids produced at the same moment do not collide.
func TestIDIsUnique(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := models.NewCustomerID(now)
		_, dup := seen[id]
		require.False(t, dup, "the id repeated: %s", id)
		seen[id] = struct{}{}
	}
}

// TestIDSortsByTime proves that the lexicographic order of the id preserves
// time order.
//
// Sortability is the only ground for getting natural creation order with
// "ORDER BY id"; had the timestamp not been at the START of the body, the order
// would be random.
func TestIDSortsByTime(t *testing.T) {
	earlier := models.NewCustomerID(time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC))
	later := models.NewCustomerID(time.Date(2026, 8, 23, 12, 0, 1, 0, time.UTC))
	assert.Less(t, earlier, later, "the following id has to be lexicographically greater")
}

// TestIsGuest proves that the guest/account distinction can be read from the
// model.
func TestIsGuest(t *testing.T) {
	assert.True(t, models.Customer{HasAccount: false}.IsGuest())
	assert.False(t, models.Customer{HasAccount: true}.IsGuest())
}

// TestDefaultKind proves the kind validation of the default address.
//
// The type is exported and a caller can construct a value outside the enum; if
// such a value silently fell through to shipping, the client would be changing
// the shipping address while believing it had marked the billing address.
func TestDefaultKind(t *testing.T) {
	assert.True(t, models.DefaultShipping.Valid())
	assert.True(t, models.DefaultBilling.Valid())
	assert.False(t, models.DefaultKind(42).Valid(), "an undefined value has to be invalid")

	assert.Equal(t, "shipping", models.DefaultShipping.String())
	assert.Equal(t, "billing", models.DefaultBilling.String())
}
