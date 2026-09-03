package openapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ClashingRecord is one of the TWO types of the name-clash test; the other is
// defined under the SAME name by the external test package (openapi_test).
//
// The two types being in two SEPARATE packages is required: two types with the
// same name in one package do not compile, so the clash can only arise between
// packages and cannot be produced by a single test file. That is why the
// internal test file exists — for the compiler it is part of the openapi
// package and its name clashes with openapi_test's, but it never enters the
// production binary.
type ClashingRecord struct {
	Field string `json:"field"`
}

// TestComponentNameDoesNotLeakGoDetails verifies that the published component
// names do not depend on Go's export rule or on a package's naming habits.
//
// A component name is NOT an internal detail but a published contract: client
// generators produce class names from it and changing the name after a client
// has been generated is breaking. Without the normalization, "StoreProduct"
// (exported) would stand next to "cartDTO" (unexported) in one document and the
// generated client would carry two different naming schemes.
func TestComponentNameDoesNotLeakGoDetails(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		goName string
		want   string
	}{
		"an unexported name is upper-cased": {goName: "cartDTO", want: "Cart"},
		"the DTO suffix is dropped":         {goName: "addressDTO", want: "Address"},
		"an exported name is kept":          {goName: "StoreProduct", want: "StoreProduct"},
		"request types stay meaningful":     {goName: "createCartRequest", want: "CreateCartRequest"},
		"a name that is only DTO survives":  {goName: "DTO", want: "DTO"},
		"an empty name stays empty":         {goName: "", want: ""},
	}

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, componentName(tt.goName))
		})
	}
}
